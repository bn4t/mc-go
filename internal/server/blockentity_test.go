package server

import (
	"path/filepath"
	"testing"

	"mc-go/internal/world"
)

// Block entities are the half of a chunk a client cannot rebuild: the
// blocks say a chest stands there and nothing about what is in it.
// This drives the whole path a restart takes - save a world, load it
// into a fresh server - and checks the contents came back.
//
// The cross-server half, that the same file is the real server's own
// format, is the blackbox suite's TestWorldBlockEntityParity.
func TestBlockEntityRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "world")
	saved := New("unused")
	saved.WorldDir = dir
	d := saved.dims[dimOverworld]

	chestPos := world.Pos{X: 5, Y: -60, Z: 5}
	hoist := func(name string, pos world.Pos) world.Block {
		b, ok := world.BlockState(name)
		if !ok {
			t.Fatalf("unknown block %s", name)
		}
		saved.world.SetBlock(pos, b)
		return b
	}
	hoist("chest", chestPos)
	chest := saved.chestAt(d, chestPos)
	chest.slots[0] = ItemStack{ID: world.ItemIDs["diamond"], Count: 3}
	// A shulker box inside the chest: its contents travel with the
	// item, in a data component rather than in slots of the chest.
	var box [shulkerSlots]ItemStack
	box[2] = ItemStack{ID: world.ItemIDs["apple"], Count: 1}
	box[7] = ItemStack{ID: world.ItemIDs["emerald"], Count: 5}
	chest.slots[26] = ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: &box}

	signPos := world.Pos{X: 6, Y: -60, Z: 6}
	hoist("oak_sign", signPos)
	saved.ensureSign(d, signPos)
	d.signs.mu.Lock()
	d.signs.signs[signPos].Front = [4]string{`"hello"`, `{"text":"there"}`, "", ""}
	d.signs.signs[signPos].Back = [4]string{`"behind"`, "", "", ""}
	d.signs.mu.Unlock()

	furnacePos := world.Pos{X: 7, Y: -60, Z: 7}
	hoist("furnace", furnacePos)
	f := saved.furnaceAt(d, furnacePos)
	f.slots[furnaceInput] = ItemStack{ID: world.ItemIDs["raw_iron"], Count: 2}
	f.slots[furnaceFuel] = ItemStack{ID: world.ItemIDs["coal"], Count: 1}
	f.burn, f.burnTotal, f.cook, f.cookTotal = 1234, 1600, 37, 200

	hopperPos := world.Pos{X: 8, Y: -60, Z: 8}
	hoist("hopper", hopperPos)
	h := saved.hopperAt(d, hopperPos)
	h.slots[3] = ItemStack{ID: world.ItemIDs["gold_ingot"], Count: 12}
	h.cooldown = 4

	barrelPos := world.Pos{X: 9, Y: -60, Z: 9}
	hoist("barrel", barrelPos)
	barrel := saved.containerAt(d, barrelPos)
	if barrel == nil {
		t.Fatal("no container for a barrel")
	}
	barrel.slots[1] = ItemStack{ID: world.ItemIDs["apple"], Count: 7}

	brewPos := world.Pos{X: 10, Y: -60, Z: 10}
	hoist("brewing_stand", brewPos)
	brew := saved.containerAt(d, brewPos)
	if brew == nil {
		t.Fatal("no container for a brewing stand")
	}
	brew.slots[3] = ItemStack{ID: world.ItemIDs["blaze_powder"], Count: 2}
	brew.brewTime, brew.fuel = 111, 17

	if err := saved.SaveWorld(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A second server on the same directory is what a restart is.
	loaded := New("unused")
	loaded.WorldDir = dir
	if err := loaded.LoadWorld(); err != nil {
		t.Fatalf("load: %v", err)
	}
	ld := loaded.dims[dimOverworld]

	gotChest := loaded.chestAt(ld, chestPos)
	if got := gotChest.slots[0]; got.ID != world.ItemIDs["diamond"] || got.Count != 3 {
		t.Errorf("chest slot 0 = %+v, want 3 diamonds", got)
	}
	gotBox := gotChest.slots[26]
	if gotBox.ID != world.ItemIDs["shulker_box"] {
		t.Fatalf("chest slot 26 = %+v, want the shulker box back", gotBox)
	}
	if gotBox.Contents == nil {
		t.Fatal("the shulker box came back empty")
	}
	if got := gotBox.Contents[2]; got.ID != world.ItemIDs["apple"] || got.Count != 1 {
		t.Errorf("box slot 2 = %+v, want one apple", got)
	}
	if got := gotBox.Contents[7]; got.ID != world.ItemIDs["emerald"] || got.Count != 5 {
		t.Errorf("box slot 7 = %+v, want 5 emeralds", got)
	}
	if got := gotChest.slots[25]; !got.Empty() {
		t.Errorf("chest slot 25 = %+v, want it empty", got)
	}

	st, ok := loaded.signAt(ld, signPos)
	if !ok {
		t.Fatal("the sign did not come back")
	}
	if st.Front[0] != `"hello"` || st.Front[1] != `{"text":"there"}` || st.Front[2] != "" {
		t.Errorf("sign front = %q, want the lines as the client typed them", st.Front)
	}
	if st.Back[0] != `"behind"` {
		t.Errorf("sign back = %q, want the back line", st.Back)
	}

	gotFurnace := loaded.furnaceAt(ld, furnacePos)
	if got := gotFurnace.slots[furnaceInput]; got.ID != world.ItemIDs["raw_iron"] || got.Count != 2 {
		t.Errorf("furnace input = %+v, want 2 raw iron", got)
	}
	if got := gotFurnace.slots[furnaceFuel]; got.ID != world.ItemIDs["coal"] || got.Count != 1 {
		t.Errorf("furnace fuel = %+v, want one coal", got)
	}
	if gotFurnace.burn != 1234 || gotFurnace.burnTotal != 1600 || gotFurnace.cook != 37 || gotFurnace.cookTotal != 200 {
		t.Errorf("furnace counters = %d/%d %d/%d, want 1234/1600 37/200",
			gotFurnace.burn, gotFurnace.burnTotal, gotFurnace.cook, gotFurnace.cookTotal)
	}

	gotHopper := loaded.hopperAt(ld, hopperPos)
	if got := gotHopper.slots[3]; got.ID != world.ItemIDs["gold_ingot"] || got.Count != 12 {
		t.Errorf("hopper slot 3 = %+v, want 12 gold ingots", got)
	}
	if gotHopper.cooldown != 4 {
		t.Errorf("hopper cooldown = %d, want 4", gotHopper.cooldown)
	}

	gotBarrel := loaded.containerAt(ld, barrelPos)
	if gotBarrel == nil {
		t.Fatal("the barrel did not come back")
	}
	if got := gotBarrel.slots[1]; got.ID != world.ItemIDs["apple"] || got.Count != 7 {
		t.Errorf("barrel slot 1 = %+v, want 7 apples", got)
	}

	gotBrew := loaded.containerAt(ld, brewPos)
	if gotBrew == nil {
		t.Fatal("the brewing stand did not come back")
	}
	if got := gotBrew.slots[3]; got.ID != world.ItemIDs["blaze_powder"] || got.Count != 2 {
		t.Errorf("brewing stand slot 3 = %+v, want 2 blaze powder", got)
	}
	if gotBrew.brewTime != 111 || gotBrew.fuel != 17 {
		t.Errorf("brewing stand brew/fuel = %d/%d, want 111/17", gotBrew.brewTime, gotBrew.fuel)
	}
}

