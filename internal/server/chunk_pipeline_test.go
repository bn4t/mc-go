package server

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

func chunkPoolUsage(p *chunkEncodingPool) (int, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.jobs, p.bytes
}

func awaitChunkPoolEmpty(t *testing.T, p *chunkEncodingPool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		jobs, bytes := chunkPoolUsage(p)
		if jobs == 0 && bytes == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	jobs, bytes := chunkPoolUsage(p)
	t.Fatalf("encoder retains %d jobs / %d bytes", jobs, bytes)
}

func TestChunkPoolReservationAndCancellation(t *testing.T) {
	p := newChunkEncodingPool(1, 2, 100)
	t.Cleanup(p.Stop)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	r, ok := p.tryReserve(2, 100)
	if !ok {
		t.Fatal("initial reservation refused")
	}
	if _, ok := p.tryReserve(1, 0); ok {
		t.Fatal("job reservations exceeded global limit")
	}
	started := make(chan struct{})
	first := r.Submit(40, func() ([]byte, error) {
		close(started)
		<-release
		return []byte("first"), nil
	})
	<-started
	second := r.Submit(60, func() ([]byte, error) {
		t.Error("canceled queued encoder executed")
		return nil, nil
	})
	r.Close()
	second.Cancel()
	first.Cancel()
	if jobs, bytes := chunkPoolUsage(p); jobs != 1 || bytes != 40 {
		t.Fatalf("running input was released early: %d jobs / %d bytes", jobs, bytes)
	}
	if _, ok := p.tryReserve(1, 61); ok {
		t.Fatal("byte reservations exceeded global limit")
	}
	r2, ok := p.tryReserve(1, 60)
	if !ok {
		t.Fatal("canceled queued work did not release capacity")
	}
	r2.Close()
	once.Do(func() { close(release) })
	awaitChunkPoolEmpty(t, p)
	first.Cancel()
	second.Cancel()
	if jobs, bytes := chunkPoolUsage(p); jobs != 0 || bytes != 0 {
		t.Fatalf("duplicate cancellation changed quotas: %d / %d", jobs, bytes)
	}
}

func TestChunkPoolStopCancelsFuturesAndJoinsWorkers(t *testing.T) {
	p := newChunkEncodingPool(1, 3, 120)
	t.Cleanup(p.Stop)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	r, _ := p.tryReserve(3, 120)
	started := make(chan struct{})
	f := r.Submit(40, func() ([]byte, error) {
		close(started)
		<-release
		return nil, nil
	})
	<-started
	stopped := make(chan struct{})
	go func() { p.Stop(); close(stopped) }()
	select {
	case <-f.done:
	case <-time.After(time.Second):
		t.Fatal("stopping encoder did not wake future waiter")
	}
	if _, err := f.wait(nil); !errors.Is(err, errChunkEncodingStopped) {
		t.Fatalf("future error = %v", err)
	}
	if jobs, bytes := chunkPoolUsage(p); jobs != 1 || bytes != 40 {
		t.Fatalf("stopped encoder retains wrong running quota: %d / %d", jobs, bytes)
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned before its worker exited")
	default:
	}
	once.Do(func() { close(release) })
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not join completed worker")
	}
	r.Close()
	awaitChunkPoolEmpty(t, p)
	if _, ok := p.tryReserve(1, 1); ok {
		t.Fatal("stopped pool accepted work")
	}
}

func TestChunkFuturePreservesPacketFIFOAndOutputBudget(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	p := newChunkEncodingPool(2, 2, 1<<20)
	t.Cleanup(p.Stop)
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	sess, client := outboundPipe(t, s, true)
	r, _ := p.tryReserve(2, 1<<20)
	first := r.Submit(1<<19, func() ([]byte, error) {
		<-release
		return []byte("one"), nil
	})
	second := r.Submit(1<<19, func() ([]byte, error) { return []byte("two"), nil })
	r.Close()
	if err := sess.writeChunkFuture(1, first); err != nil {
		t.Fatal(err)
	}
	if err := sess.writeChunkFuture(2, second); err != nil {
		t.Fatal(err)
	}
	if err := sess.write(3, []byte("delta")); err != nil {
		t.Fatal(err)
	}
	<-second.done
	if packets, bytes := outboundPending(sess); packets != 3 || bytes != 10 {
		t.Fatalf("snapshot input leaked into output budget: %d packets / %d bytes", packets, bytes)
	}
	read := make(chan error, 1)
	go func() {
		pr := protocol.NewReader(client)
		for _, want := range []int32{1, 2, 3} {
			packet, err := pr.ReadPacket()
			if err != nil {
				read <- err
				return
			}
			if packet.ID != want {
				read <- fmt.Errorf("packet %d overtook %d", packet.ID, want)
				return
			}
		}
		read <- nil
	}()
	select {
	case err := <-read:
		t.Fatalf("unresolved first snapshot did not hold FIFO: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("resolved chunk futures did not drain")
	}
	sess.flush(time.Second)
	if packets, bytes := outboundPending(sess); packets != 0 || bytes != 0 {
		t.Fatalf("written futures retain output quota: %d / %d", packets, bytes)
	}
	awaitChunkPoolEmpty(t, p)
}

func TestChunkFutureCompletionEnforcesOutputBudget(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.OutboundByteLimit = 8
	sess, _ := outboundPipe(t, s, true)
	f := &chunkFuture{done: make(chan struct{})}
	if err := sess.writeChunkFuture(1, f); err != nil {
		t.Fatal(err)
	}
	f.complete(make([]byte, 8), nil) // packet id makes nine bytes
	sess.wg.Wait()
	if !sess.gone() {
		t.Fatal("oversized completed chunk did not disconnect session")
	}
	if packets, bytes := outboundPending(sess); packets != 0 || bytes != 0 {
		t.Fatalf("overloaded future retains output quota: %d / %d", packets, bytes)
	}
}

func TestChunkFutureReadyBeforeAdmissionAccountsPayload(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.OutboundByteLimit = 8
	for _, size := range []int{7, 8} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			sess, _ := outboundPipe(t, s, true)
			f := &chunkFuture{done: make(chan struct{})}
			f.complete(make([]byte, size), nil)
			err := sess.writeChunkFuture(1, f)
			if size == 8 {
				if !errors.Is(err, ErrOutboundLimit) {
					t.Fatalf("ready oversized payload admitted: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if packets, bytes := outboundPending(sess); packets != 1 || bytes != 8 {
					t.Fatalf("ready payload accounting: %d packets / %d bytes", packets, bytes)
				}
			}
			sess.closeQueue()
			sess.wg.Wait()
			if packets, bytes := outboundPending(sess); packets != 0 || bytes != 0 {
				t.Fatalf("ready payload cleanup retained quota: %d / %d", packets, bytes)
			}
		})
	}
}

