//go:build blackbox

package blackbox

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

// TestCloneParity covers /clone's source forms (begin/end and
// from/to), masks (replace/masked/filtered), modes (normal/force/
// move), and the real world result: destination cells get the source
// blocks, masked skips air, filtered keeps only the predicate, move
// erases the source.
func TestCloneParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		// Lay down a 3x3 stone platform at 0..2,-60,0..2 with a dirt
		// center, and a gold-block column the masked/filtered clones
		// should leave standing.
		`fill 0 -60 0 2 -60 2 minecraft:stone`,
		`setblock 1 -60 1 minecraft:dirt`,
		`fill 30 -60 0 34 -60 2 minecraft:gold_block`,
		`setblock 44 -60 0 minecraft:gold_block`,
		// Parse skeleton.
		`clone`,
		`clone 0 -60`,
		`clone x -60 0`,
		`clone 0 -60 0 2 -60 2`,
		// Plain replace clone + result probes.
		`clone 0 -60 0 2 -60 2 8 -60 0`,
		`execute if block 9 -60 1 minecraft:dirt run say ZCL-dst-dirt`,
		`execute unless block 9 -60 1 minecraft:dirt run say ZCL-dst-miss`,
		`execute if block 8 -60 0 minecraft:stone run say ZCL-dst-stone`,
		`execute unless block 8 -60 0 minecraft:stone run say ZCL-dst-stone-miss`,
		// masked: air cells never overwrite the destination.
		`clone 3 -60 0 3 -60 2 30 -60 0 masked`,
		`execute if block 30 -60 1 minecraft:gold_block run say ZCL-masked-kept`,
		`execute unless block 30 -60 1 minecraft:gold_block run say ZCL-masked-wrote`,
		// replace writes air.
		`clone 3 -60 0 3 -60 2 31 -60 0 replace`,
		`execute if block 31 -60 1 minecraft:air run say ZCL-replace-air`,
		`execute unless block 31 -60 1 minecraft:air run say ZCL-replace-noair`,
		// filtered copies only matching cells.
		`clone 0 -60 0 2 -60 2 32 -60 0 filtered minecraft:dirt`,
		`execute if block 33 -60 1 minecraft:dirt run say ZCL-filter-dirt`,
		`execute unless block 33 -60 1 minecraft:dirt run say ZCL-filter-dirt-miss`,
		`execute if block 32 -60 0 minecraft:air run say ZCL-filter-stone`,
		`execute unless block 32 -60 0 minecraft:air run say ZCL-filter-stone-kept`,
		// filtered move erases the copied source cells.
		`clone 0 -60 0 2 -60 2 36 -60 0 filtered minecraft:dirt move`,
		`execute if block 1 -60 1 minecraft:air run say ZCL-move-src`,
		`execute unless block 1 -60 1 minecraft:air run say ZCL-move-src-kept`,
		// Overlap rules: modes only parse after a mask literal.
		`clone 0 -60 0 4 -60 0 2 -60 0`,
		`clone 0 -60 0 4 -60 0 2 -60 0 replace normal`,
		`clone 0 -60 0 4 -60 0 2 -60 0 replace move`,
		`clone 0 -60 0 4 -60 0 2 -60 0 replace force`,
		`clone 0 -60 0 4 -60 0 2 -60 0 masked force`,
		// All-air source.
		`clone 60 -59 0 62 -59 0 64 -59 0 replace`,
		`clone 60 -59 0 62 -59 0 64 -59 0 masked`,
		// Bad mask/mode and bad filter.
		`clone 0 -60 0 2 -60 2 8 -60 0 bogus`,
		`clone 0 -60 0 2 -60 2 8 -60 0 masked bogus`,
		`clone 0 -60 0 2 -60 2 8 -60 0 normal masked`,
		`clone 0 -60 0 2 -60 2 8 -60 0 filtered minecraft:bogus`,
		`clone 0 -60 0 2 -60 2 8 -60 0 filtered minecraft:stone[snowy=true]`,
		`clone 0 -60 0 2 -60 2 8 -60 0 filtered`,
		// Unloaded positions.
		`clone 0 -60 0 2 -60 2 2000 -60 0`,
		`clone 2000 -60 0 2002 -60 0 8 -60 0`,
		// from/to form.
		`clone from`,
		`clone from minecraft:overworld`,
		`clone from minecraft:overworld 0 -60 0 2 -60 2`,
		`clone from minecraft:overworld 0 -60 0 2 -60 2 8 -60 0`,
		`clone from minecraft:overworld 0 -60 0 2 -60 2 to minecraft:overworld 8 -60 0`,
		`clone from minecraft:bogusdim 0 -60 0 2 -60 2 to minecraft:overworld 8 -60 0`,
		`clone from minecraft:overworld 0 -60 0 2 -60 2 to minecraft:overworld 8 -60 0 masked move`,
		// strict sits before the mask literal.
		`clone 0 -60 0 2 -60 2 8 -60 0 strict`,
		`clone 0 -60 0 2 -60 2 8 -60 0 masked strict`,
		`clone 0 -60 0 2 -60 2 8 -60 0 strict masked`,
		`clone 0 -60 0 2 -60 2 8 -60 0 strict filtered minecraft:dirt`,
		`clone 0 -60 0 2 -60 2 8 -60 0 strict masked move`,
		`clone 0 -60 0 2 -60 2 8 -60 0 filtered #minecraft:logs`,
	}

	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		// Advertise the harness view distance so the server-side loaded
		// set covers the probe coordinates the way vanilla's 10-chunk
		// tracking does.
		sendClientInformation(t, pw)
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		// Let the initial chunk stream drain: replies queued behind it
		// otherwise slide into the next command's capture window.
		time.Sleep(4 * time.Second)
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			t.Logf("send %d %q", i, cmd)
			func() { defer func() { recover() }(); sendCommand(t, pw, cmd) }()
			// A say marker right after each probe makes the window
			// self-synchronizing: the reply set is everything captured
			// before the marker's own event, however long the probe's
			// reply took to queue.
			marker := fmt.Sprintf("ZMK%d", i)
			func() { defer func() { recover() }(); sendCommand(t, pw, "say "+marker) }()
			var evs []string
			deadline := time.Now().Add(3 * time.Second)
			for {
				gotMarker := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						gotMarker = true
						continue
					}
					if tree, ok := componentTree(line, true); ok {
						evs = append(evs, "chat "+numRe.ReplaceAllString(maskUUID(tree), "N"))
					} else if strings.HasPrefix(line, "0x79:") {
						i := strings.Index(line, ":")
						if root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:])); err == nil {
							evs = append(evs, "chat "+numRe.ReplaceAllString(maskUUID(renderNBT(root, true)), "N"))
						}
					}
				}
				if gotMarker || time.Now().After(deadline) {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if len(want[i]) != len(got[i]) {
			t.Errorf("cmd %q: vanilla %v\n  mc-go %v", cmd, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("cmd %q event %d:\n  vanilla %s\n  mc-go   %s", cmd, j, want[i][j], got[i][j])
			}
		}
	}
}
