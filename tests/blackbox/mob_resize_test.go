package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// Pin measured refresh timing, cached boxes, saved form and independent live position.
func TestMobScaleResizeLifecycleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("ResizeRead")}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, "ResizeRead")
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkResolvedTP(pr, pw)
		serial := 0
		command := func(cmd string) []string {
			serial++
			tag := fmt.Sprintf("RSReceipt%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+tag)
			return waitForExactSayBB(t, sink, tag, 30*time.Second)
		}
		collect := func(tag string) []string { return waitForExactSayBB(t, sink, tag, 30*time.Second) }
		command("gamemode creative @s")
		command("tp @s 0.5 162 0.5")
		command("tick freeze")
		setupPlayerTickBarrierBB(t, pw)
		waitPlayerTicksBB(t, pw, collect, "RSReady", 3)
		command("fill 3 157 -5 14 170 5 air")
		command("scoreboard objectives add RSScale dummy")
		horse := "@e[type=horse,tag=RSBody,limit=1]"
		position := func(target string) [3]float64 {
			for _, line := range command("data get entity " + target + " Pos") {
				if !strings.Contains(line, "data.entity.query") {
					continue
				}
				value, ok := parseSNBT(line)
				list, yes := value.([]any)
				if !ok || !yes || len(list) != 3 {
					continue
				}
				var out [3]float64
				for axis := range out {
					n, ok := hrNum(list[axis])
					if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
						t.Fatal("nonfinite position receipt")
					}
					out[axis] = n
				}
				return out
			}
			t.Fatalf("%s missing position for %s", addr, target)
			return [3]float64{}
		}
		scaleReceipt := func(target string, want float64) {
			command("execute store result score #scale RSScale run attribute " + target + " minecraft:scale get 1000")
			if !structureSaidBB(command(fmt.Sprintf("execute if score #scale RSScale matches %d run say RSScaleApplied", int(want*1000))), "RSScaleApplied") {
				t.Fatalf("%s effective scale %g not applied", addr, want)
			}
		}
		bodyBoxReceipt := func(label string, scale float64) bool {
			wide := structureSaidBB(command("execute if entity @e[type=horse,tag=RSBody,x=9.5,y=162,z=0,dx=0,dy=0,dz=0] run say RSBodyWide"), "RSBodyWide")
			near := structureSaidBB(command("execute if entity @e[type=horse,tag=RSBody,x=8.9,y=162,z=0,dx=0,dy=0,dz=0] run say RSBodyNear"), "RSBodyNear")
			if wide != (scale >= 2) || near != (scale >= 1) {
				t.Errorf("%s %s boxes wide%t near%t want scale%g", addr, label, wide, near, scale)
			}
			return wide
		}
		sample := func(label, target string, wantY float64, checkBox bool, scale float64) {
			saved := position(target)
			command("kill @e[type=marker,tag=RSLive]")
			command("execute at " + target + " run summon marker ~ ~ ~ {Tags:[RSLive]}")
			live := position("@e[type=marker,tag=RSLive,limit=1]")
			want := [3]float64{8.5, wantY, .5}
			if target == "@s" {
				want[0] = .5
			}
			for axis := range want {
				if math.Abs(saved[axis]-want[axis]) > 1e-8 || math.Abs(live[axis]-want[axis]) > 1e-8 {
					t.Errorf("%s %s axis%d saved%.17g live%.17g want%.17g", addr, label, axis, saved[axis], live[axis], want[axis])
				}
			}
			wide := false
			if checkBox {
				wide = bodyBoxReceipt(label, scale)
				scaleReceipt(target, scale)
			}
			t.Logf("%s %s saved%v live%v bboxWide%t", addr, label, saved, live, wide)
		}
		cases := []struct {
			name           string
			admit          bool
			scale          float64
			floor, ceiling bool
		}{
			{"freshCommand", false, 2, false, false},
			{"admittedAir", true, 2, false, false},
			{"admittedFloor", true, 2, true, false},
			{"admittedCeiling", true, 2, false, true},
			{"admittedSealed", true, 2, true, true},
			{"admittedTooLarge", true, 3, false, false},
			{"admittedShrink", true, .5, false, false},
		}
		for _, tc := range cases {
			command("kill " + horse)
			command("fill 3 157 -5 14 170 5 air")
			if tc.floor {
				command("fill 3 161 -5 14 161 5 stone")
			}
			if tc.ceiling {
				command("fill 3 164 -5 14 164 5 stone")
			}
			if tc.floor && !structureSaidBB(command("execute if block 8 161 0 stone run say RSFloor"), "RSFloor") {
				t.Fatal("floor missing")
			}
			if tc.ceiling && !structureSaidBB(command("execute if block 8 164 0 stone run say RSCeiling"), "RSCeiling") {
				t.Fatal("ceiling missing")
			}
			command("summon horse 8.5 162 0.5 {Tags:[RSBody],NoAI:1b,NoGravity:1b}")
			if !structureSaidBB(command("execute if entity @e[type=horse,tag=RSBody,nbt={NoAI:1b,NoGravity:1b},limit=1] run say RSBodyFlags"), "RSBodyFlags") {
				t.Fatal("actual horse NoAI/NoGravity flags missing")
			}
			sample(tc.name+"Initial", horse, 162, false, tc.scale)
			if tc.admit {
				shulkerStepTicks(t, pw, sink, 1)
				sample(tc.name+"Admitted", horse, 162, false, 1)
			}
			command(fmt.Sprintf("attribute %s minecraft:scale base set %g", horse, tc.scale))
			scaleReceipt(horse, tc.scale)
			bodyBoxReceipt(tc.name+"Pending", 1)
			sample(tc.name+"Set", horse, 162, false, tc.scale)
			wantY := 162.0
			if tc.scale == 2 {
				switch {
				case tc.floor && tc.ceiling:
					wantY = 162.000001
				case tc.floor:
					wantY = 162
				case tc.ceiling:
					wantY = 160.79999995231628
				default:
					wantY = 161.19999998807907
				}
			}
			shulkerStepTicks(t, pw, sink, 1)
			sample(tc.name+"Tick1", horse, wantY, true, tc.scale)
			shulkerStepTicks(t, pw, sink, 1)
			sample(tc.name+"Tick2", horse, wantY, true, tc.scale)
		}
		// Vex.tick holds noPhysics during the living callback, so scale
		// changes its box without moving its feet even in empty space.
		command("kill " + horse)
		command("fill 3 157 -5 14 170 5 air")
		command("summon vex 8.5 162 0.5 {Tags:[RSVex],NoAI:1b,NoGravity:1b}")
		vex := "@e[type=vex,tag=RSVex,limit=1]"
		shulkerStepTicks(t, pw, sink, 1)
		sample("vexAdmitted", vex, 162, false, 1)
		command("attribute " + vex + " minecraft:scale base set 2")
		scaleReceipt(vex, 2)
		sample("vexSet", vex, 162, false, 2)
		shulkerStepTicks(t, pw, sink, 1)
		sample("vexNoPhysics", vex, 162, false, 2)
		if !structureSaidBB(command("execute if entity @e[type=vex,tag=RSVex,x=8.8,y=162,z=0,dx=0,dy=0,dz=0] run say RSVexWide"), "RSVexWide") {
			t.Errorf("%s missing scaled vex bbox", addr)
		}
		command("attribute @s minecraft:scale base set 2")
		waitPlayerTicksBB(t, pw, collect, "RSPlayerScale", 3)
		scaleReceipt("@s", 2)
		sample("playerScale2", "@s", 162, false, 2)
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
