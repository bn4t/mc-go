package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

// Collision overlap, including grazing faces, controls whether an orb escapes.
func TestOrbInsideCollisionShapeParity(t *testing.T) {
	const actor = "OrbInsideShape"
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
			receipt := fmt.Sprintf("ORB_INSIDE_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return waitForExactSayBB(t, sink, receipt, 30*time.Second)
		}
		command("gamemode spectator @s")
		command("tp @s 10.0 165.0 10.0")
		command("tick freeze")
		for _, noGravity := range []bool{false, true} {
			for _, tc := range []struct {
				name, state string
				x, y, z     float64
			}{{"fence_corner", "oak_fence", .1, 162.2, .1}, {"fence_center", "oak_fence", .5, 162.2, .5}, {"top_slab_gap", "oak_slab[type=top]", .5, 162, .5}, {"slab_overlap_5e8", "oak_slab[type=top]", .5, 162.00000005, .5},
				{"slab_overlap_15e8", "oak_slab[type=top]", .5, 162.00000015, .5},
				{"slab_overlap_2e7", "oak_slab[type=top]", .5, 162.0000002, .5},
				{"slab_overlap_25e8", "oak_slab[type=top]", .5, 162.00000025, .5},
				{"fence_touch", "oak_fence", .125, 162.2, .5},
				{"fence_overlap_5e8", "oak_fence", .12500005, 162.2, .5},
				{"fence_overlap_2e7", "oak_fence", .1250002, 162.2, .5},
				{"top_slab_hit", "oak_slab[type=top]", .5, 162.6, .5}} {
				command("kill @e[type=experience_orb]")
				command("fill -1 161 -1 1 164 1 air")
				command("setblock 0 162 0 " + tc.state)
				witness := fmt.Sprintf("ORB_INSIDE_STATE_%t_%s", noGravity, tc.name)
				if lines := command("execute if block 0 162 0 " + tc.state + " run say " + witness); !structureSaidBB(lines, witness) {
					t.Fatal("missing shape witness")
				}
				flag := 0
				if noGravity {
					flag = 1
				}
				tag := fmt.Sprintf("OrbInside%t%s", noGravity, tc.name)
				sel := "@e[type=experience_orb,tag=" + tag + ",limit=1]"
				command(fmt.Sprintf(`summon experience_orb %.17g %.17g %.17g {Tags:["%s"],Value:1s,NoGravity:%db,Motion:[0d,0d,0d],Age:0s}`, tc.x, tc.y, tc.z, tag, flag))
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
				delta := [3]float64{x - tc.x, y - tc.y, z - tc.z}
				axis, sign := -1, float64(-1)
				switch tc.name {
				case "fence_center", "slab_overlap_15e8", "slab_overlap_2e7", "slab_overlap_25e8":
					axis = 2
				case "fence_overlap_2e7":
					axis = 0
				case "top_slab_hit":
					axis = 1
					sign = 1
				}
				for a := range delta {
					if a == axis {
						if distance := sign * delta[a]; distance < .1-1e-8 || distance > .3+1e-8 {
							t.Errorf("%s NoGravity=%t %s axis%d escape=%g want [.1,.3]", addr, noGravity, tc.name, a, distance)
						}
					} else {
						want := float64(0)
						if axis == -1 && a == 1 && !noGravity {
							want = -.03
						}
						if math.Abs(delta[a]-want) > 1e-8 {
							t.Errorf("%s NoGravity=%t %s axis%d displacement=%g want %g", addr, noGravity, tc.name, a, delta[a], want)
						}
					}
					if want := delta[a] * float64(float32(.98)); math.Abs(motion[a]-want) > 1e-8 {
						t.Errorf("%s NoGravity=%t %s axis%d Motion=%g want %g", addr, noGravity, tc.name, a, motion[a], want)
					}
				}
				t.Logf("checked %s NoGravity=%t shape=%s", addr, noGravity, tc.name)
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
