package worldgen

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// structureGoldens are the structure probe's records, by kind, each a
// record's tab-separated fields after the kind.
var structureGoldens = sync.OnceValues(func() (map[string][][]string, error) {
	file, err := os.Open("testdata/structures.tsv.gz")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	out := map[string][][]string{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		out[f[0]] = append(out[f[0]], f[1:])
	}
	return out, sc.Err()
})

func loadStructureGoldens(t *testing.T) map[string][][]string {
	t.Helper()
	g, err := structureGoldens()
	if err != nil {
		t.Fatal(err)
	}
	return g
}

var probeGenerators sync.Map

// probeGenerator is the generator of a probed dimension for seed 12345.
func probeGenerator(t *testing.T, dim string) *Generator {
	t.Helper()
	if g, ok := probeGenerators.Load(dim); ok {
		return g.(*Generator)
	}
	g, err := NewDimensionGenerator("minecraft:"+dim, 12345)
	if err != nil {
		t.Fatal(err)
	}
	g2, _ := probeGenerators.LoadOrStore(dim, g)
	return g2.(*Generator)
}

func atoi32(t *testing.T, s string) int32 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	return int32(v)
}

// TestStructureSetsMatchVanilla checks each dimension holds the structure
// sets vanilla's does, that every set picks the chunks vanilla's picks
// in the probed square, and the stronghold rings.
func TestStructureSetsMatchVanilla(t *testing.T) {
	g := loadStructureGoldens(t)
	radius := map[string]int32{"overworld": 160, "the_nether": 64, "the_end": 160}
	wantSets := map[string][]string{}
	for _, r := range g["set"] {
		wantSets[r[0]] = append(wantSets[r[0]], r[1])
	}
	wantChunks := map[string]map[[2]int32]bool{}
	for _, r := range g["chunk"] {
		k := r[0] + " " + r[1]
		if wantChunks[k] == nil {
			wantChunks[k] = map[[2]int32]bool{}
		}
		wantChunks[k][[2]int32{atoi32(t, r[2]), atoi32(t, r[3])}] = true
	}
	for dim, sets := range wantSets {
		p := probeGenerator(t, dim).structures()
		var got []string
		for _, s := range p.sets {
			got = append(got, s.name)
		}
		if strings.Join(got, " ") != strings.Join(sets, " ") {
			t.Errorf("%s sets: got %v, want %v", dim, got, sets)
			continue
		}
		for _, s := range p.sets {
			want := wantChunks[dim+" "+s.name]
			n := radius[dim]
			var missing, extra []string
			for cx := -n; cx < n; cx++ {
				for cz := -n; cz < n; cz++ {
					c := [2]int32{cx, cz}
					if is := p.isStructureChunk(s, cx, cz); is != want[c] {
						if is {
							extra = append(extra, fmt.Sprint(c))
						} else {
							missing = append(missing, fmt.Sprint(c))
						}
					}
				}
			}
			if len(missing)+len(extra) > 0 {
				t.Errorf("%s %s: %d chunks missing (%v), %d extra (%v)", dim, s.name, len(missing), first(missing, 5), len(extra), first(extra, 5))
			}
		}
	}
	p := probeGenerator(t, "overworld").structures()
	p.ensureRings()
	for _, r := range g["ring"] {
		i := atoi32(t, r[2])
		want := [2]int32{atoi32(t, r[3]), atoi32(t, r[4])}
		if got := p.rings[r[1]][i]; got != want {
			t.Errorf("%s %s ring position %d: got %v, want %v", r[0], r[1], i, got, want)
		}
	}
	if n := len(p.rings["minecraft:strongholds"]); n != 128 {
		t.Errorf("%d stronghold positions, want 128", n)
	}
}

