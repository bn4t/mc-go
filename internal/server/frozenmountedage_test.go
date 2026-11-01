package server

import "testing"

func TestFrozenMountedAgeSharedFrame(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	makeMob := func(age int32) *Entity {
		return s.entities.Spawn(&Entity{Type: mustEntityType("camel"), Dim: dimOverworld, Living: true, NoAI: true, NoGravity: true, Y: 162, Age: age, Baby: age < 0})
	}
	root, child, sibling, free := makeMob(10), makeMob(-2), makeMob(10), makeMob(10)
	root.Passengers = []int32{child.ID, sibling.ID}
	child.Riding = root.ID
	sibling.Riding = root.ID
	frozenScalePlayer(t, s, "AgeA", child)
	frozenScalePlayer(t, s, "AgeB", child)
	s.SetFrozen(true)
	// A world step may happen before the next owner frame. It must not age
	// mounted graph members, even before any previous callback selection.
	s.mobTickStep()
	if root.Age != 10 || child.Age != -2 || sibling.Age != 10 || free.Age != 9 {
		t.Fatalf("world phase: %d %d %d %d", root.Age, child.Age, sibling.Age, free.Age)
	}
	s.frozenMountedScaleCallbacksOwned()
	if root.Age != 9 || child.Age != -1 || sibling.Age != 9 || free.Age != 9 {
		t.Fatal("owner frame age mismatch")
	}
	s.frozenMountedScaleCallbacksOwned()
	if root.Age != 8 || child.Age != 0 || child.Baby || !child.MetaDirty || sibling.Age != 8 {
		t.Fatal("baby transition or duplicate age")
	}
	s.mobTickStep()
	if root.Age != 8 || child.Age != 0 || sibling.Age != 8 || free.Age != 8 {
		t.Fatal("world step duplicated mounted age")
	}
	root.Passengers = nil
	s.frozenMountedScaleCallbacksOwned()
	if root.Age != 8 || sibling.Age != 8 {
		t.Fatal("broken reciprocal graph aged")
	}
}

func TestMobAgeFrameBounds(t *testing.T) {
	e := &Entity{Age: -1, Baby: true, AgeLocked: true}
	mobAgeFrame(e, "camel")
	if e.Age != -1 || !e.Baby || e.MetaDirty {
		t.Fatal("locked baby aged")
	}
	e.Age = 1
	mobAgeFrame(e, "camel")
	if e.Age != 0 {
		t.Fatal("positive cooldown did not expire")
	}
	e.Age = -1
	e.AgeLocked = false
	mobAgeFrame(e, "zombie")
	if e.Age != -1 {
		t.Fatal("non-ageable advanced")
	}
}

func TestFrozenMountedAgePreservesPendingMetadata(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("cow"), Dim: dimOverworld, Living: true, Y: 162, Age: -1, Baby: true, MetaDirty: true, dataSync: []byte{16, 8, 1, 255}})
	e.metaSent.baby = true
	frozenScalePlayer(t, s, "AgePending", e)
	s.SetFrozen(true)
	s.frozenMountedScaleCallbacksOwned()
	if e.Age != 0 || e.Baby || !e.MetaDirty || !e.metaSent.baby || len(e.dataSync) != 4 {
		t.Fatal("pending command pack or later adult transition lost")
	}
	s.clock.mu.Lock()
	s.clock.count++
	s.clock.mu.Unlock()
	s.mobSyncFlush(e)
	if len(e.dataSync) != 0 || !e.MetaDirty || !e.metaSent.baby {
		t.Fatal("queued baby pack did not remain ahead of adult metadata")
	}
	s.mobSyncFlush(e)
	if e.MetaDirty || e.metaSent.baby {
		t.Fatal("adult metadata did not follow queued baby pack")
	}
}