func TestChunkFutureFailureClosesSession(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	sess, _ := outboundPipe(t, s, true)
	f := &chunkFuture{done: make(chan struct{})}
	if err := sess.writeChunkFuture(1, f); err != nil {
		t.Fatal(err)
	}
	f.complete(nil, errors.New("unmapped block state"))
	sess.wg.Wait()
	if !sess.gone() {
		t.Fatal("encoding failure left an incomplete stream connected")
	}
	if packets, bytes := outboundPending(sess); packets != 0 || bytes != 0 {
		t.Fatalf("failed future retains output quota: %d / %d", packets, bytes)
	}
}

func TestChunkPoolConcurrentStopSubmitCancel(t *testing.T) {
	for range 20 {
		p := newChunkEncodingPool(2, 32, 32)
		r, _ := p.tryReserve(32, 32)
		futures := make(chan *chunkFuture, 32)
		first := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			defer close(futures)
			for i := range 32 {
				f := r.Submit(1, func() ([]byte, error) { return []byte("encoded"), nil })
				futures <- f
				if i == 0 {
					close(first)
				}
			}
			r.Close()
		}()
		go func() {
			defer wg.Done()
			for f := range futures {
				f.Cancel()
				select {
				case <-f.done:
				case <-time.After(time.Second):
					t.Error("cancellation left a future unresolved")
				}
			}
		}()
		go func() {
			defer wg.Done()
			<-first
			p.Stop()
		}()
		wg.Wait()
		p.Stop()
		awaitChunkPoolEmpty(t, p)
	}
}

func TestChunkFutureCompletionRacesSessionClose(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	for range 40 {
		sess, _ := outboundPipe(t, s, true)
		f := &chunkFuture{done: make(chan struct{})}
		if err := sess.writeChunkFuture(1, f); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); f.complete([]byte("encoded"), nil) }()
		go func() { defer wg.Done(); sess.closeQueue() }()
		wg.Wait()
		sess.wg.Wait()
		if packets, bytes := outboundPending(sess); packets != 0 || bytes != 0 {
			t.Fatalf("concurrent completion/close retains quota: %d / %d", packets, bytes)
		}
	}
}

func TestChunkPipelineShutdownUnblocksWriterBeforeWorkerJoin(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	client, conn := net.Pipe()
	handled := make(chan struct{})
	go func() { s.handleConn(conn); close(handled) }()
	var sess *Session
	deadline := time.Now().Add(time.Second)
	for sess == nil && time.Now().Before(deadline) {
		s.sessions.Range(func(_, value any) bool { sess = value.(*Session); return false })
		if sess == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if sess == nil {
		_ = client.Close()
		t.Fatal("connection did not register")
	}
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		_ = client.Close()
		<-handled
	})
	r, ok := s.tryReserveChunkEncoding(1, 40)
	if !ok {
		t.Fatal("could not reserve encoder")
	}
	started := make(chan struct{})
	f := r.Submit(40, func() ([]byte, error) {
		close(started)
		<-release
		return []byte("encoded"), nil
	})
	r.Close()
	<-started
	if err := sess.writeChunkFuture(1, f); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("connection teardown waited on running chunk encoder")
	}
	select {
	case <-f.done:
	default:
		t.Fatal("session teardown did not cancel unresolved writer future")
	}
	if jobs, bytes := chunkPoolUsage(s.chunkEncoder); jobs != 1 || bytes != 40 {
		t.Fatalf("running snapshot released before worker return: %d / %d", jobs, bytes)
	}
	select {
	case <-stopped:
		t.Fatal("runtime stopped before encoder worker exited")
	default:
	}
	once.Do(func() { close(release) })
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("runtime did not join released encoder worker")
	}
	awaitChunkPoolEmpty(t, s.chunkEncoder)
}
