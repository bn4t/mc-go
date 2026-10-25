package server

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"mc-go/api/player"
	"mc-go/api/plugin"
	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func admitSharedAvatar(t *testing.T, s *Server, name string) (player.Connection, player.State) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, err := (attachmentCore{frontendCore: frontendCore{s: s}, attachment: &frontendAttachment{ctx: ctx}}).Admit(ctx, player.Profile{Name: name, UUID: offlineUUID(name)}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	first := nextAvatarUpdate(t, c, player.Snapshot)
	for _, p := range first.Players {
		if p.ID == c.ID() {
			return c, p
		}
	}
	t.Fatal("initial snapshot omitted admitted avatar")
	return nil, player.State{}
}

func nextAvatarUpdate(t *testing.T, c player.Connection, kind player.UpdateKind) player.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < 64; i++ {
		u, err := c.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if u.Kind == kind {
			return u
		}
	}
	t.Fatalf("missing avatar update kind %d", kind)
	return player.Update{}
}

func TestSharedAvatarMovementUsesCanonicalRules(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	c, initial := admitSharedAvatar(t, s, "AvatarRules")
	moves, unsub := s.bus.Subscribe(event.TopicPlayerMove, 16)
	defer unsub()
	ctx := context.Background()
	pose := initial.Pose
	pose.Y = 10
	if ok, err := c.Move(ctx, pose); err != nil || !ok {
		t.Fatalf("initial move=%v,%v", ok, err)
	}
	remove := s.hooks.OnMove(ctx, 0, func(_ context.Context, m *plugin.Move) bool {
		if m.ID != c.ID() || m.Player != initial.Profile.Name {
			t.Errorf("hook identity=%v,%s", m.ID, m.Player)
		}
		switch m.To.X {
		case 1:
			m.To.X = 1.5
		case 2:
			return false
		case 3:
			m.To.X = math.NaN()
		}
		return true
	})
	defer remove()
	pose.X = 1
	if ok, err := c.Move(ctx, pose); err != nil || !ok {
		t.Fatalf("modified move=%v,%v", ok, err)
	}
	if got := nextAvatarUpdate(t, c, player.Correction).Player.Pose.X; got != 1.5 {
		t.Fatalf("modified correction=%v", got)
	}
	s.simulationAction(func() { s.world.SetBlock(world.Pos{X: 4, Y: 10, Z: 0}, world.Stone) })
	for _, x := range []float64{2, 3, 4.5, 30_000_000, math.Inf(1)} {
		pose.X = x
		if ok, err := c.Move(ctx, pose); err != nil || ok {
			t.Fatalf("rejected move X%v=%v,%v", x, ok, err)
		}
		var got player.State
		s.simulationAction(func() { got = s.findPlayerByIDOwned(c.ID()).snapshot() })
		entity, exists := s.entities.Get(initial.EntityID)
		if !exists || got.Pose.X != 1.5 || entity.X != 1.5 {
			t.Fatalf("rejection mutated canonical pose: player=%+v entity=%+v", got, entity)
		}
	}
	count := 0
	s.sessions.Range(func(_, _ any) bool { count++; return true })
	if count != 0 {
		t.Fatalf("neutral admission created %d Java sessions", count)
	}
	if len(moves) != 2 {
		t.Fatalf("accepted moves published %d observations, want 2", len(moves))
	}
	for len(moves) > 0 {
		move := (<-moves).Data.(PlayerMove)
		if move.ID != c.ID() {
			t.Fatalf("movement observation lost incarnation: %+v", move)
		}
	}
}

func readAvatarSpawn(t *testing.T, r *protocol.Reader, ids ProtoIDs, number int32, state player.State) {
	readAvatarSpawnWithProfile(t, r, ids, number, state, false)
}

func readAvatarSpawnWithProfile(t *testing.T, r *protocol.Reader, ids ProtoIDs, number int32, state player.State, knownProfile bool) {
	t.Helper()
	profileSeen := knownProfile
	for i := 0; i < 256; i++ {
		pkt, err := r.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if pkt.ID == ids.PlayPlayerInfoUpd && bytes.Contains(pkt.Data, []byte(state.Profile.Name)) {
			profileSeen = true
		}
		if pkt.ID != ids.PlaySpawnEntity {
			continue
		}
		p := protocol.NewPacketReader(pkt.Data)
		id, err := p.VarInt()
		if err != nil || id != state.EntityID {
			continue
		}
		uuid, _ := p.UUID()
		wireType, _ := p.VarInt()
		profile, _ := javaProfileFor(number)
		wantType, _ := profile.builtinRegistryID("minecraft:entity_type", "minecraft:player")
		if !profileSeen || uuid != state.Profile.UUID || wireType != wantType {
			t.Fatalf("avatar spawn before profile or wrong identity: profile=%v uuid=%x type=%d", profileSeen, uuid, wireType)
		}
		return
	}
	t.Fatal("avatar spawn missing")
}

func readAvatarMove(t *testing.T, r *protocol.Reader, ids ProtoIDs, id int32, wantX float64) {
	t.Helper()
	for i := 0; i < 256; i++ {
		pkt, err := r.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if pkt.ID != ids.PlayEntityTeleportCB {
			continue
		}
		p := protocol.NewPacketReader(pkt.Data)
		entity, _ := p.VarInt()
		if entity != id {
			continue
		}
		x, _ := p.Double()
		if x != wantX {
			t.Fatalf("avatar wire X=%v want%v", x, wantX)
		}
		return
	}
	t.Fatal("avatar movement missing")
}

