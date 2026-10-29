package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Movement must enter actual collision shapes, including their lower and
// lateral bounds, rather than treating every occupied cell as a column.
func TestPlayerMovementShapeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("ShapeMove")}, nil)
	defer targets.cleanup()
	type scenario struct {
		name          string
		setup         []string
		start, target [3]float64
		accepted      bool
	}
	cases := []scenario{
		{"top_slab_roof", []string{"fill -3 159 -3 3 159 3 stone_slab[type=bottom]", "setblock 1 161 0 stone_slab[type=top]"}, [3]float64{.5, 159.5, .5}, [3]float64{1.1, 159.5, .5}, true},
		{"crouch_top_slab", []string{"setblock 1 161 0 stone_slab[type=top]"}, [3]float64{.5, 160, .5}, [3]float64{1.1, 160, .5}, true},
		{"top_trapdoor_roof", []string{"fill -3 159 -3 3 159 3 stone_slab[type=bottom]", "setblock 1 161 0 oak_trapdoor[half=top,open=false]"}, [3]float64{.5, 159.5, .5}, [3]float64{1.1, 159.5, .5}, true},
		{"bottom_trapdoor_roof", []string{"fill -3 159 -3 3 159 3 stone_slab[type=bottom]", "setblock 1 161 0 oak_trapdoor[half=bottom,open=false]"}, [3]float64{.5, 159.5, .5}, [3]float64{1.1, 159.5, .5}, false},
		{"open_trapdoor_lateral", []string{"setblock 1 160 0 oak_trapdoor[open=true,facing=north]"}, [3]float64{.5, 160, .5}, [3]float64{1.1, 160, .5}, true},
		{"chest_margin", []string{"setblock 1 160 0 chest"}, [3]float64{.3, 160, .5}, [3]float64{.75, 160, .5}, true},
		{"chest_new_shape", []string{"setblock 0 160 0 chest"}, [3]float64{1.27, 160, .5}, [3]float64{1.2, 160, .5}, false},
		{"stair_low_step", []string{"setblock 1 160 0 stone_stairs[facing=east,half=bottom,shape=straight]"}, [3]float64{.5, 160.5, .5}, [3]float64{1.15, 160.5, .5}, true},
		{"tip_offset_clear", []string{"setblock 0 160 0 pointed_dripstone[vertical_direction=up,thickness=tip]"}, [3]float64{1.3, 160, .4}, [3]float64{.9, 160, .4}, true},
		{"tip_offset_collision", []string{"setblock 0 160 0 pointed_dripstone[vertical_direction=up,thickness=tip]"}, [3]float64{1.3, 160, .4}, [3]float64{.8, 160, .4}, false},
		{"tip_positive_hash_clear", []string{"setblock 1 160 0 pointed_dripstone[vertical_direction=up,thickness=tip]"}, [3]float64{.5, 160, .5}, [3]float64{1.1, 160, .5}, true},
		{"tip_positive_hash_collision", []string{"setblock 1 160 0 pointed_dripstone[vertical_direction=up,thickness=tip]"}, [3]float64{.5, 160, .5}, [3]float64{1.15, 160, .5}, false},
		{"tip_negative_hash_clear", []string{"setblock -1 160 0 pointed_dripstone[vertical_direction=up,thickness=tip]"}, [3]float64{.3, 160, .4}, [3]float64{-.1, 160, .4}, true},
		{"tip_negative_hash_collision", []string{"setblock -1 160 0 pointed_dripstone[vertical_direction=up,thickness=tip]"}, [3]float64{.3, 160, .4}, [3]float64{-.2, 160, .4}, false},
		{"tip_down_lower_gap", []string{"setblock 0 163 0 stone", "setblock 0 162 0 pointed_dripstone[vertical_direction=down,thickness=tip]"}, [3]float64{1.3, 160.4, .4}, [3]float64{.5, 160.4, .4}, true},
		{"solid_control", []string{"setblock 1 160 0 stone"}, [3]float64{.5, 160, .5}, [3]float64{1.1, 160, .5}, false},
		{"spectator_control", []string{"setblock 1 160 0 stone", "gamemode spectator @s"}, [3]float64{.5, 160, .5}, [3]float64{1.1, 160, .5}, true},
	}
	run := func(addr string) []float64 {
		conn, pr, pw := joinPlayBlackbox(t, addr, "ShapeMove")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		await := func(marker string) {
			t.Helper()
			until := time.Now().Add(15 * time.Second)
			for time.Now().Before(until) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s marker %s missing", addr, marker)
		}
		sendCommand(t, pw, "gamemode creative @s")
		out := make([]float64, 0, len(cases))
		for i, tc := range cases {
			sendCommand(t, pw, "tp @s 4 160 3")
			sendCommand(t, pw, "fill -3 159 -3 3 164 3 air")
			sendCommand(t, pw, "fill -3 159 -3 3 159 3 stone")
			for _, cmd := range tc.setup {
				sendCommand(t, pw, cmd)
			}
			sendCommand(t, pw, fmt.Sprintf("tp @s %v %v %v", tc.start[0], tc.start[1], tc.start[2]))
			flag := byte(0)
			if tc.name == "crouch_top_slab" {
				flag = 0x20
			}
			_ = pw.WritePacket(server.IDPlayPlayerInputSB, []byte{flag})
			marker := fmt.Sprintf("SHAPEPREP%d", i)
			sendCommand(t, pw, "say "+marker)
			await(marker)
			time.Sleep(200 * time.Millisecond)
			var b protocol.Buffer
			b.Double(tc.target[0])
			b.Double(tc.target[1])
			b.Double(tc.target[2])
			b.Byte(0)
			if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(150 * time.Millisecond)
			x, _ := dataGetVec2(t, sink, pw, "@s", "Pos")
			out = append(out, x)
			t.Logf("%s %s x=%v", addr, tc.name, x)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, tc := range cases {
		want := tc.start[0]
		if tc.accepted {
			want = tc.target[0]
		}
		if math.Abs(vanilla[i]-want) > 1e-6 || math.Abs(mcgo[i]-vanilla[i]) > 1e-6 {
			t.Errorf("%s vanilla=%v mcgo=%v want%v accepted%v", tc.name, vanilla[i], mcgo[i], want, tc.accepted)
		}
	}
}
