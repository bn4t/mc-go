package blackbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// treeTypes are the feature types this group implements; treeSelectors
// are the selector types trees are reached through.
var (
	treeTypes     = map[string]bool{"tree": true, "fallen_tree": true, "huge_brown_mushroom": true, "huge_red_mushroom": true}
	treeSelectors = map[string]bool{"random_selector": true, "simple_random_selector": true, "random_boolean_selector": true, "sequence": true, "weighted_random_selector": true}
)

// reachableTypes collects every configured feature type a placed
// feature can place through selectors.
func reachableTypes(data worldgen.Data, raw json.RawMessage, out map[string]bool) {
	var name string
	var cf map[string]json.RawMessage
	if json.Unmarshal(raw, &name) == nil {
		b, ok := data.Get("configured_feature/" + strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			return
		}
		json.Unmarshal(b, &cf)
	} else {
		json.Unmarshal(raw, &cf)
	}
	var typ string
	json.Unmarshal(cf["type"], &typ)
	typ = strings.TrimPrefix(typ, "minecraft:")
	out[typ] = true
	var cfg map[string]json.RawMessage
	json.Unmarshal(cf["config"], &cfg)
	placed := func(p json.RawMessage) {
		var pn string
		var po map[string]json.RawMessage
		if json.Unmarshal(p, &pn) == nil {
			b, ok := data.Get("placed_feature/" + strings.TrimPrefix(pn, "minecraft:"))
			if !ok {
				return
			}
			json.Unmarshal(b, &po)
		} else {
			json.Unmarshal(p, &po)
		}
		reachableTypes(data, po["feature"], out)
	}
	switch typ {
	case "random_selector":
		var fs []map[string]json.RawMessage
		json.Unmarshal(cfg["features"], &fs)
		for _, f := range fs {
			placed(f["feature"])
		}
		placed(cfg["default"])
	case "simple_random_selector", "sequence":
		var fs []json.RawMessage
		if json.Unmarshal(cfg["features"], &fs) != nil {
			fs = []json.RawMessage{cfg["features"]}
		}
		for _, f := range fs {
			placed(f)
		}
	case "random_boolean_selector":
		placed(cfg["feature_true"])
		placed(cfg["feature_false"])
	case "weighted_random_selector":
		var fs []map[string]json.RawMessage
		json.Unmarshal(cfg["features"], &fs)
		for _, f := range fs {
			placed(f["data"])
		}
	}
}

// keepTreePlaced keeps placed features that place one of the tree
// types, directly or through selectors, and nothing outside the tree
// types, the selectors and simple_block (the jungle grass under bamboo
// jungle trees).
func keepTreePlaced(p string) bool {
	data := worldgen.Vanilla()
	raw, ok := data.Get("placed_feature/" + p)
	if !ok {
		return false
	}
	var po map[string]json.RawMessage
	json.Unmarshal(raw, &po)
	types := map[string]bool{}
	reachableTypes(data, po["feature"], types)
	mine := false
	for ty := range types {
		switch {
		case treeTypes[ty]:
			mine = true
		case treeSelectors[ty], ty == "simple_block":
		default:
			return false
		}
	}
	return mine
}

