// Blackbox parity pin for PanicGoal on the 26.2 decompile. The
// measured semantics: shouldPanic() reads getLastDamageSource(),
// which LivingEntity clears once the damage stamp ages past 40 game
// ticks — so a single panic-causing hit keeps the goal re-firing for
// ~40 ticks. Every fire picks a fresh DefaultRandomPos.getPos
// target — a uniform random cell ±5 horizontal / ±4 vertical that
// passes isStableDestination + no-malus — NOT a line away from the
// attacker — and runs the navigation to it at speedModifier ×
// movement_speed. The pin is the window, not the trajectory: a mob
// should keep moving for most of the ~40 ticks (pos-packet count
// near the window), cover real distance, and stay bounded by the
// ±5-block random targets rather than escaping on a flee-vector.
package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestPanicFleeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type fleeObs struct {
		posTicks int
		travel   float64
		netDisp  float64
		flips    int
	}

	run := func(t *testing.T, name, addr string) fleeObs {
		o := fleeObs{}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		_ = newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:cow]")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "gamemode survival BOATB")
		// Open flat ground far from the earlier pens — the random ±5
		// targets mostly land reachable.
		sendCommand(t, v.pwa, "tp BOATA 30.5 -60 30.5")
		sendCommand(t, v.pwa, "tp BOATB 30.5 -60 36.5")
		cow := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"cow", 32.5, -60, 30.5, "{}")
		if cow == 0 {
			t.Fatalf("%s: no cow spawn packet", name)
		}

		sendAttackEntity(v.pwa, cow)
		// Watch the whole 40-tick window plus tail; posTicks counts
		// only the first ~2.4 s so post-panic stroll motion can't
		// inflate the panic-window pin.
		merged := vehObs{
			posNet:   map[int32]float64{},
			lastPos:  map[int32][3]float64{},
			posTicks: map[int32]int{},
			posTrail: map[int32][][2]float64{},
		}
		start := time.Now()
		deadline := start.Add(4 * time.Second)
		for time.Now().Before(deadline) {
			// Conn A observes: BOATA is inside the cow's tracking
			// range by construction (he punched it), while BOATB's
			// tp can still be queued behind his join window.
			wo := vehWatch(t, v, false, cow, 400*time.Millisecond)
			for eid, d := range wo.posNet {
				merged.posNet[eid] += d
			}
			for eid, p := range wo.lastPos {
				merged.lastPos[eid] = p
			}
			if time.Since(start) < 2400*time.Millisecond {
				for eid, n := range wo.posTicks {
					merged.posTicks[eid] += n
				}
			}
			for eid, tr := range wo.posTrail {
				merged.posTrail[eid] = append(merged.posTrail[eid], tr...)
			}
		}
		o.posTicks = merged.posTicks[cow]
		o.travel = merged.posNet[cow]
		// summonTrackedNBT consumes the cow's add packet, so lastPos
		// never got a base — take net displacement from the delta
		// trail's running sum instead of absolute positions.
		tr := merged.posTrail[cow]
		sx, sz := 0.0, 0.0
		for _, d := range tr {
			sx += d[0]
			sz += d[1]
		}
		o.netDisp = math.Hypot(sx, sz)
		// Direction flips on either axis with a real step size — the
		// random-target signature (informational in the log only).
		for i := 1; i < len(tr); i++ {
			if math.Abs(tr[i][0]) > 0.03 && math.Abs(tr[i-1][0]) > 0.03 &&
				(tr[i][0] < 0) != (tr[i-1][0] < 0) {
				o.flips++
			}
		}
		t.Logf("%s: posTicks=%d travel=%.2f netDisp=%.2f flips=%d trail=%v",
			name, o.posTicks, o.travel, o.netDisp, o.flips, tr)
		if o.travel < 1.0 {
			t.Fatalf("%s: travel %.2f < 1.0 — panic never ran", name, o.travel)
		}
		// Vanilla's entity tracker emits a pos packet every
		// updateInterval ticks (~3), so ~45 ticks of panic motion
		// lands ~15 packets; a single ~10-tick run lands ~3-5.
		if o.posTicks < 10 {
			t.Fatalf("%s: posTicks %d < 10 — panic window too short (want ~40t re-arms)", name, o.posTicks)
		}
		// The ±5-target contract bounds displacement loosely: aligned
		// rolls inside one window legitimately reach ~11-14 (each roll
		// walks ~7 with overshoot), while the pre-parity away-from-
		// attacker run ran 20+ on a single vector — the cap sits in
		// between and tolerates the rare all-aligned chain.
		if o.netDisp > 16 {
			t.Fatalf("%s: netDisp %.2f > 16 — fled on a vector, not ±5 random targets", name, o.netDisp)
		}
		// (No "goes quiet" pin: vanilla's stroll goal resumes after
		// the window and keeps ~0.2b/t motion inside the observation.)
		return o
	}

	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	t.Logf("parity: 40-tick panic window with random targets on both legs")
	_ = vo
	_ = mo
}
