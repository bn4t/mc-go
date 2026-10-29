package blackbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// terrainStrip selects which generation stages the oracle world keeps.
// The real server always runs every stage, so a stage is isolated by a
// data pack that empties the later ones: biome features and carvers
// cleared, and optionally the overworld surface rule replaced by an
// empty sequence.
type terrainStrip struct {
	surface  bool // keep the surface rule
	carvers  bool // keep biome carvers
	features bool // keep biome features
}

// terrainDatapack builds the data pack files (keyed by path under the
// server directory) from mc-go's bundled copy of the same worldgen data.
func terrainDatapack(t *testing.T, s terrainStrip) map[string]string {
	t.Helper()
	files := map[string]string{
		"world/datapacks/strip/pack.mcmeta": `{"pack":{"description":"terrain stages","min_format":[107,1],"max_format":[107,1]}}`,
	}
	data := worldgen.Vanilla()
	for _, k := range worldgen.VanillaKeys("biome/") {
		raw, _ := data.Get(k)
		var b map[string]json.RawMessage
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatal(err)
		}
		if !s.features {
			var steps []json.RawMessage
			json.Unmarshal(b["features"], &steps)
			empty := make([][]string, len(steps))
			for i := range empty {
				empty[i] = []string{}
			}
			b["features"], _ = json.Marshal(empty)
		}
		if !s.carvers {
			b["carvers"] = json.RawMessage(`[]`)
		}
		out, _ := json.Marshal(b)
		files["world/datapacks/strip/data/minecraft/worldgen/"+k+".json"] = string(out)
	}
	if !s.surface {
		raw, _ := data.Get("noise_settings/overworld")
		var ns map[string]json.RawMessage
		json.Unmarshal(raw, &ns)
		ns["surface_rule"] = json.RawMessage(`{"type":"minecraft:sequence","sequence":[]}`)
		out, _ := json.Marshal(ns)
		files["world/datapacks/strip/data/minecraft/worldgen/noise_settings/overworld.json"] = string(out)
	}
	return files
}

// terrainArea is an inclusive chunk rectangle.
type terrainArea struct{ x0, z0, x1, z1 int32 }

// vanillaTerrain has the real server generate a normal world for seed,
// with ticking frozen so no fluid or block tick runs after generation,
// force-loads the areas, waits until every chunk is loaded, saves and
// stops. It returns the overworld region directory.
func vanillaTerrain(t *testing.T, seed int64, strip terrainStrip, areas []terrainArea) string {
	t.Helper()
	return vanillaTerrainFiles(t, seed, terrainDatapack(t, strip), areas)
}

// vanillaTerrainFiles is vanillaTerrain with caller-built data pack
// files.
func vanillaTerrainFiles(t *testing.T, seed int64, files map[string]string, areas []terrainArea) string {
	t.Helper()
	return vanillaDimTerrain(t, seed, files, "overworld", areas)
}

// vanillaDimTerrain is vanillaTerrainFiles for any dimension
// ("overworld", "the_nether", "the_end"): the areas are force-loaded in
// that dimension and its region directory is returned.
func vanillaDimTerrain(t *testing.T, seed int64, files map[string]string, dim string, areas []terrainArea) string {
	t.Helper()
	jar := blackboxJar(t)
	dir := t.TempDir()
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
	}
	_, say, wait := vanillaConsole(t, jar, port, props, files, dir)
	logPath := filepath.Join(dir, "vanilla.log")
	logHas := func(s string) bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), s)
	}
	say("tick freeze")
	in := "execute in minecraft:" + dim + " run "
	for _, a := range areas {
		say(fmt.Sprintf(in+"forceload add %d %d %d %d", a.x0*16, a.z0*16, a.x1*16+15, a.z1*16+15))
	}
	// Every chunk must be loaded before the save: probe each one until
	// the console confirms it.
	for i, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				tag := fmt.Sprintf("loaded-%d-%d-%d", i, cx, cz)
				deadline := time.Now().Add(120 * time.Second)
				for !logHas(tag) {
					if time.Now().After(deadline) {
						vanillaLogTail(t)
						t.Fatalf("chunk %d,%d never loaded", cx, cz)
					}
					say(fmt.Sprintf("execute in minecraft:%s if loaded %d 0 %d run say %s", dim, cx*16, cz*16, tag))
					time.Sleep(200 * time.Millisecond)
				}
			}
		}
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d", code)
	}
	return filepath.Join(dir, "world", "dimensions", "minecraft", dim, "region")
}

