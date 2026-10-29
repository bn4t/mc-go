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
	_, say, wait := vanillaConsole(t, jar, port, props, terrainDatapack(t, strip), dir)
	logPath := filepath.Join(dir, "vanilla.log")
	logHas := func(s string) bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), s)
	}
	say("tick freeze")
	for _, a := range areas {
		say(fmt.Sprintf("forceload add %d %d %d %d", a.x0*16, a.z0*16, a.x1*16+15, a.z1*16+15))
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
					say(fmt.Sprintf("execute if loaded %d 0 %d run say %s", cx*16, cz*16, tag))
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
	return filepath.Join(dir, "world", "dimensions", "minecraft", "overworld", "region")
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