// TestBlockEntityItemNBT pins the stack encoding against the shape the
// real server writes, including the parts that differ between a stack
// in a slot and one nested inside a shulker box's container component.
func TestBlockEntityItemNBT(t *testing.T) {
	it := ItemStack{ID: world.ItemIDs["diamond"], Count: 1}
	got := itemNBT(it)
	if got["id"] != "minecraft:diamond" {
		t.Errorf("id = %v", got["id"])
	}
	if _, ok := got["components"]; ok {
		t.Error("a stack with no components carries no components key at all")
	}
	// The block entity is where the empty components compound goes: the
	// real server writes one on every block entity it saves, a sign's
	// as much as a chest's.
	be := blockEntityNBT(world.Pos{X: 1, Y: 2, Z: 3}, blockEntitySpecs["chest"], func(map[string]any) {})
	comps, ok := be["components"].(map[string]any)
	if !ok || len(comps) != 0 {
		t.Errorf("block entity components = %#v, want an empty compound", be["components"])
	}
	// A nested stack leaves count out when it is 1 and names its slot
	// as an int, where a slot list uses a byte.
	inner := itemNBTInner(it)
	if _, ok := inner["count"]; ok {
		t.Error("a nested stack of one omits count")
	}
	big := ItemStack{ID: world.ItemIDs["diamond"], Count: 5}
	if itemNBTInner(big)["count"] != int32(5) {
		t.Error("a nested stack of five carries its count")
	}
	slotted := itemNBTSlotted(it, 3)
	if slotted["Slot"] != int8(3) {
		t.Errorf("Slot = %#v, want an int8", slotted["Slot"])
	}

	back := itemFromNBT(got)
	if back.ID != it.ID || back.Count != 1 {
		t.Errorf("round trip = %+v, want %+v", back, it)
	}
}
