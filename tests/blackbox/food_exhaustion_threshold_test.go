package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// Hunger199 adds exactly one exhaustion per living tick. Short bottled
// instances establish3/4/5 without mutating player NBT or action-time sampling.
func TestFoodExhaustionThresholdParity(t *testing.T) {
	names := []string{"FoodEdgeBelow", "FoodEdgeExact", "FoodEdgeAbove"}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			for i, c := range []struct{ duration, exhaustion, saturation int }{{3, 3, 5}, {4, 4, 5}, {5, 1, 4}} {
				t.Run(names[i], func(t *testing.T) {
					conn, pr, pw := joinPlayBlackbox(t, leg.addr, names[i])
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
							t.Fatal("food boundary barrier absent", marker)
						}
						return lines
					}
					setupPlayerTickBarrierBB(t, pw)
					for _, cmd := range []string{"difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s"} {
						sendCommand(t, pw, cmd)
					}
					sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f,foodExhaustionLevel:0.0f} run say FOODBOUNDINITIAL`)
					sendCommand(t, pw, "say FOODBOUNDREADY")
					if !structureSaidBB(collect("FOODBOUNDREADY"), "FOODBOUNDINITIAL") {
						t.Fatal("fresh food pools absent")
					}
					sendCommand(t, pw, fmt.Sprintf(`item replace entity @s weapon.mainhand with minecraft:potion[minecraft:potion_contents={custom_effects:[{id:"minecraft:hunger",duration:%d,amplifier:199}]}]`, c.duration))
					sendCommand(t, pw, `execute if items entity @s weapon.mainhand minecraft:potion run say FOODBOUNDBOTTLED`)
					sendCommand(t, pw, "say FOODBOUNDITEM")
					if !structureSaidBB(collect("FOODBOUNDITEM"), "FOODBOUNDBOTTLED") {
						t.Fatal("potion not established")
					}
					sendUseItemAim(t, pw, 0, 1, 0, 0)
					waitPlayerTicksBB(t, pw, collect, "FOODBOUNDSETTLED", 45)
					sendCommand(t, pw, fmt.Sprintf(`execute if items entity @s weapon.mainhand minecraft:glass_bottle if data entity @s {foodLevel:20,foodSaturationLevel:%d.0f,foodExhaustionLevel:%d.0f} unless data entity @s {active_effects:[{id:"minecraft:hunger"}]} run say FOODBOUNDVALUE`, c.saturation, c.exhaustion))
					sendCommand(t, pw, "say FOODBOUNDEND")
					if !structureSaidBB(collect("FOODBOUNDEND"), "FOODBOUNDVALUE") {
						t.Errorf("%d exact Hunger pulses: want food20/saturation%d/exhaustion%d with consumed bottle and no status", c.duration, c.saturation, c.exhaustion)
					}
				})
			}
		})
	}
}
