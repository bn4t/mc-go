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

func TestSpawnEggCollisionCellParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("EggCell")}, nil)
	defer targets.cleanup()
	cases := []struct {
		name, block string
		face        int32
		want        [3]float64
	}{
		{"solid_side", "stone", 2, [3]float64{.5, 160, .5}},
		{"empty_air_side", "air", 2, [3]float64{.5, 160, 1.5}},
		{"grass_side", "short_grass", 2, [3]float64{.5, 160, 1.5}},
		{"snow_one_side", "snow[layers=1]", 2, [3]float64{.5, 160, 1.5}},
		{"snow_two_side", "snow[layers=2]", 2, [3]float64{.5, 160, .5}},
		{"potted_oak_side", "potted_oak_sapling", 2, [3]float64{.5, 160, .5}},
		{"empty_pot_side", "flower_pot", 2, [3]float64{.5, 160, .5}},
		{"solid_top", "stone", 1, [3]float64{.5, 161, 1.5}},
		{"snow_one_top", "snow[layers=1]", 1, [3]float64{.5, 160, 1.5}},
		{"snow_two_top", "snow[layers=2]", 1, [3]float64{.5, 160.125, 1.5}},
		{"potted_oak_top", "potted_oak_sapling", 1, [3]float64{.5, 160.375, 1.5}},
	}
	run := func(addr string) [][3]float64 {
		conn, pr, pw := joinPlayBlackbox(t, addr, "EggCell")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		waitMarker := func(marker string) {
			t.Helper()
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing marker %s", addr, marker)
		}
		position := func(x, y, z float64) {
			t.Helper()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				p := dataGetVec3(t, sink, pw, "@s", "Pos")
				if math.Abs(p[0]-x) < 1e-6 && math.Abs(p[1]-y) < 1e-6 && math.Abs(p[2]-z) < 1e-6 {
					return
				}
			}
			t.Fatalf("%s missing acknowledged actor position", addr)
		}
		sendPlayerInput(pw, 0x20) // Bypass a flower pot's ordinary unpot interaction.
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "say EGGFROZEN")
		waitMarker("EGGFROZEN")
		var out [][3]float64
		for i, tc := range cases {
			sendCommand(t, pw, "tp @s 8.0 160.0 8.0")
			position(8, 160, 8)
			sendCommand(t, pw, "kill @e[type=pig]")
			sendCommand(t, pw, "fill -2 159 -3 2 163 3 air")
			sendCommand(t, pw, "fill -2 159 -3 2 159 3 stone")
			sendCommand(t, pw, "setblock 0 160 1 "+tc.block)
			name := fmt.Sprintf("EggCase%d", i)
			sendCommand(t, pw, fmt.Sprintf(`item replace entity @s weapon.mainhand with pig_spawn_egg[custom_name="%s"] 5`, name))
			sendCommand(t, pw, "tp @s 0.5 160.0 -2.5")
			position(.5, 160, -2.5)
			cursor := [3]float32{.5, .5, 0}
			if tc.face == 1 {
				cursor = [3]float32{.5, 1, .5}
			}
			sequence := int32(2200 + i)
			sendUseItemOnCursor(t, pw, 0, 160, 1, tc.face, cursor[0], cursor[1], cursor[2], sequence)
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
				t.Fatalf("%s %s missing click acknowledgement", addr, tc.name)
			}
			pos := dataGetVec3(t, sink, pw, fmt.Sprintf("@e[type=pig,name=%s,limit=1]", name), "Pos")
			out = append(out, pos)
			t.Logf("%s %s pos%v", addr, tc.name, pos)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, tc := range cases {
		for axis, want := range tc.want {
			if math.IsNaN(vanilla[i][axis]) || math.IsNaN(mcgo[i][axis]) || math.Abs(vanilla[i][axis]-want) > 1e-6 || math.Abs(mcgo[i][axis]-vanilla[i][axis]) > 1e-6 {
				t.Errorf("%s vanilla%v mcgo%v want%v", tc.name, vanilla[i], mcgo[i], tc.want)
				break
			}
		}
	}
}
