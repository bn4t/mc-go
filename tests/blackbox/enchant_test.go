package blackbox

import (
	"testing"
	"time"
)

// TestEnchantParity drives the enchanting table end to end on both
// servers: open shows the ten container properties (costs, seed,
// clues), inserting an enchantable item plus lapis rolls the three
// offers, a button click enchants the item (book transmutes to
// enchanted_book), consumes i+1 lapis and levels, re-rolls the seed,
// and fires enchanted_item (the Enchanter announce). Offer VALUES are
// per-player-seeded, so the compare covers shapes and bookkeeping, not
// the rolled enchantment ids.
func TestEnchantParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("EnchV", "EnchM"),
	}, nil)
	defer tg.cleanup()

	run := func(addr, name string) {
		d := newClickDriverOn(t, addr, name, "minecraft:enchanting_table", 13, [][2]string{
			{"hotbar.0", "minecraft:book"},
			{"hotbar.1", "minecraft:lapis_lazuli 10"},
		}, nil)
		sendCommand(t, d.pw, "xp set @s 30 levels")
		if !digSentinel(t, d.pw, d.sink, "XPSET", 10*time.Second) {
			t.Fatalf("%s: no xp reply", name)
		}
		d.fold()

		// At open every property was broadcast once: zero costs and
		// clues, and a fresh player's seed of 0.
		for i := int16(0); i < 3; i++ {
			if d.props[i] != 0 {
				t.Fatalf("%s: open cost[%d]=%d want 0", name, i, d.props[i])
			}
		}
		if d.props[3] != 0 {
			t.Fatalf("%s: open seed=%d want 0", name, d.props[3])
		}
		for i := int16(4); i < 10; i++ {
			if d.props[i] != -1 {
				t.Fatalf("%s: open clue[%d]=%d want -1", name, i, d.props[i])
			}
		}

		// Book in, lapis in: three offers roll (books accept every
		// in-table enchantment at any positive cost).
		d.props = map[int16]int16{}
		d.click("book-in", 29, 0, 1, false, false)
		d.click("lapis-in", 30, 0, 1, false, false)
		if got := d.snap.menu[0]; got != "1058#1" {
			t.Fatalf("%s: slot0=%s want book 1058#1", name, got)
		}
		if got := d.snap.menu[1]; got != "928#10" {
			t.Fatalf("%s: slot1=%s want lapis 928#10", name, got)
		}
		for i := int16(0); i < 3; i++ {
			cost, okc := d.props[i]
			clue, okl := d.props[4+i]
			lvl, okv := d.props[7+i]
			if !okc || !okl || !okv {
				t.Fatalf("%s: offer %d props missing: %v", name, i, d.props)
			}
			if cost <= 0 || clue < 0 || lvl <= 0 {
				t.Fatalf("%s: offer %d cost=%d clue=%d lvl=%d", name, i, cost, clue, lvl)
			}
		}
		if cost := d.props[0]; cost > 3 {
			// Power-0 offers cap at j/3 or 1; a cost this high would
			// fail the level check anyway, but flag it as off-script.
			t.Fatalf("%s: cost[0]=%d implausible for power 0", name, cost)
		}

		// Survival so the click consumes lapis — in creative vanilla's
		// ItemStack.consume respects infinite materials and the stack
		// stays 10. Levels drop either way.
		sendCommand(t, d.pw, "gamemode survival @s")
		if !digSentinel(t, d.pw, d.sink, "GMS", 10*time.Second) {
			t.Fatalf("%s: no gamemode reply", name)
		}
		d.fold()

		// Click option 0: costs one level (i+1) and one lapis, the book
		// becomes an enchanted_book, the seed re-rolls, the offers
		// clear (the result is not enchantable), Enchanter announces,
		// and the table-use sound packet goes out.
		d.snap.menu = map[int32]string{}
		d.props = map[int16]int16{}
		d.xpSeen = false
		d.button("enchant", 0)
		if got := d.snap.menu[0]; got != "1274#1" {
			t.Fatalf("%s: slot0=%s want enchanted_book 1274#1", name, got)
		}
		if got := d.snap.menu[1]; got != "928#9" {
			t.Fatalf("%s: slot1=%s want lapis 928#9", name, got)
		}
		if !d.xpSeen || d.xpLevel != 29 {
			t.Fatalf("%s: xp=%d seen=%v want 29", name, d.xpLevel, d.xpSeen)
		}
		if d.props[3] == 0 {
			t.Fatalf("%s: seed did not re-roll", name)
		}
		for i := int16(0); i < 3; i++ {
			if d.props[i] != 0 || d.props[4+i] != -1 || d.props[7+i] != -1 {
				t.Fatalf("%s: post-enchant offer %d not cleared: %v", name, i, d.props)
			}
		}
		if d.announce == 0 {
			t.Fatalf("%s: no Enchanter announce", name)
		}
		if d.sndPkts == 0 {
			t.Fatalf("%s: no table-use sound packet", name)
		}
	}

	run(tg.vanilla, "EnchV")
	run(tg.mcgo, "EnchM")
}
