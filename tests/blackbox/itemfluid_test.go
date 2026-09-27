package blackbox

import (
	"math"
	"testing"
	"time"
)

// TestItemFluidParity: an item submerged deeper than 0.1 switches off
// gravity and runs the fluid formula — xz damped 0.95 and y lifted
// +0.0005 while below 0.06 — rising until the surface bobs it around
// the submersion boundary. Measured on the 26.2 jar: ItemEntity.tick
// calls setUnderwaterMovement/setUnderLavaMovement (both
// setFluidMovement(0.95)) when isInWater/isInLava with
// getFluidHeight > 0.1, else applyGravity; Entity.tick runs
// updateFluidInteraction (the current push) and ItemEntity.tick calls
// it again at the tail OR-ing needsSync with in-fluid. Still source
// water carries no flow, so the pin is the rise: a settled item under
// a fresh water column climbs to the surface on both servers.
func TestItemFluidParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("IF")}, nil)
	defer targets.cleanup()

	type pos struct{ x, y, z float64 }
	land := map[string]pos{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "IF")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			// A walled 1x1 pool over the floor: stone ring four
			// blocks tall at x/z=9..11, water column above the item
			// cell keeps the flow inside.
			for _, cmd := range []string{
				"fill 9 -60 9 11 -56 9 minecraft:stone",
				"fill 9 -60 11 11 -56 11 minecraft:stone",
				"fill 9 -60 9 9 -56 11 minecraft:stone",
				"fill 11 -60 9 11 -56 11 minecraft:stone",
			} {
				sendCommand(t, pw, cmd)
			}
			// Summon on the dry floor and let the random spawn kick
			// settle before the water arrives — verify the entity
			// exists first (vanilla drops summons at a low rate).
			cmd := `summon minecraft:item 10.5 -60 10.5 ` +
				`{Item:{id:"minecraft:stone",count:1}}`
			for attempt := 0; attempt < 5; attempt++ {
				sendCommand(t, pw, cmd)
				time.Sleep(300 * time.Millisecond)
				line := dataGetLine(t, sink, pw,
					"@e[type=minecraft:item,limit=1,sort=nearest]", "Pos")
				if _, ok := parseSNBT(line); ok {
					break
				}
				if attempt == 4 {
					t.Fatalf("%s: item summon never landed", s.label)
				}
			}
			time.Sleep(2500 * time.Millisecond)
			sendCommand(t, pw, "fill 10 -60 10 10 -57 10 minecraft:water")
			// The rise is slow (+0.0005/tick to the 0.06 cap) — sprint
			// past it; sprint ticks mutate Pos server-side on both.
			time.Sleep(300 * time.Millisecond)
			sendCommand(t, pw, "tick sprint 300")
			time.Sleep(1500 * time.Millisecond)
			line := dataGetLine(t, sink, pw,
				"@e[type=minecraft:item,limit=1,sort=nearest]", "Pos")
			pl, ok := parseSNBT(line)
			if !ok {
				t.Fatalf("%s: item Pos unreadable: %q", s.label, line)
			}
			l, ok := pl.([]any)
			if !ok || len(l) != 3 {
				t.Fatalf("%s: item Pos shape: %v", s.label, pl)
			}
			var p pos
			for i, v := range l {
				n, _ := hrNum(v)
				switch i {
				case 0:
					p.x = n
				case 1:
					p.y = n
				case 2:
					p.z = n
				}
			}
			land[s.label] = p
		})
	}
	v, m := land["vanilla"], land["mcgo"]
	// The item bobs at the water surface: feet in the -56.x band on
	// both servers (water top cell surface is -56; the equilibrium
	// sits just under it, spread by how much downward momentum each
	// leg carried through the boundary — ~0.3 of bob). The still-water
	// pin is the height, not a correlation — assert each leg in the
	// band and no sideways drift out of the cell.
	for _, c := range []struct {
		label string
		p     pos
	}{{"vanilla", v}, {"mcgo", m}} {
		if c.p.y < -57.3 || c.p.y > -55.2 {
			t.Errorf("%s: item y %.3f outside surface band [-57.3,-55.2] (pos %v)",
				c.label, c.p.y, c.p)
		}
		if math.Abs(c.p.x-10.5) > 0.6 || math.Abs(c.p.z-10.5) > 0.6 {
			t.Errorf("%s: item drifted out of the pool cell: %v", c.label, c.p)
		}
	}
	if d := math.Abs(v.y - m.y); d > 0.5 {
		t.Errorf("y divergence vanilla %.3f vs mc-go %.3f (>0.5)", v.y, m.y)
	}
}
