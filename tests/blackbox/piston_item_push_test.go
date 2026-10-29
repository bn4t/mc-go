package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// Exact frozen phases expose physical movement independently of the
// already-pinned moving-block collision shapes.
func TestPistonItemPushParity(t *testing.T) {
	const player = "PistonItemReader"
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer tg.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s -2 160 3")
		sendCommand(t, pw, "fill -3 155 -3 3 155 3 stone")
		var out []float64
		dirs := []struct {
			name string
			off  [3]int
		}{{"east", [3]int{1, 0, 0}}, {"west", [3]int{-1, 0, 0}}, {"up", [3]int{0, 1, 0}}, {"down", [3]int{0, -1, 0}}, {"south", [3]int{0, 0, 1}}, {"north", [3]int{0, 0, -1}}}
		for _, dir := range dirs {
			for _, tc := range []struct {
				name  string
				stone bool
			}{{"head", false}, {"stone", true}} {
				sendCommand(t, pw, "tick unfreeze")
				sendCommand(t, pw, "fill -3 159 -3 3 166 3 air")
				sendCommand(t, pw, fmt.Sprintf("setblock 0 162 0 piston[facing=%s]", dir.name))
				if tc.stone {
					sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d stone", dir.off[0], 162+dir.off[1], dir.off[2]))
				}
				name := tc.name + "_" + dir.name
				initial := [3]float64{.5, 162.375, .5}
				distance := 1.0
				if tc.stone {
					distance = 1.875
				}
				for i := 0; i < 3; i++ {
					initial[i] += float64(dir.off[i]) * distance
				}
				tag := "PistonItem_" + name
				sel := "@e[type=item,tag=" + tag + ",limit=1]"
				sendCommand(t, pw, fmt.Sprintf(`summon item %.5f %.5f %.5f {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:1b,Motion:[0d,0d,0d],PickupDelay:32767s,Age:0s}`, initial[0], initial[1], initial[2], tag))
				until := time.Now().Add(10 * time.Second)
				for {
					st, age := dataGetSel(t, sink, pw, sel, "Age")
					if st == "query" && age >= 3 {
						break
					}
					if time.Now().After(until) {
						t.Fatal("item not ticking")
					}
					time.Sleep(50 * time.Millisecond)
				}
				sendCommand(t, pw, "tick freeze")
				shulkerStepTicks(t, pw, sink, 1)
				sendCommand(t, pw, fmt.Sprintf("tp %s %.5f %.5f %.5f", sel, initial[0], initial[1], initial[2]))
				sendCommand(t, pw, "data merge entity "+sel+" {Motion:[0d,0d,0d]}")
				x, y, z, ok := dataGetPos(t, sink, pw, sel)
				if !ok || math.Abs(x-initial[0]) > 1e-7 || math.Abs(y-initial[1]) > 1e-7 || math.Abs(z-initial[2]) > 1e-7 {
					t.Fatalf("%s %s initial %v %v %v ok%t", addr, tc.name, x, y, z, ok)
				}
				power := [3]int{0, 162, -1}
				if dir.off[2] != 0 {
					power = [3]int{-1, 162, 0}
				}
				sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d redstone_block", power[0], power[1], power[2]))
				for phase := 1; phase <= 4; phase++ {
					shulkerStepTicks(t, pw, sink, 1)
					x, y, z, ok = dataGetPos(t, sink, pw, sel)
					if !ok {
						t.Fatal("missing item position")
					}
					parsed, parsedOK := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
					motionValues, listOK := parsed.([]any)
					if !parsedOK || !listOK || len(motionValues) != 3 {
						t.Fatal("missing item motion")
					}
					var motion [3]float64
					for i := range motion {
						var valid bool
						motion[i], valid = hrNum(motionValues[i])
						if !valid {
							t.Fatal("invalid item motion")
						}
					}
					delta := .135
					if tc.stone {
						delta = .26
					}
					if phase >= 2 {
						delta += .5
					}
					want := initial
					for i := range want {
						want[i] += float64(dir.off[i]) * delta
					}
					if math.Abs(x-want[0]) > 1e-6 || math.Abs(y-want[1]) > 1e-6 || math.Abs(z-want[2]) > 1e-6 || motion != [3]float64{} {
						t.Errorf("%s %s phase%d pos%v,%v,%v motion%v want%v", addr, name, phase, x, y, z, motion, want)
					}
					t.Logf("%s %s phase%d pos%.9f,%.9f,%.9f motion%v", addr, name, phase, x, y, z, motion)
					out = append(out, x, y, z, motion[0], motion[1], motion[2])
				}
				sendCommand(t, pw, "kill "+sel)
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(tg.vanilla), run(tg.mcgo)
	for i := range v {
		if math.Abs(v[i]-g[i]) > 1e-6 {
			t.Errorf("observation%d vanilla%v mcgo%v", i, v[i], g[i])
		}
	}
}
