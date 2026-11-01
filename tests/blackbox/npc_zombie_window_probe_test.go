package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestNPCZombieSpeedWindowDiagnostic(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("SpeedWindow")}, nil)
	defer targets.cleanup()
	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		conn, pr, pw := joinPlayBlackbox(t, addr, "SpeedWindow")
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		collect := func(marker string) []string {
			var lines []string
			until := time.Now().Add(15 * time.Second)
			for time.Now().Before(until) && !structureSaidBB(lines, marker) {
				lines = append(lines, sink.take()...)
				time.Sleep(10 * time.Millisecond)
			}
			if !structureSaidBB(lines, marker) {
				t.Fatalf("%s missing %s", addr, marker)
			}
			return lines
		}
		for _, cmd := range []string{"tick freeze", "tp @s 0 -60 20", "gamemode survival @s", "effect give @s resistance 100 255 true", "fill -4 -61 -3 4 -61 25 stone", "scoreboard objectives add WindowProof dummy", `summon zombie 0 -60 0 {PersistenceRequired:1b,IsBaby:0b,Tags:["window_zombie"]}`, `attribute @e[tag=window_zombie,limit=1] movement_speed base set 0.1`, `attribute @e[tag=window_zombie,limit=1] follow_range base set 100`} {
			sendCommand(t, pw, cmd)
		}
		step := func(n int, label string) {
			sendCommand(t, pw, "execute store result score #start WindowProof run time query gametime")
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			done := false
			for i := 0; i < 250 && !done; i++ {
				end, yes := fmt.Sprintf("%s_END%dX", label, i), fmt.Sprintf("%s_DONE%dX", label, i)
				sendCommand(t, pw, "execute store result score #now WindowProof run time query gametime")
				sendCommand(t, pw, "scoreboard players operation #now WindowProof -= #start WindowProof")
				sendCommand(t, pw, fmt.Sprintf("execute if score #now WindowProof matches %d.. run say %s", n, yes))
				sendCommand(t, pw, "say "+end)
				done = structureSaidBB(collect(end), yes)
				if !done {
					time.Sleep(40 * time.Millisecond)
				}
			}
			if !done {
				t.Fatal("step incomplete")
			}
		}
		step(40, "WARM")
		receipt := func(label string) {
			sendCommand(t, pw, `scoreboard players set #motion WindowProof -999999`)
			sendCommand(t, pw, `execute store result score #motion WindowProof run data get entity @e[tag=window_zombie,limit=1] Motion[2] 10000`)
			sendCommand(t, pw, `data get entity @e[tag=window_zombie,limit=1] Pos`)
			sendCommand(t, pw, `data get entity @e[tag=window_zombie,limit=1] Motion`)
			sendCommand(t, pw, `execute if score #motion WindowProof matches 1.. if entity @s[gamemode=survival,x=0,y=-60,z=20,distance=..1] if entity @e[tag=window_zombie,x=0,y=-60,z=0,distance=..12] run say `+label+`READY`)
			sendCommand(t, pw, "say "+label+"END")
			out := collect(label + "END")
			t.Logf("%s %s receipt %v", addr, label, out)
			if !structureSaidBB(out, label+"READY") {
				t.Fatalf("%s %s positive chase not established", addr, label)
			}
		}
		receipt("WINDOWBASE")
		sendCommand(t, pw, `execute store result score #z0 WindowProof run data get entity @e[tag=window_zombie,limit=1] Pos[2] 100000`)
		step(10, "BASE")
		sendCommand(t, pw, `execute store result score #z1 WindowProof run data get entity @e[tag=window_zombie,limit=1] Pos[2] 100000`)
		sendCommand(t, pw, `effect give @e[tag=window_zombie,limit=1] speed 30 1 true`)
		sendCommand(t, pw, `execute if data entity @e[tag=window_zombie,limit=1] {active_effects:[{id:"minecraft:speed",amplifier:1b}]} run say WINDOWEFFECT`)
		sendCommand(t, pw, `scoreboard players set #effect WindowProof -999999`)
		sendCommand(t, pw, `execute store result score #effect WindowProof run attribute @e[tag=window_zombie,limit=1] movement_speed modifier value get minecraft:effect.speed 1000`)
		sendCommand(t, pw, `execute if score #effect WindowProof matches 400 run say WINDOWMODIFIER`)
		sendCommand(t, pw, "say WINDOWEFFECTEND")
		effectReceipt := collect("WINDOWEFFECTEND")
		if !structureSaidBB(effectReceipt, "WINDOWEFFECT") || !structureSaidBB(effectReceipt, "WINDOWMODIFIER") {
			t.Fatal("speed effect absent")
		}
		receipt("WINDOWFAST")
		step(10, "FAST")
		sendCommand(t, pw, `execute store result score #z2 WindowProof run data get entity @e[tag=window_zombie,limit=1] Pos[2] 100000`)
		sendCommand(t, pw, `scoreboard players operation #base WindowProof = #z1 WindowProof`)
		sendCommand(t, pw, `scoreboard players operation #base WindowProof -= #z0 WindowProof`)
		sendCommand(t, pw, `scoreboard players operation #fast WindowProof = #z2 WindowProof`)
		sendCommand(t, pw, `scoreboard players operation #fast WindowProof -= #z1 WindowProof`)
		sendCommand(t, pw, `scoreboard players get #base WindowProof`)
		sendCommand(t, pw, `scoreboard players get #fast WindowProof`)
		sendCommand(t, pw, `scoreboard players operation #fast WindowProof -= #base WindowProof`)
		sendCommand(t, pw, `execute if score #base WindowProof matches 100.. if score #fast WindowProof matches 100.. run say WINDOWFASTER`)
		sendCommand(t, pw, "say WINDOWCOMPAREEND")
		out := collect("WINDOWCOMPAREEND")
		t.Logf("%s compare %v", addr, out)
		if !structureSaidBB(out, "WINDOWFASTER") {
			t.Errorf("%s speed window did not advance sufficiently faster", addr)
		}
		conn.Close()
	}
}
