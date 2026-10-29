package server

import (
	"mc-go/internal/world"
	"strconv"
	"testing"
)

func TestScheduledFluidRegisteredTypes(t *testing.T) {
	for _, name := range []string{"water", "lava"} {
		for _, level := range []int{0, 1, 7, 8, 15} {
			state, ok := world.BlockStateOf(name, map[string]string{"level": strconv.Itoa(level)})
			if !ok {
				t.Fatal(name, level)
			}
			want := name
			if level != 0 {
				want = "flowing_" + name
			}
			if got := scheduledFluidStateType(state); got != want {
				t.Errorf("%s[%d] owner %s want%s", name, level, got, want)
			}
		}
	}
	for _, name := range []string{"kelp", "seagrass", "bubble_column"} {
		state, ok := world.BlockStateOf(name, nil)
		if !ok {
			t.Fatal(name)
		}
		if got := scheduledFluidStateType(state); got != "water" {
			t.Errorf("intrinsic %s owner%s", name, got)
		}
	}
	state, ok := world.BlockStateOf("oak_stairs", map[string]string{"waterlogged": "true"})
	if !ok {
		t.Fatal("waterlogged stairs")
	}
	if got := scheduledFluidStateType(state); got != "water" {
		t.Errorf("waterlogged source owner%s", got)
	}
	if got := scheduledFluidStateType(world.Air); got != "empty" {
		t.Errorf("air owner%s", got)
	}
}
func TestScheduledFluidFirstRequestAndOwners(t *testing.T) {
	q := newTickScheduler()
	k := schedKey{dim: "test", pos: world.Pos{X: 1}, kind: tickFluid, fluid: "water"}
	q.schedulePrio(k, 30, tickPrioHigh)
	q.schedulePrio(k, 5, tickPrioExtremelyHigh)
	if at, ok := q.queuedAt(k); !ok || at != 30 || q.prio[k] != tickPrioHigh {
		t.Fatal("same-fluid request changed deadline or priority")
	}
	other := k
	other.fluid = "flowing_water"
	q.schedule(other, 7)
	lava := k
	lava.fluid = "lava"
	q.schedule(lava, 40)
	if q.pending() != 3 {
		t.Fatal("different fluid owners merged")
	}
	due := q.poll(7)
	if len(due) != 1 || due[0] != other {
		t.Fatalf("due%v", due)
	}
	if !q.hasScheduled(k) || !q.hasScheduled(lava) {
		t.Fatal("other owner consumed future request")
	}
	if len(q.poll(29)) != 0 {
		t.Fatal("first request ran early")
	}
	if due = q.poll(30); len(due) != 1 || due[0] != k {
		t.Fatal("first owner deadline lost")
	}
}
func TestScheduledFluidDispatchRejectsReplacedOwner(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 4, Y: -60, Z: 4}
	for _, owners := range [][2]string{{"water", "lava"}, {"flowing_water", "water"}, {"flowing_lava", "lava"}} {
		current := owners[1]
		state, ok := world.BlockStateOf(current, map[string]string{"level": "0"})
		if !ok {
			t.Fatal(current)
		}
		d.w.SetBlock(p, state)
		side := world.Pos{X: p.X, Y: p.Y, Z: p.Z + 1}
		d.w.SetBlock(side, world.Air)
		s.ticks.schedule(schedKey{dim: d.name, pos: p, kind: tickFluid, fluid: owners[0]}, s.TickCount())
		s.scheduledTickStep()
		if d.w.GetBlock(side) != world.Air {
			t.Errorf("%s request dispatched replacement%s", owners[0], current)
		}
	}
	// An ordinary matching owner still performs the scheduled fluid callback.
	d.w.SetBlock(p, water(t, 0))
	s.ticks.schedule(schedKey{dim: d.name, pos: p, kind: tickFluid}, s.TickCount())
	s.scheduledTickStep()
	if levelAt(d, fluidWater, world.Pos{X: 4, Y: -60, Z: 5}) != 1 {
		t.Fatal("matching source callback did not spread")
	}
}
