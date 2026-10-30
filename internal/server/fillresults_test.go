package server

import (
	"mc-go/internal/world"
	"strings"
	"testing"
)

func TestFillUnavailableDoesNotGenerateOrMutate(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	player := placeSession(t, s, "Filler", 8.5, -60, 5.5)
	player.active.Store(true)
	player.spawned.Store(true)
	d := s.dims[dimOverworld]
	old := d.w
	t.Cleanup(old.Stop)
	provider := &scheduledPolicyProvider{missing: true}
	d.w = world.NewProvided(nil, 1, provider)
	s.simulationAction(func() {
		r := s.runFill("Filler", strings.Fields("1000000 -58 1000000 1000001 -58 1000001 stone"))
		if r.outcome != commandFailed || !r.hasResult || r.result != 0 || r.fail != nil {
			t.Fatalf("unavailable fill completion %#v", r)
		}
		if len(r.comp.extra) != 1 || r.comp.extra[0].translate != "argument.pos.unloaded" {
			t.Fatalf("wrong position error %#v", r)
		}
	})
	if calls := provider.calls.Load(); calls != 0 {
		t.Fatalf("fill generated unavailable chunks: %d", calls)
	}
	if d.w.PeekBaseChunk(1000000>>4, 1000000>>4) != nil {
		t.Fatal("fill materialized unavailable target")
	}
}

func TestFillManualEmbeddingTypedOutcomes(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	args := strings.Fields("4008 -58 4 4009 -58 5 stone")
	first := s.runFill("", args)
	if first.outcome != commandSucceeded || !first.hasResult || first.result != 4 {
		t.Fatalf("manual fill %#v", first)
	}
	repeat := s.runFill("", args)
	if repeat.outcome != commandFailed || !repeat.hasResult || repeat.result != 0 {
		t.Fatalf("no-change fill %#v", repeat)
	}
	args[6] = "not_a_block"
	syntax := s.runFill("", args)
	if syntax.fail == nil || syntax.hasResult {
		t.Fatalf("syntax completed result %#v", syntax)
	}
}
