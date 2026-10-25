package plugin

import (
	"context"
	"sort"

	"mc-go/api/player"
)

// AdmissionDecision is deny-only: its zero value permits admission. Reason is
// plain text, suitable for the frontend to present using its own codec.
type AdmissionDecision struct {
	Denied bool
	Reason string
}

// AdmissionHandler receives a copied, authenticated profile before any player
// replacement or actor creation. Identity cannot be modified. Handlers run on
// the action owner and must return promptly, like movement handlers.
type AdmissionHandler func(context.Context, player.Profile) AdmissionDecision

type admissionHook struct {
	id       uint64
	priority int
	ctx      context.Context
	fn       AdmissionHandler
	plugin   string
}

// OnAdmission installs a synchronous admission policy until cancellation,
// unsubscribe or Close. Lower priority values run first; ties preserve order.
func (h *Hooks) OnAdmission(ctx context.Context, priority int, fn AdmissionHandler) func() {
	return h.onAdmission(ctx, priority, fn, h.plugin)
}

func (h *Hooks) onAdmission(ctx context.Context, priority int, fn AdmissionHandler, owner string) func() {
	if h.root != nil {
		if h.scope.Err() != nil || fn == nil {
			return func() {}
		}
		lifetime, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(h.scope, cancel)
		unsubscribe := h.root.onAdmission(lifetime, priority, func(action context.Context, profile player.Profile) AdmissionDecision {
			if h.scope.Err() != nil || ctx.Err() != nil {
				return AdmissionDecision{}
			}
			decision := fn(action, profile)
			if h.scope.Err() != nil || ctx.Err() != nil {
				return AdmissionDecision{Denied: true}
			}
			return decision
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
	h.admissions = append(h.admissions, admissionHook{id: id, priority: priority, ctx: ctx, fn: fn, plugin: owner})
	sort.SliceStable(h.admissions, func(i, j int) bool { return h.admissions[i].priority < h.admissions[j].priority })
	h.mu.Unlock()
	remove := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, entry := range h.admissions {
			if entry.id == id {
				h.admissions = append(h.admissions[:i], h.admissions[i+1:]...)
				break
			}
		}
	}
	stop := context.AfterFunc(ctx, remove)
	return func() { stop(); remove() }
}

// BeforeAdmission is called by the host before changing lifecycle state.
// Cancellation or a callback panic rejects admission with an error. A policy
// denial has Denied=true and no error. The host must recheck its own commit
// conditions after dispatch; hooks do not reserve capacity or mutate players.
func (h *Hooks) BeforeAdmission(ctx context.Context, profile player.Profile) (decision AdmissionDecision, err error) {
	if h.root != nil {
		if err = h.scope.Err(); err != nil {
			return AdmissionDecision{Denied: true}, err
		}
		return h.root.BeforeAdmission(ctx, profile)
	}
	h.mu.Lock()
	closed := h.closed
	if closed || ctx.Err() != nil {
		h.mu.Unlock()
		if closed {
			return AdmissionDecision{Denied: true}, ErrHooksClosed
		}
		return AdmissionDecision{Denied: true}, ctx.Err()
	}
	callbacks := append([]admissionHook(nil), h.admissions...)
	h.running.Add(1)
	h.mu.Unlock()
	defer h.running.Done()
	for _, callback := range callbacks {
		if err = ctx.Err(); err != nil {
			return AdmissionDecision{Denied: true}, err
		}
		if callback.ctx.Err() != nil {
			continue
		}
		err = callPlugin(callback.plugin, "admission hook", func() error {
			lifetime, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(callback.ctx, cancel)
			defer cancel()
			defer stop()
			decision = callback.fn(lifetime, profile)
			if err := lifetime.Err(); err != nil {
				return err
			}
			return callback.ctx.Err()
		})
		if err != nil {
			return AdmissionDecision{Denied: true}, err
		}
		if err = ctx.Err(); err != nil {
			return AdmissionDecision{Denied: true}, err
		}
		if decision.Denied {
			return decision, nil
		}
	}
	if err = ctx.Err(); err != nil {
		return AdmissionDecision{Denied: true}, err
	}
	return AdmissionDecision{}, nil
}
