package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// Each forced exit pins first-tick displacement, impulse bounds and drag.
func TestEmbeddedOrbMovementParity(t *testing.T) {
	const actor = "OrbEmbedRead"
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
			receipt := fmt.Sprintf("ORB_EMBED_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return waitForExactSayBB(t, sink, receipt, 30*time.Second)
		}
		command("gamemode spectator @s")
		command("tp @s 10.0 165.0 10.0")
		command("tick freeze")
		for _, noGravity := range []bool{false, true} {
			for _, dir := range []struct {
				name       string
				dx, dy, dz int
			}{{"north", 0, 0, -1}, {"south", 0, 0, 1}, {"west", -1, 0, 0}, {"east", 1, 0, 0}, {"up", 0, 1, 0}} {
				command("kill @e[type=experience_orb]")
				command("fill -1 161 -1 1 163 1 stone")
				command(fmt.Sprintf("setblock %d %d %d air", dir.dx, 162+dir.dy, dir.dz))
				witness := fmt.Sprintf("ORB_EMBED_ROOM_%t_%s", noGravity, dir.name)
				if lines := command(fmt.Sprintf("execute if block 0 162 0 stone if block %d %d %d air run say %s", dir.dx, 162+dir.dy, dir.dz, witness)); !structureSaidBB(lines, witness) {
					t.Fatal("missing embedded room witness")
				}
				flag := 0
				if noGravity {
					flag = 1
				}
				tag := fmt.Sprintf("OrbEmbed%t%s", noGravity, dir.name)
				sel := "@e[type=experience_orb,tag=" + tag + ",limit=1]"
				command(fmt.Sprintf(`summon experience_orb .25 162.3 .6 {Tags:["%s"],Value:1s,NoGravity:%db,Motion:[0d,0d,0d],Age:0s}`, tag, flag))
				age := float64(0)
				for tick := 0; tick < 3 && age == 0; tick++ {
					shulkerStepTicks(t, pw, sink, 1)
					state, value := dataGetSel(t, sink, pw, sel, "Age")
					if state != "query" {
						t.Fatal("missing orb age")
					}
					age = value
				}
				if age != 1 {
					t.Fatalf("first orb age%v want1", age)
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
				delta := [3]float64{x - .25, y - 162.3, z - .6}
				axis, sign := 0, float64(dir.dx)
				if dir.dy != 0 {
					axis, sign = 1, float64(dir.dy)
				} else if dir.dz != 0 {
					axis, sign = 2, float64(dir.dz)
				}
				speed := sign * delta[axis]
				if speed < .1-1e-7 || speed > .3+1e-7 {
					t.Errorf("%s NoGravity=%t exit=%s escape speed %v outside [.1,.3]", addr, noGravity, dir.name, speed)
				}
				for component := range delta {
					if component != axis && math.Abs(delta[component]) > 1e-10 {
						t.Errorf("%s NoGravity=%t exit=%s off-axis displacement %v", addr, noGravity, dir.name, delta)
					}
					if math.Abs(motion[component]-delta[component]*float64(float32(.98))) > 1e-10 {
						t.Errorf("%s NoGravity=%t exit=%s Motion[%d]=%v does not match displacement %v with drag", addr, noGravity, dir.name, component, motion[component], delta[component])
					}
				}
				t.Logf("%s NoGravity=%t exit=%s delta=%v Motion=%v", addr, noGravity, dir.name, delta, motion)
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
