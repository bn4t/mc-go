package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNPCSpeedEffectAttributeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("NPCSpeed")}, nil)
	defer targets.cleanup()
	for _, addr := range []string{targets.vanilla, targets.mcgo} {
		conn, pr, pw := joinPlayBlackbox(t, addr, "NPCSpeed")
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		collect := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) && !structureSaidBB(lines, marker) {
				lines = append(lines, sink.take()...)
				time.Sleep(10 * time.Millisecond)
			}
			if !structureSaidBB(lines, marker) {
				t.Fatalf("%s missing %s", addr, marker)
			}
			return lines
		}
		for _, cmd := range []string{"tick freeze", "scoreboard objectives add NPCProof dummy", `summon minecraft:cow ~ ~ ~ {NoAI:1b,NoGravity:1b,Tags:["npc_speed_probe"]}`} {
			sendCommand(t, pw, cmd)
		}
		target := "@e[type=minecraft:cow,tag=npc_speed_probe,limit=1]"
		checks := []struct {
			label   string
			setup   []string
			queries []string
		}{
			{"base", nil, []string{"base get 1000|200", "get 1000|200"}},
			{"speed", []string{"effect give " + target + " speed 30 0 true"}, []string{"base get 1000|200", "get 1000|240", "modifier value get minecraft:effect.speed 1000|200"}},
			{"replace", []string{"effect give " + target + " speed 30 1 true"}, []string{"get 1000|280", "modifier value get minecraft:effect.speed 1000|400"}},
			{"compose", []string{"effect give " + target + " slowness 30 0 true"}, []string{"get 1000|238", "modifier value get minecraft:effect.slowness 1000|-150"}},
			{"clear", []string{"effect clear " + target + " speed"}, []string{"get 1000|170", "modifier value get minecraft:effect.slowness 1000|-150"}},
			{"clearall", []string{"effect clear " + target}, []string{"get 1000|200"}},
		}
		for i, c := range checks {
			for _, cmd := range c.setup {
				sendCommand(t, pw, cmd)
			}
			for j, q := range c.queries {
				parts := strings.Split(q, "|")
				sendCommand(t, pw, "scoreboard players set @s NPCProof -999999")
				sendCommand(t, pw, "execute store result score @s NPCProof run attribute "+target+" movement_speed "+parts[0])
				yes, end := fmt.Sprintf("NPCYES%d_%dX", i, j), fmt.Sprintf("NPCEND%d_%dX", i, j)
				sendCommand(t, pw, "execute if score @s NPCProof matches "+parts[1]+" run say "+yes)
				sendCommand(t, pw, "say "+end)
				if !structureSaidBB(collect(end), yes) {
					t.Errorf("%s %s %s expected %s", addr, c.label, parts[0], parts[1])
				}
			}

			if c.label == "speed" {
				sendCommand(t, pw, "data get entity "+target+" attributes")
				sendCommand(t, pw, "say NPCRAWATTRS")
				t.Logf("%s live raw attributes: %v", addr, collect("NPCRAWATTRS"))
				sendCommand(t, pw, `execute if data entity `+target+` {attributes:[{id:"minecraft:movement_speed",modifiers:[{id:"minecraft:effect.speed",amount:0.20000000298023224d,operation:"add_multiplied_total"}]}]} run say NPCMODIFIERMATCH`)
				sendCommand(t, pw, "say NPCMODIFIEREND")
				if !structureSaidBB(collect("NPCMODIFIEREND"), "NPCMODIFIERMATCH") {
					t.Errorf("%s raw modifier mismatch", addr)
				}
			}
		}
		sendCommand(t, pw, `summon minecraft:cow ~ ~ ~ {NoAI:1b,NoGravity:1b,Tags:["npc_equal_effect"]}`)
		sendCommand(t, pw, `effect give @e[tag=npc_equal_effect,limit=1] speed 1 0 true`)
		sendCommand(t, pw, `effect give @e[tag=npc_equal_effect,limit=1] speed 1 1 true`)
		sendCommand(t, pw, `execute if data entity @e[tag=npc_equal_effect,limit=1] {active_effects:[{id:"minecraft:speed",amplifier:1b,duration:20}]} run say NPCEQUALREADY`)
		sendCommand(t, pw, "say NPCEQUALREADYEND")
		if !structureSaidBB(collect("NPCEQUALREADYEND"), "NPCEQUALREADY") {
			t.Fatalf("%s equal-duration setup failed", addr)
		}
		sendCommand(t, pw, "execute store result score #start NPCProof run time query gametime")
		sendCommand(t, pw, "tick step 20")
		equalDone := false
		for n := 0; n < 160 && !equalDone; n++ {
			end, yes := fmt.Sprintf("NPCEQUALSTEPEND%dX", n), fmt.Sprintf("NPCEQUALSTEPYES%dX", n)
			sendCommand(t, pw, "execute store result score #now NPCProof run time query gametime")
			sendCommand(t, pw, "scoreboard players operation #now NPCProof -= #start NPCProof")
			sendCommand(t, pw, "execute if score #now NPCProof matches 20.. run say "+yes)
			sendCommand(t, pw, "say "+end)
			equalDone = structureSaidBB(collect(end), yes)
			if !equalDone {
				time.Sleep(40 * time.Millisecond)
			}
		}
		if !equalDone {
			t.Fatal("equal-duration step incomplete")
		}
		sendCommand(t, pw, `execute store success score @s NPCProof run attribute @e[tag=npc_equal_effect,limit=1] movement_speed modifier value get minecraft:effect.speed`)
		sendCommand(t, pw, `execute if score @s NPCProof matches 0 unless data entity @e[tag=npc_equal_effect,limit=1] {active_effects:[{id:"minecraft:speed"}]} run say NPCEQUALGONE`)
		sendCommand(t, pw, "say NPCEQUALEND")
		if !structureSaidBB(collect("NPCEQUALEND"), "NPCEQUALGONE") {
			t.Errorf("%s equal duration left effect or modifier", addr)
		}
		sendCommand(t, pw, `summon minecraft:cow ~ ~ ~ {NoAI:1b,NoGravity:1b,Tags:["npc_raw_effect"],Age:-100,active_effects:[{id:"minecraft:speed",duration:200,amplifier:0b}]}`)
		sendCommand(t, pw, `attribute @e[type=minecraft:cow,tag=npc_raw_effect,limit=1] movement_speed get`)
		sendCommand(t, pw, `data get entity @e[type=minecraft:cow,tag=npc_raw_effect,limit=1] attributes`)
		sendCommand(t, pw, "say NPCRAWEND")
		t.Logf("%s raw summon diagnostics: %v", addr, collect("NPCRAWEND"))
		sendCommand(t, pw, `execute if data entity @e[tag=npc_raw_effect,limit=1] {Age:-100,active_effects:[{id:"minecraft:speed",duration:200}]} run say NPCRAWACTIVE`)
		sendCommand(t, pw, `execute store success score @s NPCProof run attribute @e[tag=npc_raw_effect,limit=1] movement_speed modifier value get minecraft:effect.speed`)
		sendCommand(t, pw, `execute if score @s NPCProof matches 0 run say NPCRAWNOMOD`)
		sendCommand(t, pw, "say NPCRAWCHECKEND")
		raw := collect("NPCRAWCHECKEND")
		if !structureSaidBB(raw, "NPCRAWACTIVE") || !structureSaidBB(raw, "NPCRAWNOMOD") {
			t.Errorf("%s raw summon check %v", addr, raw)
		}
		sendCommand(t, pw, `summon minecraft:cow ~ ~ ~ {NoAI:1b,NoGravity:1b,Tags:["npc_raw_modifier"],attributes:[{id:"minecraft:movement_speed",base:0.2d,modifiers:[{id:"minecraft:effect.speed",amount:0.75d,operation:"add_multiplied_total"}]}]}`)
		sendCommand(t, pw, `effect clear @e[tag=npc_raw_modifier,limit=1] speed`)
		sendCommand(t, pw, "scoreboard players set @s NPCProof -999999")
		sendCommand(t, pw, `execute store result score @s NPCProof run attribute @e[tag=npc_raw_modifier,limit=1] movement_speed modifier value get minecraft:effect.speed 1000`)
		sendCommand(t, pw, `execute if score @s NPCProof matches 750 run say NPCRAWIDPRESERVED`)
		sendCommand(t, pw, "say NPCRAWIDEND")
		if !structureSaidBB(collect("NPCRAWIDEND"), "NPCRAWIDPRESERVED") {
			t.Errorf("%s raw modifier lost without effect", addr)
		}
		sendCommand(t, pw, "effect give "+target+" speed 10 0 true")
		sendCommand(t, pw, "effect give "+target+" speed 1 1 true")
		sendCommand(t, pw, "execute store result score #start NPCProof run time query gametime")
		sendCommand(t, pw, "tick step 21")
		stepped := false
		for n := 0; n < 160 && !stepped; n++ {
			end, yes := fmt.Sprintf("NPCSTEPEND%dX", n), fmt.Sprintf("NPCSTEPYES%dX", n)
			sendCommand(t, pw, "execute store result score #now NPCProof run time query gametime")
			sendCommand(t, pw, "scoreboard players operation #now NPCProof -= #start NPCProof")
			sendCommand(t, pw, "execute if score #now NPCProof matches 21.. run say "+yes)
			sendCommand(t, pw, "say "+end)
			stepped = structureSaidBB(collect(end), yes)
			if !stepped {
				time.Sleep(40 * time.Millisecond)
			}
		}
		if !stepped {
			t.Fatal("effect world step incomplete")
		}
		sendCommand(t, pw, `execute store success score @s NPCProof run attribute @e[tag=npc_raw_effect,limit=1] movement_speed modifier value get minecraft:effect.speed`)
		sendCommand(t, pw, `execute if score @s NPCProof matches 0 run say NPCRAWPOSTABSENT`)
		sendCommand(t, pw, `execute if data entity @e[tag=npc_raw_effect,limit=1] {Age:-79,active_effects:[{id:"minecraft:speed",duration:179}]} run say NPCRAWTICKED`)
		sendCommand(t, pw, `execute if score @s NPCProof matches 1 run say NPCRAWPOSTPRESENT`)
		sendCommand(t, pw, "say NPCRAWPOSTEND")
		post := collect("NPCRAWPOSTEND")
		t.Logf("%s raw after admitted ticks: %v", addr, post)
		if !structureSaidBB(post, "NPCRAWPOSTABSENT") || !structureSaidBB(post, "NPCRAWTICKED") {
			t.Errorf("%s raw summon materialized an effect modifier on its first admitted ticks", addr)
		}

		sendCommand(t, pw, "scoreboard players set @s NPCProof -999999")
		sendCommand(t, pw, "execute store result score @s NPCProof run attribute "+target+" movement_speed modifier value get minecraft:effect.speed 1000")
		sendCommand(t, pw, "execute if score @s NPCProof matches 200 run say NPCHIDDENPROMOTED")
		sendCommand(t, pw, "say NPCHIDDENEND")
		if !structureSaidBB(collect("NPCHIDDENEND"), "NPCHIDDENPROMOTED") {
			t.Errorf("%s hidden speed modifier promotion failed", addr)
		}
		sendCommand(t, pw, "execute store result score #start NPCProof run time query gametime")
		sendCommand(t, pw, "tick step 180")
		expired := false
		for n := 0; n < 450 && !expired; n++ {
			end, yes := fmt.Sprintf("NPCEXPIREEND%dX", n), fmt.Sprintf("NPCEXPIREYES%dX", n)
			sendCommand(t, pw, "execute store result score #now NPCProof run time query gametime")
			sendCommand(t, pw, "scoreboard players operation #now NPCProof -= #start NPCProof")
			sendCommand(t, pw, "execute if score #now NPCProof matches 180.. run say "+yes)
			sendCommand(t, pw, "say "+end)
			expired = structureSaidBB(collect(end), yes)
			if !expired {
				time.Sleep(40 * time.Millisecond)
			}
		}
		if !expired {
			t.Fatal("effect expiry world step incomplete")
		}
		sendCommand(t, pw, "scoreboard players set @s NPCProof -999999")
		sendCommand(t, pw, "execute store success score @s NPCProof run attribute "+target+" movement_speed modifier value get minecraft:effect.speed")
		sendCommand(t, pw, "execute if score @s NPCProof matches 0 run say NPCEXPIRED")
		sendCommand(t, pw, "say NPCEXPIREDEND")
		if !structureSaidBB(collect("NPCEXPIREDEND"), "NPCEXPIRED") {
			t.Errorf("%s speed modifier survived expiry", addr)
		}
		t.Logf("%s completed all native attribute controls", addr)
		conn.Close()
	}
}
