package blackbox

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestShulkerLidEntityPushParity(t *testing.T) {
	const actor, observer = "LidPusher", "LidReader"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor, observer)}, nil)
	defer targets.cleanup()
	type direction struct {
		name    string
		axis    int
		sign    float64
		blocked bool
	}
	dirs := []direction{{"east", 0, 1, false}, {"west", 0, -1, false}, {"up", 1, 1, false}, {"down", 1, -1, false}, {"south", 2, 1, false}, {"north", 2, -1, false}, {"east_wall", 0, 1, true}}
	run := func(addr string) []float64 {
		driver := newClickDriverOn(t, addr, actor, "minecraft:shulker_box", 20, nil, nil)
		driver.close()
		sendCommand(t, driver.pw, "gamemode creative @s")
		sendCommand(t, driver.pw, "tp @s -2 160 2")
		conn, pr, pw := joinPlayBlackbox(t, addr, observer)
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		scalar := func(selector, path string) float64 {
			serial++
			marker := fmt.Sprintf("LIDVALUE%d", serial)
			sendCommand(t, pw, "data get entity "+selector+" "+path)
			sendCommand(t, pw, "say "+marker)
			value := math.NaN()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if i := strings.Index(line, "data.entity.query"); i >= 0 {
						value = lastNumberSigned(line[:i])
					}
					if strings.Contains(line, marker) {
						if math.IsNaN(value) {
							t.Fatalf("%s no%s for%s", addr, path, selector)
						}
						return value
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing%s", addr, marker)
			return 0
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 4 160 3")
		sendCommand(t, pw, "fill -3 159 -3 4 159 3 stone")
		var out []float64
		for _, dir := range dirs {
			sendCommand(t, pw, "tick unfreeze")
			sendCommand(t, pw, fmt.Sprintf("setblock 0 162 0 shulker_box[facing=%s]", strings.TrimSuffix(dir.name, "_wall")))
			if dir.blocked {
				sendCommand(t, pw, "setblock 2 162 0 stone")
			}
			near := [3]float64{.5, 162.5, .5}
			if dir.axis == 1 {
				if dir.sign > 0 {
					near[1] = 163.125
				} else {
					near[1] = 161.625
				}
			} else {
				near[dir.axis] = .5 + dir.sign*.75
			}
			far, behind := near, near
			far[dir.axis] += dir.sign * 1
			if dir.blocked {
				far[0] = 3.25
			}
			behind[dir.axis] -= dir.sign * 1.5
			labels := []string{"near", "far", "behind"}
			points := [][3]float64{near, far, behind}
			items := []string{"stone", "bone", "stick"}
			selectors := make([]string, 3)
			for i, label := range labels {
				tag := "Lid_" + dir.name + "_" + label
				selectors[i] = "@e[type=item,tag=" + tag + ",limit=1]"
				p := points[i]
				sendCommand(t, pw, fmt.Sprintf(`summon item %.5f %.5f %.5f {Tags:["%s"],Item:{id:"minecraft:%s",count:1},NoGravity:1b,Motion:[0d,0d,0d],PickupDelay:32767s,Age:0s}`, p[0], p[1], p[2], tag, items[i]))
			}
			// Summon live and prove each entity has joined the tick list before
			// freezing and resetting its measured initial state.
			for _, selector := range selectors {
				deadline := time.Now().Add(10 * time.Second)
				for scalar(selector, "Age") < 3 {
					if time.Now().After(deadline) {
						t.Fatal("item never began ticking")
					}
					time.Sleep(50 * time.Millisecond)
				}
			}
			sendCommand(t, pw, "tick freeze")
			shulkerStepTicks(t, pw, sink, 1)
			for i, selector := range selectors {
				p := points[i]
				sendCommand(t, pw, fmt.Sprintf("tp %s %.5f %.5f %.5f", selector, p[0], p[1], p[2]))
				sendCommand(t, pw, "data merge entity "+selector+" {Motion:[0d,0d,0d]}")
			}
			path := "Pos[" + strconv.Itoa(dir.axis) + "]"
			initial := scalar(selectors[0], path)
			if math.Abs(initial-near[dir.axis]) > 1e-7 {
				t.Fatalf("%s %s initial=%v want%v", addr, dir.name, initial, near[dir.axis])
			}
			driver.reopen(0, 162, 0, 70, "lid-push-open")
			if !judgeUse(driver.opened).open {
				t.Fatal("shulker failed to open")
			}
			lastNear := initial
			for phase := 1; phase <= 10; phase++ {
				shulkerStepTicks(t, pw, sink, 1)
				got := scalar(selectors[0], path)
				wantDelta := []float64{0, .11, .22, .33, .44, .55, .66, .77, .88, .88}[phase-1] * dir.sign
				if dir.blocked {
					wantDelta = math.Min(wantDelta, .625)
				}
				if math.Abs(got-initial-wantDelta) > 1e-6 {
					t.Errorf("%s %s phase%d delta%.9f want%.9f", addr, dir.name, phase, got-initial, wantDelta)
				}
				out = append(out, got)
				t.Logf("%s %s phase%d near=%.9f delta=%.9f", addr, dir.name, phase, got, got-initial)
				lastNear = got
			}
			for i := 1; i < 3; i++ {
				got := scalar(selectors[i], path)
				out = append(out, got)
				if math.Abs(got-points[i][dir.axis]) > 1e-6 {
					t.Errorf("%s %s %s moved%v want%v", addr, dir.name, labels[i], got, points[i][dir.axis])
				}
			}
			motion := scalar(selectors[0], "Motion["+strconv.Itoa(dir.axis)+"]")
			out = append(out, motion)
			if motion != 0 {
				t.Errorf("%s %s injected velocity%v", addr, dir.name, motion)
			}
			driver.close()
			shulkerStepTicks(t, pw, sink, 10)
			got := scalar(selectors[0], path)
			out = append(out, got)
			if math.Abs(got-lastNear) > 1e-6 {
				t.Errorf("%s %s closing dragged item%v", addr, dir.name, got)
			}
			for _, selector := range selectors {
				sendCommand(t, pw, "kill "+selector)
			}
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(targets.vanilla), run(targets.mcgo)
	for i := range v {
		if math.Abs(v[i]-g[i]) > 1e-6 {
			t.Errorf("observation%d vanilla=%v mcgo=%v", i, v[i], g[i])
		}
	}
}
