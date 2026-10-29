package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestItemDamageCounterAndProtection(t *testing.T) {
	s := New("unused")
	spawn := func(name string) *Entity {
		return s.entities.Spawn(&Entity{Type: mustEntityType("item"), Item: ItemStack{ID: world.ItemIDs[name], Count: 1}})
	}
	e := spawn("stone")
	if e.ItemHP != 5 {
		t.Fatal(e.ItemHP)
	}
	for _, want := range []int32{4, 3} {
		if !s.damageItemEntity(e, .25, "generic", 0) || e.ItemHP != want {
			t.Fatalf("health %d want %d", e.ItemHP, want)
		}
	}
	e.Invulnerable = true
	if s.damageItemEntity(e, 1, "generic", 0) || e.ItemHP != 3 {
		t.Fatal("invulnerable accepted")
	}
	e.Invulnerable = false
	if !s.damageItemEntity(e, 3, "cactus", 0) {
		t.Fatal("lethal rejected")
	}
	if _, ok := s.entities.GetPtr(e.ID); ok {
		t.Fatal("lethal survived")
	}
	if s.damageItemEntity(e, 0, "generic", 0) {
		t.Fatal("removed accepted")
	}
	victim := spawn("stone")
	mob := s.entities.Spawn(&Entity{Type: mustEntityType("zombie"), Living: true})
	s.gameRules["minecraft:mob_griefing"] = "false"
	if s.damageItemEntity(victim, 1, "mob_attack", mob.ID) {
		t.Fatal("mob griefing accepted")
	}
	s.gameRules["minecraft:mob_griefing"] = "true"
	if !s.damageItemEntity(victim, 1, "mob_attack", mob.ID) || victim.ItemHP != 4 {
		t.Fatal("mob hit rejected")
	}
	star := spawn("nether_star")
	if s.damageItemEntity(star, 100, "explosion", 0) || star.ItemHP != 5 {
		t.Fatal("star explosion")
	}
	if !s.damageItemEntity(star, 1, "in_fire", 0) || star.ItemHP != 4 {
		t.Fatal("star fire")
	}
	metal := spawn("netherite_ingot")
	s.lavaInside(metal, "item")
	if metal.ItemHP != 5 || metal.FireTicks != 0 {
		t.Fatal("fireproof ignited")
	}
	e = spawn("stone")
	e.ItemHP = 0
	if !s.damageItemEntity(e, 0, "generic", 0) {
		t.Fatal("zero health damage rejected")
	}
	if _, ok := s.entities.GetPtr(e.ID); ok {
		t.Fatal("zero health resurrected")
	}
}

func TestItemHealthSaveDefaultsAndExplicitZero(t *testing.T) {
	s := New("unused")
	e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Item: ItemStack{ID: world.ItemIDs["stone"], Count: 1}})
	e.ItemHP = 0
	e.ItemAge = 72
	save := entitySaveMap(e, 0, s.entities).m
	if v, ok := save["Health"].(int16); !ok || v != 0 {
		t.Fatalf("health %#v", save["Health"])
	}
	s.applyEntitySave(e, save)
	if e.ItemHP != 0 || e.ItemAge != 72 {
		t.Fatalf("zero roundtrip %d %d", e.ItemHP, e.ItemAge)
	}
	delete(save, "Health")
	s.applyEntitySave(e, save)
	if e.ItemHP != 5 {
		t.Fatal("absent Health default", e.ItemHP)
	}
}

func TestItemBlastResistanceAndNewbornSnapshot(t *testing.T) {
	s := New("unused")
	src := s.entities.Spawn(&Entity{Type: mustEntityType("tnt"), Dim: dimOverworld, X: 0, Y: 72, Z: 0})
	spawn := func(name string) *Entity {
		return s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: dimOverworld, X: 1, Y: 72, Z: 0, Item: ItemStack{ID: world.ItemIDs[name], Count: 1}})
	}
	star := spawn("nether_star")
	star.MotionX = .3
	bundle := spawn("bundle")
	bundleSetItems(&bundle.Item, []ItemStack{{ID: world.ItemIDs["stone"], Count: 3}})
	s.blastItemEntities(src, []*Entity{star, bundle}, 0, 72, 0, 1, true)
	if star.ItemHP != 5 || star.MotionX <= .3 {
		t.Fatalf("resistant item health/push: %d %f", star.ItemHP, star.MotionX)
	}
	if _, ok := s.entities.GetPtr(bundle.ID); ok {
		t.Fatal("bundle survived")
	}
	found := false
	for _, e := range s.entities.All() {
		if e.Item.ID == world.ItemIDs["stone"] && e.Item.Count == 3 {
			found = true
			if e.ItemHP != 5 || e.MotionY != .2 {
				t.Fatal("child rehit")
			}
		}
	}
	if !found {
		t.Fatal("no newborn spill")
	}
}

func TestItemHealthNativeRegionRoundTrip(t *testing.T) {
	original := New("unused")
	original.WorldDir = t.TempDir()
	d := original.dims[dimOverworld]
	for _, health := range []int32{0, 3, 5} {
		original.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: d.name, X: float64(health), Y: 72, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 1}, ItemHP: health})
	}
	// Explicit zero follows construction, just as Entity.load does.
	for _, e := range original.entities.All() {
		if e.X == 0 {
			p, _ := original.entities.GetPtr(e.ID)
			p.ItemHP = 0
		}
	}
	if err := original.saveEntityRegions(d); err != nil {
		t.Fatal(err)
	}
	restored := New("unused")
	restored.WorldDir = original.WorldDir
	restored.restoreEntities(restored.dims[d.name])
	got := restored.entities.All()
	if len(got) != 3 {
		t.Fatalf("restored%d", len(got))
	}
	for _, e := range got {
		if e.ItemHP != int32(e.X) {
			t.Fatalf("health %d at %f", e.ItemHP, e.X)
		}
	}
}

func TestItemCactusContactAboveCollisionShape(t *testing.T) {
	s := New("unused")
	w := s.dimWorld(dimOverworld)
	w.SetBlock(world.Pos{X: 4, Y: 72, Z: 0}, mkBlockState(t, "cactus", nil))
	if entityInsideSolid(w, 4.5, 72.95, .5, .125, .25) {
		t.Fatal("cactus collision ends at15/16; upper contact band is not solid")
	}
	e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: dimOverworld, X: 4.5, Y: 72.95, Z: .5, NoGravity: true, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 1}})
	for _, want := range []int32{4, 3, 2, 1} {
		s.entityInsideStep()
		if e.ItemHP != want {
			t.Fatalf("health%d want%d", e.ItemHP, want)
		}
	}
}
