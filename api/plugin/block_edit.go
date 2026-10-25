package plugin

import (
	"context"
	"sort"

	blockregistry "mc-go/api/registry"
	"mc-go/api/worldview"
)

// BlockEditor is an optional privileged API capability. Submission never waits
// for simulation ownership and is safe inside hooks. Acceptance is not application:
// read the single receipt outcome outside the hook to learn the committed result.
// The host shares a queue of at most 256 pending actions with enforcement;
// saturation rejects without creating a receipt. Neighbor side effects are not
// an atomic batch or bounded to the requested cell.
type BlockEditor interface {
	SubmitBlockEdit(context.Context, worldview.Edit) (worldview.EditReceipt, error)
}

// LifecycleBlockEditor is the host capability used by scoped plugin views.
// Hosts retain both contexts and assign origin from the manager-provided name.
type LifecycleBlockEditor interface {
	SubmitBlockEditContext(request, lifetime context.Context, plugin string, edit worldview.Edit) (worldview.EditReceipt, error)
}

func (a scopedAPI) submitBlockEdit(ctx context.Context, edit worldview.Edit) (worldview.EditReceipt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.ctx.Err(); err != nil {
		return nil, err
	}
	if edit.Expected != nil {
		expected := *edit.Expected
		edit.Expected = &expected
	}
	host, ok := a.API.(LifecycleBlockEditor)
	if !ok {
		return nil, worldview.ErrRejected
	}
	return host.SubmitBlockEditContext(ctx, a.ctx, a.name, edit)
}

func (a editingAPI) SubmitBlockEdit(ctx context.Context, edit worldview.Edit) (worldview.EditReceipt, error) {
	return a.submitBlockEdit(ctx, edit)
}
func (a controlledEditingAPI) SubmitBlockEdit(ctx context.Context, edit worldview.Edit) (worldview.EditReceipt, error) {
	return a.submitBlockEdit(ctx, edit)
}

// BlockEdit describes only the typed privileged edit entry point. It is not a
// player dig/place event or a hook for every physics write. Only Target may be
// modified; the host preserves origin, dimension, position and Before, resolves
// the final complete state key and rechecks all commit conditions after hooks.
type BlockEdit struct {
	Origin    string
	Dimension string
	Position  worldview.Pos
	Before    blockregistry.StateKey
	Target    blockregistry.StateKey
}

type BlockEditHandler func(context.Context, *BlockEdit) bool

type blockEditHook struct {
	id       uint64
	priority int
	ctx      context.Context
	fn       BlockEditHandler
	plugin   string
}

func (h *Hooks) OnBlockEdit(ctx context.Context, priority int, fn BlockEditHandler) func() {
	return h.onBlockEdit(ctx, priority, fn, h.plugin)
}

func (h *Hooks) onBlockEdit(ctx context.Context, priority int, fn BlockEditHandler, owner string) func() {
	if h.root != nil {
		if h.scope.Err() != nil || fn == nil {
			return func() {}
		}
		lifetime, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(h.scope, cancel)
		unsubscribe := h.root.onBlockEdit(lifetime, priority, func(action context.Context, edit *BlockEdit) bool {
			if h.scope.Err() != nil || ctx.Err() != nil {
				return true
			}
			accepted := fn(action, edit)
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
	h.blockEdits = append(h.blockEdits, blockEditHook{id: id, priority: priority, ctx: ctx, fn: fn, plugin: owner})
	sort.SliceStable(h.blockEdits, func(i, j int) bool { return h.blockEdits[i].priority < h.blockEdits[j].priority })
	h.mu.Unlock()
	remove := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, entry := range h.blockEdits {
			if entry.id == id {
				h.blockEdits = append(h.blockEdits[:i], h.blockEdits[i+1:]...)
				break
			}
		}
	}
	stop := context.AfterFunc(ctx, remove)
	return func() { stop(); remove() }
}

// BeforeBlockEdit dispatches policy before any write or write side effect.
// A false result with no error is a deliberate veto; cancellation and contained
// callback panics return errors. Handlers must return promptly, never retain the
// proposal pointer, and never wait for an edit receipt on the action owner.
func (h *Hooks) BeforeBlockEdit(ctx context.Context, proposal BlockEdit) (BlockEdit, bool, error) {
	if h.root != nil {
		if err := h.scope.Err(); err != nil {
			return proposal, false, err
		}
		return h.root.BeforeBlockEdit(ctx, proposal)
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
	callbacks := append([]blockEditHook(nil), h.blockEdits...)
	h.running.Add(1)
	h.mu.Unlock()
	defer h.running.Done()
	result := proposal
	for _, callback := range callbacks {
		if err := ctx.Err(); err != nil {
			return proposal, false, err
		}
		if callback.ctx.Err() != nil {
			continue
		}
		accepted := false
		err := callPlugin(callback.plugin, "block edit hook", func() error {
			lifetime, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(callback.ctx, cancel)
			defer cancel()
			defer stop()
			accepted = callback.fn(lifetime, &result)
			if err := lifetime.Err(); err != nil {
				return err
			}
			return callback.ctx.Err()
		})
		if err != nil || !accepted {
			return proposal, false, err
		}
		result.Origin, result.Dimension, result.Position, result.Before = proposal.Origin, proposal.Dimension, proposal.Position, proposal.Before
	}
	if err := ctx.Err(); err != nil {
		return proposal, false, err
	}
	return result, true, nil
}
