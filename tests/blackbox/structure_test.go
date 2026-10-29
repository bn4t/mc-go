//go:build blackbox

package blackbox

import (
	"crypto/sha256"
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

// Structure parity: the real server generates the area with
// generate-structures=true and a stage data pack that keeps only the
// structure sets under test (every other set's placement replaced by a
// frequency-0 spread) and strips biome features; mc-go generates the
// same area from the same data. Two things are compared:
//
//   - the structure starts saved in every start chunk of the area (any
//     chunk status): structure id, pieces in order with their type,
//     bounding box, position, rotation, pool element and junctions;
//   - every interior chunk block for block, plus the positions of its
//     block entities.
//
// Block entity contents (loot seeds, spawner data) and template
// entities (villagers, iron golems, cats) are not compared.

// structureOverrides is the stage data: biome features stripped (or
// kept by keepFeature), carvers and surface on, and every structure set
// not in keep disabled.
func structureOverrides(keepFeature func(string) bool, keep ...string) map[string]json.RawMessage {
	if keepFeature == nil {
		keepFeature = func(string) bool { return false }
	}
	ov := worldgen.StageOverrides(worldgen.Vanilla(), keepFeature, true, true)
	want := map[string]bool{}
	for _, k := range keep {
		want[k] = true
	}
	data := worldgen.Vanilla()
	for _, k := range worldgen.VanillaKeys("structure_set/") {
		if want[strings.TrimPrefix(k, "structure_set/")] {
			continue
		}
		raw, _ := data.Get(k)
		var s map[string]json.RawMessage
		json.Unmarshal(raw, &s)
		s["placement"] = json.RawMessage(`{"type":"minecraft:random_spread","salt":0,"spacing":2,"separation":1,"frequency":0.0}`)
		enc, _ := json.Marshal(s)
		ov[k] = enc
	}
	return ov
}

// vanillaStructureTerrain is vanillaDimTerrain with structures on.
// With MCGO_STRUCT_CACHE set to a directory, the saved region files are
// kept there per test, seed, data pack and area and reused (iterating
// on mc-go without rebooting the real server).
func vanillaStructureTerrain(t *testing.T, seed int64, files map[string]string, dim string, areas []terrainArea) string {
	t.Helper()
	jar := blackboxJar(t)
	var cacheDir string
	if c := os.Getenv("MCGO_STRUCT_CACHE"); c != "" {
		h := sha256.New()
		fmt.Fprint(h, seed, dim, areas)
		keys := make([]string, 0, len(files))
		for k := range files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprint(h, k, files[k])
		}
		cacheDir = filepath.Join(c, fmt.Sprintf("%s-%x", t.Name(), h.Sum(nil)[:8]))
		if _, err := os.Stat(filepath.Join(cacheDir, "done")); err == nil {
			return cacheDir
		}
	}
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
		"generate-structures": "true",
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
	for _, a := range areas {
		say(fmt.Sprintf("execute in minecraft:%s run forceload add %d %d %d %d", dim, a.x0*16, a.z0*16, a.x1*16+15, a.z1*16+15))
	}
	for i, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				tag := fmt.Sprintf("loaded-%d-%d-%d", i, cx, cz)
				deadline := time.Now().Add(240 * time.Second)
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
	region := filepath.Join(dir, "world", "dimensions", "minecraft", dim, "region")
	if cacheDir != "" {
		os.MkdirAll(cacheDir, 0o755)
		ents, _ := os.ReadDir(region)
		for _, e := range ents {
			b, err := os.ReadFile(filepath.Join(region, e.Name()))
			if err == nil {
				os.WriteFile(filepath.Join(cacheDir, e.Name()), b, 0o644)
			}
		}
		os.WriteFile(filepath.Join(cacheDir, "done"), nil, 0o644)
	}
	return region
}

// savedRegionChunk returns a region chunk compound at any status.
func savedRegionChunk(t *testing.T, regionDir string, cx, cz int32) (map[string]any, bool) {
	t.Helper()
	chunks, err := world.ReadRegionFile(filepath.Join(regionDir, fmt.Sprintf("r.%d.%d.mca", cx>>5, cz>>5)))
	if err != nil {
		return nil, false
	}
	c, ok := chunks[[2]int32{cx & 31, cz & 31}]
	return c, ok
}

