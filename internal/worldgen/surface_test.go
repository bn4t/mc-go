package worldgen

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"strings"
	"testing"
)

func genColumnRuns(c *GenChunk, x, z int32) string {
	var out []string
	start := c.MinY
	top := c.MinY + c.Height - 1
	for y := c.MinY; y <= top; y++ {
		s := c.Get(x, y, z)
		if y == top || c.Get(x, y+1, z) != s {
			out = append(out, fmt.Sprintf("%d-%d=%s", start, y, StateName(s)))
			start = y + 1
		}
	}
	return strings.Join(out, " ")
}

// TestSurfaceMatchesVanilla runs the noise fill and the surface stage —
// surface rules over zoomed biomes, badlands pillars and icebergs — on
// the probe's chunks and compares every column with the official
// generator's.
func TestSurfaceMatchesVanilla(t *testing.T) {
	file, err := os.Open("testdata/chunk_fill.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := Settings("minecraft:overworld")
	params, _ := BiomeParameters("minecraft:overworld")
	type chunkKey struct {
		seed   string
		cx, cz int32
	}
	// One searcher for the whole file, as the probe's single thread.
	search := params.NewSearcher()
	fillers := map[string]*NoiseChunkFiller{}
	built := map[chunkKey]*GenChunk{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	columns, bad := 0, 0
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if parts[0] != "surface" {
			continue
		}
		seed := parts[1]
		var cx, cz, x, z int32
		fmt.Sscanf(parts[2], "%d,%d", &cx, &cz)
		fmt.Sscanf(parts[3], "%d,%d", &x, &z)
		f, ok := fillers[seed]
		if !ok {
			f = NewNoiseChunkFiller(NewRandomState(settings, atoi64(t, seed)))
			fillers[seed] = f
		}
		key := chunkKey{seed, cx, cz}
		c, ok := built[key]
		if !ok {
			region := NewBiomeRegion(f.rs, search, cx, cz)
			blocks, _ := f.Fill(cx, cz)
			c = f.NewGenChunk(cx, cz, blocks)
			f.BuildSurface(c, region.Block(f.rs.ZoomSeed()))
			built[key] = c
		}
		if got := genColumnRuns(c, x, z); got != parts[4] {
			bad++
			if bad <= 8 {
				t.Errorf("seed %s chunk %d,%d column %d,%d:\n  got  %s\n  want %s", seed, cx, cz, x, z, got, parts[4])
			}
		}
		columns++
	}
	if columns != 3*8*256 {
		t.Fatalf("checked %d columns", columns)
	}
	if bad > 0 {
		t.Fatalf("%d of %d columns differ", bad, columns)
	}
}
