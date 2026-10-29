package blackbox

import (
	"encoding/json"
	"fmt"
	"math/rand"
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

// featureParityKeep is featureParity with a placed-feature filter.
func featureParityKeep(t *testing.T, seed int64, areas []terrainArea, keep func(string) bool) {
	t.Helper()
	ov := worldgen.StageOverrides(worldgen.Vanilla(), keep, true, true)
	// MCGO_TREE_OVERRIDE names a JSON object of extra worldgen entries
	// ("configured_feature/x": {...}) applied to both servers, for
	// isolating one tree variant while debugging.
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
	// MCGO_TREE_REGION names a directory caching the oracle's region
	// files for these areas, so mc-go can be re-run against one oracle
	// world while iterating.
	// The oracle runs with a java agent logging its chunk decoration
	// order (feature_tree_order_test.go).
	agentTrace := ""
	if s := os.Getenv("MCGO_TREE_AGENT_TRACE"); s != "" {
		if strings.HasPrefix(s, "@") {
			b, _ := os.ReadFile(s[1:])
			s = strings.TrimSpace(string(b))
		}
		agentTrace = ";" + s
	}
	runOracle := func() (string, string) {
		agent, hook := buildOrderAgent(t)
		logPath := filepath.Join(t.TempDir(), "order.log")
		t.Setenv("JAVA_TOOL_OPTIONS", strings.TrimSpace(os.Getenv("JAVA_TOOL_OPTIONS")+" -javaagent:"+agent+"="+hook+";"+logPath+agentTrace))
		return vanillaTerrainFiles(t, seed, stageDatapack(ov), areas), logPath
	}
	region := os.Getenv("MCGO_TREE_REGION")
	var orderLog string
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
		os.WriteFile(filepath.Join(region, "order.log"), b, 0o644)
		os.WriteFile(filepath.Join(region, "done"), nil, 0o644)
		orderLog = filepath.Join(region, "order.log")
	} else if _, err := os.Stat(filepath.Join(region, "order.log")); err == nil {
		orderLog = filepath.Join(region, "order.log")
	}
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	if s := os.Getenv("MCGO_TREE_TRACE"); s != "" {
		f := strings.Split(s, ",")
		x, _ := strconv.Atoi(f[0])
		z, _ := strconv.Atoi(f[1])
		worldgen.TreeDebug = func(c [2]int32, msg string) {
			if c == [2]int32{int32(x), int32(z)} {
				t.Log(msg)
			}
		}
		defer func() { worldgen.TreeDebug = nil }()
	}
	minY, height := g.RS.MinY(), g.RS.Height()
	maxBad := 5
	if v, err := strconv.Atoi(os.Getenv("MCGO_TREE_MAXBAD")); err == nil {
		maxBad = v
	}
	// Vanilla decorates chunks concurrently, so a chunk's trees can see
	// (or not) the trees its neighbours placed first: a canopy hanging
	// over the chunk raises the OCEAN_FLOOR origin onto leaves and the
	// sapling check drops the tree, shifting every later draw of the
	// chunk's random. The real order differs from run to run, so mc-go
	// decorates the area under several orders (row sweeps in every
	// direction, then seeded shuffles) and each chunk is judged by the
	// order that reproduces it best.
	orders := decorationOrders(areas)
	if v, err := strconv.Atoi(os.Getenv("MCGO_TREE_ORDERS")); err == nil && v < len(orders) {
		orders = orders[:v]
	}
	if orderLog != "" {
		inArea := map[[2]int32]bool{}
		for _, c := range orders[0] {
			inArea[c] = true
		}
		logged, overlaps := readDecorationOrder(t, orderLog)
		// The oracle also decorates a ring of chunks around each forced
		// area (lighting needs decorated neighbours), and their trees
		// reach into the area: replay every logged chunk within two of
		// an area.
		near := func(c [2]int32) bool {
			for _, a := range areas {
				if c[0] >= a.x0-2 && c[0] <= a.x1+2 && c[1] >= a.z0-2 && c[1] <= a.z1+2 {
					return true
				}
			}
			return false
		}
		var vo [][2]int32
		for _, c := range logged {
			if inArea[c] || near(c) {
				vo = append(vo, c)
				delete(inArea, c)
			}
		}
		for _, c := range orders[0] {
			if inArea[c] {
				vo = append(vo, c)
			}
		}
		t.Logf("oracle decoration order logged for %d chunks (%d overlapping neighbour decorations)", len(logged), overlaps)
		orders = [][][2]int32{vo}
	}
	type result struct {
		bad, orderDep, exempt int
		msgs                  []string
	}
	terrain := map[[2]int32]*worldgen.ProtoChunk{}
	for _, c := range orders[0] {
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				k := [2]int32{c[0] + dx, c[1] + dz}
				if terrain[k] == nil {
					terrain[k] = g.Terrain(k[0], k[1])
				}
			}
		}
	}
	saved := map[[2]int32]savedChunk{}
	for _, a := range areas {
		for cx := a.x0 + 1; cx < a.x1; cx++ {
			for cz := a.z0 + 1; cz < a.z1; cz++ {
				saved[[2]int32{cx, cz}] = readSavedChunk(t, region, cx, cz, minY, height)
			}
		}
	}
	var writes map[worldgen.BlockPos]map[[2]int32]world.Block
	g.WriteLog = func(p worldgen.BlockPos, c [2]int32, b world.Block) {
		m := writes[p]
		if m == nil {
			m = map[[2]int32]world.Block{}
			writes[p] = m
		}
		m[c] = b
	}
	decorate := func(order [][2]int32) map[[2]int32]*worldgen.ProtoChunk {
		writes = map[worldgen.BlockPos]map[[2]int32]world.Block{}
		chunks := make(map[[2]int32]*worldgen.ProtoChunk, len(terrain))
		for k, p := range terrain {
			c := *p
			c.Blocks = append([]world.Block(nil), p.Blocks...)
			c.PostProcess = append([][3]int32(nil), p.PostProcess...)
			c.FluidMarks = append([][3]int32(nil), p.FluidMarks...)
			chunks[k] = &c
		}
		for _, c := range order {
			g.Decorate(g.NewRegion(chunks[c], chunks))
		}
		return chunks
	}
	compare := func(ai int, key [2]int32, p *worldgen.ProtoChunk) *result {
		r := &result{}
		sv := saved[key]
		cx, cz := key[0], key[1]
		for i, w := range sv.blocks {
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
					r.orderDep++
					continue
				}
			}
			if gs == "air" && strings.Contains(w, "[level=") {
				r.exempt++
				continue
			}
			if len(r.msgs) < maxBad {
				r.msgs = append(r.msgs, fmt.Sprintf("area %d (%d,%d,%d): mc-go %s, vanilla %s", ai, pos[0], pos[1], pos[2], gs, w))
			}
			r.bad++
		}
		return r
	}
	type keyed struct {
		ai  int
		key [2]int32
	}
	var compared []keyed
	for ai, a := range areas {
		for cx := a.x0 + 1; cx < a.x1; cx++ {
			for cz := a.z0 + 1; cz < a.z1; cz++ {
				compared = append(compared, keyed{ai, [2]int32{cx, cz}})
			}
		}
	}
	eval := func(order [][2]int32) (map[[2]int32]*result, int) {
		chunks := decorate(order)
		res := map[[2]int32]*result{}
		sum := 0
		for _, k := range compared {
			r := compare(k.ai, k.key, chunks[k.key])
			res[k.key] = r
			sum += r.bad
		}
		return res, sum
	}
	logs := os.Getenv("MCGO_TREE_LOGS") != ""
	var best map[[2]int32]*result
	var bestSeq [][2]int32
	bestSum, bestIdx := -1, 0
	for oi, order := range orders {
		res, sum := eval(order)
		if oi == 0 {
			worldgen.TreeDebug = nil
			if logs {
				chunks := decorate(order)
				for _, k := range compared {
					sv, p := saved[k.key], chunks[k.key]
					// Lowest log per column on both sides (tree bases).
					for i := 0; i < 256; i++ {
						vy, gy := int32(1<<30), int32(1<<30)
						for y := int32(0); y < height; y++ {
							j := int(y)*256 + i
							if vy == 1<<30 && strings.HasSuffix(strings.SplitN(sv.blocks[j], "[", 2)[0], "_log") {
								vy = y + minY
							}
							if gy == 1<<30 && strings.HasSuffix(strings.SplitN(protoStateString(p.Blocks[j]), "[", 2)[0], "_log") {
								gy = y + minY
							}
						}
						if vy != gy {
							t.Logf("log column %d,%d: vanilla %d mc-go %d", k.key[0]*16+int32(i&15), k.key[1]*16+int32(i>>4), vy, gy)
						}
					}
				}
			}
		}
		if bestSum < 0 || sum < bestSum {
			best, bestSeq, bestSum, bestIdx = res, order, sum, oi
		}
		if sum == 0 {
			break
		}
	}
	// Hill-climb the order: flip the relative order of neighbouring
	// chunks while that lowers the total mismatch count.
	for round := 0; round < 4 && bestSum > 0 && orderLog == ""; round++ {
		improved := false
		for _, k := range compared {
			for dz := int32(-1); dz <= 1 && bestSum > 0; dz++ {
				for dx := int32(-1); dx <= 1 && bestSum > 0; dx++ {
					if dx == 0 && dz == 0 {
						continue
					}
					n := [2]int32{k.key[0] + dx, k.key[1] + dz}
					for _, after := range []bool{false, true} {
						order := moveRelative(bestSeq, k.key, n, after)
						if order == nil {
							continue
						}
						if res, sum := eval(order); sum < bestSum {
							best, bestSeq, bestSum, bestIdx = res, order, sum, len(orders)
							improved = true
						}
					}
				}
			}
		}
		if !improved {
			break
		}
	}
	t.Logf("best decoration order: candidate %d of %d (%d = hill-climbed)", bestIdx, len(orders), len(orders))
	if s := os.Getenv("MCGO_TREE_DUMP"); s != "" {
		// x,z,y0,y1: both sides of one column.
		var x, z, y0, y1 int32
		fmt.Sscanf(s, "%d,%d,%d,%d", &x, &z, &y0, &y1)
		chunks := decorate(bestSeq)
		key := [2]int32{x >> 4, z >> 4}
		if sv := readSavedChunk(t, region, key[0], key[1], minY, height); true {
			for y := y0; y <= y1; y++ {
				i := int((y-minY)*256 + (z&15)*16 + (x & 15))
				t.Logf("dump %d,%d,%d: mc-go %s vanilla %s", x, y, z, protoStateString(chunks[key].Blocks[i]), sv.blocks[i])
			}
		}
	}
	total, orderDep, exempt, badChunks := 0, 0, 0, 0
	for _, k := range compared {
		r := best[k.key]
		for _, m := range r.msgs {
			t.Error(m)
		}
		if r.bad > 0 {
			t.Errorf("chunk %d,%d: %d mismatched blocks", k.key[0], k.key[1], r.bad)
			badChunks++
		}
		orderDep += r.orderDep
		exempt += r.exempt
		total++
	}
	if len(worldgen.UnsupportedFeatures) > 0 {
		t.Errorf("unsupported feature types placed nothing: %v", worldgen.UnsupportedFeatures)
	}
	if n := worldgen.JSetTieBreaks.Load(); n > 0 {
		t.Logf("%d tree-bin tie breaks (identity-hash order not modeled)", n)
	}
	t.Logf("%d chunks compared (%d mismatched); %d order-dependent cells, %d fluid post-processing cells accepted", total, badChunks, orderDep, exempt)
}

