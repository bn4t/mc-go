package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestVehicleCargoItemSlotsAndNative(t *testing.T) {
	s := nativeSaveTestServer(t)
	for _, name := range []string{"chest_minecart", "hopper_minecart", "oak_chest_boat"} {
		e := cargoUnitEntity(s, name)
		d := &dataEntity{mob: e}
		size := vehicleContainerSize(name)
		if got, ok := s.itemRead(d, 0); !ok || got.Count != 3 {
			t.Fatalf("%s read %+v/%v", name, got, ok)
		}
		emerald, _ := world.ItemID("emerald")
		stack := ItemStack{ID: emerald, Count: 4}
		if !s.itemAccepts(d, int32(size-1), stack) || s.itemAccepts(d, int32(size), stack) || s.itemAccepts(d, -1, stack) {
			t.Fatal("capacity", name)
		}
		s.itemWrite(d, int32(size-1), stack)
		if got, ok := s.itemRead(d, int32(size-1)); !ok || got != stack {
			t.Fatal("write", name)
		}
		save := entitySaveMap(e, 0, s.entities).m
		restored := make([]ItemStack, size)
		itemsInto(save["Items"], restored)
		if restored[size-1] != stack {
			t.Fatal("native projection", name)
		}
		if id, ok := parseItemSlot("hotbar.0"); !ok || id != 0 {
			t.Fatal("hotbar alias")
		}
		if id, ok := parseItemSlot("inventory.0"); !ok || id != 9 {
			t.Fatal("inventory alias")
		}
	}
	e := s.entities.Spawn(&Entity{Type: mustEntityType("minecart")})
	if _, ok := s.itemRead(&dataEntity{mob: e}, 0); ok {
		t.Fatal("plain cart exposed cargo")
	}
}

func TestVehicleCargoItemLazyLootAndViewerDelta(t *testing.T) {
	s := nativeSaveTestServer(t)
	e := cargoUnitEntity(s, "chest_minecart")
	viewer, _ := enforceSession(t, s, "CargoItemViewer")
	viewer.setPos(1.5, 0, 0, 0, 0, true)
	if !s.openVehicleCargo(viewer, e) {
		t.Fatal("menu")
	}
	emerald, _ := world.ItemID("emerald")
	s.itemWrite(&dataEntity{mob: e}, 0, ItemStack{ID: emerald, Count: 4})
	if viewer.menuStack(0).ID != emerald || viewer.window.commandUpdates == nil {
		t.Fatal("viewer not queued")
	}
	save := entitySaveMap(e, 0, s.entities).m
	save["LootTable"] = "minecraft:empty"
	save["LootTableSeed"] = int64(123)
	s.applyEntitySave(e, save)
	got, ok := s.itemRead(&dataEntity{mob: e}, 0)
	if !ok || !got.Empty() || e.vehicleCargo.lootTable != "" {
		t.Fatal("lazy read did not unpack empty loot")
	}
	save = entitySaveMap(e, 0, s.entities).m
	if _, exists := save["LootTable"]; exists {
		t.Fatal("loot remained armed")
	}
}

func TestVehicleCargoItemTargetResults(t *testing.T) {
	s := nativeSaveTestServer(t)
	first := cargoUnitEntity(s, "chest_minecart")
	second := cargoUnitEntity(s, "hopper_minecart")
	emerald, _ := world.ItemID("emerald")
	stack := ItemStack{ID: emerald, Count: 4}
	rep := s.writeItemTargets([]*dataEntity{{mob: first}, {mob: second}}, 0, stack)
	if !rep.hasResult || rep.result != 2 {
		t.Fatalf("multi result %+v", rep)
	}
	rep = s.writeItemTargets([]*dataEntity{{mob: first}, {mob: second}}, 27, stack)
	if !rep.hasResult || rep.result != 0 || rep.outcome != commandFailed {
		t.Fatalf("failure result %+v", rep)
	}
}
