package blackbox

import (
	"math"
	"testing"
	"time"
)

// TestMobLavaTravelParity: LivingEntity.travel's lava branch —
// travelInLava's moveRelative(0.02) drive, the isInShallowFluid(LAVA)
// split (depth ≤ jump threshold damps {0.5,0.8,0.5}+fall adjust, deep
// scales 0.5), effectiveGravity at d/4 (four times water's d/16), and
// the unconditional jumpOutOfFluid(y0) tail that both fluid branches
// share.
//
// The headline parity point: wither_skeleton registers no FloatGoal
// (measured off the jar — skeletons, zombies and piglins omit it),
// so nothing fires the aiStep jump arms and the mob sinks through
// 4-deep lava and sits at the bed instead of bobbing the surface.
// It's also fire-immune, so lava's 4-per-tick inside-effect leaves
// its health untouched — a pig would die inside three ticks.
// Measured on the 26.2 jar: reads at ~200t and ~320t both land the
// skeleton's feet at exactly -60.0 on the pool bed.
func TestMobLavaTravelParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("LT")}, nil)
	defer targets.cleanup()

	type pos struct{ x, y, z float64 }
	mid := map[string]pos{}
	bed := map[string]pos{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "LT")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamerule do_mob_spawning false")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			for _, cmd := range []string{
				"fill 9 -61 9 11 -56 9 minecraft:stone",
				"fill 9 -61 11 11 -56 11 minecraft:stone",
				"fill 9 -61 9 9 -56 11 minecraft:stone",
				"fill 11 -61 9 11 -56 11 minecraft:stone",
				// 4-deep lava column: cells -60..-57.
				"fill 10 -60 10 10 -57 10 minecraft:lava",
			} {
				sendCommand(t, pw, cmd)
			}
			cmd := `summon minecraft:wither_skeleton 10.5 -57 10.5`
			for attempt := 0; attempt < 5; attempt++ {
				sendCommand(t, pw, cmd)
				time.Sleep(300 * time.Millisecond)
				if d := dataGetAll(t, sink, pw,
					"@e[type=minecraft:wither_skeleton,limit=1,sort=nearest]"); d != "" {
					break
				}
				if attempt == 4 {
					t.Fatalf("%s: wither skeleton summon never landed", s.label)
				}
			}
			readPos := func() (pos, bool) {
				line := dataGetLine(t, sink, pw,
					"@e[type=minecraft:wither_skeleton,limit=1,sort=nearest]", "Pos")
				pl, ok := parseSNBT(line)
				if !ok {
					return pos{}, false
				}
				l, _ := pl.([]any)
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
				return p, true
			}
			sendCommand(t, pw, "tick sprint 200")
			time.Sleep(1500 * time.Millisecond)
			p, ok := readPos()
			if !ok {
				t.Fatalf("%s: wither skeleton gone before mid-read", s.label)
			}
			mid[s.label] = p
			sendCommand(t, pw, "tick sprint 120")
			time.Sleep(1200 * time.Millisecond)
			p2, ok2 := readPos()
			if !ok2 {
				t.Fatalf("%s: wither skeleton gone before final read", s.label)
			}
			bed[s.label] = p2
			// Fire immunity: lava's inside-effect does nothing, so
			// Health stays at the spawn base on both servers.
			if d := dataGetAll(t, sink, pw,
				"@e[type=minecraft:wither_skeleton,limit=1,sort=nearest]"); d != "" {
				if hp, _ := snbtFloat(t, d, "Health"); hp < 19.5 {
					t.Errorf("%s: wither skeleton took fire damage in lava: Health %.1f", s.label, hp)
				}
			}
		})
	}
	// The skeleton rests on the pool bed: floor cell -61 tops at
	// -60.0, and vanilla reads -60.000 on both snapshots — allow a
	// ±0.35 band for a hop caught mid-flight on either server.
	for _, c := range []struct {
		label string
		m, b  pos
	}{{"vanilla", mid["vanilla"], bed["vanilla"]},
		{"mcgo", mid["mcgo"], bed["mcgo"]}} {
		for tag, p := range map[string]pos{"mid": c.m, "bed": c.b} {
			if p.y < -60.35 || p.y > -59.3 {
				t.Errorf("%s %s: wither skeleton y %.3f outside bed band [-60.35,-59.3] (pos %v)",
					c.label, tag, p.y, p)
			}
			if math.Abs(p.x-10.5) > 0.9 || math.Abs(p.z-10.5) > 0.9 {
				t.Errorf("%s %s: drifted out of the pool cell: %v", c.label, tag, p)
			}
		}
	}
	if d := math.Abs(mid["vanilla"].y - mid["mcgo"].y); d > 0.5 {
		t.Errorf("mid y divergence vanilla %.3f vs mc-go %.3f (>0.5)",
			mid["vanilla"].y, mid["mcgo"].y)
	}
}
