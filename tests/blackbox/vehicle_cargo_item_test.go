package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestVehicleCargoItemParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, pr, pw := joinPlayBlackbox(t, leg.addr, "P")
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("CARGO_ITEM_BARRIER%d", serial)
			sendCommand(t, pw, "say "+marker)
			return waitForExactSayBB(t, sink, marker, 15*time.Second)
		}
		sendCommand(t, pw, "tp @s 0 -60 0")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "scoreboard objectives add cargo_result dummy")
		barrier()
		for _, name := range []string{"chest_minecart", "hopper_minecart", "oak_chest_boat", "minecart"} {
			sendCommand(t, pw, "kill @e[tag=cargo_target]")
			sendCommand(t, pw, "kill @e[type=item]")
			sendCommand(t, pw, fmt.Sprintf(`summon %s 8 -60 8 {Tags:["cargo_target"],NoGravity:1b,Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`, name))
			barrier()
			sendCommand(t, pw, "execute if entity @e[tag=cargo_target,limit=1] run say ITEM_TARGET_READY")
			if !structureSaidBB(barrier(), "ITEM_TARGET_READY") {
				t.Fatalf("%s %s target missing", leg.name, name)
			}
			for index, command := range []string{
				"item replace entity @e[tag=cargo_target,limit=1] container.0 with emerald 4",
				"item replace entity @s hotbar.0 from entity @e[tag=cargo_target,limit=1] hotbar.0",
				"item replace entity @e[tag=cargo_target,limit=1] container.4 with gold_ingot 2",
				"item replace entity @e[tag=cargo_target,limit=1] inventory.0 with iron_ingot 2",
				"item replace entity @e[tag=cargo_target,limit=1] container.26 with stone 2",
				"item replace entity @e[tag=cargo_target,limit=1] container.27 with stone 2",
			} {
				want := 1
				if name == "minecart" || index == 5 || name == "hopper_minecart" && index >= 3 {
					want = 0
				}
				sendCommand(t, pw, "execute store result score value cargo_result run "+command)
				sendCommand(t, pw, fmt.Sprintf("execute if score value cargo_result matches %d run say ITEM_RESULT", want))
				if !structureSaidBB(barrier(), "ITEM_RESULT") {
					t.Errorf("%s %s command%d expected result%d", leg.name, name, index, want)
				}
				if want == 1 {
					predicate := `{Items:[{Slot:0b,id:"minecraft:emerald",count:4}]}`
					target := "@e[tag=cargo_target,limit=1]"
					if index == 1 {
						target = "@s"
						predicate = `{Inventory:[{Slot:0b,id:"minecraft:emerald",count:4}]}`
					} else if index == 2 {
						predicate = `{Items:[{Slot:4b,id:"minecraft:gold_ingot",count:2}]}`
					} else if index == 3 {
						predicate = `{Items:[{Slot:9b,id:"minecraft:iron_ingot",count:2}]}`
					} else if index == 4 {
						predicate = `{Items:[{Slot:26b,id:"minecraft:stone",count:2}]}`
					}
					sendCommand(t, pw, "execute if data entity "+target+" "+predicate+" run say ITEM_NATIVE")
					if !structureSaidBB(barrier(), "ITEM_NATIVE") {
						t.Errorf("%s %s command%d native state missing", leg.name, name, index)
					}
				}
			}
		}
		sendCommand(t, pw, `summon chest_minecart 8 -60 8 {Tags:["cargo_multi"],NoGravity:1b}`)
		sendCommand(t, pw, `summon chest_minecart 9 -60 8 {Tags:["cargo_multi"],NoGravity:1b}`)
		barrier()
		sendCommand(t, pw, "execute store result score value cargo_result run item replace entity @e[tag=cargo_multi] container.0 with emerald 4")
		sendCommand(t, pw, "scoreboard players set native cargo_result 0")
		sendCommand(t, pw, `execute as @e[tag=cargo_multi] if data entity @s {Items:[{Slot:0b,id:"minecraft:emerald",count:4}]} run scoreboard players add native cargo_result 1`)
		sendCommand(t, pw, "execute if score value cargo_result matches 2 if score native cargo_result matches 2 run say ITEM_MULTI_RESULT")
		if !structureSaidBB(barrier(), "ITEM_MULTI_RESULT") {
			t.Errorf("%s multi-target result/native count differs from2", leg.name)
		}
		sendCommand(t, pw, "kill @e[tag=cargo_target]")
		sendCommand(t, pw, `summon chest_minecart 8 -60 8 {Tags:["cargo_target"],NoGravity:1b,LootTable:"minecraft:empty",LootTableSeed:123L}`)
		barrier()
		sendCommand(t, pw, "execute if data entity @e[tag=cargo_target,limit=1] LootTable run say ITEM_LOOT_ARMED")
		if !structureSaidBB(barrier(), "ITEM_LOOT_ARMED") {
			t.Fatalf("%s loot source not armed", leg.name)
		}
		sendCommand(t, pw, "item replace entity @s hotbar.0 from entity @e[tag=cargo_target,limit=1] container.0")
		sendCommand(t, pw, "execute unless data entity @e[tag=cargo_target,limit=1] LootTable unless data entity @e[tag=cargo_target,limit=1] LootTableSeed unless data entity @s {Inventory:[{Slot:0b}]} run say ITEM_LOOT_CONSUMED")
		if !structureSaidBB(barrier(), "ITEM_LOOT_CONSUMED") {
			sendCommand(t, pw, "execute unless data entity @e[tag=cargo_target,limit=1] LootTable run say ITEM_TABLE_GONE")
			sendCommand(t, pw, "execute unless data entity @e[tag=cargo_target,limit=1] LootTableSeed run say ITEM_SEED_GONE")
			sendCommand(t, pw, "execute unless data entity @s {Inventory:[{Slot:0b}]} run say ITEM_INVENTORY_GONE")
			lines := barrier()
			t.Errorf("%s empty loot read retained loot or player item: tableGone%v seedGone%v inventoryGone%v", leg.name, structureSaidBB(lines, "ITEM_TABLE_GONE"), structureSaidBB(lines, "ITEM_SEED_GONE"), structureSaidBB(lines, "ITEM_INVENTORY_GONE"))
		}
		c.Close()
	}
}
