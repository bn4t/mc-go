package blackbox

import (
	"fmt"
	"strings"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func actionGestureSnapshot(d *clickDriver) string {
	props := map[int16]int16{}
	for key, value := range d.props {
		if key != 3 || d.props[4] == 0 {
			props[key] = value
		}
	}
	// Enchantment seed is random; all other offered values are deterministic
	// for the deliberately non-enchantable lapis item in this gesture probe.
	if d.props[4] == -1 && d.props[5] == -1 && d.props[6] == -1 {
		delete(props, 3)
	}
	return fmt.Sprintf("menu=%v cursor=%s props=%v level=%d sounds=%v anvil=%d grind=%d", d.snap.menu, d.snap.carried, props, d.xpLevel, d.soundIDs, d.sounds, d.grindSounds)
}

func TestActionMenuGesturesParity(t *testing.T) {
	names := []string{}
	for n := 0; n < 8; n++ {
		names = append(names, fmt.Sprintf("AG%dV", n), fmt.Sprintf("AG%dM", n))
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	cases := []struct {
		block   string
		menu    int32
		size    int
		gives   [][2]string
		slots   []int16
		seed    int16
		collect byte
	}{
		{"anvil", 8, 3, [][2]string{{"hotbar.0", "minecraft:stone 8"}, {"hotbar.1", `minecraft:stone[custom_name="Swap"] 1`}}, []int16{0, 2, 3}, 1, 0},
		{"smithing_table", 21, 4, [][2]string{{"hotbar.0", "minecraft:netherite_upgrade_smithing_template 2"}, {"hotbar.1", "minecraft:netherite_upgrade_smithing_template 1"}, {"hotbar.2", "minecraft:diamond_chestplate"}, {"hotbar.3", "minecraft:netherite_ingot 2"}}, []int16{0, 1, 2, 3, 4}, 1, 0},
		{"grindstone", 15, 3, [][2]string{{"hotbar.0", `minecraft:stick[enchantments={knockback:1}] 2`}, {"hotbar.1", "minecraft:stick 1"}}, []int16{0, 2, 3}, 1, 0},
		{"loom", 18, 4, [][2]string{{"hotbar.0", "minecraft:white_banner 4"}, {"hotbar.1", `minecraft:white_banner[banner_patterns=[{pattern:"minecraft:stripe_top",color:"blue"}]] 1`}, {"hotbar.2", "minecraft:blue_dye 2"}}, []int16{0, 1, 2, 3, 4}, 1, 0},
		{"stonecutter", 24, 2, [][2]string{{"hotbar.0", "minecraft:stone 8"}, {"hotbar.1", "minecraft:stone_brick_slab 1"}}, []int16{0, 1, 2}, 1, 0},
		{"cartography_table", 23, 3, [][2]string{{"hotbar.0", "minecraft:map 8"}, {"hotbar.1", "minecraft:map 1"}}, []int16{0, 1, 2, 3}, 1, 1},
		{"enchanting_table", 13, 2, [][2]string{{"hotbar.0", "minecraft:lapis_lazuli 8"}, {"hotbar.1", "minecraft:lapis_lazuli 1"}}, []int16{0, 1, 2}, 1, 0},
		{"beacon", 9, 1, [][2]string{{"hotbar.0", "minecraft:emerald 8"}, {"hotbar.1", "minecraft:emerald 1"}}, []int16{0, 1}, 1, 0},
	}
	for n, tc := range cases {
		t.Run(tc.block, func(t *testing.T) {
			run := func(addr, name string) []string {
				d := newClickDriverOn(t, addr, name, "minecraft:"+tc.block, tc.menu, tc.gives, nil)
				command := func(cmd string) {
					sendCommand(t, d.pw, cmd)
					settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, cmd)
				}
				input := func(slot int16, button byte) {
					d.click("sync-input", int16(tc.size), 0, 6, false, true)
					d.click("input", slot, button, 2, false, false)
				}
				switch tc.block {
				case "anvil":
					command("gamemode survival @s")
					command("experience add @s 5 levels")
					d.rename("pending-name", "Swap")
				case "smithing_table":
					input(1, 2)
					input(2, 3)
				case "loom":
					input(1, 2)
				case "cartography_table":
					var held protocol.Buffer
					held.Short(1)
					_ = d.pw.WritePacket(server.IDPlayHeldChangeSB, held.Bytes())
					var use protocol.Buffer
					use.VarInt(0)
					use.VarInt(0)
					use.Float(0)
					use.Float(0)
					_ = d.pw.WritePacket(server.IDPlayUseItemSB, use.Bytes())
					settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "make-map")
					found := int16(-1)
					for sl := int16(tc.size); sl < int16(tc.size+36); sl++ {
						if strings.HasPrefix(d.snap.menu[int32(sl)], "1133#") {
							found = sl
							break
						}
					}
					if found < 0 {
						t.Fatalf("%s missing map: %v", name, d.snap.menu)
					}
					d.click("map-pick", found, 0, 0, false, false)
					d.click("map-in", 0, 0, 0, false, false)
				}
				source := int16(tc.size + 27)
				d.click("lift-drag", source, 0, 0, false, false)
				d.click("drag-start", -999, 0, 5, false, false)
				for _, slot := range tc.slots {
					d.click("drag-cell", slot, 1, 5, false, false)
				}
				d.click("drag-end", -999, 2, 5, false, false)
				if tc.block == "loom" || tc.block == "stonecutter" {
					d.button("select-output", 5)
				}
				out := []string{actionGestureSnapshot(d)}
				if d.snap.carried != "-" {
					d.click("park-remainder", source, 0, 0, false, false)
				}
				if tc.block == "cartography_table" {
					command(`item replace entity @s hotbar.1 with minecraft:filled_map[map_id=0] 1`)
					d.click("sync-map-seed", 4, 0, 6, false, true)
				}
				d.click("seed-collect", source+tc.seed, 0, 0, false, false)
				d.orbSpawns, d.orbXP = 0, 0
				d.click("collect", int16(tc.size+1), tc.collect, 6, false, false)
				out = append(out, actionGestureSnapshot(d))
				d.close()
				d.click("sync-player", 9, 0, 6, true, true)
				out = append(out, fmt.Sprintf("p0=%v cursor=%s", d.snap.p0, d.snap.carried))
				return out
			}
			want := run(tg.vanilla, names[n*2])
			got := run(tg.mcgo, names[n*2+1])
			for i := range want {
				t.Logf("step%d vanilla=%s mcgo=%s", i, want[i], got[i])
				if want[i] != got[i] {
					t.Errorf("step%d action gesture differs", i)
				}
			}
		})
	}
}

