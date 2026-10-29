//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// A stationary player negotiates pose against the actual collision shape,
// including roofs whose collision starts above the bottom of their cell.
func TestPlayerStationaryPoseFitParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("PoseFit")}, nil)
	defer tg.cleanup()
	run := func(addr string) []int32 {
		c, pr, pw := joinPlayBlackbox(t, addr, "PoseFit")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		wait := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				batch := sink.take()
				lines = append(lines, batch...)
				for _, line := range batch {
					if strings.Contains(line, marker) {
						return lines
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s marker %s missing", addr, marker)
			return nil
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "fill -2 159 -2 2 159 2 stone")
		sendCommand(t, pw, "fill -2 160 -2 2 163 2 air")
		sendCommand(t, pw, "tp @s 0.5 160 0.5")
		sendCommand(t, pw, "say POSEINITIALIZED")
		wait("POSEINITIALIZED")
		time.Sleep(time.Second)
		sink.take()
		var out []int32
		previousPose := int32(0)
		for i, tc := range []struct {
			block string
			pose  int32
		}{
			{"stone", 3}, {"air", 0},
			{"stone_slab[type=top]", 5}, {"air", 0},
			{"stone_slab[type=bottom]", 3}, {"air", 0},
			{"oak_trapdoor[half=top,open=false]", 0},
			{"oak_trapdoor[half=bottom,open=false]", 3}, {"air", 0},
		} {
			sink.take()
			sendCommand(t, pw, "setblock 0 161 0 "+tc.block)
			marker := fmt.Sprintf("POSEROOF%dEND", i)
			sendCommand(t, pw, "say "+marker)
			lines := wait(marker)
			got := int32(-1)
			unchanged := tc.block == "oak_trapdoor[half=top,open=false]" && previousPose == 0
			if unchanged {
				got = previousPose
				time.Sleep(400 * time.Millisecond)
				lines = append(lines, sink.take()...)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				for _, line := range lines {
					prefix := fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)
					if !strings.HasPrefix(line, prefix) {
						continue
					}
					_, entries := fwDecodeMeta([]byte(strings.TrimPrefix(line, prefix)))
					for _, e := range entries {
						if e.idx == 6 && e.typ == 20 {
							got = int32(e.i)
							if unchanged && got != 0 {
								t.Errorf("%s top trapdoor changed standing pose to %d", addr, got)
							}
						}
					}
				}
				if got == tc.pose || time.Now().After(deadline) {
					break
				}
				time.Sleep(20 * time.Millisecond)
				lines = sink.take()
			}
			t.Logf("%s roof=%s pose=%d", addr, tc.block, got)
			out = append(out, got)
			previousPose = got
			if got != tc.pose {
				t.Errorf("%s roof=%s pose=%d want %d", addr, tc.block, got, tc.pose)
			}
		}
		return out
	}
	v, m := run(tg.vanilla), run(tg.mcgo)
	if fmt.Sprint(v) != fmt.Sprint(m) {
		t.Fatalf("vanilla %v mcgo %v", v, m)
	}
}
