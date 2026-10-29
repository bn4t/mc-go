package blackbox

// TestExecuteIfParity pins vanilla's execute condition forms on the
// wire: if/unless items, data, score, entity, dimension, biome and
// predicate, each chained into a `run data get` whose reply (or silence
// — a failed condition emits nothing) is compared verbatim between the
// two servers. Wildcard slot ranges, the count=N predicate, tag
// predicates and the silent-failure edge cases are all measured.

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestExecuteIfParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	tag := fmt.Sprintf("exif%d", time.Now().UnixNano()%1_000_000)
	sel := fmt.Sprintf("@e[type=minecraft:pig,tag=%s,limit=1]", tag)
	// Absolute coords — replies echo the position, so a ~ anchor would
	// differ whenever the two servers spawn the test player apart.
	cmds := []string{
		"item replace entity @s weapon.mainhand with minecraft:stick 3",
		"execute if items entity @s weapon.mainhand minecraft:stick run data get storage z:z",
		"execute if items entity @s weapon.mainhand stick run data get storage z:z",
		"execute if items entity @s weapon.mainhand minecraft:stone run data get storage z:z",
		"execute unless items entity @s weapon.mainhand minecraft:stone run data get storage z:z",
		"execute if items entity @s weapon.mainhand minecraft:* run data get storage z:z",
		"execute if items entity @s weapon.mainhand #minecraft:swords run data get storage z:z",
		"execute if items entity @s weapon.offhand minecraft:stick run data get storage z:z",
		"execute if items entity @s weapon.offhand minecraft:air run data get storage z:z",
		"execute if items entity @s weapon.mainhand minecraft:stick[count=3] run data get storage z:z",
		"execute if items entity @s weapon.mainhand minecraft:stick[count=5] run data get storage z:z",
		"execute if items entity @s weapon.mainhand minecraft:stick[custom_data~{k:1}] run data get storage z:z",
		"execute if items entity @s weapon.* minecraft:stick run data get storage z:z",
		"execute if items entity nosuchplayer weapon.mainhand minecraft:stick run data get storage z:z",
		"execute if items block 1 -59 1 container.0 minecraft:stick run data get storage z:z",
		"setblock 1 -59 1 minecraft:chest",
		"item replace block 1 -59 1 container.0 with minecraft:stick 2",
		"execute if items block 1 -59 1 container.0 minecraft:stick run data get storage z:z",
		"execute if items block 1 -59 1 container.0 minecraft:stick[count=2] run data get storage z:z",
		"execute if items block 1 -59 1 container.* minecraft:stick[count=2] run data get storage z:z",
		"item replace entity @s weapon.mainhand with minecraft:diamond_sword",
		"execute if items entity @s weapon.mainhand #minecraft:swords run data get storage z:z",
		"execute if items entity @s weapon.mainhand minecraft:diamond_sword unless items entity @s weapon.mainhand minecraft:stone run data get storage z:z",
		"scoreboard objectives add zzobj dummy",
		"scoreboard objectives add zzobj2 dummy",
		"scoreboard players set @s zzobj 7",
		"scoreboard players set @s zzobj2 9",
		"execute if score @s zzobj matches 7 run data get storage z:z",
		"execute if score @s zzobj matches 8 run data get storage z:z",
		"execute if score @s zzobj matches 5..10 run data get storage z:z",
		"execute if score @s zzobj matches ..6 run data get storage z:z",
		"execute if score @s zzobj matches 8.. run data get storage z:z",
		"execute if score @s zznosuch matches 1 run data get storage z:z",
		"execute if score @s zzobj < @s zzobj2 run data get storage z:z",
		"execute if score @s zzobj > @s zzobj2 run data get storage z:z",
		"execute if score @s zzobj = @s zzobj2 run data get storage z:z",
		"execute if score nosuchholder zzobj matches 7 run data get storage z:z",
		"scoreboard players set @s zzmissingobj 1",
		"data merge storage z:z {a:9b,arr:[1,2,3]}",
		"execute if data storage z:z a run data get storage z:z",
		"execute if data storage zznone:ns a run data get storage z:z",
		"execute if data storage z:z nosuch run data get storage z:z",
		"execute if data entity @s Health run data get storage z:z",
		"execute if data entity " + sel + " Health run data get storage z:z",
		"execute if data block 1 -59 1 Items run data get storage z:z",
		"execute if data block 1 -58 1 Items run data get storage z:z",
		"execute if data storage z:z run data get storage z:z",
		"execute if dimension minecraft:overworld run data get storage z:z",
		"execute if dimension minecraft:the_nether run data get storage z:z",
		"execute if biome 1 -59 1 minecraft:the_void run data get storage z:z",
		"execute if biome 1 -59 1 minecraft:plains run data get storage z:z",
		"execute if predicate nosuch:pred run data get storage z:z",
		"execute if entity @s run data get storage z:z",
		"execute if entity " + sel + " run data get storage z:z",
		"execute if entity @e[type=minecraft:creeper] run data get storage z:z",
		"execute if entity nosuchplayer run data get storage z:z",
		"execute unless data storage zznone:ns a run data get storage z:z",
		"execute if entity " + sel + " run data get storage z:z",
		"execute if data entity " + sel + " Health run data get storage z:z",
		"execute if items entity " + sel + " weapon.mainhand minecraft:stick run data get storage z:z",
		// if loaded / if blocks / if function
		"execute if loaded 0 -59 0 run data get storage z:z",
		"execute if loaded 30000000 -59 30000000 run data get storage z:z",
		"execute if loaded 0 run data get storage z:z",
		"execute if loaded a b c run data get storage z:z",
		"execute if function minecraft:nosuch run data get storage z:z",
		"execute if function nosuch:x run data get storage z:z",
		"execute if function #minecraft:nosuch run data get storage z:z",
		"execute if function run data get storage z:z",
		"execute if blocks 1 -59 1 1 -59 1 2 -59 1 all run data get storage z:z",
		"execute if blocks 1 -59 1 1 -59 1 3 -59 1 all run data get storage z:z",
		"execute if blocks 1 -59 1 1 -59 1 3 -59 1 masked run data get storage z:z",
		"execute if blocks 1 -59 1 1 -59 1 3 -59 1 strict run data get storage z:z",
		"execute if blocks 3 -59 1 3 -59 1 4 -59 1 all run data get storage z:z",
		"execute if blocks 0 -64 0 1 -59 1 30000 60 30000 all run data get storage z:z",
		"execute if blocks 1 -59 1 1 -59 1 2 -59 1 bogus run data get storage z:z",
		"execute if blocks 1 -59 1 run data get storage z:z",
		"summon minecraft:pig 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"]}",
		"summon minecraft:pig 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"]}",
		// bare-condition leaves reply conditional.pass/fail
		"execute if score @s zzobj matches 7",
		"execute if score @s zzobj matches 8",
		"execute unless score @s zzobj matches 8",
		"execute if entity @s",
		"execute if entity " + sel + "",
		"execute if entity @e[type=minecraft:pig,tag=" + tag + "]",
		"execute if entity @e[type=minecraft:creeper]",
		"execute if dimension minecraft:overworld",
		"execute if data storage zznone:ns a",
		"execute if score @s zznosuch matches 1",
		// store result/success targets
		"execute store result storage z:z sv int 1 run data get storage z:z a",
		"data get storage z:z sv",
		"execute store success storage z:z sok byte 1 run data get storage z:z a",
		"data get storage z:z sok",
		"execute store result storage z:z sb byte 2 run data get storage z:z a",
		"data get storage z:z sb",
		"execute store result storage z:z sd double 0.5 run data get storage z:z a",
		"data get storage z:z sd",
		"execute store result storage z:z sl long 1 run data get storage z:z a",
		"data get storage z:z sl",
		"execute store result score @s zzobj run data get storage z:z a",
		"scoreboard players get @s zzobj",
		"execute store success score fakename zzobj run data get storage z:z a",
		"scoreboard players get fakename zzobj",
		"execute store result score @s zzmissingobj run data get storage z:z a",
		"execute store result entity " + sel + " NoAI byte 1 run data get storage z:z a",
		"data get entity " + sel + " NoAI",
		"execute store result entity " + sel + " Age int 1 run data get storage z:z a",
		"data get entity " + sel + " Age",
		"execute store result entity @s Health byte 1 run data get storage z:z a",
		"data get entity @s Health",
		"execute store result block 1 -59 1 Items[0].Count byte 1 run data get storage z:z a",
		"data get block 1 -59 1 Items[0].Count",
		"execute store result block 1 -58 1 Items byte 1 run data get storage z:z a",
		"execute store result bossbar nosuch:bar value run data get storage z:z a",
		"execute store result bossbar nosuch:bar bogus run data get storage z:z a",
		"execute store bogus storage z:z v int 1 run data get storage z:z a",
		"execute store result storage z:z v int run data get storage z:z a",
		"execute store result storage z:z v int x run data get storage z:z a",
		"execute store result storage z:z v banana 1 run data get storage z:z a",
		"execute store result storage z:z v2 int 1 run data get storage z:z a",
		"data get storage z:z v2",
		"execute store result storage z:z v3 int 1 run data get storage z:z arr",
		"data get storage z:z v3",
		"execute store result storage z:z v4 int 1 run execute if score @s zzobj matches 1",
		"data get storage z:z v4",
		"execute store success storage z:z v5 byte 1 run execute if score @s zzobj matches 1",
		"data get storage z:z v5",
		"execute store result storage z:z dead int 1 if score @s zzobj matches 99 run data get storage z:z a",
		"data get storage z:z dead",
		"execute store result storage z:z bad run nosuchcmd",
		"data get storage z:z bad",
		// chain modifiers
		"execute positioned 1.7 -59 1.8 run setblock ~ ~ ~ minecraft:stone",
		"execute positioned 1.7 -59 1.8 run setblock ~ ~ ~ minecraft:air",
		"execute positioned 5 -59 7 if block ~ ~ ~ minecraft:air run data get storage z:z",
		"execute positioned 1.7 -59.4 1.8 align xyz run setblock ~ ~ ~ minecraft:dirt",
		"execute positioned 1.7 -59.4 1.8 align xyz run setblock ~ ~ ~ minecraft:air",
		"execute positioned over world_surface run data get block ~ ~-1 ~ Items",
		"execute positioned over bogusmap run data get storage z:z",
		"execute at @e[type=minecraft:pig,tag=" + tag + ",limit=1] run setblock ~ ~ ~ minecraft:dirt",
		"execute at @e[type=minecraft:pig,tag=" + tag + ",limit=1] run setblock ~ ~ ~ minecraft:air",
		"execute positioned as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run data get block ~ ~-1 ~",
		"execute positioned as @e[type=minecraft:creeper] run data get storage z:z",
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run data get entity @s Age",
		"execute as @e[type=minecraft:pig,tag=" + tag + "] run data get entity @s Age",
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run data get block ~ ~-1 ~",
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run nosuchcmd",
		"execute as @s run data get entity @s Health",
		"execute as @e[type=minecraft:creeper] run data get storage z:z",
		"execute as P run data get entity @s Health",
		"execute as nosuchplayer run data get storage z:z",
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] if score @s nosuchobj matches 9",
		"execute at @e[type=minecraft:pig,tag=" + tag + ",limit=1] if entity @s",
		// tp through a forked @s moves the bound mob — the NoAI pig
		// floats, so the destination and the follow-up Pos are
		// deterministic on both servers.
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run tp @s 3.5 -58 4.5",
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run data get entity @s Pos",
		"execute at @e[type=minecraft:pig,tag=" + tag + ",limit=1] run tp @s ~1 ~ ~-1",
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run data get entity @s Pos",
		"execute at @e[type=minecraft:pig,tag=" + tag + ",limit=1] run tp @s ~ ~-1 ~",
		"execute as @e[type=minecraft:pig,tag=" + tag + ",limit=1] run data get entity @s Pos",
		"tp @e[type=minecraft:pig,tag=" + tag + ",limit=1] 9.5 -59 10.5",
		"execute at @s positioned 4 -59 4 run setblock ~ ~ ~ minecraft:stone",
		"execute at @s positioned 4 -59 4 run setblock ~ ~ ~ minecraft:air",
		"execute rotated 90 45 run data get storage z:z",
		"execute rotated as @e[type=minecraft:pig,tag=" + tag + "] run data get storage z:z",
		"execute facing 0 0 0 run data get storage z:z",
		"execute facing entity @e[type=minecraft:pig,tag=" + tag + "] feet run data get storage z:z",
		"execute facing entity @e[type=minecraft:pig,tag=" + tag + "] bogus run data get storage z:z",
		"execute anchored eyes run data get storage z:z",
		"execute anchored bogus run data get storage z:z",
		"execute on vehicle run data get storage z:z",
		"execute on bogus run data get storage z:z",
		"execute summon minecraft:zombie run data get entity @s Health",
		"execute summon minecraft:zombie",
		"execute summon minecraft:zombie if entity @s",
		"execute summon nosuch:mob run data get storage z:z",
		"execute positioned 3 -59 3 summon minecraft:creeper run data get entity @s Health",
		"kill @e[type=minecraft:zombie]",
		"kill @e[type=minecraft:creeper]",
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()
		var out [][]string
		for _, cmd := range cmds {
			ageStore := cmd == "execute store result entity "+sel+" Age int 1 run data get storage z:z a"
			if ageStore {
				// The stored value is 9. A 450 ms sleep can observe either
				// zero or one at the tick boundary; pin ten exact ticks.
				sendCommand(t, pw, "tick freeze")
				waitDrained(t, pw, sink)
			}
			sink.take()
			sendCommand(t, pw, cmd)
			time.Sleep(450 * time.Millisecond)
			var evs []string
			for _, line := range sink.take() {
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			out = append(out, evs)
			if ageStore {
				before := gameTimeOf(t, pw, sink)
				sendCommand(t, pw, "tick step 10")
				deadline := time.Now().Add(10 * time.Second)
				for gameTimeOf(t, pw, sink) < before+10 {
					if time.Now().After(deadline) {
						t.Fatal("age-store tick step did not complete")
					}
					time.Sleep(20 * time.Millisecond)
				}
				sendCommand(t, pw, "tick unfreeze")
				waitDrained(t, pw, sink)
			}
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cmds {
		if fmt.Sprintf("%v", want[i]) != fmt.Sprintf("%v", got[i]) {
			t.Errorf("cmd %q:\n  vanilla %v\n  mc-go   %v", cmd, want[i], got[i])
		}
	}
}
