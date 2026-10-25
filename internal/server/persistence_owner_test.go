package server

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAutosaveWaitsForCoherentPlayerAction(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.DataPath = filepath.Join(t.TempDir(), "players.json")
	sess, _ := enforceSession(t, s, "Saver")
	entered, release, mutationDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(mutationDone)
		s.simulationAction(func() {
			sess.health = 20
			close(entered)
			<-release
			sess.food = 20
		})
	}()
	<-entered
	saved := make(chan error, 1)
	go func() { saved <- s.SaveServerData() }()
	// Autosave cannot observe the intentionally half-applied health/food update.
	select {
	case err := <-saved:
		close(release)
		<-mutationDone
		t.Fatalf("autosave completed inside a player action: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-mutationDone
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	check := func() {
		raw, err := os.ReadFile(s.DataPath)
		if err != nil {
			t.Fatal(err)
		}
		var data serverDataFile
		if err = json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		p, ok := data.Players["Saver"]
		if !ok || p.Health != float32(p.Food) {
			t.Fatalf("incoherent saved player: health=%v food=%v", p.Health, p.Food)
		}
	}
	check()
	// Exercise the same public autosave entry point against repeated owned state
	// updates under -race, without starting clocks or encoding world chunks.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			s.simulationAction(func() { sess.health = float32(i); runtime.Gosched(); sess.food = int32(i) })
		}
	}()
	for range 12 {
		if err := s.SaveServerData(); err != nil {
			t.Fatal(err)
		}
		check()
	}
	<-done
}

func TestAutosaveAdmissionRejectsConcurrentShutdown(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	s.WorldPath = filepath.Join(t.TempDir(), "world.json")
	s.DataPath = filepath.Join(t.TempDir(), "players.json")
	s.simulation.mu.Lock()
	worldSaved, playersSaved := make(chan error, 1), make(chan error, 1)
	go func() { worldSaved <- s.SaveWorld() }()
	go func() { playersSaved <- s.SaveServerData() }()
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	deadline := time.Now().Add(time.Second)
	for !s.ShuttingDown() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s.simulation.mu.Unlock()
	if err := <-worldSaved; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("world save during shutdown=%v", err)
	}
	if err := <-playersSaved; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("player save during shutdown=%v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop blocked after rejecting autosaves")
	}
	for _, path := range []string{s.WorldPath, s.DataPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("rejected autosave wrote %s: %v", path, err)
		}
	}
}
