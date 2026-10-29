package blackbox

import (
	"fmt"
	"testing"
)

// The wire oracle pins drag filtering and collection across specialized
// storage menus, including a furnace input that has no smelting recipe.
func TestSpecializedGesturesParity(t *testing.T) {
	names := []string{"SGFV", "SGFM", "SGHV", "SGHM", "SGBV", "SGBM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	for n, tc := range []struct {
		block string
		menu  int32
		size  int
		item  string
		slots []int16
	}{
		{"furnace", 14, 3, "stone", []int16{0, 1, 2, 3}},
		{"hopper", 16, 5, "stone", []int16{0, 1, 2}},
		{"brewing_stand", 11, 5, "glass_bottle", []int16{0, 1, 2, 3, 4}},
	} {
		t.Run(tc.block, func(t *testing.T) {
			run := func(addr, name string) []clickSnap {
				block := "minecraft:" + tc.block
				d := newClickDriverOn(t, addr, name, block, tc.menu, [][2]string{{"hotbar.0", "minecraft:" + tc.item + " 12"}, {"hotbar.1", "minecraft:" + tc.item + " 5"}, {"weapon.offhand", "minecraft:" + tc.item + " 3"}}, nil)
				var out []clickSnap
				d.click("lift", int16(tc.size+27), 0, 0, false, false)
				d.click("drag-start", -999, 0, 5, false, false)
				for _, sl := range tc.slots {
					d.click("drag-add", sl, 1, 5, false, false)
				}
				d.click("drag-end", -999, 2, 5, false, false)
				out = append(out, snapAnvil(d).snap)
				// Put remainder in a player slot, seed collection from another stack.
				d.click("stash", int16(tc.size+1), 0, 0, false, false)
				d.click("seed", int16(tc.size+28), 0, 0, false, false)
				d.click("collect", int16(tc.size+2), 0, 6, false, false)
				out = append(out, snapAnvil(d).snap)
				if tc.block == "hopper" {
					d.click("invalid-hotbar", 0, 9, 2, false, false)
					d.click("offhand", 0, 40, 2, false, false)
					out = append(out, snapAnvil(d).snap)
				}
				return out
			}
			want := run(tg.vanilla, names[n*2])
			got := run(tg.mcgo, names[n*2+1])
			for i := range want {
				t.Logf("%s step%d vanilla=%v mcgo=%v", tc.block, i, want[i], got[i])
				if diff := diffSnaps(want[i], got[i]); len(diff) > 0 {
					t.Error(fmt.Sprint(diff))
				}
			}
		})
	}
}

// A setblock inventory tag must be visible before the first menu click.
func TestFurnaceCommandInventoryParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("FCIV", "FCIM")}, nil)
	defer tg.cleanup()
	run := func(addr, name string) clickSnap {
		d := newClickDriverOn(t, addr, name, `minecraft:furnace{Items:[{Slot:0b,id:"minecraft:diamond_ore",count:2},{Slot:2b,id:"minecraft:stone",count:4}]}`, 14, [][2]string{{"hotbar.0", "minecraft:stone 1"}}, nil)
		if d.snap.menu[2] != "1#4" {
			t.Errorf("%s command output=%s want stone4", name, d.snap.menu[2])
		}
		return snapAnvil(d).snap
	}
	want := run(tg.vanilla, "FCIV")
	got := run(tg.mcgo, "FCIM")
	if diff := diffSnaps(want, got); len(diff) > 0 {
		t.Error(diff)
	}
}
