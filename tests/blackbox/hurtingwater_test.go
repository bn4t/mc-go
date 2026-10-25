package blackbox

import (
	"math"
	"testing"
	"time"
)

// TestHurtingWaterParity pins a hurting projectile's water-entry
// trajectory against the real server: AbstractHurtingProjectile's
// applyInertia reads the isInWater flag Entity.tick latches at the
// previous tick's tail — one tick behind the box position — so the
// liquid inertia (0.8 base) reaches the flight a tick after the box
// first touches water, not at the boundary tick itself. The flow push
// rides the same Entity.tick tail with its own one-tick lag on the
// flag. Measured on the 26.2 jar via freeze + `tick step` checkpoints,
// matching the arrow model pinned in `TestArrowStickParity` lane D2.
func TestHurtingWaterParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("HW"),
	}, nil)
	defer targets.cleanup()

	type leg struct {
		deepMot, deepPos [][]float64
		pushMot, pushPos []float64
	}
	legs := map[string]*leg{}

	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		conn, pr, pw := joinPlayBlackbox(t, tg.addr, "HW")
		defer conn.Close()
		sink := newChatSinkTP(pr, pw)
		// Same client_information as the inertia test: without it
		// vanilla's default view distance 2 untracks the skull mid-
		// flight and the probes come back empty.
		sendClientInformation(t, pw)
		sendCommand(t, pw, "tp @s 30 -60 -20 0 0")
		dataGetLine(t, sink, pw, "@s", "Pos")
		l := &leg{}

		// Deep lane: a large water box, interior source cells read
		// zero getFlow — only the inertia lag shows in the trajectory.
		sendCommand(t, pw, "fill 27 -61 -11 39 -55 3 minecraft:air")
		sendCommand(t, pw, "fill 28 -61 -10 38 -55 2 minecraft:water")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "kill @e[type=minecraft:wither_skull]")
		sendCommand(t, pw, "summon minecraft:wither_skull 32 -59 -8 {Motion:[0.0d,0.0d,0.5d]}")
		selDeep := "@e[type=minecraft:wither_skull,x=29,y=-62,z=-10,dx=6,dy=6,dz=12,limit=1]"
		for i := 0; i < 4; i++ {
			sendCommand(t, pw, "tick step 2")
			// /tick step is not atomic — wait the count out before
			// probing; the frozen world keeps the state stable.
			time.Sleep(800 * time.Millisecond)
			dataGetLine(t, sink, pw, "@s", "Pos")
			m := dataGetFloats(t, sink, pw, selDeep, "Motion")
			p := dataGetFloats(t, sink, pw, selDeep, "Pos")
			// Scalar data gets under freeze+step read one tick stale
			// on alternating calls — a second identical read can't be
			// from a different tick while frozen, so it is reliable.
			m = dataGetFloats(t, sink, pw, selDeep, "Motion")
			p = dataGetFloats(t, sink, pw, selDeep, "Pos")
			t.Logf("%s deepstep %d mot=%v pos=%v", tg.tag, (i+1)*2, m, p)
			l.deepMot = append(l.deepMot, m)
			l.deepPos = append(l.deepPos, p)
		}
		sendCommand(t, pw, "tick unfreeze")
		time.Sleep(400 * time.Millisecond)

		// Boundary lane: a small source box whose edge cells flow
		// outward — the skull grazes a flowing boundary cell and takes
		// the ~0.014/tick push once the flag latches.
		sendCommand(t, pw, "fill 44 -61 -10 50 -55 0 minecraft:air")
		sendCommand(t, pw, "fill 45 -61 -6 49 -56 -2 minecraft:water")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "kill @e[type=minecraft:wither_skull]")
		sendCommand(t, pw, "summon minecraft:wither_skull 46 -59 -8 {Motion:[0.0d,0.0d,0.5d]}")
		selPush := "@e[type=minecraft:wither_skull,x=44,y=-62,z=-10,dx=6,dy=6,dz=10,limit=1]"
		for i := 0; i < 4; i++ {
			sendCommand(t, pw, "tick step 2")
			time.Sleep(800 * time.Millisecond)
			dataGetLine(t, sink, pw, "@s", "Pos")
			m := dataGetFloats(t, sink, pw, selPush, "Motion")
			p := dataGetFloats(t, sink, pw, selPush, "Pos")
			m = dataGetFloats(t, sink, pw, selPush, "Motion")
			p = dataGetFloats(t, sink, pw, selPush, "Pos")
			t.Logf("%s pushstep %d mot=%v pos=%v", tg.tag, (i+1)*2, m, p)
		}
		l.pushMot = dataGetFloats(t, sink, pw, selPush, "Motion")
		l.pushMot = dataGetFloats(t, sink, pw, selPush, "Motion")
		l.pushPos = dataGetFloats(t, sink, pw, selPush, "Pos")
		l.pushPos = dataGetFloats(t, sink, pw, selPush, "Pos")
		sendCommand(t, pw, "tick unfreeze")
		time.Sleep(400 * time.Millisecond)
		legs[tg.tag] = l
	}

	eqEps := func(name string, a, b []float64) {
		t.Helper()
		if len(a) != len(b) {
			t.Fatalf("%s len %v vs %v (%v vs %v)", name, len(a), len(b), a, b)
		}
		for i := range a {
			if math.Abs(a[i]-b[i]) > 1e-6 {
				t.Fatalf("%s[%d]: %v vs %v (full %v vs %v)", name, i, a[i], b[i], a, b)
			}
		}
	}
	v, g := legs["vanilla"], legs["mc-go"]
	// The deep lane's data gets can land a tick apart between legs:
	// summoned under freeze, vanilla's entity joins the tick list a
	// tick late, and the step-checkpoint reads shift ±1 index between
	// runs. Pin the trajectory instead: find the single constant
	// index shift aligning the two sequences, then require every
	// overlapping checkpoint to match to 1e-6. A real physics
	// divergence (wrong inertia, a missing flag tick) aligns at no
	// shift.
	eqShifted := func(name string, van, mine [][]float64) {
		t.Helper()
		sameVec := func(a, b []float64) bool {
			if len(a) != len(b) {
				return false
			}
			for i := range a {
				if math.Abs(a[i]-b[i]) > 1e-6 {
					return false
				}
			}
			return true
		}
		for lag := -1; lag <= 1; lag++ {
			// van[k] should equal mine[k+lag] wherever both exist.
			ok := false
			for k := range van {
				j := k + lag
				if j < 0 || j >= len(mine) {
					continue
				}
				if !sameVec(van[k], mine[j]) {
					ok = false
					break
				}
				ok = true
			}
			if ok {
				t.Logf("%s aligned at lag %d", name, lag)
				return
			}
		}
		t.Fatalf("%s: no constant shift aligns vanilla %v with mc-go %v", name, van, mine)
	}
	eqShifted("deepMot", v.deepMot, g.deepMot)
	eqShifted("deepPos", v.deepPos, g.deepPos)
	eqEps("pushMot", v.pushMot, g.pushMot)
	eqEps("pushPos", v.pushPos, g.pushPos)
}
