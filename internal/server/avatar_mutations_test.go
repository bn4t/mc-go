package server

import (
	"context"
	"testing"

	"mc-go/api/player"
	"mc-go/internal/world"
)

func assertAvatarFeedEmpty(t *testing.T, c player.Connection) {
	t.Helper()
	m := c.(*avatarConnection).mailbox
	m.mu.Lock()
	defer m.mu.Unlock()
	if pending := len(m.queue) - m.head; pending != 0 {
		t.Fatalf("unexpected extra presence updates: %+v", m.queue[m.head:])
	}
}

func TestSharedAvatarFeedTracksLegacyJavaMutations(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	observer, _ := admitSharedAvatar(t, s, "ObserveChanges")
	conn, reader, writer, sess := configurationBeforePlay(t, s, 776, "JavaChanges")
	defer conn.Close()
	if err := writer.WritePacket(sess.ids().ConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := waitPacket(t, reader, sess.ids().PlayUpdateAttributes); err != nil {
		t.Fatal(err)
	}
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
	joined := nextAvatarUpdate(t, observer, player.Added).Player
	lastRevision := uint64(0)
	check := func(name string, action func()) player.State {
		t.Helper()
		s.simulationAction(action)
		update := nextAvatarUpdate(t, observer, player.Moved)
		state, ok := s.playerStateByID(joined.ID)
		if !ok || update.Player != state || update.Player.ID != joined.ID || update.Revision <= lastRevision {
			t.Fatalf("%s update=%+v committed=%+v exists%v", name, update, state, ok)
		}
		lastRevision = update.Revision
		assertAvatarFeedEmpty(t, observer)
		return state
	}
	state := check("dimension", func() { s.changeDimension(sess, s.dims[dimNether], .5, 40, .5, 0) })
	if state.Dimension != dimNether || state.Pose.Y != 40 {
		t.Fatalf("dimension transition was not atomic: %+v", state)
	}
	state = check("respawn", func() { sess.dead = true; s.respawnPlayer(sess) })
	if state.Dimension != dimOverworld || state.Pose.Y != -60 || state.EntityID != joined.EntityID {
		t.Fatalf("respawn state=%+v", state)
	}
	check("teleportWithin", func() { s.teleportWithin(sess, 2.5, 10, .5) })
	var vehicle *Entity
	state = check("mount", func() {
		vehicle = s.entities.Spawn(&Entity{Type: mustEntityType("minecart"), Dim: dimOverworld, X: 3.5, Y: 10, Z: .5})
		s.rideVehicle(sess, vehicle.ID, false)
	})
	if sess.riding.Load() != vehicle.ID || state.Pose.X != vehicle.X {
		t.Fatalf("mount state=%+v", state)
	}
	check("seat follow", func() { vehicle.X += 1; s.rideTickStep(sess) })
	check("dismount", func() { s.dismount(sess) })
	state = check("mode", func() { s.setGameMode(sess, GameModeSurvival) })
	if state.Gamemode != int32(GameModeSurvival) {
		t.Fatalf("gamemode omitted: %+v", state)
	}
	state = check("wire gamemode", func() { sendCmdPipe(t, writer, "gamemode creative JavaChanges") })
	if state.Gamemode != int32(GameModeCreative) {
		t.Fatalf("wire gamemode omitted: %+v", state)
	}
	state = check("bed", func() {
		foot, _ := parseBlockName("red_bed[facing=south,part=foot]")
		head, _ := parseBlockName("red_bed[facing=south,part=head]")
		s.world.SetBlock(world.Pos{X: 0, Y: 10, Z: 0}, foot)
		s.world.SetBlock(world.Pos{X: 0, Y: 10, Z: 1}, head)
		s.clock.mu.Lock()
		s.clock.day = 13000
		s.clock.mu.Unlock()
		s.useBed(sess, world.Pos{X: 0, Y: 10, Z: 0})
	})
	if state.Pose.Y != 10.6875 || !sess.sleeping.Load() {
		t.Fatalf("bed pose omitted: %+v", state)
	}
	// Exercise full-state equality even when an identity rotation leaves the
	// position untouched. Current respawn retains its entity ID.
	oldID := state.EntityID
	state = check("entity identity", func() {
		p := sess.playerRecord.position()
		e := s.entities.Spawn(&Entity{UUID: sess.getUUID(), Type: mustEntityType("player"), Dim: sess.dimName(), X: p.X, Y: p.Y, Z: p.Z})
		sess.setEntityID(e.ID)
		s.entities.Remove(oldID)
	})
	if state.EntityID == oldID || state.Pose.Y != 10.6875 {
		t.Fatalf("entity-only state change omitted: %+v", state)
	}
	s.simulationAction(func() {
		p := sess.playerRecord.position()
		sess.setPos(p.X, p.Y, p.Z, p.Yaw, p.Pitch, p.OnGround)
		sess.setMode(byte(state.Gamemode))
		_ = s.runInDimension(sess.getName(), s.dims[dimNether], []string{"list"})
	})
	assertAvatarFeedEmpty(t, observer)
	// The existing explicit shared move and its setter mark coalesce too.
	check("shared move", func() {
		p := sess.playerRecord.position()
		p.X = 6.5
		if !s.applyPlayerRecordMoveContextOwned(context.Background(), &sess.playerRecord, p) {
			t.Fatal("shared move rejected")
		}
	})
	s.simulationAction(func() {
		before := s.TickCount()
		s.clock.mu.Lock()
		s.clock.frozen = true
		s.clock.mu.Unlock()
		_ = s.runInDimension(sess.getName(), s.dims[dimNether], []string{"tick", "step", "1"})
		if s.TickCount() != before+1 {
			t.Fatal("temporary command dimension did not exercise a tick-owner yield")
		}
	})
	assertAvatarFeedEmpty(t, observer)
	if current, ok := s.playerStateByID(joined.ID); !ok || current.Dimension != dimOverworld {
		t.Fatalf("temporary yielded command changed physical dimension: %+v", current)
	}
}
