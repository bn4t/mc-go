package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// World age stays frozen; every exposure uses admitted player frames. Real
// lava establishes burning before powder entry, without player NBT mutation.
func TestPlayerBurningPowderMeltParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("PowMelt")}, nil)
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", tg.vanilla}, {"go", tg.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "PowMelt")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			sink.cap = 1 << 20
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
			tp := func(x, z float64) {
				sendCommand(t, pw, fmt.Sprintf("tp @s %.3f -50.000 %.3f", x, z))
				expect(fmt.Sprintf("execute if data entity @s {Pos:[%gd,-50d,%gd]}", x, z))
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
			expect("execute if block 4 -50 2 lava if block 4 -49 2 lava if block 4 -48 2 lava if block 2 -51 2 stone")
			tp(2.5, 2.5)
			sendCommand(t, pw, "execute store result score @s MeltWorldA run time query gametime")
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "attribute @s max_health base set 100")
			sendCommand(t, pw, "effect give @s instant_health 1 10 true")
			advance(3)
			expect(`execute if data entity @s {Health:100.0f,playerGameType:0} unless data entity @s {active_effects:[{id:"minecraft:instant_health"}]}`)
			sendCommand(t, pw, "setblock 8 -50 2 powder_snow")
			expect("execute if block 8 -50 2 powder_snow")
			// Nonburning contact freezes and must leave the snow intact.
			tp(8.5, 2.5)
			advance(10)
			sendCommand(t, pw, "execute store result score @s MeltFrozen run data get entity @s TicksFrozen")
			expect("execute if block 8 -50 2 powder_snow if score @s MeltFrozen matches 10..")
			tp(2.5, 2.5)
			advance(30)
			tp(4.5, 2.5)
			advance(6)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			// Air alone retains the established positive burn.
			tp(2.5, 2.5)
			advance(3)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if score @s MeltFire matches 1..")
			tp(8.5, 2.5)
			advance(6)
			sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
			expect("execute if block 8 -50 2 air if score @s MeltFire matches ..0")
			expect("execute unless data entity @s TicksFrozen")
			sendCommand(t, pw, "execute store result score @s MeltHealth run data get entity @s Health")
			expect("execute if score @s MeltHealth matches 1..99")
			sendCommand(t, pw, "execute store result score @s MeltWorldB run time query gametime")
			expect("execute if score @s MeltWorldA = @s MeltWorldB")
			tp(2.5, 2.5)

			// Multiple ordinary body overlaps share one burning entry. Query
			// every cell independently before asserting any aggregate result.
			for _, shape := range []struct {
				name  string
				cells [][2]int
				x, z  float64
			}{
				{"two", [][2]int{{8, 2}, {9, 2}}, 9, 2.5},
				{"four", [][2]int{{8, 2}, {8, 3}, {9, 2}, {9, 3}}, 9, 3},
			} {
				tp(2.5, 2.5)
				for _, cell := range shape.cells {
					sendCommand(t, pw, fmt.Sprintf("setblock %d -50 %d powder_snow", cell[0], cell[1]))
					expect(fmt.Sprintf("execute if block %d -50 %d powder_snow", cell[0], cell[1]))
				}
				sendCommand(t, pw, "setblock 11 -50 4 powder_snow")
				expect("execute if block 11 -50 4 powder_snow")
				tp(4.5, 2.5)
				advance(6)
				sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
				expect("execute if score @s MeltFire matches 1..")
				tp(2.5, 2.5)
				advance(3)
				sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
				expect("execute if score @s MeltFire matches 1..")
				tp(shape.x, shape.z)
				advance(6)
				seq++
				prefix := fmt.Sprintf("MULTI%d", seq)
				for i, cell := range shape.cells {
					sendCommand(t, pw, fmt.Sprintf("execute if block %d -50 %d air run say %sCELL%dAIR", cell[0], cell[1], prefix, i))
					sendCommand(t, pw, fmt.Sprintf("execute if block %d -50 %d powder_snow run say %sCELL%dSNOW", cell[0], cell[1], prefix, i))
				}
				sendCommand(t, pw, "say "+prefix+"END")
				lines := collect(prefix + "END")
				expect(fmt.Sprintf("execute if data entity @s {Pos:[%gd,-50d,%gd]}", shape.x, shape.z))
				for i, cell := range shape.cells {
					air := structureSaidBB(lines, fmt.Sprintf("%sCELL%dAIR", prefix, i))
					snow := structureSaidBB(lines, fmt.Sprintf("%sCELL%dSNOW", prefix, i))
					t.Logf("%s %s cell(%d,-50,%d): air=%v snow=%v", leg.name, shape.name, cell[0], cell[1], air, snow)
					if air == snow {
						t.Errorf("missing or duplicate typed cell receipt")
					}
					if !air {
						t.Errorf("contacted cell did not melt: native two/four-cell measurement requires air")
					}

				}
				expect("execute if block 11 -50 4 powder_snow")
				sendCommand(t, pw, "execute store result score @s MeltFire run data get entity @s Fire")
				expect("execute if score @s MeltFire matches ..0")
				sendCommand(t, pw, "data get entity @s")
				seq++
				stateMarker := fmt.Sprintf("MULTISTATE%d", seq)
				sendCommand(t, pw, "say "+stateMarker)
				foundState := false
				for _, line := range collect(stateMarker) {
					if strings.HasPrefix(line, "0x79:") && strings.Contains(line, "commands.data.entity.query") {
						foundState = true
						t.Logf("%s %s player state %s", leg.name, shape.name, snbtFromChat(line))
					}
				}
				if !foundState {
					t.Fatal("multi player snapshot missing")
				}
				sendCommand(t, pw, "execute store result score @s MeltHealth run data get entity @s Health")
				expect("execute if score @s MeltHealth matches 1..99")
				sendCommand(t, pw, "execute store result score @s MeltWorldB run time query gametime")
				expect("execute if score @s MeltWorldA = @s MeltWorldB")
				tp(2.5, 2.5)
			}
		})
	}
}
