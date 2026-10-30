package server

import (
	"testing"
	"time"
)

// The frozen loop polls commands faster than the player cadence. Entity base
// ticks must follow the same receipt as living/food/stat ticks, not every poll.
func TestFrozenPlayerBaseTickCadence(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "FrozenCadence")
	s.SetFrozen(true)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { s.runGameLoop(stop); close(done) }()
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	})
	deadline := time.Now().Add(5 * time.Second)
	var living, base int
	for living < 3 && time.Now().Before(deadline) {
		s.simulationAction(func() { living, base = int(p.locTicks), int(p.entityTicks) })
		if living < 3 {
			time.Sleep(time.Millisecond)
		}
	}
	close(stop)
	<-done
	s.simulationAction(func() { living, base = int(p.locTicks), int(p.entityTicks) })
	if living < 3 {
		t.Fatal("player loop failed to advance", living, base)
	}
	if base != living {
		t.Fatalf("frozen entity base ticks%d, living/stat ticks%d", base, living)
	}
	if s.gameTime() != 0 {
		t.Fatal("frozen world advanced", s.gameTime())
	}
}