func TestSharedAvatarJavaPresenceMovementAndLeave(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			s := simulationTestServer(t)
			t.Cleanup(s.Stop)
			c, early, writer, sess := configurationBeforePlay(t, s, number, "EarlyViewer")
			defer c.Close()
			if err := writer.WritePacket(sess.ids().ConfigFinishAck, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := waitPacket(t, early, sess.ids().PlayUpdateAttributes); err != nil {
				t.Fatal(err)
			}
			avatar, state := admitSharedAvatar(t, s, "SharedAvatar")
			readAvatarSpawn(t, early, sess.ids(), number, state)
			lateConn, late, lateWriter, lateSess := configurationBeforePlay(t, s, number, "LateViewer")
			defer lateConn.Close()
			if err := lateWriter.WritePacket(lateSess.ids().ConfigFinishAck, nil); err != nil {
				t.Fatal(err)
			}
			readAvatarSpawn(t, late, lateSess.ids(), number, state)
			pose := state.Pose
			pose.X += 1
			if ok, err := avatar.Move(context.Background(), pose); err != nil || !ok {
				t.Fatalf("move=%v,%v", ok, err)
			}
			readAvatarMove(t, early, sess.ids(), state.EntityID, pose.X)
			readAvatarMove(t, late, lateSess.ids(), state.EntityID, pose.X)
			// Reject a fresh solid overlap and a canceled hook through the
			// same public move call. A subsequent accepted move is a wire
			// barrier: either rejected teleport leaking to a peer fails it.
			s.simulationAction(func() {
				s.world.SetBlock(world.Pos{X: 4, Y: int(pose.Y), Z: 0}, world.Stone)
			})
			bad := pose
			bad.X = 4.5
			if ok, err := avatar.Move(context.Background(), bad); err != nil || ok {
				t.Fatalf("collision move=%v,%v", ok, err)
			}
			if correction := nextAvatarUpdate(t, avatar, player.Correction); correction.Player.Pose != pose {
				t.Fatalf("collision correction=%+v want%+v", correction.Player.Pose, pose)
			}
			removeHook := s.hooks.OnMove(context.Background(), 0, func(_ context.Context, proposal *plugin.Move) bool {
				return proposal.ID != avatar.ID()
			})
			bad.X = 2.5
			ok, err := avatar.Move(context.Background(), bad)
			removeHook()
			if err != nil || ok {
				t.Fatalf("canceled move=%v,%v", ok, err)
			}
			if correction := nextAvatarUpdate(t, avatar, player.Correction); correction.Player.Pose != pose {
				t.Fatalf("hook correction=%+v want%+v", correction.Player.Pose, pose)
			}
			pose.X = 2
			if ok, err := avatar.Move(context.Background(), pose); err != nil || !ok {
				t.Fatalf("move after rejections=%v,%v", ok, err)
			}
			readAvatarMove(t, early, sess.ids(), state.EntityID, pose.X)
			readAvatarMove(t, late, lateSess.ids(), state.EntityID, pose.X)
			// Crossing the view boundary removes only the rendered entity.
			// The profile remains listed and can pair again without a new row.
			pose.X = 1000.5
			if ok, err := avatar.Move(context.Background(), pose); err != nil || !ok {
				t.Fatalf("far move=%v,%v", ok, err)
			}
			for _, r := range []*protocol.Reader{early, late} {
				for i := 0; ; i++ {
					pkt, err := r.ReadPacket()
					if err != nil {
						t.Fatal(err)
					}
					if pkt.ID == sess.ids().PlayPlayerInfoRem && bytes.Contains(pkt.Data, state.Profile.UUID[:]) {
						t.Fatal("leaving render range removed the profile")
					}
					if pkt.ID == sess.ids().PlayRemoveEntity {
						p := protocol.NewPacketReader(pkt.Data)
						_, _ = p.VarInt()
						if id, _ := p.VarInt(); id == state.EntityID {
							break
						}
					}
					if i == 255 {
						t.Fatal("range crossing did not unpair neutral avatar")
					}
				}
			}
			pose.X = state.Pose.X + 1
			if ok, err := avatar.Move(context.Background(), pose); err != nil || !ok {
				t.Fatalf("return move=%v,%v", ok, err)
			}
			readAvatarSpawnWithProfile(t, early, sess.ids(), number, state, true)
			readAvatarSpawnWithProfile(t, late, lateSess.ids(), number, state, true)
			if err := avatar.Close(); err != nil {
				t.Fatal(err)
			}
			for _, r := range []*protocol.Reader{early, late} {
				infoRemoved := false
				for i := 0; i < 64; i++ {
					pkt, err := r.ReadPacket()
					if err != nil {
						t.Fatal(err)
					}
					if pkt.ID == sess.ids().PlayPlayerInfoRem && bytes.Contains(pkt.Data, state.Profile.UUID[:]) {
						infoRemoved = true
					}
					if pkt.ID == sess.ids().PlayRemoveEntity {
						p := protocol.NewPacketReader(pkt.Data)
						n, _ := p.VarInt()
						id, _ := p.VarInt()
						if n == 1 && id == state.EntityID {
							if !infoRemoved {
								t.Fatal("entity removal overtook tab removal")
							}
							break
						}
					}
					if i == 63 {
						t.Fatal("avatar removal missing")
					}
				}
			}
		})
	}
}
