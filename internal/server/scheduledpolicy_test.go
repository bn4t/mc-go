package server

import (
	"sync/atomic"
	"testing"

	"mc-go/internal/world"
)

type scheduledPolicyProvider struct {
	calls   atomic.Int32
	missing bool
}

func (p *scheduledPolicyProvider) ProvideChunk(cx, cz int) *world.BaseChunk {
	p.calls.Add(1)
	if p.missing {
		return nil
	}
	return &world.BaseChunk{MinY: -64, Height: 384, Sections: make([][]world.Block, 24)}
}

func TestSavedPolicyManualNoSessionGameTick(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	pos := world.Pos{X: 1, Y: -60, Z: 1}
	button, ok := world.BlockStateOf("stone_button", map[string]string{"face": "floor", "powered": "true"})
	if !ok {
		t.Fatal("fixture state")
	}
	s.dims[dimOverworld].w.SetBlock(pos, button)
	s.ticks.deferSavedTick(schedKey{dim: dimOverworld, pos: pos, kind: tickBlock, block: "stone_button"}, 17, 0)
	for i := 0; i < 16; i++ {
		s.GameTick()
	}
	if got := s.dims[dimOverworld].w.GetBlock(pos); got != button {
		t.Fatal("manual embedding deadline early")
	}
	s.GameTick()
	if powered, _ := world.StateProperty(s.dims[dimOverworld].w.GetBlock(pos), "powered"); powered != "false" {
		t.Fatal("manual embedding deadline did not run")
	}
}

func TestSavedPolicyLoadingAndSimulationAreIndependent(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.ViewDistance, s.SimulationDistance = 6, 2
	p := placeSession(t, s, "Ticket", 8.5, -60, 5.5)
	p.active.Store(true)
	p.spawned.Store(true)
	chunks := []scheduledChunk{{dim: dimOverworld, x: 3}, {dim: dimOverworld, x: 3, z: 3}, {dim: dimOverworld, x: 4}, {dim: dimOverworld, x: 7}, {dim: dimOverworld, x: 8}}
	for _, chunk := range chunks {
		s.ticks.deferSavedTick(schedKey{dim: chunk.dim, pos: world.Pos{X: chunk.x*16 + 8, Y: -60, Z: chunk.z*16 + 4}, kind: tickBlock, block: "stone_button"}, 17, 0)
	}
	s.activateSavedTicksOwned(false)
	if len(s.ticks.relative) != 1 || len(s.ticks.queued) != 4 {
		t.Fatalf("loading admission relative%d active%d", len(s.ticks.relative), len(s.ticks.queued))
	}
	s.clock.mu.Lock()
	s.clock.count = 20
	s.clock.mu.Unlock()
	got := s.ticks.pollEligible(20, s.scheduledDispatchOwned())
	if len(got) != 2 {
		t.Fatalf("simulation admission returned%d", len(got))
	}
	for _, key := range got {
		if max(key.pos.X>>4, key.pos.Z>>4) != 3 {
			t.Fatalf("wrong simulation callback %#v", key)
		}
	}
	rows := s.captureScheduledChunkTicksOwned()[dimOverworld]
	if rows[[2]int32{4, 0}].Blocks[0].Delay != -3 || rows[[2]int32{8, 0}].Blocks[0].Delay != 17 {
		t.Fatalf("absolute/relative snapshot %#v", rows)
	}
}

func TestSavedPolicyForceRequestsDoNotUnpackBeforeClear(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 4008, Y: -59, Z: 5}, kind: tickBlock, block: "stone_button"}
	s.ticks.deferSavedTick(k, 17, 0)
	s.forceLoadSet(dimOverworld)[chunkLong(250, 0)] = true
	s.ticks.clearBlockArea(dimOverworld, k.pos, k.pos)
	if len(s.ticks.relative) != 1 || len(s.ticks.queued) != 0 {
		t.Fatal("force request eagerly unpacked or clear removed pending row")
	}
	s.activateSavedTicksOwned(false)
	if len(s.ticks.relative) != 0 {
		t.Fatal("existing forced-chunk set did not admit pending row")
	}
	if at, ok := s.ticks.queuedAt(k); !ok || at != 17 {
		t.Fatalf("forced activation deadline%d,%v", at, ok)
	}
}

