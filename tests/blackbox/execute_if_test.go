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
		"data merge storage z:z {a:1b}",
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
		"summon minecraft:pig 9.5 -59 10.5 {NoAI:1b,Tags:[\"" + tag + "\"]}",
		"execute if entity " + sel + " run data get storage z:z",
		"execute if data entity " + sel + " Health run data get storage z:z",
		"execute if items entity " + sel + " weapon.mainhand minecraft:stick run data get storage z:z",
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
