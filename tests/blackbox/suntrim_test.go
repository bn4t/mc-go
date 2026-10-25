package blackbox

import (
	"math"
	"testing"
	"time"
)

// TestRestrictSunParity pins RestrictSunGoal + GroundPathNavigation's
// trimPath edge: a bare-headed AbstractSkeleton that is already under
// cover during bright daylight never follows a path into open sky —
// vanilla truncates the path at the first sky-visible node, so the
// mob pins itself along the shadow's edge rather than walking out.
// Each leg puts a roof over a bare skeleton at noon and requires its
// Pos to stay inside the roof footprint for the whole window; the
// mob standing in sun is never trimmed (sunlit skeletons walk free,
// FleeSunGoal owns the burn-driven escape).
func TestRestrictSunParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)

	run := func(addr string) (float64, float64) {
		cv, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		sink := newChatSinkTP(pr, pw)

		sendCommand(t, pw, "time set 1000")
		sendCommand(t, pw, "weather clear")
		// Floor then roof — the skeleton stands at y -60 under a
		// stone slab at y -58 spanning -6..6 on both axes.
		sendCommand(t, pw, "fill -8 -61 -8 8 -61 8 minecraft:stone")
		sendCommand(t, pw, "fill -6 -58 -6 6 -58 6 minecraft:stone")
		summonAt(t, sink, pw, "minecraft:skeleton", 0, -60, 0)

		// Wander window: enough for several stroll retargets on
		// vanilla (RandomStrollGoal idles then walks) and a few
		// heading re-picks on mc-go (every 40 ticks).
		time.Sleep(25 * time.Second)
		sink.take() // drop ambient chatter

		sel := "@e[type=minecraft:skeleton,limit=1]"
		x := nbtFloatOf(t, sink, pw, sel, "Pos[0]")
		z := nbtFloatOf(t, sink, pw, sel, "Pos[2]")
		if x < -1 || z < -1 {
			t.Fatalf("Pos query failed on %s", addr)
		}
		return x, z
	}

	vx, vz := run(targets.vanilla)
	mx, mz := run(targets.mcgo)
	t.Logf("vanilla skeleton ended at %.1f,%.1f", vx, vz)
	t.Logf("mc-go   skeleton ended at %.1f,%.1f", mx, mz)
	// The roof shades blocks -6..6; the wander step refuses the
	// shade->sun crossing at the block boundary, so the center can
	// reach 7.9 at most. Both legs must stay inside.
	if math.Abs(vx) > 7.9 || math.Abs(vz) > 7.9 {
		t.Errorf("vanilla skeleton left the shade: %.1f,%.1f", vx, vz)
	}
	if math.Abs(mx) > 7.9 || math.Abs(mz) > 7.9 {
		t.Errorf("mc-go skeleton left the shade: %.1f,%.1f", mx, mz)
	}
}
