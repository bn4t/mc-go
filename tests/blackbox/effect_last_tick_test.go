package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestFiniteEffectLastTickParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("FinalPulse")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			conn, pr, pw := joinPlayBlackbox(t, leg.addr, "FinalPulse")
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
					t.Fatal("last-tick barrier absent", marker)
				}
				return lines
			}
			barrier := func(marker string) []string { sendCommand(t, pw, "say "+marker); return collect(marker) }
			setupPlayerTickBarrierBB(t, pw)
			for _, cmd := range []string{"difficulty peaceful", "gamerule minecraft:natural_health_regeneration false", "gamemode survival @s", "attribute @s max_health base set 1000", "scoreboard objectives add LastPulse dummy"} {
				sendCommand(t, pw, cmd)
			}
			phase := 0
			for _, effect := range []string{"regeneration", "poison", "wither"} {
				for _, duration := range []int{0, 1} {
					phase++
					tag := fmt.Sprintf("FinalMob%d", phase)
					target := "@e[tag=" + tag + ",limit=1]"
					start, want, amp := 20, 20, 5
					if effect == "regeneration" {
						start, want = 10, 10
					}
					if effect == "poison" {
						amp = 4
					}
					if duration == 1 {
						if effect == "regeneration" {
							want++
						} else {
							want--
						}
					}
					sendCommand(t, pw, fmt.Sprintf(`summon villager ~2 ~ ~ {NoAI:1b,NoGravity:1b,Silent:1b,Health:%d.0f,Tags:["%s"],active_effects:[{id:"minecraft:%s",duration:%d,amplifier:%db}]}`, start, tag, effect, duration, amp))
					ready, end := fmt.Sprintf("LASTSPAWN%dX", phase), fmt.Sprintf("LASTSPAWNEND%dX", phase)
					sendCommand(t, pw, "execute if entity "+target+" run say "+ready)
					if !structureSaidBB(barrier(end), ready) {
						t.Fatal("actual spawned body absent", tag)
					}
					waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("LASTWAIT%dX", phase), 5)
					good, end := fmt.Sprintf("LASTPROOF%dX", phase), fmt.Sprintf("LASTEND%dX", phase)
					sendCommand(t, pw, fmt.Sprintf(`execute if data entity %s {Health:%d.0f} unless data entity %s {active_effects:[{id:"minecraft:%s"}]} run say %s`, target, want, target, effect, good))
					pass := structureSaidBB(barrier(end), good)
					t.Logf("%s duration%d health%d=%v", effect, duration, want, pass)
					if !pass {
						t.Errorf("%s duration%d final-pulse mismatch", effect, duration)
					}
				}
			}
			sendCommand(t, pw, `execute if data entity @s {Health:20.0f,foodExhaustionLevel:0.0f} run say LASTPLAYERINITIAL`)
			if !structureSaidBB(barrier("LASTPLAYERSTART"), "LASTPLAYERINITIAL") {
				t.Fatal("uncapped player pools not established")
			}
			sendCommand(t, pw, "effect give @s regeneration 1 5 true")
			waitPlayerTicksBB(t, pw, collect, "LASTPLAYERREGEN", 25)
			sendCommand(t, pw, `execute if data entity @s {Health:40.0f} unless data entity @s {active_effects:[{id:"minecraft:regeneration"}]} run say LASTPLAYERHEAL`)
			if !structureSaidBB(barrier("LASTPLAYERHEALEND"), "LASTPLAYERHEAL") {
				t.Error("uncapped20-pulse regeneration mismatch")
			}
			sendCommand(t, pw, "effect give @s hunger 1 0 true")
			waitPlayerTicksBB(t, pw, collect, "LASTPLAYERHUNGER", 25)
			sendCommand(t, pw, "execute store result score @s LastPulse run data get entity @s foodExhaustionLevel 1000000")
			sendCommand(t, pw, `execute if score @s LastPulse matches 99990..100010 unless data entity @s {active_effects:[{id:"minecraft:hunger"}]} run say LASTPLAYEREXHAUST`)
			if !structureSaidBB(barrier("LASTPLAYEREXHAUSTEND"), "LASTPLAYEREXHAUST") {
				t.Error("20-pulse exhaustion cardinality mismatch")
			}
		})
	}
}
