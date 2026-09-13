package blackbox

// Menu-click parity beyond the chest: hopper, crafting-table, barrel,
// and furnace menus run an identical pickup/place/clone/close script
// on both servers, comparing logical slot state after every step. This
// pins each menu's click answer, including the cursor sync and the
// close that returns a full cursor to the inventory. The crafting
// table keeps no storage, so its close also returns the grid; the
// script ends with grid dirt and a cobble cursor to pin which lands
// first. The furnace script pins input/fuel accept rules, take-only
// output, and quick-move destinations.

import (
	"fmt"
	"testing"
	"time"
)

func TestMenuClickParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("MenuVh", "MenuMh", "MenuVc", "MenuMc", "MenuVb", "MenuMb", "MenuVf", "MenuMf"),
	}, nil)
	defer tg.cleanup()

	type miniStep struct {
		tag        string
		slot       int16
		button     byte
		mode       int32
		closeFirst bool
		ops        []string
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
		{
			// Furnace menu: 0 input, 1 fuel, 2 result, 3-29 main,
			// 30-38 hotbar. Input takes smeltables, fuel takes fuel,
			// the result is take-only; the output fill runs through
			// /item on the open block.
			name: "furnace", block: "minecraft:furnace", menuID: 14,
			gives: [][2]string{
				{"hotbar.0", "minecraft:dirt 64"},
				{"hotbar.1", "minecraft:coal 16"},
				{"hotbar.2", "minecraft:raw_iron 16"},
				{"hotbar.3", "minecraft:coal 16"},
				{"hotbar.4", "minecraft:oak_log 16"},
			},
			steps: []miniStep{
				{tag: "pickup-dirt", slot: 30},
				{tag: "place-input-dirt", slot: 0},
				{tag: "place-main", slot: 3},
				{tag: "pickup-coal", slot: 31},
				{tag: "place-fuel", slot: 1},
				{tag: "pickup-iron", slot: 32},
				{tag: "place-input", slot: 0},
				{tag: "shift-coal", slot: 33, mode: 1},
				{tag: "shift-log", slot: 34, mode: 1},
				{tag: "swap-input", slot: 0, mode: 2},
				{tag: "fill-output", ops: []string{
					"item replace block 8 -61 0 container.2 with minecraft:iron_ingot 8",
				}},
				{tag: "clone-output", slot: 2, button: 2, mode: 3},
				{tag: "place-ingots", slot: 4},
				{tag: "take-output", slot: 2},
				{tag: "close", closeFirst: true},
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := func(addr, name string) []clickSnap {
				d := newClickDriverOn(t, addr, name, c.block, c.menuID, c.gives, nil)
				snaps := make([]clickSnap, 0, len(c.steps))
				opN := 0
				for _, s := range c.steps {
					switch {
					case s.closeFirst:
						d.close()
					case len(s.ops) > 0:
						for _, cmd := range s.ops {
							sendCommand(t, d.pw, cmd)
							opN++
							if !digSentinel(t, d.pw, d.sink, fmt.Sprintf("MNOP%s%d", name, opN), 10*time.Second) {
								t.Fatalf("%s: no op ack", name)
							}
						}
						d.fold()
					default:
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

			suffix := map[string]string{"hopper": "h", "crafting": "c", "barrel": "b", "furnace": "f"}[c.name]
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
