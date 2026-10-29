package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestChestBlockEventOrderedSetIdentity(t *testing.T) {
	s := New("unused")
	d := &dimension{name: dimOverworld}
	pos := world.Pos{X: 4, Y: 5, Z: 6}
	chest, _ := world.BlockState("chest")
	rotated, _ := world.WithProperty(chest, "facing", "east")
	trapped, _ := world.BlockState("trapped_chest")
	s.queueChestBlockEvent(d, pos, chest, 1)
	s.queueChestBlockEvent(d, pos, chest, 2)
	s.queueChestBlockEvent(d, pos, rotated, 1)
	s.queueChestBlockEvent(d, pos, chest, 0)
	if len(s.blockEvents) != 3 || s.blockEvents[0].b1 != 1 || s.blockEvents[1].b1 != 2 || s.blockEvents[2].b1 != 0 {
		t.Fatalf("ordered dedup events=%v", s.blockEvents)
	}
	s.queueChestBlockEvent(d, pos, trapped, 1)
	if len(s.blockEvents) != 4 {
		t.Fatal("different expected block type incorrectly deduplicated")
	}
	s.queueChestBlockEvent(&dimension{name: "minecraft:the_nether"}, pos, chest, 1)
	if len(s.blockEvents) != 5 {
		t.Fatal("different dimension incorrectly deduplicated")
	}
}
