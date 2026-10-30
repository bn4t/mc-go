package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// Native first-tick controls cover the orb response, friction and block effects.
func TestXPOrbPhysicsParity(t *testing.T) {
	const actor = "OrbPhysicsRead"
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
			receipt := fmt.Sprintf("ORB_PHYS_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return waitForExactSayBB(t, sink, receipt, 30*time.Second)
		}
		command("gamemode spectator @s")
		command("tp @s 10.0 165.0 10.0")
		command("tick freeze")
		if x, y, z, ok := dataGetPos(t, sink, pw, "@s"); !ok || x != 10 || y != 165 || z != 10 {
			t.Fatalf("%s actor position %v,%v,%v valid%t", addr, x, y, z, ok)
		}
		type sample struct {
			surface         string
			gap, rawY, rawX float64
			noGravity       bool
		}
		var cases []sample
		for _, noGravity := range []bool{false, true} {
			for _, surface := range []string{"stone", "slime_block"} {
				for _, gap := range []float64{0, 5e-8, .0002, .0004} {
					cases = append(cases, sample{surface: surface, gap: gap, rawY: -.2, noGravity: noGravity})
				}
				for _, rawY := range []float64{-.01, -.02, -.03, -.04} {
					cases = append(cases, sample{surface: surface, rawY: rawY, noGravity: noGravity})
				}
				for _, rawY := range []float64{-.2, -.03} {
					cases = append(cases, sample{surface: surface, rawY: rawY, rawX: .1, noGravity: noGravity})
				}
			}
			cases = append(cases, sample{surface: "air", rawY: -.2, rawX: .1, noGravity: noGravity})
		}
		for i, tc := range cases {
			command("kill @e[type=experience_orb]")
			command("fill -2 162 -2 3 164 3 air")
			if tc.surface != "air" {
				command("fill -2 162 -2 3 162 3 " + tc.surface)
			}
			witness := fmt.Sprintf("ORB_PHYS_SURFACE_%d", i)
			if lines := command("execute if block 0 162 0 " + tc.surface + " run say " + witness); !structureSaidBB(lines, witness) {
				t.Fatalf("%s missing surface %s", addr, tc.surface)
			}

			sensor := tc.surface == "slime_block" && !tc.noGravity && tc.rawY == -.2 && tc.gap == 0 && tc.rawX == 0
			if sensor {
				command("setblock 2 163 2 sculk_sensor")
				shulkerStepTicks(t, pw, sink, 45)
			}
			initialY, flag := tc.rawY+.03, 0
			if tc.noGravity {
				initialY, flag = tc.rawY, 1
			}
			initialX := tc.rawX
			tag := fmt.Sprintf("OrbPhysics%d", i)
			sel := "@e[type=experience_orb,tag=" + tag + ",limit=1]"
			startY := 163 + tc.gap
			command(fmt.Sprintf(`summon experience_orb 0.5 %.17g 0.5 {Tags:["%s"],Value:1s,NoGravity:%db,Motion:[%.17gd,%.17gd,0d],Age:0s,OnGround:0b}`, startY, tag, flag, initialX, initialY))
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
				t.Fatalf("%s first orb age%v want1", addr, age)
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
					t.Fatal("invalid orb Motion component")
				}
			}
			t.Logf("%s surface%s NoGravity%t gap%.12g rawY%.12g rawX%.12g Pos%v,%v,%v Motion%v", addr, tc.surface, tc.noGravity, tc.gap, tc.rawY, tc.rawX, x, y, z, motion)

			wantX, wantY := .5+initialX, startY
			effectiveGravity := .03
			if tc.noGravity {
				effectiveGravity = 0
			}
			friction := float32(.98)
			walk := float64(1)
			postBounceY := float64(0)
			if tc.surface != "air" {
				blockFriction := float32(.6)
				if tc.surface == "slime_block" {
					blockFriction = .8
					if -tc.rawY >= effectiveGravity {
						postBounceY = -tc.rawY * float64(float32(.8))
					}
					if postBounceY < .1 {
						walk = .4 + .2*postBounceY
					}
				}
				friction *= blockFriction
			}
			wantMotion := [3]float64{initialX * walk * float64(friction), postBounceY * float64(friction), 0}
			if tc.surface == "air" {
				wantY += tc.rawY
				wantMotion[1] = tc.rawY * float64(friction)
			} else {
				if tc.gap*tc.gap > 1e-7 {
					wantY = 163
				}
				if tc.rawY < -effectiveGravity {
					wantMotion[1] = -tc.rawY * .4
				}
			}
			if math.Abs(x-wantX) > 1e-10 || math.Abs(y-wantY) > 1e-10 || z != .5 {
				t.Errorf("%s %s NoGravity%t gap%v position%v,%v,%v want%v,%v,.5", addr, tc.surface, tc.noGravity, tc.gap, x, y, z, wantX, wantY)
			}
			for axis := range motion {
				if math.Abs(motion[axis]-wantMotion[axis]) > 1e-11 {
					t.Errorf("%s %s NoGravity%t gap%v Motion[%d]%v want%v", addr, tc.surface, tc.noGravity, tc.gap, axis, motion[axis], wantMotion[axis])
				}
			}
			if sensor {
				shulkerStepTicks(t, pw, sink, 6)
				found := false
				for _, line := range command("data get block 2 163 2 last_vibration_frequency") {
					if i := strings.Index(line, "data.block.query"); i >= 0 {
						found = true
						if value := lastNumberSigned(line[:i]); value != 2 {
							t.Errorf("%s slime bounce vibration%v want2", addr, value)
						}
					}
				}
				if !found {
					t.Fatal("missing slime sensor frequency")
				}
				if lines := command("execute if block 2 163 2 sculk_sensor[sculk_sensor_phase=active] run say ORB_PHYS_SENSOR_ACTIVE"); !structureSaidBB(lines, "ORB_PHYS_SENSOR_ACTIVE") {
					t.Errorf("%s slime sensor not active", addr)
				}
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
