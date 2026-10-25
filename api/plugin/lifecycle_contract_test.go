package plugin

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"mc-go/api/player"
)

func TestAdmissionPoliciesUseCopiedIdentityAndDeterministicVeto(t *testing.T) {
	h := NewHooks()
	defer h.Close()
	profile := player.Profile{Name: "authenticated", UUID: [16]byte{1}}
	var order []int
	h.OnAdmission(context.Background(), 5, func(_ context.Context, got player.Profile) AdmissionDecision {
		order = append(order, 2)
		if got != profile {
			t.Fatal("earlier handler changed identity")
		}
		return AdmissionDecision{Denied: true, Reason: "maintenance"}
	})
	h.OnAdmission(context.Background(), -1, func(_ context.Context, got player.Profile) AdmissionDecision {
		order = append(order, 1)
		got.Name = "forged"
		return AdmissionDecision{}
	})
	h.OnAdmission(context.Background(), 5, func(context.Context, player.Profile) AdmissionDecision {
		order = append(order, 3)
		return AdmissionDecision{}
	})
	decision, err := h.BeforeAdmission(context.Background(), profile)
	if err != nil || !decision.Denied || decision.Reason != "maintenance" || !reflect.DeepEqual(order, []int{1, 2}) {
		t.Fatalf("decision=%+v err=%v order=%v", decision, err, order)
	}
}

func TestActionCancellationRejectsEmptyAndFinalHookDispatch(t *testing.T) {
	for _, handlers := range []bool{false, true} {
		h := NewHooks()
		ctx, cancel := context.WithCancel(context.Background())
		if handlers {
			h.OnMove(context.Background(), 0, func(context.Context, *Move) bool { cancel(); return true })
		} else {
			cancel()
		}
		if _, accepted, err := h.BeforeMoveE(ctx, Move{}); accepted || !errors.Is(err, context.Canceled) {
			t.Fatalf("move handlers=%v accepted=%v err=%v", handlers, accepted, err)
		}
		h.Close()
		h = NewHooks()
		ctx, cancel = context.WithCancel(context.Background())
		if handlers {
			h.OnAdmission(context.Background(), 0, func(context.Context, player.Profile) AdmissionDecision { cancel(); return AdmissionDecision{} })
		} else {
			cancel()
		}
		if decision, err := h.BeforeAdmission(ctx, player.Profile{}); !decision.Denied || !errors.Is(err, context.Canceled) {
			t.Fatalf("admission handlers=%v decision=%+v err=%v", handlers, decision, err)
		}
		h.Close()
	}
}

