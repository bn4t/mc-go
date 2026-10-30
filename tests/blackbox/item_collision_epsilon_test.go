package blackbox

import (
	"fmt"
	"math"
	"testing"
)

func TestItemCollisionEpsilonParity(t *testing.T) {
	const actor = "ItemFaceReader"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s -2.0 160.0 3.0")
		sendCommand(t, pw, "tick freeze")
		for _, dir := range []struct {
			name string
			axis int
			sign float64
		}{{"east", 0, 1}, {"west", 0, -1}, {"up", 1, 1}, {"down", 1, -1}, {"south", 2, 1}, {"north", 2, -1}} {
			for _, movement := range []struct {
				speed          float64
				parallel, wall bool
			}{{.2, false, true}, {.2, true, true}, {.0002, false, true}, {.0002, false, false}} {
				parallel, speed := movement.parallel, movement.speed
				if parallel && dir.axis == 1 {
					continue
				}
				for i, overlap := range []float64{0, -5e-8, 5e-8} {
					if !movement.wall && overlap != 0 {
						continue
					}
					sendCommand(t, pw, "kill @e[type=item]")
					sendCommand(t, pw, "fill -3 159 -3 3 166 3 air")
					if movement.wall {
						sendCommand(t, pw, "setblock 0 162 0 stone")
					}
					initial := [3]float64{.5, 162.3, .5}
					target := -.125
					if dir.sign < 0 {
						target = 1.125
					}
					if dir.axis == 1 {
						target = 161.75
						if dir.sign < 0 {
							target = 163
						}
					}
					initial[dir.axis] = target + dir.sign*overlap
					motion := [3]float64{}
					motion[dir.axis] = dir.sign * speed
					parallelAxis := 2
					if dir.axis == 2 {
						parallelAxis = 0
					}
					if parallel {
						motion[parallelAxis] = .05
					}
					tag := fmt.Sprintf("ItemFace%s%d%t%t%v", dir.name, i, parallel, movement.wall, speed)
					sel := "@e[type=item,tag=" + tag + ",limit=1]"
					sendCommand(t, pw, fmt.Sprintf(`summon item %.17g %.17g %.17g {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:1b,Motion:[%.17gd,%.17gd,%.17gd],PickupDelay:32767s,Age:0s,OnGround:0b}`, initial[0], initial[1], initial[2], tag, motion[0], motion[1], motion[2]))
					age := float64(0)
					for tick := 0; tick < 3 && age == 0; tick++ {
						shulkerStepTicks(t, pw, sink, 1)
						state, n := dataGetSel(t, sink, pw, sel, "Age")
						if state != "query" {
							t.Fatal("missing age")
						}
						age = n
					}
					if age != 1 {
						t.Fatalf("age%v", age)
					}
					x, y, z, ok := dataGetPos(t, sink, pw, sel)
					if !ok {
						t.Fatal("missing item")
					}
					pos := [3]float64{x, y, z}
					parsed, ok := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
					list, isList := parsed.([]any)
					if !ok || !isList || len(list) != 3 {
						t.Fatal("missing motion")
					}
					var gotMotion [3]float64
					for axis := range gotMotion {
						n, valid := hrNum(list[axis])
						if !valid {
							t.Fatal("invalid motion")
						}
						gotMotion[axis] = n
					}
					want := initial
					wantMotion := [3]float64{}
					if movement.wall && (parallel || speed*speed < 1e-7) {
						want[dir.axis] = target
					}
					if !movement.wall {
						want[dir.axis] += dir.sign * speed
						wantMotion[dir.axis] = dir.sign * speed * float64(float32(.98))
					}
					if parallel {
						want[dir.axis] = target
						want[parallelAxis] += .05
						wantMotion[parallelAxis] = .05 * float64(float32(.98))
					}
					t.Logf("%s %s speed%v wall%t parallel%t overlap%.12g pos%v Motion%v", addr, dir.name, speed, movement.wall, parallel, overlap, pos, gotMotion)
					for axis := range pos {
						if math.Abs(pos[axis]-want[axis]) > 1e-10 || math.Abs(gotMotion[axis]-wantMotion[axis]) > 1e-12 {
							t.Errorf("%s %s overlap%v axis%d pos%v want%v Motion%v", addr, dir.name, overlap, axis, pos[axis], want[axis], gotMotion[axis])
						}
					}
				}
			}
		}
		sendCommand(t, pw, "tick unfreeze")
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
