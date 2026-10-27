package worldgen

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

// TestMthSinTableMatchesVanilla checks the sine table the carvers turn
// with against the official server's, by a hash of its float bits.
func TestMthSinTableMatchesVanilla(t *testing.T) {
	var h int64
	for _, f := range mthSinTable {
		h = h*31 + int64(int32(math.Float32bits(f)))
	}
	for _, l := range loadProbe(t) {
		if l.kind == "mthsin" {
			if got := fmt.Sprint(h); got != l.value {
				t.Fatalf("sine table hash %s, want %s", got, l.value)
			}
			return
		}
	}
	t.Fatal("no mthsin golden")
}

// TestCarversMatchVanilla runs the overworld's cave, extra cave and
// canyon carvers and the nether's cave carver over the probe's chunks —
// every chunk within eight starting carvers by its corner biome, tunnels
// and rooms cut through the mask, aquifer water and lava, the lava
// level, grass restored on exposed dirt — and compares every column
// after the carvers stage.
func TestCarversMatchVanilla(t *testing.T) {
	for _, dim := range []struct{ kind, settings, params string }{
		{"carve", "minecraft:overworld", "minecraft:overworld"},
		{"carve.nether", "minecraft:nether", "minecraft:nether"},
	} {
		t.Run(dim.kind, func(t *testing.T) {
			file, err := os.Open("testdata/chunk_fill.tsv.gz")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			zr, err := gzip.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			settings, _ := Settings(dim.settings)
			params, _ := BiomeParameters(dim.params)
			// One searcher per parameter list, as the probe's thread
			// keeps one previous answer per search tree.
			search := params.NewSearcher()
			fillers := map[string]*NoiseChunkFiller{}
			built := map[string]*GenChunk{}
			sc := bufio.NewScanner(zr)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			columns, bad := 0, 0
			for sc.Scan() {
				parts := strings.Split(sc.Text(), "\t")
				if parts[0] != dim.kind {
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
				key := seed + "/" + parts[2]
				c, ok := built[key]
				if !ok {
					rs := f.rs
					region := NewBiomeRegion(rs, search, cx, cz)
					blocks, _ := f.Fill(cx, cz)
					c = f.NewGenChunk(cx, cz, blocks)
					f.BuildSurface(c, region.Block(rs.ZoomSeed()))
					direct := func(qx, qy, qz int32) string { return search.Find(rs.Climate(qx, qy, qz)) }
					f.Carve(c, func(sx, sz int32) string { return direct(sx*4, 0, sz*4) },
						func(bx, by, bz int32) string { return ZoomBiome(rs.ZoomSeed(), bx, by, bz, direct) })
					built[key] = c
				}
				var got string
				if dim.kind == "carve" {
					got = genColumnRuns(c, x, z)
				} else {
					got = dimensionRuns(c, 0, 256, x, z)
				}
				if got != parts[4] {
					bad++
					if bad <= 6 {
						t.Errorf("seed %s chunk %d,%d column %d,%d:\n  got  %s\n  want %s", seed, cx, cz, x, z, got, parts[4])
					}
				}
				columns++
			}
			if columns == 0 {
				t.Fatal("no columns checked")
			}
			if bad > 0 {
				t.Fatalf("%d of %d columns differ", bad, columns)
			}
			t.Logf("%d columns match", columns)
		})
	}
}
