package server

import (
	"math"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"testing"
)

func TestHutResidentAdmissionIndependentOfNaturalSpawning(t *testing.T) {
	s := simulationTestServer(t)
	s.SetFrozen(true)
	s.gameRules["minecraft:spawn_mobs"] = "false"
	d := s.dims[dimOverworld]
	d.terrain = &terrainSource{genEntities: []worldgen.GenEntity{
		{Type: "end_crystal", X: 1, Y: 70, Z: 1},
		{Structure: "swamp_hut", Type: "witch", X: -2381.5, Y: 66, Z: -1386.5},
		{Structure: "swamp_hut", Type: "cat", X: -2381.5, Y: 66, Z: -1386.5},
		{Structure: "unsupported", Type: "cat", X: 2, Y: 70, Z: 2},
	}}
	s.chunkGenSpawnStep()
	counts := map[string]int{}
	for _, entity := range s.entities.All() {
		name, _ := EntityTypeName(entity.Type)
		counts[name]++
		if !entity.Living || !entity.PersistenceRequired || entity.NoAI || entity.X != -2381.5 || entity.Y != 66 || entity.Z != -1386.5 || entity.Yaw != 0 {
			t.Fatal("resident initialization", entity)
		}
		mod, present := entity.AttrMods["follow_range"]["minecraft:random_spawn_bonus"]
		if !present || mod.op != 1 || math.IsNaN(mod.value) || math.IsInf(mod.value, 0) || math.Abs(mod.value) > .11485 {
			t.Fatal("resident finalization", name, mod, present)
		}
		if name == "witch" && (!entity.CanJoinRaid || entity.Health != 26) {
			t.Fatal("witch resident", entity.CanJoinRaid, entity.Health)
		}
		if name == "cat" && (entity.VariantName != "all_black" || entity.Health != 10 || (entity.SoundVariant != "classic" && entity.SoundVariant != "royal")) {
			t.Fatal("cat resident", entity.VariantName, entity.SoundVariant, entity.Health)
		}
	}
	if counts["witch"] != 1 || counts["cat"] != 1 || len(counts) != 2 {
		t.Fatal("resident population", counts)
	}
	if len(d.terrain.genEntities) != 2 || d.terrain.genEntities[0].Type != "end_crystal" || d.terrain.genEntities[1].Structure != "unsupported" {
		t.Fatal("unrelated generated entities consumed", d.terrain.genEntities)
	}
	s.chunkGenSpawnStep()
	if len(s.entities.All()) != 2 {
		t.Fatal("generated residents duplicated")
	}
}

// The resident pair is written through the ordinary entity codec, with the
// original identities and finalization preserved. Restored structure flags are
// pinned separately by the worldgen descriptor test.
func TestHutResidentNativeEntityRoundTrip(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	for _, name := range []string{"witch", "cat"} {
		s.spawnHutResident(d, worldgen.GenEntity{Structure: "swamp_hut", Type: name, X: -2381.5, Y: 66, Z: -1386.5})
	}
	saved := s.captureEntityRegionsOwned(d)
	s2 := simulationTestServer(t)
	s2.WorldDir = t.TempDir()
	if err := world.SaveEntityRegions(world.DimensionDir(s2.WorldDir, dimOverworld), saved); err != nil {
		t.Fatal(err)
	}
	s2.restoreEntities(s2.dims[dimOverworld])
	if len(s2.entities.All()) != 2 {
		t.Fatal("restored resident count", len(s2.entities.All()))
	}
	for _, original := range s.entities.All() {
		var restored *Entity
		for _, candidate := range s2.entities.All() {
			if candidate.UUID == original.UUID {
				restored = &candidate
				break
			}
		}
		if restored == nil {
			t.Fatal("resident identity lost", original.UUID)
		}
		if restored.X != original.X || restored.Y != original.Y || restored.Z != original.Z || restored.Health != original.Health || !restored.PersistenceRequired || restored.VariantName != original.VariantName || orStr(restored.SoundVariant, "classic") != orStr(original.SoundVariant, "classic") || restored.CanJoinRaid != original.CanJoinRaid {
			t.Fatalf("resident reload type%d: pos (%g,%g,%g)→(%g,%g,%g), health %g→%g, persistence %v, variant %q→%q, sound %q→%q, raid %v→%v", original.Type, original.X, original.Y, original.Z, restored.X, restored.Y, restored.Z, original.Health, restored.Health, restored.PersistenceRequired, original.VariantName, restored.VariantName, original.SoundVariant, restored.SoundVariant, original.CanJoinRaid, restored.CanJoinRaid)
		}
		if restored.AttrMods["follow_range"]["minecraft:random_spawn_bonus"] != original.AttrMods["follow_range"]["minecraft:random_spawn_bonus"] {
			t.Fatal("resident finalization rerolled")
		}
	}
	s2.chunkGenSpawnStep()
	if len(s2.entities.All()) != 2 {
		t.Fatal("restored residents duplicated")
	}
}

// Native saves of a loaded hut include both residents before shutdown. A
// provider handoff can precede the next player pass, so the save boundary must
// admit that already-generated pair rather than persist only its piece flags.
func TestHutResidentImmediateNativeSave(t *testing.T) {
	s := simulationTestServer(t)
	s.WorldDir = t.TempDir()
	s.SetFrozen(true)
	s.gameRules["minecraft:spawn_mobs"] = "false"
	d := s.dims[dimOverworld]
	d.terrain = &terrainSource{genEntities: []worldgen.GenEntity{
		{Structure: "swamp_hut", Type: "witch", X: -2381.5, Y: 66, Z: -1386.5},
		{Structure: "swamp_hut", Type: "cat", X: -2381.5, Y: 66, Z: -1386.5},
	}}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	saved, err := world.LoadEntityRegions(world.DimensionDir(s.WorldDir, dimOverworld))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("immediate native snapshot residents=%d, want2", len(saved))
	}
	s.chunkGenSpawnStep()
	if len(s.entities.All()) != 2 || len(d.terrain.genEntities) != 0 {
		t.Fatal("save/player-pass admission duplicated residents")
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	s2 := simulationTestServer(t)
	s2.WorldDir = s.WorldDir
	s2.restoreEntities(s2.dims[dimOverworld])
	s2.chunkGenSpawnStep()
	if len(s2.entities.All()) != 2 {
		t.Fatal("post-save native reload resident count", len(s2.entities.All()))
	}
}
