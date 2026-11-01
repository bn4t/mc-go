package server

import (
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"path/filepath"
	"testing"
)

func TestGeneratedLeafTickOwnerAdmissionAndStateGuard(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: -60, Z: 1}
	near := world.Pos{X: 1, Y: -60, Z: 2}
	leaf, _ := world.BlockStateOf("oak_leaves", map[string]string{"distance": "2"})
	log, _ := world.BlockState("oak_log")
	d.w.SetBlock(pos, leaf)
	d.w.SetBlock(near, log)
	row := worldgen.GeneratedLeafTick{Pos: worldgen.BlockPos{1, -60, 1}, Block: "oak_leaves"}
	d.terrain = &terrainSource{genLeafTicks: []worldgen.GeneratedLeafTick{row, row}}
	before := s.captureScheduledChunkTicksOwned()[dimOverworld][[2]int32{0, 0}].Blocks
	if len(before) != 1 || before[0].Delay != 0 || before[0].Type != "minecraft:oak_leaves" {
		t.Fatalf("dormant snapshot: %#v", before)
	}
	s.GameTick()
	if got, _ := world.StateProperty(d.w.GetBlock(pos), "distance"); got != "1" {
		t.Fatalf("leaf distance after activated tick: %s", got)
	}
	// A typed pending leaf tick cannot act on a replacement block.
	d.w.SetBlock(pos, leaf)
	d.terrain.genLeafTicks = []worldgen.GeneratedLeafTick{row}
	s.drainGeneratedLeafTicksOwned()
	d.w.SetBlock(pos, world.Dirt)
	s.GameTick()
	if got := d.w.GetBlock(pos); got != world.Dirt {
		t.Fatalf("stale tick changed replacement to %v", got)
	}
}
func TestGeneratedLeafTickFencesDetachedSnapshot(t *testing.T) {
	q := newTickScheduler()
	key := schedKey{dim: dimOverworld, pos: world.Pos{X: 1}, kind: tickBlock, block: "oak_leaves"}
	c := scheduledChunkOf(key)
	q.deferGeneratedTick(key, 0)
	q.detachChunks(map[scheduledChunk]bool{c: true})
	old, ok := q.snapshotDetachedChunk(c, 0)
	if !ok {
		t.Fatal("missing detached snapshot")
	}
	other := key
	other.pos.X = 2
	q.deferGeneratedTick(other, 0)
	if q.discardSerializedChunk(old) {
		t.Fatal("stale snapshot discarded new generated tick")
	}
	if len(q.relative[c]) != 2 {
		t.Fatalf("relative records %d", len(q.relative[c]))
	}
}

func TestGeneratedLeafTickProviderHandoffOnce(t *testing.T) {
	src, err := newTerrainSource(0, nil, "overworld", "overworld", -64, 384, false)
	if err != nil {
		t.Fatal(err)
	}
	if src.ProvideChunk(-1, 0) == nil {
		t.Fatal("first generated chunk absent")
	}
	src.freshMu.Lock()
	first := len(src.genLeafTicks)
	src.genLeafTicks = nil
	src.freshMu.Unlock()
	if first == 0 {
		t.Fatal("generated feature chunk carried no leaf requests")
	}
	if src.ProvideChunk(-1, 0) == nil {
		t.Fatal("repeated chunk absent")
	}
	src.freshMu.Lock()
	again := len(src.genLeafTicks)
	src.freshMu.Unlock()
	if again != 0 {
		t.Fatalf("repeated provider handoff queued %d duplicate requests", again)
	}
}

func TestGeneratedLeafTickSaveAndReloadBeforeActivation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "world")
	s := New("unused")
	s.WorldDir = dir
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: 100, Z: 1}
	near := world.Pos{X: 1, Y: 100, Z: 2}
	leaf, _ := world.BlockStateOf("oak_leaves", map[string]string{"distance": "2"})
	log, _ := world.BlockState("oak_log")
	d.w.SetBlock(pos, leaf)
	d.w.SetBlock(near, log)
	d.terrain = &terrainSource{genLeafTicks: []worldgen.GeneratedLeafTick{{Pos: worldgen.BlockPos{1, 100, 1}, Block: "oak_leaves"}}}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	r := New("unused")
	r.WorldDir = dir
	if err := r.loadWorld(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	rows := r.captureScheduledChunkTicksOwned()[dimOverworld][[2]int32{0, 0}].Blocks
	if len(rows) != 1 || rows[0].Type != "minecraft:oak_leaves" || rows[0].Delay != 0 {
		t.Fatalf("reloaded dormant records %#v", rows)
	}
	if got, _ := world.StateProperty(r.dims[dimOverworld].w.GetBlock(pos), "distance"); got != "2" {
		t.Fatalf("premature leaf distance %s", got)
	}
	r.GameTick()
	if got, _ := world.StateProperty(r.dims[dimOverworld].w.GetBlock(pos), "distance"); got != "1" {
		t.Fatalf("reloaded leaf tick distance %s", got)
	}
}
