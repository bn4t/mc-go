package blackbox

import (
	"testing"
	"time"
)

func TestSaturationMobCloudNoStatusParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("SatMobCloud")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "SatMobCloud")
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
					t.Fatal("mob cloud barrier absent", marker)
				}
				return lines
			}
			setupPlayerTickBarrierBB(t, pw)
			sendCommand(t, pw, "tick freeze")
			waitPlayerTicksBB(t, pw, collect, "SATMOBFROZEN", 2)
			for _, cmd := range []string{"gamerule minecraft:spawn_mobs false", "scoreboard objectives add SatMobNow dummy", "scoreboard objectives add SatMobGoal dummy"} {
				sendCommand(t, pw, cmd)
			}
			sendCommand(t, pw, `execute at @s run summon minecraft:villager ~4 ~ ~ {NoAI:1b,NoGravity:1b,Tags:["SatCloudNPC"],Health:20f}`)
			sendCommand(t, pw, `execute at @s run summon minecraft:area_effect_cloud ~4 ~ ~ {Tags:["SatMobDose"],Radius:1f,RadiusOnUse:0f,RadiusPerTick:0f,Duration:100,DurationOnUse:-1000,WaitTime:0,potion_duration_scale:1f,potion_contents:{custom_effects:[{id:"minecraft:saturation",duration:200}]}}`)
			sendCommand(t, pw, `execute if data entity @e[type=minecraft:villager,tag=SatCloudNPC,limit=1] {Health:20f} if entity @e[type=minecraft:area_effect_cloud,tag=SatMobDose] run say SATMOBREADY`)
			sendCommand(t, pw, "say SATMOBSETUP")
			if !structureSaidBB(collect("SATMOBSETUP"), "SATMOBREADY") {
				t.Fatal("mob/cloud ready receipt absent")
			}
			sendCommand(t, pw, "execute store result score @s SatMobGoal run time query gametime")
			sendCommand(t, pw, "scoreboard players add @s SatMobGoal 5")
			sendCommand(t, pw, "tick step 5")
			done := false
			deadline := time.Now().Add(12 * time.Second)
			for !done && time.Now().Before(deadline) {
				sendCommand(t, pw, "execute store result score @s SatMobNow run time query gametime")
				sendCommand(t, pw, "execute if score @s SatMobNow = @s SatMobGoal run say SATMOBSTEPPED")
				sendCommand(t, pw, "say SATMOBPOLL")
				done = structureSaidBB(collect("SATMOBPOLL"), "SATMOBSTEPPED")
			}
			if !done {
				t.Fatal("single five-world-tick step incomplete")
			}
			sendCommand(t, pw, `execute if data entity @e[type=minecraft:villager,tag=SatCloudNPC,limit=1] {Health:20f} unless data entity @e[type=minecraft:villager,tag=SatCloudNPC,limit=1] {active_effects:[{id:"minecraft:saturation"}]} unless entity @e[type=minecraft:area_effect_cloud,tag=SatMobDose] run say SATMOBNOSTATUS`)
			sendCommand(t, pw, `execute if data entity @e[type=minecraft:villager,tag=SatCloudNPC,limit=1] {Health:20f} run say SATMOBHEALTH`)
			sendCommand(t, pw, `execute unless data entity @e[type=minecraft:villager,tag=SatCloudNPC,limit=1] {active_effects:[{id:"minecraft:saturation"}]} run say SATMOBEMPTY`)
			sendCommand(t, pw, `execute unless entity @e[type=minecraft:area_effect_cloud,tag=SatMobDose] run say SATMOBRETIRED`)
			sendCommand(t, pw, "say SATMOBEND")
			lines := collect("SATMOBEND")
			if !structureSaidBB(lines, "SATMOBNOSTATUS") {
				t.Errorf("mob Health20=%v noStatus=%v cloudRetired=%v", structureSaidBB(lines, "SATMOBHEALTH"), structureSaidBB(lines, "SATMOBEMPTY"), structureSaidBB(lines, "SATMOBRETIRED"))
			}
		})
	}
}
