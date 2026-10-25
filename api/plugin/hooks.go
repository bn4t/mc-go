package plugin

import (
	"context"
	"sort"
	"sync"
)

// Position is a protocol-neutral player pose.
type Position struct {
	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool
}

// Move is an uncommitted client movement. Handlers may edit To; the host
// preserves Player and From and validates the resulting destination again.
type Move struct {
	Player   string
	From, To Position
}

// MoveHandler runs synchronously on the authoritative action owner. Return
// false to cancel the action and stop further handlers. Do not retain proposal
// pointers or block on other server actions; use the plugin API's queued
// enforcement requests for mutations. Context cancellation is lifecycle-only.
type MoveHandler func(context.Context, *Move) bool

type moveHook struct {
	id       uint64
	priority int
	ctx      context.Context
	fn       MoveHandler
}

// Hooks separates lossless before-commit decisions from the lossy event bus.
// Lower priorities run first; ties run in registration order. A panic cancels
// the action. Close rejects future dispatch and waits for in-flight handlers.
type Hooks struct {
	root        *Hooks
	scope       context.Context
	scopeCancel context.CancelFunc
	mu          sync.Mutex
	next        uint64
	moves       []moveHook
	closed      bool
	running     sync.WaitGroup
}

func NewHooks() *Hooks { return &Hooks{} }

func (h *Hooks) withContext(ctx context.Context) *Hooks {
	scoped, cancel := context.WithCancel(ctx)
	return &Hooks{root: h, scope: scoped, scopeCancel: cancel}
}

// OnMove subscribes until unsubscribe, context cancellation, or Close.
func (h *Hooks) OnMove(ctx context.Context, priority int, fn MoveHandler) func() {
	if h.root != nil {
		if h.scope.Err() != nil || fn == nil {
			return func() {}
		}
		lifetime, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(h.scope, cancel)
		unsubscribe := h.root.OnMove(lifetime, priority, func(action context.Context, move *Move) bool {
			if h.scope.Err() != nil || ctx.Err() != nil {
				return true
			}
			return fn(action, move)
		})
		return func() { stop(); cancel(); unsubscribe() }
	}

	h.mu.Lock()
	if h.closed || ctx.Err() != nil || fn == nil {
		h.mu.Unlock()
		return func() {}
	}
	h.next++
	id := h.next
	h.moves = append(h.moves, moveHook{id, priority, ctx, fn})
	sort.SliceStable(h.moves, func(i, j int) bool { return h.moves[i].priority < h.moves[j].priority })
	h.mu.Unlock()
	remove := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, entry := range h.moves {
			if entry.id == id {
				h.moves = append(h.moves[:i], h.moves[i+1:]...)
				break
			}
		}
	}
	stop := context.AfterFunc(ctx, remove)
	return func() { stop(); remove() }
}

// BeforeMove is called by the host before committing canonical movement.
// Dispatch captures the active subscriptions; changes affect the next action.
func (h *Hooks) BeforeMove(ctx context.Context, proposal Move) (result Move, accepted bool) {
	if h.root != nil {
		if h.scope.Err() != nil {
			return proposal, false
		}
		return h.root.BeforeMove(ctx, proposal)
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return proposal, false
	}
	callbacks := append([]moveHook(nil), h.moves...)
	h.running.Add(1)
	h.mu.Unlock()
	defer h.running.Done()
	result, accepted = proposal, true
	defer func() {
		if recover() != nil {
			result, accepted = proposal, false
		}
	}()
	for _, callback := range callbacks {
		if ctx.Err() != nil {
			return proposal, false
		}
		if callback.ctx.Err() != nil {
			continue
		}
		if !invokeMoveHandler(ctx, callback, &result) {
			return proposal, false
		}
		result.Player, result.From = proposal.Player, proposal.From
	}
	return result, true
}

// invokeMoveHandler delivers both action and subscription cancellation without
// leaving a goroutine waiting on either context after a completed callback.
func invokeMoveHandler(action context.Context, callback moveHook, move *Move) bool {
	ctx, cancel := context.WithCancel(action)
	stop := context.AfterFunc(callback.ctx, cancel)
	defer cancel()
	defer stop()
	return callback.fn(ctx, move)
}

// Close must be called outside a handler, after the host stops accepting actions.
func (h *Hooks) Close() {
	if h.root != nil {
		h.scopeCancel()
		return
	}
	h.mu.Lock()
	h.closed = true
	h.moves = nil
	h.mu.Unlock()
	h.running.Wait()
}
