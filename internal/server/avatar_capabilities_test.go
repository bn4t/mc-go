package server

import (
	"context"
	"reflect"
	"testing"

	"mc-go/internal/world"
)

func TestSharedAvatarDoesNotRunInsideBlockGameplay(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	avatar, state := admitSharedAvatar(t, s, "NoGameplay")
	pose := state.Pose
	pose.Y = 10
	if ok, err := avatar.Move(context.Background(), pose); err != nil || !ok {
		t.Fatalf("move=%v,%v", ok, err)
	}
	wire := world.BlockStateMust("tripwire")
	snow := world.BlockStateMust("powder_snow")
	base := world.Pos{X: 0, Y: 10, Z: 0}
	s.simulationAction(func() {
		s.world.SetBlock(base, wire)
		s.world.SetBlock(world.Pos{X: 0, Y: 11, Z: 0}, snow)
		e, _ := s.entities.GetPtr(state.EntityID)
		if !e.presenceOnly {
			t.Fatal("neutral record did not stamp its entity capability")
		}
		e.FireTicks, e.TicksFrozen, e.PortalCooldown = 10, 7, 5
		before := s.world.Snapshot()
		if s.tripwireEntities(s.dims[dimOverworld], base, wire) {
			t.Fatal("presence avatar counted as a tripwire occupant")
		}
		s.gameTickOwned()
		if after := s.world.Snapshot(); !reflect.DeepEqual(before, after) {
			t.Fatalf("presence-only tick changed blocks: before=%v after=%v", before, after)
		}
		if e.FireTicks != 10 || e.TicksFrozen != 7 || e.PortalCooldown != 5 || e.IsInPowderSnow {
			t.Fatalf("presence-only tick changed survival fields: fire%d frozen%d portal%d snow%v", e.FireTicks, e.TicksFrozen, e.PortalCooldown, e.IsInPowderSnow)
		}
	})
}

func TestSharedAvatarExclusionKeepsJavaAndNonlivingInteractions(t *testing.T) {
	for _, kind := range []string{"java", "falling_block"} {
		t.Run(kind, func(t *testing.T) {
			s := simulationTestServer(t)
			t.Cleanup(s.Stop)
			entity := &Entity{Type: mustEntityType("falling_block"), Dim: dimOverworld, X: .5, Y: 10.4, Z: .5}
			if kind == "java" {
				sess := placeSession(t, s, "ActualJava", .5, 10.4, .5)
				entity.Type = mustEntityType("player")
				entity.UUID = sess.getUUID()
				entity = s.entities.Spawn(entity)
				sess.entityID.Store(entity.ID)
			} else {
				entity = s.entities.Spawn(entity)
			}
			s.simulationAction(func() {
				d := s.dims[dimOverworld]
				wirePos := world.Pos{X: 0, Y: 10, Z: 0}
				wire := world.BlockStateMust("tripwire")
				s.world.SetBlock(wirePos, wire)
				s.world.SetBlock(world.Pos{X: 0, Y: 11, Z: 0}, world.BlockStateMust("powder_snow"))
				if entity.presenceOnly || !s.tripwireEntities(d, wirePos, wire) {
					t.Fatal("ordinary entity was excluded from tripwire occupancy")
				}
				entity.PortalCooldown = 5
				s.entityInsideStep()
				s.portalCooldownStep()
				powered, _ := world.StateProperty(s.world.GetBlock(wirePos), "powered")
				if powered != "true" || !entity.IsInPowderSnow || entity.TicksFrozen != 1 || entity.PortalCooldown != 4 {
					t.Fatalf("ordinary inside effects changed: powered%s snow%v frozen%d portal%d", powered, entity.IsInPowderSnow, entity.TicksFrozen, entity.PortalCooldown)
				}
			})
		})
	}
}

