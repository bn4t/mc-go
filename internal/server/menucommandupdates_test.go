package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestMenuCommandQueueViewersAndOriginalValue(t *testing.T) {
	s := &Server{}
	d, other := &dimension{}, &dimension{}
	pos := world.Pos{X: 7, Y: 10, Z: 2}
	windows := []*openWindow{{dim: d, pos: pos, chest: &chestData{}}, {dim: d, pos: pos, chest: &chestData{}}, {dim: other, pos: pos, chest: &chestData{}}, {dim: d, pos: world.Pos{}, chest: &chestData{}}, nil}
	for i, w := range windows {
		s.sessions.Store(i, &Session{window: w})
	}
	first := ItemStack{ID: 1, Count: 2}
	s.queueMenuCommandSlot(d, pos, 0, first, 0)
	s.queueMenuCommandSlot(d, pos, 0, ItemStack{ID: 1, Count: 3}, 0)
	for i, w := range windows {
		if w == nil {
			continue
		}
		if i < 2 {
			if w.commandUpdates == nil || w.commandUpdates.slots[0] != first {
				t.Errorf("viewer%d lost prewrite value", i)
			}
		} else if w.commandUpdates != nil {
			t.Errorf("unrelated viewer%d queued", i)
		}
	}
}

func TestMenuRemoteStackEqualityIgnoresBundleSelection(t *testing.T) {
	a := ItemStack{ID: 1, Count: 1, Bundle: `[{"id":1,"count":2}]`, BundleSelected: 0}
	b := a
	b.BundleSelected = 1
	if !menuRemoteStackEqual(a, b) {
		t.Fatal("selection alone changed remote contents")
	}
	b.Count = 2
	if menuRemoteStackEqual(a, b) {
		t.Fatal("count mutation suppressed")
	}
	b.Count = 1
	b.Bundle = `[]`
	if menuRemoteStackEqual(a, b) {
		t.Fatal("bundle content mutation suppressed")
	}
}