// savedChunk is a decoded region chunk: block state strings
// ("stone", "water[level=0]") indexed ((y-minY)*16+z)*16+x, plus the
// raw compound.
type savedChunk struct {
	blocks []string
	raw    map[string]any
}

func readSavedChunk(t *testing.T, regionDir string, cx, cz, minY, height int32) savedChunk {
	t.Helper()
	chunks, err := world.ReadRegionFile(filepath.Join(regionDir, fmt.Sprintf("r.%d.%d.mca", cx>>5, cz>>5)))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := chunks[[2]int32{cx & 31, cz & 31}]
	if !ok {
		t.Fatalf("chunk %d,%d missing from region", cx, cz)
	}
	if st, _ := c["Status"].(string); st != "minecraft:full" {
		t.Fatalf("chunk %d,%d status %q", cx, cz, st)
	}
	out := make([]string, 16*16*height)
	sections, _ := c["sections"].(nbt.List)
	for _, sAny := range sections.Items {
		sec := sAny.(map[string]any)
		sy := int32(sec["Y"].(int8))
		states, ok := sec["block_states"].(map[string]any)
		if !ok {
			continue
		}
		pal := states["palette"].(nbt.List)
		names := make([]string, len(pal.Items))
		for i, pAny := range pal.Items {
			pc := pAny.(map[string]any)
			names[i] = stateString(pc)
		}
		idx := make([]int32, 4096)
		if data, ok := states["data"].([]int64); ok && len(names) > 1 {
			bpe := 4
			for 1<<uint(bpe) < len(names) {
				bpe++
			}
			per := 64 / bpe
			mask := int64(1<<uint(bpe)) - 1
			for i := 0; i < 4096; i++ {
				idx[i] = int32((data[i/per] >> (uint(i%per) * uint(bpe))) & mask)
			}
		}
		for i := 0; i < 4096; i++ {
			y := sy*16 + int32(i>>8)
			if y < minY || y >= minY+height {
				continue
			}
			out[((y-minY)*16+(int32(i>>4)&15))*16+int32(i&15)] = names[idx[i]]
		}
	}
	return savedChunk{blocks: out, raw: c}
}

func stateString(pc map[string]any) string {
	name := strings.TrimPrefix(pc["Name"].(string), "minecraft:")
	pm, ok := pc["Properties"].(map[string]any)
	if !ok || len(pm) == 0 {
		return name
	}
	kv := make([]string, 0, len(pm))
	for k, v := range pm {
		kv = append(kv, k+"="+v.(string))
	}
	sort.Strings(kv)
	return name + "[" + strings.Join(kv, ",") + "]"
}

var noiseSubstanceStates = map[worldgen.Substance]string{
	worldgen.SubDefault: "stone", worldgen.SubAir: "air", worldgen.SubWater: "water[level=0]",
	worldgen.SubLava: "lava[level=0]", worldgen.SubCopperOre: "copper_ore",
	worldgen.SubRawCopperBlock: "raw_copper_block", worldgen.SubGranite: "granite",
	worldgen.SubDeepslateIronOre: "deepslate_iron_ore", worldgen.SubRawIronBlock: "raw_iron_block",
	worldgen.SubTuff: "tuff",
}

