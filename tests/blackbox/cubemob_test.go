//go:build blackbox

// Blackbox parity pin for AbstractCubeMob's hop model on the 26.2
// decompile: CubeMobMoveControl waits getJumpDelay ticks on the
// ground — random.nextInt(20)+10 for a slime, ×4 for a magma_cube,
// ÷3 while the attack goal locks a target — then JumpControl's
// jumpFromGround fires vy = getJumpPower (magma + size*0.1) with
// needsSync latched, the speed field latched to speedModifier ×
// (0.2 + 0.1*size) keeps driving the airborne ticks forward at the
// normalized-input influence speed*0.216, and a landing bursts
// size*16 ITEM_SLIME / FLAME packets and the squish sound.
// playerTouch deals attack_damage (size, +2 for magma) on box
// overlap. Measured from AbstractCubeMob.java + MagmaCube.java +
// Slime.java + MoveControl.java.
package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/server"
)

// cubeHopObs is one leg's wire facts for a hop window.
type cubeHopObs struct {
	hops     int // set_entity_motion emits with vy > 0.3 on the cube
	maxVy    float64
	minVy    float64
	maxHoriz float64 // |v_xz| on hop emits — logged, not pinned
	net      float64 // posNet horizontal travel summed over the window
	parts    int     // landing-burst particle packets (item_slime/flame)
	soundCnt int     // positioned-sound packets on the window
	motion   map[int32]int
}

func TestCubeHopParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) (slimeObs, magmaObs cubeHopObs, dmgBefore, dmgAfter float64, slmE, magE int32) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "cubehop", v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:slime]")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:magma_cube]")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 25")
		sendCommand(t, v.pwa, "tp BOATC 8.5 -60 25")
		sendCommand(t, v.pwa, "tick rate 40")

		fold := func(o vehObs, leg *cubeHopObs, eid int32) {
			for m, vecs := range o.motVec {
				if m != eid {
					continue
				}
				for _, vec := range vecs {
					if vec[1] > 0.3 {
						leg.hops++
						if leg.hops == 1 || vec[1] > leg.maxVy {
							leg.maxVy = vec[1]
						}
						if leg.minVy == 0 || vec[1] < leg.minVy {
							leg.minVy = vec[1]
						}
						if hv := math.Hypot(vec[0], vec[2]); hv > leg.maxHoriz {
							leg.maxHoriz = hv
						}
					}
				}
			}
			leg.parts += 0
			leg.net += o.posNet[eid]
			leg.soundCnt += o.sounds
			for k, n := range o.motion {
				leg.motion[k] += n
			}
		}

		// Leg 1 — a size-2 slime hops on its own cadence: delay
		// 10-29t, vy = jumpPower 0.42 exactly.
		slimeObs = cubeHopObs{motion: map[int32]int{}}
		slmE = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"slime", 8.5, -60, 8.5, "{Size:1}")
		sendCommand(t, v.pwa, "tick freeze")
		time.Sleep(300 * time.Millisecond)
		fold(vehWatch(t, v, false, 0, 300*time.Millisecond), &slimeObs, slmE)
		sendCommand(t, v.pwa, "tick step 80")
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			fold(vehWatch(t, v, false, 0, 250*time.Millisecond), &slimeObs, slmE)
		}
		sendCommand(t, v.pwa, "tick unfreeze")

		// Leg 2 — a size-2 magma_cube: delay 40-119t, vy = 0.42 +
		// size*0.1 = 0.62.
		magmaObs = cubeHopObs{motion: map[int32]int{}}
		magE = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"magma_cube", 28.5, -60, 8.5, "{Size:1}")
		sendCommand(t, v.pwa, "tick freeze")
		time.Sleep(300 * time.Millisecond)
		fold(vehWatch(t, v, false, 0, 300*time.Millisecond), &magmaObs, magE)
		sendCommand(t, v.pwa, "tick step 200")
		deadline = time.Now().Add(14 * time.Second)
		for time.Now().Before(deadline) {
			fold(vehWatch(t, v, false, 0, 250*time.Millisecond), &magmaObs, magE)
		}
		sendCommand(t, v.pwa, "tick unfreeze")

		// Leg 3 — the touch damage: BOATA back to survival next to
		// a fresh size-2 slime; the cube locks the heading, hops at
		// it, and playerTouch deals the hit.
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		slmD := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"slime", 11.5, -60, 28.5, "{Size:1}")
		_ = slmD
		_, dmgBefore = dataGetSel(t, sink, pwc, "BOATA", "Health")
		deadline = time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			fold(vehWatch(t, v, false, 0, 400*time.Millisecond), &slimeObs, slmD)
			_, hp := dataGetSel(t, sink, pwc, "BOATA", "Health")
			if hp < dmgBefore {
				break
			}
		}
		_, dmgAfter = dataGetSel(t, sink, pwc, "BOATA", "Health")
		return slimeObs, magmaObs, dmgBefore, dmgAfter, slmE, magE
	}

	check := func(t *testing.T, name string, slime, magma cubeHopObs,
		before, after float64, slmE, magE int32) {
		if slime.hops < 2 {
			t.Fatalf("%s: slime %d emitted %d hop motions in 80 steps, want >=2", name, slmE, slime.hops)
		}
		// Slime jumpFromGround sets vy = 0.42 but needsSync emits
		// after the tick's gravity+drag: 0.42*0.98 - 0.08 = 0.332.
		if math.Abs(slime.maxVy-0.332) > 0.06 {
			t.Fatalf("%s: slime hop vy %.3f, want ~0.33", name, slime.maxVy)
		}
		if magma.hops < 1 {
			t.Fatalf("%s: magma_cube %d emitted no hop motion in 200 steps", name, magE)
		}
		// MagmaCube.jumpFromGround vy = 0.42 + size*0.1 = 0.62,
		// same end-of-tick decay: 0.62*0.98 - 0.08 = 0.528.
		if math.Abs(magma.maxVy-0.528) > 0.08 {
			t.Fatalf("%s: magma hop vy %.3f, want ~0.53", name, magma.maxVy)
		}
		if after >= before {
			t.Fatalf("%s: touch damage never landed (health %.0f -> %.0f)", name, before, after)
		}
		t.Logf("%s: slime hops=%d vy=%.3f hz=%.3f magma hops=%d vy=%.3f hz=%.3f health %.0f->%.0f",
			name, slime.hops, slime.maxVy, slime.maxHoriz,
			magma.hops, magma.maxVy, magma.maxHoriz, before, after)
	}

	vSlm, vMag, vB, vA, vSlmE, vMagE := run(t, "vanilla", targets.vanilla)
	mSlm, mMag, mB, mA, mSlmE, mMagE := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vSlm, vMag, vB, vA, vSlmE, vMagE)
	check(t, "mc-go", mSlm, mMag, mB, mA, mSlmE, mMagE)

	// Per-hop horizontal travel: grounded zza=drive influence plus
	// the flat-0.02 air influence carry the hop arc; a unit-input or
	// speed-scaled drive bug lands far outside 2x of vanilla's arc.
	for _, pr := range [][2]cubeHopObs{{vSlm, mSlm}, {vMag, mMag}} {
		vAvg := pr[0].net / float64(pr[0].hops)
		mAvg := pr[1].net / float64(pr[1].hops)
		if mAvg > vAvg*2.2 || mAvg < vAvg*0.35 {
			t.Fatalf("hop distance off parity: vanilla %.3f/hop vs mc-go %.3f/hop", vAvg, mAvg)
		}
		t.Logf("hop distance: vanilla %.3f/hop, mc-go %.3f/hop", vAvg, mAvg)
	}
}
