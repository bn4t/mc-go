//go:build blackbox

// Sun-shelter parity: 26.2's FleeSunGoal is registered only on the
// AbstractSkeleton family — a burning, bare-headed skeleton under open
// sky paths to a shaded cell (getHidePos takes up to ten random
// samples in a ±10 XZ / ±3 Y box; the first non-sky-visible standable
// cell wins) and stands once there. Zombies and every other
// #burn_in_daylight type have no such goal — they wander while they
// burn. A helmeted skeleton never ignites, so it never seeks.
//
// The seek is pinned by the mob's sampled position after a fixed
// window: a stone roof east of the summon point is the only shade in
// range, so drifting toward it means the goal ran.

package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// TestMobSunFleeParity pins the seek on both servers:
//   - burning skeleton reaches the roof's shade (east end >= 5)
//   - burning zombie never seeks (same flag false on both)
//   - helmeted skeleton stays unlit and wanders near the start
func TestMobSunFleeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(240 * time.Second))
		obs := &fleeObs{}
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "weather clear")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)

		leg := func(name, typ, nbt string) {
			// Flatten the arena and raise the only shade east: a
			// stone roof at x +8..+16, four blocks overhead — the
			// goal's ±10 sampling box reaches it from origin.
			sendCommand(t, pw, "fill ~-16 ~-1 ~-16 ~24 ~-1 ~16 minecraft:stone")
			sendCommand(t, pw, "fill ~-16 ~ ~-16 ~24 ~10 ~16 minecraft:air")
			sendCommand(t, pw, "fill ~8 ~4 ~-4 ~16 ~4 ~4 minecraft:stone")
			sendCommand(t, pw, "time set 1000")
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s ~ ~ ~ %s", typ, nbt))
			fleePump(t, c, pr, pw, obs, 10*time.Second)
			pos := fleePos(t, c, pr, pw, obs, "@e[type=minecraft:"+typ+",limit=1]")
			out = append(out, fmt.Sprintf("%s east=%v", name, pos[0] >= 5))
			sendCommand(t, pw, fmt.Sprintf("kill @e[type=minecraft:%s]", typ))
			fleePump(t, c, pr, pw, obs, 400*time.Millisecond)
		}

		leg("burning_skeleton", "skeleton", "{Fire:600s}")
		leg("burning_zombie", "zombie", "{Fire:600s}")
		leg("helmeted_skeleton", "skeleton",
			"{Fire:600s,equipment:{head:{id:\"minecraft:leather_helmet\",count:1}}}")
		return out
	}

	van := run(targets.vanilla)
	got := run(targets.mcgo)
	for k := range van {
		t.Logf("leg %d vanilla: %s", k, van[k])
		t.Logf("leg %d mc-go:   %s", k, got[k])
		if van[k] != got[k] {
			t.Errorf("leg %d mismatch: %q vs %q", k, van[k], got[k])
		}
	}
}
