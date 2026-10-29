//go:build blackbox

package blackbox

import (
	"encoding/json"
	"testing"

	"mc-go/internal/worldgen"
)

// endfAddToBiomes appends placed features to one decoration step of
// every biome (worldgen overrides for nethCase.extra).
func endfAddToBiomes(step int, placed ...string) map[string]string {
	out := map[string]string{}
	data := worldgen.Vanilla()
	for _, k := range worldgen.VanillaKeys("biome/") {
		raw, _ := data.Get(k)
		var b map[string]json.RawMessage
		json.Unmarshal(raw, &b)
		var steps [][]string
		if json.Unmarshal(b["features"], &steps) != nil || len(steps) <= step {
			continue
		}
		for _, p := range placed {
			steps[step] = append(steps[step], "minecraft:"+p)
		}
		b["features"], _ = json.Marshal(steps)
		enc, _ := json.Marshal(b)
		out[k] = string(enc)
	}
	return out
}

// TestFeatureOverworldMiscParity pins the overworld members of the
// group: ice spikes (SpikeFeature, with the ice patches, sand/clay/
// gravel disks and freeze layer around them) in the ice spikes biome,
// and — added to every biome on both servers, since vanilla only places
// them from villages, the bonus-chest option and the void preset — the
// five block piles (hay, melon, snow, ice, pumpkin) on the surface, the
// bonus chest with its torches and the void start platform at the
// origin.
func TestFeatureOverworldMiscParity(t *testing.T) {
	extra := endfAddToBiomes(9, "endf_pile_hay", "endf_pile_melon", "endf_pile_snow", "endf_pile_ice",
		"endf_pile_pumpkin", "endf_bonus_chest", "void_start_platform")
	for _, p := range []string{"hay", "melon", "snow", "ice", "pumpkin"} {
		extra["placed_feature/endf_pile_"+p] = `{"feature":"minecraft:pile_` + p + `","placement":[` +
			`{"type":"minecraft:count","count":2},{"type":"minecraft:in_square"},` +
			`{"type":"minecraft:heightmap","heightmap":"WORLD_SURFACE_WG"},{"type":"minecraft:biome"}]}`
	}
	extra["placed_feature/endf_bonus_chest"] = `{"feature":"minecraft:bonus_chest","placement":[{"type":"minecraft:biome"}]}`
	nethParity(t, nethCase{
		seed: 12345, dim: "overworld", settings: "overworld", biomes: "overworld",
		areas:   []terrainArea{{48, -48, 51, -45}, {-2, -2, 1, 1}},
		extra:   extra,
		allowed: []string{"spike", "disk", "freeze_top_layer", "block_pile", "bonus_chest", "void_start_platform"},
		require: map[string]int{"packed_ice": 500, "hay_block": 20, "melon": 20, "snow": 20, "chest": 20, "torch": 20,
			"cobblestone": 1, "stone": 200},
	})
}

// endAreas (seed 12345): the main island with the spike ring and the
// arrival platform's chunk (6, 0), end highlands and small end islands.
var endAreas = []terrainArea{{-4, -4, 7, 3}, {64, -104, 67, -101}, {-56, -120, -53, -117}}

// endFeatureTypes are the types the end biomes' decoration reaches.
var endFeatureTypes = []string{"chorus_plant", "end_gateway", "end_island", "end_spike", "end_platform",
	"random_selector", "simple_random_selector", "random_boolean_selector", "sequence", "weighted_random_selector"}

// TestFeatureEndParity pins the end decoration: the ten obsidian spikes
// on the main island (seeded shuffle of sizes, radius-42 ring, air
// cleared above y 65, iron-bar cages on the two guarded ones, the
// crystals' bedrock and fire), the obsidian arrival platform at
// (100, 49, 0), chorus plants and return gateways (with their block
// entities) in the highlands and the floating end-stone islands of the
// small end islands. The gateway's 1-in-700 rarity is raised to 1 in 4
// and the islands' 1 in 14 to 1 in 2 on both servers so the areas hold
// enough of them. The exit portal at the
// origin is the dragon fight's, which never runs on a frozen,
// player-less server.
func TestFeatureEndParity(t *testing.T) {
	nethParity(t, nethCase{
		seed: 12345, dim: "the_end", settings: "end", biomes: "the_end",
		areas: endAreas,
		extra: map[string]string{
			"placed_feature/end_gateway_return": `{"feature":"minecraft:end_gateway_return","placement":[` +
				`{"type":"minecraft:rarity_filter","chance":4},{"type":"minecraft:in_square"},` +
				`{"type":"minecraft:heightmap","heightmap":"MOTION_BLOCKING"},` +
				`{"type":"minecraft:random_offset","xz_spread":0,"y_spread":{"type":"minecraft:uniform","min_inclusive":3,"max_inclusive":9}},` +
				`{"type":"minecraft:biome"}]}`,
			"placed_feature/end_island_decorated": `{"feature":"minecraft:end_island","placement":[` +
				`{"type":"minecraft:rarity_filter","chance":2},{"type":"minecraft:count","count":{"type":"minecraft:weighted_list",` +
				`"distribution":[{"data":1,"weight":3},{"data":2,"weight":1}]}},{"type":"minecraft:in_square"},` +
				`{"type":"minecraft:height_range","height":{"type":"minecraft:uniform","max_inclusive":{"absolute":70},"min_inclusive":{"absolute":55}}},` +
				`{"type":"minecraft:biome"}]}`,
		},
		allowed: endFeatureTypes,
		require: map[string]int{"obsidian": 2000, "iron_bars": 50, "fire": 2, "chorus_plant": 100, "chorus_flower": 20,
			"end_gateway": 2, "bedrock": 20, "end_stone": 100},
		checkBlockEntities: []string{"end_gateway"},
	})
}