// treeParity has the real server decorate the areas with only the
// placed features keep accepts, logging (through a java agent, see
// feature_tree_order_test.go) the order it decorated chunks in, then
// replays that order in mc-go and compares every chunk whose
// neighbours were all decorated block for block.
//
// The order matters for trees: vanilla decorates chunks concurrently,
// and a canopy a neighbour placed first raises a column's OCEAN_FLOOR
// origin onto leaves, so the sapling check drops the tree and every
// later draw of that chunk's random shifts. Vanilla also decorates
// rings of chunks around each forced area (lighting wants decorated
// neighbours) whose trees reach into it, so every logged chunk within
// three of an area is replayed too.
//
// MCGO_TREE_REGION caches the oracle's region files and order log in a
// directory so mc-go can be re-run against one oracle world;
// MCGO_TREE_OVERRIDE merges extra worldgen JSON entries into both
// sides (isolating one tree variant); MCGO_TREE_MAXBAD caps the
// mismatches reported per chunk.
func treeParity(t *testing.T, seed int64, areas []terrainArea, keep func(string) bool) {
	t.Helper()
	ov := worldgen.StageOverrides(worldgen.Vanilla(), keep, true, true)
	if f := os.Getenv("MCGO_TREE_OVERRIDE"); f != "" {
		var extra map[string]json.RawMessage
		b, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(b, &extra) != nil {
			t.Fatalf("bad MCGO_TREE_OVERRIDE %s", f)
		}
		for k, v := range extra {
			ov[k] = v
		}
	}
	runOracle := func() (string, string) {
		agent, hook := buildOrderAgent(t)
		logPath := filepath.Join(t.TempDir(), "order.log")
		t.Setenv("JAVA_TOOL_OPTIONS", strings.TrimSpace(os.Getenv("JAVA_TOOL_OPTIONS")+" -javaagent:"+agent+"="+hook+";"+logPath))
		return vanillaTerrainFiles(t, seed, stageDatapack(ov), areas), logPath
	}
	region := os.Getenv("MCGO_TREE_REGION")
	orderLog := filepath.Join(region, "order.log")
	if region == "" {
		region, orderLog = runOracle()
	} else if _, err := os.Stat(filepath.Join(region, "done")); err != nil {
		src, lp := runOracle()
		os.MkdirAll(region, 0o755)
		ents, _ := os.ReadDir(src)
		for _, e := range ents {
			b, _ := os.ReadFile(filepath.Join(src, e.Name()))
			os.WriteFile(filepath.Join(region, e.Name()), b, 0o644)
		}
		b, _ := os.ReadFile(lp)
		os.WriteFile(orderLog, b, 0o644)
		os.WriteFile(filepath.Join(region, "done"), nil, 0o644)
	}
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	minY, height := g.RS.MinY(), g.RS.Height()
	maxBad := 5
	if v, err := strconv.Atoi(os.Getenv("MCGO_TREE_MAXBAD")); err == nil {
		maxBad = v
	}

	logged, overlaps := readDecorationOrder(t, orderLog)
	near := func(c [2]int32) bool {
		for _, a := range areas {
			if c[0] >= a.x0-3 && c[0] <= a.x1+3 && c[1] >= a.z0-3 && c[1] <= a.z1+3 {
				return true
			}
		}
		return false
	}
	var order [][2]int32
	for _, c := range logged {
		if near(c) {
			order = append(order, c)
		}
	}
	t.Logf("oracle decoration order: %d chunks logged, %d replayed, %d overlapping neighbour decorations", len(logged), len(order), overlaps)

	writes := map[worldgen.BlockPos]map[[2]int32]world.Block{}
	g.WriteLog = func(p worldgen.BlockPos, c [2]int32, b world.Block) {
		m := writes[p]
		if m == nil {
			m = map[[2]int32]world.Block{}
			writes[p] = m
		}
		m[c] = b
	}
	chunks := map[[2]int32]*worldgen.ProtoChunk{}
	for _, c := range order {
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				k := [2]int32{c[0] + dx, c[1] + dz}
				if chunks[k] == nil {
					chunks[k] = g.Terrain(k[0], k[1])
				}
			}
		}
	}
	for _, c := range order {
		g.Decorate(g.NewRegion(chunks[c], chunks))
	}

	total, orderDep, exempt, badChunks := 0, 0, 0, 0
	for ai, a := range areas {
		for cx := a.x0 + 1; cx < a.x1; cx++ {
			for cz := a.z0 + 1; cz < a.z1; cz++ {
				saved := readSavedChunk(t, region, cx, cz, minY, height)
				p := chunks[[2]int32{cx, cz}]
				if p == nil {
					t.Fatalf("chunk %d,%d was not decorated by the oracle", cx, cz)
				}
				bad := 0
				for i, w := range saved.blocks {
					gs := protoStateString(p.Blocks[i])
					if gs == w {
						continue
					}
					pos := worldgen.BlockPos{cx*16 + int32(i&15), int32(i>>8) + minY, cz*16 + int32((i>>4)&15)}
					if m := writes[pos]; len(m) > 1 {
						ok := false
						for _, b := range m {
							ok = ok || protoStateString(b) == w
						}
						if ok {
							orderDep++
							continue
						}
					}
					if gs == "air" && strings.Contains(w, "[level=") {
						exempt++
						continue
					}
					if bad < maxBad {
						t.Errorf("area %d (%d,%d,%d): mc-go %s, vanilla %s", ai, pos[0], pos[1], pos[2], gs, w)
					}
					bad++
				}
				if bad > 0 {
					t.Errorf("chunk %d,%d: %d mismatched blocks", cx, cz, bad)
					badChunks++
				}
				total++
			}
		}
	}
	if len(worldgen.UnsupportedFeatures) > 0 {
		t.Errorf("unsupported feature types placed nothing: %v", worldgen.UnsupportedFeatures)
	}
	if n := worldgen.JSetTieBreaks.Load(); n > 0 {
		t.Logf("%d tree-bin tie breaks (identity-hash order not modeled)", n)
	}
	t.Logf("%d chunks compared (%d mismatched); %d order-dependent cells, %d fluid post-processing cells accepted", total, badChunks, orderDep, exempt)
}

