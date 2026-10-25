package server

import (
	"context"
	"fmt"
	"sync"

	"mc-go/api/plugin"
	"mc-go/api/worldview"
)

var _ plugin.BlockEditor = serverAPI{}
var _ plugin.LifecycleBlockEditor = serverAPI{}

// blockEditWork owns one buffered receipt. Cancellation resolves queued work
// immediately. Once the owner starts dispatch, it checks cancellation before
// its first mutation and completes the outcome itself; cancellation cannot race
// a successful commit into reporting a false canceled result.
type blockEditWork struct {
	mu       sync.Mutex
	running  bool
	done     bool
	out      chan worldview.EditOutcome
	cleanups []func()
}

func (w *blockEditWork) addCleanup(fn func()) {
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		fn()
		return
	}
	w.cleanups = append(w.cleanups, fn)
	w.mu.Unlock()
}

func (w *blockEditWork) claim() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return false
	}
	w.running = true
	return true
}

func (w *blockEditWork) finish(result worldview.EditResult, err error, queuedOnly bool) {
	w.mu.Lock()
	if w.done || queuedOnly && w.running {
		w.mu.Unlock()
		return
	}
	w.done = true
	cleanups := w.cleanups
	w.cleanups = nil
	w.out <- worldview.EditOutcome{Result: result, Err: err}
	close(w.out)
	w.mu.Unlock()
	for _, cleanup := range cleanups {
		cleanup()
	}
}

func (a serverAPI) SubmitBlockEdit(ctx context.Context, edit worldview.Edit) (worldview.EditReceipt, error) {
	return a.SubmitBlockEditContext(ctx, a.s.runtimeCtx, "", edit)
}

func (a serverAPI) SubmitBlockEditContext(ctx, lifetime context.Context, name string, request worldview.Edit) (worldview.EditReceipt, error) {
	edit := copyBlockEdit(request)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := lifetime.Err(); err != nil {
		return nil, err
	}
	if a.s.ShuttingDown() {
		return nil, worldview.ErrClosed
	}
	d, err := a.s.worldviewDimension(edit.Dimension)
	if err != nil {
		return nil, err
	}
	if err = validateWorldviewPosition(d, edit.Position); err != nil {
		return nil, err
	}
	if _, valid := a.s.BlockCatalog().Lookup(edit.State); !valid {
		return nil, fmt.Errorf("%w: unknown canonical target state", worldview.ErrRejected)
	}
	if edit.Expected != nil {
		if _, valid := a.s.BlockCatalog().Lookup(*edit.Expected); !valid {
			return nil, fmt.Errorf("%w: unknown canonical expected state", worldview.ErrRejected)
		}
	}
	bound, stopLifetime := bindActionLifetime(ctx, lifetime)
	bound, stopRuntime := bindActionLifetime(bound, a.s.runtimeCtx)
	work := &blockEditWork{out: make(chan worldview.EditOutcome, 1)}
	work.addCleanup(stopLifetime)
	work.addCleanup(stopRuntime)
	for _, cancellation := range []struct {
		ctx context.Context
		err func() error
	}{{ctx, ctx.Err}, {lifetime, lifetime.Err}, {a.s.runtimeCtx, func() error { return worldview.ErrClosed }}} {
		stop := context.AfterFunc(cancellation.ctx, func() {
			work.finish(worldview.EditResult{}, cancellation.err(), true)
		})
		work.addCleanup(func() { stop() })
	}
	accepted := a.s.queueSimulationActionWithDiscard(func() {
		if !work.claim() {
			return
		}
		result, err := a.s.editBlockOwned(bound, edit, "plugin:"+name)
		work.finish(result, err, false)
	}, func() {
		work.finish(worldview.EditResult{}, worldview.ErrClosed, true)
	})
	if !accepted {
		err := error(worldview.ErrLimit)
		if a.s.ShuttingDown() {
			err = worldview.ErrClosed
		}
		work.finish(worldview.EditResult{}, err, false)
		return nil, err
	}
	return worldview.EditReceipt(work.out), nil
}
