package anticheat

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/api/event"
	"mc-go/api/plugin"
)

type kickCall struct{ player, reason string }

type fakeAPI struct {
	bus        *event.Bus
	cmd        *plugin.Commands
	mu         sync.Mutex
	kicks      []kickCall
	states     map[string]plugin.PlayerState
	violations []plugin.Violation
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{bus: event.New(), cmd: plugin.NewCommands(), states: map[string]plugin.PlayerState{}}
}

func (f *fakeAPI) Context() context.Context   { return context.Background() }
func (f *fakeAPI) Hooks() *plugin.Hooks       { return plugin.NewHooks() }
func (f *fakeAPI) Bus() *event.Bus            { return f.bus }
func (f *fakeAPI) Commands() *plugin.Commands { return f.cmd }
func (f *fakeAPI) ServerInfo() plugin.Info    { return plugin.Info{} }
func (f *fakeAPI) Kick(player, reason string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kicks = append(f.kicks, kickCall{player, reason})
	return true
}
func (f *fakeAPI) Teleport(player string, x, y, z float64) bool { return true }
func (f *fakeAPI) PlayerState(player string) (plugin.PlayerState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[player]
	return st, ok
}
func (f *fakeAPI) ReportViolation(v plugin.Violation) {
	f.mu.Lock()
	f.violations = append(f.violations, v)
	f.mu.Unlock()
	f.bus.Publish(event.TopicViolation, v)
}

func violationsFor(f *fakeAPI, check string) []plugin.Violation {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []plugin.Violation
	for _, v := range f.violations {
		if v.Check == check {
			out = append(out, v)
		}
	}
	return out
}

func TestSpeedViolation(t *testing.T) {
	api := newFakeAPI()
	d := newDetector(api, 100)
	base := time.Now()
	d.onMove(event.PlayerMove{Name: "Flash", X: 0, Y: 65, Z: 0, At: base})
	d.onMove(event.PlayerMove{Name: "Flash", X: 100, Y: 65, Z: 0, At: base.Add(50 * time.Millisecond)})
	vs := violationsFor(api, checkSpeed)
	if len(vs) != 1 || vs[0].Player != "Flash" || vs[0].Count != 1 {
		t.Fatalf("speed violations = %+v", vs)
	}
}

func TestSpeedLegitFlightClean(t *testing.T) {
	api := newFakeAPI()
	d := newDetector(api, 100)
	base := time.Now()
	d.onMove(event.PlayerMove{Name: "Pilot", X: 0, Y: 65, Z: 0, At: base})
	// Legal creative flight: 10 m/s for a full second.
	for i := 1; i <= 20; i++ {
		d.onMove(event.PlayerMove{Name: "Pilot", X: float64(i) / 2, Y: 65, Z: 0,
			At: base.Add(time.Duration(i) * 50 * time.Millisecond)})
	}
	if vs := violationsFor(api, checkSpeed); len(vs) != 0 {
		t.Fatalf("legit flight flagged: %+v", vs)
	}
}

func TestTimerViolation(t *testing.T) {
	api := newFakeAPI()
	d := newDetector(api, 100)
	base := time.Now()
	for i := 0; i < 70; i++ {
		d.onMove(event.PlayerMove{Name: "Timer", X: 0, Y: 65, Z: 0,
			At: base.Add(time.Duration(i) * 10 * time.Millisecond)})
	}
	if vs := violationsFor(api, checkTimer); len(vs) == 0 {
		t.Fatal("packet timer not flagged")
	}
}

func TestReachViolation(t *testing.T) {
	api := newFakeAPI()
	d := newDetector(api, 100)
	d.onBlock(event.BlockChange{Player: "Digger", X: 100, Y: 65, Z: 100, PX: 0, PY: 65, PZ: 0})
	if vs := violationsFor(api, checkReach); len(vs) != 1 {
		t.Fatalf("reach violations = %+v", vs)
	}
	// Adjacent break is clean.
	d.onBlock(event.BlockChange{Player: "Digger", X: 1, Y: 65, Z: 0, PX: 0.5, PY: 65, PZ: 0.5})
	if vs := violationsFor(api, checkReach); len(vs) != 1 {
		t.Fatalf("adjacent break flagged: %+v", vs)
	}
	// Console/test changes carry no actor snapshot and are skipped.
	d.onBlock(event.BlockChange{Player: "Console", X: 100, Y: 65, Z: 100})
	if vs := violationsFor(api, checkReach); len(vs) != 1 {
		t.Fatalf("actorless change flagged: %+v", vs)
	}
}

func TestAutoKickOnce(t *testing.T) {
	api := newFakeAPI()
	d := newDetector(api, 2)
	d.violate("Hacker", checkSpeed, "fast")
	if len(api.kicks) != 0 {
		t.Fatal("kicked below threshold")
	}
	d.violate("Hacker", checkSpeed, "fast")
	d.violate("Hacker", checkSpeed, "fast")
	api.mu.Lock()
	n := len(api.kicks)
	api.mu.Unlock()
	if n != 1 {
		t.Fatalf("kicks = %d, want exactly 1", n)
	}
	if !strings.Contains(api.kicks[0].reason, checkSpeed) {
		t.Fatalf("kick reason = %q", api.kicks[0].reason)
	}
}

func TestTeleportGrace(t *testing.T) {
	api := newFakeAPI()
	d := newDetector(api, 100)
	base := time.Now()
	d.onMove(event.PlayerMove{Name: "Blink", X: 0, Y: 65, Z: 0, At: base})
	d.onTeleport(event.PlayerTeleport{Name: "Blink", X: 500, Y: 70, Z: 500})
	d.onMove(event.PlayerMove{Name: "Blink", X: 500.5, Y: 70, Z: 500, At: time.Now()})
	if vs := violationsFor(api, checkSpeed); len(vs) != 0 {
		t.Fatalf("teleport landing flagged: %+v", vs)
	}
}

func TestCommand(t *testing.T) {
	api := newFakeAPI()
	d := newDetector(api, 100)
	if out := d.command(nil); !strings.Contains(out, "tracking 0 players") {
		t.Fatalf("status = %q", out)
	}
	d.violate("Hacker", checkSpeed, "fast")
	if out := d.command([]string{"violations"}); !strings.Contains(out, "Hacker") {
		t.Fatalf("violations = %q", out)
	}
	if out := d.command([]string{"violations", "Hacker"}); !strings.Contains(out, "speed=1") {
		t.Fatalf("player violations = %q", out)
	}
	if out := d.command([]string{"violations", "Clean"}); !strings.Contains(out, "no violations") {
		t.Fatalf("clean lookup = %q", out)
	}
}

func TestInitRegistersCommand(t *testing.T) {
	api := newFakeAPI()
	a := &anticheat{}
	if err := a.Init(api); err != nil {
		t.Fatal(err)
	}
	if out, ok := api.cmd.Run("ac", "Op", []string{"status"}); !ok || !strings.Contains(out, "anticheat") {
		t.Fatalf("/ac = %q,%v", out, ok)
	}
}
