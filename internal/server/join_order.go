package server

import "sync"

// joinOrder admits prepared initial joins in the order they began.
// Vanilla places each player as its login completes, one after another,
// so an earlier joiner is in the world — and hears the next one's join
// message — before the next is placed. mc-go's preparations run
// interleaved and can finish out of order; the chain makes each
// admission wait until every earlier one has been placed or given up.
type joinOrder struct {
	mu   sync.Mutex
	last chan struct{} // closed once the newest ticket is released
}

// joinTicket is one join's place in the chain: wait until prev closes,
// then release mine.
type joinTicket struct {
	prev, mine chan struct{}
	once       sync.Once
}

func (o *joinOrder) take() *joinTicket {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := &joinTicket{prev: o.last, mine: make(chan struct{})}
	o.last = t.mine
	return t
}

// turn is closed when every earlier ticket has been released.
func (t *joinTicket) turn() <-chan struct{} {
	if t.prev == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return t.prev
}

// release passes the turn on. A join that leaves before its turn still
// releases only after its predecessors, so the order holds for later
// tickets.
func (t *joinTicket) release() {
	t.once.Do(func() {
		select {
		case <-t.turn():
			close(t.mine)
		default:
			go func() {
				<-t.turn()
				close(t.mine)
			}()
		}
	})
}
