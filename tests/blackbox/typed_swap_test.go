package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestTypedSwapCapacityParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("TSCV", "TSCM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []clickSnap {
		d := newClickDriverOn(t, addr, name, "minecraft:brewing_stand", 11, [][2]string{{"hotbar.0", "minecraft:glass_bottle 8"}, {"hotbar.1", "minecraft:glass_bottle 4"}, {"weapon.offhand", "minecraft:glass_bottle 5"}}, nil)
		var out []clickSnap
		for _, c := range []struct {
			slot   int16
			button byte
		}{{0, 0}, {0, 1}, {1, 40}, {1, 40}, {4, 40}} {
			d.click("typed-swap", c.slot, c.button, 2, false, false)
			out = append(out, snapAnvil(d).snap)
		}
		d.close()
		d.click("sync-offhand", 9, 0, 0, true, true)
		out = append(out, snapAnvil(d).snap)
		return out
	}
	want := run(tg.vanilla, "TSCV")
	got := run(tg.mcgo, "TSCM")
	for n := range want {
		t.Logf("step%d vanilla=%v mcgo=%v", n, want[n], got[n])
		if diff := diffSnaps(want[n], got[n]); len(diff) > 0 {
			t.Errorf("step%d: %v", n, diff)
		}
	}
}