// TestTerrainNoiseParity pins the noise stage — density functions, cell
// interpolation, aquifers and ore veins — block for block against chunks
// the real server generated from the same seed with the surface rule,
// carvers and features stripped. Areas span ocean floor, mountains,
// cave networks, flooded and lava aquifers and copper/iron veins on
// both sides of the origin.
//
// One documented exemption: a full chunk has been through post-
// processing, where the real server ticks the fluid at every position
// the aquifer marked, so flowing fluid (level > 0) where the noise stage
// leaves air beside a marked source is post-processing, not noise.
func TestTerrainNoiseParity(t *testing.T) {
	const seed = 12345
	areas := []terrainArea{{0, 0, 3, 3}, {-125, 187, -121, 191}, {312, -438, 316, -434}, {-19, -19, -15, -15}}
	region := vanillaTerrain(t, seed, terrainStrip{}, areas)
	rs, err := worldgen.NewRandomState(worldgen.Vanilla(), "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	minY, height := rs.MinY(), rs.Height()
	total, exempt := 0, 0
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				want := readSavedChunk(t, region, cx, cz, minY, height)
				got := rs.FillNoise(cx, cz)
				marked := map[[3]int32]bool{}
				for _, m := range got.FluidMarks {
					marked[m] = true
				}
				nearMark := func(x, y, z int32) bool {
					for _, d := range [][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}} {
						if marked[[3]int32{x + d[0], y + d[1], z + d[2]}] {
							return true
						}
					}
					return false
				}
				bad := 0
				for i, w := range want.blocks {
					g := noiseSubstanceStates[got.Cells[i]]
					if g == w {
						continue
					}
					x := cx*16 + int32(i&15)
					z := cz*16 + int32((i>>4)&15)
					y := int32(i>>8) + minY
					if g == "air" && (strings.HasPrefix(w, "water[level=") || strings.HasPrefix(w, "lava[level=")) &&
						!strings.HasSuffix(w, "level=0]") && nearMark(x, y, z) {
						exempt++
						continue
					}
					if bad < 5 {
						t.Errorf("(%d,%d,%d): mc-go %s, vanilla %s", x, y, z, g, w)
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
	t.Logf("%d chunks compared, %d post-processing fluid cells exempted", total, exempt)
}

// savedBiomes decodes a saved chunk's per-section 4x4x4 biome palettes,
// indexed ((qy-minQY)*4+qz)*4+qx.
func savedBiomes(c map[string]any, minY, height int32) []string {
	out := make([]string, 16*height/4)
	sections, _ := c["sections"].(nbt.List)
	for _, sAny := range sections.Items {
		sec := sAny.(map[string]any)
		sy := int32(sec["Y"].(int8))
		b, ok := sec["biomes"].(map[string]any)
		if !ok {
			continue
		}
		pal := b["palette"].(nbt.List)
		names := make([]string, len(pal.Items))
		for i, p := range pal.Items {
			names[i] = strings.TrimPrefix(p.(string), "minecraft:")
		}
		idx := make([]int, 64)
		if data, ok := b["data"].([]int64); ok && len(names) > 1 {
			bpe := 0
			for 1<<uint(bpe) < len(names) {
				bpe++
			}
			per := 64 / bpe
			for i := 0; i < 64; i++ {
				idx[i] = int((data[i/per] >> (uint(i%per) * uint(bpe))) & (1<<uint(bpe) - 1))
			}
		}
		for i := 0; i < 64; i++ {
			qy := sy*4 + int32(i>>4) - minY/4
			if qy >= 0 && qy < height/4 {
				out[qy*16+int32(i&15)] = names[idx[i]]
			}
		}
	}
	return out
}

// TestTerrainBiomeParity pins the multi-noise biome source: climate
// sampling through the chunk's cached router, float quantization, the
// rebuilt parameter RTree and the previous-result tie break, cell for
// cell against the real server's saved biome palettes, on a second seed
// whose areas spread over many climates (and so re-check the noise
// stage there too).
func TestTerrainBiomeParity(t *testing.T) {
	const seed = -4172144997902289642
	areas := []terrainArea{{-2, -2, 1, 1}, {250, 40, 252, 42}, {-400, 900, -398, 902}, {1500, -1500, 1502, -1498},
		{-3000, -3000, -2998, -2998}, {70, -620, 72, -618}, {-900, 100, -898, 102}}
	region := vanillaTerrain(t, seed, terrainStrip{}, areas)
	rs, err := worldgen.NewRandomState(worldgen.Vanilla(), "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := worldgen.NewMultiNoiseBiomeSource(worldgen.Vanilla(), "overworld")
	if err != nil {
		t.Fatal(err)
	}
	minY, height := rs.MinY(), rs.Height()
	seen := map[string]bool{}
	cells, badBlocks := 0, 0
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				saved := readSavedChunk(t, region, cx, cz, minY, height)
				want := savedBiomes(saved.raw, minY, height)
				got := rs.ChunkBiomes(bs, rs.NewChunk(cx, cz), cx, cz)
				bad := 0
				for i := range want {
					seen[want[i]] = true
					cells++
					if got[i] != want[i] {
						if bad < 5 {
							t.Errorf("chunk %d,%d biome cell %d: mc-go %s, vanilla %s", cx, cz, i, got[i], want[i])
						}
						bad++
					}
				}
				noise := rs.FillNoise(cx, cz)
				for i, w := range saved.blocks {
					g := noiseSubstanceStates[noise.Cells[i]]
					// Same post-processing and surface-extension caveats as
					// the noise pin: only exact non-fluid disagreements count.
					if g != w && !strings.Contains(w, "level=") && w != "packed_ice" && w != "snow_block" &&
						!strings.HasSuffix(w, "terracotta") {
						badBlocks++
					}
				}
			}
		}
	}
	if badBlocks > 0 {
		t.Errorf("%d noise-stage block mismatches", badBlocks)
	}
	names := make([]string, 0, len(seen))
	for b := range seen {
		names = append(names, b)
	}
	sort.Strings(names)
	t.Logf("%d biome cells, %d biomes: %v", cells, len(names), names)
}

