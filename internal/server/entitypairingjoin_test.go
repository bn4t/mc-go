package server

import (
	"mc-go/internal/protocol"
	"testing"
)

// Initial chunk delivery may pair an entity before the join burst runs.
// A later newly eligible chunk still needs its first pairing.
func TestJoinBurstPreservesExistingAndNewPairings(t *testing.T) {
	s, observer := trackingFixture(3, 1)
	first, _ := s.entities.GetPtr(2)
	later, _ := s.entities.GetPtr(3)
	first.X, first.Z = 1, 1
	observer.trackedEnts = map[int32]struct{}{}
	observer.sent = map[[2]int]bool{{0, 0}: true}
	spawns := func() []int32 {
		var ids []int32
		for _, p := range entityPackets(t, observer) {
			if p.id == observer.ids().PlaySpawnEntity {
				id, err := protocol.NewPacketReader(p.data).VarInt()
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
		}
		return ids
	}
	s.evalPair(observer, first)
	if ids := spawns(); len(ids) != 1 || ids[0] != first.ID {
		t.Fatalf("initial chunk pairing%v", ids)
	}
	if err := s.sendEntitySpawnsTo(observer); err != nil {
		t.Fatal(err)
	}
	if ids := spawns(); len(ids) != 0 {
		t.Fatalf("join duplicated pairings%v", ids)
	}
	if observer.tracksEntity(later.ID) {
		t.Fatal("entity in unseen chunk marked paired")
	}
	observer.sent[[2]int{2, 0}] = true
	if err := s.sendEntitySpawnsTo(observer); err != nil {
		t.Fatal(err)
	}
	if ids := spawns(); len(ids) != 1 || ids[0] != later.ID {
		t.Fatalf("newly eligible pairing%v", ids)
	}
	if err := s.sendEntitySpawnsTo(observer); err != nil {
		t.Fatal(err)
	}
	if ids := spawns(); len(ids) != 0 {
		t.Fatalf("repeat join duplicated%v", ids)
	}
}