func TestSharedAvatarRemainsVisibleAfterLastJavaLeavesDimension(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	conn, reader, writer, sess := configurationBeforePlay(t, s, 776, "Traveler")
	defer conn.Close()
	if err := writer.WritePacket(sess.ids().ConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := waitPacket(t, reader, sess.ids().PlayUpdateAttributes); err != nil {
		t.Fatal(err)
	}
	_, state := admitSharedAvatar(t, s, "PersistentView")
	readAvatarSpawn(t, reader, sess.ids(), 776, state)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			if _, err := reader.ReadPacket(); err != nil {
				return
			}
		}
	}()
	defer func() { _ = conn.Close(); <-drained }()
	s.simulationAction(func() {
		s.changeDimension(sess, s.dims[dimNether], .5, 40, .5, 0)
		if !s.shouldPark(dimOverworld) {
			t.Fatal("fixture did not unload the last Java observer's dimension")
		}
		e, exists := s.entities.Get(state.EntityID)
		if !exists || e.Dormant {
			t.Fatal("active neutral presence was parked with world gameplay")
		}
	})
	back, backReader, backWriter, backSession := configurationBeforePlay(t, s, 776, "NewObserver")
	defer back.Close()
	if err := backWriter.WritePacket(backSession.ids().ConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	readAvatarSpawn(t, backReader, backSession.ids(), 776, state)
}

func TestSharedAvatarRejectsEnvironmentalPositionMutation(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	avatar, state := admitSharedAvatar(t, s, "NoPhysics")
	java := placeSession(t, s, "JavaPhysics", .5, 10.96, .5)
	javaEntity := s.entities.Spawn(&Entity{Type: mustEntityType("player"), UUID: java.getUUID(), Dim: dimOverworld, X: .5, Y: 10.96, Z: .5})
	java.setEntityID(javaEntity.ID)
	item := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: dimOverworld, X: .5, Y: 10.96, Z: .5})
	s.simulationAction(func() {
		p := s.findPlayerByIDOwned(avatar.ID())
		pose := p.position()
		pose.X, pose.Y, pose.Z = .5, 10.96, .5
		s.commitPlayerPoseOwned(p, pose)
		if s.entities.Update(state.EntityID, 9, 20, 9, 0, 0, false) {
			t.Fatal("generic entity update repositioned a neutral incarnation")
		}
		if !s.entities.Update(javaEntity.ID, .5, 10.96, .5, 0, 0, false) || !s.entities.Update(item.ID, .5, 10.96, .5, 0, 0, false) {
			t.Fatal("generic update rejected an ordinary entity")
		}
		for _, name := range []string{"piston", "farmland"} {
			s.commitPlayerPoseOwned(p, pose)
			java.setPos(.5, 10.96, .5, 0, 0, false)
			s.entities.Update(javaEntity.ID, .5, 10.96, .5, 0, 0, false)
			s.entities.Update(item.ID, .5, 10.96, .5, 0, 0, false)
			if name == "piston" {
				s.pistonPushTick(s.dims[dimOverworld], movingPiston{pos: world.Pos{X: 0, Y: 10, Z: 0}, dir: rsEast}, .5)
			} else {
				s.pushEntitiesUpFarmland(s.dims[dimOverworld], world.Pos{X: 0, Y: 10, Z: 0})
			}
			e, _ := s.entities.Get(state.EntityID)
			if e.X != pose.X || e.Y != pose.Y || p.position() != pose {
				t.Fatalf("%s moved neutral record/entity: pose=%+v entity=(%v,%v)", name, p.position(), e.X, e.Y)
			}
			if name == "piston" && (javaEntity.X <= .5 || item.X <= .5) {
				t.Fatal("piston stopped moving ordinary entities")
			}
			if name == "farmland" && (javaEntity.Y <= 10.96 || item.Y <= 10.96) {
				t.Fatal("farmland stopped moving ordinary entities")
			}
		}
		pose.X = 2.5
		s.commitPlayerPoseOwned(p, pose)
		e, _ := s.entities.Get(state.EntityID)
		if e.X != 2.5 || p.position().X != 2.5 {
			t.Fatal("authoritative player commit did not move record and entity together")
		}
	})
}
