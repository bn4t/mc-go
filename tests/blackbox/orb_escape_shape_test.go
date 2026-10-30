package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// A clear escape corridor isolates collision-shape eligibility from clipping.
func TestOrbEscapeShapeParity(t *testing.T) {
	const actor = "OrbShapeRead"
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
			receipt := fmt.Sprintf("ORB_SHAPE_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return waitForExactSayBB(t, sink, receipt, 30*time.Second)
		}
		command("gamemode spectator @s")
		command("tp @s 10.0 165.0 10.0")
		command("tick freeze")
		for _, shape := range []struct{ name, state string }{{"air", "air"}, {"bottom_slab", "oak_slab[type=bottom]"}, {"top_slab", "oak_slab[type=top]"}, {"stairs", "oak_stairs"}, {"trapdoor", "oak_trapdoor"}, {"fence", "oak_fence"}, {"glass", "glass"}, {"double_slab", "oak_slab[type=double]"}} {
			for _, dir := range []struct {
				name   string
				dx, dz int
				x, z   float64
			}{{"north", 0, -1, .1, .1}, {"south", 0, 1, .1, .9}, {"west", -1, 0, .1, .1}, {"east", 1, 0, .9, .1}} {
				command("kill @e[type=experience_orb]")
				command("fill -1 161 -1 1 163 1 stone")
				command("fill -1 163 -1 1 163 1 air")
				if dir.dx == 0 {
					command(fmt.Sprintf("setblock -1 162 %d air", dir.dz))
					command(fmt.Sprintf("setblock 1 162 %d air", dir.dz))
				} else {
					command(fmt.Sprintf("setblock %d 162 -1 air", dir.dx))
					command(fmt.Sprintf("setblock %d 162 1 air", dir.dx))
				}
				command(fmt.Sprintf("setblock %d 162 %d %s", dir.dx, dir.dz, shape.state))
				witness := fmt.Sprintf("ORB_SHAPE_STATE_%s_%s", shape.name, dir.name)
				if lines := command(fmt.Sprintf("execute if block %d 162 %d %s run say %s", dir.dx, dir.dz, shape.state, witness)); !structureSaidBB(lines, witness) {
					t.Fatal("missing candidate shape")
				}
				tag := fmt.Sprintf("OrbShape%s%s", shape.name, dir.name)
				sel := "@e[type=experience_orb,tag=" + tag + ",limit=1]"
				command(fmt.Sprintf(`summon experience_orb %.17g 162.3 %.17g {Tags:["%s"],Value:1s,NoGravity:1b,Motion:[0d,0d,0d],Age:0s}`, dir.x, dir.z, tag))
				age := float64(0)
				for tick := 0; tick < 3 && age == 0; tick++ {
					shulkerStepTicks(t, pw, sink, 1)
					state, value := dataGetSel(t, sink, pw, sel, "Age")
					if state != "query" {
						t.Fatal("missing orb Age")
					}
					age = value
				}
				if age != 1 {
					t.Fatalf("first Age%v want1", age)
				}
				x, y, z, ok := dataGetPos(t, sink, pw, sel)
				if !ok {
					t.Fatal("missing orb position")
				}
				parsed, valid := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
				values, list := parsed.([]any)
				if !valid || !list || len(values) != 3 {
					t.Fatal("missing orb Motion")
				}
				var motion [3]float64
				for axis := range motion {
					var valid bool
					motion[axis], valid = hrNum(values[axis])
					if !valid {
						t.Fatal("invalid orb Motion")
					}
				}
				delta := [3]float64{x - dir.x, y - 162.3, z - dir.z}
				axis, sign := 0, float64(dir.dx)
				if dir.dz != 0 {
					axis, sign = 2, float64(dir.dz)
				}
				if shape.name == "glass" || shape.name == "double_slab" {
					axis, sign = 1, 1
				}
				speed := sign * delta[axis]
				if speed < .1-1e-7 || speed > .3+1e-7 {
					t.Errorf("%s %s/%s speed%v outside [.1,.3]", addr, shape.name, dir.name, speed)
				}
				for component := range delta {
					if component != axis && math.Abs(delta[component]) > 1e-10 {
						t.Errorf("%s %s/%s off-axis delta%v", addr, shape.name, dir.name, delta)
					}
					if math.Abs(motion[component]-delta[component]*float64(float32(.98))) > 1e-10 {
						t.Errorf("%s %s/%s Motion%v does not match delta%v with drag", addr, shape.name, dir.name, motion, delta)
					}
				}
				t.Logf("%s shape=%s candidate=%s delta=%v Motion=%v", addr, shape.name, dir.name, delta, motion)
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
