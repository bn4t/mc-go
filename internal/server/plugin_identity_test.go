package server

import (
	"context"
	"strings"
	"testing"

	"mc-go/api/event"
	"mc-go/api/player"
	"mc-go/api/plugin"
)

func pluginAvatarCore(s *Server) attachmentCore {
	return attachmentCore{frontendCore: frontendCore{s: s}, attachment: &frontendAttachment{ctx: context.Background()}}
}

func TestQueuedPluginEnforcementNeverTargetsReplacementIncarnation(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	first, _ := enforceSession(t, s, "Reused")
	api := serverAPI{s}
	if !api.Teleport("Reused", 50, 70, 50) || !api.Kick("Reused", "stale by-name request") || !api.TeleportID(first.ID(), 60, 80, 60) || !api.KickID(first.ID(), "stale ID request") {
		t.Fatal("active incarnation rejected queued requests")
	}
	// There is no ticker in this fixture. Admission takes the owner next and
	// replaces the old actor before the owner's queued enforcement batch drains.
	replacement, _ := enforceSession(t, s, "Reused")
	if first.ID() == replacement.ID() {
		t.Fatal("replacement reused incarnation")
	}
	state, found := s.playerStateByID(replacement.ID())
	if !found || state.Pose.X != 8.5 || state.Pose.Y != 65 || state.Pose.Z != 8.5 || s.OnlineCount() != 1 {
		t.Fatalf("stale requests affected replacement: found=%v state=%+v online=%d", found, state, s.OnlineCount())
	}
	if _, found := api.PlayerStateID(first.ID()); found || api.KickID(first.ID(), "late") || api.TeleportID(first.ID(), 1, 2, 3) {
		t.Fatal("retired incarnation remained addressable")
	}
	if replacement.closing.Load() || !replacement.active.Load() || first.active.Load() {
		t.Fatal("stale work affected replacement liveness")
	}
}

func TestScopedIDEnforcementCancellationAndHookSubmission(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	connection, err := pluginAvatarCore(s).Admit(context.Background(), player.Profile{Name: "Scoped", UUID: offlineUUID("Scoped")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	extension := &lifecyclePlugin{}
	manager, err := plugin.NewManager(context.Background(), serverAPI{s}, []plugin.Plugin{extension})
	if err != nil {
		t.Fatal(err)
	}
	control, ok := extension.api.(plugin.PlayerControl)
	if !ok || !control.TeleportID(connection.ID(), 50, 70, 50) || !control.KickID(connection.ID(), "expired") {
		t.Fatal("ID capability unavailable")
	}
	before, _ := s.playerStateByID(connection.ID())
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	s.simulationAction(func() {})
	after, found := s.playerStateByID(connection.ID())
	if !found || after != before || control.KickID(connection.ID(), "late") || control.TeleportID(connection.ID(), 1, 2, 3) {
		t.Fatalf("expired ID enforcement ran: %+v %v", after, found)
	}
	remove := s.hooks.OnMove(context.Background(), 0, func(_ context.Context, move *plugin.Move) bool {
		if !((serverAPI{s}).TeleportID(move.ID, 8, 20, 8)) {
			t.Error("hook could not submit exact-ID enforcement")
		}
		return true
	})
	defer remove()
	pose := before.Pose
	pose.Y = 10
	if applied, err := connection.Move(context.Background(), pose); err != nil || !applied {
		t.Fatalf("hook move=%v,%v", applied, err)
	}
	after, found = s.playerStateByID(connection.ID())
	if !found || after.Pose.X != 8 || after.Pose.Y != 20 || after.Pose.Z != 8 {
		t.Fatalf("deferred hook request missing: %+v %v", after, found)
	}
}

func TestPluginCommandPanicIsGenericAndRuntimeContinues(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	s.commands.Register("panicplugin", func(string, []string) string { panic("secret callback detail") })
	s.commands.Register("healthyplugin", func(string, []string) string { return "healthy" })
	s.simulationAction(func() {
		failed, found := s.runCommandAt("", 4, "panicplugin", nil)
		if !found || failed.text != "Plugin command failed" || strings.Contains(failed.text, "secret") {
			t.Fatalf("unsafe command fault response: %+v %v", failed, found)
		}
		healthy, found := s.runCommandAt("", 4, "healthyplugin", nil)
		if !found || healthy.text != "healthy" {
			t.Fatalf("subsequent command failed: %+v %v", healthy, found)
		}
	})
}

func TestTypedPluginLifecycleObservesCommittedIncarnationValues(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	joins, stopJoin := s.bus.Subscribe(event.TopicPlayerJoined, 2)
	defer stopJoin()
	leaves, stopLeave := s.bus.Subscribe(event.TopicPlayerLeft, 2)
	defer stopLeave()
	connection, err := pluginAvatarCore(s).Admit(context.Background(), player.Profile{Name: "Observed", UUID: offlineUUID("Observed")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	joined := (<-joins).Data.(event.PlayerJoined)
	state, found := s.playerStateByID(connection.ID())
	if !found || joined.Player != state || joined.Player.ID != connection.ID() || state.EntityID == 0 {
		t.Fatalf("join preceded commit: joined=%+v state=%+v found=%v", joined, state, found)
	}
	joined.Player.Profile.Name = "mutated copy"
	joined.Player.Pose.X = 999
	if again, _ := s.playerStateByID(connection.ID()); again != state {
		t.Fatal("lifecycle value event aliases live actor")
	}
	record := s.findPlayerByIDOwned(connection.ID())
	if !((serverAPI{s}).KickID(connection.ID(), "Policy retired")) {
		t.Fatal("kick rejected live incarnation")
	}
	s.simulationAction(func() {})
	left := (<-leaves).Data.(event.PlayerLeft)
	if left.Player != state || left.Reason != "Policy retired" {
		t.Fatalf("retirement observation=%+v", left)
	}
	if _, found := s.playerStateByID(connection.ID()); found || s.OnlineCount() != 0 {
		t.Fatal("leave observation preceded retirement")
	}
	if _, found := s.entities.Get(state.EntityID); found {
		t.Fatal("retirement left authoritative entity")
	}
	s.simulationAction(func() { s.retirePlayerOwned(record, "duplicate") })
	select {
	case duplicate := <-leaves:
		t.Fatalf("duplicate retirement emitted event: %+v", duplicate)
	default:
	}
}

func TestPluginStateQueryUsesCommittedStateDuringOwnerMutation(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	sess, _ := enforceSession(t, s, "Committed")
	before, found := (serverAPI{s}).PlayerStateID(sess.ID())
	if !found {
		t.Fatal("missing admitted player")
	}
	s.simulationAction(func() {
		sess.setPos(100, 70, 200, 45, 15, false)
		if halfway, found := (serverAPI{s}).PlayerStateID(sess.ID()); !found || halfway != before {
			t.Fatalf("query exposed partial pose transaction: %+v found=%v", halfway, found)
		}
		sess.setMode(GameModeSurvival)
		if halfway, found := s.PlayerStateOf("Committed"); !found || halfway != before {
			t.Fatalf("name query exposed uncommitted pose/mode: %+v found=%v", halfway, found)
		}
	})
	after, found := (serverAPI{s}).PlayerStateID(sess.ID())
	if !found || after.ID != before.ID || after.X != 100 || after.Y != 70 || after.Z != 200 || after.Yaw != 45 || after.Pitch != 15 || after.OnGround || after.Gamemode != GameModeSurvival {
		t.Fatalf("committed query omitted completed transaction: %+v found=%v", after, found)
	}
}
