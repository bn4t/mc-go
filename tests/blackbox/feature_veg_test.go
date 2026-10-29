//go:build blackbox

package blackbox

import (
	"encoding/json"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// vegFeatureTypes collects every configured feature type a placed
// feature can reach: its own and those of the placed features nested in
// its config (selectors, patches, root systems).
func vegFeatureTypes(data worldgen.Data, placed string) map[string]bool {
	out := map[string]bool{}
	var walkPlaced func(raw json.RawMessage)
	var walkConfigured func(raw json.RawMessage)
	var walk func(v any)
	resolve := func(dir, name string) json.RawMessage {
		raw, ok := data.Get(dir + "/" + strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			return nil
		}
		return raw
	}
	walkPlaced = func(raw json.RawMessage) {
		var name string
		if json.Unmarshal(raw, &name) == nil {
			if r := resolve("placed_feature", name); r != nil {
				walkPlaced(r)
			}
			return
		}
		var o map[string]json.RawMessage
		json.Unmarshal(raw, &o)
		walkConfigured(o["feature"])
	}
	walkConfigured = func(raw json.RawMessage) {
		var name string
		if json.Unmarshal(raw, &name) == nil {
			if r := resolve("configured_feature", name); r != nil {
				walkConfigured(r)
			}
			return
		}
		var o map[string]any
		json.Unmarshal(raw, &o)
		ty, _ := o["type"].(string)
		out[strings.TrimPrefix(ty, "minecraft:")] = true
		walk(o["config"])
	}
	placedKeys := map[string]bool{"feature": true, "features": true, "default": true, "feature_true": true,
		"feature_false": true, "vegetation_feature": true}
	var walkKey func(k string, v any)
	walkKey = func(k string, v any) {
		switch x := v.(type) {
		case string:
			if placedKeys[k] {
				walkPlaced(json.RawMessage(`"` + x + `"`))
			}
		case []any:
			for _, e := range x {
				walkKey(k, e)
			}
		case map[string]any:
			if _, ok := x["placement"]; ok {
				if _, ok := x["feature"]; ok {
					enc, _ := json.Marshal(x)
					walkPlaced(enc)
					return
				}
			}
			walk(x)
		}
	}
	walk = func(v any) {
		if m, ok := v.(map[string]any); ok {
			for k, e := range m {
				walkKey(k, e)
			}
		}
	}
	walkPlaced(json.RawMessage(`"` + placed + `"`))
	return out
}

// vegParity is featureParity keyed on the whole set of feature types a
// placed feature can reach: a placed feature is kept only when every
// type it nests is allowed, so selectors that also hold trees or other
// groups' features stay out. extra overrides worldgen entries on both
// servers.
func vegParity(t *testing.T, seed int64, areas []terrainArea, extra map[string]string, allowed ...string) {
	t.Helper()
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	extraRaw := map[string]json.RawMessage{}
	for k, v := range extra {
		extraRaw[k] = json.RawMessage(v)
	}
	base := worldgen.Overlay(worldgen.Vanilla(), extraRaw)
	var kept []string
	keep := func(p string) bool {
		ts := vegFeatureTypes(base, p)
		if len(ts) == 0 {
			return false
		}
		for ty := range ts {
			if !ok[ty] {
				return false
			}
		}
		kept = append(kept, p)
		return true
	}
	ov := worldgen.StageOverrides(base, keep, true, true)
	for k, v := range extraRaw {
		ov[k] = v
	}
	seen := map[string]bool{}
	var uniq []string
	for _, k := range kept {
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, k)
		}
	}
	t.Logf("kept placed features: %v", uniq)
	region := vegVanillaRegion(t, seed, stageDatapack(ov), areas)
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	writes := map[worldgen.BlockPos]map[[2]int32]world.Block{}
	g.WriteLog = func(p worldgen.BlockPos, c [2]int32, b world.Block) {
		m := writes[p]
		if m == nil {
			m = map[[2]int32]world.Block{}
			writes[p] = m
		}
		m[c] = b
	}
	var order [][2]int32
	for _, a := range areas {
		for cz := a.z0; cz <= a.z1; cz++ {
			for cx := a.x0; cx <= a.x1; cx++ {
				order = append(order, [2]int32{cx, cz})
			}
		}
	}
	// Vanilla decorates chunks concurrently, so a feature that reads the
	// heightmap or the blocks a neighbour's features wrote (patch
	// origins, air checks) sees a schedule-dependent world. mc-go
	// decorates the same terrain in several orders; a cell passes when
	// vanilla's block is what one of those orders produced.
	terrain := map[[2]int32]*worldgen.ProtoChunk{}
	for _, c := range order {
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				k := [2]int32{c[0] + dx, c[1] + dz}
				if terrain[k] == nil {
					terrain[k] = g.Terrain(k[0], k[1])
				}
			}
		}
	}
	var interior [][2]int32
	for _, a := range areas {
		for cx := a.x0 + 1; cx < a.x1; cx++ {
			for cz := a.z0 + 1; cz < a.z1; cz++ {
				interior = append(interior, [2]int32{cx, cz})
			}
		}
	}
	var chunks map[[2]int32]*worldgen.ProtoChunk
	var alt []map[[2]int32][]world.Block
	for oi := 0; oi < vegOrders; oi++ {
		ord := slices.Clone(order)
		if oi > 0 {
			rnd := rand.New(rand.NewPCG(uint64(oi), 7))
			rnd.Shuffle(len(ord), func(i, j int) { ord[i], ord[j] = ord[j], ord[i] })
		}
		cs := make(map[[2]int32]*worldgen.ProtoChunk, len(terrain))
		for k, p := range terrain {
			q := *p
			q.Blocks = slices.Clone(p.Blocks)
			q.PostProcess = slices.Clone(p.PostProcess)
			cs[k] = &q
		}
		for _, c := range ord {
			g.Decorate(g.NewRegion(cs[c], cs))
		}
		if oi == 0 {
			chunks = cs
			continue
		}
		m := map[[2]int32][]world.Block{}
		for _, k := range interior {
			m[k] = cs[k].Blocks
		}
		alt = append(alt, m)
	}
	minY, height := g.RS.MinY(), g.RS.Height()
	total, orderDep, exempt, written := 0, 0, 0, 0
	for _, a := range areas {
		for cx := a.x0 + 1; cx < a.x1; cx++ {
			for cz := a.z0 + 1; cz < a.z1; cz++ {
				saved := readSavedChunk(t, region, cx, cz, minY, height)
				p := chunks[[2]int32{cx, cz}]
				bad := 0
				for i, w := range saved.blocks {
					pos := worldgen.BlockPos{cx*16 + int32(i&15), int32(i>>8) + minY, cz*16 + int32((i>>4)&15)}
					if writes[pos] != nil {
						written++
					}
					gs := protoStateString(p.Blocks[i])
					if gs == w {
						continue
					}
					ok := false
					if m := writes[pos]; len(m) > 1 {
						for _, b := range m {
							ok = ok || protoStateString(b) == w
						}
					}
					for _, m := range alt {
						ok = ok || protoStateString(m[[2]int32{cx, cz}][i]) == w
					}
					if ok {
						orderDep++
						continue
					}
					if gs == "air" && strings.Contains(w, "[level=") {
						exempt++
						continue
					}
					if bad < 8 {
						t.Errorf("(%d,%d,%d): mc-go %s, vanilla %s", pos[0], pos[1], pos[2], gs, w)
					}
					bad++
				}
				if bad > 0 {
					t.Errorf("chunk %d,%d: %d mismatched blocks", cx, cz, bad)
				}
				total++
			}
		}
	}
	if len(worldgen.UnsupportedFeatures) > 0 {
		t.Errorf("unsupported feature types placed nothing: %v", worldgen.UnsupportedFeatures)
	}
	if len(worldgen.UnsupportedSurvive) > 0 {
		t.Errorf("canSurvive not modeled for: %v", worldgen.UnsupportedSurvive)
	}
	t.Logf("%d chunks compared, %d feature-written cells; %d order-dependent cells, %d fluid post-processing cells accepted",
		total, written, orderDep, exempt)
}

