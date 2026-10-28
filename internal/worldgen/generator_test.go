package worldgen

import (
	"sync"
	"testing"
)

// TestGeneratorServesConsistentChunks generates a block of chunks from
// many goroutines and checks a chunk is its carved blocks under the
// writes of the features runs reaching it: its own run's everywhere it
// wrote, and nothing elsewhere a run did not write.
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
	got := g.Chunk(0, 0)
	carved := g.carved(0, 0).genChunk()
	written := map[int32]bool{}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			for _, ch := range g.runFeatures(dx, dz).changes[(1-dx)*3+(1-dz)] {
				written[ch.index] = true
			}
		}
	}
	own := map[int32]StateID{}
	for _, ch := range g.runFeatures(0, 0).changes[4] {
		own[ch.index] = ch.state
	}
	if len(own) == 0 {
		t.Fatal("the chunk's own features changed nothing")
	}
	for y := carved.MinY; y < carved.MinY+carved.Height; y++ {
		for z := int32(0); z < 16; z++ {
			for x := int32(0); x < 16; x++ {
				i := int32(carved.index(x, y, z))
				a := got.Block(x, y, z)
				if s, ok := own[i]; ok && a != s {
					t.Fatalf("block %d,%d,%d: %s, own run wrote %s", x, y, z, StateName(a), StateName(s))
				}
				if !written[i] && a != carved.States[i] {
					t.Fatalf("block %d,%d,%d: %s, carved %s and unwritten", x, y, z, StateName(a), StateName(carved.States[i]))
				}
			}
		}
	}
	if b := g.BiomeAt(0, 16, 0); b == "" {
		t.Fatal("no biome")
	}
}

// TestGeneratorIsPure checks a chunk does not depend on the order chunks
// are asked for or on what the caches have dropped.
func TestGeneratorIsPure(t *testing.T) {
	a, err := NewGenerator("minecraft:overworld", "minecraft:overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := NewGenerator("minecraft:overworld", "minecraft:overworld", 12345)
	// b keeps almost nothing and is asked from the far side first.
	b.carvedCache.limit, b.runCache.limit = 4, 2
	for _, c := range [][2]int32{{2, 1}, {1, 1}, {1, 0}} {
		b.Chunk(c[0], c[1])
	}
	for _, c := range [][2]int32{{0, 0}, {1, 0}, {1, 1}} {
		ga, gb := a.Chunk(c[0], c[1]), b.Chunk(c[0], c[1])
		for sy := range ga.sections {
			for y := int32(0); y < 16; y++ {
				for z := int32(0); z < 16; z++ {
					for x := int32(0); x < 16; x++ {
						wy := ga.MinY + int32(sy)*16 + y
						if p, q := ga.Block(x, wy, z), gb.Block(x, wy, z); p != q {
							t.Fatalf("chunk %v block %d,%d,%d: %s then %s", c, x, wy, z, StateName(p), StateName(q))
						}
					}
				}
			}
		}
	}
}
