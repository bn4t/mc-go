package blackbox

// Menu-click parity beyond the chest: hopper, crafting-table, and
// barrel menus run an identical pickup/place/clone/close script on
// both servers, comparing logical slot state after every step. This
// pins each menu's click answer, including the cursor sync and the
// close that returns a full cursor to the inventory. The crafting
// table keeps no storage, so its close also returns the grid; the
// script ends with grid dirt and a cobble cursor to pin which lands
// first.

import (
	"testing"
)

func TestMenuClickParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("MenuVh", "MenuMh", "MenuVc", "MenuMc", "MenuVb", "MenuMb"),
	}, nil)
	defer tg.cleanup()

	type miniStep struct {
		tag        string
		slot       int16
		button     byte
		mode       int32
		closeFirst bool
	}
	cases := []struct {
		name   string
		block  string
		menuID int32
		gives  [][2]string
		steps  []miniStep
	}{
		{
			name: "hopper", block: "minecraft:hopper", menuID: 16,
			gives: [][2]string{{"hotbar.0", "minecraft:dirt 64"}},
			steps: []miniStep{
				{tag: "pickup", slot: 32},
				{tag: "place", slot: 0},
				{tag: "clone", slot: 0, button: 2, mode: 3},
				{tag: "close", closeFirst: true},
			},
		},
		{
			name: "crafting", block: "minecraft:crafting_table", menuID: 12,
			gives: [][2]string{
				{"hotbar.0", "minecraft:dirt 64"},
				{"hotbar.1", "minecraft:cobblestone 32"},
			},
			steps: []miniStep{
				{tag: "pickup-dirt", slot: 37},
				{tag: "place-grid", slot: 1},
				{tag: "clone-grid", slot: 1, button: 2, mode: 3},
				// Full dirt cursor swaps for the cobble: the close
				// returns grid dirt and cursor cobble together.
				{tag: "swap-cobble", slot: 38},
				{tag: "close", closeFirst: true},
			},
		},
		{
			name: "barrel", block: "minecraft:barrel", menuID: 2,
			gives: [][2]string{{"hotbar.0", "minecraft:dirt 64"}},
			steps: []miniStep{
				{tag: "pickup", slot: 54},
				{tag: "place", slot: 0},
				{tag: "clone", slot: 0, button: 2, mode: 3},
				{tag: "close", closeFirst: true},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(addr, name string) []clickSnap {
				d := newClickDriverOn(t, addr, name, c.block, c.menuID, c.gives, nil)
				snaps := make([]clickSnap, 0, len(c.steps))
				for _, s := range c.steps {
					if s.closeFirst {
						d.close()
					} else {
						d.click(s.tag, s.slot, s.button, s.mode, false, false)
					}
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

			suffix := map[string]string{"hopper": "h", "crafting": "c", "barrel": "b"}[c.name]
			want := run(tg.vanilla, "MenuV"+suffix)
			got := run(tg.mcgo, "MenuM"+suffix)
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
