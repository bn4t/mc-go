package plugin

import (
	"context"
	"testing"

	"mc-go/internal/event"
)

type stubAPI struct {
	bus *event.Bus
	cmd *Commands
}

func (s stubAPI) Context() context.Context                     { return context.Background() }
func (s stubAPI) Hooks() *Hooks                                { return NewHooks() }
func (s stubAPI) Bus() *event.Bus                              { return s.bus }
func (s stubAPI) Commands() *Commands                          { return s.cmd }
func (s stubAPI) ServerInfo() Info                             { return Info{} }
func (s stubAPI) Kick(player, reason string) bool              { return false }
func (s stubAPI) Teleport(player string, x, y, z float64) bool { return false }
func (s stubAPI) PlayerState(player string) (PlayerState, bool) {
	return PlayerState{}, false
}
func (s stubAPI) ReportViolation(v Violation) {}

func TestCommandsRun(t *testing.T) {
	c := NewCommands()
	c.Register("ping", func(player string, args []string) string { return "pong " + player })
	out, ok := c.Run("ping", "steve", nil)
	if !ok || out != "pong steve" {
		t.Fatalf("run = %q,%v", out, ok)
	}
	if _, ok := c.Run("missing", "steve", nil); ok {
		t.Fatal("missing command reported found")
	}
	if len(c.Names()) != 1 {
		t.Fatalf("names = %v", c.Names())
	}
}

func TestRegisterAndInit(t *testing.T) {
	called := false
	Register(&testPlugin{fn: func() { called = true }})
	api := stubAPI{bus: event.New(), cmd: NewCommands()}
	if err := InitAll(api); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("registered plugin Init not called")
	}
}

type testPlugin struct{ fn func() }

func (p *testPlugin) Name() string       { return "test-hook" }
func (p *testPlugin) Init(api API) error { p.fn(); return nil }
