package server

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

type keepAlivePeer struct {
	pingSeen  chan struct{}
	replySeen chan struct{}
	readErr   chan error
	pings     atomic.Int64
}

// The peer always reads. Its mode controls whether it echoes heartbeats,
// sends another valid play packet instead, or remains silent.
func readKeepAlivePeer(s *Server, sess *Session, client net.Conn, mode string) *keepAlivePeer {
	peer := &keepAlivePeer{pingSeen: make(chan struct{}), replySeen: make(chan struct{}), readErr: make(chan error, 1)}
	go func() {
		pr, pw := protocol.NewReader(client), protocol.NewWriter(client)
		var first sync.Once
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				peer.readErr <- err
				return
			}
			if p.ID == sess.ids().PlayDisconnect {
				peer.readErr <- fmt.Errorf("server disconnect: %q", p.Data)
				return
			}
			if p.ID != sess.ids().PlayKeepAliveCB {
				continue
			}
			peer.pings.Add(1)
			first.Do(func() { close(peer.pingSeen) })
			var id int32
			var body []byte
			switch mode {
			case "echo":
				id, body = sess.ids().PlayKeepAliveSB, p.Data
			case "other":
				id = sess.ids().PlayPlayerLoadedSB
			default:
				continue
			}
			if err := pw.WritePacket(id, body); err != nil {
				peer.readErr <- err
				return
			}
		}
	}()
	go func() {
		pr := protocol.NewReader(sess.conn)
		var first sync.Once
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				return
			}
			sess.markAlive()
			if p.ID == sess.ids().PlayKeepAliveSB {
				s.routeKeepAlive(sess, p)
			}
			first.Do(func() { close(peer.replySeen) })
		}
	}()
	return peer
}

// A peer cannot answer a request that is still queued behind server work.
// These reads have no deadlines, isolating liveness from framing retries.
func TestKeepAliveWaitsForChunkDelivery(t *testing.T) {
	for _, mode := range []string{"echo", "other", "silent", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s := New("unused")
			s.KeepAliveEvery = 10 * time.Millisecond
			s.KeepAliveTimeout = 60 * time.Millisecond
			sess, client := outboundPipe(t, s, true)
			sess.markAlive()
			future := &chunkFuture{done: make(chan struct{})}
			if err := sess.writeChunkFuture(sess.ids().PlayChunkData, future); err != nil {
				t.Fatal(err)
			}
			peer := readKeepAlivePeer(s, sess, client, mode)
			stop := sess.startKeepAlive()
			defer close(stop)
			time.Sleep(3 * s.KeepAliveTimeout)
			if peer.pings.Load() != 0 {
				t.Fatal("heartbeat overtook the unfinished chunk")
			}
			if sess.gone() {
				t.Fatal("healthy reader timed out before its queued heartbeat reached the wire")
			}
			if mode == "cancel" {
				future.Cancel()
				select {
				case <-peer.readErr:
				case <-time.After(time.Second):
					t.Fatal("canceled future left writer or reader blocked")
				}
				if !sess.gone() {
					t.Fatal("canceled output did not close the session")
				}
				return
			}
			future.complete([]byte("ready"), nil)
			select {
			case <-peer.pingSeen:
			case err := <-peer.readErr:
				t.Fatal(err)
			case <-time.After(time.Second):
				t.Fatal("ready output never delivered a heartbeat")
			}
			if mode == "silent" {
				deadline := time.Now().Add(time.Second)
				for !sess.gone() && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if !sess.gone() {
					t.Fatal("later heartbeat writes kept an unresponsive client alive")
				}
				return
			}
			time.Sleep(3 * s.KeepAliveTimeout)
			if sess.gone() {
				t.Fatal("responding client disconnected after chunk delivery")
			}
			select {
			case err := <-peer.readErr:
				t.Fatalf("healthy reader failed: %v", err)
			default:
			}
		})
	}
}

// The full heartbeat reaches the reader, which responds before its body
// write returns. This exposes the completion/echo race deterministically.
type heldKeepAliveWrite struct {
	conn    net.Conn
	id      byte
	release <-chan struct{}
	once    sync.Once
}

func (w *heldKeepAliveWrite) Write(p []byte) (int, error) {
	n, err := w.conn.Write(p)
	if err == nil && len(p) == 9 && p[0] == w.id {
		w.once.Do(func() { <-w.release })
	}
	return n, err
}

func TestKeepAliveEchoBeforeWriteCompletion(t *testing.T) {
	s := New("unused")
	s.KeepAliveEvery = 10 * time.Millisecond
	s.KeepAliveTimeout = 60 * time.Millisecond
	sess, client := outboundPipe(t, s, false)
	sess.markAlive()
	release := make(chan struct{})
	var released sync.Once
	defer released.Do(func() { close(release) })
	sess.pw = protocol.NewWriter(&heldKeepAliveWrite{conn: sess.conn, id: byte(sess.ids().PlayKeepAliveCB), release: release})
	sess.wg.Add(1)
	go sess.writeLoop()
	peer := readKeepAlivePeer(s, sess, client, "echo")
	sess.kaNext.Store(1)
	sess.kaTime.Store(time.Now().UnixNano())
	var body protocol.Buffer
	body.Long(1)
	if err := sess.write(sess.ids().PlayKeepAliveCB, body.Bytes()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-peer.replySeen:
	case err := <-peer.readErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("peer did not echo the delivered heartbeat")
	}
	// Put unfinished output immediately behind that already-answered ping.
	// The periodic sender starts only now, so none can overtake this future.
	future := &chunkFuture{done: make(chan struct{})}
	if err := sess.writeChunkFuture(sess.ids().PlayChunkData, future); err != nil {
		t.Fatal(err)
	}
	released.Do(func() { close(release) })
	stop := sess.startKeepAlive()
	defer close(stop)
	time.Sleep(3 * s.KeepAliveTimeout)
	if sess.gone() {
		t.Fatal("writer completion rearmed a heartbeat the client had already answered")
	}
	if peer.pings.Load() != 1 {
		t.Fatal("later heartbeat overtook the unfinished output")
	}
}
