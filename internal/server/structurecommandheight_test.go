package server

import (
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"path/filepath"
	"runtime"
	"testing"
)

type commandWGProvider struct{ calls int }

func (p *commandWGProvider) ProvideChunk(cx, cz int) *world.BaseChunk {
	p.calls++
	return &world.BaseChunk{MinY: -64, Height: 384, Sections: make([][]world.Block, 24)}
}

func TestCommandWGResidentLifetime(t *testing.T) {
	provider := &commandWGProvider{}
	w := world.NewProvided(nil, 4, provider)
	w.Start()
	defer w.Stop()
	_ = w.BaseChunk(0, 0)
	d := &dimension{w: w}
	bounds := worldgen.BoundingBox{MinX: 0, MaxX: 15, MinZ: 0, MaxZ: 15}
	maps, identities := prepareCommandWG(d, bounds)
	sample := [256]int32{}
	sample[0] = 66
	key := [3]int32{0, 0, worldgen.HMOceanFloorWG}
	maps[key] = sample
	commitCommandWG(d, maps, identities)
	maps = nil
	identities = nil
	// The world, rather than the metadata cache or a detached snapshot, must
	// keep the weak identity alive through a GC and ordinary block mutations.
	runtime.GC()
	stone, ok := world.BlockState("stone")
	if !ok || stone == world.Air {
		t.Fatal("positive stone fixture unavailable")
	}
	w.SetBlock(world.Pos{X: 0, Y: 80, Z: 0}, stone)
	if got := w.GetBlock(world.Pos{X: 0, Y: 80, Z: 0}); got != stone {
		t.Fatal("ordinary edit absent")
	}
	repeated, resident := prepareCommandWG(d, bounds)
	if repeated[key][0] != 66 || provider.calls != 1 {
		t.Fatalf("retained=%d provider calls%d", repeated[key][0], provider.calls)
	}
	if resident[[2]int{0, 0}] != w.PeekBaseChunk(0, 0) {
		t.Fatal("identity is not the live resident")
	}
	repeated = nil
	resident = nil
	dir := t.TempDir()
	if err := w.SaveDimension(dir, dimOverworld, -64, 384, nil, nil); err != nil {
		t.Fatal(err)
	}
	savedMaps, _ := prepareCommandWG(d, bounds)
	if savedMaps[key][0] != 66 {
		t.Fatal("saving discarded the resident cache")
	}
	savedMaps = nil
	regions, err := world.ReadRegionFile(filepath.Join(world.DimensionDir(dir, dimOverworld), "region", "r.0.0.mca"))
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 1 {
		t.Fatalf("saved chunks%d", len(regions))
	}
	for _, tag := range regions {
		h, _ := tag["Heightmaps"].(map[string]any)
		if len(h) != 4 {
			t.Fatalf("saved heightmaps count%d", len(h))
		}
		if _, ok := h["OCEAN_FLOOR_WG"]; ok {
			t.Fatal("serialized runtime ocean WG")
		}
		if _, ok := h["WORLD_SURFACE_WG"]; ok {
			t.Fatal("serialized runtime surface WG")
		}
	}
	if err := w.LoadDimension(dir, dimOverworld, -64, 384); err != nil {
		t.Fatal(err)
	}
	replaced, _ := prepareCommandWG(d, bounds)
	if len(replaced) != 0 {
		t.Fatal("reused maps after resident replacement")
	}
	// Cold loading a new world must not deserialize runtime _WG samples.
	coldProvider := &commandWGProvider{}
	cold := world.NewProvided(nil, 4, coldProvider)
	cold.Start()
	defer cold.Stop()
	if err := cold.LoadDimension(dir, dimOverworld, -64, 384); err != nil {
		t.Fatal(err)
	}
	if cold.PeekBaseChunk(0, 0) == nil || cold.GetBlock(world.Pos{X: 0, Y: 80, Z: 0}) != stone {
		t.Fatal("cold reload did not install the saved resident/edit")
	}
	coldDimension := &dimension{w: cold}
	loaded, _ := prepareCommandWG(coldDimension, bounds)
	if len(loaded) != 0 || coldProvider.calls != 0 {
		t.Fatalf("cold maps%d provider calls%d", len(loaded), coldProvider.calls)
	}
	// A missing neighbor is a no-generation metadata lookup.
	missing := worldgen.BoundingBox{MinX: 16, MaxX: 31, MinZ: 0, MaxZ: 15}
	future, _ := prepareCommandWG(d, missing)
	if len(future) != 0 || provider.calls != 1 {
		t.Fatalf("future maps%d provider calls%d", len(future), provider.calls)
	}
}
