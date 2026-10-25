package server

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

func outboundPipe(t *testing.T, s *Server, startWriter bool) (*Session, net.Conn) {
	t.Helper()
	client, conn := net.Pipe()
	sess := &Session{
		id: s.nextID.Add(1), server: s, conn: conn,
		done: make(chan struct{}), ver: LatestVersion(),
	}
	sess.initOutbound()
	sess.pw = protocol.NewWriter(conn)
	sess.state.Store(StatePlay)
	if startWriter {
		sess.wg.Add(1)
		go sess.writeLoop()
	}
	t.Cleanup(func() {
		sess.closeQueue()
		_ = client.Close()
		sess.wg.Wait()
	})
	return sess, client
}

func outboundPending(sess *Session) (int, int64) {
	sess.outMu.Lock()
	defer sess.outMu.Unlock()
	return sess.outPackets, sess.outBytes
}

func TestOutboundStalledClientDoesNotBlockTicks(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.OutboundPacketLimit = 8
	s.WriteTimeout = 30 * time.Second
	s.ViewDistance = 1
	slow, _ := outboundPipe(t, s, true)
	healthy, client := outboundPipe(t, s, true)
	// Transport isolation needs no generated skylight. Keep fake players in
	// the existing skylight-free dimension, away from its lava surface.
	for _, sess := range []*Session{slow, healthy} {
		sess.dim.Store(s.dims[dimNether])
		sess.setPos(8.5, 65, 8.5, 0, 0, true)
	}
	s.sessions.Store(slow.id, slow)
	s.sessions.Store(healthy.id, healthy)
	t.Cleanup(func() {
		s.sessions.Delete(slow.id)
		s.sessions.Delete(healthy.id)
	})
	const ticks = 24
	progress := make(chan error, 1)
	go func() {
		pr := protocol.NewReader(client)
		for range ticks {
			p, err := pr.ReadPacket()
			if err != nil {
				progress <- err
				return
			}
			if p.ID != healthy.ids().PlayUpdateTime {
				progress <- errors.New("unexpected healthy-client packet")
				return
			}
			progress <- nil
		}
	}()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range ticks {
			s.StepTicks(1)
			s.broadcastUpdateTime()
			if err := <-progress; err != nil {
				return
			}
			// Keep the healthy client's work drained; the slow client's
			// socket never completes even its first frame header.
			healthy.flush(time.Second)
		}
	}()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("stalled peer blocked simulation or healthy-client progress")
	}
	if s.TickCount() != ticks || healthy.gone() {
		t.Fatalf("healthy progress: ticks=%d, disconnected=%v", s.TickCount(), healthy.gone())
	}
	if !slow.gone() {
		t.Fatal("slow peer was not disconnected on budget exhaustion")
	}
	slow.wg.Wait()
	if packets, size := outboundPending(slow); packets != 0 || size != 0 {
		t.Fatalf("disconnected queue retains %d packets / %d bytes", packets, size)
	}
}

func TestOutboundByteBudgetIncludesActiveWrite(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.OutboundByteLimit = 10
	s.OutboundPacketLimit = 4
	sess, _ := outboundPipe(t, s, true)
	if err := sess.write(1, make([]byte, 7)); err != nil {
		t.Fatal(err)
	}
	// Wait for the first packet to leave the channel, while net.Pipe
	// keeps its socket write blocked. It must still consume eight bytes.
	deadline := time.Now().Add(time.Second)
	for len(sess.sendCh) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sess.sendCh) != 0 {
		t.Fatal("writer did not begin packet")
	}
	if err := sess.write(2, []byte{1, 2}); !errors.Is(err, ErrOutboundLimit) {
		t.Fatalf("admission error = %v, want byte-budget disconnect", err)
	}
	sess.wg.Wait()
	if packets, size := outboundPending(sess); packets != 0 || size != 0 {
		t.Fatalf("queue retains %d packets / %d bytes", packets, size)
	}
	if err := sess.write(3, nil); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("post-disconnect admission = %v", err)
	}
}