func TestTypedResultSwapParity(t *testing.T) {
	names := []string{"TR0V", "TR0M", "TR1V", "TR1M", "TR2V", "TR2M", "TR3V", "TR3M", "TR4V", "TR4M", "TR5V", "TR5M"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	cases := []struct {
		block    string
		menu     int32
		size     int
		gives    [][2]string
		places   [][2]int16
		selectID int32
		result   int16
		button   byte
	}{
		{"stonecutter", 24, 2, [][2]string{{"hotbar.0", "minecraft:stone 3"}}, [][2]int16{{0, 0}}, 5, 1, 8},
		{"smithing_table", 21, 4, [][2]string{{"hotbar.0", "minecraft:diamond_chestplate"}, {"hotbar.1", "minecraft:netherite_upgrade_smithing_template 2"}, {"hotbar.2", "minecraft:netherite_ingot 2"}}, [][2]int16{{0, 1}, {1, 0}, {2, 2}}, -1, 3, 40},
		{"grindstone", 15, 3, [][2]string{{"hotbar.0", `minecraft:stick[enchantments={knockback:1}] 1`}}, [][2]int16{{0, 0}}, -1, 2, 8},
		{"loom", 18, 4, [][2]string{{"hotbar.0", "minecraft:white_banner 2"}, {"hotbar.1", "minecraft:blue_dye 2"}}, [][2]int16{{0, 0}, {1, 1}}, 5, 3, 40},
		{"cartography_table", 23, 3, [][2]string{{"hotbar.0", "minecraft:map"}, {"inventory.0", "minecraft:paper 2"}}, nil, -1, 2, 8},
		{"anvil", 8, 3, [][2]string{{"hotbar.0", "minecraft:iron_pickaxe[damage=100]"}}, [][2]int16{{0, 0}}, -1, 2, 40},
	}
	for n, tc := range cases {
		t.Run(tc.block, func(t *testing.T) {
			run := func(addr, name string) []string {
				d := newClickDriverOn(t, addr, name, "minecraft:"+tc.block, tc.menu, tc.gives, nil)
				if tc.block == "anvil" {
					sendCommand(t, d.pw, "gamemode survival @s")
					sendCommand(t, d.pw, "experience add @s 5 levels")
					settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "anvil-payment-setup")
				}
				for _, place := range tc.places {
					d.click("sync-input-state", int16(tc.size), 0, 0, false, true)
					d.click("input-hotbar-swap", place[1], byte(place[0]), 2, false, false)
				}
				if tc.block == "cartography_table" {
					var use protocol.Buffer
					use.VarInt(0)
					use.VarInt(0)
					use.Float(0)
					use.Float(0)
					if err := d.pw.WritePacket(server.IDPlayUseItemSB, use.Bytes()); err != nil {
						t.Fatal(err)
					}
					settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "create-map")
					found := int16(-1)
					for sl := int16(3); sl < 39; sl++ {
						if strings.HasPrefix(d.snap.menu[int32(sl)], "1133#") {
							found = sl
							break
						}
					}
					if found < 0 {
						t.Fatalf("%s map creation failed: %v", name, d.snap.menu)
					}
					d.click("pick-map", found, 0, 0, false, false)
					d.click("map-input", 0, 0, 0, false, false)
					d.click("pick-paper", 3, 0, 0, false, false)
					d.click("paper-input", 1, 0, 0, false, false)
				}
				if tc.block == "anvil" {
					d.rename("rename-output", "Swap")
				}
				if tc.selectID >= 0 {
					d.button("select-result", tc.selectID)
				}
				if d.snap.menu[int32(tc.result)] == "-" {
					t.Fatalf("%s no result: %v", name, d.snap.menu)
				}
				var resultID, resultCount int32
				if _, err := fmt.Sscanf(d.snap.menu[int32(tc.result)], "%d#%d", &resultID, &resultCount); err != nil {
					t.Fatal(err)
				}
				recipe := ""
				if tc.block == "stonecutter" {
					recipe = "stone_brick_slab_from_stone_stonecutting"
				}
				if tc.block == "smithing_table" {
					recipe = "netherite_chestplate_smithing"
				}
				if recipe != "" {
					sendCommand(t, d.pw, "recipe take @s minecraft:"+recipe)
					settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "forget-result-recipe")
				}
				// Count only orbs emitted by this take, excluding tracked orbs
				// from earlier players that arrived during this menu setup.
				d.orbSpawns, d.orbXP = 0, 0
				var click protocol.Buffer
				click.VarInt(d.win)
				click.VarInt(d.state)
				click.Short(tc.result)
				click.Byte(tc.button)
				click.VarInt(2)
				click.VarInt(0)
				click.VarInt(0)
				if err := d.pw.WritePacket(server.IDPlayClickContainerSB, click.Bytes()); err != nil {
					t.Fatal(err)
				}
				var recipeAdds []byte
				settleUse(t, d.sink, func(packets []usePkt) {
					d.foldPkts(packets)
					for _, p := range packets {
						if p.id == server.IDPlayRecipeBookAdd {
							recipeAdds = append(recipeAdds, p.data...)
						}
					}
				}, "swap-result")
				if recipe != "" && len(recipeAdds) == 0 {
					t.Errorf("%s result did not unlock recipe", name)
				}
				if tc.block == "anvil" && d.xpLevel != 4 {
					t.Errorf("%s anvil swap level=%d want4", name, d.xpLevel)
				}
				if tc.block == "grindstone" && d.orbSpawns == 0 {
					t.Errorf("%s grindstone swap spawned no XP", name)
				}
				var request protocol.Buffer
				request.VarInt(1)
				if err := d.pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes()); err != nil {
					t.Fatal(err)
				}
				crafted := int32(0)
				settleUse(t, d.sink, func(packets []usePkt) {
					d.foldPkts(packets)
					for _, p := range packets {
						if p.id == 0x03 {
							r := protocol.NewPacketReader(p.data)
							n, _ := r.VarInt()
							for j := int32(0); j < n; j++ {
								cat, _ := r.VarInt()
								id, _ := r.VarInt()
								value, _ := r.VarInt()
								if cat == 1 && id == resultID {
									crafted = value
								}
							}
						}
					}
				}, "result-crafted-stat")
				expected := int32(0)
				if tc.block == "stonecutter" || tc.block == "smithing_table" {
					expected = resultCount
				}
				if crafted != expected {
					t.Errorf("%s crafted=%d want%d", name, crafted, expected)
				}
				out := []string{fmt.Sprintf("menu=%v cursor=%s sounds=%v grind=%d announce=%d level=%d xp-orbs=%v crafted=%d recipes=%x", d.snap.menu, d.snap.carried, d.soundIDs, d.grindSounds, d.announce, d.xpLevel, d.orbSpawns > 0, crafted, recipeAdds)}
				d.close()
				d.click("sync-result-inventory", 9, 0, 0, true, true)
				out = append(out, fmt.Sprintf("p0=%v cursor=%s", d.snap.p0, d.snap.carried))
				return out
			}
			want := run(tg.vanilla, names[n*2])
			got := run(tg.mcgo, names[n*2+1])
			for i := range want {
				t.Logf("step%d vanilla=%s mcgo=%s", i, want[i], got[i])
				if want[i] != got[i] {
					t.Errorf("step%d result swap differs", i)
				}
			}
		})
	}
}

