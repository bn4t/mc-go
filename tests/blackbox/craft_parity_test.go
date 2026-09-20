package blackbox

// Crafting parity: the 2x2 player grid and the 3x3 crafting-table menu
// run the same recipe scripts on both servers, diffing the result slot,
// grid consumption, remainders, and the cursor after every step. Menu
// 12 geometry: 0 result, 1-9 grid, 10-36 main, 37-45 hotbar; window 0
// carries result 0, grid 1-4, main 9-35, hotbar 36-44.

import (
	"testing"
)

func TestCraftRecipeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CraftV1", "CraftM1", "CraftV2", "CraftM2",
			"CraftV3", "CraftM3", "CraftV4", "CraftM4",
			"CraftV5", "CraftM5", "CraftV6", "CraftM6",
			"CraftV7", "CraftM7", "CraftV8", "CraftM8",
			"CraftV9", "CraftM9", "CraftV10", "CraftM10",
			"CraftV11", "CraftM11"),
	}, nil)
	defer tg.cleanup()

	type step struct {
		tag    string
		slot   int16
		button byte
		mode   int32
		w0     bool
	}
	cases := []struct {
		name  string
		suf   string
		block string
		menu  int32
		gives [][2]string
		steps []step
	}{
		{
			// Shaped 3x3: diamond_pickaxe = "###" diamonds, " S ",
			// " S " sticks. Right-place one per slot, verify the
			// result, take it, verify the grid spent one each.
			name: "pickaxe", suf: "1", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:diamond 64"},
				{"hotbar.1", "minecraft:stick 64"},
			},
			steps: []step{
				{tag: "lift-dia", slot: 37},
				{tag: "dia1", slot: 1, button: 1},
				{tag: "dia2", slot: 2, button: 1},
				{tag: "dia3", slot: 3, button: 1},
				{tag: "dia-back", slot: 37},
				{tag: "lift-stick", slot: 38},
				{tag: "st5", slot: 5, button: 1},
				{tag: "st8", slot: 8, button: 1},
				{tag: "st-back", slot: 38},
				{tag: "take", slot: 0},
				{tag: "park", slot: 10},
			},
		},
		{
			// Stair L: six planks produce four stairs.
			name: "stairs", suf: "2", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_planks 64"}},
			steps: []step{
				{tag: "lift", slot: 37},
				{tag: "p1", slot: 1, button: 1},
				{tag: "p4", slot: 4, button: 1},
				{tag: "p5", slot: 5, button: 1},
				{tag: "p7", slot: 7, button: 1},
				{tag: "p8", slot: 8, button: 1},
				{tag: "p9", slot: 9, button: 1},
				{tag: "back", slot: 37},
				{tag: "take", slot: 0},
			},
		},
		{
			// Mirrored stair L: whether the pack carries a second
			// mirrored recipe or the mirror simply fails, the two
			// servers must answer the same.
			name: "stairs-mirror", suf: "3", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_planks 64"}},
			steps: []step{
				{tag: "lift", slot: 37},
				{tag: "p3", slot: 3, button: 1},
				{tag: "p5", slot: 5, button: 1},
				{tag: "p6", slot: 6, button: 1},
				{tag: "p7", slot: 7, button: 1},
				{tag: "p8", slot: 8, button: 1},
				{tag: "p9", slot: 9, button: 1},
				{tag: "back", slot: 37},
			},
		},
		{
			// A lone log anywhere in the 3x3 bbox-normalizes to its
			// planks recipe; bottom-right corner pins the slide.
			name: "log-corner", suf: "4", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_log 16"}},
			steps: []step{
				{tag: "lift", slot: 37},
				{tag: "log9", slot: 9, button: 1},
				{tag: "back", slot: 37},
				{tag: "take", slot: 0},
			},
		},
		{
			// Shapeless: diorite + cobblestone -> 2 andesite, in any
			// arrangement.
			name: "andesite", suf: "5", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:diorite 16"},
				{"hotbar.1", "minecraft:cobblestone 16"},
			},
			steps: []step{
				{tag: "lift-d", slot: 37},
				{tag: "d5", slot: 5, button: 1},
				{tag: "back-d", slot: 37},
				{tag: "lift-c", slot: 38},
				{tag: "c9", slot: 9, button: 1},
				{tag: "back-c", slot: 38},
				{tag: "take", slot: 0},
			},
		},
		{
			// Cake: three milk buckets over sugar/egg/sugar over
			// wheat. Whether the emptied buckets stay in the grid is
			// what this pins (the remainder source in 26.2 has no
			// dedicated component - measured here).
			name: "cake", suf: "6", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:milk_bucket 3"},
				{"hotbar.1", "minecraft:sugar 2"},
				{"hotbar.2", "minecraft:egg 1"},
				{"hotbar.3", "minecraft:wheat 3"},
			},
			steps: []step{
				{tag: "lift-milk", slot: 37},
				{tag: "m1", slot: 1, button: 1},
				{tag: "m2", slot: 2, button: 1},
				{tag: "m3", slot: 3, button: 1},
				{tag: "lift-sugar", slot: 38},
				{tag: "s4", slot: 4, button: 1},
				{tag: "s6", slot: 6, button: 1},
				{tag: "lift-egg", slot: 39},
				{tag: "e5", slot: 5, button: 1},
				{tag: "lift-wheat", slot: 40},
				{tag: "w7", slot: 7, button: 1},
				{tag: "w8", slot: 8, button: 1},
				{tag: "w9", slot: 9, button: 1},
				{tag: "take", slot: 0},
			},
		},
		{
			// Dispenser with a damaged bow: ingredient tests match
			// the item id, so damage is irrelevant. Grid:
			// cobble ring 1,2,3,4,6,7,9; bow at 5; redstone at 8.
			name: "dispenser", suf: "7", block: "minecraft:crafting_table", menu: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:cobblestone 64"},
				{"hotbar.1", "minecraft:bow[minecraft:damage=100]"},
				{"hotbar.2", "minecraft:redstone 8"},
			},
			steps: []step{
				{tag: "lift-c", slot: 37},
				{tag: "c1", slot: 1, button: 1},
				{tag: "c2", slot: 2, button: 1},
				{tag: "c3", slot: 3, button: 1},
				{tag: "c4", slot: 4, button: 1},
				{tag: "c6", slot: 6, button: 1},
				{tag: "c7", slot: 7, button: 1},
				{tag: "c9", slot: 9, button: 1},
				{tag: "back-c", slot: 37},
				{tag: "lift-bow", slot: 38},
				{tag: "b5", slot: 5},
				{tag: "lift-red", slot: 39},
				{tag: "r8", slot: 8, button: 1},
				{tag: "back-red", slot: 39},
				{tag: "take", slot: 0},
			},
		},
		{
			// Window-0 2x2: a log in the player grid crafts planks,
			// and the take consumes through the window-0 path.
			name: "p2x2", suf: "8", block: "minecraft:chest", menu: 2,
			gives: [][2]string{{"hotbar.0", "minecraft:oak_log 16"}},
			steps: []step{
				{tag: "lift", slot: 36, w0: true},
				{tag: "log", slot: 1, button: 1, w0: true},
				{tag: "back", slot: 36, w0: true},
				{tag: "take", slot: 0, w0: true},
				{tag: "park", slot: 9, w0: true},
			},
		},
		{
			// Special repair: two damaged pickaxes merge with the
			// 5%-of-max bonus; the result's damage component is the
			// whole comparison.
			name: "repair", suf: "9", block: "minecraft:chest", menu: 2,
			gives: [][2]string{
				{"hotbar.0", "minecraft:stone_pickaxe[minecraft:damage=80]"},
				{"hotbar.1", "minecraft:stone_pickaxe[minecraft:damage=100]"},
			},
			steps: []step{
				{tag: "lift-a", slot: 36, w0: true},
				{tag: "a1", slot: 1, w0: true},
				{tag: "lift-b", slot: 37, w0: true},
				{tag: "b2", slot: 2, w0: true},
				{tag: "take", slot: 0, w0: true},
				{tag: "park", slot: 9, w0: true},
			},
		},
		{
			// Repair only pairs same-id damageables: a pickaxe plus
			// a shovel must not produce a result.
			name: "repair-mismatch", suf: "10", block: "minecraft:chest", menu: 2,
			gives: [][2]string{
				{"hotbar.0", "minecraft:stone_pickaxe[minecraft:damage=80]"},
				{"hotbar.1", "minecraft:stone_shovel[minecraft:damage=80]"},
			},
			steps: []step{
				{tag: "lift-a", slot: 36, w0: true},
				{tag: "a1", slot: 1, w0: true},
				{tag: "lift-b", slot: 37, w0: true},
				{tag: "b2", slot: 2, w0: true},
				{tag: "take?", slot: 0, w0: true},
			},
		},
		{
			// Transmute: a bundle plus a dye recolors in place; the
			// input stack's components ride onto the result.
			name: "transmute-bundle", suf: "11", block: "minecraft:chest", menu: 2,
			gives: [][2]string{
				{"hotbar.0", "minecraft:bundle"},
				{"hotbar.1", "minecraft:red_dye 4"},
			},
			steps: []step{
				{tag: "lift-bundle", slot: 36, w0: true},
				{tag: "b1", slot: 1, w0: true},
				{tag: "lift-dye", slot: 37, w0: true},
				{tag: "d2", slot: 2, button: 1, w0: true},
				{tag: "back-dye", slot: 37, w0: true},
				{tag: "take", slot: 0, w0: true},
				{tag: "park", slot: 9, w0: true},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(addr, name string) []clickSnap {
				d := newClickDriverOn(t, addr, name, c.block, c.menu, c.gives, nil)
				snaps := make([]clickSnap, 0, len(c.steps))
				for _, s := range c.steps {
					d.click(s.tag, s.slot, s.button, s.mode, s.w0, false)
					cp := clickSnap{carried: d.snap.carried, menu: map[int32]string{}, p0: map[int32]string{}}
					for k, v := range d.snap.menu {
						cp.menu[k] = v
					}
					for k, v := range d.snap.p0 {
						cp.p0[k] = v
					}
					snaps = append(snaps, cp)
				}
				return snaps
			}
			want := run(tg.vanilla, "CraftV"+c.suf)
			got := run(tg.mcgo, "CraftM"+c.suf)
			for i, s := range c.steps {
				if diffs := diffSnaps(want[i], got[i]); len(diffs) > 0 {
					for _, df := range diffs {
						t.Errorf("%s step %s: %s", c.name, s.tag, df)
					}
				}
			}
		})
	}
}
