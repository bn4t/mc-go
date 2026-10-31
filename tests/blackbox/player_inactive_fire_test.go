package blackbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// World age stays frozen; every exposure uses admitted player frames. Real
// lava establishes burning before powder entry, without player NBT mutation.
func TestPlayerInactiveFireParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("PowMelt")}, nil)
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "PowMelt")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			collect := func(marker string) []string {
				var lines []string
				until := time.Now().Add(15 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(until) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatal("powder melt receipt absent", marker)
				}
				return lines
			}
			seq := 0
			expect := func(condition string) {
				seq++
				marker := fmt.Sprintf("MELTEXPECT%d", seq)
				sendCommand(t, pw, condition+" run say "+marker)
				sendCommand(t, pw, "say "+marker+"END")
				if !structureSaidBB(collect(marker+"END"), marker) {
					t.Fatal("powder melt outcome", condition)
				}
			}
			advance := func(n int) { seq++; waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("MELTTICKS%d", seq), n) }
			tp := func(x float64) {
				sendCommand(t, pw, fmt.Sprintf("tp @s %.3f -50.000 2.500", x))
				expect(fmt.Sprintf("execute if data entity @s {Pos:[%gd,-50d,2.5d]}", x))
			}

			lastTick := int64(-1)
			snapshot := func(tag string, fire int) {
				expect(fmt.Sprintf("execute if data entity @s {Fire:%ds}", fire))
				sendCommand(t, pw, "data get entity @s Fire")
				sendCommand(t, pw, "scoreboard players get @s BBPlayerTick")
				seq++
				marker := fmt.Sprintf("FIRESTATE%d", seq)
				sendCommand(t, pw, "say "+marker)
				foundTick := false
				for _, line := range collect(marker) {
					if key, args, ok := componentArgs(line); ok && key == "commands.scoreboard.players.get.success" && len(args) > 1 {
						value := args[1]
						if wrapped, ok := value.(map[string]any); ok {
							value = wrapped[""]
						}
						tick, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
						if err != nil || tick <= lastTick {
							t.Fatalf("invalid/nonadvancing actual player counter %v after %d", value, lastTick)
						}
						t.Logf("%s %s actual player ticks=%d delta=%d", leg.name, tag, tick, tick-lastTick)
						lastTick = tick
						foundTick = true
					}
					if strings.HasPrefix(line, "0x79:") && (strings.Contains(line, "commands.data.entity.query") || strings.Contains(line, "commands.scoreboard.players.get.success")) {
						t.Logf("%s %s receipt %s", leg.name, tag, snbtFromChat(line))
					}
				}
				if !foundTick {
					t.Fatal("actual player counter receipt missing")
				}
			}
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"tick freeze", "difficulty normal", "gamerule minecraft:spawn_mobs false", "gamerule minecraft:natural_health_regeneration false", "gamerule minecraft:mob_griefing false", "forceload add 0 0", "scoreboard objectives add MeltWorldA dummy", "scoreboard objectives add MeltWorldB dummy", "scoreboard objectives add MeltFire dummy", "scoreboard objectives add MeltFrozen dummy", "scoreboard objectives add MeltHealth dummy"} {
				sendCommand(t, pw, cmd)
			}
			until := time.Now().Add(20 * time.Second)
			for {
				sendCommand(t, pw, "execute if loaded 8 -50 2 run say MELTLOADED")
				sendCommand(t, pw, "say MELTLOADEND")
				if structureSaidBB(collect("MELTLOADEND"), "MELTLOADED") {
					break
				}
				if time.Now().After(until) {
					t.Fatal("melt arena not loaded")
				}
			}
			sendCommand(t, pw, "fill 0 -51 0 12 -51 5 stone")
			sendCommand(t, pw, "fill 4 -50 2 4 -48 2 lava")
			sendCommand(t, pw, "fill 10 -50 2 10 -48 2 water")
			expect("execute if block 10 -50 2 water if block 10 -49 2 water if block 10 -48 2 water")
			expect("execute if block 4 -50 2 lava if block 4 -49 2 lava if block 4 -48 2 lava if block 2 -51 2 stone")
			tp(2.5)
			sendCommand(t, pw, "execute store result score @s MeltWorldA run time query gametime")
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "attribute @s max_health base set 100")
			sendCommand(t, pw, "effect give @s instant_health 1 10 true")
			advance(3)
			expect(`execute if data entity @s {Health:100.0f,playerGameType:0} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]}`)
			sendCommand(t, pw, "gamemode creative @s")
			advance(3)
			expect("execute if data entity @s {playerGameType:1,Health:100.0f}")
			snapshot("creative-dry-default", -20)
			advance(6)
			snapshot("creative-dry-six-frames", -20)
			sendCommand(t, pw, "gamemode survival @s")
			advance(3)
			expect("execute if data entity @s {playerGameType:0,Health:100.0f}")
			snapshot("ordinary-default", -20)
			advance(6)
			snapshot("ordinary-six-frames", -20)
			sendCommand(t, pw, "setblock 8 -50 2 powder_snow")
			expect("execute if block 8 -50 2 powder_snow")
			// Nonburning contact freezes and must leave the snow intact.
			tp(8.5)
			advance(10)
			sendCommand(t, pw, "execute store result score @s MeltFrozen run data get entity @s TicksFrozen")
			expect("execute if block 8 -50 2 powder_snow if score @s MeltFrozen matches 10..")
			tp(2.5)
			advance(30)
			tp(4.5)
			advance(6)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			// Air alone retains the established positive burn.
			tp(2.5)
			advance(3)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			tp(8.5)
			advance(1)
			snapshot("powder-one-admitted-frame", -20)
			advance(5)
			snapshot("powder-six-admitted-frames", -20)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if block 8 -50 2 air if score @s MeltFire matches ..0")
			expect("execute unless data entity @s TicksFrozen")
			sendCommand(t, pw, "execute store result score @s MeltHealth run data get entity @s Health")
			expect("execute if score @s MeltHealth matches 1..99")

			// Water extinguishing uses the same admitted-frame settlement.
			tp(2.5)
			advance(3)
			snapshot("dry-after-powder", -20)
			tp(4.5)
			advance(6)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			tp(10.5)
			advance(1)
			snapshot("water-one-admitted-frame", -20)
			advance(5)
			snapshot("water-six-admitted-frames", -20)
			tp(2.5)
			advance(6)
			snapshot("dry-after-water", -20)
			// Natural expiry is measured only after enough actual player
			// frames to cover the last independently positive lava burn.
			tp(4.5)
			advance(6)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			tp(2.5)
			advance(3)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			advance(150)
			advance(150)
			advance(20)
			snapshot("natural-expiry-air", -20)
			expect("execute if data entity @s {Air:300s}")
			sendCommand(t, pw, "execute store result score @s MeltHealth run data get entity @s Health")
			expect("execute if score @s MeltHealth matches 1..99")
			sendCommand(t, pw, "setblock 12 -50 2 fire")
			expect("execute if block 12 -50 2 fire")
			tp(12.5)
			sawWarmup, sawIgnition := false, false
			for attempt := 0; attempt < 30 && !sawIgnition; attempt++ {
				sendCommand(t, pw, "data get entity @s Fire")
				seq++
				tag := fmt.Sprintf("WARMREAD%d", seq)
				sendCommand(t, pw, "say "+tag)
				for _, line := range collect(tag) {
					if key, _, ok := componentArgs(line); ok && key == "commands.data.entity.query" {
						scalar := strings.TrimSuffix(strings.TrimPrefix(snbtFromChat(line), "PowMelt"), "s")
						fire, err := strconv.Atoi(scalar)
						if err != nil {
							t.Fatal("warmup fire scalar", scalar)
						}
						t.Logf("%s real fire contact sample=%d", leg.name, fire)
						if fire > -20 && fire <= 0 {
							sawWarmup = true
						}
						if fire > 0 {
							sawIgnition = true
						}
					}
				}
				if !sawIgnition {
					advance(1)
				}
			}
			if leg.name == "native" && !sawWarmup {
				t.Error("native negative fire warmup was not observed")
			}
			if !sawIgnition {
				t.Fatal("real fire contact did not ignite")
			}
			t.Logf("%s warmup-observed=%v ignition-observed=%v (Go early warmup timing is not claimed)", leg.name, sawWarmup, sawIgnition)
			advance(3)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			tp(10.5)
			advance(6)
			snapshot("water-after-fire-control", -20)
			tp(2.5)
			sendCommand(t, pw, "execute store result score @s MeltWorldB run time query gametime")
			expect("execute if score @s MeltWorldA = @s MeltWorldB")
			tp(2.5)
		})
	}
}