func nbtI(v any) int32 {
	switch x := v.(type) {
	case int32:
		return x
	case int8:
		return int32(x)
	case int16:
		return int32(x)
	case int64:
		return int32(x)
	}
	return 0
}

var rotationSaved = []string{"none", "clockwise_90", "clockwise_180", "counterclockwise_90"}

// elementSummary renders a pool element (saved NBT or datapack JSON
// decoded into maps) as type:location.
func elementSummary(e map[string]any) string {
	ty := strings.TrimPrefix(fmt.Sprint(e["element_type"]), "minecraft:")
	switch ty {
	case "feature_pool_element":
		return ty + ":" + strings.TrimPrefix(fmt.Sprint(e["feature"]), "minecraft:")
	case "list_pool_element":
		var parts []string
		var items []any
		switch l := e["elements"].(type) {
		case nbt.List:
			items = l.Items
		case []any:
			items = l
		}
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				parts = append(parts, elementSummary(m))
			}
		}
		return ty + ":[" + strings.Join(parts, ",") + "]"
	case "empty_pool_element":
		return ty
	}
	return ty + ":" + strings.TrimPrefix(fmt.Sprint(e["location"]), "minecraft:")
}

// pieceSummary renders a saved jigsaw piece compound.
func savedPieceSummary(c map[string]any) string {
	id := strings.TrimPrefix(fmt.Sprint(c["id"]), "minecraft:")
	bb, _ := c["BB"].([]int32)
	s := fmt.Sprintf("%s bb=%v", id, bb)
	if id != "jigsaw" {
		return s
	}
	el, _ := c["pool_element"].(map[string]any)
	s += fmt.Sprintf(" pos=%d,%d,%d rot=%v gld=%d el=%s", nbtI(c["PosX"]), nbtI(c["PosY"]), nbtI(c["PosZ"]),
		strings.ToLower(fmt.Sprint(c["rotation"])), nbtI(c["ground_level_delta"]), elementSummary(el))
	if js, ok := c["junctions"].(nbt.List); ok {
		for _, j := range js.Items {
			m := j.(map[string]any)
			s += fmt.Sprintf(" j(%d,%d,%d,%d,%v)", nbtI(m["source_x"]), nbtI(m["source_ground_y"]), nbtI(m["source_z"]), nbtI(m["delta_y"]), m["dest_proj"])
		}
	}
	return s
}

func goPieceSummary(p worldgen.StructurePiece) string {
	b := p.BoundingBox()
	bb := []int32{b.MinX, b.MinY, b.MinZ, b.MaxX, b.MaxY, b.MaxZ}
	s := fmt.Sprintf("%s bb=%v", p.Type(), bb)
	jp, ok := p.(*worldgen.JigsawPiece)
	if !ok {
		return s
	}
	var el map[string]any
	json.Unmarshal(jp.ElementJSON(), &el)
	s += fmt.Sprintf(" pos=%d,%d,%d rot=%v gld=%d el=%s", jp.Position[0], jp.Position[1], jp.Position[2],
		rotationSaved[jp.Rotation], jp.GroundLvl, elementSummary(el))
	for _, j := range jp.Junctions {
		s += fmt.Sprintf(" j(%d,%d,%d,%d,%v)", j.SourceX, j.SourceGroundY, j.SourceZ, j.DeltaY, []string{"terrain_matching", "rigid"}[j.DestProjection])
	}
	return s
}

