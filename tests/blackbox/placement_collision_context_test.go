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

func TestPlacementCollisionContextParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("PlaceShape")}, nil)
	defer targets.cleanup()
	cases := []struct {
		name, scale, item, block string
		setup                    []string
		x                        float64
		target                   [3]int32
		face                     int32
		pose                     int32
		accepted                 bool
	}{
		{"rod_clear", "1", "end_rod", "end_rod", nil, .05, [3]int32{0, 160, 0}, 1, 0, true},
		{"rod_contact", "1", "end_rod", "end_rod", nil, .10, [3]int32{0, 160, 0}, 1, 0, false},
		{"copper_chest_clear", "1", "copper_chest", "copper_chest", nil, .75, [3]int32{1, 160, 0}, 1, 0, true},
		{"copper_chest_contact", "1", "copper_chest", "copper_chest", nil, .80, [3]int32{1, 160, 0}, 1, 0, false},
		{"small_width", "0.5", "stone", "stone", nil, .75, [3]int32{1, 160, 0}, 1, 0, true},
		{"normal_width", "1", "stone", "stone", nil, .75, [3]int32{1, 160, 0}, 1, 0, false},
		{"large_width", "2", "stone", "stone", nil, .5, [3]int32{1, 160, 0}, 1, 0, false},
		{"forced_crawl", "1", "stone", "stone", []string{"setblock 0 161 0 stone", "setblock 1 162 0 stone"}, .8, [3]int32{1, 161, 0}, 0, 3, true},
		{"forced_crouch", "1", "stone_slab", "stone_slab[type=top]", []string{"setblock 0 161 0 stone_slab[type=top]", "setblock 1 162 0 stone"}, .8, [3]int32{1, 161, 0}, 0, 5, true},
		{"scaffold_inside", "1", "scaffolding", "scaffolding", nil, .5, [3]int32{0, 160, 0}, 1, 0, true},
	}
	run := func(addr string) []bool {
		conn, pr, pw := joinPlayBlackbox(t, addr, "PlaceShape")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		pose := int32(0)
		metadataPrefix := fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)
		collect := func(marker string) []string {
			t.Helper()
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				batch := sink.take()
				lines = append(lines, batch...)
				done := false
				for _, line := range batch {
					if strings.Contains(line, marker) {
						done = true
					}
					if strings.HasPrefix(line, metadataPrefix) {
						_, entries := fwDecodeMeta([]byte(strings.TrimPrefix(line, metadataPrefix)))
						for _, entry := range entries {
							if entry.idx == 6 && entry.typ == 20 {
								pose = int32(entry.i)
							}
						}
					}
				}
				if done {
					return lines
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing marker %s", addr, marker)
			return nil
		}
		ready := func(x float64, wantPose int32, tag string) {
			t.Helper()
			deadline := time.Now().Add(3 * time.Second)
			for n := 0; time.Now().Before(deadline); n++ {
				sendCommand(t, pw, "data get entity @s Pos[0]")
				marker := fmt.Sprintf("PLACEREADY%s%dX", tag, n)
				sendCommand(t, pw, "say "+marker)
				px := math.NaN()
				for _, line := range collect(marker) {
					if end := strings.Index(line, "data.entity.query"); end >= 0 {
						px = lastNumberSigned(line[:end])
					}
				}
				if !math.IsNaN(px) && math.Abs(px-x) < 1e-6 && pose == wantPose {
					return
				}
			}
			t.Fatalf("%s %s start pose%d want%d", addr, tag, pose, wantPose)
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "say PLACEFROZEN")
		collect("PLACEFROZEN")
		var out []bool
		for i, tc := range cases {
			sendCommand(t, pw, "tp @s 8.0 160.0 8.0")
			_ = pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0})
			ready(8, 0, fmt.Sprintf("WAY%d", i))
			sendCommand(t, pw, "fill -2 159 -2 3 165 2 air")
			sendCommand(t, pw, "fill -2 159 -2 3 159 2 stone")
			sendCommand(t, pw, "attribute @s minecraft:scale base set "+tc.scale)
			for _, command := range tc.setup {
				sendCommand(t, pw, command)
			}
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with "+tc.item+" 5")
			sendCommand(t, pw, fmt.Sprintf("tp @s %.6f 160.0 0.5", tc.x))
			ready(tc.x, tc.pose, fmt.Sprintf("START%d", i))
			// Attribute dimension refresh is deferred; prove completed ticks.
			shulkerStepTicks(t, pw, sink, 2)
			clicked := tc.target
			cy := float32(1)
			if tc.face == 1 {
				clicked[1]--
			} else {
				clicked[1]++
				cy = 0
			}
			sequence := int32(2300 + i)
			sendUseItemOnCursor(t, pw, clicked[0], clicked[1], clicked[2], tc.face, .5, cy, .5, sequence)
			acked := false
			deadline := time.Now().Add(5 * time.Second)
			prefix := fmt.Sprintf("%#x:", server.IDPlayBlockChangedAck)
			for time.Now().Before(deadline) && !acked {
				for _, line := range sink.take() {
					if strings.HasPrefix(line, prefix) {
						ack, err := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix))).VarInt()
						if err == nil && ack == sequence {
							acked = true
						}
					}
				}
				if !acked {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !acked {
				t.Fatalf("%s %s missing click acknowledgement", addr, tc.name)
			}
			present := fmt.Sprintf("PLACEPRESENT%dX", i)
			marker := fmt.Sprintf("PLACEDONE%dX", i)
			sendCommand(t, pw, fmt.Sprintf("execute if block %d %d %d %s run say %s", tc.target[0], tc.target[1], tc.target[2], tc.block, present))
			sendCommand(t, pw, "say "+marker)
			found := false
			for _, line := range collect(marker) {
				if strings.Contains(line, present) {
					found = true
				}
			}
			out = append(out, found)
			t.Logf("%s %s placed%v pose%d", addr, tc.name, found, pose)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, tc := range cases {
		if vanilla[i] != tc.accepted || mcgo[i] != vanilla[i] {
			t.Errorf("%s vanilla%v mcgo%v want%v", tc.name, vanilla[i], mcgo[i], tc.accepted)
		}
	}
}
