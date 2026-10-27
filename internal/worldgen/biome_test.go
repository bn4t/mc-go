package worldgen

import (
	"strconv"
	"strings"
	"testing"
)

// TestBiomesMatchVanilla checks the overworld multi-noise biome at 600
// quart positions per seed, the zoom seed, and the block-level zoom at
// 400 block positions per seed against the official server.
func TestBiomesMatchVanilla(t *testing.T) {
	settings, _ := Settings("minecraft:overworld")
	params, ok := BiomeParameters("minecraft:overworld")
	if !ok {
		t.Fatal("no overworld parameter list")
	}
	states := map[int64]*RandomState{}
	state := func(seed int64) *RandomState {
		rs, ok := states[seed]
		if !ok {
			rs = NewRandomState(settings, seed)
			states[seed] = rs
		}
		return rs
	}
	triple := func(s string) (a, b, c int32) {
		f := strings.Split(s, ",")
		return int32(atoi64(t, f[0])), int32(atoi64(t, f[1])), int32(atoi64(t, f[2]))
	}
	// One searcher for the whole file: the probe asked its source in this
	// order on one thread, each search seeded with the previous answer.
	search := params.NewSearcher()
	counts := map[string]int{}
	for _, l := range loadProbe(t) {
		switch l.kind {
		case "biome":
			seed := atoi64(t, l.fields[0])
			qx, qy, qz := triple(l.fields[1])
			if got := search.Find(state(seed).Climate(qx, qy, qz)); got != l.value {
				t.Errorf("seed %d quart %d,%d,%d: %s want %s", seed, qx, qy, qz, got, l.value)
			}
		case "zoomseed":
			seed := atoi64(t, l.fields[0])
			if got := strconv.FormatInt(ZoomSeed(seed), 10); got != l.value {
				t.Errorf("zoom seed of %d: %s want %s", seed, got, l.value)
			}
		case "zoom":
			seed := atoi64(t, l.fields[0])
			x, y, z := triple(l.fields[1])
			rs := state(seed)
			got := ZoomBiome(ZoomSeed(seed), x, y, z, func(qx, qy, qz int32) string {
				return search.Find(rs.Climate(qx, qy, qz))
			})
			if got != l.value {
				t.Errorf("seed %d block %d,%d,%d: %s want %s", seed, x, y, z, got, l.value)
			}
		default:
			continue
		}
		counts[l.kind]++
	}
	if counts["biome"] != 1800 || counts["zoom"] != 1200 || counts["zoomseed"] != 3 {
		t.Fatalf("goldens checked: %v", counts)
	}
}
