package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestItemClosestSpaceShapeParity(t *testing.T) {
	const player = "ItemShapeReader"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer targets.cleanup()
	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 3.0 165.0 3.0")
		sendCommand(t, pw, "tick freeze")
		dirs := []struct {
			name       string
			axis       int
			sign       float64
			dx, dy, dz int
		}{{"north", 2, -1, 0, 0, -1}, {"south", 2, 1, 0, 0, 1}, {"west", 0, -1, -1, 0, 0}, {"east", 0, 1, 1, 0, 0}, {"up", 1, 1, 0, 1, 0}}
		serial := 0
		candidate := func(x, y, z int, state, phase string) {
			serial++
			witness := fmt.Sprintf("ESCAPE_STATE_%d", serial)
			receipt := fmt.Sprintf("ESCAPE_RECEIPT_%d", serial)
			sendCommand(t, pw, fmt.Sprintf("execute if block %d %d %d %s run say %s", x, y, z, state, witness))
			sendCommand(t, pw, "say "+receipt)
			lines := waitForExactSayBB(t, sink, receipt, 30*time.Second)
			if !structureSaidBB(lines, witness) {
				t.Fatalf("%s candidate at %d,%d,%d must remain %s %s; replies %v", addr, x, y, z, state, phase, lines)
			}
		}
		var out []string
		for _, shape := range []struct {
			name, state string
			full        bool
		}{
			{"air", "air", false}, {"bottom_slab", "oak_slab[type=bottom]", false}, {"top_slab", "oak_slab[type=top]", false}, {"double_slab", "oak_slab[type=double]", true}, {"stairs", "oak_stairs", false}, {"glass", "glass", true}, {"farmland", "farmland[moisture=7]", false}, {"trapdoor", "oak_trapdoor", false}, {"fence", "oak_fence", false},
		} {
			noGravity := true
			for i, dir := range dirs {
				escape := dir
				if shape.full {
					escape = dirs[4]
				}
				sendCommand(t, pw, "kill @e[type=item]")
				sendCommand(t, pw, "fill -1 161 -1 1 163 1 stone")
				// Farmland must have clear space above; covered farmland becomes dirt.
				if shape.name == "farmland" {
					sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d air", dir.dx, 163+dir.dy, dir.dz))
				}
				sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d %s", dir.dx, 162+dir.dy, dir.dz, shape.state))
				candidate(dir.dx, 162+dir.dy, dir.dz, shape.state, "before item tick")
				tag := fmt.Sprintf("ItemEscape_%t_%d", noGravity, i)
				sel := "@e[type=item,tag=" + tag + ",limit=1]"
				gravity := 0
				if noGravity {
					gravity = 1
				}
				sendCommand(t, pw, fmt.Sprintf(`summon item 0.250000000 162.300000000 0.600000000 {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:%db,Motion:[0d,0d,0d],PickupDelay:32767s,Age:0s,OnGround:0b}`, tag, gravity))
				age := float64(0)
				for tick := 0; tick < 3 && age == 0; tick++ {
					shulkerStepTicks(t, pw, sink, 1)
					state, value := dataGetSel(t, sink, pw, sel, "Age")
					if state != "query" {
						t.Fatal("missing item age")
					}
					age = value
				}
				if age != 1 {
					t.Fatalf("%s first age%v want1", addr, age)
				}
				stateAfter := shape.state
				if shape.name == "farmland" {
					stateAfter = "farmland"
				}
				candidate(dir.dx, 162+dir.dy, dir.dz, stateAfter, "after item tick")
				x, y, z, ok := dataGetPos(t, sink, pw, sel)
				if !ok {
					t.Fatal("missing escaped item")
				}
				parsed, valid := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
				values, list := parsed.([]any)
				if !valid || !list || len(values) != 3 {
					t.Fatal("missing escape Motion")
				}
				var motion [3]float64
				for axis := range motion {
					var good bool
					motion[axis], good = hrNum(values[axis])
					if !good {
						t.Fatal("invalid escape Motion")
					}
				}
				delta := [3]float64{x - .25, y - 162.3, z - .6}
				raw := delta
				if !noGravity && escape.axis != 1 {
					raw[1] += .03
				}
				speed := escape.sign * raw[escape.axis]
				bounds := speed >= .1-1e-7 && speed <= .3+1e-7
				direction := true
				drag := true
				for axis := range raw {
					if axis != escape.axis && math.Abs(raw[axis]) > 1e-7 {
						direction = false
					}
					if math.Abs(motion[axis]-delta[axis]*float64(float32(.98))) > 1e-10 {
						drag = false
					}
				}
				t.Logf("%s %s noGravity%t delta%v Motion%v speed%.12f", addr, shape.name+"/"+dir.name, noGravity, delta, motion, speed)
				if !bounds || !direction || !drag {
					t.Errorf("%s %s noGravity%t bounds%t direction%t drag%t", addr, shape.name+"/"+dir.name, noGravity, bounds, direction, drag)
				}
				out = append(out, fmt.Sprintf("%s:%t:bounds%t:direction%t:drag%t", shape.name+"/"+dir.name, noGravity, bounds, direction, drag))
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(targets.vanilla), run(targets.mcgo)
	if fmt.Sprint(v) != fmt.Sprint(g) {
		t.Errorf("escape observations differ vanilla%v mcgo%v", v, g)
	}
}
