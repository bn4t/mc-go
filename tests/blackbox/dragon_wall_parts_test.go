//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// A dragon head blocked by an immune block still checks its neck and body
// for destructible blocks. HOVERING keeps the geometry independent of flight.
func TestDragonWallPartsParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("WallDragon")}, nil)
	defer tg.cleanup()
	run := func(addr string) []bool {
		c, pr, pw := joinPlayBlackbox(t, addr, "WallDragon")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		wait := func(marker string) []string {
			var out []string
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				lines := sink.take()
				out = append(out, lines...)
				for _, line := range lines {
					if strings.Contains(line, marker) {
						return out
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s missing %s", addr, marker)
			return nil
		}
		barrier := func(marker string) { sendCommand(t, pw, "say "+marker); wait(marker) }
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 0.5 105 12.5")
		sendCommand(t, pw, "tick freeze")
		barrier("WALLREADY")
		var out []bool
		for i, tc := range []struct {
			head  string
			grief bool
		}{{"bedrock", true}, {"air", true}, {"bedrock", false}} {
			sendCommand(t, pw, "kill @e[type=minecraft:ender_dragon]")
			sendCommand(t, pw, "fill -3 99 -8 3 104 3 air")
			sendCommand(t, pw, "setblock 0 99 0 bedrock")
			sendCommand(t, pw, "setblock 0 100 -6 "+tc.head)
			sendCommand(t, pw, "setblock 0 100 -4 stone")
			sendCommand(t, pw, "setblock 0 100 0 dragon_egg")
			sendCommand(t, pw, fmt.Sprintf("gamerule minecraft:mob_griefing %t", tc.grief))
			sendCommand(t, pw, "summon minecraft:ender_dragon 0.5 100 0.5 {DragonPhase:10,Rotation:[0f,0f]}")
			barrier(fmt.Sprintf("WALLSETUP%d", i))
			sprintTicks(t, pw, sink, 1)
			for j, pos := range []string{"0 100 -4", "0 100 0"} {
				marker := fmt.Sprintf("WALLAIR%d%dYES", i, j)
				sendCommand(t, pw, "execute if block "+pos+" air run say "+marker)
				end := fmt.Sprintf("WALLCHECK%d%dEND", i, j)
				sendCommand(t, pw, "say "+end)
				lines := wait(end)
				air := false
				for _, line := range lines {
					if strings.Contains(line, marker) {
						air = true
					}
				}
				out = append(out, air)
				t.Logf("%s head=%s grief=%t pos=%s air=%t", addr, tc.head, tc.grief, pos, air)
				if air != tc.grief {
					t.Errorf("%s head=%s grief=%t pos=%s air=%t", addr, tc.head, tc.grief, pos, air)
				}
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, m := run(tg.vanilla), run(tg.mcgo)
	if fmt.Sprint(v) != fmt.Sprint(m) {
		t.Fatalf("vanilla%v mcgo%v", v, m)
	}
}