func protoStateString(b world.Block) string {
	n, props, _ := world.DescribeState(b)
	if len(props) == 0 {
		return n
	}
	kv := make([]string, 0, len(props))
	for k, v := range props {
		kv = append(kv, k+"="+v)
	}
	sort.Strings(kv)
	return n + "[" + strings.Join(kv, ",") + "]"
}

// TestTerrainSurfaceParity pins the surface stage — SurfaceSystem's
// column walk (stone depth above/below, water height), every surface
// rule and condition in the overworld rule tree (biome zoom, noise
// thresholds, vertical gradients, steep, hole, temperature with the
// frozen modifier, above preliminary surface), the terracotta bands and
// the eroded-badlands pillar and frozen-ocean iceberg extensions — block
// for block against the real server with carvers and features stripped.
// Areas were chosen to hit each biome family: badlands (all three),
// peaks and slopes, ice spikes, mushroom fields, deep frozen and warm
// oceans, cherry grove, pale garden, desert, swamps.
func TestTerrainSurfaceParity(t *testing.T) {
	const seed = 12345
	var areas []terrainArea
	for _, c := range [][2]int32{{-320, -288}, {-176, 112}, {-272, -272}, {368, 368}, {48, -48}, {272, 272},
		{224, 64}, {-32, 64}, {112, -96}, {-272, -240}, {32, -64}, {-208, -592}, {-144, 32}, {-112, -112}, {-160, 48}} {
		areas = append(areas, terrainArea{c[0], c[1], c[0] + 1, c[1] + 1})
	}
	region := vanillaTerrain(t, seed, terrainStrip{surface: true}, areas)
	g, err := worldgen.NewGenerator(worldgen.Vanilla(), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	g.Carve = false
	compareTerrain(t, region, g, areas)
}

// compareTerrain diffs the generator's proto chunks against the saved
// chunks block for block, exempting only flowing fluid that post-
// processing ticked out of a source the generator marked.
func compareTerrain(t *testing.T, region string, g *worldgen.Generator, areas []terrainArea) {
	t.Helper()
	minY, height := g.RS.LevelMinY(), g.RS.LevelHeight()
	total, exempt := 0, 0
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				want := readSavedChunk(t, region, cx, cz, minY, height)
				p := g.Terrain(cx, cz)
				marked := map[[3]int32]bool{}
				for _, m := range p.FluidMarks {
					marked[m] = true
				}
				bad := 0
				for i, w := range want.blocks {
					gs := protoStateString(p.Blocks[i])
					if gs == w {
						continue
					}
					x := cx*16 + int32(i&15)
					z := cz*16 + int32((i>>4)&15)
					y := int32(i>>8) + minY
					// Post-processing ticks each marked fluid once: it flows
					// into neighbouring air and two adjacent sources can
					// form a new source.
					if gs == "air" && strings.Contains(w, "[level=") {
						near := false
						for _, d := range [][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}} {
							near = near || marked[[3]int32{x + d[0], y + d[1], z + d[2]}]
						}
						if near {
							exempt++
							continue
						}
					}
					if bad < 5 {
						t.Errorf("(%d,%d,%d): mc-go %s, vanilla %s", x, y, z, gs, w)
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
	t.Logf("%d chunks compared, %d post-processing fluid cells exempted", total, exempt)
}

// TestTerrainCarverParity pins the carver stage — the 17x17 start-chunk
// scan with per-carver large-feature seeds, the carver biome at each
// start chunk's corner, cave rooms and branching tunnels, canyons with
// their width factors and vertical radius profile, the carving mask,
// aquifer-decided carve states (including barrier cells left solid and
// lava below the lava level), the fluid marks they leave and the
// surface rule's top material on exposed dirt — block for block against
// the real server with only features stripped, on two seeds.
func TestTerrainCarverParity(t *testing.T) {
	for _, tc := range []struct {
		seed  int64
		areas []terrainArea
	}{
		{12345, []terrainArea{{62, 62, 66, 66}, {-125, 187, -121, 191}, {-19, -19, -15, -15}, {-320, -288, -319, -287}, {368, 368, 369, 369}}},
		{-4172144997902289642, []terrainArea{{-2, -2, 1, 1}, {1500, -1500, 1502, -1498}, {-900, 100, -898, 102}}},
	} {
		region := vanillaTerrain(t, tc.seed, terrainStrip{surface: true, carvers: true}, tc.areas)
		g, err := worldgen.NewGenerator(worldgen.Vanilla(), "overworld", "overworld", tc.seed)
		if err != nil {
			t.Fatal(err)
		}
		compareTerrain(t, region, g, tc.areas)
	}
}

// stageDatapack turns worldgen stage overrides into data pack files.
func stageDatapack(overrides map[string]json.RawMessage) map[string]string {
	files := map[string]string{
		"world/datapacks/stage/pack.mcmeta": `{"pack":{"description":"worldgen stage","min_format":[107,1],"max_format":[107,1]}}`,
	}
	for k, v := range overrides {
		files["world/datapacks/stage/data/minecraft/worldgen/"+k+".json"] = string(v)
	}
	return files
}

// featureParity has the real server decorate the forced areas with only
// the given feature types enabled and compares every chunk whose
// neighbours were all decorated (the area shrunk by one) block for
// block. mc-go decorates the whole forced area. Vanilla schedules chunk
// decoration concurrently, so where features from several chunks wrote
// the same cell the outcome depends on which ran last: such a cell
// passes when vanilla's block is one of the values those chunks wrote.
func featureParity(t *testing.T, seed int64, areas []terrainArea, types ...string) {
	t.Helper()
	want := map[string]bool{}
	for _, ty := range types {
		want[ty] = true
	}
	keep := func(p string) bool { return want[worldgen.PlacedFeatureType(worldgen.Vanilla(), p)] }
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
	total, orderDep, exempt := 0, 0, 0
	for _, a := range areas {
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
					if bad < 5 {
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
	t.Logf("%d chunks compared; %d order-dependent cells, %d fluid post-processing cells accepted", total, orderDep, exempt)
}

// TestFeatureOreParity pins ore and scattered_ore placement — the
// feature sorter's global per-step indices, decoration and feature
// seeds, count/in_square/height_range/biome placement, ore blob shapes,
// air-exposure discards and the raw section writes — against the real
// server with only those features enabled.
func TestFeatureOreParity(t *testing.T) {
	featureParity(t, 12345, []terrainArea{{0, 0, 3, 3}, {62, 62, 66, 66}, {-125, 187, -121, 191}, {-321, -289, -318, -286}}, "ore", "scattered_ore")
}