func TestArmorGestureParity(t *testing.T) {
	names := []string{"AD0V", "AD0M", "AC0V", "AC0M", "AC1V", "AC1M", "AC2V", "AC2M"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	player := func(addr, name, item string) *clickDriver {
		d := newClickDriverOn(t, addr, name, "minecraft:chest", 2, [][2]string{{"hotbar.0", item}, {"hotbar.1", strings.Replace(item, " 1", " 2", 1)}}, nil)
		d.close()
		d.click("sync-player", 9, 0, 6, true, true)
		return d
	}
	t.Run("armor-drag", func(t *testing.T) {
		run := func(addr, name string) clickSnap {
			d := player(addr, name, "minecraft:carved_pumpkin 8")
			d.click("lift", 36, 0, 0, true, false)
			d.click("start", -999, 0, 5, true, false)
			for _, sl := range []int16{5, 6, 8, 0, 9} {
				d.click("armor-cell", sl, 1, 5, true, false)
			}
			d.click("finish", -999, 2, 5, true, false)
			if !strings.HasSuffix(d.snap.p0[5], "#1") || d.snap.p0[6] != "-" || d.snap.p0[8] != "-" {
				t.Errorf("%s armor slots=%v", name, d.snap.p0)
			}
			return snapAnvil(d).snap
		}
		want := run(tg.vanilla, names[0])
		got := run(tg.mcgo, names[1])
		if diff := diffSnaps(want, got); len(diff) > 0 {
			t.Error(diff)
		}
	})
	for n, tc := range []struct {
		mode  string
		slot  int16
		count int
		keep  bool
	}{{"survival", 9, 3, true}, {"creative", 9, 4, false}, {"survival", 5, 3, true}} {
		t.Run(fmt.Sprintf("binding-%s-%d", tc.mode, tc.slot), func(t *testing.T) {
			run := func(addr, name string) clickSnap {
				item := `minecraft:carved_pumpkin[enchantments={binding_curse:1}] 1`
				d := player(addr, name, item)
				sendCommand(t, d.pw, "gamemode "+tc.mode+" @s")
				sendCommand(t, d.pw, "item replace entity @s armor.head with "+item)
				settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "wear-binding")
				d.click("sync-binding", 9, 0, 6, true, true)
				d.click("seed", 36, 0, 0, true, false)
				d.click("collect-binding", tc.slot, 0, 6, true, false)
				if !strings.Contains(d.snap.carried, fmt.Sprintf("#%d[", tc.count)) {
					t.Errorf("%s binding cursor=%s want%d", name, d.snap.carried, tc.count)
				}
				if (d.snap.p0[5] != "-") != tc.keep {
					t.Errorf("%s armor binding=%s keep=%v", name, d.snap.p0[5], tc.keep)
				}
				return snapAnvil(d).snap
			}
			want := run(tg.vanilla, names[2+n*2])
			got := run(tg.mcgo, names[3+n*2])
			if diff := diffSnaps(want, got); len(diff) > 0 {
				t.Error(diff)
			}
		})
	}
}

