//go:build blackbox

// Blackbox parity pin for collision-shape line of sight: vanilla's
// hasLineOfSight clips (COLLIDER) against the cell's real collision
// boxes, not a unit cube — a low block's empty upper half is air for
// the ray while its filled part blocks. FrogAi's attackables sensor
// is the observable: it picks only VISIBLE FROG_FOOD cubes, and the
// pick emits DATA_TONGUE_TARGET_ID (metadata 19, target eid+1) when
// ShootTongue starts.
//
// Eyes on the flat floor sit at 0.425 (frog) / ~0.44 (size-1 slime),
// so the sight ray rides y≈-59.57 — above snow[layers=2]'s 0.25 top,
// inside snow[layers=6]'s 0.75 column, inside a lone glass pane's
// 0.4375..0.5625 node, and inside stone. Probes: (A) snow 2 layers —
// sight passes, the hook emits and the eat lands; (B) snow 6 layers —
// no emit; (C) glass pane — no emit; (D) stone — no emit. Every frog
// is penned on three sides with stone so a stroll can't rotate the
// ray around the probe block; each wall cell sits squarely on the
// eye-to-eye segment. Measured on the 26.2 jar: SnowLayerBlock's
// n/8-tall box, CrossCollisionBlock's pane node,
// LivingEntity.hasLineOfSight.
package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestFrogSightShapeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, addr string) (lowEmit, highEmit, paneEmit, stoneEmit, lowEaten bool) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "frogsight", v.ca, v.pra, v.pwa, 0)

		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		// Park the watchers inside tracking range — past ~32 blocks
		// both servers untrack the entities, remove_entities lands on
		// the wire, and the metadata stops arriving (a dead slime and
		// an untracked slime look identical at 0x4D).
		sendCommand(t, v.pwa, "tp BOATA 26 -60 11")
		sendCommand(t, v.pwa, "tp BOATB 26 -60 11")

		set := func(x, z int, b string) {
			sendCommand(t, v.pwa, "setblock "+strconv.Itoa(x)+" -60 "+strconv.Itoa(z)+" "+b)
		}
		type pair struct{ frog, slime int32 }
		spawn := func(x int, wall string) pair {
			// Corridor pen: lateral stone through z 8..10 and a rear
			// cell keep the frog on its column — a strolling frog
			// otherwise peeks around a one-cell wall and emits anyway
			// (measured on vanilla).
			for z := 8; z <= 10; z++ {
				set(x-1, z, "minecraft:stone")
				set(x+1, z, "minecraft:stone")
			}
			set(x, 7, "minecraft:stone")
			set(x, 11, wall)
			f := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "frog", float64(x)+0.5, -60, 8.5, "{}")
			s := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "slime", float64(x)+0.5, -60, 13.5, "{Size:0,NoAI:1b}")
			return pair{f, s}
		}
		hookOf := func(p pair) string {
			return strconv.Itoa(int(p.frog)) + ":" + strconv.Itoa(int(p.slime)+1)
		}

		// Pairs sit 12 blocks apart — past the sensor's 10-block
		// detection range so no frog hunts another pair's slime.
		low := spawn(8, "minecraft:snow[layers=2]")   // 0.25 top — ray clears
		high := spawn(20, "minecraft:snow[layers=6]") // 0.75 top — ray inside
		pane := spawn(32, "minecraft:glass_pane")     // 1/16 node — ray inside
		stone := spawn(44, "minecraft:stone")         // full cube — ray inside

		hookL, hookH, hookP, hookSt := hookOf(low), hookOf(high), hookOf(pane), hookOf(stone)
		deadline := time.Now().Add(16 * time.Second)
		for time.Now().Before(deadline) && !lowEaten {
			o := vehWatch(t, v, false, 0, 300*time.Millisecond)
			for _, h := range o.tongueHist {
				if strings.HasSuffix(h, ":0") {
					continue
				}
				lowEmit = lowEmit || h == hookL
				highEmit = highEmit || h == hookH
				paneEmit = paneEmit || h == hookP
				stoneEmit = stoneEmit || h == hookSt
			}
			if o.removed[low.slime] {
				lowEaten = true
			}
		}
		// Tail watch: a late emit on a blocked pair still counts.
		for i := 0; i < 8; i++ {
			o := vehWatch(t, v, false, 0, 300*time.Millisecond)
			for _, h := range o.tongueHist {
				if strings.HasSuffix(h, ":0") {
					continue
				}
				lowEmit = lowEmit || h == hookL
				highEmit = highEmit || h == hookH
				paneEmit = paneEmit || h == hookP
				stoneEmit = stoneEmit || h == hookSt
			}
		}
		return
	}

	check := func(name string, lowEmit, highEmit, paneEmit, stoneEmit, lowEaten bool) {
		t.Helper()
		if !lowEmit {
			t.Errorf("%s: no tongue hook over snow[layers=2] — the empty half should be clear", name)
		}
		if !lowEaten {
			t.Errorf("%s: slime behind snow[layers=2] was never eaten", name)
		}
		if highEmit {
			t.Errorf("%s: tongue hook through snow[layers=6] — the 0.75 column should block", name)
		}
		if paneEmit {
			t.Errorf("%s: tongue hook through a glass pane — the node should block", name)
		}
		if stoneEmit {
			t.Errorf("%s: tongue hook through stone", name)
		}
		t.Logf("%s: low emit=%v eaten=%v | high=%v pane=%v stone=%v",
			name, lowEmit, lowEaten, highEmit, paneEmit, stoneEmit)
	}

	vL, vH, vP, vSt, vE := run(t, targets.vanilla)
	mL, mH, mP, mSt, mE := run(t, targets.mcgo)
	check("vanilla", vL, vH, vP, vSt, vE)
	check("mc-go", mL, mH, mP, mSt, mE)
}
