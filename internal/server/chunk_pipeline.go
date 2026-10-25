package server

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"

	"mc-go/internal/protocol"
)

var (
	errChunkEncodingStopped = errors.New("chunk encoder stopped")
	errChunkReservation     = errors.New("chunk snapshot exceeds its reservation")
)

const (
	defaultChunkEncodingJobs  = 128
	defaultChunkEncodingBytes = 64 << 20
)

// chunkEncodingPool bounds detached worker snapshot inputs, including running
// encoders and producer reservations. Initial-join fallback may additionally
// retain one snapshot on the simulation owner to preserve packet ordering.
// Output bodies belong to session budgets. Workers receive closures over
// detached snapshots, never mutable world views.
type chunkEncodingPool struct {
	mu           sync.Mutex
	ready        *sync.Cond
	stopped      bool
	maxJobs      int
	maxBytes     int64
	jobs         int
	bytes        int64
	queue        []*chunkEncodingJob
	active       map[*chunkEncodingJob]struct{}
	reservations map[*chunkEncodingReservation]struct{}
	wg           sync.WaitGroup
}

type chunkEncodingReservation struct {
	pool   *chunkEncodingPool
	jobs   int
	bytes  int64
	closed bool
}

type chunkEncodingJob struct {
	pool    *chunkEncodingPool
	future  *chunkFuture
	bytes   int64
	encode  func() ([]byte, error)
	running bool
	retired bool
}

// chunkFuture occupies the exact outbound FIFO position of its packet. Its
// output is charged when it becomes ready, including when an earlier future
// holds up the writer. Cancellation never releases a running snapshot early.
type chunkFuture struct {
	mu       sync.Mutex
	done     chan struct{}
	finished bool
	data     []byte
	err      error
	job      *chunkEncodingJob
	session  *Session
	id       int32
	admitted bool
	retired  bool
	charge   int64
}

func newChunkEncodingPool(workers, jobs int, bytes int64) *chunkEncodingPool {
	p := &chunkEncodingPool{
		maxJobs: jobs, maxBytes: bytes,
		active:       make(map[*chunkEncodingJob]struct{}),
		reservations: make(map[*chunkEncodingReservation]struct{}),
	}
	p.ready = sync.NewCond(&p.mu)
	p.wg.Add(workers)
	for range workers {
		go p.work()
	}
	return p
}

func (s *Server) chunkEncodingPool() *chunkEncodingPool {
	s.chunkEncoderMu.Lock()
	defer s.chunkEncoderMu.Unlock()
	if s.shuttingDown.Load() {
		return nil
	}
	if s.chunkEncoder == nil {
		workers := s.ChunkEncodingWorkers
		if workers <= 0 {
			workers = min(runtime.GOMAXPROCS(0), 4)
		}
		jobs := s.ChunkEncodingJobs
		if jobs <= 0 {
			jobs = defaultChunkEncodingJobs
		}
		bytes := s.ChunkEncodingBytes
		if bytes <= 0 {
			bytes = defaultChunkEncodingBytes
		}
		s.chunkEncoder = newChunkEncodingPool(workers, jobs, bytes)
	}
	return s.chunkEncoder
}

// tryReserveChunkEncoding never waits for a worker or queue space. Producers
// reserve a whole batch before removing pending columns or marking them sent.
func (s *Server) tryReserveChunkEncoding(jobs int, bytes int64) (*chunkEncodingReservation, bool) {
	p := s.chunkEncodingPool()
	if p == nil {
		return nil, false
	}
	return p.tryReserve(jobs, bytes)
}

func (s *Server) chunkEncodingCanFit(jobs int, bytes int64) bool {
	p := s.chunkEncodingPool()
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.stopped && jobs > 0 && bytes >= 0 && jobs <= p.maxJobs && bytes <= p.maxBytes
}

func (s *Server) chunkEncodingLimits() (jobs int, bytes int64) {
	p := s.chunkEncodingPool()
	if p == nil {
		return 0, 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return 0, 0
	}
	return p.maxJobs, p.maxBytes
}

func (p *chunkEncodingPool) tryReserve(jobs int, bytes int64) (*chunkEncodingReservation, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || jobs <= 0 || bytes < 0 || jobs > p.maxJobs-p.jobs || bytes > p.maxBytes-p.bytes {
		return nil, false
	}
	r := &chunkEncodingReservation{pool: p, jobs: jobs, bytes: bytes}
	p.jobs += jobs
	p.bytes += bytes
	p.reservations[r] = struct{}{}
	return r, true
}

// Submit transfers part of a reservation to a detached encoding job. Close
// must still be called to release any unused reservation, including on errors.
func (r *chunkEncodingReservation) Submit(bytes int64, encode func() ([]byte, error)) *chunkFuture {
	f := &chunkFuture{done: make(chan struct{})}
	p := r.pool
	p.mu.Lock()
	if p.stopped || r.closed {
		p.mu.Unlock()
		f.complete(nil, errChunkEncodingStopped)
		return f
	}
	if r.jobs <= 0 || bytes < 0 || bytes > r.bytes || encode == nil {
		p.mu.Unlock()
		f.complete(nil, errChunkReservation)
		return f
	}
	r.jobs--
	r.bytes -= bytes
	j := &chunkEncodingJob{pool: p, future: f, bytes: bytes, encode: encode}
	f.job = j
	p.queue = append(p.queue, j)
	p.ready.Signal()
	p.mu.Unlock()
	return f
}

