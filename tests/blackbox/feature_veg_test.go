//go:build blackbox

package blackbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	addVegProbe(ov)
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
	if only := os.Getenv("MCGO_VEG_ONLY"); only != "" {
		// Debugging aid: compare a subset of the areas (indices).
		var sub []terrainArea
		for _, f := range strings.Split(only, ",") {
			var i int
			fmt.Sscan(f, &i)
			sub = append(sub, areas[i])
		}
		areas = sub
	}
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	// A forced chunk is entity-ticking (ticket level 31): the two rings
	// around it load FULL as well, and those need the third ring lit,
	// hence decorated. mc-go decorates the same area+3 rings (terrain
	// for one more), learns the order from the probe stacks in the
	// FULL chunks (area+2) and compares the forced area itself: its
	// cells depend on decorations up to two rings out, whose order the
	// probe pins (the outer rings see ring-3 pairs it may leave open).
	total, orderDep, exempt, written, edgeTotal, skipTotal := 0, 0, 0, 0, 0, 0
	for _, a := range areas {
		grow := func(r int32) [][2]int32 {
			var out [][2]int32
			for cz := a.z0 - r; cz <= a.z1+r; cz++ {
				for cx := a.x0 - r; cx <= a.x1+r; cx++ {
					out = append(out, [2]int32{cx, cz})
				}
			}
			return out
		}
		order := grow(vegDecoratedRings)
		terrain := func() map[[2]int32]*worldgen.ProtoChunk {
			cs := map[[2]int32]*worldgen.ProtoChunk{}
			for _, c := range grow(vegDecoratedRings + 1) {
				cs[c] = g.Terrain(c[0], c[1])
			}
			return cs
		}
		minY, height := g.RS.MinY(), g.RS.Height()

		// Vanilla decorates chunks in a schedule mc-go cannot predict,
		// and features that read what a neighbour's features wrote
		// (heightmap origins, air checks, patch scans) see a
		// schedule-dependent world. The order probe recovers it: a first
		// pass learns where each chunk's probe markers land, vanilla's
		// marker stacks then say which chunk decorated first.
		var probes []vegProbeWrite
		g.WriteLog = func(p worldgen.BlockPos, c [2]int32, b world.Block) {
			if p[1] >= vegProbeY {
				probes = append(probes, vegProbeWrite{p, c, protoStateString(b)})
			}
		}
		cs := terrain()
		for _, c := range order {
			g.Decorate(g.NewRegion(cs[c], cs))
		}
		saved := map[[2]int32]savedChunk{}
		for _, c := range grow(vegDecoratedRings - 1) {
			saved[c] = readSavedChunk(t, region, c[0], c[1], minY, height)
		}
		derived, edges, skipped := vegProbeOrder(t, order, probes, saved, minY)
		edgeTotal += edges
		skipTotal += skipped

		writes := map[worldgen.BlockPos]map[[2]int32]world.Block{}
		g.WriteLog = func(p worldgen.BlockPos, c [2]int32, b world.Block) {
			m := writes[p]
			if m == nil {
				m = map[[2]int32]world.Block{}
				writes[p] = m
			}
			m[c] = b
		}
		cs = terrain()
		for _, c := range derived {
			g.Decorate(g.NewRegion(cs[c], cs))
		}
		for _, k := range grow(0) {
			cx, cz := k[0], k[1]
			sv := saved[k]
			p := cs[k]
			bad := 0
			for i, w := range sv.blocks {
				pos := worldgen.BlockPos{cx*16 + int32(i&15), int32(i>>8) + minY, cz*16 + int32((i>>4)&15)}
				if writes[pos] != nil {
					written++
				}
				gs := protoStateString(p.Blocks[i])
				if gs == w {
					continue
				}
				// Decorations the probe left unordered (no shared marker
				// column): where several chunks wrote a cell, any of
				// their values is a possible final state.
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
				// Post-processing ticks the aquifer's marked fluids once
				// (FULL status); the flow can wash out a plant a feature
				// put next to them.
				if (gs == "air" || writes[pos] != nil) && strings.Contains(w, "[level=") {
					exempt++
					continue
				}
				if bad < 8 || os.Getenv("MCGO_VEG_VERBOSE") != "" {
					var wr []string
					for c, b := range writes[pos] {
						wr = append(wr, fmt.Sprintf("%v=%s", c, protoStateString(b)))
					}
					var below string
					if i >= 256 {
						below = fmt.Sprintf("; below mc-go %s vanilla %s", protoStateString(p.Blocks[i-256]), sv.blocks[i-256])
					}
					t.Errorf("(%d,%d,%d): mc-go %s, vanilla %s (writes %v%s)", pos[0], pos[1], pos[2], gs, w, wr, below)
				}
				bad++
			}
			if bad > 0 {
				t.Errorf("chunk %d,%d: %d mismatched blocks", cx, cz, bad)
			}
			total++
		}
	}
	if len(worldgen.UnsupportedFeatures) > 0 {
		t.Errorf("unsupported feature types placed nothing: %v", worldgen.UnsupportedFeatures)
	}
	if len(worldgen.UnsupportedSurvive) > 0 {
		t.Errorf("canSurvive not modeled for: %v", worldgen.UnsupportedSurvive)
	}
	t.Logf("decoration order from %d probe precedences (%d unmatched columns)", edgeTotal, skipTotal)
	t.Logf("%d chunks compared, %d feature-written cells; %d order-dependent cells, %d fluid post-processing cells accepted",
		total, written, orderDep, exempt)
}

