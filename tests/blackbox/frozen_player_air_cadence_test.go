package blackbox

import (
	"testing"
	"time"
)

func TestFrozenPlayerAirCadenceParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("FrozenAir")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "FrozenAir")
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
					t.Fatal("frozen air receipt absent", marker)
				}
				return lines
			}
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"gamemode survival @s", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "tick freeze", "scoreboard objectives add AirBefore dummy", "scoreboard objectives add AirAfter dummy", "scoreboard objectives add AirTickA dummy", "scoreboard objectives add AirElapsed dummy", "scoreboard objectives add AirLost dummy", "scoreboard objectives add AirDiff dummy", "scoreboard objectives add AirWorldA dummy", "scoreboard objectives add AirWorldB dummy", "fill 10 -64 10 10 -54 10 minecraft:water"} {
				sendCommand(t, pw, cmd)
			}
			sendCommand(t, pw, `execute if block 10 -60 10 minecraft:water if data entity @s {Air:300s,Health:20.0f} run say AIRINITIAL`)
			sendCommand(t, pw, "say AIRSETUP")
			if !structureSaidBB(collect("AIRSETUP"), "AIRINITIAL") {
				t.Fatal("air pool and water column not established")
			}
			sendCommand(t, pw, "tp @s 10.5 -61.5 10.5")
			sendCommand(t, pw, "say AIRSUBMERGED")
			collect("AIRSUBMERGED")
			sendCommand(t, pw, "execute store result score @s AirWorldA run time query gametime")
			sendCommand(t, pw, "execute store result score @s AirBefore run data get entity @s Air")
			sendCommand(t, pw, "scoreboard players operation @s AirTickA = @s BBPlayerTick")
			sendCommand(t, pw, "say AIRBEFORE")
			collect("AIRBEFORE")
			waitPlayerTicksBB(t, pw, collect, "AIRWINDOW", 25)
			sendCommand(t, pw, "execute store result score @s AirAfter run data get entity @s Air")
			sendCommand(t, pw, "scoreboard players operation @s AirElapsed = @s BBPlayerTick")
			sendCommand(t, pw, "scoreboard players operation @s AirElapsed -= @s AirTickA")
			sendCommand(t, pw, "scoreboard players operation @s AirLost = @s AirBefore")
			sendCommand(t, pw, "scoreboard players operation @s AirLost -= @s AirAfter")
			sendCommand(t, pw, "scoreboard players operation @s AirDiff = @s AirLost")
			sendCommand(t, pw, "scoreboard players operation @s AirDiff -= @s AirElapsed")
			sendCommand(t, pw, "execute store result score @s AirWorldB run time query gametime")
			sendCommand(t, pw, `execute if score @s AirElapsed matches 25..60 if score @s AirLost matches 20..65 if score @s AirDiff matches -3..3 if score @s AirWorldA = @s AirWorldB if data entity @s {Health:20.0f} run say AIRCADENCE`)
			sendCommand(t, pw, `execute if score @s AirDiff matches -3..3 run say AIRDIFFOK`)
			sendCommand(t, pw, `execute if score @s AirWorldA = @s AirWorldB run say AIRWORLDFROZEN`)
			sendCommand(t, pw, "say AIREND")
			lines := collect("AIREND")
			if !structureSaidBB(lines, "AIRCADENCE") {
				t.Errorf("air drain must follow actual player ticks within paired-sample skew: differenceOK=%v worldFrozen=%v", structureSaidBB(lines, "AIRDIFFOK"), structureSaidBB(lines, "AIRWORLDFROZEN"))
			}
		})
	}
}