// Displaced oversized-slot items must not use an empty offhand as a free
// inventory cell. Creative discards an unplaceable item; survival drops it.
func TestTypedSwapFullInventoryParity(t *testing.T) {
	names := []string{"TF0V", "TF0M", "TF1V", "TF1M"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	for n, mode := range []string{"creative", "survival"} {
		t.Run(mode, func(t *testing.T) {
			run := func(addr, name string) string {
				d := newClickDriverOn(t, addr, name, "minecraft:brewing_stand", 11, [][2]string{{"hotbar.0", "minecraft:glass_bottle 2"}}, nil)
				d.click("seed-bottle", 0, 0, 2, false, false)
				sendCommand(t, d.pw, "gamemode "+mode+" @s")
				sendCommand(t, d.pw, "tick freeze")
				for slot := 0; slot < 9; slot++ {
					sendCommand(t, d.pw, fmt.Sprintf("item replace entity @s hotbar.%d with minecraft:stone 64", slot))
				}
				for slot := 0; slot < 27; slot++ {
					sendCommand(t, d.pw, fmt.Sprintf("item replace entity @s inventory.%d with minecraft:stone 64", slot))
				}
				sendCommand(t, d.pw, `item replace entity @s hotbar.1 with minecraft:glass_bottle[custom_name='{"text":"incoming"}'] 2`)
				if !digSentinel(t, d.pw, d.sink, "FULL-SWAP-READY", 10*time.Second) {
					t.Fatal("full inventory barrier")
				}
				// Invalid-state PICKUP_ALL with an empty cursor is a safe content resync,
				// including when every player slot contains a stack.
				d.click("full-resync", 5, 0, 6, false, true)
				var click protocol.Buffer
				click.VarInt(d.win)
				click.VarInt(d.state)
				click.Short(0)
				click.Byte(1)
				click.VarInt(2)
				click.VarInt(0)
				click.VarInt(0)
				if err := d.pw.WritePacket(server.IDPlayClickContainerSB, click.Bytes()); err != nil {
					t.Fatal(err)
				}
				itemType, _ := server.EntityTypeID("item")
				spawned := map[int32]bool{}
				stacks := map[int32]string{}
				settleUse(t, d.sink, func(packets []usePkt) {
					d.foldPkts(packets)
					for _, p := range packets {
						r := protocol.NewPacketReader(p.data)
						if p.id == server.IDPlaySpawnEntity {
							id, _ := r.VarInt()
							_, _ = r.UUID()
							typ, _ := r.VarInt()
							if typ == itemType {
								spawned[id] = true
							}
						}
						if p.id == server.IDPlayEntityMetadata {
							id, _ := r.VarInt()
							if !spawned[id] {
								continue
							}
							idx, _ := r.Byte()
							typ, _ := r.Byte()
							if idx == 8 && typ == 7 {
								if it, err := readSlotBB(r); err == nil {
									stacks[id] = stackKey(it)
								}
							}
						}
					}
				}, "full-swap")
				expected := 0
				if mode == "survival" {
					expected = 1
				}
				if len(spawned) != expected {
					t.Errorf("%s dropped entities=%d want%d", name, len(spawned), expected)
				}
				for id := range spawned {
					if stacks[id] != "1149#1" {
						t.Errorf("%s dropped stack=%q want plain bottle1", name, stacks[id])
					}
				}
				var request protocol.Buffer
				request.VarInt(1)
				if err := d.pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes()); err != nil {
					t.Fatal(err)
				}
				dropped := int32(0)
				settleUse(t, d.sink, func(packets []usePkt) {
					for _, p := range packets {
						if p.id != 0x03 {
							continue
						}
						r := protocol.NewPacketReader(p.data)
						count, _ := r.VarInt()
						for i := int32(0); i < count; i++ {
							cat, _ := r.VarInt()
							id, _ := r.VarInt()
							value, _ := r.VarInt()
							if cat == 5 && id == 1149 {
								dropped = value
							}
						}
					}
				}, "full-swap-stat")
				if dropped != int32(expected) {
					t.Errorf("%s dropped stat=%d want%d", name, dropped, expected)
				}
				d.close()
				d.click("full-player-resync", 9, 0, 6, true, true)
				if d.snap.p0[45] != "-" {
					t.Errorf("%s displaced item occupied empty offhand: %s", name, d.snap.p0[45])
				}
				result := fmt.Sprintf("p0=%v cursor=%s drops=%d dropped=%d", d.snap.p0, d.snap.carried, len(spawned), dropped)
				sendCommand(t, d.pw, "tick unfreeze")
				if !digSentinel(t, d.pw, d.sink, "FULL-SWAP-DONE", 10*time.Second) {
					t.Fatal("unfreeze barrier")
				}
				return result
			}
			want := run(tg.vanilla, names[n*2])
			got := run(tg.mcgo, names[n*2+1])
			t.Logf("vanilla=%s mcgo=%s", want, got)
			if want != got {
				t.Error("full-inventory swap differs")
			}
		})
	}
}
