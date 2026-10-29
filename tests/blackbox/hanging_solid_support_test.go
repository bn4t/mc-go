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

func TestHangingSolidSupportParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("SolidHanger")}, nil)
	defer targets.cleanup()
	cases := []struct {
		name, block   string
		blocked, want bool
	}{
		{"stone", "stone", false, true},
		{"blocked_front", "stone", true, false},
		{"end_rod", "end_rod[facing=up]", false, false},
		{"scaffolding", "scaffolding[distance=0,bottom=false]", false, false},
		{"powder_snow", "powder_snow", false, false},
		{"cobweb", "cobweb", false, true},
		{"bamboo_sapling", "bamboo_sapling", false, true},
		{"glass", "glass", false, true},
	}
	run := func(addr string) []bool {
		conn, pr, pw := joinPlayBlackbox(t, addr, "SolidHanger")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		collect := func(marker string) []string {
			t.Helper()
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				batch := sink.take()
				lines = append(lines, batch...)
				for _, line := range batch {
					if strings.Contains(line, marker) {
						return lines
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing marker %s", addr, marker)
			return nil
		}
		position := func(x, y, z float64) {
			t.Helper()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				v := dataGetVec3(t, sink, pw, "@s", "Pos")
				px, py, pz := v[0], v[1], v[2]
				if !math.IsNaN(px) && !math.IsNaN(py) && !math.IsNaN(pz) && math.Abs(px-x) < 1e-6 && math.Abs(py-y) < 1e-6 && math.Abs(pz-z) < 1e-6 {
					return
				}
			}
			t.Fatalf("%s acknowledged position missing", addr)
		}
		sendCommand(t, pw, "gamemode creative @s")
		var out []bool
		for i, tc := range cases {
			sendCommand(t, pw, "tp @s 8.0 160.0 8.0")
			position(8, 160, 8)
			sendCommand(t, pw, "kill @e[type=item_frame]")
			sendCommand(t, pw, "fill -2 159 -3 2 163 3 air")
			sendCommand(t, pw, "fill -2 159 -3 2 159 3 stone")
			sendCommand(t, pw, "setblock 0 159 1 grass_block")
			sendCommand(t, pw, "setblock 0 160 1 "+tc.block)
			if tc.blocked {
				sendCommand(t, pw, "setblock 0 160 0 stone")
			}
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with item_frame 5")
			sendCommand(t, pw, "tp @s 0.5 160.0 -2.5")
			position(.5, 160, -2.5)
			sendUseItemOnCursor(t, pw, 0, 160, 1, 2, .5, .5, 0, int32(2100+i))
			sequence := int32(2100 + i)
			acknowledged := false
			deadline := time.Now().Add(5 * time.Second)
			prefix := fmt.Sprintf("%#x:", server.IDPlayBlockChangedAck)
			for time.Now().Before(deadline) && !acknowledged {
				for _, line := range sink.take() {
					if strings.HasPrefix(line, prefix) {
						ack, err := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix))).VarInt()
						if err == nil && ack == sequence {
							acknowledged = true
						}
					}
				}
				if !acknowledged {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !acknowledged {
				t.Fatalf("%s %s missing click acknowledgement %d", addr, tc.name, sequence)
			}

			present := fmt.Sprintf("HANGPRESENT%dX", i)
			marker := fmt.Sprintf("HANGDONE%dX", i)
			sendCommand(t, pw, "execute if entity @e[type=item_frame] run say "+present)
			sendCommand(t, pw, "say "+marker)
			found := false
			for _, line := range collect(marker) {
				if strings.Contains(line, present) {
					found = true
				}
			}
			out = append(out, found)
			t.Logf("%s %s placed%v", addr, tc.name, found)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, tc := range cases {
		if vanilla[i] != tc.want || mcgo[i] != vanilla[i] {
			t.Errorf("%s vanilla%v mcgo%v want%v", tc.name, vanilla[i], mcgo[i], tc.want)
		}
	}
}