// compareStarts diffs every structure start saved in the areas'
// chunks against mc-go's; it returns the number of starts compared.
func compareStarts(t *testing.T, region string, g *worldgen.Generator, areas []terrainArea) int {
	t.Helper()
	n := 0
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				c, ok := savedRegionChunk(t, region, cx, cz)
				var want map[string]map[string]any
				if ok {
					if st, ok := c["structures"].(map[string]any); ok {
						if ss, ok := st["starts"].(map[string]any); ok {
							want = map[string]map[string]any{}
							for k, v := range ss {
								want[strings.TrimPrefix(k, "minecraft:")] = v.(map[string]any)
							}
						}
					}
				}
				got := map[string]*worldgen.StructureStart{}
				for _, s := range g.StructureStarts(cx, cz) {
					got[s.Structure] = s
				}
				if !ok {
					if len(got) > 0 {
						t.Logf("chunk %d,%d not saved by vanilla; mc-go has %d starts", cx, cz, len(got))
					}
					continue
				}
				for name, w := range want {
					n++
					gs := got[name]
					if gs == nil {
						t.Errorf("chunk %d,%d: vanilla start %s missing in mc-go", cx, cz, name)
						continue
					}
					kids, _ := w["Children"].(nbt.List)
					if len(kids.Items) != len(gs.Pieces) {
						t.Errorf("chunk %d,%d %s: %d pieces, vanilla %d", cx, cz, name, len(gs.Pieces), len(kids.Items))
					}
					bad := 0
					for i := 0; i < len(kids.Items) && i < len(gs.Pieces); i++ {
						ws := savedPieceSummary(kids.Items[i].(map[string]any))
						gp := goPieceSummary(gs.Pieces[i])
						if ws != gp {
							if bad < 3 {
								t.Errorf("chunk %d,%d %s piece %d:\n  mc-go   %s\n  vanilla %s", cx, cz, name, i, gp, ws)
							}
							bad++
						}
					}
					if bad == 0 && len(kids.Items) == len(gs.Pieces) {
						t.Logf("chunk %d,%d %s: %d pieces match", cx, cz, name, len(gs.Pieces))
					}
				}
				for name := range got {
					if want[name] == nil {
						t.Errorf("chunk %d,%d: mc-go start %s not in vanilla", cx, cz, name)
					}
				}
			}
		}
	}
	return n
}

// structureParity runs one overworld structure pin over areas
// (inclusive chunk rectangles); the interior chunks are compared block
// for block.
func structureParity(t *testing.T, seed int64, areas []terrainArea, sets ...string) {
	t.Helper()
	structureParityDim(t, seed, "overworld", "overworld", "overworld", structureOverrides(nil, sets...), areas)
}

// structureParityDim is structureParity for any dimension (noise
// settings and biome list as NewGenerator takes them) over explicit
// stage data.
func structureParityDim(t *testing.T, seed int64, dim, settings, biomes string, ov map[string]json.RawMessage, areas []terrainArea) {
	t.Helper()
	region := vanillaStructureTerrain(t, seed, stageDatapack(ov), dim, areas)
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), settings, biomes, seed)
	if err != nil {
		t.Fatal(err)
	}
	if dim != "overworld" {
		// The nether and end dimension types span y 0..255.
		g.RS.SetLevelBounds(0, 256)
	}
	g.Structures = true
	if n := compareStarts(t, region, g, areas); n == 0 {
		t.Errorf("no structure starts in the compared areas")
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
	minY, height := g.RS.LevelMinY(), g.RS.LevelHeight()
	total, orderDep, exempt, beOK := 0, 0, 0, 0
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
					if (gs == "air" || gs == "cave_air") && strings.Contains(w, "[level=") {
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
				beOK += compareStructBlockEntities(t, saved, p, cx, cz, minY)
				total++
			}
		}
	}
	if len(worldgen.UnsupportedProcessors) > 0 {
		t.Errorf("unsupported template processors passed blocks through: %v", worldgen.UnsupportedProcessors)
	}
	if len(worldgen.UnsupportedFeatures) > 0 {
		t.Errorf("unsupported feature types placed nothing: %v", worldgen.UnsupportedFeatures)
	}
	t.Logf("%d chunks compared; %d order-dependent cells, %d fluid post-processing cells accepted; %d block entities matched",
		total, orderDep, exempt, beOK)
}

