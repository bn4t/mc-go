package worldgen

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"strings"
	"testing"
)

// dimensionRuns renders a column over a dimension's full build height
// (the noise settings may cover less; above them is air).
func dimensionRuns(c *GenChunk, minY, height, x, z int32) string {
	var out []string
	start := minY
	top := minY + height - 1
	for y := minY; y <= top; y++ {
		s := c.Get(x, y, z)
		if y == top || c.Get(x, y+1, z) != s {
			out = append(out, fmt.Sprintf("%d-%d=%s", start, y, StateName(s)))
			start = y + 1
		}
	}
	return strings.Join(out, " ")
}

// TestNetherAndEndMatchVanilla runs the nether and end generators — the
// nether's legacy-seeded noise and five-point climate list, the End's
// island density and distance/erosion biomes — over the probe's chunks
// and compares every column after the noise fill and after the surface.
func TestNetherAndEndMatchVanilla(t *testing.T) {
	for _, dim := range []struct {
		tag, id string
	}{{".nether", "minecraft:the_nether"}, {".end", "minecraft:the_end"}} {
		t.Run(dim.id, func(t *testing.T) {
			file, err := os.Open("testdata/chunk_fill.tsv.gz")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			zr, err := gzip.NewReader(file)
			if err != nil {
				t.Fatal(err)
			}
			type built struct{ fill, surface *GenChunk }
			gens := map[string]*Generator{}
			fillers := map[string]*NoiseChunkFiller{}
			searchers := map[string]*BiomeSearcher{}
			chunks := map[string]*built{}
			var search *BiomeSearcher
			sc := bufio.NewScanner(zr)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			counts, bad := 0, 0
			for sc.Scan() {
				parts := strings.Split(sc.Text(), "\t")
				kind := parts[0]
				if kind != "fill"+dim.tag && kind != "surface"+dim.tag {
					continue
				}
				seed := parts[1]
				g, ok := gens[seed]
				if !ok {
					g, err = NewDimensionGenerator(dim.id, atoi64(t, seed))
					if err != nil {
						t.Fatal(err)
					}
					gens[seed] = g
					fillers[seed] = NewNoiseChunkFiller(g.rs)
					if search == nil {
						search = g.params.NewSearcher()
					}
					searchers[seed] = search
				}
				var cx, cz, x, z int32
				fmt.Sscanf(parts[2], "%d,%d", &cx, &cz)
				fmt.Sscanf(parts[3], "%d,%d", &x, &z)
				key := seed + "/" + parts[2]
				b, ok := chunks[key]
				if !ok {
					f := fillers[seed]
					region := NewBiomeRegionFrom(g.rs, g.source, searchers[seed], cx, cz)
					blocks, _ := f.Fill(cx, cz)
					b = &built{fill: f.NewGenChunk(cx, cz, blocks)}
					b.surface = f.NewGenChunk(cx, cz, blocks)
					f.BuildSurface(b.surface, region.Block(g.rs.ZoomSeed()))
					chunks[key] = b
				}
				c := b.fill
				if strings.HasPrefix(kind, "surface") {
					c = b.surface
				}
				if got := dimensionRuns(c, 0, 256, x, z); got != parts[4] {
					bad++
					if bad <= 6 {
						t.Errorf("%s seed %s chunk %d,%d column %d,%d:\n  got  %s\n  want %s", kind, seed, cx, cz, x, z, got, parts[4])
					}
				}
				counts++
			}
			if counts != 2*2*6*256 {
				t.Fatalf("checked %d columns", counts)
			}
			if bad > 0 {
				t.Fatalf("%d of %d columns differ", bad, counts)
			}
		})
	}
}
