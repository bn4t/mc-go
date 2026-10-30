package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestItemNoGravityRestitutionParity(t *testing.T) {
	const actor = "GravityBounce"
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
			mark := fmt.Sprintf("GRAVITY_BOUNCE_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+mark)
			return waitForExactSayBB(t, sink, mark, 30*time.Second)
		}
		command("gamemode creative @s")
		command("tp @s 10.0 165.0 10.0")
		command("tick freeze")
		for _, noGravity := range []bool{false, true} {
			block := "slime_block"
			for i, gap := range []float64{0, 5e-8, .0002, .0004} {
				command("kill @e[type=item]")
				command(fmt.Sprintf("fill -2 162 -2 3 162 3 %s", block))
				tag := fmt.Sprintf("GravityBounce%t%d", noGravity, i)
				sel := "@e[type=item,tag=" + tag + ",limit=1]"
				startY := 163 + gap
				initialY, gravityFlag := -.16, 0
				if noGravity {
					initialY, gravityFlag = -.2, 1
				}
				command(fmt.Sprintf(`summon item 0.5 %.17g 0.5 {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:%db,Motion:[0d,%.17gd,0d],PickupDelay:32767s,Age:0s,OnGround:0b}`, startY, tag, gravityFlag, initialY))
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
				parsed, ok := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
				list, isList := parsed.([]any)
				if !ok || !isList || len(list) != 3 {
					t.Fatal("missing Motion")
				}
				var motion [3]float64
				for axis := range motion {
					n, valid := hrNum(list[axis])
					if !valid {
						t.Fatal("invalid Motion")
					}
					motion[axis] = n
				}
				wantY := startY
				if gap*gap > 1e-7 {
					wantY = 163
				}

				ratio := (startY - 163) / .2
				drag := float64(float32(.98))
				gravity := .04
				if noGravity {
					gravity = 0
				}
				wantMotion := (ratio*gravity + .2) * (1 + ratio*(drag-1)) * float64(float32(.8)) * drag
				t.Logf("%s noGravity%t gap%.12g firstPos%v,%v,%v Motion%v", addr, noGravity, gap, x, y, z, motion)
				if x != .5 || z != .5 || math.Abs(y-wantY) > 1e-10 || motion[0] != 0 || motion[2] != 0 || math.Abs(motion[1]-wantMotion) > 1e-11 {
					t.Errorf("%s noGravity%t gap%v Y%v want%v MotionY%v want%v", addr, noGravity, gap, y, wantY, motion[1], wantMotion)
				}
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
