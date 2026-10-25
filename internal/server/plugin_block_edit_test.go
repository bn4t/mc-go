package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"mc-go/api/plugin"
	"mc-go/api/worldview"
	"mc-go/internal/world"
)

func editReceiptOutcome(t *testing.T, receipt worldview.EditReceipt) worldview.EditOutcome {
	t.Helper()
	select {
	case outcome, ok := <-receipt:
		if !ok {
			t.Fatal("receipt closed without outcome")
		}
		select {
		case _, more := <-receipt:
			if more {
				t.Fatal("receipt completed more than once")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("receipt did not close after its outcome")
		}
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("accepted receipt never completed")
		return worldview.EditOutcome{}
	}
}

func TestPluginBlockEditReceiptCompletesEveryTerminalPath(t *testing.T) {
	for _, mode := range []string{"success", "veto", "cancel", "unload", "discard", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			s := simulationTestServer(t)
			t.Cleanup(s.Stop)
			extension := &lifecyclePlugin{}
			manager, err := plugin.NewManager(context.Background(), serverAPI{s}, []plugin.Plugin{extension})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = manager.Close() })
			editor, ok := extension.api.(plugin.BlockEditor)
			if !ok {
				t.Fatal("missing optional block editor")
			}
			if _, ok := extension.api.(plugin.PlayerControl); !ok {
				t.Fatal("adding editor removed existing optional controls")
			}
			remove := s.hooks.OnBlockEdit(context.Background(), 0, func(_ context.Context, edit *plugin.BlockEdit) bool {
				if edit.Origin != "plugin:lifetime-fixture" {
					t.Errorf("caller spoofed host origin: %q", edit.Origin)
				}
				return mode != "veto"
			})
			defer remove()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := blockEditKey(t, "air")
			receipt, err := editor.SubmitBlockEdit(ctx, worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{X: 2, Y: 10, Z: 2}, State: blockEditKey(t, "stone"), Expected: &expected})
			if err != nil || receipt == nil {
				t.Fatalf("submission=%v %v", receipt, err)
			}
			// Caller-owned CAS storage must not remain aliased after acceptance.
			expected = blockEditKey(t, "stone")
			wantErr := error(nil)
			switch mode {
			case "cancel":
				cancel()
				wantErr = context.Canceled
			case "unload":
				if err := manager.Close(); err != nil {
					t.Fatal(err)
				}
				wantErr = context.Canceled
			case "discard":
				s.discardSimulationActions()
				wantErr = worldview.ErrClosed
			case "shutdown":
				s.Stop()
				wantErr = worldview.ErrClosed
			case "veto":
				wantErr = worldview.ErrRejected
				s.simulationAction(func() {})
			default:
				s.simulationAction(func() {})
			}
			outcome := editReceiptOutcome(t, receipt)
			if !errors.Is(outcome.Err, wantErr) || outcome.Result.Applied != (mode == "success") {
				t.Fatalf("terminal outcome=%+v wantErr=%v", outcome, wantErr)
			}
			if mode != "shutdown" {
				s.simulationAction(func() {})
			}
			want := world.Air
			if mode == "success" {
				want = world.Stone
			}
			if got := s.world.GetBlock(world.Pos{X: 2, Y: 10, Z: 2}); got != want {
				t.Fatalf("canceled/rejected work wrote state %d want %d", got, want)
			}
		})
	}
}

func TestPluginBlockEditImmediateRejectionHasNoReceipt(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	api := serverAPI{s}
	request := worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{Y: 10}, State: blockEditKey(t, "stone")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if receipt, err := api.SubmitBlockEdit(ctx, request); receipt != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled immediate rejection=%v %v", receipt, err)
	}
	for i := 0; i < maxPendingSimulationActions; i++ {
		if !s.queueSimulationAction(func() {}) {
			t.Fatal("queue failed before bound")
		}
	}
	if receipt, err := api.SubmitBlockEdit(context.Background(), request); receipt != nil || !errors.Is(err, worldview.ErrLimit) {
		t.Fatalf("queue-full immediate rejection=%v %v", receipt, err)
	}
	s.discardSimulationActions()
}

