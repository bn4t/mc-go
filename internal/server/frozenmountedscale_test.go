package server

import "testing"

func frozenScalePlayer(t *testing.T, s *Server, name string, vehicle *Entity) *Entity {
	t.Helper()
	sess := placeSession(t, s, name, .5, 162, .5)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("player"), UUID: sess.getUUID(), Dim: vehicle.Dim, Living: true, AttrBase: map[string]float64{"scale": 3}})
	sess.playerRecord.setEntityID(e.ID)
	sess.riding.Store(vehicle.ID)
	e.Riding = vehicle.ID
	vehicle.Passengers = append(vehicle.Passengers, e.ID)
	return e
}

func TestFrozenMountedScaleRootAndCadence(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	root := s.entities.Spawn(&Entity{Type: mustEntityType("camel"), Dim: dimOverworld, Living: true, X: .5, Y: 162, Z: .5, NoAI: true, NoGravity: true, Age: 10000, FireTicks: 9, PortalCooldown: 11, SyncCount: 137, AttrBase: map[string]float64{"scale": 1.5}})
	player := frozenScalePlayer(t, s, "FrozenScaleA", root)
	secondPlayer := frozenScalePlayer(t, s, "FrozenScaleB", root)
	beforePlayerBaseline, beforeSecondBaseline := player.syncInit, secondPlayer.syncInit
	s.SetFrozen(true)
	s.frozenMountedScaleCallbacksOwned()
	if root.mobCollisionScale != 1.5 || root.Y != 161.40625 {
		t.Fatalf("root cache%g position%g", root.mobCollisionScale, root.Y)
	}
	if root.SyncCount != 137 || root.TickCount != 0 || root.Age != 10000 || root.FireTicks != 9 || root.PortalCooldown != 11 {
		t.Fatalf("callback advanced body/tracker %+v", root)
	}
	if player.mobCollisionScale != 0 || secondPlayer.mobCollisionScale != 0 {
		t.Fatal("player consumed NPC dimension cache")
	}
	if player.syncInit != beforePlayerBaseline || secondPlayer.syncInit != beforeSecondBaseline {
		t.Fatal("dimension callback changed a player tracker baseline")
	}
	y := root.Y
	s.frozenMountedScaleCallbacksOwned()
	if root.Y != y || root.SyncCount != 137 {
		t.Fatal("repeat callback displaced root or advanced tracker")
	}
}

func TestFrozenMountedScaleIndirectAndSiblingAnchors(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	spawn := func(name string, x float64) *Entity {
		return s.entities.Spawn(&Entity{Type: mustEntityType(name), Dim: dimOverworld, Living: true, X: x, Y: 162, Z: .5, AttrBase: map[string]float64{"scale": 1.5}})
	}
	root, child, sibling, free, npcRoot, npcChild := spawn("camel", .5), spawn("camel", 3.5), spawn("camel", 6.5), spawn("camel", 9.5), spawn("camel", 12.5), spawn("camel", 15.5)
	root.Passengers = []int32{child.ID, sibling.ID}
	child.Riding = root.ID
	sibling.Riding = root.ID
	npcRoot.Passengers = []int32{npcChild.ID}
	npcChild.Riding = npcRoot.ID
	frozenScalePlayer(t, s, "FrozenIndirect", child)
	s.SetFrozen(true)
	s.frozenMountedScaleCallbacksOwned()
	for _, e := range []*Entity{root, child, sibling} {
		if e.mobCollisionScale != 1.5 {
			t.Fatal("eligible graph cache missing", e.ID)
		}
	}
	for _, e := range []*Entity{free, npcRoot, npcChild} {
		if e.mobCollisionScale != 0 || e.Y != 162 {
			t.Fatal("unselected body refreshed", e.ID)
		}
	}
	if child.X != 3.5 || child.Y != 162 || sibling.X != 6.5 || sibling.Y != 162 {
		t.Fatal("riding callback replaced an existing anchor")
	}
}

func TestFrozenMountedScaleAdmissionAndFreezeGuards(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	root := s.entities.Spawn(&Entity{Type: mustEntityType("camel"), Dim: dimOverworld, Living: true, Y: 162, AttrBase: map[string]float64{"scale": 1.5}})
	p := frozenScalePlayer(t, s, "FrozenGuards", root)
	s.frozenMountedScaleCallbacksOwned()
	if root.mobCollisionScale != 0 {
		t.Fatal("normal world callback ran")
	}
	s.SetFrozen(true)
	root.Dormant = true
	s.frozenMountedScaleCallbacksOwned()
	if root.mobCollisionScale != 0 {
		t.Fatal("dormant callback ran")
	}
	root.Dormant = false
	root.Passengers = nil
	s.frozenMountedScaleCallbacksOwned()
	if root.mobCollisionScale != 0 {
		t.Fatal("stale reciprocal mount admitted")
	}
	root.Passengers = []int32{p.ID}
	root.Riding = p.ID
	p.Passengers = []int32{root.ID}
	s.frozenMountedScaleCallbacksOwned()
	if root.mobCollisionScale != 0 {
		t.Fatal("cyclic mount admitted")
	}
	root.Riding = 0
	p.Passengers = nil
	s.refreshMobCollisionScale(root)
	if root.mobCollisionScale != 0 {
		t.Fatal("construction guard lost")
	}
	s.frozenMountedScaleCallbacksOwned()
	if root.mobCollisionScale != 1.5 {
		t.Fatal("admitted frozen callback missing")
	}
}

func TestFrozenMountedScaleNoPhysics(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	root := s.entities.Spawn(&Entity{Type: mustEntityType("vex"), Dim: dimOverworld, Living: true, X: .5, Y: 162, Z: .5, AttrBase: map[string]float64{"scale": 2}})
	frozenScalePlayer(t, s, "FrozenVex", root)
	s.SetFrozen(true)
	s.frozenMountedScaleCallbacksOwned()
	if root.mobCollisionScale != 2 || root.Y != 162 {
		t.Fatal("noPhysics dimensions/position", root.mobCollisionScale, root.Y)
	}
}