// TestFeatureTreeParity pins tree, fallen_tree and the huge mushrooms
// (with the selectors that reach them) against the real server across
// the forest families of seed 12345. MCGO_TREE_AREAS picks a subset of
// treeAreas by index.
func TestFeatureTreeParity(t *testing.T) {
	idx := make([]int, len(treeAreas))
	for i := range idx {
		idx[i] = i
	}
	if s := os.Getenv("MCGO_TREE_AREAS"); s != "" {
		idx = nil
		for _, f := range strings.Split(s, ",") {
			i, _ := strconv.Atoi(f)
			idx = append(idx, i)
		}
	}
	// One oracle server per group of four areas keeps its heap small.
	cache := os.Getenv("MCGO_TREE_REGION")
	for len(idx) > 0 {
		n := min(4, len(idx))
		group := idx[:n]
		idx = idx[n:]
		var areas []terrainArea
		name := "areas"
		for _, i := range group {
			areas = append(areas, treeAreas[i])
			name += "_" + strconv.Itoa(i)
		}
		t.Run(name, func(t *testing.T) {
			if cache != "" {
				t.Setenv("MCGO_TREE_REGION", filepath.Join(cache, name))
			}
			treeParity(t, 12345, areas, keepTreePlaced)
		})
	}
}

var treeAreas = []terrainArea{
	{-41, 7, -37, 11},        // 0 forest
	{-33, -33, -29, -29},     // 1 birch forest
	{-41, -33, -37, -29},     // 2 jungle
	{39, 39, 43, 43},         // 3 snowy taiga
	{47, 23, 51, 27},         // 4 taiga
	{-97, -145, -93, -141},   // 5 dark forest
	{-161, 39, -157, 43},     // 6 mangrove swamp
	{-89, -1, -85, 3},        // 7 bamboo jungle
	{255, 279, 259, 283},     // 8 mushroom fields
	{-209, -593, -205, -589}, // 9 pale garden (carpets need the vegetation group's MossyCarpetBlock.placeAt)
	{319, 111, 323, 115},     // 10 cherry grove
	{-81, -97, -77, -93},     // 11 savanna
	{-121, -105, -117, -101}, // 12 swamp
	{263, -17, 267, -13},     // 13 meadow
	{-25, -185, -21, -181},   // 14 windswept hills
	{39, -233, 43, -229},     // 15 windswept forest
	{-241, -153, -237, -149}, // 16 old growth pine taiga
	{-241, -137, -237, -133}, // 17 old growth spruce taiga
	{0, 0, 4, 4},             // 18 plains near spawn
}
