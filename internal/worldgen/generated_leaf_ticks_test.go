package worldgen

import (
	"mc-go/internal/world"
	"testing"
)

func TestGeneratedLeafShapeSchedulesDeduplicatedTicks(t *testing.T) {
	c := &ProtoChunk{CX: 0, CZ: 0, MinY: 0, Height: 16, Blocks: make([]world.Block, 4096)}
	rg := &Region{center: c, chunks: map[[2]int32]*ProtoChunk{{0, 0}: c}}
	p := BlockPos{1, 5, 1}
	q := BlockPos{1, 5, 2}
	leaf, _ := world.BlockStateOf("oak_leaves", map[string]string{"distance": "2"})
	log, _ := world.BlockState("oak_log")
	if got := treeUpdateShape(rg, leaf, p, dirSouth, q, log); got != leaf {
		t.Fatal("leaf shape changed state")
	}
	treeUpdateShape(rg, leaf, p, dirSouth, q, log)
	if len(c.LeafTicks) != 1 || c.LeafTicks[0] != (GeneratedLeafTick{Pos: p, Block: "oak_leaves"}) {
		t.Fatalf("tick requests: %#v", c.LeafTicks)
	}
	d1, _ := world.BlockStateOf("oak_leaves", map[string]string{"distance": "1"})
	p2 := BlockPos{2, 5, 1}
	treeUpdateShape(rg, d1, p2, dirSouth, q, log)
	if len(c.LeafTicks) != 1 {
		t.Fatal("distance-one leaf next to log scheduled")
	}
	scaffold, ok := world.BlockStateOf("scaffolding", map[string]string{"distance": "0"})
	if !ok {
		t.Fatal("missing scaffolding state")
	}
	treeUpdateShape(rg, d1, BlockPos{3, 5, 1}, dirSouth, q, scaffold)
	if len(c.LeafTicks) != 2 || c.LeafTicks[1] != (GeneratedLeafTick{Pos: BlockPos{3, 5, 1}, Block: "oak_leaves"}) {
		t.Fatalf("scaffolding must schedule the leaf: %#v", c.LeafTicks)
	}

	birch, _ := world.BlockStateOf("birch_leaves", map[string]string{"distance": "2"})
	treeUpdateShape(rg, birch, p, dirSouth, q, log)
	if len(c.LeafTicks) != 3 || c.LeafTicks[2].Block != "birch_leaves" {
		t.Fatalf("typed dedup: %#v", c.LeafTicks)
	}
}

// The native 26.2 seeded trace captured requests for these two leaves during
// TreeFeature edge updates. Replaying its measured startup feature order
// exercises request creation independently of the server provider order.
func TestGeneratedLeafTicksNativeSeed0Trace(t *testing.T) {
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", 0)
	if err != nil {
		t.Fatal(err)
	}
	order := [][2]int32{{-3, 1}, {-3, -1}, {-3, 0}, {-2, -1}, {-2, 0}, {-2, 1}, {-1, -1}, {-1, 0}, {-1, 1}}
	chunks := g.GenerateArea(order)
	c := chunks[[2]int32{-1, 0}]
	for _, y := range []int32{67, 68} {
		p := BlockPos{-7, y, 3}
		found := false
		for _, row := range c.LeafTicks {
			if row.Pos == p && row.Block == "oak_leaves" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("native-captured request absent at %v", p)
		}
		b := c.Get(9, y, 3)
		if d, _ := world.StateProperty(b, "distance"); d != "2" {
			t.Errorf("pre-tick distance at %v = %s", p, d)
		}
	}
}
