package server

import (
	"path/filepath"
	"testing"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

func normalTestServer(t *testing.T, dir string) *Server {
	t.Helper()
	s := New("127.0.0.1:0")
	s.WorldDir = dir
	s.LevelType = LevelTypeNormal
	s.WorldSeed = 12345
	// Features stripped keeps the test fast; the pipeline is the same.
	s.WorldgenOverlay = worldgen.StageOverrides(worldgen.Vanilla(), func(string) bool { return false }, true, true)
	if err := s.loadWorld(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.world.Stop() })
	return s
}

// TestNormalWorldSaveReload saves a generated world and loads it back:
// level.dat keeps the generator, saved chunks come back from disk
// (edits included) and unsaved ones regenerate identically.
func TestNormalWorldSaveReload(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "world")
	s := normalTestServer(t, dir)
	if !s.world.Provided() {
		t.Fatal("normal level type did not install a provided world")
	}
	sx, sy, sz := s.WorldSpawn()
	edit := world.Pos{X: 3, Y: 100, Z: 4}
	s.world.SetBlock(edit, world.Dirt)
	base := s.world.BaseChunk(0, 0)
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	far := New("127.0.0.1:0")
	far.LevelType, far.WorldSeed, far.WorldgenOverlay = LevelTypeNormal, 12345, s.WorldgenOverlay
	if err := far.applyLevelType(); err != nil {
		t.Fatal(err)
	}
	want := far.world.BaseChunk(9, -7)
	far.world.Stop()

	r := New("127.0.0.1:0")
	r.WorldDir = dir
	r.WorldgenOverlay = s.WorldgenOverlay
	if err := r.loadWorld(); err != nil {
		t.Fatal(err)
	}
	defer r.world.Stop()
	if r.LevelType != LevelTypeNormal || !r.world.Provided() {
		t.Fatalf("reloaded level type %q provided %v", r.LevelType, r.world.Provided())
	}
	if x, y, z := r.WorldSpawn(); x != sx || y != sy || z != sz {
		t.Fatalf("spawn %v,%v,%v reloaded as %v,%v,%v", sx, sy, sz, x, y, z)
	}
	if got := r.world.GetBlock(edit); got != world.Dirt {
		t.Fatalf("edit reloaded as %v", got)
	}
	if r.world.PeekBaseChunk(0, 0) == nil {
		t.Fatal("saved chunk was not installed from disk")
	}
	for y := base.MinY; y < base.MinY+base.Height; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				p := world.Pos{X: x, Y: y, Z: z}
				if p == edit {
					continue
				}
				if a, b := base.At(x, y, z), r.world.GetBlock(p); a != b {
					t.Fatalf("block %v: generated %v reloaded %v", p, a, b)
				}
			}
		}
	}
	got := r.world.BaseChunk(9, -7)
	for i := range want.Sections {
		for j := range want.Sections[i] {
			if want.Sections[i][j] != got.Sections[i][j] {
				t.Fatalf("regenerated chunk differs in section %d", i)
			}
		}
	}
	for i := range want.Biomes {
		if want.Biomes[i] != got.Biomes[i] {
			t.Fatalf("regenerated biome cell %d: %s vs %s", i, want.Biomes[i], got.Biomes[i])
		}
	}
}
