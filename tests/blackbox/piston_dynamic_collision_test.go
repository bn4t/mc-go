package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

// Moving-block collision follows the carried shape through two animation
// ticks, then a separate cleanup tick. A source piston carries its head.
func TestMovingPistonCollisionParity(t *testing.T) {
	const player = "PistonShape"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer targets.cleanup()
	type scenario struct {
		name, kind string
		sign       int
	}
	cases := []scenario{{"stone_east", "stone", 1}, {"stone_west", "stone", -1}, {"head_east", "head", 1}, {"head_west", "head", -1}, {"retract_east", "retract", 1}, {"retract_west", "retract", -1}, {"wet_slab", "slab", 1}, {"orphan", "orphan", 1}}
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		query := func(x int, block string) bool {
			serial++
			hit, end := fmt.Sprintf("PISCOLHIT%d", serial), fmt.Sprintf("PISCOLEND%d", serial)
			sendCommand(t, pw, fmt.Sprintf("execute if block %d 160 0 %s run say %s", x, block, hit))
			sendCommand(t, pw, "say "+end)
			found := false
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, hit) {
						found = true
					}
					if strings.Contains(line, end) {
						return found
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing%s", addr, end)
			return false
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tick freeze")
		var out []float64
		for _, tc := range cases {
			sign := tc.sign
			facing := "east"
			if sign < 0 {
				facing = "west"
			}
			wx := func(v float64) float64 {
				if sign < 0 {
					return 1 - v
				}
				return v
			}
			sendCommand(t, pw, "tp @s 8 160 3")
			sendCommand(t, pw, "fill -4 160 -2 4 164 2 air")
			sendCommand(t, pw, "fill -4 159 -2 4 159 2 stone")
			shulkerStepTicks(t, pw, sink, 1)
			node, final := 2*sign, "stone"
			probes := [2]float64{2.6, 2.9}
			y := 160.0
			switch tc.kind {
			case "head":
				node, final = sign, "piston_head[facing="+facing+",short=false]"
				probes = [2]float64{1.6, 1.9}
			case "retract":
				node, final = 0, "piston[facing="+facing+",extended=false]"
				probes = [2]float64{1.6, 2}
			case "slab":
				final = "stone_slab[type=bottom,waterlogged=false]"
				y = 160.75
			}
			if tc.kind == "orphan" {
				sendCommand(t, pw, fmt.Sprintf("setblock %d 160 0 moving_piston[facing=%s]", node, facing))
			} else {
				if tc.kind == "stone" || tc.kind == "slab" {
					block := "stone"
					if tc.kind == "slab" {
						block = "stone_slab[type=bottom,waterlogged=true]"
					}
					sendCommand(t, pw, fmt.Sprintf("setblock %d 160 0 %s", sign, block))
				}
				sendCommand(t, pw, fmt.Sprintf("setblock 0 160 0 piston[facing=%s]", facing))
				sendCommand(t, pw, "setblock 0 160 -1 redstone_block")
				if tc.kind == "retract" {
					shulkerStepTicks(t, pw, sink, 3)
					sendCommand(t, pw, "setblock 0 160 -1 air")
				}
			}
			for phase := 0; phase <= 4; phase++ {
				moving := query(node, "moving_piston[facing="+facing+"]")
				settled := query(node, final)
				wantMoving := phase == 1 || phase == 2
				wantSettled := phase >= 3
				if tc.kind == "orphan" {
					wantMoving, wantSettled = true, false
				}
				if moving != wantMoving || settled != wantSettled {
					t.Errorf("%s %s phase%d moving=%t settled=%t want%t/%t", addr, tc.name, phase, moving, settled, wantMoving, wantSettled)
				}
				out = append(out, boolFloat(moving), boolFloat(settled))
				if tc.kind == "stone" || tc.kind == "slab" {
					headMoving := query(sign, "moving_piston[facing="+facing+"]")
					headSettled := query(sign, "piston_head[facing="+facing+",short=false]")
					if headMoving != (phase == 1 || phase == 2) || headSettled != (phase >= 3) {
						t.Errorf("%s %s phase%d sourceheadmoving=%t settled=%t", addr, tc.name, phase, headMoving, headSettled)
					}
					out = append(out, boolFloat(headMoving), boolFloat(headSettled))
				}
				for j, target := range probes {
					start := wx(3.4)
					sendCommand(t, pw, fmt.Sprintf("tp @s %.2f %.2f 0.5", start, y))
					sx, _ := dataGetVec2(t, sink, pw, "@s", "Pos")
					if math.Abs(sx-start) > 1e-6 {
						t.Fatalf("%s %s phase%d setupx=%v", addr, tc.name, phase, sx)
					}
					var b protocol.Buffer
					b.Double(wx(target))
					b.Double(y)
					b.Double(.5)
					b.Byte(0)
					if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
						t.Fatal(err)
					}
					time.Sleep(100 * time.Millisecond)
					got, _ := dataGetVec2(t, sink, pw, "@s", "Pos")
					accepted := phase == 0 || (phase == 1 && j == 1)
					if tc.kind == "retract" {
						accepted = phase >= 2 || (phase == 1 && j == 1)
					}
					if tc.kind == "slab" || tc.kind == "orphan" {
						accepted = true
					}
					want := start
					if accepted {
						want = wx(target)
					}
					out = append(out, got)
					t.Logf("%s %s phase%d target%.2f x=%.8f", addr, tc.name, phase, target, got)
					if math.Abs(got-want) > 1e-6 {
						t.Errorf("%s %s phase%d target%.2f got%v want%v", addr, tc.name, phase, target, got, want)
					}
				}
				sendCommand(t, pw, fmt.Sprintf("tp @s %.2f %.2f 0.5", wx(3.4), y))
				dataGetVec2(t, sink, pw, "@s", "Pos")
				if phase < 4 {
					shulkerStepTicks(t, pw, sink, 1)
				}
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

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
