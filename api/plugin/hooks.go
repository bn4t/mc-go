package plugin

import (
	"context"
	"errors"
	"sort"
	"sync"

	"mc-go/api/player"
)

var ErrHooksClosed = errors.New("plugin: hooks closed")

// Position is a protocol-neutral player pose.
type Position struct {
	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool
}

// Move is an uncommitted client movement. Handlers may edit To; the host
// preserves Player and From and validates the resulting destination again.
type Move struct {
	ID       player.ID
	Player   string
	From, To Position
}

// MoveHandler runs synchronously on the authoritative action owner. Return
// false to cancel the action and stop further handlers. Do not retain proposal
// pointers or block on other server actions; use the plugin API's queued
// enforcement requests for mutations. Cancellation during a callback rejects
// that action; canceling an idle subscription removes it from future actions.
type MoveHandler func(context.Context, *Move) bool

type moveHook struct {
	id       uint64
	priority int
	ctx      context.Context
	fn       MoveHandler
	plugin   string
}

// Hooks separates lossless before-commit decisions from the lossy event bus.
// Lower priorities run first; ties run in registration order. A panic cancels
// the action. Close rejects future dispatch and waits for in-flight handlers.
type Hooks struct {
	root        *Hooks
	scope       context.Context
	scopeCancel context.CancelFunc
	plugin      string
	mu          sync.Mutex
	next        uint64
	moves       []moveHook
	admissions  []admissionHook
	blockEdits  []blockEditHook
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
	return h.onMove(ctx, priority, fn, h.plugin)
}

func (h *Hooks) onMove(ctx context.Context, priority int, fn MoveHandler, owner string) func() {
	if h.root != nil {
		if h.scope.Err() != nil || fn == nil {
			return func() {}
		}
		lifetime, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(h.scope, cancel)
		unsubscribe := h.root.onMove(lifetime, priority, func(action context.Context, move *Move) bool {
			if h.scope.Err() != nil || ctx.Err() != nil {
				return true
			}
			accepted := fn(action, move)
			return accepted && h.scope.Err() == nil && ctx.Err() == nil
		}, owner)
		return func() { stop(); cancel(); unsubscribe() }
	}

	h.mu.Lock()
	if h.closed || ctx.Err() != nil || fn == nil {
		h.mu.Unlock()
		return func() {}
	}
	h.next++
	id := h.next
	h.moves = append(h.moves, moveHook{id: id, priority: priority, ctx: ctx, fn: fn, plugin: owner})
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
	result, accepted, _ = h.BeforeMoveE(ctx, proposal)
	return
}

// BeforeMoveE additionally identifies cancellation and contained callback panics.
// A false result with no error is an intentional policy veto.
func (h *Hooks) BeforeMoveE(ctx context.Context, proposal Move) (result Move, accepted bool, err error) {
	if h.root != nil {
		if h.scope.Err() != nil {
			return proposal, false, h.scope.Err()
		}
		return h.root.BeforeMoveE(ctx, proposal)
	}
	h.mu.Lock()
	closed := h.closed
	if closed || ctx.Err() != nil {
		h.mu.Unlock()
		if closed {
			return proposal, false, ErrHooksClosed
		}
		return proposal, false, ctx.Err()
	}
	callbacks := append([]moveHook(nil), h.moves...)
	h.running.Add(1)
	h.mu.Unlock()
	defer h.running.Done()
	result, accepted = proposal, true
	for _, callback := range callbacks {
		if ctx.Err() != nil {
			return proposal, false, ctx.Err()
		}
		if callback.ctx.Err() != nil {
			continue
		}
		ok := false
		if err = callPlugin(callback.plugin, "movement hook", func() error {
			var callbackErr error
			ok, callbackErr = invokeMoveHandler(ctx, callback, &result)
			return callbackErr
		}); err != nil {
			return proposal, false, err
		}
		if !ok {
			return proposal, false, nil
		}
		result.ID, result.Player, result.From = proposal.ID, proposal.Player, proposal.From
	}
	if err = ctx.Err(); err != nil {
		return proposal, false, err
	}
	return result, true, nil
}

// invokeMoveHandler delivers both action and subscription cancellation without
// leaving a goroutine waiting on either context after a completed callback.
func invokeMoveHandler(action context.Context, callback moveHook, move *Move) (bool, error) {
	ctx, cancel := context.WithCancel(action)
	stop := context.AfterFunc(callback.ctx, cancel)
	defer cancel()
	defer stop()
	accepted := callback.fn(ctx, move)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := callback.ctx.Err(); err != nil {
		return false, err
	}
	return accepted, nil
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
	h.admissions = nil
	h.blockEdits = nil
	h.mu.Unlock()
	h.running.Wait()
}
