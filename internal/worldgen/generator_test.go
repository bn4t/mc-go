package worldgen

import (
	"sync"
	"testing"
)

// TestGeneratorServesConsistentChunks generates a block of chunks from
// many goroutines and checks every chunk equals a sequential fill.
func TestGeneratorServesConsistentChunks(t *testing.T) {
	g, err := NewGenerator("minecraft:overworld", "minecraft:overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for cx := int32(-2); cx <= 1; cx++ {
				for cz := int32(-2); cz <= 1; cz++ {
					g.Chunk(cx+int32(i%2), cz)
				}
			}
		}(i)
	}
	wg.Wait()
	settings, _ := Settings("minecraft:overworld")
	params, _ := BiomeParameters("minecraft:overworld")
	f := NewNoiseChunkFiller(NewRandomState(settings, 12345))
	search := params.NewSearcher()
	region := NewBiomeRegion(f.rs, search, 0, 0)
	blocks, _ := f.Fill(0, 0)
	want := f.NewGenChunk(0, 0, blocks)
	f.BuildSurface(want, region.Block(f.rs.ZoomSeed()))
	got := g.Chunk(0, 0)
	for y := want.MinY; y < want.MinY+want.Height; y++ {
		for z := int32(0); z < 16; z++ {
			for x := int32(0); x < 16; x++ {
				if a, b := got.Block(x, y, z), want.Get(x, y, z); a != b {
					t.Fatalf("block %d,%d,%d: %s want %s", x, y, z, StateName(a), StateName(b))
				}
			}
		}
	}
	if b := g.BiomeAt(0, 16, 0); b == "" {
		t.Fatal("no biome")
	}
}
