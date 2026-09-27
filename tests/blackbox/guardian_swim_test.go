package blackbox

import (
	"math"
	"testing"
	"time"
)

// TestGuardianSwimParity pins Guardian's water movement, measured off
// the 26.2 jar: RandomStrollGoal(1.0, 80) rolls a 3D aquatic target
// every ~80 idle ticks, GuardianMoveControl's MOVE_TO then lerps the
// speed field toward speedModifier·movement_speed with the sin wobble
// + bob delta, and Guardian.travelInWater runs moveRelative(0.1) +
// delta·0.9 — no gravity, only a -0.005 idle sink. Observable parity
// vs a land mob dropped in the same water: the zombie sinks several
// blocks toward the bed, while the guardian hovers and swims —
// horizontal displacement plus a vertical range from the strolls'
// y-drive. The pool is oversized on purpose: the ±10 aquatic pick
// lands inside water ~every roll, so a server that can't swim fails
// deterministically instead of flaking on pick RNG.
func TestGuardianSwimParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("SwA")}, nil)
	defer targets.cleanup()

	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		cA, prA, pwA := joinPlayBlackbox(t, s.addr, "SwA")
		defer cA.Close()
		sendClientInformation(t, pwA)
		sinkA := newChatSinkTP(prA, pwA)
		time.Sleep(600 * time.Millisecond)

		// A 22x10x22 stone shell at (33..54, -64..-55, 33..54) with a
		// 20x8x20 water interior — sized so the ±10 aquatic pick lands
		// in water nearly every roll, and deep enough that the
		// land-mob sink is several blocks before it beds. (Deeper is
		// impossible — /fill below world min-y silently no-ops.)
		sendCommand(t, pwA, "fill 33 -64 33 54 -55 54 minecraft:stone")
		sendCommand(t, pwA, "fill 34 -63 34 53 -56 53 minecraft:water")
		sendCommand(t, pwA, "summon minecraft:guardian 43.5 -59 43.5 {PersistenceRequired:1b}")
		sendCommand(t, pwA, "summon minecraft:zombie 45.5 -59 45.5 {PersistenceRequired:1b}")
		gSel := "@e[type=minecraft:guardian,limit=1]"
		zSel := "@e[type=minecraft:zombie,limit=1]"
		time.Sleep(800 * time.Millisecond)

		var maxDisp, minGY, maxGY, zMinY float64
		var gx, gz float64
		ok := false
		deadline := time.Now().Add(18 * time.Second)
		minGY, zMinY = -59, -59
		for time.Now().Before(deadline) {
			if x, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Pos[0]"); ok2 {
				gx, ok = x, true
			}
			if z, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Pos[2]"); ok2 {
				gz = z
			}
			if ok {
				if d := math.Hypot(gx-43.5, gz-43.5); d > maxDisp {
					maxDisp = d
				}
			}
			if y, ok2 := flopQueryNum(t, sinkA, pwA, gSel, "Pos[1]"); ok2 {
				if y < minGY {
					minGY = y
				}
				if y > maxGY {
					maxGY = y
				}
			}
			if y, ok2 := flopQueryNum(t, sinkA, pwA, zSel, "Pos[1]"); ok2 && y < zMinY {
				zMinY = y
			}
			time.Sleep(400 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("%s: guardian Pos never answered", s.label)
		}
		if maxDisp < 1.5 {
			t.Fatalf("%s: guardian barely moved — max horizontal displacement %v, want >= 1.5", s.label, maxDisp)
		}
		// Strolls carry a y-drive and the idle -0.005 sinks between
		// them, so a swimming guardian covers vertical ground — a
		// floored land mob's range is the bed's ~0.
		if r := maxGY - minGY; r < 0.8 {
			t.Fatalf("%s: guardian vertical range %v too small — strolls never lifted it (min %v, max %v)", s.label, r, minGY, maxGY)
		}

		// Contrast leg: the zombie gets the generic fluid sink and
		// drops several blocks toward the bed.
		if zMinY >= -62.5 {
			t.Fatalf("%s: zombie did not sink in the pool — min Pos[1] %v, want < -62.5", s.label, zMinY)
		}
	}
}
