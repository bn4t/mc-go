package blackbox

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

type handUseCase struct {
	name, main, off, clicked, wantBlock, entity string
	hand                                        int32
	mainStart, offStart, mainEnd, offEnd        int32
	sneak, creative, menu, book, air            bool
}

func TestItemUseHandPriorityParity(t *testing.T) {
	cases := []handUseCase{
		{name: "main_block", main: "stone", off: "dirt", clicked: "stone", wantBlock: "stone", mainStart: 3, offStart: 5, mainEnd: 2, offEnd: 5},
		{name: "off_block", main: "dirt", off: "stone", clicked: "stone", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "main_empty_no_fallback", off: "stone", clicked: "stone", wantBlock: "air", offStart: 5, offEnd: 5},
		{name: "off_empty_no_fallback", main: "stone", clicked: "stone", wantBlock: "air", hand: 1, mainStart: 3, mainEnd: 3},
		{name: "off_block_main_empty", off: "stone", clicked: "stone", wantBlock: "stone", hand: 1, offStart: 5, offEnd: 4},
		{name: "main_egg", main: "pig_spawn_egg", off: "dirt", clicked: "stone", wantBlock: "air", entity: "pig", mainStart: 3, offStart: 5, mainEnd: 2, offEnd: 5},
		{name: "off_egg", main: "dirt", off: "pig_spawn_egg", clicked: "stone", wantBlock: "air", entity: "pig", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "off_empty_ignores_main_egg", main: "pig_spawn_egg", clicked: "stone", wantBlock: "air", hand: 1, mainStart: 3, mainEnd: 3},
		{name: "off_egg_main_empty", off: "pig_spawn_egg", clicked: "stone", wantBlock: "air", entity: "pig", hand: 1, offStart: 5, offEnd: 4},
		{name: "off_egg_creative", main: "dirt", off: "pig_spawn_egg", clicked: "stone", wantBlock: "air", entity: "pig", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, creative: true},
		{name: "off_block_creative", main: "dirt", off: "stone", clicked: "stone", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, creative: true},
		{name: "main_chest_default", main: "stone", off: "dirt", clicked: "chest", wantBlock: "air", mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5, menu: true},
		{name: "off_chest_skips_default", main: "dirt", off: "stone", clicked: "chest", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "off_chest_sneak_main_empty", off: "stone", clicked: "chest", wantBlock: "stone", hand: 1, offStart: 5, offEnd: 4, sneak: true},
		{name: "off_block_ignores_main_flint", main: "flint_and_steel", off: "stone", clicked: "tnt", wantBlock: "stone", hand: 1, mainStart: 1, offStart: 5, mainEnd: 1, offEnd: 4},
		{name: "off_block_ignores_main_bonemeal", main: "bone_meal", off: "stone", clicked: "grass_block", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "off_block_ignores_main_crystal", main: "end_crystal", off: "stone", clicked: "obsidian", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "main_rocket", main: "firework_rocket", off: "dirt", clicked: "stone", wantBlock: "air", entity: "firework_rocket", mainStart: 3, offStart: 5, mainEnd: 2, offEnd: 5},
		{name: "off_rocket", main: "dirt", off: "firework_rocket", clicked: "stone", wantBlock: "air", entity: "firework_rocket", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "off_lectern_book", main: "dirt", off: "writable_book", clicked: "lectern", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 0, book: true},
		{name: "off_lectern_book_creative", main: "dirt", off: "writable_book", clicked: "lectern", wantBlock: "air", hand: 1, mainStart: 3, offStart: 1, mainEnd: 3, offEnd: 1, book: true, creative: true},
		{name: "off_lectern_block_skips_default", main: "dirt", off: "stone", clicked: "lectern", wantBlock: "stone", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4},
		{name: "main_lectern_block_default", main: "stone", off: "dirt", clicked: "lectern", wantBlock: "air", mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 5},
		{name: "main_empty_off_block_chest", off: "stone", clicked: "chest", wantBlock: "air", offStart: 5, offEnd: 5, menu: true},
		{name: "main_empty_sneak_off_block_chest", off: "stone", clicked: "chest", wantBlock: "air", offStart: 5, offEnd: 5, sneak: true},
		{name: "off_egg_air_water", main: "dirt", off: "pig_spawn_egg", clicked: "water", wantBlock: "air", entity: "pig", hand: 1, mainStart: 3, offStart: 5, mainEnd: 3, offEnd: 4, air: true},
	}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("HandUse")}, nil)
	defer targets.cleanup()
	type result struct {
		main, off, mainID, offID, mainDamage, offDamage int32
		block, pigMain, pigOff, rocket, menu, book      bool
	}
	run := func(addr string) []result {
		conn, pr, pw := joinPlayBlackbox(t, addr, "HandUse")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		slots := map[int16]server.ItemStack{}
		menuSlots := map[int32]int32{}
		storeSlot := func(wid int32, slot int16, item server.ItemStack) {
			if wid == 0 {
				slots[slot] = item
				return
			}
			base, ok := menuSlots[wid]
			if !ok {
				return
			}
			index := int32(slot)
			if index >= base && index < base+27 {
				slots[int16(9+index-base)] = item
			}
			if index >= base+27 && index < base+36 {
				slots[int16(36+index-base-27)] = item
			}
		}
		opened := false
		window := int32(0)
		fold := func(lines []string) {
			for _, line := range lines {
				sep := strings.IndexByte(line, ':')
				if sep < 0 {
					continue
				}
				var id int32
				if _, err := fmt.Sscanf(line[:sep], "%v", &id); err != nil {
					continue
				}
				r := protocol.NewPacketReader([]byte(line[sep+1:]))
				switch id {
				case server.IDPlayContainerSlot:
					wid, e := r.VarInt()
					_, _ = r.VarInt()
					slot, e2 := r.Short()
					it, e3 := readSlotBB(r)
					if e == nil && e2 == nil && e3 == nil {
						storeSlot(wid, slot, it)
					}
				case server.IDPlayContainerContent:
					wid, e := r.VarInt()
					_, _ = r.VarInt()
					n, e2 := r.VarInt()
					if e == nil && e2 == nil {
						if wid > 0 && n >= 36 {
							menuSlots[wid] = n - 36
						}
						for i := int32(0); i < n; i++ {
							it, err := readSlotBB(r)
							if err != nil {
								t.Fatalf("%s inventory slot%d: %v", addr, i, err)
							}
							storeSlot(wid, int16(i), it)
						}
					}
				case server.IDPlaySetPlayerInventory:
					slot, e := r.VarInt()
					it, e2 := readSlotBB(r)
					if e == nil && e2 == nil {
						if slot < 9 {
							slot += 36
						} else if slot == 40 {
							slot = 45
						}
						slots[int16(slot)] = it
					}
				case server.IDPlayOpenScreen:
					window, _ = r.VarInt()
					opened = true
				}
			}
		}
		collect := func(marker string) []string {
			t.Helper()
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				batch := sink.take()
				fold(batch)
				lines = append(lines, batch...)
				for _, line := range batch {
					if strings.Contains(line, marker) {
						return lines
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing marker%s", addr, marker)
			return nil
		}
		position := func(x float64, tag string) {
			t.Helper()
			deadline := time.Now().Add(3 * time.Second)
			for n := 0; time.Now().Before(deadline); n++ {
				sendCommand(t, pw, "data get entity @s Pos[0]")
				marker := fmt.Sprintf("HANDPOS%s%dX", tag, n)
				sendCommand(t, pw, "say "+marker)
				v := math.NaN()
				for _, line := range collect(marker) {
					if end := strings.Index(line, "data.entity.query"); end >= 0 {
						v = lastNumberSigned(line[:end])
					}
				}
				if !math.IsNaN(v) && math.Abs(v-x) < 1e-6 {
					return
				}
			}
			t.Fatalf("%s %s missing actor position", addr, tag)
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "say HANDFROZEN")
		collect("HANDFROZEN")
		var out []result
		for i, tc := range cases {
			if window != 0 {
				var b protocol.Buffer
				b.VarInt(window)
				_ = pw.WritePacket(server.IDPlayCloseContainerSB, b.Bytes())
				window = 0
			}
			sendCommand(t, pw, "tp @s 8.0 160.0 8.0")
			position(8, fmt.Sprintf("WAY%d", i))
			// Frozen dying bodies retain building collision; relocate live fixtures first.
			sendCommand(t, pw, "tp @e[type=pig,distance=..32] 1000.0 160.0 1000.0")
			sendCommand(t, pw, "kill @e[type=pig]")
			sendCommand(t, pw, "kill @e[type=tnt]")
			sendCommand(t, pw, "kill @e[type=firework_rocket]")
			sendCommand(t, pw, "kill @e[type=end_crystal]")
			sendCommand(t, pw, "clear @s")
			clearMarker := fmt.Sprintf("HANDCLEAR%dX", i)
			sendCommand(t, pw, "say "+clearMarker)
			collect(clearMarker)
			sendCommand(t, pw, "fill -2 159 -3 2 164 3 air")
			sendCommand(t, pw, "fill -2 159 -3 2 159 3 stone")
			sendCommand(t, pw, "setblock 0 159 0 "+tc.clicked)
			if tc.air {
				sendCommand(t, pw, "fill -1 159 -2 1 159 -1 air")
			}
			mode := "survival"
			if tc.creative {
				mode = "creative"
			}
			sendCommand(t, pw, "gamemode "+mode+" @s")
			names := [2]string{fmt.Sprintf("MainEgg%d", i), fmt.Sprintf("OffEgg%d", i)}
			for hand, item := range []string{tc.main, tc.off} {
				if item == "" {
					continue
				}
				count := tc.mainStart
				if hand == 1 {
					count = tc.offStart
				}
				value := item
				if strings.HasSuffix(item, "_spawn_egg") {
					value = fmt.Sprintf(`%s[custom_name="%s"]`, item, names[hand])
				}
				slot := "weapon.mainhand"
				if hand == 1 {
					slot = "weapon.offhand"
				}
				sendCommand(t, pw, fmt.Sprintf("item replace entity @s %s with %s %d", slot, value, count))
			}
			input := byte(0)
			if tc.sneak {
				input = 0x20
			}
			_ = pw.WritePacket(server.IDPlayPlayerInputSB, []byte{input})
			sendCommand(t, pw, "tp @s 0.5 160.0 -2.5")
			position(.5, fmt.Sprintf("START%d", i))
			initialMainID, initialOffID := int32(0), int32(0)
			if tc.mainStart > 0 {
				initialMainID = world.ItemIDs[tc.main]
			}
			if tc.offStart > 0 {
				initialOffID = world.ItemIDs[tc.off]
			}
			ready := false
			deadline := time.Now().Add(3 * time.Second)
			for n := 0; time.Now().Before(deadline); n++ {
				marker := fmt.Sprintf("HANDREADY%d_%dX", i, n)
				sendCommand(t, pw, "say "+marker)
				collect(marker)
				if slots[36].Count == tc.mainStart && slots[45].Count == tc.offStart && slots[36].ID == initialMainID && slots[45].ID == initialOffID && slots[36].Damage == 0 && slots[45].Damage == 0 {
					ready = true
					break
				}
			}
			if !ready {
				t.Fatalf("%s %s initial hand slots main%+v off%+v", addr, tc.name, slots[36], slots[45])
			}
			opened = false
			sequence := int32(2600 + i)
			var b protocol.Buffer
			b.VarInt(tc.hand)
			if tc.air {
				b.VarInt(sequence)
				b.Float(0)
				b.Float(35)
				_ = pw.WritePacket(server.IDPlayUseItemSB, b.Bytes())
			} else {
				b.Position(0, 159, 0)
				b.VarInt(1)
				b.Float(.5)
				b.Float(1)
				b.Float(.5)
				b.Bool(false)
				b.Bool(false)
				b.VarInt(sequence)
				_ = pw.WritePacket(server.IDPlayUseItemOnSB, b.Bytes())
			}
			acked := false
			deadline = time.Now().Add(5 * time.Second)
			ackPrefix := fmt.Sprintf("%#x:", server.IDPlayBlockChangedAck)
			for time.Now().Before(deadline) && !acked {
				batch := sink.take()
				fold(batch)
				for _, line := range batch {
					if strings.HasPrefix(line, ackPrefix) {
						ack, err := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, ackPrefix))).VarInt()
						if err == nil && ack == sequence {
							acked = true
						}
					}
				}
				if !acked {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if !acked {
				t.Fatalf("%s %s missing use acknowledgement", addr, tc.name)
			}
			markers := []string{fmt.Sprintf("HANDBLOCK%dX", i), fmt.Sprintf("HANDMAINPIG%dX", i), fmt.Sprintf("HANDOFFPIG%dX", i), fmt.Sprintf("HANDROCKET%dX", i), fmt.Sprintf("HANDBOOK%dX", i)}
			sendCommand(t, pw, "execute if block 0 160 0 "+tc.wantBlock+" run say "+markers[0])
			for hand, name := range names {
				sendCommand(t, pw, fmt.Sprintf("execute if entity @e[type=pig,name=%s] run say %s", name, markers[hand+1]))
			}
			sendCommand(t, pw, "execute if entity @e[type=firework_rocket] run say "+markers[3])
			sendCommand(t, pw, "execute if block 0 159 0 lectern[has_book=true] run say "+markers[4])
			end := fmt.Sprintf("HANDDONE%dX", i)
			sendCommand(t, pw, "say "+end)
			lines := collect(end)
			flags := [5]bool{}
			for _, line := range lines {
				for j, marker := range markers {
					if strings.Contains(line, marker) {
						flags[j] = true
					}
				}
			}
			r := result{main: slots[36].Count, off: slots[45].Count, mainID: slots[36].ID, offID: slots[45].ID, mainDamage: slots[36].Damage, offDamage: slots[45].Damage, block: flags[0], pigMain: flags[1], pigOff: flags[2], rocket: flags[3], menu: opened, book: flags[4]}
			out = append(out, r)
			t.Logf("%s %s %+v", addr, tc.name, r)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, tc := range cases {
		want := result{main: tc.mainEnd, off: tc.offEnd, block: true, menu: tc.menu, book: tc.book, rocket: tc.entity == "firework_rocket"}
		if tc.mainEnd > 0 {
			want.mainID = world.ItemIDs[tc.main]
		}
		if tc.offEnd > 0 {
			want.offID = world.ItemIDs[tc.off]
		}
		if tc.entity == "pig" {
			if tc.hand == 0 {
				want.pigMain = true
			} else {
				want.pigOff = true
			}
		}
		if vanilla[i] != want || mcgo[i] != vanilla[i] {
			t.Errorf("%s vanilla%+v mcgo%+v want%+v", tc.name, vanilla[i], mcgo[i], want)
		}
	}
}
