package worldgen

import (
	"strings"
	"testing"

	"mc-go/internal/world"
)

// TestBlockPropsOrder cross-checks that the oracle's state order matches
// mc-go's protocol 776 mapping and spot-checks well-known facts.
func TestBlockPropsOrder(t *testing.T) {
	m, _ := world.BlockMapping(776)
	for id, name := range vanillaStateNames {
		b, ok := m.FromWire(int32(id))
		if !ok {
			t.Fatalf("state %d missing", id)
		}
		n, _ := world.StateOwner(b)
		if !strings.Contains(name, "minecraft:"+n+"}") {
			t.Errorf("state %d: oracle %s, mc-go %s", id, name, n)
		}
	}
	if !isAir(world.Air) || isAir(blockNamed("stone")) {
		t.Error("air bits")
	}
	if !blocksMotionState(blockNamed("stone")) || blocksMotionState(blockNamed("short_grass")) {
		t.Error("blocksMotion bits")
	}
	if !isLeaves(blockNamed("oak_leaves")) || !hasFluid(blockNamed("water")) || !isReplaceable(blockNamed("short_grass")) {
		t.Error("leaves/fluid/replaceable bits")
	}
}
