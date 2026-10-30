package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestSaturationCloudDirectDoseParity(t *testing.T) {
	names := []string{"SatCloudZero", "SatCloudLong"}
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
							t.Fatal("cloud barrier absent", marker)
						}
						return lines
					}
					setupPlayerTickBarrierBB(t, pw)
					sendCommand(t, pw, "tick freeze")
					waitPlayerTicksBB(t, pw, collect, "SATCLOUDFROZEN", 2)
					for _, cmd := range []string{"difficulty normal", "gamerule minecraft:natural_health_regeneration false", "gamerule minecraft:spawn_mobs false", "gamemode survival @s"} {
						sendCommand(t, pw, cmd)
					}
					if i > 0 {
						retired := false
						deadline := time.Now().Add(12 * time.Second)
						for !retired && time.Now().Before(deadline) {
							sendCommand(t, pw, "execute unless entity @a[name=SatCloudZero] run say SATCLOUDOLDLEFT")
							sendCommand(t, pw, "say SATCLOUDRETIREPOLL")
							retired = structureSaidBB(collect("SATCLOUDRETIREPOLL"), "SATCLOUDOLDLEFT")
						}
						if !retired {
							t.Fatal("previous receiver remains present")
						}
					}
					if i == 0 {
						sendCommand(t, pw, "scoreboard objectives add SatCloudNow dummy")
						sendCommand(t, pw, "scoreboard objectives add SatCloudGoal dummy")
					}
					sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:5.0f} run say SATCLOUDINITIAL`)
					sendCommand(t, pw, "say SATCLOUDREADY")
					if !structureSaidBB(collect("SATCLOUDREADY"), "SATCLOUDINITIAL") {
						t.Fatal("initial food pools absent")
					}
					sendCommand(t, pw, fmt.Sprintf(`execute at @s run summon minecraft:area_effect_cloud ~ ~ ~ {Tags:["SatDirectCloud"],Radius:1f,RadiusOnUse:0f,RadiusPerTick:0f,Duration:100,DurationOnUse:-1000,WaitTime:0,potion_duration_scale:0f,potion_contents:{custom_effects:[{id:"minecraft:saturation",duration:%d}]}}`, duration))
					sendCommand(t, pw, `execute if entity @e[type=minecraft:area_effect_cloud,tag=SatDirectCloud] run say SATCLOUDSPAWN`)
					sendCommand(t, pw, "say SATCLOUDSETUP")
					if !structureSaidBB(collect("SATCLOUDSETUP"), "SATCLOUDSPAWN") {
						t.Fatal("cloud spawn not observed")
					}
					sendCommand(t, pw, "execute store result score @s SatCloudGoal run time query gametime")
					sendCommand(t, pw, "scoreboard players add @s SatCloudGoal 5")
					sendCommand(t, pw, "tick step 5")
					done := false
					deadline := time.Now().Add(12 * time.Second)
					for !done && time.Now().Before(deadline) {
						sendCommand(t, pw, "execute store result score @s SatCloudNow run time query gametime")
						sendCommand(t, pw, "execute if score @s SatCloudNow = @s SatCloudGoal run say SATCLOUDSTEPPED")
						sendCommand(t, pw, "say SATCLOUDPOLL")
						done = structureSaidBB(collect("SATCLOUDPOLL"), "SATCLOUDSTEPPED")
					}
					if !done {
						t.Fatal("single five-tick step incomplete")
					}
					sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:7.0f} unless data entity @s {active_effects:[{id:"minecraft:saturation"}]} unless entity @e[type=minecraft:area_effect_cloud,tag=SatDirectCloud] run say SATCLOUDDOSE`)
					sendCommand(t, pw, `execute if data entity @s {foodLevel:20,foodSaturationLevel:7.0f} run say SATCLOUDPOOL`)
					sendCommand(t, pw, `execute unless data entity @s {active_effects:[{id:"minecraft:saturation"}]} run say SATCLOUDNOSTATUS`)
					sendCommand(t, pw, `execute unless entity @e[type=minecraft:area_effect_cloud,tag=SatDirectCloud] run say SATCLOUDRETIRED`)
					sendCommand(t, pw, "say SATCLOUDEND")
					lines := collect("SATCLOUDEND")
					if !structureSaidBB(lines, "SATCLOUDDOSE") {
						t.Errorf("duration%d: pool=%v noStatus=%v retired=%v", duration, structureSaidBB(lines, "SATCLOUDPOOL"), structureSaidBB(lines, "SATCLOUDNOSTATUS"), structureSaidBB(lines, "SATCLOUDRETIRED"))
					}
				})
			}
		})
	}
}
