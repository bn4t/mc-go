package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestFrozenMountedPortalCooldownFrames(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	makeMob := func(cd int32) *Entity {
		return s.entities.Spawn(&Entity{Type: mustEntityType("camel"), Dim: dimOverworld, Living: true, NoAI: true, NoGravity: true, Y: 162, PortalCooldown: cd})
	}
	root, child, sibling, free := makeMob(4), makeMob(4), makeMob(4), makeMob(4)
	root.Passengers = []int32{child.ID, sibling.ID}
	child.Riding = root.ID
	sibling.Riding = root.ID
	p := frozenScalePlayer(t, s, "PortalA", child)
	frozenScalePlayer(t, s, "PortalB", child)
	s.SetFrozen(true)
	s.portalCooldownStep() // world step before any owner frame
	if root.PortalCooldown != 4 || child.PortalCooldown != 4 || sibling.PortalCooldown != 4 || free.PortalCooldown != 3 {
		t.Fatal("world step selected wrong cooldown owners")
	}
	s.frozenMountedScaleCallbacksOwned()
	if root.PortalCooldown != 3 || child.PortalCooldown != 3 || sibling.PortalCooldown != 3 || free.PortalCooldown != 3 || p.PortalCooldown != 0 {
		t.Fatal("owner frame advanced incorrect entities")
	}
	s.portalCooldownStep()
	if root.PortalCooldown != 3 || child.PortalCooldown != 3 || sibling.PortalCooldown != 3 || free.PortalCooldown != 2 {
		t.Fatal("world step duplicated mounted cooldown")
	}
	for i := 0; i < 5; i++ {
		s.frozenMountedScaleCallbacksOwned()
	}
	if root.PortalCooldown != 0 || child.PortalCooldown != 0 || sibling.PortalCooldown != 0 {
		t.Fatal("cooldown did not stop at zero")
	}
	root.PortalCooldown = -1
	portalCooldownFrame(root)
	if root.PortalCooldown != -1 {
		t.Fatal("negative cooldown changed")
	}
	root.Passengers = nil
	sibling.PortalCooldown = 4
	s.frozenMountedScaleCallbacksOwned()
	if sibling.PortalCooldown != 4 {
		t.Fatal("broken graph advanced")
	}
	root.PortalCooldown = 4
	s.portalCooldownStep()
	if root.PortalCooldown != 3 || sibling.PortalCooldown != 3 {
		t.Fatal("world step did not resume detached entities")
	}
}

func TestFrozenMountedPortalCooldownContact(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	root := s.entities.Spawn(&Entity{Type: mustEntityType("camel"), Dim: dimOverworld, Living: true, NoAI: true, NoGravity: true, X: .5, Y: 162, Z: .5, Health: 20, PortalCooldown: 100, mobCollisionScale: 1.5, AttrBase: map[string]float64{"scale": 1.5}})
	player := frozenScalePlayer(t, s, "PortalContact", root)
	s.SetFrozen(true)
	s.world.SetBlock(world.Pos{X: 1, Y: 162, Z: 0}, world.BlockStateMust("nether_portal"))
	s.frozenMountedScaleCallbacksOwned()
	if root.PortalCooldown != 10 {
		t.Fatalf("first player rearm %d", root.PortalCooldown)
	}
	root.Health = 0
	root.PortalCooldown = 100
	s.frozenMountedScaleCallbacksOwned()
	if root.PortalCooldown != 99 {
		t.Fatal("zero-health root rearmed portal cooldown")
	}
	root.Health = 20
	root.PortalCooldown = 10
	s.world.SetBlock(world.Pos{X: 1, Y: 162, Z: 0}, world.Air)
	s.frozenMountedScaleCallbacksOwned()
	if root.PortalCooldown != 9 {
		t.Fatal("exit failed to drain")
	}
	child := s.entities.Spawn(&Entity{Type: mustEntityType("camel"), Dim: dimOverworld, Living: true, X: .5, Y: 162, Z: .5, PortalCooldown: 7})
	root.Passengers = []int32{child.ID}
	child.Riding = root.ID
	child.Passengers = []int32{player.ID}
	player.Riding = child.ID
	s.world.SetBlock(world.Pos{X: 1, Y: 162, Z: 0}, world.BlockStateMust("nether_portal"))
	s.frozenMountedScaleCallbacksOwned()
	if root.PortalCooldown != 300 || child.PortalCooldown != 6 {
		t.Fatalf("first NPC rearm or riding guard: root%d child%d", root.PortalCooldown, child.PortalCooldown)
	}
	s.world.SetBlock(world.Pos{X: 1, Y: 162, Z: 0}, world.Air)
	s.world.SetBlock(world.Pos{X: 2, Y: 162, Z: 0}, world.BlockStateMust("nether_portal"))
	root.PortalCooldown = 100
	s.frozenMountedScaleCallbacksOwned()
	if root.PortalCooldown != 99 {
		t.Fatal("outside portal rearmed")
	}
	root.Dormant = true
	root.PortalCooldown = 20
	s.frozenMountedScaleCallbacksOwned()
	if root.PortalCooldown != 20 {
		t.Fatal("dormant graph advanced")
	}
}
