package worldgen

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

type featureGolden struct {
	dim, seed, chunk string
	step, index      int
	name, status     string
	delta            string
}

func loadFeatureGoldens(t *testing.T) (steps map[string][][]string, features []featureGolden) {
	t.Helper()
	file, err := os.Open("testdata/features.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	steps = map[string][][]string{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<24), 1<<24)
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		switch f[0] {
		case "steps":
			var step, index int
			fmt.Sscan(f[2], &step)
			fmt.Sscan(f[3], &index)
			for len(steps[f[1]]) <= step {
				steps[f[1]] = append(steps[f[1]], nil)
			}
			if len(steps[f[1]][step]) != index {
				t.Fatalf("steps out of order at %v", f)
			}
			steps[f[1]][step] = append(steps[f[1]][step], f[4])
		case "feature":
			g := featureGolden{dim: f[1], seed: f[2], chunk: f[3], name: f[6], status: f[7], delta: f[8]}
			fmt.Sscan(f[4], &g.step)
			fmt.Sscan(f[5], &g.index)
			features = append(features, g)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return steps, features
}

// featureDims are the probed dimensions' generator settings.
var featureDims = map[string]struct {
	settings string
	source   func() (BiomeSource, *MultiNoiseBiomes)
}{
	"overworld": {"minecraft:overworld", func() (BiomeSource, *MultiNoiseBiomes) {
		p, _ := BiomeParameters("minecraft:overworld")
		return p, p
	}},
	"the_nether": {"minecraft:nether", func() (BiomeSource, *MultiNoiseBiomes) {
		p, _ := BiomeParameters("minecraft:nether")
		return p, p
	}},
	"the_end": {"minecraft:end", func() (BiomeSource, *MultiNoiseBiomes) {
		p, _ := BiomeParameters("minecraft:overworld")
		return EndBiomeSource{}, p
	}},
}

// TestFeatureOrderMatchesVanilla checks each dimension's global
// per-step feature order — the index every placed feature is seeded
// with — against the generator's own.
func TestFeatureOrderMatchesVanilla(t *testing.T) {
	steps, _ := loadFeatureGoldens(t)
	for dim, want := range steps {
		src, _ := featureDims[dim].source()
		got := featureSteps(possibleBiomes(src))
		if len(got) != len(want) {
			t.Errorf("%s: %d steps, want %d", dim, len(got), len(want))
			continue
		}
		for s := range want {
			var names []string
			for _, f := range got[s] {
				names = append(names, f.name)
			}
			if strings.Join(names, " ") != strings.Join(want[s], " ") {
				t.Errorf("%s step %d:\n  got  %v\n  want %v", dim, s, names, want[s])
			}
		}
	}
}

// buildDecoRegion builds the probe's region around (cx, cz): stored
// biomes for the 5×5 chunks, then the noise fill, surface and carvers for
// each of the middle 3×3, in the probe's order.
func buildDecoRegion(rs *RandomState, src BiomeSource, search *BiomeSearcher, f *NoiseChunkFiller, cx, cz int32) *decoRegion {
	r := &decoRegion{rs: rs, seed: rs.Seed, cx: cx, cz: cz, zoomSeed: rs.ZoomSeed(),
		genMinY: int32(rs.Settings.MinY), genDepth: int32(rs.Settings.Height), seaLevel: int32(rs.Settings.SeaLevel)}
	for dx := int32(-2); dx <= 2; dx++ {
		for dz := int32(-2); dz <= 2; dz++ {
			r.biomes[dx+2][dz+2] = FillChunkBiomesFrom(rs, src, search, cx+dx, cz+dz)
		}
	}
	stored := func(x, y, z int32) string { return ZoomBiome(r.zoomSeed, x, y, z, r.quartBiome) }
	direct := func(qx, qy, qz int32) string { return biomeNameTable[src.biomeIndex(rs, search, qx, qy, qz)] }
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			blocks, _ := f.Fill(cx+dx, cz+dz)
			c := f.NewGenChunk(cx+dx, cz+dz, blocks)
			f.BuildSurface(c, stored)
			f.Carve(c, func(sx, sz int32) string { return direct(sx*4, 0, sz*4) },
				func(x, y, z int32) string { return ZoomBiome(r.zoomSeed, x, y, z, direct) })
			c.freezeWorldgenHeights()
			r.chunks[dx+1][dz+1] = c
		}
	}
	r.minY, r.height = r.chunks[1][1].MinY, r.chunks[1][1].Height
	return r
}

// regionSnapshot copies the 3×3's blocks and heightmaps.
type regionSnapshot struct {
	states  [9][]StateID
	heights [9]*finalHeights
}

func snapshotRegion(r *decoRegion) regionSnapshot {
	var s regionSnapshot
	for i := 0; i < 9; i++ {
		c := r.chunks[i/3][i%3]
		s.states[i] = append([]StateID(nil), c.States...)
		if c.heights != nil {
			h := *c.heights
			for k, m := range h.maps {
				if m != nil {
					cp := *m
					h.maps[k] = &cp
				}
			}
			s.heights[i] = &h
		}
	}
	return s
}

