package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// Every action is sent once; exact state receipts and actual player ticks
// distinguish healing limits from command parsing and settlement.
func TestPlayerMaxHealthConsumersParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("HealthAttrs")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "HealthAttrs")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, leg.addr, conn, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(12 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !structureSaidBB(lines, marker) {
					t.Fatalf("missing exact barrier %s", marker)
				}
				return lines
			}
			receipt := func(name string, health, base, maximum float64) {
				serial++
				good, end := fmt.Sprintf("MAXHEALTH%s%dX", name, serial), fmt.Sprintf("MAXHEALTHEND%dX", serial)
				raw := good + "RAW"
				sendCommand(t, pw, "execute store result score @s HealthProof run attribute @s max_health base get 1000")
				sendCommand(t, pw, fmt.Sprintf("execute if score @s HealthProof matches %.0f run say %s", base*1000, raw))
				sendCommand(t, pw, "execute store result score @s HealthProof run attribute @s max_health get 1000")
				sendCommand(t, pw, fmt.Sprintf("execute if score @s HealthProof matches %.0f if data entity @s {Health:%.3ff} run say %s", maximum*1000, health, good))
				sendCommand(t, pw, "say "+end)
				lines := collect(end)
				ok := structureSaidBB(lines, good) && structureSaidBB(lines, raw)
				t.Logf("%s health%g/max%g=%v", name, health, maximum, ok)
				if !ok {
					t.Errorf("%s health%g/max%g receipt absent", name, health, maximum)
				}
			}
			healSerial := 0
			heal := func(amplifier int) {
				healSerial++
				sendCommand(t, pw, "effect clear @s instant_health")
				sendCommand(t, pw, fmt.Sprintf("effect give @s instant_health 1 %d true", amplifier))
				waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("HEALTHINSTANT%dX", healSerial), 2)
			}
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"difficulty peaceful", "gamerule minecraft:natural_health_regeneration false", "scoreboard objectives add HealthProof dummy", "gamemode survival @s", "effect clear @s", "attribute @s max_health base set 30"} {
				sendCommand(t, pw, cmd)
			}
			waitPlayerTicksBB(t, pw, collect, "HEALTHBASESET", 2)
			receipt("BASEONLY", 20, 30, 30)
			heal(0)
			receipt("INSTANTPARTIAL", 24, 30, 30)
			heal(10)
			receipt("INSTANTCAP", 30, 30, 30)
			sendCommand(t, pw, "damage @s 6 minecraft:generic")
			receipt("DAMAGE", 24, 30, 30)
			sendCommand(t, pw, "effect give @s regeneration 1 5 true")
			waitPlayerTicksBB(t, pw, collect, "HEALTHREGEN", 25)
			receipt("REGENCAP", 30, 30, 30)
			sendCommand(t, pw, "effect give @s health_boost 60 1 true")
			waitPlayerTicksBB(t, pw, collect, "HEALTHBOOST", 2)
			receipt("BOOSTNOHEAL", 30, 30, 38)
			heal(10)
			receipt("BOOSTHEAL", 38, 30, 38)
			sendCommand(t, pw, "effect clear @s health_boost")
			waitPlayerTicksBB(t, pw, collect, "HEALTHBOOSTREMOVED", 2)
			receipt("BOOSTREMOVAL", 30, 30, 30)
			sendCommand(t, pw, "attribute @s max_health base set 10")
			waitPlayerTicksBB(t, pw, collect, "HEALTHLOWER", 2)
			receipt("BASELOWER", 10, 10, 10)
			heal(10)
			receipt("LOWHEAL", 10, 10, 10)
			sendCommand(t, pw, "attribute @s max_health modifier add test:health 4 add_value")
			heal(10)
			receipt("COMMANDMODHEAL", 14, 10, 14)
			sendCommand(t, pw, "attribute @s max_health modifier remove test:health")
			waitPlayerTicksBB(t, pw, collect, "HEALTHMODREMOVED", 2)
			receipt("COMMANDMODREMOVAL", 10, 10, 10)
			sendCommand(t, pw, "attribute @s max_health base set 10.5")
			heal(10)
			receipt("FRACTIONALHEAL", 10.5, 10.5, 10.5)
			waitPlayerTicksBB(t, pw, collect, "HEALTHFRACTIONALIMMUNITY", 25)
			sendCommand(t, pw, "damage @s 0.25 minecraft:generic")
			receipt("FRACTIONALDAMAGE", 10.25, 10.5, 10.5)
			sendCommand(t, pw, "effect give @s regeneration 1 5 true")
			waitPlayerTicksBB(t, pw, collect, "HEALTHFRACTIONALREGEN", 3)
			receipt("FRACTIONALREGEN", 10.5, 10.5, 10.5)
			sendCommand(t, pw, "effect clear @s regeneration")
			sendCommand(t, pw, "attribute @s max_health base set 30")
			heal(10)
			sendCommand(t, pw, "effect give @s saturation 1 10 true")
			waitPlayerTicksBB(t, pw, collect, "HEALTHNATURALSTART", 25)
			sendCommand(t, pw, "damage @s 6 minecraft:generic")
			receipt("NATURALDAMAGE", 24, 30, 30)
			sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration true")
			waitPlayerTicksBB(t, pw, collect, "HEALTHNATURAL", 100)
			receipt("NATURALCAP", 30, 30, 30)
		})
	}
}
