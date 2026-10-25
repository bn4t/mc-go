package server

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/api/frontend"
	"mc-go/api/player"
)

type panickingAvatarFrontend struct {
	connection chan player.Connection
	lifetime   chan context.Context
}

func (panickingAvatarFrontend) Name() string { return "panic-avatar" }
func (f panickingAvatarFrontend) Serve(ctx context.Context, core frontend.Core, _ net.Listener) error {
	connection, err := core.Admit(ctx, player.Profile{Name: "PanicAvatar", UUID: offlineUUID("PanicAvatar")}, player.Options{})
	if err != nil {
		return err
	}
	f.connection <- connection
	f.lifetime <- ctx
	panic("private frontend detail")
}

func TestFrontendServePanicRetiresItsPlayersAndPreservesOtherAttachment(t *testing.T) {
	s := simulationTestServer(t)
	s.StartFrozen = true
	t.Cleanup(s.Stop)
	steady := &observingFrontend{core: make(chan frontend.Core, 1)}
	steadyListener := runtimeListener(t)
	steadyDone := make(chan error, 1)
	go func() { steadyDone <- s.Attach(steady, steadyListener) }()
	var core frontend.Core
	select {
	case core = <-steady.core:
	case <-time.After(5 * time.Second):
		t.Fatal("surviving frontend did not start")
	}
	t.Cleanup(func() { steadyListener.Close(); awaitServeExit(t, steadyDone) })
	broken := panickingAvatarFrontend{connection: make(chan player.Connection, 1), lifetime: make(chan context.Context, 1)}
	err := s.Attach(broken, runtimeListener(t))
	if !errors.Is(err, frontend.ErrCallbackFailed) || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe frontend fault error: %v", err)
	}
	connection := <-broken.connection
	if ctx := <-broken.lifetime; ctx.Err() == nil {
		t.Fatal("failed frontend kept its attachment lifetime")
	}
	if _, err = connection.Next(context.Background()); !errors.Is(err, player.ErrClosed) {
		t.Fatalf("failed attachment kept avatar stream: %v", err)
	}
	snapshot, err := core.Snapshot(context.Background())
	if err != nil || len(snapshot.Players) != 0 || s.OnlineCount() != 0 {
		t.Fatalf("failed attachment leaked actor: %+v err=%v online=%d", snapshot, err, s.OnlineCount())
	}
	survivor, err := core.Admit(context.Background(), player.Profile{Name: "Survivor", UUID: offlineUUID("Survivor")}, player.Options{})
	if err != nil {
		t.Fatalf("other attachment stopped after frontend fault: %v", err)
	}
	defer survivor.Close()
	if _, err = survivor.Next(context.Background()); err != nil {
		t.Fatalf("surviving attachment cannot replicate: %v", err)
	}
}