// vegOrders is how many decoration orders mc-go tries per comparison.
const vegOrders = 12

// vegSurface are the selector types surface vegetation is reached
// through, plus the plain placers.
var vegSurface = []string{"simple_block", "block_column", "random_selector", "simple_random_selector",
	"random_boolean_selector", "sequence", "weighted_random_selector", "bamboo", "vegetation_patch", "vines"}

// TestFeatureVegSurfaceParity pins surface vegetation — simple_block
// grass, ferns, flowers, flower beds, leaf litter, dry grass, bushes,
// berries, pumpkins, melons, lily pads, eyeblossoms and pale moss
// carpets (MossyCarpetBlock.placeAt), block_column cactus and sugar
// cane, bamboo with podzol, jungle vines and pale moss patches — in
// plains, flower forest, sunflower plains, meadow, desert, badlands,
// swamp, jungle, bamboo jungle, cherry grove, pale garden, dark forest
// and sparse jungle.
func TestFeatureVegSurfaceParity(t *testing.T) {
	var areas []terrainArea
	for _, c := range [][2]int32{{-32, 40}, {-176, -136}, {-64, -240}, {312, 96}, {-144, 32}, {-328, -280},
		{-128, -104}, {-64, 24}, {-104, -88}, {-368, -8}, {-216, -600}, {-136, -152}, {-80, -24}} {
		areas = append(areas, terrainArea{c[0], c[1], c[0] + 3, c[1] + 3})
	}
	vegParity(t, 12345, areas, nil, vegSurface...)
}

// vegVanillaRegion runs the oracle; with MCGO_VEG_CACHE set it reuses
// (or stores) the region files per test name, for iterating on the Go
// side without re-running the server.
func vegVanillaRegion(t *testing.T, seed int64, files map[string]string, areas []terrainArea) string {
	t.Helper()
	cache := os.Getenv("MCGO_VEG_CACHE")
	if cache == "" {
		return vanillaTerrainFiles(t, seed, files, areas)
	}
	dir := filepath.Join(cache, strings.ReplaceAll(t.Name(), "/", "_"))
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	region := vanillaTerrainFiles(t, seed, files, areas)
	if err := os.CopyFS(dir, os.DirFS(region)); err != nil {
		t.Fatal(err)
	}
	return region
}