func TestSavedPolicyQueriesObserveMaterializationWithoutLoading(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	p := placeSession(t, s, "Observer", 8.5, -60, 5.5)
	p.active.Store(true)
	p.spawned.Store(true)
	d := s.dims[dimOverworld]
	original := d.w
	t.Cleanup(original.Stop)
	provider := &scheduledPolicyProvider{missing: true}
	d.w = world.NewProvided(nil, 1, provider)
	if available, known := s.scheduledPositionAvailable(d, 0, 0, true); !known || available {
		t.Fatal("ticket coverage was mistaken for materialized entity readiness")
	}
	if s.blockPosLoaded(d, p, 1, -60, 1) || provider.calls.Load() != 0 {
		t.Fatal("readiness query generated a chunk")
	}
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1, Y: -60, Z: 1}, kind: tickBlock, block: "stone_button"}
	s.ticks.deferSavedTick(k, 17, 0)
	s.activateSavedTicksOwned(false)
	if len(s.ticks.relative) != 1 || len(s.ticks.queued) != 0 {
		t.Fatal("unmaterialized backend activated a pending row")
	}
	d.w.Stop()
	ready := &scheduledPolicyProvider{}
	d.w = world.NewProvided(nil, 1, ready)
	s.activateSavedTicksOwned(false)
	if len(s.ticks.relative) != 0 || len(s.ticks.queued) != 1 || ready.calls.Load() != 9 {
		t.Fatalf("admitted materialization did not prepare nine neighbors: %d", ready.calls.Load())
	}
	before := ready.calls.Load()
	if available, known := s.scheduledPositionAvailable(d, 0, 0, true); !known || !available || ready.calls.Load() != before {
		t.Fatal("materialized query changed backend")
	}
}

func TestSavedPolicyExecuteDimensionForkKeepsRealCoordinates(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.ViewDistance = 2
	p := placeSession(t, s, "Fork", 160.5, -60, 32.5)
	p.active.Store(true)
	p.spawned.Store(true)
	real := p.dimension()
	p.dimSwapPrev.Store(real)
	p.setDimension(s.dims[dimNether])
	plan := s.scheduledAdmissionsOwned()
	if !scheduledContains(plan.entity, scheduledChunk{dim: dimOverworld, x: 10, z: 2}) || scheduledContains(plan.entity, scheduledChunk{dim: dimNether, x: 1}) {
		t.Fatal("execute dimension fork moved the real player ticket")
	}
	p.setDimension(real)
	p.dimSwapPrev.Store(nil)
	p.setDimension(s.dims[dimNether])
	p.setPos(20.5, -60, 4.5, 0, 0, false)
	plan = s.scheduledAdmissionsOwned()
	if !scheduledContains(plan.entity, scheduledChunk{dim: dimNether, x: 1}) || scheduledContains(plan.entity, scheduledChunk{dim: dimOverworld, x: 10, z: 2}) {
		t.Fatal("real dimension transfer reused the previous realm coordinates")
	}
}

func TestSavedPolicyFutureDeadlinesDoNotPrepareChunks(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	p := placeSession(t, s, "Future", 8.5, -60, 5.5)
	p.active.Store(true)
	p.spawned.Store(true)
	d := s.dims[dimOverworld]
	original := d.w
	t.Cleanup(original.Stop)
	provider := &scheduledPolicyProvider{}
	d.w = world.NewProvided(nil, 1, provider)
	for i := 0; i < 1000; i++ {
		s.ticks.schedule(schedKey{dim: dimOverworld, pos: world.Pos{X: i}, kind: tickBlock, block: "stone_button"}, 10000)
	}
	if eligible := s.scheduledDispatchOwned(); len(eligible) != 0 || provider.calls.Load() != 0 {
		t.Fatalf("future callbacks prepared chunks: candidates%d provider%d", len(eligible), provider.calls.Load())
	}
	if s.ticks.pending() != 1000 {
		t.Fatal("future callbacks lost")
	}
}
