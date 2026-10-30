package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func TestItemBounceLegacySupportParity(t *testing.T) {
	const actor = "BounceFaceRead"
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
			mark := fmt.Sprintf("BOUNCE_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+mark)
			return waitForExactSayBB(t, sink, mark, 30*time.Second)
		}
		command("gamemode creative @s")
		command("tp @s 10.0 165.0 10.0")
		command("tick freeze")
		for _, block := range []string{"slime_block", "stone"} {
			for i, gap := range []float64{0, 5e-8, .0002, .0004} {
				command("kill @e[type=item]")
				command("setblock 2 163 2 air")
				command(fmt.Sprintf("fill -2 162 -2 3 162 3 %s", block))
				command("setblock 2 163 2 sculk_sensor")
				shulkerStepTicks(t, pw, sink, 45)
				tag := fmt.Sprintf("Bounce%s%d", block, i)
				sel := "@e[type=item,tag=" + tag + ",limit=1]"
				startY := 163 + gap
				command(fmt.Sprintf(`summon item 0.5 %.17g 0.5 {Tags:["%s"],Item:{id:"minecraft:stone",count:1},Motion:[0d,-0.16d,0d],PickupDelay:32767s,Age:0s,OnGround:0b}`, startY, tag))
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
				wantMotion := 0.
				wantFrequency := 0.
				phase := "inactive"
				if block == "slime_block" {
					ratio := (startY - 163) / .2
					drag := float64(float32(.98))
					wantMotion = (ratio*.04 + .2) * (1 + ratio*(drag-1)) * float64(float32(.8)) * drag
					wantFrequency = 2
					phase = "active"
				}
				t.Logf("%s %s gap%.12g firstPos%v,%v,%v Motion%v", addr, block, gap, x, y, z, motion)
				if x != .5 || z != .5 || math.Abs(y-wantY) > 1e-10 || motion[0] != 0 || motion[2] != 0 || math.Abs(motion[1]-wantMotion) > 1e-11 {
					t.Errorf("%s %s gap%v Y%v want%v MotionY%v want%v", addr, block, gap, y, wantY, motion[1], wantMotion)
				}
				if block == "stone" {
					continue
				}
				shulkerStepTicks(t, pw, sink, 6)
				witness := fmt.Sprintf("BOUNCE_PHASE_%s_%d", block, i)
				lines := command("execute if block 2 163 2 sculk_sensor[sculk_sensor_phase=" + phase + "] run say " + witness)
				if !structureSaidBB(lines, witness) {
					t.Errorf("%s %s gap%v sensor phase want%s", addr, block, gap, phase)
				}
				found := false
				for _, line := range command("data get block 2 163 2 last_vibration_frequency") {
					if j := strings.Index(line, "data.block.query"); j >= 0 {
						found = true
						got := lastNumber(line[:j])
						if got != wantFrequency {
							t.Errorf("%s %s gap%v frequency%v want%v", addr, block, gap, got, wantFrequency)
						}
					}
				}
				if !found {
					t.Fatal("missing sensor frequency")
				}
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