func TestOutboundWriteDeadlineClosesReader(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.WriteTimeout = 30 * time.Millisecond
	sess, _ := outboundPipe(t, s, true)
	readDone := make(chan error, 1)
	go func() {
		var b [1]byte
		_, err := sess.conn.Read(b[:])
		readDone <- err
	}()
	if err := sess.write(1, []byte("stalled")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("read succeeded after deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("write deadline did not unblock read loop")
	}
	sess.wg.Wait()
	if packets, size := outboundPending(sess); packets != 0 || size != 0 {
		t.Fatalf("expired writer retains %d packets / %d bytes", packets, size)
	}
}

func TestOutboundCompressionFIFOAndFlush(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	sess, client := outboundPipe(t, s, true)
	payload := bytes.Repeat([]byte("framed"), 200)
	if err := sess.write(IDLoginSetCompression, []byte{1}); err != nil {
		t.Fatal(err)
	}
	sess.setWriteThreshold(1)
	if err := sess.write(IDLoginSuccess, payload); err != nil {
		t.Fatal(err)
	}
	flushed := make(chan struct{})
	go func() { sess.flush(time.Second); close(flushed) }()
	select {
	case <-flushed:
		t.Fatal("flush returned while socket write was blocked")
	case <-time.After(10 * time.Millisecond):
	}
	pr := protocol.NewReader(client)
	p, err := pr.ReadPacket()
	if err != nil || p.ID != IDLoginSetCompression {
		t.Fatalf("raw compression packet = %+v, %v", p, err)
	}
	pr.SetThreshold(1)
	p, err = pr.ReadPacket()
	if err != nil || p.ID != IDLoginSuccess || !bytes.Equal(p.Data, payload) {
		t.Fatalf("compressed success packet = id %d, %v", p.ID, err)
	}
	select {
	case <-flushed:
	case <-time.After(time.Second):
		t.Fatal("flush did not observe completed writes")
	}
}

func TestOutboundConcurrentOverflowCleanup(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.OutboundPacketLimit = 16
	sess, _ := outboundPipe(t, s, true)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = sess.write(1, []byte("state"))
			}
		}()
	}
	wg.Wait()
	sess.wg.Wait()
	if packets, size := outboundPending(sess); packets != 0 || size != 0 {
		t.Fatalf("overflow cleanup retains %d packets / %d bytes", packets, size)
	}
}

func TestDefaultOutboundBudgetCoversJoinBurst(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	sess, client := outboundPipe(t, s, false)
	sess.setIdentity("Burst", offlineUUID("Burst"))
	sess.info.ViewDistance = 10
	// Retain registry configuration and the complete play join together,
	// a stronger burst than normal login (which acknowledges config first).
	if err := s.sendRegistries(sess); err != nil {
		t.Fatal(err)
	}
	if err := s.sendUpdateTags(sess); err != nil {
		t.Fatal(err)
	}
	joined := false
	s.simulationAction(func() { joined = s.joinPlayOwned(sess) })
	if !joined {
		t.Fatal("default budget rejected ordinary view-distance-10 join burst")
	}
	packets, size := outboundPending(sess)
	t.Logf("configuration + view-distance-10 join burst retained %d packets / %d payload bytes", packets, size)
	if packets == 0 || size == 0 {
		t.Fatal("burst was not retained for budget measurement")
	}
	// Now drain that burst and every remaining column through a real writer,
	// acknowledging maximum-size batches as a healthy client would. The
	// configured full view window must stream without exhausting its budget.
	wantChunks := len(sess.sent) + len(sess.pending)
	readChunks := make(chan int, 1)
	go func() {
		pr := protocol.NewReader(client)
		chunks := 0
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				readChunks <- chunks
				return
			}
			if p.ID == sess.ids().PlayChunkData {
				chunks++
			}
		}
	}()
	sess.wg.Add(1)
	go sess.writeLoop()
	sess.flush(2 * time.Second)
	for len(sess.pending) > 0 {
		var err error
		s.simulationAction(func() {
			sess.rate, sess.burst, sess.cool = maxChunkBatch, burstBatches, 0
			err = s.pumpChunks(sess)
		})
		if err != nil {
			t.Fatal(err)
		}
		sess.flush(2 * time.Second)
		if packets, _ := outboundPending(sess); packets != 0 || sess.gone() {
			t.Fatal("healthy full-window stream did not drain within its budget")
		}
	}
	sess.closeQueue()
	select {
	case got := <-readChunks:
		if got != wantChunks {
			t.Fatalf("streamed %d columns, want complete view window of %d", got, wantChunks)
		}
	case <-time.After(time.Second):
		t.Fatal("healthy stream reader did not exit")
	}
}
