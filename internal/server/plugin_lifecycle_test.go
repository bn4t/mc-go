package server

import (
	"context"
	"math"
	"testing"

	"mc-go/api/plugin"
)

type lifecyclePlugin struct{ api plugin.API }

func (*lifecyclePlugin) Name() string                { return "lifetime-fixture" }
func (p *lifecyclePlugin) Init(api plugin.API) error { p.api = api; return nil }

func TestPluginQueuedEnforcementRetainsLifetime(t *testing.T) {
	s := simulationTestServer(t)
	sess, _ := enforceSession(t, s, "Lifetime")
	extension := &lifecyclePlugin{}
	manager, err := plugin.NewManager(context.Background(), serverAPI{s}, []plugin.Plugin{extension})
	if err != nil {
		t.Fatal(err)
	}
	if !extension.api.Teleport("Lifetime", 50, 70, 50) || !extension.api.Kick("Lifetime", "expired") {
		t.Fatal("live scope rejected requests")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	s.simulationAction(func() {})
	x, y, z, _, _, _ := sess.Pos()
	if x != 8.5 || y != 65 || z != 8.5 || sess.closing.Load() {
		t.Fatalf("expired queued requests ran: pos=(%v,%v,%v) closing=%v", x, y, z, sess.closing.Load())
	}
	if extension.api.Teleport("Lifetime", 1, 2, 3) || extension.api.Kick("Lifetime", "late") {
		t.Fatal("expired scope accepted enforcement")
	}
	if (serverAPI{s}).Teleport("Lifetime", math.NaN(), 65, 0) {
		t.Fatal("invalid request accepted")
	}
	if (serverAPI{s}).Teleport("offline", 1, 65, 0) {
		t.Fatal("offline request accepted")
	}
}

func TestEnforcementCannotMutateAfterShutdown(t *testing.T) {
	s := simulationTestServer(t)
	sess, _ := enforceSession(t, s, "Stopped")
	s.shuttingDown.Store(true)
	if s.TeleportPlayer("Stopped", 100, 80, 100) || s.TeleportPlayerRot("Stopped", 100, 80, 100, 90, 0) || s.KickPlayer("Stopped", "late") {
		t.Fatal("stopped server accepted external mutation")
	}
	x, y, z, _, _, _ := sess.Pos()
	if x != 8.5 || y != 65 || z != 8.5 || sess.closing.Load() {
		t.Fatal("shutdown changed state")
	}
}
