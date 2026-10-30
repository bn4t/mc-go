package server

import "testing"

func TestPlayerHeadTrackingPreservesPresenceOnly(t *testing.T) {
	s := New("unused")
	e := s.entities.Spawn(&Entity{presenceOnly: true, Type: mustEntityType("pig"), HeadYaw: 90, syncInit: true, SyncCount: 8})
	sess := &Session{server: s}
	sess.entityID.Store(e.ID)
	s.playerHeadTrackingTick(sess)
	if e.SyncCount != 8 || e.lastYHeadB != 0 {
		t.Fatal("Java tracker mutated a presence-only incarnation")
	}
}
