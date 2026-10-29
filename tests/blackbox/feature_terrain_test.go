//go:build blackbox

package blackbox

import (
	"sort"
	"strings"
	"testing"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// featureWrites decorates the areas' interior chunks in mc-go alone,
// with only the given feature types kept, and counts the block names
// the features wrote there - a coverage guard showing that a parity
// area really exercises the feature.
func featureWrites(t *testing.T, seed int64, areas []terrainArea, types ...string) map[string]int {
	t.Helper()
	want := map[string]bool{}
	for _, ty := range types {
		want[ty] = true
	}
	keep := func(p string) bool { return want[worldgen.PlacedFeatureType(worldgen.Vanilla(), p)] }
	ov := worldgen.StageOverrides(worldgen.Vanilla(), keep, true, true)
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	inner := map[[2]int32]bool{}
	var order [][2]int32
	for _, a := range areas {
		for cz := a.z0; cz <= a.z1; cz++ {
			for cx := a.x0; cx <= a.x1; cx++ {
				order = append(order, [2]int32{cx, cz})
				if cx > a.x0 && cx < a.x1 && cz > a.z0 && cz < a.z1 {
					inner[[2]int32{cx, cz}] = true
				}
			}
		}
	}
	g.WriteLog = func(p worldgen.BlockPos, c [2]int32, b world.Block) {
		if inner[[2]int32{p[0] >> 4, p[2] >> 4}] {
			n, _ := world.StateOwner(b)
			counts[n]++
		}
	}
	g.GenerateArea(order)
	return counts
}

// featureParityPost is featureParity with mc-go's post-processing
// stand-in applied to every decorated chunk before the comparison:
// the real server converts its chunks to FULL through
// LevelChunk.postProcessGeneration, which the decoration framework
// does not run, so features whose blocks are only finished there
// (bubble columns over magma) are compared after post().
func featureParityPost(t *testing.T, seed int64, areas []terrainArea, post func(*worldgen.ProtoChunk), types ...string) {
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
	for _, c := range order {
		post(chunks[c])
	}
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

// requireWrites fails unless mc-go's features wrote at least min cells
// of each named block in the compared chunks.
func requireWrites(t *testing.T, counts map[string]int, min map[string]int) {
	t.Helper()
	var got []string
	for k, v := range counts {
		got = append(got, k+"="+itoaB(v))
	}
	sort.Strings(got)
	t.Logf("feature writes: %s", strings.Join(got, " "))
	for b, n := range min {
		if counts[b] < n {
			t.Errorf("only %d %s writes in the compared chunks, want >= %d", counts[b], b, n)
		}
	}
}

func itoaB(v int) string {
	if v == 0 {
		return "0"
	}
	var d []byte
	for v > 0 {
		d = append([]byte{byte('0' + v%10)}, d...)
		v /= 10
	}
	return string(d)
}

// TestFeatureFreezeTopLayerParity pins SnowAndFreezeFeature: ice on
// still water and snow layers (with snowy grass) under the
// MOTION_BLOCKING top of every column, in ice spikes, snowy slopes and
// a frozen ocean.
func TestFeatureFreezeTopLayerParity(t *testing.T) {
	areas := []terrainArea{{46, -50, 50, -46}, {222, 62, 226, 66}, {110, -98, 114, -94}}
	requireWrites(t, featureWrites(t, 12345, areas, "freeze_top_layer"), map[string]int{"snow": 100, "ice": 100})
	featureParity(t, 12345, areas, "freeze_top_layer")
}

// TestFeatureLakeSpringRoomParity pins LakeFeature (underground and
// surface lava lakes), SpringFeature (water and lava springs) and
// MonsterRoomFeature (dungeon walls, floors, chests and spawners).
func TestFeatureLakeSpringRoomParity(t *testing.T) {
	areas := []terrainArea{{-2, -2, 3, 3}, {-178, 110, -174, 114}}
	types := []string{"lake", "spring_feature", "monster_room"}
	requireWrites(t, featureWrites(t, 12345, areas, types...), map[string]int{"lava": 50, "water": 5, "cobblestone": 10, "spawner": 1})
	featureParity(t, 12345, areas, types...)
}

// TestFeatureDiskMagmaParity pins DiskFeature (sand, clay and gravel
// disks under ocean and river water, swamp clay, mangrove grass/dirt
// with its rule-based provider) and UnderwaterMagmaFeature (floor scan,
// per-cell chance and the buried-face test). Magma's bubble columns
// appear when the real server post-processes the chunk (the water cell
// above each magma block is marked, LiquidBlock.tick rebuilds the
// column), so the comparison runs mc-go's bubble-column pass first.
func TestFeatureDiskMagmaParity(t *testing.T) {
	areas := []terrainArea{{-74, -170, -69, -165}, {-138, -90, -133, -85}, {-170, 38, -165, 43}}
	types := []string{"disk", "underwater_magma"}
	requireWrites(t, featureWrites(t, 12345, areas, types...), map[string]int{"sand": 500, "clay": 200, "gravel": 200, "grass_block": 50, "magma_block": 20})
	featureParityPost(t, 12345, areas, worldgen.PostProcessBubbleColumns, types...)
}

// TestFeatureIcebergParity pins IcebergFeature (round and elliptical
// packed and blue ice bergs, snow caps, smoothing and cut-outs) and
// BlueIceFeature (blue ice clusters grown under packed ice) in deep
// frozen and frozen oceans.
func TestFeatureIcebergParity(t *testing.T) {
	areas := []terrainArea{{86, -122, 93, -115}, {110, -98, 117, -91}, {134, -60, 141, -53}}
	types := []string{"iceberg", "blue_ice"}
	requireWrites(t, featureWrites(t, 12345, areas, types...), map[string]int{"packed_ice": 1000, "blue_ice": 1000, "snow_block": 200, "water": 50})
	featureParity(t, 12345, areas, types...)
}

// TestFeatureDesertWellParity pins DesertWellFeature: the sand check,
// the hole test under the rim, the full well and the two suspicious
// sand picks, at two desert chunks where desert_well's 1/1000 rarity
// fires.
func TestFeatureDesertWellParity(t *testing.T) {
	areas := []terrainArea{{-233, -282, -229, -278}, {-224, -281, -220, -277}}
	requireWrites(t, featureWrites(t, 12345, areas, "desert_well"), map[string]int{"sandstone": 200, "suspicious_sand": 4})
	featureParity(t, 12345, areas, "desert_well")
}

// TestFeatureGeodeParity pins GeodeFeature: distribution points and
// their invalid-block abort, crack lines, the seed-noise layer shells
// (air filling, amethyst/budding inner, calcite, smooth basalt) and the
// bud and cluster sprouting with facing and waterlogging.
func TestFeatureGeodeParity(t *testing.T) {
	areas := []terrainArea{{20, 0, 29, 9}}
	requireWrites(t, featureWrites(t, 12345, areas, "geode"), map[string]int{"amethyst_block": 100, "budding_amethyst": 10, "calcite": 100, "smooth_basalt": 100})
	featureParity(t, 12345, areas, "geode")
}

// TestFeatureForestRockParity pins BlockBlobFeature: mossy cobblestone
// rocks sinking to forest_rock_can_place_on in old growth spruce taiga.
func TestFeatureForestRockParity(t *testing.T) {
	areas := []terrainArea{{38, -26, 43, -21}, {22, 6, 27, 11}}
	requireWrites(t, featureWrites(t, 12345, areas, "block_blob"), map[string]int{"mossy_cobblestone": 200})
	featureParity(t, 12345, areas, "block_blob")
}
