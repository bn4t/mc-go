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

func TestConnectedCollisionMovementParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("ArmMove")}, nil)
	defer targets.cleanup()
	cases := []struct {
		name, block, properties string
		clear, blocked          float64
		straight                bool
	}{
		{"fence", "oak_fence", "north=true", .95, .85, false},
		{"nether_fence", "nether_brick_fence", "north=true", .95, .85, false},
		{"pane", "glass_pane", "north=true", .9, .8, false},
		{"iron_bars", "iron_bars", "north=true", .9, .8, false},
		{"copper_bars", "copper_bars", "north=true", .9, .8, false},
		{"oxidized_bars", "oxidized_copper_bars", "north=true", .9, .8, false},
		{"wall", "cobblestone_wall", "north=low,south=low,up=false", 1.025, .95, true},
	}
	run := func(addr string) []float64 {
		conn, pr, pw := joinPlayBlackbox(t, addr, "ArmMove")
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
			t.Fatalf("%s marker%s missing", addr, marker)
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "gamerule minecraft:random_tick_speed 0")
		var out []float64
		for i, tc := range cases {
			sendCommand(t, pw, "tp @s 3 160 3")
			sendCommand(t, pw, "fill -2 160 -2 2 163 2 air")
			sendCommand(t, pw, "fill -2 159 -2 2 159 2 stone")
			sendCommand(t, pw, "setblock 0 160 -1 stone")
			if tc.straight {
				sendCommand(t, pw, "setblock 0 160 1 stone")
			}
			sendCommand(t, pw, "setblock 0 160 0 "+tc.block)
			marker := fmt.Sprintf("ARMCONNECTED%d", i)
			sendCommand(t, pw, fmt.Sprintf("execute if block 0 160 0 %s[%s] run say %s", tc.block, tc.properties, marker))
			await(marker)
			for _, target := range []float64{tc.clear, tc.blocked} {
				sendCommand(t, pw, "tp @s 1.4 160 0.5")
				marker = fmt.Sprintf("ARMPREP%d", len(out))
				sendCommand(t, pw, "say "+marker)
				await(marker)
				time.Sleep(150 * time.Millisecond)
				var b protocol.Buffer
				b.Double(target)
				b.Double(160)
				b.Double(.5)
				b.Byte(0)
				if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
				time.Sleep(120 * time.Millisecond)
				x, _ := dataGetVec2(t, sink, pw, "@s", "Pos")
				out = append(out, x)
			}
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, tc := range cases {
		for offset, want := range []float64{tc.clear, 1.4} {
			index := i*2 + offset
			if math.Abs(vanilla[index]-want) > 1e-6 || math.Abs(mcgo[index]-vanilla[index]) > 1e-6 {
				t.Errorf("%s probe%d vanilla%v mcgo%v want%v", tc.name, offset, vanilla[index], mcgo[index], want)
			}
		}
	}
}
