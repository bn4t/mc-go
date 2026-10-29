package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestVehicleCargoKilledSpillRuntimeAndScatter(t *testing.T) {
	s := nativeSaveTestServer(t)
	e := cargoUnitEntity(s, "chest_minecart")
	e.X, e.Y, e.Z = 8.6, -60, 8.7
	c := s.vehicleCargoInventory(e)
	diamond, _ := world.ItemID("diamond")
	c.slots[0] = ItemStack{ID: diamond, Count: 64, Name: "Cargo gem"}
	c.slots[4] = ItemStack{ID: diamond, Count: 3}
	s.spillVehicleCargo(e)
	total, bursts := int32(0), 0
	var shared [3]float64
	for _, drop := range s.entities.All() {
		if drop.Type != mustEntityType("item") {
			continue
		}
		total += drop.Item.Count
		bursts++
		if drop.Item.Count < 1 || drop.Item.Count > 30 || drop.PickupDelay != 0 {
			t.Fatalf("bad burst %#v", drop)
		}
		if drop.X < 8.125 || drop.X >= 8.875 || drop.Y < -60 || drop.Y >= -59.25 || drop.Z < 8.125 || drop.Z >= 8.875 {
			t.Fatal("drop outside measured scatter", drop.X, drop.Y, drop.Z)
		}
		if drop.MotionX <= -.114851 || drop.MotionX >= .114851 || drop.MotionZ <= -.114851 || drop.MotionZ >= .114851 || drop.MotionY <= .085149 || drop.MotionY >= .314851 {
			t.Fatal("drop outside measured motion", drop.MotionX, drop.MotionY, drop.MotionZ)
		}
		if drop.Item.Name == "Cargo gem" {
			pos := [3]float64{drop.X, drop.Y, drop.Z}
			if shared == [3]float64{} {
				shared = pos
			}
			if shared != pos {
				t.Fatal("one slot scattered to multiple positions")
			}
		}
	}
	if total != 67 || bursts < 4 {
		t.Fatalf("lost or unsplit runtime cargo total%d bursts%d", total, bursts)
	}
	for _, stack := range c.slots {
		if !stack.Empty() {
			t.Fatal("cargo retained after spill")
		}
	}
	before := len(s.entities.All())
	s.spillVehicleCargo(e)
	if len(s.entities.All()) != before {
		t.Fatal("cargo spilled twice")
	}
}

func TestVehicleCargoKilledEmptyLootAndNonContainer(t *testing.T) {
	s := nativeSaveTestServer(t)
	e := cargoUnitEntity(s, "hopper_minecart")
	save := entitySaveMap(e, 0, s.entities).m
	save["LootTable"] = "minecraft:empty"
	save["LootTableSeed"] = int64(123)
	s.applyEntitySave(e, save)
	before := len(s.entities.All())
	s.spillVehicleCargo(e)
	if len(s.entities.All()) != before || e.vehicleCargo.lootTable != "" {
		t.Fatal("empty loot not consumed without drops")
	}
	plain := s.entities.Spawn(&Entity{Type: mustEntityType("minecart"), Dim: dimOverworld})
	s.spillVehicleCargo(plain)
	if plain.vehicleCargo != nil {
		t.Fatal("ordinary cart acquired cargo")
	}
}
