package server

import (
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"testing"
)

func TestMineshaftCartAdmissionAndNativeSave(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	s.SetFrozen(true)
	s.gameRules["minecraft:spawn_mobs"] = "false"
	d := s.dims[dimOverworld]
	const seed = int64(-653164966778954613)
	d.terrain = &terrainSource{genEntities: []worldgen.GenEntity{
		{Structure: "mineshaft", Type: "chest_minecart", X: -85.5, Y: 41.5, Z: -20.5, LootTable: "minecraft:chests/abandoned_mineshaft", LootSeed: seed},
		{Type: "end_crystal", X: 0, Y: 70, Z: 0},
	}}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	all := s.entities.All()
	if len(all) != 1 {
		t.Fatal("generated vehicle count", len(all))
	}
	entity, _ := s.entities.GetPtr(all[0].ID)
	if name, _ := EntityTypeName(entity.Type); name != "chest_minecart" || entity.Living || entity.X != -85.5 || entity.Y != 41.5 || entity.Z != -20.5 || entity.Yaw != 0 || entity.Pitch != 0 || entity.MotionX != 0 || entity.MotionY != 0 || entity.MotionZ != 0 {
		t.Fatal("native constructor state", entity)
	}
	saved := entitySaveMap(entity, s.clock.count, s.entities).m
	if saved["HasTicked"] != int8(1) || saved["LootTable"] != "minecraft:chests/abandoned_mineshaft" || saved["LootTableSeed"] != seed {
		t.Fatal("latent native cart", saved)
	}
	if _, unpacked := saved["Items"]; unpacked {
		t.Fatal("generation eagerly unpacked loot")
	}
	inventory := s.vehicleCargoInventory(entity)
	if inventory.lootTable != "minecraft:chests/abandoned_mineshaft" || inventory.lootSeed != seed {
		t.Fatal("existing cargo did not retain generated loot")
	}
	s.chunkGenSpawnStep()
	if len(s.entities.All()) != 1 || len(d.terrain.genEntities) != 1 || d.terrain.genEntities[0].Type != "end_crystal" {
		t.Fatal("repeat admission or unrelated queue loss")
	}
	records, err := world.LoadEntityRegions(world.DimensionDir(s.WorldDir, dimOverworld))
	if err != nil || len(records) != 1 {
		t.Fatal("native snapshot", len(records), err)
	}
	reloaded := simulationTestServer(t)
	reloaded.WorldDir = s.WorldDir
	reloaded.restoreEntities(reloaded.dims[dimOverworld])
	loaded := reloaded.entities.All()
	if len(loaded) != 1 || loaded[0].UUID != entity.UUID {
		t.Fatal("native cart reload", loaded)
	}
	roundtrip := entitySaveMap(&loaded[0], reloaded.clock.count, reloaded.entities).m
	if roundtrip["LootTable"] != saved["LootTable"] || roundtrip["LootTableSeed"] != seed || roundtrip["HasTicked"] != int8(1) {
		t.Fatal("cart fields changed on reload", roundtrip)
	}
	reloaded.minecartTickStep()
	pointer, _ := reloaded.entities.GetPtr(loaded[0].ID)
	if got := entitySaveMap(pointer, reloaded.clock.count, reloaded.entities).m["HasTicked"]; got != int8(0) {
		t.Fatal("first cart pass did not clear constructor flag", got)
	}
}
