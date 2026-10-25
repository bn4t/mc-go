package server

import (
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

func configurationBeforePlay(t *testing.T, s *Server, number int32, name string) (net.Conn, *protocol.Reader, *protocol.Writer, *Session) {
	t.Helper()
	c, reader, writer := pipePair(t, s)
	if err := sendHandshake(writer, number, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var login protocol.Buffer
	login.String(name)
	login.Raw(make([]byte, 16))
	if err := writer.WritePacket(IDLoginStart, login.Bytes()); err != nil {
		t.Fatal(err)
	}
	packet, err := reader.ReadPacket()
	if err != nil || packet.ID != IDLoginSetCompression {
		t.Fatalf("compression packet=%#x,%v", packet.ID, err)
	}
	threshold, err := protocol.NewPacketReader(packet.Data).VarInt()
	if err != nil {
		t.Fatal(err)
	}
	reader.SetThreshold(threshold)
	writer.SetThreshold(threshold)
	packet, err = reader.ReadPacket()
	if err != nil || packet.ID != IDLoginSuccess {
		t.Fatalf("login packet=%#x,%v", packet.ID, err)
	}
	if err := writer.WritePacket(IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int32{IDConfigPluginMessageCB, IDConfigFeatureFlags, IDConfigKnownPacksCB} {
		packet, err = reader.ReadPacket()
		if err != nil || packet.ID != id {
			t.Fatalf("configuration packet=%#x,%v; want %#x", packet.ID, err, id)
		}
	}
	if err := writer.WritePacket(IDConfigKnownPacks, []byte{0}); err != nil {
		t.Fatal(err)
	}
	for {
		packet, err = reader.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if packet.ID == IDConfigFinish {
			break
		}
	}
	sess := s.findSessionByName(name)
	if sess == nil || sess.state.Load() != StateConfiguration {
		t.Fatal("configuration session not tracked")
	}
	return c, reader, writer, sess
}

func TestJavaPlayPublicationFollowsJoinAdmission(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			c, reader, writer, sess := configurationBeforePlay(t, s, number, fmt.Sprintf("JoinOrder%d", number))
			defer c.Close()
			// Freeze the exact first-play enqueue rather than race a timer.
			// Entity initialization gives an atomic progress signal that the
			// configuration acknowledgement entered the owned join action.
			sess.outMu.Lock()
			locked := true
			defer func() {
				if locked {
					sess.outMu.Unlock()
				}
			}()
			if err := writer.WritePacket(IDConfigFinishAck, nil); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for sess.entityID.Load() == 0 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
			if sess.entityID.Load() == 0 {
				t.Fatal("join did not reach the blocked outbound admission")
			}
			if got := sess.state.Load(); got != StateConfiguration {
				t.Fatalf("session became broadcast-eligible before Join was queued: state=%d", got)
			}
			// This fanout must skip the not-yet-published session, including
			// callers that merely read its atomic phase outside ownership.
			s.broadcastUpdateTimeBody([]byte{0})
			if len(sess.sendCh) != 0 {
				t.Fatal("play broadcast entered the queue before Join")
			}
			sess.outMu.Unlock()
			locked = false
			packet, err := reader.ReadPacket()
			if err != nil || packet.ID != sess.ids().PlayJoinGame {
				t.Fatalf("first play packet=%#x,%v; want Join Game", packet.ID, err)
			}
		})
	}
}

func TestJavaFailedJoinAdmissionDoesNotPublishPlay(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			c, _, writer, sess := configurationBeforePlay(t, s, number, fmt.Sprintf("JoinFail%d", number))
			defer c.Close()
			// Configuration is fully drained. Make the first Join packet
			// exceed admission without changing any earlier handshake packet.
			sess.outMu.Lock()
			sess.outByteLimit = 1
			sess.outMu.Unlock()
			if err := writer.WritePacket(IDConfigFinishAck, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case <-sess.done:
			case <-time.After(5 * time.Second):
				t.Fatal("oversized Join did not retire the connection")
			}
			if sess.state.Load() != StateConfiguration || sess.spawned.Load() {
				t.Fatal("failed Join published a playable session")
			}
			if len(sess.sendCh) != 0 {
				t.Fatal("failed Join queued follow-up play packets")
			}
		})
	}
}
