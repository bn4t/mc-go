package server

import (
	"fmt"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"testing"
)

// LeavesBlock.getStateForPlacement: a hand-placed leaf lands
// persistent, with its distance already computed off the six
// neighbours — 1 beside a log, 7 in the open.
func TestLeafPlaceState(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	log, ok := world.BlockStateOf("oak_log", nil)
	if !ok {
		t.Fatal("oak_log state missing")
	}
	leaf, ok := world.BlockStateOf("oak_leaves", nil)
	if !ok {
		t.Fatal("oak_leaves state missing")
	}
	s.set(d, world.Pos{X: 0, Y: -60, Z: 0}, log)

	near := leafPlaceState(d, world.Pos{X: 1, Y: -60, Z: 0}, leaf)
	if p, _ := world.StateProperty(near, "persistent"); p != "true" {
		t.Fatalf("near-leaf persistent = %q, want true", p)
	}
	if p, _ := world.StateProperty(near, "distance"); p != "1" {
		t.Fatalf("near-leaf distance = %q, want 1", p)
	}

	far := leafPlaceState(d, world.Pos{X: 10, Y: -60, Z: 0}, leaf)
	if p, _ := world.StateProperty(far, "persistent"); p != "true" {
		t.Fatalf("far-leaf persistent = %q, want true", p)
	}
	if p, _ := world.StateProperty(far, "distance"); p != "7" {
		t.Fatalf("far-leaf distance = %q, want 7", p)
	}
}

// A decaying leaf rolls its blocks/<leaf> loot table before folding
// to air: 225 decays at 5% sapling / 2% stick must drop at least one
// item entity, and a sapling among them.
func TestLeafDecayDropsLoot(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	leaf, ok := world.BlockStateOf("oak_leaves", nil)
	if !ok {
		t.Fatal("oak_leaves state missing")
	}
	for x := 0; x < 15; x++ {
		for z := 0; z < 15; z++ {
			pos := world.Pos{X: x, Y: -60, Z: z}
			s.set(d, pos, leaf)
			leafDecayRule(s, d, pos, leaf)
		}
	}
	s.entities.mu.RLock()
	var items, saplings int
	for _, e := range s.entities.byID {
		if e.Item.ID == 0 {
			continue
		}
		items++
		if e.Item.ID == world.ItemIDs["oak_sapling"] {
			saplings++
		}
	}
	s.entities.mu.RUnlock()
	if items == 0 {
		t.Fatal("225 decays dropped no items at all")
	}
	if saplings == 0 {
		t.Fatal("225 decays dropped no oak_sapling")
	}
}

// A persistent leaf is exempt from decay even at distance 7; a leaf
// whose distance settled below 7 is exempt too — only the detached
// ones crumble.
func TestLeafDecayGates(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	persisted, ok := world.BlockStateOf("oak_leaves", map[string]string{"persistent": "true"})
	if !ok {
		t.Fatal("persistent oak_leaves state missing")
	}
	attached, ok := world.BlockStateOf("oak_leaves", map[string]string{"distance": "3"})
	if !ok {
		t.Fatal("distance-3 oak_leaves state missing")
	}
	for i, blk := range []world.Block{persisted, attached} {
		pos := world.Pos{X: i, Y: -60, Z: 0}
		s.set(d, pos, blk)
		leafDecayRule(s, d, pos, blk)
		if got := d.w.GetBlock(pos); got == world.Air {
			t.Fatalf("leaf %d decayed, want exempt", i)
		}
	}
}

// Native 26.2's leaf property has domain 1..7; scaffolding has a
// distinct 0..7 property even though both are named "distance".
func TestLeafDistanceNativePropertyIdentity(t *testing.T) {
	count := 0
	for _, name := range world.BlockNames() {
		b, ok := world.BlockState(name)
		if !ok || !worldgen.IsLeaves(b) {
			continue
		}
		count++
		for distance := 1; distance <= 7; distance++ {
			state, ok := world.BlockStateOf(name, map[string]string{"distance": fmt.Sprint(distance)})
			if !ok {
				t.Fatalf("%s distance%d absent", name, distance)
			}
			if got := leafDistanceOf(state); got != distance {
				t.Fatalf("%s distance%d got%d", name, distance, got)
			}
		}
	}
	if count != 11 {
		t.Fatalf("native leaf property family count %d", count)
	}
	for distance := 0; distance <= 7; distance++ {
		state, ok := world.BlockStateOf("scaffolding", map[string]string{"distance": fmt.Sprint(distance)})
		if !ok {
			t.Fatal("missing scaffold state")
		}
		if got := leafDistanceOf(state); got != 7 {
			t.Fatalf("scaffold distance%d got%d want7", distance, got)
		}
	}
	log, _ := world.BlockState("oak_log")
	if got := leafDistanceOf(log); got != 0 {
		t.Fatalf("log distance %d", got)
	}
}
func TestLeafTickScaffoldingNeighborUsesFallback(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: -60, Z: 1}
	neighbor := world.Pos{X: 0, Y: -60, Z: 1}
	leaf, _ := world.BlockStateOf("oak_leaves", map[string]string{"distance": "1", "persistent": "true"})
	scaffold, _ := world.BlockStateOf("scaffolding", map[string]string{"distance": "0"})
	d.w.SetBlock(pos, leaf)
	d.w.SetBlock(neighbor, scaffold)
	s.leafDistanceTick(d, pos)
	if got, _ := world.StateProperty(d.w.GetBlock(pos), "distance"); got != "7" {
		t.Fatalf("leaf beside scaffolding distance=%s", got)
	}
}
