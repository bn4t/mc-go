package worldgen

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// probeLine is one golden from testdata/vanilla_probe.tsv: a kind, the
// fields identifying the sample, and the value vanilla produced.
type probeLine struct {
	kind   string
	fields []string
	value  string
}

func loadProbe(t *testing.T) []probeLine {
	t.Helper()
	f, err := os.Open("testdata/vanilla_probe.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []probeLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		out = append(out, probeLine{parts[0], parts[1 : len(parts)-1], parts[len(parts)-1]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func atoi64(t *testing.T, s string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func parseTriple(t *testing.T, s string) [3]float64 {
	t.Helper()
	parts := strings.Split(s, ",")
	var out [3]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

func newSource(kind string, seed int64) Random {
	if kind == "legacy" {
		return NewLegacy(seed)
	}
	return NewXoroshiro(seed)
}

// TestRandomSourcesMatchVanilla replays each source's draw sequence in
// the probe's order — longs, ints, bounded ints, floats, doubles,
// booleans, gaussians, then positional forks — and compares every value.
func TestRandomSourcesMatchVanilla(t *testing.T) {
	bounds := []int32{1, 2, 7, 100, 256, 1_000_000_007}
	type key struct {
		kind string
		seed int64
	}
	sources := map[key]Random{}
	positional := map[key]Positional{}
	checked := 0
	for _, l := range loadProbe(t) {
		if l.kind != "xoro" && l.kind != "legacy" {
			continue
		}
		k := key{l.kind, atoi64(t, l.fields[0])}
		r, ok := sources[k]
		if !ok {
			r = newSource(k.kind, k.seed)
			sources[k] = r
		}
		op, arg := l.fields[1], l.fields[2]
		var got string
		switch op {
		case "long":
			got = strconv.FormatInt(r.NextLong(), 10)
		case "int":
			got = strconv.FormatInt(int64(r.NextInt()), 10)
		case "intn":
			got = strconv.FormatInt(int64(r.NextIntn(bounds[atoi64(t, arg)])), 10)
		case "float":
			got = strconv.FormatInt(int64(int32(math.Float32bits(r.NextFloat()))), 10)
		case "double":
			got = strconv.FormatInt(int64(math.Float64bits(r.NextDouble())), 10)
		case "bool":
			got = strconv.FormatBool(r.NextBool())
		case "gauss":
			got = strconv.FormatInt(int64(math.Float64bits(r.NextGaussian())), 10)
		case "at", "hash":
			p, ok := positional[k]
			if !ok {
				p = r.ForkPositional()
				positional[k] = p
			}
			if op == "at" {
				c := strings.Split(arg, ",")
				got = strconv.FormatInt(p.At(int32(atoi64(t, c[0])), int32(atoi64(t, c[1])), int32(atoi64(t, c[2]))).NextLong(), 10)
			} else {
				got = strconv.FormatInt(p.FromHash(arg).NextLong(), 10)
			}
		case "fork":
			got = strconv.FormatInt(r.Fork().NextLong(), 10)
		default:
			t.Fatalf("unknown op %q", op)
		}
		if got != l.value {
			t.Errorf("%s seed %d %s %s: got %s want %s", k.kind, k.seed, op, arg, got, l.value)
		}
		checked++
	}
	if checked < 400 {
		t.Fatalf("only %d random goldens checked", checked)
	}
}

// TestNoiseMatchesVanilla compares single improved-noise octaves and the
// registered normal noises (seeded as a world's random state seeds
// them) bit for bit.
func TestNoiseMatchesVanilla(t *testing.T) {
	checked := 0
	improved := map[int64]*Improved{}
	normals := map[string]*Normal{}
	for _, l := range loadProbe(t) {
		var got float64
		switch l.kind {
		case "improved":
			seed := atoi64(t, l.fields[0])
			n, ok := improved[seed]
			if !ok {
				n = NewImproved(NewXoroshiro(seed))
				improved[seed] = n
			}
			p := parseTriple(t, l.fields[1])
			got = n.Noise(p[0], p[1], p[2])
		case "normal":
			seed := atoi64(t, l.fields[0])
			id := "minecraft:" + l.fields[1]
			key := l.fields[0] + "/" + id
			n, ok := normals[key]
			if !ok {
				params, ok := noiseParams[id]
				if !ok {
					t.Fatalf("no noise %s", id)
				}
				n = NewNormal(NewXoroshiro(seed).ForkPositional().FromHash(id), params, false)
				normals[key] = n
			}
			p := parseTriple(t, l.fields[2])
			got = n.Value(p[0], p[1], p[2])
		default:
			continue
		}
		want := math.Float64frombits(uint64(atoi64(t, l.value)))
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("%s %v: got %v want %v", l.kind, l.fields, got, want)
		}
		checked++
	}
	if checked < 300 {
		t.Fatalf("only %d noise goldens checked", checked)
	}
}

func TestLegacySourceIsTheJavaLCG(t *testing.T) {
	if got := NewLegacy(0).NextInt(); got != -1155484576 {
		t.Fatalf("first int of seed 0 = %d", got)
	}
}

// TestRouterMatchesVanilla evaluates every overworld noise router
// function — aquifer and vein noises, climate, terrain shape and the
// final density with caves — at 40 points for each of three seeds
// against the official server.
func TestRouterMatchesVanilla(t *testing.T) {
	settings, ok := Settings("minecraft:overworld")
	if !ok {
		t.Fatal("no overworld settings")
	}
	states := map[int64]*RandomState{}
	checked := 0
	for _, l := range loadProbe(t) {
		if l.kind != "router" {
			continue
		}
		seed := atoi64(t, l.fields[0])
		rs, ok := states[seed]
		if !ok {
			rs = NewRandomState(settings, seed)
			states[seed] = rs
		}
		r := &rs.Router
		fns := map[string]Func{
			"barrier": r.Barrier, "fluid_level_floodedness": r.FluidLevelFloodedness,
			"fluid_level_spread": r.FluidLevelSpread, "lava": r.Lava,
			"continents": r.Continents, "erosion": r.Erosion, "depth": r.Depth,
			"ridges": r.Ridges, "temperature": r.Temperature, "vegetation": r.Vegetation,
			"preliminary_surface_level": r.PreliminarySurfaceLevel, "final_density": r.FinalDensity,
			"vein_toggle": r.VeinToggle, "vein_ridged": r.VeinRidged, "vein_gap": r.VeinGap,
		}
		f, ok := fns[l.fields[1]]
		if !ok {
			t.Fatalf("unknown router function %s", l.fields[1])
		}
		c := strings.Split(l.fields[2], ",")
		p := Point{int32(atoi64(t, c[0])), int32(atoi64(t, c[1])), int32(atoi64(t, c[2]))}
		got := f.Compute(p)
		want := math.Float64frombits(uint64(atoi64(t, l.value)))
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Errorf("seed %d %s at %v: got %v want %v", seed, l.fields[1], p, got, want)
		}
		checked++
	}
	if checked < 1800 {
		t.Fatalf("only %d router goldens checked", checked)
	}
}
