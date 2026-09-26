//go:build blackbox

package blackbox

// TestSmithingTransformParity pins the smithing table's upgrade flow:
// template + base + addition produce the transmuted result stack —
// the recipe's result item carrying the base's whole component patch —
// a take shrinks all three ingredient slots by one and clears the
// result, and the input slots' mayPlace refuses stacks no recipe
// lists. Vanilla emits no sound, world event, or advancement on the
// take, so the snap compare covers packet shapes as well as contents.

import (
	"fmt"
	"testing"
	"time"
)

func TestSmithingTransformParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("SmithV", "SmithM"),
	}, nil)
	defer tg.cleanup()

	run := func(addr, name string) (map[string]string, *clickDriver) {
		d := newClickDriverOn(t, addr, name, "minecraft:smithing_table", 21, [][2]string{
			{"hotbar.0", `minecraft:diamond_chestplate[minecraft:custom_name="Bob",minecraft:enchantments={"minecraft:protection":2}]`},
			{"hotbar.1", "minecraft:netherite_upgrade_smithing_template"},
			{"hotbar.2", "minecraft:netherite_ingot 2"},
			{"hotbar.3", "minecraft:dirt"},
		}, nil)
		ev := map[string]string{}
		step := func(tag string, slot int16, btn byte, mode int32) {
			d.click(tag, slot, btn, mode, false, false)
			d.fold()
			ev[tag] = fmt.Sprintf("menu=%v|p0=%v|carried=%q|sounds=%d|snd=%v|announce=%d",
				d.snap.menu, d.snap.p0, d.snap.carried, d.sounds, d.soundIDs, d.announce)
		}
		// hotbar.i sits at menu slot 31+i (4 container + 27 main).
		step("pick-base", 31, 0, 0)
		step("base-in", 1, 0, 0)
		step("pick-tmpl", 32, 0, 0)
		step("tmpl-in", 0, 0, 0)
		step("pick-add", 33, 0, 0)
		step("add-in", 2, 0, 0)
		if got := d.snap.menu[3]; got != "1007#1[n\"Bob\"][e28:2]" {
			t.Fatalf("%s: result=%s want netherite_chestplate w/ base components: %v", name, got, d.snap.menu)
		}
		step("take", 3, 0, 0)
		step("take-empty", 3, 0, 0)
		// Park the result on the hotbar so the dirt probe has a free
		// cursor, then try dirt into the base slot: mayPlace refuses
		// it and the stack stays on the cursor.
		step("park", 31, 0, 0)
		step("pick-dirt", 34, 0, 0)
		step("dirt-in", 1, 0, 0)
		if d.snap.carried != "55#1" {
			t.Fatalf("%s: dirt-in carried=%q want dirt still held", name, d.snap.carried)
		}
		step("dirt-back", 34, 0, 0)
		time.Sleep(200 * time.Millisecond)
		d.fold()
		return ev, d
	}

	want, dv := run(tg.vanilla, "SmithV")
	got, dm := run(tg.mcgo, "SmithM")
	for tag, w := range want {
		if got[tag] != w {
			t.Errorf("step %s:\n  vanilla %s\n  mc-go   %s", tag, w, got[tag])
		}
	}
	for _, line := range diffSnaps(dv.snap, dm.snap) {
		t.Errorf("final snap: %s", line)
	}
}
