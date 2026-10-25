package blackbox

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestXpParity replays the /experience surface against both servers and
// requires identical replies plus identical Set Experience packets:
// add|set in both units (bare amounts default to points), negative and
// saturating adds, set-points-above-level rejection, multi-target
// replies, the single-player query gate, and XpTotal reads that pin
// the level-reset zeroing. The player's progress/level/total are three
// independent fields on the real server, so the legs walk states the
// total-only model cannot express.
func TestXpParity(t *testing.T) {
	const probe = "XpV"
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(probe),
	}, nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runXpScript(t, tg.vanilla, probe)
	got := runXpScript(t, tg.mcgo, probe)
	for i := range xpScript {
		if want[i] == got[i] {
			continue
		}
		t.Errorf("%q: mc-go and vanilla disagree\n  vanilla: %s\n  mc-go:   %s",
			xpScript[i], want[i], got[i])
	}
}

// xpScript is the command list, in order. The state legs assume the
// previous replies landed, so the order is load-bearing.
var xpScript = []string{
	"experience add @s 5 levels",
	"experience query @s levels",
	"experience add @s 7",
	"experience query @s points",
	"data get entity @s XpTotal",
	"experience set @s 20 points",
	"experience set @s 16 points",
	"experience query @s points",
	"experience add @s 1 points",
	"experience query @s levels",
	"experience set @s 30 levels",
	"experience add @s -3 points",
	"experience query @s levels",
	"experience add @s -400 points",
	"experience query @s levels",
	"data get entity @s XpTotal",
	"experience add @s 5 points",
	"experience add @s 3 points",
	"experience query @s levels",
	"experience query @s points",
	"data get entity @s XpLevel",
	"xp add @s 2 levels",
	"experience add @a 2 levels",
	"experience set @a 4 points",
	"experience set @a 200 points",
	"experience query @a levels",
	"experience set @s -1 levels",
	"experience set @s x points",
	"experience add @s 5x points",
	"experience add @s 9999999999 points",
	"experience add nobody 5",
	"experience add @s 5 bogus",
	"experience bogus @s 1",
	"experience query @s",
	"experience add",
	"experience",
	"xp",
}

func runXpScript(t *testing.T, addr, name string) []string {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	c2, pr2, pw2 := joinPlayBlackbox(t, addr, "XpOther")
	defer c2.Close()
	sink := newChatSink(pr, pw)
	_ = newChatSink(pr2, pw2)
	sink.cap = 1 << 20
	time.Sleep(2 * time.Second)
	sink.take()

	out := make([]string, 0, len(xpScript))
	for _, cmd := range xpScript {
		sink.take()
		sendCommand(t, pw, cmd)
		var msgs []string
		xpLast := ""
		xpCount := 0
		deadline := time.Now().Add(4 * time.Second)
		quiet := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if s, ok := xpPacketLine(line); ok {
					xpLast, xpCount = s, xpCount+1
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					msgs = append(msgs, tree)
					quiet = time.Now().Add(300 * time.Millisecond)
				}
			}
			// A Set Experience lands a tick after the reply it belongs
			// to — inside the quiet window, so it folds into the same
			// leg.
			if len(msgs) > 0 && time.Now().After(quiet) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if xpCount > 0 {
			msgs = append(msgs, fmt.Sprintf("xp:%s(x%d)", xpLast, xpCount))
		}
		if len(msgs) == 0 {
			out = append(out, "<no reply>")
			continue
		}
		out = append(out, strings.Join(msgs, " "))
	}
	return out
}

// xpPacketLine decodes a buffered Set Experience packet to a compare
// string: bar bits, level, total.
func xpPacketLine(line string) (string, bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", false
	}
	id, err := strconv.ParseInt(line[:i], 0, 32)
	if err != nil || int32(id) != server.IDPlaySetExperience {
		return "", false
	}
	r := protocol.NewPacketReader([]byte(line[i+1:]))
	bar, err1 := r.Float()
	lv, err2 := r.VarInt()
	tot, err3 := r.VarInt()
	if err1 != nil || err2 != nil || err3 != nil {
		return "bad-xp", true
	}
	return fmt.Sprintf("f%08x/l%d/t%d", math.Float32bits(bar), lv, tot), true
}
