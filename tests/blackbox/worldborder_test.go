//go:build blackbox

package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestWorldBorderParity pins /worldborder against the real server:
// get/center/set/add/damage/warning replies, the border packets each
// change broadcasts, and the nochange/parse/low-value failures with
// their cursors.
func TestWorldBorderParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`tp @s 8.5 -60 8.5`,
		`worldborder get`,
		`worldborder`,
		`worldborder center 10 20`,
		`worldborder center`,
		`worldborder center x z`,
		`worldborder center ~ ~`,
		`worldborder set 600`,
		`worldborder set`,
		`worldborder set abc`,
		`worldborder set 600 5`,
		`worldborder set 1`,
		`worldborder set 40 5`,
		`worldborder add 100`,
		`worldborder add`,
		`worldborder add -50`,
		`worldborder add 2 5`,
		`worldborder damage amount 2`,
		`worldborder damage amount`,
		`worldborder damage amount x`,
		`worldborder damage buffer 3`,
		`worldborder damage buffer -1`,
		`worldborder damage buffer`,
		`worldborder damage`,
		`worldborder damage bogus 1`,
		`worldborder warning time 15`,
		`worldborder warning time`,
		`worldborder warning time x`,
		`worldborder warning distance 40`,
		`worldborder warning distance -2`,
		`worldborder warning bogus`,
		`worldborder bogus`,
		`worldborder set 600 extra`,
		`worldborder set 99999999`,
		`worldborder warning time -1`,
		`worldborder warning distance x`,
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		var out [][]string
		for _, cmd := range cmds {
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
					continue
				}
				i := strings.Index(line, ":")
				if i < 0 {
					continue
				}
				id, err := strconv.ParseInt(line[:i], 0, 32)
				if err != nil {
					continue
				}
				// Border packets only: center, size, lerp, warnings.
				if id == 0x58 || id == 0x59 || id == 0x5a || id == 0x5b || id == 0x5c {
					evs = append(evs, "pkt "+maskUUID(line))
				}
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
