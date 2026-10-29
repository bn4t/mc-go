package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestDestroyedBundleSpillDefaults(t *testing.T) {
	s := New("unused")
	nested := ItemStack{ID: world.ItemIDs["blue_bundle"], Count: 1}
	bundleSetItems(&nested, []ItemStack{{ID: world.ItemIDs["netherite_scrap"], Count: 4}})
	stack := ItemStack{ID: world.ItemIDs["bundle"], Count: 1}
	bundleSetItems(&stack, []ItemStack{{ID: world.ItemIDs["ancient_debris"], Count: 2}, nested})
	parent := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: dimOverworld, X: 7.5, Y: 71.5, Z: 8.5, Item: stack, PickupDelay: 300, Thrower: 99, NoGravity: true, MotionX: 8, MotionY: 9, MotionZ: 10, Yaw: 900})
	s.lavaInside(parent, "item")
	if len(s.entities.All()) != 1 {
		t.Fatal("spill before lethal hit")
	}
	s.lavaInside(parent, "item")
	if parent.Item.Bundle != "" {
		t.Fatal("source retained contents")
	}
	children := s.entities.All()
	if len(children) != 2 {
		t.Fatalf("got%d children", len(children))
	}
	for _, e := range children {
		if e.X != 7.5 || e.Y != 71.5 || e.Z != 8.5 || e.NoGravity || e.PickupDelay != 0 || e.Thrower != 0 || e.Owner != ([16]byte{}) || e.ItemAge != 0 || e.ItemHP != 5 {
			t.Fatalf("inherited source state: %+v", e)
		}
		if e.Yaw < 0 || e.Yaw >= 360 || e.VY != 1600 {
			t.Fatal("wrong fresh item orientation/spawn velocity")
		}
		if e.MotionX < -.1 || e.MotionX > .1 || e.MotionY != .2 || e.MotionZ < -.1 || e.MotionZ > .1 {
			t.Fatal("wrong fresh-item toss")
		}
		if isBundle(e.Item) {
			p, _ := s.entities.GetPtr(e.ID)
			s.lavaInside(p, "item")
			s.lavaInside(p, "item")
		}
	}
	counts := map[int32]int32{}
	for _, e := range s.entities.All() {
		counts[e.Item.ID] += e.Item.Count
	}
	if counts[world.ItemIDs["ancient_debris"]] != 2 || counts[world.ItemIDs["netherite_scrap"]] != 4 || len(counts) != 2 {
		t.Fatalf("nested spill=%v", counts)
	}
}
