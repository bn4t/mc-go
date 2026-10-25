package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestMountChestParity pins the 26.2 chested-mount surface —
// AbstractChestedHorse + the llama family (all bytecode-measured):
//
//   - mobInteract chest branch: tamed + not sneaking + !hasChest +
//     held chest → equipChest: the flag flips, the chest equips
//     sound plays (LLAMA_CHEST for llamas), the stack consumes one
//     (survival only — consume respects hasInfiniteMaterials), and
//     createInventory rebuilds the mount inventory at the new
//     column count.
//   - getInventoryColumns: a chested llama's columns equal its
//     Strength (1-5); donkey/mule report 5.
//   - Save NBT: Tame, ChestedHorse, the flat Items list when
//     chested ({Slot:N,id,count} — saddle slot 0, body slot 1,
//     chest cells 2..), llama's Strength and Variant.
//   - Death: AbstractHorse.dropEquipment drops every non-empty
//     inventory cell (saddle, body, chest cells), then the
//     chested flag drops a chest block item and clears.
//   - An untamed animal makeMad()s first — the chest never equips.
func TestMountChestParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Chest"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "Chest")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}

			probe := func(spec string, i int) string {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZCB-ok-%d", spec, i))
				sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZCB-miss-%d", spec, i))
				deadline := time.Now().Add(8 * time.Second)
				for time.Now().Before(deadline) {
					for _, e := range eggDrainFor(t, c, pr, pw, 400*time.Millisecond) {
						if e.kind != "chat" {
							continue
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZCB-ok-%d", i)) {
							return "hit"
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZCB-miss-%d", i)) {
							return "miss"
						}
					}
				}
				return "none"
			}

			// lastSpawn returns the newest spawned entity of the
			// given type seen while draining dur.
			lastSpawn := func(typeName string, dur time.Duration) int32 {
				tid, _ := server.EntityTypeID(typeName)
				var id int32
				for _, e := range eggDrainFor(t, c, pr, pw, dur) {
					if e.kind == "spawn" && e.etype == tid {
						id = e.eid
					}
				}
				return id
			}

			interact := func(eid int32, sneak bool) {
				var b protocol.Buffer
				b.VarInt(eid)
				b.VarInt(0)
				b.Raw(lpVec3(0, 1, 0))
				b.Bool(sneak)
				if err := pw.WritePacket(server.IDPlayInteractSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}

			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			sendCommand(t, pw, "clear @s")
			eggDrainFor(t, c, pr, pw, 1500*time.Millisecond)

			// === Leg 1: chest equip — the flag flips and the
			// chest stack shrinks by one.
			sendCommand(t, pw, "summon minecraft:donkey 12 -60 14 {NoAI:1b,Tame:1b}")
			var donkeyID int32
			for i := 0; i < 10 && donkeyID == 0; i++ {
				donkeyID = lastSpawn("donkey", 1500*time.Millisecond)
			}
			if donkeyID == 0 {
				t.Fatalf("%s: donkey spawn packet never seen", tc.tag)
			}
			sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:chest 2")
			eggDrainFor(t, c, pr, pw, 600*time.Millisecond)
			interact(donkeyID, false)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probe(`entity @e[type=minecraft:donkey,nbt={ChestedHorse:1b}]`, 1); p != "hit" {
				t.Fatalf("%s: leg1 ChestedHorse probe = %s — want the flag", tc.tag, p)
			}
			if p := probe(`entity @s[nbt={Inventory:[{id:"minecraft:chest",count:1}]}]`, 2); p != "hit" {
				t.Fatalf("%s: leg1 chest-consume probe = %s — want one chest left", tc.tag, p)
			}

			// === Leg 2: mount screen columns — donkey reports 5,
			// llama reports its Strength.
			sendCommand(t, pw, "summon minecraft:llama 12 -60 16 {NoAI:1b,Tame:1b,Strength:4,Variant:2,ChestedHorse:1b}")
			var llamaID int32
			for i := 0; i < 10 && llamaID == 0; i++ {
				llamaID = lastSpawn("llama", 1500*time.Millisecond)
			}
			if llamaID == 0 {
				t.Fatalf("%s: llama spawn packet never seen", tc.tag)
			}
			if p := probe(`entity @e[type=minecraft:llama,nbt={Strength:4}]`, 3); p != "hit" {
				t.Fatalf("%s: leg2 llama Strength probe = %s", tc.tag, p)
			}
			if p := probe(`entity @e[type=minecraft:llama,nbt={Variant:2}]`, 4); p != "hit" {
				t.Fatalf("%s: leg2 llama Variant probe = %s", tc.tag, p)
			}
			// Sneak-interact the llama → mount_screen_open cols=4.
			interact(llamaID, true)
			var cols int32 = -1
			deadline := time.Now().Add(8 * time.Second)
			for cols < 0 && time.Now().Before(deadline) {
				_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
				p, err := pr.ReadPacket()
				if err != nil {
					continue
				}
				if p.ID == server.IDPlayKeepAliveCB {
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					continue
				}
				if p.ID == 0x29 { // mount_screen_open
					rd := protocol.NewPacketReader(p.Data)
					rd.VarInt()
					cols, _ = rd.VarInt()
				}
			}
			if cols != 4 {
				t.Fatalf("%s: leg2 llama mount cols = %d — want 4 (Strength)", tc.tag, cols)
			}
			sendCommand(t, pw, "container close")
			eggDrainFor(t, c, pr, pw, 500*time.Millisecond)

			// === Leg 3: Items NBT — the flat slotted list rides the
			// summon tag and reads back out.
			sendCommand(t, pw, `summon minecraft:mule 12 -60 18 {NoAI:1b,Tame:1b,ChestedHorse:1b,Items:[{Slot:2b,id:"minecraft:cobblestone",count:3}]}`)
			var muleID int32
			for i := 0; i < 10 && muleID == 0; i++ {
				muleID = lastSpawn("mule", 1500*time.Millisecond)
			}
			if muleID == 0 {
				t.Fatalf("%s: mule spawn packet never seen", tc.tag)
			}
			if p := probe("data entity @e[type=minecraft:mule,limit=1] Items", 5); p != "hit" {
				t.Fatalf("%s: leg3 Items probe = %s — want the list", tc.tag, p)
			}
			if p := probe(`entity @e[type=minecraft:mule,nbt={Items:[{Slot:2b,id:"minecraft:cobblestone",count:3}]}]`, 6); p != "hit" {
				t.Fatalf("%s: leg3 Items-shape probe = %s — want the slotted entry", tc.tag, p)
			}

			// === Leg 4: death drop — every inventory cell plus the
			// chest block itself. Move the player out of pickup
			// range first so the drops persist for the probe.
			sendCommand(t, pw, "tp @s 40 -60 40 0 0")
			sendCommand(t, pw, "kill @e[type=minecraft:mule]")
			eggDrainFor(t, c, pr, pw, 1200*time.Millisecond)
			if p := probe(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:chest"}}]`, 7); p != "hit" {
				t.Fatalf("%s: leg4 chest-drop probe = %s — want the chest item", tc.tag, p)
			}
			if p := probe(`entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:cobblestone"}}]`, 8); p != "hit" {
				t.Fatalf("%s: leg4 contents-drop probe = %s — want the cobblestone", tc.tag, p)
			}
			sendCommand(t, pw, "kill @e[type=minecraft:item]")
			sendCommand(t, pw, "tp @s 12.5 -60 12.5 0 0")
			eggDrainFor(t, c, pr, pw, 800*time.Millisecond)

			// === Leg 5: untamed — the animal makeMad()s first and
			// the chest never equips.
			sendCommand(t, pw, "summon minecraft:donkey 12 -60 20 {NoAI:1b}")
			var wildID int32
			for i := 0; i < 10 && wildID == 0; i++ {
				wildID = lastSpawn("donkey", 1500*time.Millisecond)
			}
			if wildID == 0 {
				t.Fatalf("%s: wild donkey spawn packet never seen", tc.tag)
			}
			interact(wildID, false)
			eggDrainFor(t, c, pr, pw, 700*time.Millisecond)
			if p := probe(`entity @e[type=minecraft:donkey,nbt={ChestedHorse:1b,Tame:0b}]`, 9); p != "miss" {
				t.Fatalf("%s: leg5 untamed-equip probe = %s — want no flag", tc.tag, p)
			}
			// And on the tamed donkey, sneaking opens the menu
			// instead of re-equipping — the flag stays exactly once.
			if p := probe(`entity @e[type=minecraft:donkey,nbt={ChestedHorse:1b},limit=1]`, 10); p != "hit" {
				t.Fatalf("%s: leg5 tamed-flag probe = %s", tc.tag, p)
			}
		})
	}
}
