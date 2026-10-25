package plugin

import (
	"context"
	"errors"
	blockregistry "mc-go/api/registry"
	"reflect"
	"testing"
	"time"

	"mc-go/api/event"
	"mc-go/api/player"
)

type testAPI struct {
	bus      *event.Bus
	hooks    *Hooks
	commands *Commands
	requests int
}

func newTestAPI() *testAPI {
	return &testAPI{bus: event.New(), hooks: NewHooks(), commands: NewCommands()}
}
func (a *testAPI) BlockCatalog() *blockregistry.BlockCatalog       { return nil }
func (a *testAPI) Context() context.Context                        { return context.Background() }
func (a *testAPI) Bus() *event.Bus                                 { return a.bus }
func (a *testAPI) Hooks() *Hooks                                   { return a.hooks }
func (a *testAPI) Commands() *Commands                             { return a.commands }
func (a *testAPI) ServerInfo() Info                                { return Info{} }
func (a *testAPI) Kick(string, string) bool                        { a.requests++; return true }
func (a *testAPI) Teleport(string, float64, float64, float64) bool { a.requests++; return true }
func (a *testAPI) PlayerState(string) (PlayerState, bool)          { return PlayerState{}, false }
func (a *testAPI) ReportViolation(Violation)                       { a.requests++ }

type fixturePlugin struct {
	name  string
	meta  Metadata
	init  func(API) error
	close func() error
}

func (p fixturePlugin) Name() string       { return p.name }
func (p fixturePlugin) Metadata() Metadata { return p.meta }
func (p fixturePlugin) Init(a API) error {
	if p.init != nil {
		return p.init(a)
	}
	return nil
}
func (p fixturePlugin) Close() error {
	if p.close != nil {
		return p.close()
	}
	return nil
}

func TestMoveHooksOrderingModificationAndCancellation(t *testing.T) {
	h := NewHooks()
	var order []int
	h.OnMove(context.Background(), 10, func(_ context.Context, m *Move) bool { order = append(order, 2); m.To.X *= 2; return true })
	h.OnMove(context.Background(), 0, func(_ context.Context, m *Move) bool {
		order = append(order, 1)
		m.To.X += 3
		m.Player = "forged"
		m.ID = player.ID{99}
		m.From.X = -999
		return true
	})
	h.OnMove(context.Background(), 10, func(_ context.Context, m *Move) bool {
		order = append(order, 3)
		if m.Player != "actor" || m.ID != (player.ID{1}) || m.From.X != 4 {
			t.Fatal("immutable actor/source changed")
		}
		return true
	})
	proposal := Move{ID: player.ID{1}, Player: "actor", From: Position{X: 4}, To: Position{X: 1}}
	result, ok := h.BeforeMove(context.Background(), proposal)
	if !ok || result.To.X != 8 || !reflect.DeepEqual(order, []int{1, 2, 3}) {
		t.Fatalf("result=%+v accepted=%v order=%v", result, ok, order)
	}
	unsubscribe := h.OnMove(context.Background(), -1, func(context.Context, *Move) bool { return false })
	order = nil
	result, ok = h.BeforeMove(context.Background(), proposal)
	if ok || result != proposal || len(order) != 0 {
		t.Fatal("canceled action reached later handlers")
	}
	unsubscribe()
	unsubscribe()
	if _, ok = h.BeforeMove(context.Background(), proposal); !ok {
		t.Fatal("unsubscribe did not remove veto")
	}
}

func TestMoveHookLifetimeAndPanic(t *testing.T) {
	h := NewHooks()
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	h.OnMove(ctx, 0, func(context.Context, *Move) bool { called = true; return true })
	cancel()
	if _, ok := h.BeforeMove(context.Background(), Move{}); !ok || called {
		t.Fatal("canceled subscription invoked")
	}
	h.OnMove(context.Background(), 0, func(context.Context, *Move) bool { panic("bad plugin") })
	if _, ok := h.BeforeMove(context.Background(), Move{}); ok {
		t.Fatal("panic accepted action")
	}
}

func TestHooksCloseWaitsForInFlightAndRejectsNewActions(t *testing.T) {
	h := NewHooks()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h.OnMove(context.Background(), 0, func(context.Context, *Move) bool { close(entered); <-release; return true })
	go h.BeforeMove(context.Background(), Move{})
	<-entered
	go func() { h.Close(); close(done) }()
	select {
	case <-done:
		t.Fatal("Close returned with running handler")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked")
	}
	if _, ok := h.BeforeMove(context.Background(), Move{}); ok {
		t.Fatal("closed hooks accepted dispatch")
	}
}

