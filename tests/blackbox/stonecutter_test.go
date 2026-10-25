package blackbox

// TestStonecutterParity pins the stonecutter menu: the input slot
// takes anything (no mayPlace filter — measured: dirt swaps in), a
// container button click selects a recipe by index into the filtered
// list (datapack order), the result slot mirrors the selection, each
// take shrinks the input by one and plays ui.stonecutter.take_result
// (sound holder 1673 = id+1), and the selection resets when the input
// item type changes.

import (
	"fmt"
	"testing"
	"time"
)

func TestStonecutterParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CutV", "CutM"),
	}, nil)
	defer tg.cleanup()

	run := func(addr, name string) (map[string]string, *clickDriver) {
		d := newClickDriverOn(t, addr, name, "minecraft:stonecutter", 24, [][2]string{
			{"hotbar.0", "minecraft:stone 2"},
			{"hotbar.1", "minecraft:andesite"},
			{"hotbar.2", "minecraft:dirt"},
		}, nil)
		ev := map[string]string{}
		step := func(tag string, slot int16, btn byte, mode int32) {
			d.click(tag, slot, btn, mode, false, false)
			d.fold()
			ev[tag] = fmt.Sprintf("menu=%v|p0=%v|carried=%q|sounds=%d|snd=%v|announce=%d",
				d.snap.menu, d.snap.p0, d.snap.carried, d.sounds, d.soundIDs, d.announce)
		}
		btn := func(tag string, i int32) {
			d.button(tag, i)
			d.fold()
			ev[tag] = fmt.Sprintf("menu=%v|p0=%v|carried=%q|sounds=%d|snd=%v|announce=%d",
				d.snap.menu, d.snap.p0, d.snap.carried, d.sounds, d.soundIDs, d.announce)
		}
		// 2 container slots: hotbar.i sits at menu slot 29+i.
		step("pick-stone", 29, 0, 0)
		step("stone-in", 0, 0, 0)
		// The filtered list's order: chiseled_stone_bricks, cobblestone,
		// cobblestone_slab/stairs/wall, stone_brick_* — datapack order.
		btn("sel-0", 0)
		btn("sel-1", 1)
		btn("sel-5", 5)
		btn("sel-99", 99) // out of range: ignored, result stays
		if got := d.snap.menu[1]; got != "318#2" {
			t.Fatalf("%s: sel-99 result=%s want stone_brick_slab still selected: %v", name, got, d.snap.menu)
		}
		step("take", 1, 0, 0)
		step("park", 2, 0, 0)
		// Input left at 1 count: same item type keeps the selection
		// and the result re-displays.
		if got := d.snap.menu[1]; got != "318#2" {
			t.Fatalf("%s: post-take result=%s want stone_brick_slab again: %v", name, got, d.snap.menu)
		}
		step("take2", 1, 0, 0)
		step("park2", 2, 0, 0)
		// Item-type change resets: andesite's own filtered list now.
		step("pick-and", 30, 0, 0)
		step("and-in", 0, 0, 0)
		btn("asel-0", 0)
		if got := d.snap.menu[1]; got != "736#2" {
			t.Fatalf("%s: andesite sel-0 result=%s want andesite_slab 2: %v", name, got, d.snap.menu)
		}
		// No mayPlace on the input: dirt swaps in with the andesite
		// coming to the cursor.
		step("pick-dirt", 31, 0, 0)
		step("dirt-in", 0, 0, 0)
		if got := d.snap.menu[0]; got != "55#1" {
			t.Fatalf("%s: dirt-in slot0=%s want dirt: %v", name, got, d.snap.menu)
		}
		// Dirt has no recipes: the list is empty and the result
		// cleared when the input type changed.
		btn("dirt-sel", 0)
		if got := d.snap.menu[1]; got != "-" {
			t.Fatalf("%s: dirt selected result=%s want empty: %v", name, got, d.snap.menu)
		}
		time.Sleep(200 * time.Millisecond)
		d.fold()
		return ev, d
	}

	want, dv := run(tg.vanilla, "CutV")
	got, dm := run(tg.mcgo, "CutM")
	for tag, w := range want {
		if got[tag] != w {
			t.Errorf("step %s:\n  vanilla %s\n  mc-go   %s", tag, w, got[tag])
		}
	}
	for _, line := range diffSnaps(dv.snap, dm.snap) {
		t.Errorf("final snap: %s", line)
	}
}