// vegDecoratedRings is how far around a forced area vanilla decorates.
const vegDecoratedRings = 3

// The order probe: a placed feature appended to every biome's last
// decoration step drops vegProbeCount wool markers per chunk on random
// columns of its 3x3 write zone, each on the lowest free cell at or
// above vegProbeY (high sky, untouched by real features). Where two
// chunks' markers share a column, the stack says which decorated first.
const (
	vegProbeY     = 300
	vegProbeCount = 400
)

var vegWools = []string{"white", "orange", "magenta", "light_blue", "yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue", "brown", "green", "red", "black"}

// addVegProbe adds the probe placed feature and appends it to every
// biome's last step.
func addVegProbe(ov map[string]json.RawMessage) {
	var entries []string
	for _, w := range vegWools {
		for _, kind := range []string{"wool", "concrete", "terracotta", "glazed_terracotta"} {
			entries = append(entries, fmt.Sprintf(`{"data":{"Name":"minecraft:%s_%s"},"weight":1}`, w, kind))
		}

	}
	ov["placed_feature/veg_order_probe"] = json.RawMessage(fmt.Sprintf(`{"feature":{"type":"minecraft:simple_block",`+
		`"config":{"to_place":{"type":"minecraft:weighted_state_provider","entries":[%s]}}},"placement":[`+
		`{"type":"minecraft:count","count":%d},`+
		`{"type":"minecraft:random_offset","xz_spread":{"type":"minecraft:uniform","min_inclusive":-16,"max_inclusive":16},"y_spread":0},`+
		`{"type":"minecraft:random_offset","xz_spread":{"type":"minecraft:uniform","min_inclusive":0,"max_inclusive":15},"y_spread":0},`+
		`{"type":"minecraft:height_range","height":{"absolute":%d}},`+
		`{"type":"minecraft:environment_scan","direction_of_search":"up","max_steps":19,`+
		`"target_condition":{"type":"minecraft:matching_block_tag","tag":"minecraft:air"}}]}`,
		strings.Join(entries, ","), vegProbeCount, vegProbeY))
	for k, raw := range ov {
		if !strings.HasPrefix(k, "biome/") {
			continue
		}
		var b map[string]json.RawMessage
		json.Unmarshal(raw, &b)
		var steps [][]string
		json.Unmarshal(b["features"], &steps)
		if len(steps) == 0 {
			continue
		}
		steps[len(steps)-1] = append(steps[len(steps)-1], "minecraft:veg_order_probe")
		b["features"], _ = json.Marshal(steps)
		ov[k], _ = json.Marshal(b)
	}
}

type vegProbeWrite struct {
	p     worldgen.BlockPos
	chunk [2]int32
	block string
}

