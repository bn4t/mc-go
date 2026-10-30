package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// A second effect is a positive receipt that the nearby mob received the
// splash; merely observing no Saturation would also fit a missed throw.
func TestSaturationSplashDirectDoseParity(t *testing.T) {
	names := []string{"SatSplashZero", "SatSplashLong"}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			for i, duration := range []int{0, 200} {
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
							t.Fatal("splash barrier absent", marker)
						}
						return lines
					}
					setupPlayerTickBarrierBB(t, pw)
					for _, cmd := range []string{"difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s"} {
						sendCommand(t, pw, cmd)
					}
					tag := fmt.Sprintf("SatSplashNPC%d", i)
					sendCommand(t, pw, fmt.Sprintf(`execute at @s run summon minecraft:villager ~2 ~ ~ {NoAI:1b,NoGravity:1b,Health:20f,Tags:["%s"]}`, tag))
					sendCommand(t, pw, fmt.Sprintf(`execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f} if data entity @e[type=minecraft:villager,tag=%s,limit=1] {Health:20f} run say SATSPLASHINITIAL`, tag))
					sendCommand(t, pw, "say SATSPLASHREADY")
					if !structureSaidBB(collect("SATSPLASHREADY"), "SATSPLASHINITIAL") {
						t.Fatal("initial player/mob pools absent")
					}
					sendCommand(t, pw, fmt.Sprintf(`item replace entity @s weapon.mainhand with minecraft:splash_potion[minecraft:potion_contents={custom_effects:[{id:"minecraft:saturation",duration:%d},{id:"minecraft:glowing",duration:1000}]}]`, duration))
					sendCommand(t, pw, `execute if items entity @s weapon.mainhand minecraft:splash_potion run say SATSPLASHHELD`)
					sendCommand(t, pw, "say SATSPLASHITEM")
					if !structureSaidBB(collect("SATSPLASHITEM"), "SATSPLASHHELD") {
						t.Fatal("splash item not established")
					}
					sendUseItemAim(t, pw, 0, 1, 0, 90)
					waitPlayerTicksBB(t, pw, collect, "SATSPLASHSETTLED", 45)
					sendCommand(t, pw, fmt.Sprintf(`execute unless items entity @s weapon.mainhand minecraft:splash_potion if data entity @s {foodLevel:20,foodSaturationLevel:7.0f} unless data entity @s {active_effects:[{id:"minecraft:saturation"}]} if data entity @e[type=minecraft:villager,tag=%s,limit=1] {Health:20f,active_effects:[{id:"minecraft:glowing"}]} unless data entity @e[type=minecraft:villager,tag=%s,limit=1] {active_effects:[{id:"minecraft:saturation"}]} unless entity @e[type=minecraft:splash_potion] run say SATSPLASHDOSE`, tag, tag))
					sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:7.0f} run say SATSPLASHPOOL`)
					sendCommand(t, pw, fmt.Sprintf(`execute if data entity @e[type=minecraft:villager,tag=%s,limit=1] {Health:20f,active_effects:[{id:"minecraft:glowing"}]} run say SATSPLASHMOB`, tag))
					sendCommand(t, pw, fmt.Sprintf(`execute unless data entity @e[type=minecraft:villager,tag=%s,limit=1] {active_effects:[{id:"minecraft:saturation"}]} run say SATSPLASHNOSTATUS`, tag))
					sendCommand(t, pw, "say SATSPLASHEND")
					lines := collect("SATSPLASHEND")
					if !structureSaidBB(lines, "SATSPLASHDOSE") {
						t.Errorf("duration%d: playerPool=%v mobDose=%v mobNoSaturation=%v", duration, structureSaidBB(lines, "SATSPLASHPOOL"), structureSaidBB(lines, "SATSPLASHMOB"), structureSaidBB(lines, "SATSPLASHNOSTATUS"))
					}
				})
			}
		})
	}
}