func TestPluginBlockEditCancellationInsideRunningHook(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	remove := s.hooks.OnBlockEdit(context.Background(), 0, func(ctx context.Context, _ *plugin.BlockEdit) bool {
		close(entered)
		<-ctx.Done()
		return true
	})
	defer remove()
	receipt, err := (serverAPI{s}).SubmitBlockEdit(ctx, worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{Y: 10}, State: blockEditKey(t, "stone")})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.simulationAction(func() {}); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("edit hook did not start")
	}
	cancel()
	if outcome := editReceiptOutcome(t, receipt); !errors.Is(outcome.Err, context.Canceled) || outcome.Result.Applied {
		t.Fatalf("running canceled edit=%+v", outcome)
	}
	<-done
	if s.world.GetBlock(world.Pos{Y: 10}) != world.Air {
		t.Fatal("cancellation inside hook allowed a write")
	}
}

func TestPluginBlockEditClaimPreservesSuccessfulOutcomeAfterMutation(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	work := &blockEditWork{out: make(chan worldview.EditOutcome, 1)}
	callbackFinished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		work.finish(worldview.EditResult{}, ctx.Err(), true)
		close(callbackFinished)
	})
	defer stop()
	s.simulationAction(func() {
		if !work.claim() {
			t.Fatal("owner could not claim edit")
		}
		s.world.SetBlock(world.Pos{Y: 10}, world.Stone)
		cancel()
		<-callbackFinished // callback cannot acquire or wait for ownership
		work.finish(worldview.EditResult{Applied: true, Before: world.Air, After: world.Stone}, nil, false)
	})
	if outcome := editReceiptOutcome(t, worldview.EditReceipt(work.out)); outcome.Err != nil || !outcome.Result.Applied || outcome.Result.After != world.Stone {
		t.Fatalf("late cancellation rewrote committed success: %+v", outcome)
	}
}

func TestPluginBlockEditStopCompletesRunningReceipt(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	entered := make(chan struct{})
	s.hooks.OnBlockEdit(context.Background(), 0, func(ctx context.Context, _ *plugin.BlockEdit) bool {
		close(entered)
		<-ctx.Done()
		return true
	})
	receipt, err := (serverAPI{s}).SubmitBlockEdit(context.Background(), worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{Y: 10}, State: blockEditKey(t, "stone")})
	if err != nil {
		t.Fatal(err)
	}
	ownedDone := make(chan struct{})
	go func() { s.simulationAction(func() {}); close(ownedDone) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("edit did not claim owner")
	}
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	outcome := editReceiptOutcome(t, receipt)
	if outcome.Result.Applied || !(errors.Is(outcome.Err, context.Canceled) || errors.Is(outcome.Err, worldview.ErrClosed)) {
		t.Fatalf("running edit survived shutdown: %+v", outcome)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown failed to join cooperative callback")
	}
	<-ownedDone
	if s.world.GetBlock(world.Pos{Y: 10}) != world.Air {
		t.Fatal("shutdown allowed canceled edit")
	}
}

func TestPluginBlockEditCanSubmitFromHookAndReceiptsDoNotClaimStreamRevision(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	api := serverAPI{s}
	request := worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{Y: 10}, State: blockEditKey(t, "stone")}
	var second worldview.EditReceipt
	remove := s.hooks.OnBlockEdit(context.Background(), 0, func(_ context.Context, edit *plugin.BlockEdit) bool {
		if edit.Target == request.State {
			next := request
			next.State = blockEditKey(t, "dirt")
			var err error
			second, err = api.SubmitBlockEdit(context.Background(), next)
			if err != nil {
				t.Error(err)
			}
		}
		return true
	})
	defer remove()
	first, err := api.SubmitBlockEdit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	s.simulationAction(func() {})
	if outcome := editReceiptOutcome(t, first); outcome.Err != nil || outcome.Result.After != world.Stone || second == nil {
		t.Fatalf("callback-safe submission failed: %+v", outcome)
	}
	s.simulationAction(func() {})
	if outcome := editReceiptOutcome(t, second); outcome.Err != nil || outcome.Result.Before != world.Stone || outcome.Result.After != world.Dirt {
		t.Fatalf("later receipt lost its own boundary: %+v", outcome)
	}
}
