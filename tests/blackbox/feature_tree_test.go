package blackbox

import (
	"encoding/json"
	"os"
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

// featureParityKeep is featureParity with a placed-feature filter.
func featureParityKeep(t *testing.T, seed int64, areas []terrainArea, keep func(string) bool) {
	t.Helper()
	ov := worldgen.StageOverrides(worldgen.Vanilla(), keep, true, true)
	region := vanillaTerrainFiles(t, seed, stageDatapack(ov), areas)
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
	chunks := g.GenerateArea(order)
	minY, height := g.RS.MinY(), g.RS.Height()
	maxBad := 5
	if v, err := strconv.Atoi(os.Getenv("MCGO_TREE_MAXBAD")); err == nil {
		maxBad = v
	}
	total, orderDep, exempt, badChunks := 0, 0, 0, 0
	for ai, a := range areas {
		for cx := a.x0 + 1; cx < a.x1; cx++ {
			for cz := a.z0 + 1; cz < a.z1; cz++ {
				saved := readSavedChunk(t, region, cx, cz, minY, height)
				p := chunks[[2]int32{cx, cz}]
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
// (reached through the tree selectors) against the real server across
// the forest families of seed 12345.
func TestFeatureTreeParity(t *testing.T) {
	areas := treeAreas
	if s := os.Getenv("MCGO_TREE_AREAS"); s != "" {
		areas = nil
		for _, f := range strings.Split(s, ",") {
			i, _ := strconv.Atoi(f)
			areas = append(areas, treeAreas[i])
		}
	}
	featureParityKeep(t, 12345, areas, keepTreePlaced)
}

var treeAreas = []terrainArea{
	{0, 0, 4, 4}, // plains / forest near spawn
}