func TestScopedAdmissionCancellationAndPanicReject(t *testing.T) {
	api := newTestAPI()
	var retained API
	manager, err := NewManager(context.Background(), api, []Plugin{fixturePlugin{name: "policy", init: func(a API) error {
		retained = a
		a.Hooks().OnAdmission(context.Background(), 0, func(context.Context, player.Profile) AdmissionDecision { panic("private detail") })
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := api.hooks.BeforeAdmission(context.Background(), player.Profile{})
	var fault *CallbackError
	if !decision.Denied || !errors.As(err, &fault) || fault.Plugin != "policy" || fault.Phase != "admission hook" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	retained.Hooks().OnAdmission(context.Background(), 0, func(context.Context, player.Profile) AdmissionDecision {
		t.Error("expired admission ran")
		return AdmissionDecision{Denied: true}
	})
	if decision, err = api.hooks.BeforeAdmission(context.Background(), player.Profile{}); decision.Denied || err != nil {
		t.Fatalf("expired policy survived: %+v %v", decision, err)
	}
}

func TestScopedMovementPanicRejectsAndIdentifiesFault(t *testing.T) {
	api := newTestAPI()
	manager, err := NewManager(context.Background(), api, []Plugin{fixturePlugin{name: "movement-policy", init: func(a API) error {
		a.Hooks().OnMove(context.Background(), 0, func(_ context.Context, move *Move) bool { move.To.X = 99; panic("private movement detail") })
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	api.hooks.OnMove(context.Background(), 1, func(context.Context, *Move) bool { t.Error("movement continued after panic"); return true })
	proposal := Move{ID: player.ID{1}, Player: "actor", To: Position{X: 1}}
	result, accepted, err := api.hooks.BeforeMoveE(context.Background(), proposal)
	var fault *CallbackError
	if accepted || result != proposal || !errors.As(err, &fault) || fault.Plugin != "movement-policy" || fault.Phase != "movement hook" {
		t.Fatalf("result=%+v accepted=%v err=%v", result, accepted, err)
	}
}

func TestCanceledActiveSubscriptionCannotCommitAllowedDecision(t *testing.T) {
	h := NewHooks()
	defer h.Close()
	ctx, cancel := context.WithCancel(context.Background())
	h.OnMove(ctx, 0, func(context.Context, *Move) bool { cancel(); return true })
	if _, accepted, err := h.BeforeMoveE(context.Background(), Move{}); accepted || !errors.Is(err, context.Canceled) {
		t.Fatalf("expired movement decision: accepted=%v err=%v", accepted, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	h.OnAdmission(ctx, 0, func(context.Context, player.Profile) AdmissionDecision { cancel(); return AdmissionDecision{} })
	if decision, err := h.BeforeAdmission(context.Background(), player.Profile{}); !decision.Denied || !errors.Is(err, context.Canceled) {
		t.Fatalf("expired admission decision: decision=%+v err=%v", decision, err)
	}
	api := newTestAPI()
	manager, err := NewManager(context.Background(), api, []Plugin{fixturePlugin{name: "scoped", init: func(a API) error {
		a.Hooks().OnAdmission(context.Background(), 0, func(context.Context, player.Profile) AdmissionDecision {
			a.Hooks().Close()
			return AdmissionDecision{}
		})
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if decision, _ := api.hooks.BeforeAdmission(context.Background(), player.Profile{}); !decision.Denied {
		t.Fatal("closed plugin scope accepted active decision")
	}
}

func TestPluginInitPanicRollsBackAndClosePanicContinues(t *testing.T) {
	api := newTestAPI()
	var order []string
	var retained API
	closeError := errors.New("close returned error")
	_, err := NewManager(context.Background(), api, []Plugin{
		fixturePlugin{name: "a", init: func(a API) error {
			retained = a
			order = append(order, "a")
			a.Commands().Register("partial", func(string, []string) string { t.Error("rolled back command ran"); return "" })
			return nil
		}, close: func() error { order = append(order, "close-a"); return closeError }},
		fixturePlugin{name: "b", init: func(API) error { order = append(order, "b"); panic("init failure") }, close: func() error { order = append(order, "close-b"); panic("close failure") }},
		fixturePlugin{name: "c", init: func(API) error { t.Error("initialized after panic"); return nil }},
	})
	var fault *CallbackError
	if !errors.As(err, &fault) || fault.Plugin != "b" || fault.Phase != "init" || !errors.Is(err, closeError) || !strings.Contains(err.Error(), "close failure") {
		t.Fatalf("missing attributed joined failure: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"a", "b", "close-b", "close-a"}) || retained.Context().Err() == nil {
		t.Fatalf("rollback order=%v lifetime=%v", order, retained.Context().Err())
	}
	if _, found := api.commands.Run("partial", "", nil); found {
		t.Fatal("partial command survived panic")
	}
}

func TestCommandPanicIsFoundAndNextCommandStillRuns(t *testing.T) {
	api := newTestAPI()
	cause := errors.New("sensitive diagnostic")
	manager, err := NewManager(context.Background(), api, []Plugin{fixturePlugin{name: "commands", init: func(a API) error {
		a.Commands().Register("broken", func(string, []string) string { panic(cause) })
		a.Commands().Register("healthy", func(string, []string) string { return "ok" })
		return nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	_, found, err := api.commands.RunE("broken", "", nil)
	var fault *CallbackError
	if !found || !errors.As(err, &fault) || fault.Plugin != "commands" || !errors.Is(err, cause) {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if out, found, err := api.commands.RunE("healthy", "", nil); out != "ok" || !found || err != nil {
		t.Fatalf("after panic: %q %v %v", out, found, err)
	}
}

func TestManagerCancellationDuringInitRollsBackBeforeNextPlugin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := false
	_, err := NewManager(ctx, newTestAPI(), []Plugin{
		fixturePlugin{name: "first", init: func(API) error { cancel(); return nil }, close: func() error { closed = true; return nil }},
		fixturePlugin{name: "later", init: func(API) error { t.Error("initialized after cancellation"); return nil }},
	})
	if !errors.Is(err, context.Canceled) || !closed {
		t.Fatalf("canceled initialization did not roll back: closed=%v err=%v", closed, err)
	}
}
