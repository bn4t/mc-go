package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// dataGetPos reads a selector's Pos NBT into xyz.
func dataGetPos(t *testing.T, sink *chatSink, pw *protocol.Writer, sel string) (x, y, z float64, ok bool) {
	t.Helper()
	line := dataGetLine(t, sink, pw, sel, "Pos")
	pl, ok2 := parseSNBT(line)
	if !ok2 {
		return 0, 0, 0, false
	}
	l, ok2 := pl.([]any)
	if !ok2 || len(l) != 3 {
		return 0, 0, 0, false
	}
	var p [3]float64
	for i, v := range l {
		n, ok3 := hrNum(v)
		if !ok3 {
			return 0, 0, 0, false
		}
		p[i] = n
	}
	return p[0], p[1], p[2], true
}

// TestWaterCurrentPushParity: FlowingFluid.getFlow + the
// EntityFluidInteraction tracker — every fluid cell overlapping an
// entity contributes its normalized getFlow (own-height differentials
// along the level gradient, the 0.8888889 below-look weight off an
// empty ledge, and a falling cell pressed to a solid face pulling
// -6 y), scaled by a submersion depth under 0.4, normalized and
// applied at the tag's 0.014 water factor.
//
// The probes are armor stands: a LivingEntity with no goals, so it
// never moves on its own — it sinks under gravity and lets the
// current push do the talking. (Measured probe results that ruled
// out the alternatives: items float with their box bottom just above
// the fluid top and read no overlap — a submerged NoGravity item
// climbs +4.4 through the underwater lift instead; arrows summoned
// with zero Motion never accumulate a delta; a NoAI zombie doesn't
// tick physics at all in 26.2 — it hung frozen in mid-air.)
//
// Leg A (two-source convergence): one channel fed by a source at
// each end — left front pushes +x through levels 1..3, right front
// pushes -x, and the level-4 tie cell in the middle carries flow 0.
// The stand rides +x, stalls against the opposing current, and
// parks in the dead band — the convergence point is decided purely
// by the getFlow differentials.
//
// Leg B (terminal sump): a single-source channel whose level-7 end
// drops into a dug pit — the falling cell below pulls -6 y while
// the gradient above still pushes +x, so the stand is swept into
// the sump and pinned at its floor by the waterfall arm.
func TestWaterCurrentPushParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("WC")}, nil)
	defer targets.cleanup()

	type legRes struct{ x, y, z float64 }
	land := map[string]map[string]legRes{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "WC")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule spawn_mobs false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			res := map[string]legRes{}
			sel := "@e[type=minecraft:armor_stand,limit=1,sort=nearest]"
			summonStand := func(x, y, z float64) {
				for attempt := 0; attempt < 5; attempt++ {
					sendCommand(t, pw, fmt.Sprintf(
						`summon minecraft:armor_stand %.2f %.2f %.2f`, x, y, z))
					time.Sleep(300 * time.Millisecond)
					line := dataGetLine(t, sink, pw, sel, "Pos")
					if _, ok := parseSNBT(line); ok {
						return
					}
				}
				t.Fatalf("%s: armor stand summon never landed", s.label)
			}
			// --- Leg A: walled channel on the grass surface (floor
			// is the terrain at y=-61), sources at x=-6 and x=2.
			for _, cmd := range []string{
				"fill -7 -60 2 3 -59 2 minecraft:stone",  // z=2 wall
				"fill -7 -60 4 3 -59 4 minecraft:stone",  // z=4 wall
				"fill -7 -60 3 -7 -59 3 minecraft:stone", // west cap
				"fill 3 -60 3 3 -59 3 minecraft:stone",   // east cap
				"setblock -6 -60 3 minecraft:water",      // left source
				"setblock 2 -60 3 minecraft:water",       // right source
			} {
				sendCommand(t, pw, cmd)
			}
			sprintTicks(t, pw, sink, 40) // fronts meet at the tie cell
			summonStand(-4.5, -59.5, 3.5)
			time.Sleep(400 * time.Millisecond)
			sprintTicks(t, pw, sink, 80)
			time.Sleep(300 * time.Millisecond)
			ax, ay, az, ok := dataGetPos(t, sink, pw, sel)
			if !ok {
				t.Fatalf("%s: leg A stand Pos unreadable", s.label)
			}
			res["A"] = legRes{ax, ay, az}
			sendCommand(t, pw, "kill @e[type=minecraft:armor_stand]")
			// --- Leg B: single-source channel at z=10 ending in a
			// dug sump at x=21 — the level-7 lip drops falling water
			// into the pit.
			for _, cmd := range []string{
				"fill 13 -60 9 23 -59 9 minecraft:stone",   // z=9 wall
				"fill 13 -60 11 23 -59 11 minecraft:stone", // z=11 wall
				"fill 13 -60 10 13 -59 10 minecraft:stone", // west cap
				"fill 22 -61 10 22 -59 10 minecraft:stone", // east cap (3 high)
				"setblock 21 -61 10 minecraft:air",         // dug sump cell
				"setblock 14 -60 10 minecraft:water",       // source
			} {
				sendCommand(t, pw, cmd)
			}
			sprintTicks(t, pw, sink, 40) // water reaches the sump
			summonStand(16.5, -59.5, 10.5)
			time.Sleep(400 * time.Millisecond)
			sprintTicks(t, pw, sink, 80)
			time.Sleep(300 * time.Millisecond)
			bx, by, bz, ok := dataGetPos(t, sink, pw, sel)
			if !ok {
				t.Fatalf("%s: leg B stand Pos unreadable", s.label)
			}
			res["B"] = legRes{bx, by, bz}
			sendCommand(t, pw, "kill @e[type=minecraft:armor_stand]")
			land[s.label] = res
			t.Logf("%s: A=%+v B=%+v", s.label, res["A"], res["B"])
		})
	}
	// Leg A: the stand drifts +x off its summon point and parks in
	// the convergence dead band around x=-2 on both legs.
	for _, c := range []struct {
		label string
		p     legRes
	}{{"vanilla", land["vanilla"]["A"]}, {"mcgo", land["mcgo"]["A"]}} {
		if c.p.x < -2.8 || c.p.x > -1.1 {
			t.Errorf("%s: leg A stand parked outside the dead band: x=%.3f",
				c.label, c.p.x)
		}
		if c.p.y < -60.2 || c.p.y > -59.7 {
			t.Errorf("%s: leg A stand off the lane floor: y=%.3f", c.label, c.p.y)
		}
	}
	if d := math.Abs(land["vanilla"]["A"].x - land["mcgo"]["A"].x); d > 0.8 {
		t.Errorf("leg A convergence divergence vanilla %.3f vs mc-go %.3f (>0.8)",
			land["vanilla"]["A"].x, land["mcgo"]["A"].x)
	}
	// Leg B: the stand is swept downstream and pinned at the sump
	// floor (x≈21.7, y≈-61) on both legs.
	for _, c := range []struct {
		label string
		p     legRes
	}{{"vanilla", land["vanilla"]["B"]}, {"mcgo", land["mcgo"]["B"]}} {
		if c.p.x < 21.2 || c.p.x > 21.9 {
			t.Errorf("%s: leg B stand missed the sump pin: x=%.3f", c.label, c.p.x)
		}
		if c.p.y < -61.3 || c.p.y > -60.7 {
			t.Errorf("%s: leg B stand off the sump floor: y=%.3f", c.label, c.p.y)
		}
	}
	if d := math.Abs(land["vanilla"]["B"].x - land["mcgo"]["B"].x); d > 0.3 {
		t.Errorf("leg B x divergence vanilla %.3f vs mc-go %.3f (>0.3)",
			land["vanilla"]["B"].x, land["mcgo"]["B"].x)
	}
	if d := math.Abs(land["vanilla"]["B"].y - land["mcgo"]["B"].y); d > 0.3 {
		t.Errorf("leg B y divergence vanilla %.3f vs mc-go %.3f (>0.3)",
			land["vanilla"]["B"].y, land["mcgo"]["B"].y)
	}
}