// vegProbeOrder turns vanilla's probe stacks into a decoration order:
// each column's markers, matched to the chunks whose markers mc-go put
// there, give "decorated before" edges; a topological sort (row-major
// among unordered chunks) yields the order.
func vegProbeOrder(t *testing.T, order [][2]int32, probes []vegProbeWrite, saved map[[2]int32]savedChunk, minY int32) ([][2]int32, int, int) {
	t.Helper()
	type col struct{ x, z int32 }
	// Per column, each chunk's markers in its own write order.
	byCol := map[col]map[[2]int32][]string{}
	for _, w := range probes {
		k := col{w.p[0], w.p[2]}
		if byCol[k] == nil {
			byCol[k] = map[[2]int32][]string{}
		}
		byCol[k][w.chunk] = append(byCol[k][w.chunk], w.block)
	}
	after := map[[2]int32]map[[2]int32]bool{}
	edges, skipped := 0, 0
	for k, writers := range byCol {
		sv, ok := saved[[2]int32{k.x >> 4, k.z >> 4}]
		if !ok || len(writers) < 2 {
			continue
		}
		var stack []string
		for y := int32(vegProbeY); y < minY+int32(len(sv.blocks)/256); y++ {
			b := sv.blocks[(y-minY)*256+(k.z&15)*16+(k.x&15)]
			if b == "air" {
				break
			}
			stack = append(stack, b)
		}
		// A chunk's markers go down in one decoration, so the stack is
		// the writers' sequences concatenated in decoration order: find
		// every writer permutation that spells it and keep the pairwise
		// orders all of them agree on.
		var ws [][2]int32
		for c := range writers {
			ws = append(ws, c)
		}
		var sols [][][2]int32
		used := make([]bool, len(ws))
		var perm [][2]int32
		var dfs func(at int)
		dfs = func(at int) {
			if len(perm) == len(ws) {
				if at == len(stack) {
					sols = append(sols, slices.Clone(perm))
				}
				return
			}
			for i, c := range ws {
				l := writers[c]
				if used[i] || at+len(l) > len(stack) || !slices.Equal(stack[at:at+len(l)], l) {
					continue
				}
				used[i] = true
				perm = append(perm, c)
				dfs(at + len(l))
				perm = perm[:len(perm)-1]
				used[i] = false
			}
		}
		dfs(0)
		if len(sols) == 0 {
			skipped++
			continue
		}
		rank := func(sol [][2]int32, c [2]int32) int { return slices.Index(sol, c) }
		for _, a := range ws {
			for _, b := range ws {
				if a == b {
					continue
				}
				all := true
				for _, sol := range sols {
					all = all && rank(sol, a) < rank(sol, b)
				}
				if !all {
					continue
				}
				if after[a] == nil {
					after[a] = map[[2]int32]bool{}
				}
				if !after[a][b] {
					after[a][b] = true
					edges++
				}
			}
		}
	}
	indeg := map[[2]int32]int{}
	for _, m := range after {
		for b := range m {
			indeg[b]++
		}
	}
	done := map[[2]int32]bool{}
	var out [][2]int32
	for len(out) < len(order) {
		pick := -1
		for i, c := range order {
			if !done[c] && indeg[c] == 0 {
				pick = i
				break
			}
		}
		if pick < 0 {
			t.Errorf("order probe: precedence cycle among %d chunks", len(order)-len(out))
			for _, c := range order {
				if !done[c] {
					out = append(out, c)
				}
			}
			break
		}
		c := order[pick]
		done[c] = true
		out = append(out, c)
		for b := range after[c] {
			indeg[b]--
		}
	}
	return out, edges, skipped
}

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
// jungle, bamboo jungle, cherry grove, pale garden, dark forest and
// sparse jungle. Swamp is left out: its mushrooms evidently get placed
// while a neighbour's light is initialized but not yet propagated and
// are popped by post-processing, a light-schedule effect not modeled.
func TestFeatureVegSurfaceParity(t *testing.T) {
	var areas []terrainArea
	for _, c := range [][2]int32{{-32, 40}, {-176, -136}, {-64, -240}, {312, 96}, {-144, 32}, {-328, -280},
		{-64, 24}, {-104, -88}, {-368, -8}, {-216, -600}, {-136, -152}, {-80, -24}} {
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
	run := dir + ".run"
	os.RemoveAll(run)
	region := vegVanillaRun(t, seed, files, areas, run)
	if err := os.CopyFS(dir, os.DirFS(region)); err != nil {
		t.Fatal(err)
	}
	return region
}

// vegVanillaRun is vanillaTerrainFiles in a caller-chosen directory, so
// the server log survives a failed run.
func vegVanillaRun(t *testing.T, seed int64, files map[string]string, areas []terrainArea, dir string) string {
	t.Helper()
	jar := blackboxJar(t)
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	props := map[string]string{
		"level-name":          "world",
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          fmt.Sprint(seed),
		"generate-structures": "false",
		"view-distance":       "3",
		"simulation-distance": "3",
		// One worldgen thread (-Dmax.bg.threads=1) can hold the server
		// thread past the watchdog's limit while chunks generate.
		"max-tick-time": "-1",
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// One worldgen worker: decorations then run one at a time, in an
	// order the probe can recover. With several workers, chunks whose
	// write zones overlap can decorate concurrently and a section write
	// can be lost to a palette resize race.
	t.Setenv("JAVA_TOOL_OPTIONS", strings.TrimSpace(os.Getenv("JAVA_TOOL_OPTIONS")+" -Dmax.bg.threads=1"))
	_, say, wait := vanillaConsole(t, jar, port, props, files, dir)
	logPath := filepath.Join(dir, "vanilla.log")
	logHas := func(s string) bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), s)
	}
	say("tick freeze")
	for _, a := range areas {
		say(fmt.Sprintf("forceload add %d %d %d %d", a.x0*16, a.z0*16, a.x1*16+15, a.z1*16+15))
	}
	for i, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				tag := fmt.Sprintf("loaded-%d-%d-%d", i, cx, cz)
				deadline := time.Now().Add(300 * time.Second)
				for !logHas(tag) {
					if time.Now().After(deadline) {
						t.Fatalf("chunk %d,%d never loaded (log %s)", cx, cz, logPath)
					}
					say(fmt.Sprintf("execute if loaded %d 0 %d run say %s", cx*16, cz*16, tag))
					time.Sleep(200 * time.Millisecond)
				}
			}
		}
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d (log %s)", code, logPath)
	}
	return filepath.Join(dir, "world", "dimensions", "minecraft", "overworld", "region")
}

