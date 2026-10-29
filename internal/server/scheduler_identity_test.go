package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestScheduledFirstRequestRetainsDeadlineAndPriority(t *testing.T) {
	q := newTickScheduler()
	k := schedKey{dim: "test", pos: world.Pos{X: 4}, kind: tickBlock}
	q.schedulePrio(k, 8, tickPrioHigh)
	q.schedulePrio(k, 4, tickPrioVeryHigh)
	q.schedulePrio(k, 12, tickPrioNormal)
	if at, ok := q.queuedAt(k); !ok || at != 8 || q.prio[k] != tickPrioHigh {
		t.Fatalf("changed first entry: at%d ok%v priority%d", at, ok, q.prio[k])
	}
	if got := q.poll(4); len(got) != 0 {
		t.Fatal("fired early")
	}
	if got := q.poll(8); len(got) != 1 || got[0] != k {
		t.Fatalf("due entries%v", got)
	}
	if got := q.poll(12); len(got) != 0 {
		t.Fatalf("retained duplicate callback%v", got)
	}
}

func TestScheduledBlockTypesCoexistAtOnePosition(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 4, Y: -60, Z: 4}
	k := schedKey{dim: d.name, pos: p, kind: tickBlock}
	s.simulationAction(func() {
		stone, _ := world.BlockState("stone_button")
		d.w.SetBlock(p, stone)
		s.ticks.schedule(k, 20)
		powered, _ := world.WithProperty(stone, "powered", "true")
		d.w.SetBlock(p, powered)
		if !s.ticks.hasScheduled(k) {
			t.Fatal("property-only change lost same-type tick")
		}
		oak, _ := world.BlockState("oak_button")
		d.w.SetBlock(p, oak)
		if s.ticks.hasScheduled(k) {
			t.Fatal("old block tick matched replacement")
		}
		s.ticks.schedule(k, 30)
		if s.ticks.pending() != 2 {
			t.Fatal("registered types did not coexist")
		}
		if due := s.ticks.poll(20); len(due) != 1 || due[0].block != "stone_button" {
			t.Fatalf("old type%v", due)
		}
		if due := s.ticks.poll(30); len(due) != 1 || due[0].block != "oak_button" {
			t.Fatalf("new type%v", due)
		}
	})
}