// compareStructBlockEntities checks that the saved chunk's block entities sit
// exactly where mc-go placed blocks that carry one.
func compareStructBlockEntities(t *testing.T, saved savedChunk, p *worldgen.ProtoChunk, cx, cz, minY int32) int {
	t.Helper()
	want := map[[3]int32]string{}
	if l, ok := saved.raw["block_entities"].(nbt.List); ok {
		for _, it := range l.Items {
			m := it.(map[string]any)
			want[[3]int32{nbtI(m["x"]), nbtI(m["y"]), nbtI(m["z"])}] = strings.TrimPrefix(fmt.Sprint(m["id"]), "minecraft:")
		}
	}
	var got [][3]int32
	for i, b := range p.Blocks {
		if worldgen.HasBlockEntity(b) {
			got = append(got, [3]int32{cx*16 + int32(i&15), int32(i>>8) + minY, cz*16 + int32((i>>4)&15)})
		}
	}
	n := 0
	seen := map[[3]int32]bool{}
	for _, pos := range got {
		seen[pos] = true
		if _, ok := want[pos]; ok {
			n++
			continue
		}
		t.Errorf("chunk %d,%d: mc-go block entity block at %v, none saved by vanilla", cx, cz, pos)
	}
	var missing []string
	for pos, id := range want {
		if !seen[pos] {
			missing = append(missing, fmt.Sprintf("%s@%v", id, pos))
		}
	}
	sort.Strings(missing)
	for i, m := range missing {
		if i < 5 {
			t.Errorf("chunk %d,%d: vanilla block entity %s has no mc-go block", cx, cz, m)
		}
	}
	return n
}

// TestStructureVillagePlainsParity pins a plains village: the jigsaw
// placement (start pool, children by placement priority, rigid and
// terrain-matching projection, the expansion hack, junctions), the
// beardifier's beard_thin terrain around it, and the pieces' template
// placement with legacy elements, gravity, mossify and street rules and
// feature elements.
func TestStructureVillagePlainsParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-14, -58, -7, -48}}, "villages")
}

// TestStructureVillageDesertParity pins a desert village (sandstone
// streets over sand, the desert farm and zombie-free processors).
func TestStructureVillageDesertParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-152, 33, -140, 43}}, "villages")
}

// TestStructureVillageSavannaParity pins a savanna village.
func TestStructureVillageSavannaParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-73, -104, -60, -92}}, "villages")
}

// TestStructureVillageSnowyParity pins a snowy village (ice and snow
// street rules over snowy terrain).
func TestStructureVillageSnowyParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{85, 72, 96, 78}}, "villages")
}

// TestStructurePillagerOutpostParity pins a pillager outpost: the
// legacy_type_1 frequency reduction and the village exclusion zone (the
// village set stays enabled), the tower with its feature-element decor
// and the outpost_rot processor.
func TestStructurePillagerOutpostParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{14, -63, 20, -57}}, "pillager_outposts", "villages")
}

// TestStructureTrailRuinsParity pins trail ruins: start height -15
// projected to the surface, bury terrain adaptation, the capped
// archaeology processors (suspicious gravel and sand among the rule
// replacements) and protected blocks.
func TestStructureTrailRuinsParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-99, 32, -94, 39}}, "trail_ruins")
}

// TestStructureAncientCityParity pins an ancient city: the named start
// jigsaw (city_anchor), absolute start height without projection, the
// 116-block range, beard_box adaptation, the degradation processors and
// the sculk feature elements. The whole city's pieces are compared; the
// blocks of the chunks around its center.
func TestStructureAncientCityParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-75, -51, -67, -43}}, "ancient_cities")
}

// TestStructureTrialChambersParity pins trial chambers: uniform start
// height, dimension padding, ignore_waterlogging liquid settings, the
// random and random-group pool aliases and the copper bulb degradation.
func TestStructureTrialChambersParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-128, 3, -119, 14}}, "trial_chambers")
}

// TestStructureBastionRemnantParity pins a bastion remnant in the
// nether: the nether noise column heights, start height 33 without
// projection or terrain adaptation, the bastion degradation and
// rampart processors. The nether complex set keeps only the bastion
// (the fortress is not a jigsaw structure).
func TestStructureBastionRemnantParity(t *testing.T) {
	ov := structureOverrides(nil, "nether_complexes")
	ov["structure_set/nether_complexes"] = json.RawMessage(`{"placement":{"type":"minecraft:random_spread","salt":30084232,"separation":4,"spacing":27},"structures":[{"structure":"minecraft:bastion_remnant","weight":1}]}`)
	structureParityDim(t, 12345, "the_nether", "nether", "nether", ov, []terrainArea{{-152, -13, -145, -8}})
}

// TestStructureVillageTaigaParity pins a taiga village.
func TestStructureVillageTaigaParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{71, -29, 78, -17}}, "villages")
}
