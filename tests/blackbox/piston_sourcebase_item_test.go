package blackbox

import (
	"fmt"
	"math"
	"testing"
)

// Keeping the item's center above the block tests whether plate contact
// can isolate source-base motion from random closest-space ejection.
func TestPistonSourceBaseItemParity(t *testing.T) {
	const player = "BaseItemReader"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer targets.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s -2.0 160.0 3.0")
		sendCommand(t, pw, "tick freeze")
		motion := func(sel string) [3]float64 {
			parsed, ok := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
			values, listOK := parsed.([]any)
			if !ok || !listOK || len(values) != 3 {
				t.Fatal("missing item Motion")
			}
			var out [3]float64
			for i := range out {
				var valid bool
				out[i], valid = hrNum(values[i])
				if !valid {
					t.Fatal("invalid item Motion")
				}
			}
			return out
		}
		var out []float64
		for i, start := range []float64{.9, 1.05, 1.15, 1.3, 1.6, 2.8} {
			sendCommand(t, pw, "kill @e[type=item]")
			sendCommand(t, pw, "fill -3 159 -3 4 166 3 air")
			sendCommand(t, pw, "setblock 0 162 0 piston[facing=east]")
			sendCommand(t, pw, "setblock 0 162 -1 redstone_block")
			shulkerStepTicks(t, pw, sink, 3)
			tag := fmt.Sprintf("BaseItem%d", i)
			sel := "@e[type=item,tag=" + tag + ",limit=1]"
			sendCommand(t, pw, fmt.Sprintf(`summon item %.9f 162.900000000 0.500000000 {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:1b,Motion:[0d,0d,0d],PickupDelay:32767s,Age:0s}`, start, tag))
			warmed := false
			for warm := 0; warm < 3; warm++ {
				shulkerStepTicks(t, pw, sink, 1)
				state, age := dataGetSel(t, sink, pw, sel, "Age")
				if state == "query" && age >= 1 {
					warmed = true
					break
				}
			}
			x, y, z, ok := dataGetPos(t, sink, pw, sel)
			m := motion(sel)
			t.Logf("%s initial start%.2f pos%.9f,%.9f,%.9f motion%v warmed%t", addr, start, x, y, z, m, warmed)
			if !warmed || !ok || math.Abs(x-start) > 1e-7 || math.Abs(y-162.9) > 1e-7 || z != .5 || m != [3]float64{} {
				t.Fatalf("item center-air apparatus not stationary for depth%v", start)
			}
			sendCommand(t, pw, "setblock 0 162 -1 air")
			for phase := 1; phase <= 4; phase++ {
				shulkerStepTicks(t, pw, sink, 1)
				x, y, z, ok = dataGetPos(t, sink, pw, sel)
				m = motion(sel)
				if !ok {
					t.Fatal("missing item")
				}
				t.Logf("%s start%.2f phase%d pos%.9f,%.9f,%.9f motion%v", addr, start, phase, x, y, z, m)
				wantX := start
				if i < 2 && phase >= 2 {
					wantX = 1.145
				}
				if i >= 2 && i < 5 {
					wantX = 1.145
					if phase >= 2 {
						wantX = 1.125
					}
				}
				if math.Abs(x-wantX) > 1e-7 {
					t.Errorf("%s item%d phase%d X%v want%v", addr, i, phase, x, wantX)
				}
				randomEscape := i >= 2 && i < 5 && phase == 2
				if randomEscape {
					if m[0] > -.1*float64(float32(.98)) || m[0] < -.3*float64(float32(.98)) {
						t.Errorf("escape motion outside native bounds%v", m[0])
					}
				} else if m[0] != 0 {
					t.Errorf("unexpected longitudinal motion%v", m[0])
				}
				if math.Abs(y-162.9) > 1e-7 || z != .5 || m[1] != 0 || m[2] != 0 {
					t.Errorf("unexpected transverse motion at depth%v phase%d", start, phase)
				}
				if i == 5 && math.Abs(x-start) > 1e-7 {
					t.Errorf("far item moved%v", x)
				}
				out = append(out, x, y, z, m[0], m[1], m[2])
			}
			sendCommand(t, pw, "kill "+sel)
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	_ = run(targets.vanilla)
	_ = run(targets.mcgo)
}
