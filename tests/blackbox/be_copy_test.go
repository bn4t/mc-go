package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestBECopyParity pins the copy_components / block-entity NBT surfaces
// the storage-block loot tables declare (datagen + jar-measured, 26.2):
//
//   - setblock tags: CustomName stays a verbatim JSON string, Items a
//     per-slot list ({Slot byte, id, count}), LootTable/LootTableSeed
//     the RandomizableContainer fields (seed written only when != 0).
//   - break: the loot tables' copy_components[custom_name] pulls the
//     BE's name onto the dropped stack; a container's Items spill;
//     a shulker box keeps contents in container + its loot table in
//     container_loot (persistent-only — never on the wire).
//   - place: applyImplicitComponents puts a stack's custom_name /
//     container / container_loot back onto the fresh block entity.
func TestBECopyParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Cpy"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "Cpy")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}

			probeHit := func(spec string, i int) string {
				deadline := time.Now().Add(9 * time.Second)
				for time.Now().Before(deadline) {
					sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZCP-ok-%d", spec, i))
					for _, e := range eggDrainFor(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZCP-ok-%d", i)) {
							return "hit"
						}
					}
				}
				return "none"
			}
			// probeAbsent polls once per window: any hit means the
			// component leaked onto the drop when it must not.
			probeAbsent := func(spec string, i int) string {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZCP-bad-%d", spec, i))
				for _, e := range eggDrainFor(t, c, pr, pw, 900*time.Millisecond) {
					if e.kind == "chat" && strings.Contains(e.chat, fmt.Sprintf("ZCP-bad-%d", i)) {
						return "hit"
					}
				}
				return "absent"
			}

			dig := func(action int, pos [3]int32, seq int) {
				var b protocol.Buffer
				b.VarInt(int32(action))
				b.Position(pos[0], pos[1], pos[2])
				b.VarInt(1)
				b.VarInt(int32(seq))
				if err := pw.WritePacket(server.IDPlayPlayerActionSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			// digBreak retries START→STOP pairs until the block is gone —
			// a single pair is flaky on vanilla when the first START lands
			// before the held-item replace settles (the ~0.7 progress gate
			// then never passes and the delayed destroy takes seconds).
			digBreak := func(pos [3]int32, seq int, bname string) {
				for i := 0; i < 5; i++ {
					dig(0, pos, seq+i*2)
					eggDrainFor(t, c, pr, pw, 800*time.Millisecond)
					dig(2, pos, seq+i*2+1)
					sendCommand(t, pw, fmt.Sprintf("execute unless block %d %d %d minecraft:%s run say ZCP-gone-%d", pos[0], pos[1], pos[2], bname, i))
					gone := false
					for _, e := range eggDrainFor(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind == "chat" && strings.Contains(e.chat, fmt.Sprintf("ZCP-gone-%d", i)) {
							gone = true
						}
					}
					if gone {
						return
					}
				}
				t.Fatalf("%s: block %s at %v never broke", tc.tag, bname, pos)
			}

			// moveGround lands a PlayMovePosSB with the onGround flag so
			// vanilla's getDestroyProgress takes the /30 (not the /100
			// off-ground) path — the harness sends no moves otherwise.
			moveGround := func(x, y, z float64) {
				var b protocol.Buffer
				b.Double(x)
				b.Double(y)
				b.Double(z)
				b.Byte(0x01)
				if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}

			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			sendCommand(t, pw, "clear @s")
			sendCommand(t, pw, "item replace entity @s hotbar.0 with diamond_axe 1")
			moveGround(12.5, -60, 12.5)
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 1: chest NBT in, name out on break.
			sendCommand(t, pw, `setblock 12 -60 14 minecraft:chest{CustomName:"{\"text\":\"Bob\"}",Items:[{Slot:0b,id:"minecraft:dirt",count:3}]}`)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probeHit(`data block 12 -60 14 {CustomName:"{\"text\":\"Bob\"}"}`, 0); p != "hit" {
				t.Fatalf("%s: leg1 CustomName probe = %s — want the verbatim JSON string", tc.tag, p)
			}
			if p := probeHit(`data block 12 -60 14 {Items:[{id:"minecraft:dirt"}]}`, 1); p != "hit" {
				t.Fatalf("%s: leg1 Items probe = %s — want the slot list", tc.tag, p)
			}
			digBreak([3]int32{12, -60, 14}, 0, "chest")
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			moveGround(40, -60, 40)
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)
			if p := probeHit(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:chest",components:{"minecraft:custom_name":"{\"text\":\"Bob\"}"}}}]`, 2); p != "hit" {
				t.Fatalf("%s: leg1 dropped-name probe = %s — want custom_name on the drop", tc.tag, p)
			}
			if p := probeHit(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:dirt"}}]`, 3); p != "hit" {
				t.Fatalf("%s: leg1 spill probe = %s — want the contents spilled", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			moveGround(12.5, -60, 12.5)
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 2: armed shulker box — breaking unpacks the loot
			// table into the slots (vanilla's isEmpty → unpackLootTable
			// inside playerWillDestroy), so the drop carries container +
			// custom_name and container_loot is consumed.
			sendCommand(t, pw, `item replace entity @s hotbar.0 with diamond_pickaxe 1`)
			sendCommand(t, pw, `setblock 12 -60 15 minecraft:shulker_box{CustomName:"{\"text\":\"Shk\"}",LootTable:"minecraft:chests/simple_dungeon",LootTableSeed:777,Items:[{Slot:2b,id:"minecraft:gold_ingot",count:7}]}`)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probeHit(`data block 12 -60 15 {LootTable:"minecraft:chests/simple_dungeon"}`, 4); p != "hit" {
				t.Fatalf("%s: leg2 LootTable probe = %s — want the table id", tc.tag, p)
			}
			if p := probeHit(`data block 12 -60 15 {LootTableSeed:777L}`, 5); p != "hit" {
				t.Fatalf("%s: leg2 LootTableSeed probe = %s — want the long seed", tc.tag, p)
			}
			digBreak([3]int32{12, -60, 15}, 20, "shulker_box")
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			moveGround(40, -60, 40)
			eggDrainFor(t, c, pr, pw, 900*time.Millisecond)
			if p := probeHit(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:shulker_box",components:{"minecraft:container":[{}]}}}]`, 6); p != "hit" {
				t.Fatalf("%s: leg2 container probe = %s — want rolled loot inside the drop", tc.tag, p)
			}
			if p := probeAbsent(`entity @e[type=minecraft:item,nbt={Item:{components:{"minecraft:container_loot":{}}}}]`, 7); p != "absent" {
				t.Fatalf("%s: leg2 container_loot probe = %s — armed table must be consumed", tc.tag, p)
			}
			if p := probeHit(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:shulker_box",components:{"minecraft:custom_name":"{\"text\":\"Shk\"}"}}}]`, 8); p != "hit" {
				t.Fatalf("%s: leg2 shulker-name probe = %s — want custom_name on the drop", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			moveGround(12.5, -60, 12.5)
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 3: place round-trip — a shulker stack's
			// container/container_loot/custom_name land back on the
			// placed block entity.
			sendCommand(t, pw, `item replace entity @s hotbar.0 with shulker_box[container_loot={loot_table:"minecraft:chests/simple_dungeon",seed:42L},custom_name="{\"text\":\"Plc\"}",container=[{item:{id:"minecraft:diamond",count:2},slot:0}]] 1`)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			sendUseItemOn(t, pw, 12, -61, 16, 1, 10)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probeHit(`data block 12 -60 16 {LootTable:"minecraft:chests/simple_dungeon"}`, 9); p != "hit" {
				t.Fatalf("%s: leg3 LootTable probe = %s — want the armed table", tc.tag, p)
			}
			if p := probeHit(`data block 12 -60 16 {LootTableSeed:42L}`, 10); p != "hit" {
				t.Fatalf("%s: leg3 LootTableSeed probe = %s — want seed 42", tc.tag, p)
			}
			if p := probeHit(`data block 12 -60 16 {CustomName:"{\"text\":\"Plc\"}"}`, 11); p != "hit" {
				t.Fatalf("%s: leg3 CustomName probe = %s — want the placed name", tc.tag, p)
			}
			if p := probeAbsent(`data block 12 -60 16 {Items:[{id:"minecraft:diamond"}]}`, 12); p != "absent" {
				t.Fatalf("%s: leg3 Items probe = %s — armed table hides Items", tc.tag, p)
			}

			// === Leg 4: named chest place — custom_name alone names
			// the block entity too.
			sendCommand(t, pw, `item replace entity @s hotbar.0 with chest[custom_name="{\"text\":\"PCh\"}"] 1`)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			sendUseItemOn(t, pw, 12, -61, 17, 1, 12)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probeHit(`data block 12 -60 17 {CustomName:"{\"text\":\"PCh\"}"}`, 13); p != "hit" {
				t.Fatalf("%s: leg4 chest-name probe = %s — want the name on the placed chest", tc.tag, p)
			}
		})
	}
}
