package blackbox

import (
	"encoding/binary"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"strings"
	"testing"
	"time"
)

func TestVehicleCargoParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		c, pr, pw := joinPlayBlackbox(t, leg.addr, "P")
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		var entityUUID [16]byte
		var entityID, entityType, window, menu, total, passengers int32
		var state int32
		contents := 0
		cells := map[int32]string{}
		carried := "-"
		returnedCursor := false
		var chats []string
		var closed bool
		var title any
		var stacks []string
		pump := func() []string {
			lines := sink.take()
			for _, line := range lines {
				if key, _, ok := componentArgs(line); ok && strings.Contains(key, "spectatorCantOpen") {
					chats = append(chats, key)
				}
				prefix, body, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				r := protocol.NewPacketReader([]byte(body))
				switch prefix {
				case fmt.Sprintf("%#x", server.IDPlaySpawnEntity):
					id, e := r.VarInt()
					if e != nil {
						continue
					}
					uuid, _ := r.Read(16)
					typ, e := r.VarInt()
					if e == nil && typ == entityType {
						entityID = id
						copy(entityUUID[:], uuid)
					}
				case fmt.Sprintf("%#x", server.IDPlayOpenScreen):
					window, _ = r.VarInt()
					menu, _ = r.VarInt()
					raw, _ := r.Read(r.Remaining())
					title, _ = nbt.ReadUnnamed(strings.NewReader(string(raw)))
				case fmt.Sprintf("%#x", server.IDPlayContainerContent):
					wid, _ := r.VarInt()
					st, _ := r.VarInt()
					n, _ := r.VarInt()
					if wid != window || wid == 0 {
						continue
					}
					total = n
					state = st
					contents++
					cells = map[int32]string{}
					stacks = nil
					for slot := int32(0); slot < n; slot++ {
						item, e := readSlotBB(r)
						if e != nil {
							t.Fatalf("slot%d decode:%v", slot, e)
						}
						cells[slot] = stackKey(item)
						if !item.Empty() {
							stacks = append(stacks, fmt.Sprintf("%d=%s", slot, stackKey(item)))
						}
					}
					if item, err := readSlotBB(r); err == nil {
						carried = stackKey(item)
					} else {
						t.Fatal(err)
					}
				case fmt.Sprintf("%#x", server.IDPlayContainerSlot):
					wid, _ := r.VarInt()
					st, _ := r.VarInt()
					slot, _ := r.Short()
					item, err := readSlotBB(r)
					if err != nil {
						t.Fatal(err)
					}
					if wid == window {
						state = st
						cells[int32(slot)] = stackKey(item)
					}
				case fmt.Sprintf("%#x", server.IDPlaySetPlayerInventory):
					slot, _ := r.VarInt()
					item, err := readSlotBB(r)
					diamond, _ := world.ItemID("diamond")
					if err == nil && slot == 0 && item.ID == diamond && item.Count == 2 {
						returnedCursor = true
					}
				case fmt.Sprintf("%#x", server.IDPlaySetCarriedItem):
					item, err := readSlotBB(r)
					if err != nil {
						t.Fatal(err)
					}
					carried = stackKey(item)
				case fmt.Sprintf("%#x", server.IDPlayContainerClose):
					id, _ := r.VarInt()
					if id == window {
						closed = true
					}
				case "0x6b": // protocol776 set_passengers
					id, _ := r.VarInt()
					if id == entityID {
						passengers, _ = r.VarInt()
					}
				}
			}
			return lines
		}
		await := func(marker string) []string {
			deadline := time.Now().Add(15 * time.Second)
			var lines []string
			for time.Now().Before(deadline) {
				lines = append(lines, pump()...)
				if structureSaidBB(lines, marker) {
					return lines
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s marker%s missing", leg.name, marker)
			return nil
		}
		until := func(label string, condition func() bool) {
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				pump()
				if condition() {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s %s never observed", leg.name, label)
		}
		serial := 0
		barrier := func() []string {
			serial++
			mark := fmt.Sprintf("CARGO_BARRIER%d", serial)
			sendCommand(t, pw, "say "+mark)
			return await(mark)
		}
		gameTime := func() int64 {
			sendCommand(t, pw, "time query gametime")
			for _, line := range barrier() {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.query.gametime" && len(args) > 0 {
					if value, ok := args[0].(int64); ok {
						return value
					}
				}
			}
			t.Fatal("no time")
			return 0
		}
		step := func() {
			before := gameTime()
			sendCommand(t, pw, "tick step 1")
			for i := 0; i < 100; i++ {
				if gameTime() >= before+1 {
					return
				}
			}
			t.Fatal("step incomplete")
		}
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "tp @s 6 -60 2")
		barrier()
		cases := []struct {
			name                   string
			hand                   int32
			sneak, full, inventory bool
			custom                 string
			loot, spectator        bool
		}{
			{name: "chest_minecart"}, {name: "hopper_minecart"}, {name: "oak_chest_boat", sneak: true}, {name: "oak_chest_boat"}, {name: "oak_chest_boat", inventory: true}, {name: "oak_chest_boat", full: true}, {name: "chest_minecart", hand: 1}, {name: "chest_minecart", custom: "CargoKeeper"},
			{name: "chest_minecart", loot: true}, {name: "chest_minecart", spectator: true}, {name: "chest_minecart", loot: true, spectator: true},
		}
		for index, tc := range cases {
			if window != 0 {
				var b protocol.Buffer
				b.VarInt(window)
				pw.WritePacket(server.IDPlayCloseContainerSB, b.Bytes())
			}
			sendCommand(t, pw, "ride @s dismount")
			sendCommand(t, pw, "kill @e[tag=cargo_target]")
			sendCommand(t, pw, "kill @e[type=pig]")
			sendCommand(t, pw, "kill @e[type=item]")
			sendCommand(t, pw, "clear @s")
			mode := "creative"
			if tc.spectator {
				mode = "spectator"
			}
			sendCommand(t, pw, "gamemode "+mode+" @s")
			sendCommand(t, pw, "tp @s 6 -60 2")
			barrier()
			entityType, _ = server.EntityTypeID(tc.name)
			entityID = 0
			window = 0
			menu = -1
			total = 0
			title = nil
			stacks = nil
			passengers = 0
			closed = false
			chats = nil
			state = 0
			contents = 0
			cells = map[int32]string{}
			carried = "-"
			extra := ""
			if tc.full {
				extra += `,Passengers:[{id:"minecraft:pig",NoAI:1b}]`
			}
			if tc.loot {
				extra += `,LootTable:"minecraft:empty",LootTableSeed:123L`
			}
			if tc.custom != "" {
				extra += fmt.Sprintf(",CustomName:%q", tc.custom)
			}
			sendCommand(t, pw, fmt.Sprintf(`summon minecraft:%s 6 -60 5 {NoGravity:1b,Tags:["cargo_target"],Items:[{Slot:0b,id:"minecraft:diamond",count:3}]%s}`, tc.name, extra))
			barrier()
			for tries := 0; entityID == 0 && tries < 40; tries++ {
				step()
			}
			if entityID == 0 {
				t.Fatal("target never tracked")
			}
			interact := func() {
				var b protocol.Buffer
				b.VarInt(entityID)
				b.VarInt(tc.hand)
				b.Byte(0)
				b.Bool(tc.sneak)
				pw.WritePacket(server.IDPlayInteractSB, b.Bytes())
			}
			interact()
			barrier()
			step()
			if tc.inventory {
				var b protocol.Buffer
				b.VarInt(0)
				b.VarInt(5)
				b.VarInt(0)
				pw.WritePacket(server.IDPlayPlayerCommandSB, b.Bytes())
				barrier()
			}
			positive := tc.name != "oak_chest_boat" || tc.sneak || tc.full || tc.inventory
			deadline := time.Now().Add(2 * time.Second)
			for positive && (window == 0 || total == 0) && time.Now().Before(deadline) {
				pump()
				time.Sleep(20 * time.Millisecond)
			}
			if leg.name == "vanilla" && index < 8 && positive && window == 0 {
				t.Fatalf("vanilla case%d no expected menu", index)
			}
			sendCommand(t, pw, "execute if data entity @s RootVehicle run say CARGO_RIDING")
			riding := structureSaidBB(barrier(), "CARGO_RIDING")
			sendCommand(t, pw, "execute if data entity @e[tag=cargo_target,limit=1] LootTable run say CARGO_LOOT_ARMED")
			armed := structureSaidBB(barrier(), "CARGO_LOOT_ARMED")
			t.Logf("%s loot case%d armed%v spectator%v chats%v", leg.name, index, armed, tc.spectator, chats)
			t.Logf("%s case%d %s hand%d sneak%v full%v inventory%v ->menu%d slots%d title%#v cargo%v passengers%d riding%v", leg.name, index, tc.name, tc.hand, tc.sneak, tc.full, tc.inventory, menu, total, title, stacks, passengers, riding)
			wantMenu, wantSlots := int32(menu), int32(63)
			switch {
			case index == 3 || index == 10:
				wantMenu = -1
				wantSlots = 0
			case tc.name == "hopper_minecart":
				wantMenu = 16
				wantSlots = 41
			default:
				wantMenu = 2
			}
			if menu != wantMenu || total != wantSlots {
				t.Errorf("%s case%d menu/slots=%d/%d want%d/%d", leg.name, index, menu, total, wantMenu, wantSlots)
			}
			if wantMenu >= 0 {
				body, ok := title.(map[string]any)
				if !ok {
					t.Fatalf("%s case%d title was not decorated entity name: %#v", leg.name, index, title)
				}
				if tc.custom != "" {
					if body["text"] != tc.custom {
						t.Errorf("%s custom title=%#v", leg.name, body)
					}
				} else if body["translate"] != "entity.minecraft."+tc.name {
					t.Errorf("%s default title=%#v", leg.name, body)
				}
				hover, ok := body["hover_event"].(map[string]any)
				if !ok || hover["action"] != "show_entity" || hover["id"] != "minecraft:"+tc.name || body["insertion"] == nil {
					t.Errorf("%s case%d title decoration=%#v", leg.name, index, body)
				}
				expectedUUID := make([]int32, 4)
				for i := range expectedUUID {
					expectedUUID[i] = int32(binary.BigEndian.Uint32(entityUUID[i*4 : i*4+4]))
				}
				actualUUID, ok := hover["uuid"].([]int32)
				expectedInsertion := fmt.Sprintf("%x-%x-%x-%x-%x", entityUUID[0:4], entityUUID[4:6], entityUUID[6:8], entityUUID[8:10], entityUUID[10:16])
				if !ok || fmt.Sprint(actualUUID) != fmt.Sprint(expectedUUID) || body["insertion"] != expectedInsertion {
					t.Errorf("%s case%d menu title identifies another entity: %#v", leg.name, index, body)
				}
				diamond, _ := world.ItemID("diamond")
				want := []string{fmt.Sprintf("0=%s", stackKey(server.ItemStack{ID: diamond, Count: 3}))}
				if tc.loot {
					want = nil
				}
				if fmt.Sprint(stacks) != fmt.Sprint(want) {
					t.Errorf("%s case%d cargo=%v want%v", leg.name, index, stacks, want)
				}
			}
			wantRiding := index == 3 || index == 4
			if riding != wantRiding {
				t.Errorf("%s case%d riding%v want%v", leg.name, index, riding, wantRiding)
			}
			if index == 5 && passengers != 1 {
				t.Errorf("%s full chestboat has%d passengers want1", leg.name, passengers)
			}
			if armed != (tc.loot && tc.spectator) {
				t.Errorf("%s case%d loot armed%v", leg.name, index, armed)
			}
			if index == 10 && (len(chats) != 1 || chats[0] != "container.spectatorCantOpen") {
				t.Errorf("%s armed spectator feedback=%v", leg.name, chats)
			}

			diamondID, _ := world.ItemID("diamond")
			diamondKey := func(count int32) string { return stackKey(server.ItemStack{ID: diamondID, Count: count}) }
			click := func(slot int16, button byte) {
				var b protocol.Buffer
				b.VarInt(window)
				b.VarInt(state)
				b.Short(slot)
				b.Byte(button)
				b.VarInt(0)
				b.VarInt(0)
				b.VarInt(0)
				if err := pw.WritePacket(server.IDPlayClickContainerSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			if index == 9 {
				previous := contents
				click(0, 0)
				until("spectator menu resync", func() bool { return contents > previous })
				if cells[0] != diamondKey(3) || carried != "-" {
					t.Errorf("%s spectator changed cargo/cursor: %s/%s", leg.name, cells[0], carried)
				}
			}
			if index == 0 {
				click(0, 0)
				until("cargo pickup", func() bool { return cells[0] == "-" && carried == diamondKey(3) })
				click(4, 1)
				until("cargo single placement", func() bool { return cells[4] == diamondKey(1) && carried == diamondKey(2) })
				returnedCursor = false
				var close protocol.Buffer
				close.VarInt(window)
				pw.WritePacket(server.IDPlayCloseContainerSB, close.Bytes())
				until("cursor returned to player inventory", func() bool { return returnedCursor })
				barrier()
				sendCommand(t, pw, `execute if data entity @s {Inventory:[{Slot:0b,id:"minecraft:diamond",count:2}]} if data entity @e[tag=cargo_target,limit=1] {Items:[{Slot:4b,id:"minecraft:diamond",count:1}]} run say CARGO_CURSOR_RETURNED`)
				if !structureSaidBB(barrier(), "CARGO_CURSOR_RETURNED") {
					sendCommand(t, pw, "data get entity @s Inventory")
					sendCommand(t, pw, "data get entity @e[tag=cargo_target,limit=1] Items")
					t.Errorf("%s close lost cursor or cargo contents; native=%v", leg.name, barrier())
				}
				previous := window
				interact()
				barrier()
				until("cargo reopen", func() bool { return window != previous && cells[4] == diamondKey(1) })
			}
			if index == 0 {
				sendCommand(t, pw, "gamemode survival @s")
				sendCommand(t, pw, "tp @s 6.5 -60 -1.90")
				barrier()
				step()
				pump()
				if closed {
					t.Errorf("%s cargo menu closed inside measured distance", leg.name)
				}
				sendCommand(t, pw, "tp @s 6.5 -60 -1.96")
				barrier()
				step()
				pump()
				if !closed {
					t.Errorf("%s cargo menu stayed open outside measured distance", leg.name)
				}
			}
		}
		c.Close()
	}
}
