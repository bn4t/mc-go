//go:build blackbox

// Blackbox parity pin for AbstractCubeMob.remove's SPLIT_ON_DEATH
// conversion on the 26.2 decompile. A size>1 cube dying (any cause —
// the corpse lingers twenty ticks, then remove(KILLED) runs the
// split) spawns getSplitCount() = 2+rand(3) children of half its
// size on a 2x2 grid: (i%2-0.5, i/2-0.5)·width/2 with the size-
// scaled width 0.52·size, snapped to +0.5y and a random yaw. Because
// convertCommon carries NoAi, a NoAI parent's children are NoAI too
// — every wave stays at its snapped spot, so the second kill's grid
// is centered on each wave-1 child and the third kill (size 1)
// spawns nothing at all.
package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestSlimeSplitParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type splitObs struct {
		children1 []int32 // wave-1 child eids (grid checked)
		adds2     int     // wave-2 slime adds
		adds3     int     // wave-3 slime adds (size-1 gate: must be 0)
		parentRem bool
	}

	run := func(t *testing.T, name, addr string) splitObs {
		o := splitObs{}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		_ = newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:slime]")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "gamemode survival BOATB")
		sendCommand(t, v.pwa, "tp BOATB 9.5 -60 14.5")
		// NoAI pins the parent: the wave-1 grid centers on the summon
		// spot, and convertCommon's NoAi carry pins every descendant
		// at its own snapped cell in turn.
		slime := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"slime", 9.5, -60, 9.5, "{Size:3,NoAI:1b}")
		if slime == 0 {
			t.Fatalf("%s: no slime spawn packet", name)
		}
		px, py, pz := 9.5, -60.0, 9.5

		// wave 1: kill the size-4 parent; children must land on
		// px±0.52 / pz±0.52, py+0.5 — width 0.52·4 → off 1.04.
		sendCommand(t, v.pwa, "kill @e[type=minecraft:slime]")
		gridPos := map[int32][3]float64{}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			wo := vehWatch(t, v, true, slime, 400*time.Millisecond)
			for _, se := range wo.slimeEIDs {
				if se != slime {
					o.children1 = append(o.children1, se)
					gridPos[se] = wo.lastPos[se]
				}
			}
			if wo.removed[slime] {
				o.parentRem = true
			}
		}
		onGrid := 0
		for _, se := range o.children1 {
			p := gridPos[se]
			if math.Abs(math.Abs(p[0]-px)-0.52) < 0.05 &&
				math.Abs(math.Abs(p[2]-pz)-0.52) < 0.05 &&
				math.Abs(p[1]-(py+0.5)) < 0.05 {
				onGrid++
			}
		}

		// wave 2: kill the size-2 children — each splits 2-4 more on
		// a ±0.26 grid around its CURRENT spot. NoAI children still
		// collide-push each other off their spawn cells, so only the
		// count is pinned here; existence at all proves size 2, and
		// wave 3's zero proves the size-1 gate.
		sendCommand(t, v.pwa, "kill @e[type=minecraft:slime]")
		deadline = time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			wo := vehWatch(t, v, true, slime, 400*time.Millisecond)
			for _, se := range wo.slimeEIDs {
				if se != slime {
					o.adds2++
				}
			}
		}

		// wave 3: the size-1 children die with no split — zero adds.
		sendCommand(t, v.pwa, "kill @e[type=minecraft:slime]")
		deadline = time.Now().Add(2500 * time.Millisecond)
		for time.Now().Before(deadline) {
			wo := vehWatch(t, v, true, slime, 400*time.Millisecond)
			for _, se := range wo.slimeEIDs {
				if se != slime {
					o.adds3++
				}
			}
		}
		t.Logf("%s: wave1 children=%d onGrid=%d parentRem=%v wave2 adds=%d wave3 adds=%d",
			name, len(o.children1), onGrid, o.parentRem, o.adds2, o.adds3)
		if len(o.children1) < 2 || len(o.children1) > 4 {
			t.Fatalf("%s: wave1 children = %d, want 2-4", name, len(o.children1))
		}
		if onGrid != len(o.children1) {
			t.Fatalf("%s: %d of %d wave1 children off the ±0.52 grid", name, len(o.children1)-onGrid, len(o.children1))
		}
		if !o.parentRem {
			t.Fatalf("%s: parent never removed", name)
		}
		want2 := 2 * len(o.children1)
		if o.adds2 < want2 || o.adds2 > 4*len(o.children1) {
			t.Fatalf("%s: wave2 adds = %d, want %d-%d", name, o.adds2, want2, 4*len(o.children1))
		}
		if o.adds3 != 0 {
			t.Fatalf("%s: wave3 adds = %d, want 0 (size-1 gate)", name, o.adds3)
		}
		return o
	}

	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	t.Logf("parity: split grid + size gate on both legs")
	_ = vo
	_ = mo
}