// moveRelative returns order with c moved just before (or after) n,
// or nil when it already is on that side.
func moveRelative(order [][2]int32, c, n [2]int32, after bool) [][2]int32 {
	ic, in := -1, -1
	for i, k := range order {
		if k == c {
			ic = i
		}
		if k == n {
			in = i
		}
	}
	if ic < 0 || in < 0 || (ic > in) == after {
		return nil
	}
	var out [][2]int32
	for _, k := range order {
		if k == c {
			continue
		}
		if k == n && after {
			out = append(out, n, c)
			continue
		}
		if k == n {
			out = append(out, c, n)
			continue
		}
		out = append(out, k)
	}
	return out
}

// decorationOrders lists candidate chunk decoration orders: row-major
// first, then the eight axis sweeps, then seeded shuffles.
func decorationOrders(areas []terrainArea) [][][2]int32 {
	var out [][][2]int32
	for sweep := 0; sweep < 8; sweep++ {
		var order [][2]int32
		for _, a := range areas {
			n := (a.x1 - a.x0 + 1) * (a.z1 - a.z0 + 1)
			for i := int32(0); i < n; i++ {
				w := a.x1 - a.x0 + 1
				h := a.z1 - a.z0 + 1
				var u, v int32
				if sweep&4 == 0 {
					u, v = i%w, i/w
				} else {
					v, u = i%h, i/h
				}
				if sweep&1 != 0 {
					u = w - 1 - u
				}
				if sweep&2 != 0 {
					v = h - 1 - v
				}
				order = append(order, [2]int32{a.x0 + u, a.z0 + v})
			}
		}
		out = append(out, order)
	}
	rnd := rand.New(rand.NewSource(1))
	for k := 0; k < 40; k++ {
		order := append([][2]int32(nil), out[0]...)
		rnd.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		out = append(out, order)
	}
	return out
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
	{-41, 7, -37, 11},        // 0 forest
	{-33, -33, -29, -29},     // 1 birch forest
	{-41, -33, -37, -29},     // 2 jungle
	{39, 39, 43, 43},         // 3 snowy taiga
	{47, 23, 51, 27},         // 4 taiga
	{-97, -145, -93, -141},   // 5 dark forest
	{-161, 39, -157, 43},     // 6 mangrove swamp
	{-89, -1, -85, 3},        // 7 bamboo jungle
	{255, 279, 259, 283},     // 8 mushroom fields
	{-209, -593, -205, -589}, // 9 pale garden
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
