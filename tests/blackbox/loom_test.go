package blackbox

// TestLoomParity pins the loom menu on 26.2:
//   - the banner slot takes BannerItems only, the dye slot the
//     loom_dyes tag, the pattern slot loom_patterns items; the result
//     is take-only (measured mayPlace on LoomMenu$3/$4/$5/$6).
//   - an empty pattern slot offers the 32 no_item_required patterns
//     (registry order); a pattern item its namesake. A one-entry list
//     auto-selects; a kept selection rides Container Property 0.
//   - the result is a count-1 banner copy with the layer appended;
//     six layers refuse (selection -1, empty result). A take consumes
//     one banner and one dye — the pattern item is never consumed —
//     and plays ui.loom.take_result at the block.

import (
	"fmt"
	"testing"
	"time"
)

func TestLoomParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("LoomV", "LoomM"),
	}, nil)
	defer tg.cleanup()

	run := func(addr, name string) (map[string]string, *clickDriver) {
		d := newClickDriverOn(t, addr, name, "minecraft:loom", 18, [][2]string{
			{"inventory.0", "minecraft:white_banner 16"},
			{"inventory.1", "minecraft:black_banner 4"},
			{"inventory.2", "minecraft:creeper_banner_pattern"},
			{"inventory.3", "minecraft:flower_banner_pattern"},
			{"inventory.4", "minecraft:stone"},
			{"hotbar.0", "minecraft:blue_dye 8"},
			{"hotbar.1", "minecraft:red_dye 8"},
			{"hotbar.2", "minecraft:dirt"},
		}, nil)
		ev := map[string]string{}
		prop := func() int16 { return d.props[0] }
		step := func(tag string) {
			d.fold()
			ev[tag] = fmt.Sprintf("m0=%s|m1=%s|m2=%s|m3=%s|car=%s|prop=%d|snd=%v",
				d.snap.menu[0], d.snap.menu[1], d.snap.menu[2], d.snap.menu[3],
				d.snap.carried, prop(), d.soundIDs)
		}
		click := func(tag string, slot int16, btn byte, mode int32) {
			d.click(tag, slot, btn, mode, false, false)
			step(tag)
		}
		button := func(tag string, i int32) {
			d.button(tag, i)
			step(tag)
		}
		// 4 container slots: hotbar.i = 31+i, inventory.i = 4+i.
		click("pick-white", 4, 0, 0)
		click("banner-in", 0, 0, 0)
		if got := d.snap.menu[0]; got != "1296#16" {
			t.Fatalf("%s: banner-in slot0=%s want 1296#16: %v", name, got, d.snap.menu)
		}
		click("pick-blue", 31, 0, 0)
		click("dye-in", 1, 0, 0)
		if got := d.snap.menu[3]; got != "-" {
			t.Fatalf("%s: pre-select result=%s want empty: %v", name, got, d.snap.menu)
		}
		button("sel-5", 5) // free list idx 5 = stripe_top
		if got := d.snap.menu[3]; got != "1296#1[bpstripe_top:blue]" {
			t.Fatalf("%s: sel-5 result=%s: %v", name, got, d.snap.menu)
		}
		if got := prop(); got != 5 {
			t.Fatalf("%s: sel-5 prop=%d want 5", name, got)
		}
		button("sel-bad", 33) // out of range: ignored
		button("sel-neg", -1)
		if got := d.snap.menu[3]; got != "1296#1[bpstripe_top:blue]" {
			t.Fatalf("%s: bad buttons changed result=%s", name, got)
		}
		click("take-1", 3, 0, 0)
		click("park-1", 9, 0, 0)
		if got := d.snap.menu[3]; got != "1296#1[bpstripe_top:blue]" {
			t.Fatalf("%s: post-take result=%s want re-displayed: %v", name, got, d.snap.menu)
		}
		if got := d.snap.menu[0]; got != "1296#15" {
			t.Fatalf("%s: post-take banner=%s want 1296#15: %v", name, got, d.snap.menu)
		}
		button("sel-20", 20) // free list idx 20 = diagonal_up_right
		click("take-2", 3, 0, 0)
		click("park-2", 10, 0, 0)
		// Feed the loomed banner back: slot swap puts the layered one
		// on top and the take appends a second layer.
		click("re-pick", 9, 0, 0)
		click("swap-in", 0, 0, 0)
		if got := d.snap.menu[0]; got != "1296#1[bpstripe_top:blue]" {
			t.Fatalf("%s: swap-in slot0=%s: %v", name, got, d.snap.menu)
		}
		click("park-wh", 12, 0, 0)
		if got := d.snap.menu[3]; got != "1296#1[bpstripe_top:blue,diagonal_up_right:blue]" {
			t.Fatalf("%s: 2-layer result=%s: %v", name, got, d.snap.menu)
		}
		click("take-3", 3, 0, 0)
		click("park-3", 11, 0, 0)
		if got := d.snap.menu[3]; got != "-" {
			t.Fatalf("%s: emptied-banner result=%s want -: %v", name, got, d.snap.menu)
		}
		if got := prop(); got != -1 {
			t.Fatalf("%s: emptied-banner prop=%d want -1", name, got)
		}
		click("re-white", 12, 0, 0)
		click("white-in", 0, 0, 0)
		// Black banners swap in for the whites.
		click("pick-black", 5, 0, 0)
		click("black-swap", 0, 0, 0)
		click("park-wh2", 12, 0, 0)
		button("sel-1", 1) // free list idx 1 = square_bottom_right
		if got := d.snap.menu[3]; got != "1311#1[bpsquare_bottom_right:blue]" {
			t.Fatalf("%s: black sel-1 result=%s: %v", name, got, d.snap.menu)
		}
		click("take-black", 3, 0, 0)
		click("park-black", 13, 0, 0)
		// A pattern item narrows the list to its namesake and
		// auto-selects (one entry).
		click("pick-creep", 6, 0, 0)
		click("creep-in", 2, 0, 0)
		if got := d.snap.menu[3]; got != "1311#1[bpcreeper:blue]" {
			t.Fatalf("%s: creeper result=%s: %v", name, got, d.snap.menu)
		}
		if got := prop(); got != 0 {
			t.Fatalf("%s: creeper prop=%d want 0", name, got)
		}
		button("sel-1of1", 5) // out of a 1-entry list: dropped
		if got := d.snap.menu[3]; got != "1311#1[bpcreeper:blue]" {
			t.Fatalf("%s: 1-of-1 bad button changed result=%s", name, got)
		}
		click("take-4", 3, 0, 0)
		click("park-4", 14, 0, 0)
		// The pattern item swaps for another.
		click("pick-flower", 7, 0, 0)
		click("flower-in", 2, 0, 0)
		click("park-creep", 15, 0, 0)
		if got := d.snap.menu[3]; got != "1311#1[bpflower:blue]" {
			t.Fatalf("%s: flower result=%s: %v", name, got, d.snap.menu)
		}
		click("take-5", 3, 0, 0)
		click("park-5", 16, 0, 0)
		// Pulling the pattern item resets: flower is gated out of the
		// free list, so the kept index cannot carry over.
		click("pat-out", 2, 0, 0)
		if got := d.snap.menu[3]; got != "-" {
			t.Fatalf("%s: pat-out result=%s want -: %v", name, got, d.snap.menu)
		}
		if got := prop(); got != -1 {
			t.Fatalf("%s: pat-out prop=%d want -1", name, got)
		}
		click("pat-in2", 2, 0, 0)
		if got := d.snap.menu[3]; got != "1311#1[bpflower:blue]" {
			t.Fatalf("%s: flower re-in result=%s: %v", name, got, d.snap.menu)
		}
		// The last black banner take empties the slot; blue dye is
		// down to 1.
		click("take-6", 3, 0, 0)
		click("park-6", 17, 0, 0)
		if got := d.snap.menu[3]; got != "-" {
			t.Fatalf("%s: banner-empty result=%s: %v", name, got, d.snap.menu)
		}
		// mayPlace: dirt refuses every slot, stone the pattern slot,
		// dyes the banner slot, banners the dye slot.
		click("pick-dirt", 33, 0, 0)
		click("dirt-banner", 0, 0, 0)
		click("dirt-dye", 1, 0, 0)
		click("dirt-pat", 2, 0, 0)
		if got := d.snap.carried; got != "55#1" {
			t.Fatalf("%s: dirt refusals carried=%s want 55#1: %v", name, got, d.snap.menu)
		}
		click("park-dirt", 33, 0, 0)
		click("pick-stone", 8, 0, 0)
		click("stone-pat", 2, 0, 0)
		if got := d.snap.carried; got != "1#1" {
			t.Fatalf("%s: stone refusal carried=%s want 1#1", name, got)
		}
		click("park-stone", 8, 0, 0)
		click("pick-red", 32, 0, 0)
		click("red-banner", 0, 0, 0)
		if got := d.snap.carried; got != "1109#8" {
			t.Fatalf("%s: dye-into-banner carried=%s want 1109#8", name, got)
		}
		click("red-dye", 1, 0, 0) // dye accepts dye: swaps blue 1 out
		click("park-blue", 18, 0, 0)
		click("pick-wh3", 12, 0, 0)
		click("white-dye", 1, 0, 0) // banner into dye slot refused
		if got := d.snap.carried; got != "1296#14" {
			t.Fatalf("%s: banner-into-dye carried=%s want 1296#14", name, got)
		}
		click("white-in2", 0, 0, 0)
		if got := d.snap.menu[3]; got != "1296#1[bpflower:red]" {
			t.Fatalf("%s: white+red result=%s: %v", name, got, d.snap.menu)
		}
		// Six-layer cap: feed each take back in; once the input carries
		// 6 layers slotsChanged refuses (empty result, selection -1).
		for i := 2; i <= 7; i++ {
			click(fmt.Sprintf("cap-take%d", i), 3, 0, 0)
			click(fmt.Sprintf("cap-in%d", i), 0, 0, 0)
			// cap-in either swapped the plain stack onto the cursor
			// (first pass) or placed onto the emptied slot — park into
			// a distinct empty slot each pass so an empty cursor is a
			// no-op instead of a pickup.
			click(fmt.Sprintf("cap-park%d", i), int16(18+i), 0, 0)
		}
		want6 := "1296#1[bpflower:red,flower:red,flower:red,flower:red,flower:red,flower:red]"
		// The banner in slot 0 now carries 6 layers; the result is
		// empty and the selection -1.
		if got := d.snap.menu[0]; got != want6 {
			t.Fatalf("%s: 6-layer banner=%s want %s: %v car=%s", name, got, want6, d.snap.menu, d.snap.carried)
		}
		step("cap")
		if got := d.snap.menu[3]; got != "-" {
			t.Fatalf("%s: cap result=%s want -: %v", name, got, d.snap.menu)
		}
		if got := prop(); got != -1 {
			t.Fatalf("%s: cap prop=%d want -1", name, got)
		}
		// clickMenuButton itself has no layer check: selecting at 6
		// layers still writes a seventh onto the result.
		button("cap-sel", 0)
		want7 := "1296#1[bpflower:red,flower:red,flower:red,flower:red,flower:red,flower:red,flower:red]"
		if got := d.snap.menu[3]; got != want7 {
			t.Fatalf("%s: cap-sel result=%s want %s", name, got, want7)
		}
		if got := prop(); got != 0 {
			t.Fatalf("%s: cap-sel prop=%d want 0", name, got)
		}
		time.Sleep(200 * time.Millisecond)
		d.fold()
		d.close()
		time.Sleep(200 * time.Millisecond)
		d.fold()
		return ev, d
	}

	want, dv := run(tg.vanilla, "LoomV")
	got, dm := run(tg.mcgo, "LoomM")
	for tag, w := range want {
		if got[tag] != w {
			t.Errorf("step %s:\n  vanilla %s\n  mc-go   %s", tag, w, got[tag])
		}
	}
	for _, line := range diffSnaps(dv.snap, dm.snap) {
		t.Errorf("final snap: %s", line)
	}
}