// TestFeatureVegOceanParity pins ocean floor vegetation — kelp columns
// capped with an aged head, seagrass and tall seagrass, sea pickles and
// the three warm-ocean coral shapes (tree, claw, mushroom) with their
// coral, fan and pickle toppings — in warm, lukewarm, normal and cold
// oceans.
func TestFeatureVegOceanParity(t *testing.T) {
	var areas []terrainArea
	for _, c := range [][2]int32{{-272, -240}, {-312, -320}, {-112, 88}, {-136, 152}, {0, 0}, {0, 40}, {16, -128}} {
		areas = append(areas, terrainArea{c[0], c[1], c[0] + 3, c[1] + 3})
	}
	vegParity(t, 12345, areas, nil, append([]string{"kelp", "seagrass", "sea_pickle", "coral_tree", "coral_claw",
		"coral_mushroom"}, vegSurface...)...)
}

// TestFeatureVegCaveParity pins cave vegetation — glow lichen and sculk
// vein multiface growth with spreading, lush-cave moss patches (floor
// and ceiling), clay pools with dripleaves, cave vines, spore blossoms,
// the rooted-azalea root system (rooted dirt, hanging roots), pointed
// dripstone and sulfur spikes (speleothem), their clusters with pools,
// large dripstone and deep-dark sculk patches with catalysts, sensors
// and shriekers — in lush caves, dripstone caves, the deep dark and
// sulfur caves. The azalea tree the root system grows is stood in for
// by a log (simple_block) so the pin covers the root system alone.
func TestFeatureVegCaveParity(t *testing.T) {
	var areas []terrainArea
	for _, c := range [][2]int32{{-8, -8}, {-8, 8}, {8, 8}, {-64, -72}, {24, -72}, {48, -40},
		{-40, -24}, {16, -24}, {32, 120}, {-88, 136}} {
		areas = append(areas, terrainArea{c[0], c[1], c[0] + 3, c[1] + 3})
	}
	extra := map[string]string{
		"configured_feature/azalea_tree": `{"type":"minecraft:simple_block","config":{"to_place":` +
			`{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:oak_log","Properties":{"axis":"y"}}}}}`,
	}
	vegParity(t, 12345, areas, extra, append([]string{"multiface_growth", "waterlogged_vegetation_patch",
		"root_system", "speleothem", "speleothem_cluster", "large_dripstone", "sculk_patch"}, vegSurface...)...)
}