func first(s []string, n int) []string {
	sort.Strings(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// TestBaseHeightsMatchVanilla checks the noise-only heights and columns
// structures measure the ground with, in all three dimensions.
func TestBaseHeightsMatchVanilla(t *testing.T) {
	g := loadStructureGoldens(t)
	bad := 0
	for _, r := range g["height"] {
		gen := probeGenerator(t, r[0])
		x, z := atoi32(t, r[1]), atoi32(t, r[2])
		ws, of := gen.baseHeight(x, z, worldSurfaceWG), gen.baseHeight(x, z, oceanFloorWG)
		if want := r[3] + " " + r[4]; fmt.Sprint(ws, " ", of) != want {
			if bad++; bad <= 10 {
				t.Errorf("%s %d,%d: surface and ocean floor %d %d, want %s", r[0], x, z, ws, of, want)
			}
		}
	}
	for _, r := range g["column"] {
		gen := probeGenerator(t, r[0])
		x, z := atoi32(t, r[1]), atoi32(t, r[2])
		var runs []string
		last, count := "", 0
		// The nether and End levels reach above their noise, and read air there.
		col := gen.baseColumn(x, z)
		if r[0] != "overworld" {
			for len(col) < 256 {
				col = append(col, stAir)
			}
		}
		for _, s := range col {
			name, _, _ := strings.Cut(strings.TrimPrefix(StateName(s), "minecraft:"), "[")
			if name == last {
				count++
				continue
			}
			if last != "" {
				runs = append(runs, fmt.Sprintf("%s*%d", last, count))
			}
			last, count = name, 1
		}
		runs = append(runs, fmt.Sprintf("%s*%d", last, count))
		if got := strings.Join(runs, " "); got != r[3] {
			if bad++; bad <= 10 {
				t.Errorf("%s column %d,%d:\n got %s\nwant %s", r[0], x, z, got, r[3])
			}
		}
	}
}

// TestStructureStartsMatchVanilla builds the structure starts of every
// chunk the probe ran the structure-starts stage on and compares each
// start's save tag with vanilla's, for the structure types implemented.
func TestStructureStartsMatchVanilla(t *testing.T) {
	g := loadStructureGoldens(t)
	type chunkKey struct {
		dim    string
		cx, cz int32
	}
	picked := map[chunkKey]bool{}
	for _, r := range g["probed"] {
		picked[chunkKey{r[0], atoi32(t, r[1]), atoi32(t, r[2])}] = true
	}
	want := map[chunkKey]map[string]string{}
	for _, r := range g["start"] {
		k := chunkKey{r[0], atoi32(t, r[1]), atoi32(t, r[2])}
		if want[k] == nil {
			want[k] = map[string]string{}
		}
		want[k][r[3]] = r[4]
	}
	// A set's weighted pick only matches where every structure it can
	// pick is implemented.
	implemented := func(name string) bool {
		for _, set := range structureSets() {
			for _, e := range set.entries {
				if e.structure != name {
					continue
				}
				for _, o := range set.entries {
					if _, ok := structureGenerators[structureDefs()[o.structure].kind]; !ok {
						return false
					}
				}
			}
		}
		return true
	}
	var keys []chunkKey
	for k := range picked {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.dim != b.dim {
			return a.dim < b.dim
		}
		if a.cx != b.cx {
			return a.cx < b.cx
		}
		return a.cz < b.cz
	})
	// The chunks are built in parallel, then compared in order.
	built := make([]map[string]string, len(keys))
	for _, k := range keys {
		probeGenerator(t, k.dim)
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < runtime.GOMAXPROCS(0); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				k := keys[i]
				got := map[string]string{}
				for _, s := range probeGenerator(t, k.dim).createStructures(k.cx, k.cz) {
					got[s.structure] = snbt(s.tag())
				}
				built[i] = got
			}
		}()
	}
	for i := range keys {
		next <- i
	}
	close(next)
	wg.Wait()
	matched := map[string]int{}
	failed := 0
	for i, k := range keys {
		got := built[i]
		names := map[string]bool{}
		for n := range want[k] {
			names[n] = true
		}
		for n := range got {
			names[n] = true
		}
		for n := range names {
			if !implemented(n) {
				continue
			}
			w, g := want[k][n], got[n]
			if w == g {
				matched[n]++
				continue
			}
			if failed++; failed <= 8 {
				if w == "" {
					w = "(none)"
				}
				if g == "" {
					g = "(none)"
				}
				t.Errorf("%s chunk %d,%d %s:\n got %s\nwant %s", k.dim, k.cx, k.cz, n, first1(g, 600), first1(w, 600))
			}
		}
	}
	t.Logf("matching starts by structure: %v", matched)
	if failed > 0 {
		t.Errorf("%d starts differ", failed)
	}
}

func first1(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
