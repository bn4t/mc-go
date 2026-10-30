package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestPeacefulFoodExhaustionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("PeacefulFood")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "PeacefulFood")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkTP(pr, pw)
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(12 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatal("food barrier absent", marker)
				}
				return lines
			}
			barrier := func(marker string) []string { sendCommand(t, pw, "say "+marker); return collect(marker) }
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "difficulty peaceful", "gamemode survival @s"} {
				sendCommand(t, pw, cmd)
			}
			sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f,foodExhaustionLevel:0.0f} run say PEACEFOODINITIAL`)
			if !structureSaidBB(barrier("PEACEFOODSTART"), "PEACEFOODINITIAL") {
				t.Fatal("initial food pools not established")
			}
			for i, c := range []struct {
				difficulty string
				food       int
			}{{"peaceful", 20}, {"normal", 14}} {
				sendCommand(t, pw, "difficulty "+c.difficulty)
				sendCommand(t, pw, "effect give @s hunger 1 255 true")
				given := fmt.Sprintf("PEACEFOODGIVEN%dX", i)
				received := false
				eid, _ := server.MobEffectID("hunger")
				for _, line := range barrier(given) {
					prefix, body, ok := strings.Cut(line, ":")
					if !ok || prefix != fmt.Sprintf("%#x", server.IDPlayUpdateMobEffect) {
						continue
					}
					r := protocol.NewPacketReader([]byte(body))
					_, e0 := r.VarInt()
					id, e1 := r.VarInt()
					amp, e2 := r.VarInt()
					duration, e3 := r.VarInt()
					_, e4 := r.Byte()
					if e0 == nil && e1 == nil && e2 == nil && e3 == nil && e4 == nil && r.Remaining() == 0 && id == eid && amp == 255 && duration == 20 {
						received = true
					}
				}
				if !received {
					t.Fatal("actual hunger255/duration20 receipt absent", c.difficulty)
				}
				waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("PEACEFOODWAIT%dX", i), 25)
				good, end := fmt.Sprintf("PEACEFOODPROOF%dX", i), fmt.Sprintf("PEACEFOODEND%dX", i)
				sendCommand(t, pw, fmt.Sprintf(`execute if data entity @s {foodLevel:%d,foodSaturationLevel:0.0f} unless data entity @s {active_effects:[{id:"minecraft:hunger"}]} run say %s`, c.food, good))
				if !structureSaidBB(barrier(end), good) {
					t.Errorf("%s food%d/saturation0 receipt absent", c.difficulty, c.food)
				}
			}
		})
	}
}