func (s regionSnapshot) restore(r *decoRegion) {
	for i := 0; i < 9; i++ {
		c := r.chunks[i/3][i%3]
		copy(c.States, s.states[i])
		c.heights = s.heights[i]
		if c.heights != nil {
			h := *c.heights
			for k, m := range h.maps {
				if m != nil {
					cp := *m
					h.maps[k] = &cp
				}
			}
			c.heights = &h
		}
	}
}

// regionDelta lists the blocks that differ from a snapshot as the probe
// prints them, in dimension build-height coordinates.
func regionDelta(r *decoRegion, before regionSnapshot, dimMinY, dimHeight int32) string {
	var parts []string
	for i := 0; i < 9; i++ {
		c := r.chunks[i/3][i%3]
		minX, minZ := c.CX*16, c.CZ*16
		for y := dimMinY; y < dimMinY+dimHeight; y++ {
			for z := int32(0); z < 16; z++ {
				for x := int32(0); x < 16; x++ {
					if y < c.MinY || y >= c.MinY+c.Height {
						continue
					}
					j := c.index(x, y, z)
					if c.States[j] != before.states[i][j] {
						parts = append(parts, fmt.Sprintf("%d,%d,%d=%s", minX+x, y, minZ+z, StateName(c.States[j])))
					}
				}
			}
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// applyDelta writes a probe delta into the region: straight into the
// sections for the ore features, as a generation region write otherwise.
func applyDelta(r *decoRegion, delta string, raw bool) {
	if delta == "-" {
		return
	}
	for _, e := range strings.Split(delta, " ") {
		pos, state, _ := strings.Cut(e, "=")
		var x, y, z int32
		fmt.Sscanf(pos, "%d,%d,%d", &x, &y, &z)
		c := r.chunk(x, z)
		s := InternState(state)
		if raw {
			c.setRaw(x&15, y, z&15, s)
		} else {
			c.setLive(x&15, y, z&15, s)
		}
	}
}

// TestFeaturesMatchVanilla decorates the probe's chunks feature by
// feature. Each feature whose type is implemented runs and must change
// exactly the blocks vanilla's did; every feature's vanilla changes are
// then applied, so the next feature starts from vanilla's state.
func TestFeaturesMatchVanilla(t *testing.T) {
	_, goldens := loadFeatureGoldens(t)
	type group struct {
		dim, seed, chunk string
		features         []featureGolden
	}
	var groups []*group
	for _, g := range goldens {
		if len(groups) == 0 || groups[len(groups)-1].chunk != g.chunk || groups[len(groups)-1].dim != g.dim {
			groups = append(groups, &group{dim: g.dim, seed: g.seed, chunk: g.chunk})
		}
		last := groups[len(groups)-1]
		last.features = append(last.features, g)
	}
	searchers := map[string]*BiomeSearcher{}
	matched, mismatched := map[string]int{}, map[string]int{}
	skipped := map[string]int{}
	for _, gr := range groups {
		dim := featureDims[gr.dim]
		settings, _ := Settings(dim.settings)
		src, params := dim.source()
		if searchers[gr.dim] == nil {
			searchers[gr.dim] = params.NewSearcher()
		}
		rs := NewRandomState(settings, atoi64(t, gr.seed))
		f := NewNoiseChunkFiller(rs)
		var cx, cz int32
		fmt.Sscanf(gr.chunk, "%d,%d", &cx, &cz)
		r := buildDecoRegion(rs, src, searchers[gr.dim], f, cx, cz)
		dimMinY, dimHeight := r.minY, r.height
		if gr.dim != "overworld" {
			dimMinY, dimHeight = 0, 256
		}
		steps := featureSteps(possibleBiomes(src))
		next := 0
		r.decorate(steps, func(step, index int, pf *placedFeature, place func() bool) {
			if next >= len(gr.features) {
				t.Fatalf("%s %s: more features than the probe ran", gr.dim, gr.chunk)
			}
			want := gr.features[next]
			next++
			if want.step != step || want.index != index || want.name != pf.name {
				t.Fatalf("%s %s: ran %s (%d/%d), probe ran %s (%d/%d)", gr.dim, gr.chunk, pf.name, step, index, want.name, want.step, want.index)
			}
			before := snapshotRegion(r)
			kind := pf.feature.kind
			ok := func() (ok bool) {
				defer func() {
					if e := recover(); e != nil {
						if u, isU := e.(unsupportedFeature); isU {
							skipped[u.what]++
							ok = false
							return
						}
						panic(e)
					}
				}()
				place()
				return true
			}()
			if ok {
				got := regionDelta(r, before, dimMinY, dimHeight)
				if got == want.delta {
					matched[kind]++
				} else {
					mismatched[kind]++
					if mismatched[kind] <= 3 {
						t.Errorf("%s %s %s: delta differs\n  got  %.400s\n  want %.400s", gr.dim, gr.chunk, pf.name, got, want.delta)
					}
				}
			}
			before.restore(r)
			applyDelta(r, want.delta, kind == "minecraft:ore")
		})
		if next != len(gr.features) {
			t.Fatalf("%s %s: ran %d features, probe ran %d", gr.dim, gr.chunk, next, len(gr.features))
		}
	}
	report := func(label string, m map[string]int) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Logf("%s %-40s %d", label, k, m[k])
		}
	}
	report("match", matched)
	report("DIFF ", mismatched)
	report("skip ", skipped)
}