func (r *chunkEncodingReservation) Close() {
	p := r.pool
	p.mu.Lock()
	if !r.closed {
		r.closed = true
		p.jobs -= r.jobs
		p.bytes -= r.bytes
		r.jobs, r.bytes = 0, 0
		delete(p.reservations, r)
	}
	p.mu.Unlock()
}

func (p *chunkEncodingPool) work() {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.stopped {
			p.ready.Wait()
		}
		if p.stopped {
			p.mu.Unlock()
			return
		}
		j := p.queue[0]
		copy(p.queue, p.queue[1:])
		p.queue[len(p.queue)-1] = nil
		p.queue = p.queue[:len(p.queue)-1]
		j.running = true
		p.active[j] = struct{}{}
		encode := j.encode
		p.mu.Unlock()

		data, err := runChunkEncoding(encode)
		j.future.complete(data, err)
		encode = nil
		p.mu.Lock()
		j.encode = nil
		p.retireLocked(j)
		delete(p.active, j)
		p.mu.Unlock()
	}
}

func runChunkEncoding(encode func() ([]byte, error)) (data []byte, err error) {
	defer func() {
		if caught := recover(); caught != nil {
			err = fmt.Errorf("chunk encoder panic: %v", caught)
		}
	}()
	return encode()
}

func (p *chunkEncodingPool) retireLocked(j *chunkEncodingJob) {
	if !j.retired {
		j.retired = true
		p.jobs--
		p.bytes -= j.bytes
	}
}

func (p *chunkEncodingPool) cancel(j *chunkEncodingJob) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if j.running || j.retired {
		return
	}
	for i, queued := range p.queue {
		if queued == j {
			copy(p.queue[i:], p.queue[i+1:])
			p.queue[len(p.queue)-1] = nil
			p.queue = p.queue[:len(p.queue)-1]
			j.encode = nil
			p.retireLocked(j)
			return
		}
	}
}

func (p *chunkEncodingPool) Stop() {
	p.mu.Lock()
	var futures []*chunkFuture
	if !p.stopped {
		p.stopped = true
		for r := range p.reservations {
			r.closed = true
			p.jobs -= r.jobs
			p.bytes -= r.bytes
			r.jobs, r.bytes = 0, 0
		}
		clear(p.reservations)
		for _, j := range p.queue {
			j.encode = nil
			p.retireLocked(j)
			futures = append(futures, j.future)
		}
		p.queue = nil
		for j := range p.active {
			futures = append(futures, j.future)
		}
		p.ready.Broadcast()
	}
	p.mu.Unlock()
	for _, f := range futures {
		f.cancelResult(errChunkEncodingStopped)
	}
	p.wg.Wait()
}

func (s *Server) stopChunkEncoder() {
	s.chunkEncoderMu.Lock()
	p := s.chunkEncoder
	s.chunkEncoderMu.Unlock()
	if p != nil {
		p.Stop()
	}
}

func (f *chunkFuture) cancelResult(err error) {
	f.mu.Lock()
	if !f.finished {
		f.finished = true
		f.err = err
		close(f.done)
	}
	f.mu.Unlock()
}

func (f *chunkFuture) complete(data []byte, err error) {
	f.mu.Lock()
	if f.finished {
		f.mu.Unlock()
		return
	}
	var closeSession *Session
	if s := f.session; s != nil && f.admitted && !f.retired {
		s.outMu.Lock()
		select {
		case <-s.done:
			err = net.ErrClosed
		default:
			if err == nil {
				charge := int64(len(data)) + int64(protocol.VarIntLen(f.id))
				limit := s.outByteLimit
				if limit <= 0 {
					limit = defaultOutboundBytes
				}
				if charge > limit-s.outBytes {
					err = ErrOutboundLimit
				} else {
					f.charge = charge
					s.outBytes += charge
				}
			}
			if err != nil {
				s.closeQueueLocked()
				closeSession = s
			}
		}
		s.outMu.Unlock()
	}
	if err == nil && !f.retired {
		f.data = data
	}
	f.err = err
	f.finished = true
	close(f.done)
	f.mu.Unlock()
	if closeSession != nil && closeSession.conn != nil {
		_ = closeSession.conn.Close()
	}
}

// writeChunkFuture binds an encoder result to its packet position without
// waiting for computation. Finished bodies and later completions use the same
// output-byte budget; raw snapshot memory is accounted by the global pool.
func (sess *Session) writeChunkFuture(id int32, f *chunkFuture) error {
	f.mu.Lock()
	if f.admitted || f.retired {
		f.mu.Unlock()
		return errors.New("chunk future already consumed")
	}
	if f.finished && f.err != nil {
		err := f.err
		f.retired = true
		f.mu.Unlock()
		f.Cancel()
		sess.closeQueue()
		return err
	}
	f.session, f.id = sess, id
	if f.finished {
		f.charge = int64(len(f.data)) + int64(protocol.VarIntLen(id))
	}
	err := sess.enqueue(sendPkt{id: id, future: f, charge: f.charge})
	if err == nil {
		f.admitted = true
	} else {
		f.retired = true
		f.data = nil
	}
	f.mu.Unlock()
	if err != nil {
		f.Cancel()
	}
	return err
}

func (f *chunkFuture) Cancel() {
	f.cancelResult(net.ErrClosed)
	if j := f.job; j != nil {
		j.pool.cancel(j)
	}
}

func (f *chunkFuture) wait(done <-chan struct{}) ([]byte, error) {
	select {
	case <-done:
		return nil, net.ErrClosed
	case <-f.done:
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.data, f.err
	}
}
