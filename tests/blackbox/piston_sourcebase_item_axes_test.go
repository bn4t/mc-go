package blackbox

import (
	"fmt"
	"math"
	"testing"
)

// Keeping the item's center above the block tests whether plate contact
// can isolate source-base motion from random closest-space ejection.
func TestPistonSourceBaseItemAxesParity(t *testing.T) {
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
		for _, dir := range []struct {
			name string
			axis int
			sign float64
		}{{"east", 0, 1}, {"west", 0, -1}, {"up", 1, 1}, {"down", 1, -1}, {"south", 2, 1}, {"north", 2, -1}} {
			for i, start := range []float64{.9, 1.3, 2.8} {
				initial := [3]float64{.5, 162.9, .5}
				local := start
				if dir.sign < 0 {
					local = 1 - start
				}
				initial[dir.axis] = local
				if dir.axis == 1 {
					initial[0] = .9
					initial[1] = 162 + local - .125
				}
				power := [3]int{0, 162, -1}
				if dir.axis == 2 {
					power = [3]int{-1, 162, 0}
				}
				sendCommand(t, pw, "kill @e[type=item]")
				sendCommand(t, pw, "fill -3 159 -3 4 166 3 air")
				sendCommand(t, pw, fmt.Sprintf("setblock 0 162 0 piston[facing=%s]", dir.name))
				sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d redstone_block", power[0], power[1], power[2]))
				shulkerStepTicks(t, pw, sink, 3)
				tag := fmt.Sprintf("BaseItem%s%d", dir.name, i)
				sel := "@e[type=item,tag=" + tag + ",limit=1]"
				sendCommand(t, pw, fmt.Sprintf(`summon item %.9f %.9f %.9f {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:1b,Motion:[0d,0d,0d],PickupDelay:32767s,Age:0s}`, initial[0], initial[1], initial[2], tag))
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
				t.Logf("%s initial start%.2f pos%.9f,%.9f,%.9f motion%v warmed%t", addr+" "+dir.name, start, x, y, z, m, warmed)
				if !warmed || !ok || math.Abs(x-initial[0]) > 1e-7 || math.Abs(y-initial[1]) > 1e-7 || math.Abs(z-initial[2]) > 1e-7 || m != [3]float64{} {
					t.Fatalf("item center-air apparatus not stationary for depth%v", start)
				}
				sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d air", power[0], power[1], power[2]))
				previousPos := initial
				previousMotion := [3]float64{}
				for phase := 1; phase <= 4; phase++ {
					shulkerStepTicks(t, pw, sink, 1)
					x, y, z, ok = dataGetPos(t, sink, pw, sel)
					m = motion(sel)
					if !ok {
						t.Fatal("missing item")
					}
					t.Logf("%s start%.2f phase%d pos%.9f,%.9f,%.9f motion%v", addr+" "+dir.name, start, phase, x, y, z, m)
					pos := [3]float64{x, y, z}
					wantPos := initial
					center := start
					if i == 0 && phase >= 2 || i == 1 {
						center = 1.145
					}
					if i == 1 && phase >= 2 && dir.axis != 1 {
						center = 1.125
					}
					if dir.sign < 0 {
						center = 1 - center
					}
					if dir.axis == 1 {
						center += 162 - .125
					}
					wantPos[dir.axis] = center
					wantMotion := [3]float64{}
					drag := float64(float32(.98))
					if i == 1 && phase >= 2 && dir.axis == 1 {
						if phase == 2 {
							impulse := m[0] / drag
							if impulse < .1 || impulse > .3 {
								t.Errorf("vertical escape impulse%v", impulse)
							}
							wantPos[0] = initial[0] + impulse
							wantMotion[0] = m[0]
						} else {
							wantPos[0] = previousPos[0] + previousMotion[0]
							wantMotion[0] = previousMotion[0] * drag
						}
					} else if i == 1 && phase == 2 {
						impulse := -dir.sign * m[dir.axis] / drag
						if impulse < .1 || impulse > .3 {
							t.Errorf("horizontal escape impulse%v", impulse)
						}
						wantMotion[dir.axis] = m[dir.axis]
					}
					for axis := range pos {
						if math.Abs(pos[axis]-wantPos[axis]) > 1e-7 || math.Abs(m[axis]-wantMotion[axis]) > 1e-12 {
							t.Errorf("%s %s offset%v phase%d axis%d pos%v want%v motion%v want%v", addr, dir.name, start, phase, axis, pos[axis], wantPos[axis], m[axis], wantMotion[axis])
						}
					}
					previousPos, previousMotion = pos, m
					out = append(out, x, y, z, m[0], m[1], m[2])
				}
				sendCommand(t, pw, "kill "+sel)
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	_ = run(targets.vanilla)
	_ = run(targets.mcgo)
}