func TestAnvilCollectPermissionParity(t *testing.T) {
	names := []string{"AP0V", "AP0M", "AP1V", "AP1M"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	for n, tc := range []struct {
		levels int
		seed   int
		slot   int16
		extra  int
	}{{0, 1, 2, 2}, {5, 63, 3, 0}} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			run := func(addr, name string) string {
				gives := [][2]string{{"hotbar.0", "minecraft:stone 4"}, {"hotbar.1", fmt.Sprintf(`minecraft:stone[custom_name="Swap"] %d`, tc.seed)}}
				if tc.extra > 0 {
					gives = append(gives, [2]string{"hotbar.2", fmt.Sprintf(`minecraft:stone[custom_name="Swap"] %d`, tc.extra)})
				}
				d := newClickDriverOn(t, addr, name, "minecraft:anvil", 8, gives, nil)
				sendCommand(t, d.pw, "gamemode survival @s")
				sendCommand(t, d.pw, fmt.Sprintf("experience add @s %d levels", tc.levels))
				settleUse(t, d.sink, func(packets []usePkt) { d.foldPkts(packets) }, "anvil-levels")
				d.click("input", 0, 0, 2, false, false)
				d.rename("name", "Swap")
				d.click("seed", 31, 0, 0, false, false)
				d.click("collect-permission", tc.slot, 0, 6, false, false)
				expected := tc.seed + tc.extra
				if !strings.Contains(d.snap.carried, fmt.Sprintf("#%d[", expected)) {
					t.Errorf("%s cursor=%s want%d", name, d.snap.carried, expected)
				}
				if d.snap.menu[2] == "-" || d.snap.menu[0] == "-" || d.xpLevel != int32(tc.levels) {
					t.Errorf("%s forbidden result changed: %s", name, actionGestureSnapshot(d))
				}
				return actionGestureSnapshot(d)
			}
			want := run(tg.vanilla, names[n*2])
			got := run(tg.mcgo, names[n*2+1])
			t.Logf("vanilla=%s mcgo=%s", want, got)
			if want != got {
				t.Error("anvil collection permission differs")
			}
		})
	}
}

func TestSmithingGestureErrorParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("SGEV", "SGEM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []string {
		d := newClickDriverOn(t, addr, name, "minecraft:smithing_table", 21, [][2]string{{"hotbar.0", "minecraft:spire_armor_trim_smithing_template 2"}, {"hotbar.1", "minecraft:spire_armor_trim_smithing_template 1"}, {"hotbar.2", "minecraft:diamond_pickaxe"}, {"hotbar.3", "minecraft:amethyst_shard 2"}}, nil)
		for _, pair := range [][2]int16{{1, 2}, {2, 3}} {
			d.click("sync-input", 4, 0, 6, false, true)
			d.click("input", pair[0], byte(pair[1]), 2, false, false)
		}
		d.click("lift-template", 31, 0, 0, false, false)
		d.click("start", -999, 0, 5, false, false)
		for _, slot := range []int16{0, 3, 4} {
			d.click("template-cell", slot, 1, 5, false, false)
		}
		d.click("finish", -999, 2, 5, false, false)
		if d.props[0] != 1 || d.snap.menu[3] != "-" {
			t.Errorf("%s full invalid recipe property=%v menu=%v", name, d.props, d.snap.menu)
		}
		out := []string{actionGestureSnapshot(d)}
		d.click("seed", 32, 0, 0, false, false)
		d.click("collect", 5, 0, 6, false, false)
		if d.props[0] != 0 {
			t.Errorf("%s incomplete grid kept error=%v", name, d.props)
		}
		out = append(out, actionGestureSnapshot(d))
		return out
	}
	want := run(tg.vanilla, "SGEV")
	got := run(tg.mcgo, "SGEM")
	for i := range want {
		t.Logf("step%d vanilla=%s mcgo=%s", i, want[i], got[i])
		if want[i] != got[i] {
			t.Errorf("step%d smithing error differs", i)
		}
	}
}

func TestEnchantGestureCloseParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("EGCV", "EGCM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) string {
		d := newClickDriverOn(t, addr, name, "minecraft:enchanting_table", 13, [][2]string{{"hotbar.0", "minecraft:lapis_lazuli 8"}}, nil)
		d.click("lift", 29, 0, 0, false, false)
		d.click("start", -999, 0, 5, false, false)
		for _, slot := range []int16{0, 1, 2} {
			d.click("cell", slot, 1, 5, false, false)
		}
		d.click("finish", -999, 2, 5, false, false)
		d.close()
		d.click("sync-close", 9, 0, 6, true, true)
		return fmt.Sprintf("p0=%v cursor=%s", d.snap.p0, d.snap.carried)
	}
	want := run(tg.vanilla, "EGCV")
	got := run(tg.mcgo, "EGCM")
	t.Logf("vanilla=%s mcgo=%s", want, got)
	if want != got {
		t.Error("enchant gesture close lost transient inputs or cursor")
	}
}
