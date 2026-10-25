package server

import (
	"context"
	"net"
	"testing"

	"mc-go/internal/event"
	"mc-go/internal/plugin"
)

// enforceSession tracks a fake play-stage session without a network
// flow: writes land in the buffered queue for direct inspection.
func enforceSession(t *testing.T, s *Server, name string) (*Session, net.Conn) {
	t.Helper()
	cc, sc := net.Pipe()
	sess := &Session{
		id:     s.nextID.Add(1),
		server: s,
		conn:   sc,
		sendCh: make(chan sendPkt, 64),
		done:   make(chan struct{}),
		ver:    LatestVersion(),
	}
	sess.teleportNext.Store(1)
	sess.setIdentity(name, offlineUUID(name))
	sess.state.Store(StatePlay)
	sess.setMode(byte(s.GameMode))
	sess.setPos(8.5, 65, 8.5, 90, 0, true)
	sess.playerRecord.java, sess.playerRecord.core = sess, s
	s.simulationAction(func() {
		if err := s.reservePlayerOwned(context.Background(), &sess.playerRecord); err != nil {
			t.Fatal(err)
		}
		s.replacePlayersOwned(&sess.playerRecord)
		s.commitPlayerAdmissionOwned(&sess.playerRecord)
	})
	s.track(sess)
	t.Cleanup(func() {
		_ = cc.Close()
		_ = sc.Close()
		s.untrack(sess)
	})
	return sess, cc
}

func TestPlayerStateOf(t *testing.T) {
	s := New("unused")
	sess, _ := enforceSession(t, s, "Warden")
	sess.rtt.Store(int64(25_000_000))

	st, ok := s.PlayerStateOf("Warden")
	if !ok {
		t.Fatal("no state for tracked player")
	}
	if st.X != 8.5 || st.Y != 65 || st.Z != 8.5 || st.Yaw != 90 || !st.OnGround {
		t.Fatalf("state = %+v", st)
	}
	if st.Latency != 25_000_000 {
		t.Fatalf("latency = %v", st.Latency)
	}
	if st.Gamemode != 1 {
		t.Fatalf("gamemode = %d", st.Gamemode)
	}
	s.simulationAction(func() { sess.setMode(0) })
	if survival, _ := s.PlayerStateOf("Warden"); survival.Gamemode != 0 {
		t.Fatalf("survival gamemode = %d", survival.Gamemode)
	}
	if _, ok := s.PlayerStateOf("Nobody"); ok {
		t.Fatal("state for unknown player")
	}
	var _ plugin.PlayerState = st
}

func TestTeleportPlayer(t *testing.T) {
	s := New("unused")
	enforceSession(t, s, "Warden")
	ch, unsub := s.Bus().Subscribe(event.TopicPlayerTeleport, 8)
	defer unsub()

	if !s.TeleportPlayer("Warden", 100.5, 70, -40.5) {
		t.Fatal("teleport failed")
	}
	st, _ := s.PlayerStateOf("Warden")
	if st.X != 100.5 || st.Y != 70 || st.Z != -40.5 {
		t.Fatalf("state after teleport = %+v", st)
	}
	select {
	case e := <-ch:
		tp := e.Data.(PlayerTeleport)
		if tp.Name != "Warden" || tp.X != 100.5 {
			t.Fatalf("teleport event = %+v", tp)
		}
	default:
		t.Fatal("no teleport event")
	}
	if s.TeleportPlayer("Nobody", 0, 65, 0) {
		t.Fatal("teleport of unknown player succeeded")
	}
}

func TestKickPlayer(t *testing.T) {
	s := New("unused")
	sess, _ := enforceSession(t, s, "Warden")

	if !s.KickPlayer("Warden", "test kick") {
		t.Fatal("kick failed")
	}
	select {
	case pkt := <-sess.sendCh:
		pkt = materializeOutboundPacket(t, sess, pkt)
		if pkt.id != IDPlayDisconnect {
			t.Fatalf("kick packet = %#x", pkt.id)
		}
	default:
		t.Fatal("no kick packet queued")
	}
	if s.KickPlayer("Nobody", "x") {
		t.Fatal("kick of unknown player succeeded")
	}
}