func TestManagerOrderScopeInvalidationAndReverseClose(t *testing.T) {
	api := newTestAPI()
	var order []string
	var retained API
	invoked := 0
	plugins := []Plugin{
		fixturePlugin{name: "a", meta: Metadata{APIVersion: APIVersion, Dependencies: []string{"z"}}, init: func(a API) error {
			order = append(order, "a")
			retained = a
			a.Commands().RegisterContext(context.Background(), "owned", func(string, []string) string { return "ok" })
			a.Hooks().OnMove(context.Background(), 0, func(context.Context, *Move) bool { invoked++; return true })
			return nil
		}, close: func() error { order = append(order, "close-a"); return nil }},
		fixturePlugin{name: "z", init: func(API) error { order = append(order, "z"); return nil }, close: func() error { order = append(order, "close-z"); return nil }},
	}
	manager, err := NewManager(context.Background(), api, plugins)
	if err != nil {
		t.Fatal(err)
	}
	api.hooks.BeforeMove(context.Background(), Move{})
	if invoked != 1 {
		t.Fatal("live hook not invoked")
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"z", "a", "close-a", "close-z"}) {
		t.Fatal(order)
	}
	if retained.Context().Err() == nil {
		t.Fatal("plugin context still active")
	}
	if _, ok := api.commands.Run("owned", "", nil); ok {
		t.Fatal("closed plugin command ran")
	}
	retained.Commands().WithContext(context.Background()).Register("late", func(string, []string) string { return "bad" })
	if _, ok := api.commands.Run("late", "", nil); ok {
		t.Fatal("expired scope registered command")
	}
	retained.Hooks().OnMove(context.Background(), 0, func(context.Context, *Move) bool { invoked++; return true })
	api.hooks.BeforeMove(context.Background(), Move{})
	if invoked != 1 {
		t.Fatal("closed plugin hook ran")
	}
	if retained.Kick("x", "y") || retained.Teleport("x", 1, 2, 3) {
		t.Fatal("closed plugin enforcement accepted")
	}
	retained.ReportViolation(Violation{})
	if api.requests != 0 {
		t.Fatal("closed scope reached host")
	}
	// Closing a scoped view cannot destroy other plugins' shared resources.
	retained.Hooks().Close()
	retained.Bus().Close()
	if _, ok := api.hooks.BeforeMove(context.Background(), Move{}); !ok {
		t.Fatal("scope closed global hooks")
	}
	ch, unsub := api.bus.Subscribe("live", 1)
	defer unsub()
	api.bus.Publish("live", 1)
	if (<-ch).Data != 1 {
		t.Fatal("scope closed global bus")
	}
}

func TestManagerValidationAndInitRollback(t *testing.T) {
	for name, plugins := range map[string][]Plugin{
		"version":   {fixturePlugin{name: "bad", meta: Metadata{APIVersion: APIVersion + 1}}},
		"missing":   {fixturePlugin{name: "bad", meta: Metadata{Dependencies: []string{"absent"}}}},
		"cycle":     {fixturePlugin{name: "a", meta: Metadata{Dependencies: []string{"b"}}}, fixturePlugin{name: "b", meta: Metadata{Dependencies: []string{"a"}}}},
		"duplicate": {fixturePlugin{name: "a"}, fixturePlugin{name: "a"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewManager(context.Background(), newTestAPI(), plugins); err == nil {
				t.Fatal("invalid set accepted")
			}
		})
	}
	api := newTestAPI()
	var failedScope API
	closed := false
	_, err := NewManager(context.Background(), api, []Plugin{fixturePlugin{name: "fail", init: func(a API) error {
		failedScope = a
		a.Commands().Register("partial", func(string, []string) string { return "bad" })
		a.Hooks().OnMove(context.Background(), 0, func(context.Context, *Move) bool { t.Error("failed Init hook ran"); return true })
		return errors.New("initialization failed")
	}, close: func() error { closed = true; return nil }}})
	if err == nil || !closed || failedScope.Context().Err() == nil {
		t.Fatal("Init rollback did not close/cancel")
	}
	if _, ok := api.commands.Run("partial", "", nil); ok {
		t.Fatal("partial command survived")
	}
	api.hooks.BeforeMove(context.Background(), Move{})
}

// Cancellation reaches a callback that is already running, not only future
// dispatch. Trusted handlers must still return; Go callbacks cannot be forced.
func TestMoveHookActiveCallbackReceivesSubscriptionCancellation(t *testing.T) {
	h := NewHooks()
	lifetime, cancel := context.WithCancel(context.Background())
	entered, finished := make(chan struct{}), make(chan bool, 1)
	h.OnMove(lifetime, 0, func(ctx context.Context, _ *Move) bool {
		close(entered)
		<-ctx.Done()
		return false
	})
	go func() { _, accepted := h.BeforeMove(context.Background(), Move{}); finished <- accepted }()
	<-entered
	cancel()
	select {
	case accepted := <-finished:
		if accepted {
			t.Fatal("canceled callback accepted action")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription cancellation did not reach active callback")
	}
	h.Close()
}
