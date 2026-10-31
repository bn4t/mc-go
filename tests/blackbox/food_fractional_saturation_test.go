package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestFractionalSaturationDrainParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("FoodFraction")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "FoodFraction")
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
					t.Fatal("fractional food barrier absent", marker)
				}
				return lines
			}
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s"} {
				sendCommand(t, pw, cmd)
			}
			expect := func(condition, marker string) {
				sendCommand(t, pw, condition+" run say "+marker)
				sendCommand(t, pw, "say "+marker+"END")
				if !structureSaidBB(collect(marker+"END"), marker) {
					t.Fatal("fractional setup receipt absent", marker)
				}
			}
			expect(`execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f,foodExhaustionLevel:0.0f}`, "FRACINITIAL")
			sequence := int32(0)
			drink := func(duration, ticks int, marker string) {
				sendCommand(t, pw, fmt.Sprintf(`item replace entity @s weapon.mainhand with minecraft:potion[minecraft:potion_contents={custom_effects:[{id:"minecraft:hunger",duration:%d,amplifier:199}]}]`, duration))
				expect(`execute if items entity @s weapon.mainhand minecraft:potion`, marker+"HELD")
				sequence++
				sendUseItemAim(t, pw, 0, sequence, 0, 0)
				waitPlayerTicksBB(t, pw, collect, marker+"SETTLED", ticks)
			}
			// Twenty-five integer pulses spend six budget quanta, yielding a real
			// hunger point so an ordinary cookie can be eaten without custom food data.
			drink(25, 70, "FRACPREP")
			expect(`execute if items entity @s weapon.mainhand minecraft:glass_bottle if data entity @s {foodLevel:19,foodSaturationLevel:0.0f,foodExhaustionLevel:1.0f} unless data entity @s {active_effects:[{id:"minecraft:hunger"}]}`, "FRACEMPTY")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:cookie")
			expect(`execute if items entity @s weapon.mainhand minecraft:cookie`, "FRACCOOKIE")
			sequence++
			sendUseItemAim(t, pw, 0, sequence, 0, 0)
			waitPlayerTicksBB(t, pw, collect, "FRACEATEN", 45)
			expect(`execute unless items entity @s weapon.mainhand minecraft:cookie if data entity @s {foodLevel:20,foodSaturationLevel:0.4f,foodExhaustionLevel:1.0f}`, "FRACPOOL")
			drink(4, 45, "FRACDRAIN")
			sendCommand(t, pw, `execute if items entity @s weapon.mainhand minecraft:glass_bottle if data entity @s {foodLevel:20,foodSaturationLevel:0.0f,foodExhaustionLevel:1.0f} unless data entity @s {active_effects:[{id:"minecraft:hunger"}]} run say FRACCLAMPED`)
			sendCommand(t, pw, "say FRACEND")
			if !structureSaidBB(collect("FRACEND"), "FRACCLAMPED") {
				t.Error("one budget quantum should clamp saturation0.4 to0, retaining food20/exhaustion1 and no active Hunger")
			}
		})
	}
}
