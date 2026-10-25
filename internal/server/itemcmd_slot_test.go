package server

import "testing"

// The /item slot registry maps every name to one global index, measured
// off the real server (see item_slots.tsv and TestItemCommandParity). The
// numbered families alias: hotbar.N is container.N, and inventory.N is
// container.9+N. The player has 0-35; container.36-53 and horse.4-14 are
// known names it does not have.
func TestItemSlotRegistry(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int32
	}{
		{"container.0", 0},
		{"container.35", 35},
		{"container.53", 53},
		{"hotbar.0", 0},
		{"hotbar.8", 8},
		{"inventory.0", 9},
		{"inventory.26", 35},
		{"enderchest.0", 200},
		{"weapon.mainhand", itemSlotMainhand},
		{"weapon.offhand", itemSlotOffhand},
		{"armor.feet", itemSlotFeet},
		{"armor.head", itemSlotHead},
		{"armor.body", itemSlotBody},
		{"player.cursor", itemSlotCursor},
		{"player.crafting.0", 500},
		{"horse.0", 500},
		{"horse.3", 503},
		{"horse.14", 514},
	} {
		id, ok := parseItemSlot(tc.name)
		if !ok || id != tc.want {
			t.Errorf("%s: got (%d, %v), want (%d, true)", tc.name, id, ok, tc.want)
		}
	}
	for _, name := range []string{
		"container.54", "hotbar.9", "inventory.27", "horse.15",
		"bogus.0", "container", "container.0.5", "Container.0",
	} {
		if id, ok := parseItemSlot(name); ok {
			t.Errorf("%s: parsed as %d, want unknown", name, id)
		}
	}
	for id := int32(0); id <= 35; id++ {
		if !playerSlotValid(id) {
			t.Errorf("container.%d: player should have it", id)
		}
	}
	for _, id := range []int32{36, 40, 41, 53, 504, 514} {
		if playerSlotValid(id) {
			t.Errorf("index %d: player should not have it", id)
		}
	}
	// 0-8 are the hotbar slots of the window, 9-35 line up directly.
	for id := int32(0); id <= 8; id++ {
		if got := entityMainSlot(id); got != int16(slotHotbarStart+id) {
			t.Errorf("index %d: maps to %d, want %d", id, got, slotHotbarStart+id)
		}
	}
	for id := int32(9); id <= 35; id++ {
		if got := entityMainSlot(id); got != int16(id) {
			t.Errorf("index %d: maps to %d, want %d", id, got, id)
		}
	}
}
