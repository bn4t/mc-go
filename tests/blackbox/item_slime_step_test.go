package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestItemSlimeStepParity(t *testing.T) {
	const actor = "SlimeStepRead"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		command := func(cmd string) []string {
			serial++
			receipt := fmt.Sprintf("SLIME_STEP_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return waitForExactSayBB(t, sink, receipt, 30*time.Second)
		}
		command("gamemode spectator @s")
		command("tp @s 10.0 165.0 10.0")
		command("tick freeze")
		for _, noGravity := range []bool{false, true} {
			for i, rawY := range []float64{-.03, -.11, -.12, -.13, -.2} {
				command("kill @e[type=item]")
				command("fill -2 162 -2 3 164 3 air")
				command("fill -2 162 -2 3 162 3 slime_block")
				witness := fmt.Sprintf("SLIME_STEP_SURFACE_%t_%d", noGravity, i)
				if lines := command("execute if block 0 162 0 slime_block run say " + witness); !structureSaidBB(lines, witness) {
					t.Fatal("missing slime surface")
				}
				gravity, flag := .04, 0
				if noGravity {
					gravity, flag = 0, 1
				}
				tag := fmt.Sprintf("SlimeStep%t%d", noGravity, i)
				sel := "@e[type=item,tag=" + tag + ",limit=1]"
				command(fmt.Sprintf(`summon item 0.5 163.0 0.5 {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:%db,Motion:[.1d,%.17gd,0d],Age:0s,OnGround:0b,PickupDelay:32767s}`, tag, flag, rawY+gravity))
				age := float64(0)
				for tick := 0; tick < 3 && age == 0; tick++ {
					shulkerStepTicks(t, pw, sink, 1)
					state, value := dataGetSel(t, sink, pw, sel, "Age")
					if state != "query" {
						t.Fatal("missing item Age")
					}
					age = value
				}
				if age != 1 {
					t.Fatalf("first item age%v want1", age)
				}
				x, y, z, ok := dataGetPos(t, sink, pw, sel)
				if !ok {
					t.Fatal("missing item position")
				}
				parsed, valid := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
				values, list := parsed.([]any)
				if !valid || !list || len(values) != 3 {
					t.Fatal("missing item Motion")
				}
				var motion [3]float64
				for axis := range motion {
					var valid bool
					motion[axis], valid = hrNum(values[axis])
					if !valid {
						t.Fatal("invalid item Motion")
					}
				}
				bounceY := float64(0)
				if -rawY >= gravity {
					bounceY = -rawY * float64(float32(.8))
				}
				walk := float64(1)
				if bounceY < .1 {
					walk = .4 + bounceY*.2
				}
				friction := float64(float32(float32(.98) * float32(.8)))
				want := [3]float64{.1 * walk * friction, bounceY * float64(float32(.98)), 0}
				t.Logf("%s NoGravity%t rawY%v firstPos%v,%v,%v Motion%v want%v", addr, noGravity, rawY, x, y, z, motion, want)
				if math.Abs(x-.6) > 1e-10 || y != 163 || z != .5 {
					t.Errorf("%s item position%v,%v,%v want.6,163,.5", addr, x, y, z)
				}
				for axis := range motion {
					if math.Abs(motion[axis]-want[axis]) > 1e-11 {
						t.Errorf("%s NoGravity%t rawY%v Motion[%d]%v want%v", addr, noGravity, rawY, axis, motion[axis], want[axis])
					}
				}
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
