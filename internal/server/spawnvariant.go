package server

import "math/rand"

// finalizeSpawn's variant roll, measured off the 26.2 datapack
// registries: each *_variant entry carries spawn_conditions
// [{condition?, priority}] — VariantUtils.selectVariantToSpawn keeps
// the entries whose condition matches the SpawnContext biome
// (unconditional entries always match), drops all but the highest
// priority tier, then picks uniformly. Sound variants are picked by
// pickRandomSoundVariant — a flat uniform draw over the whole
// sound-variant registry (Cat/Cow/Pig/Chicken/Wolf only; frog has no
// sound registry, salmon none at all).
//
// The roll runs inside EntityType.spawn for every Mob — /summon
// included — before the custom-NBT load, so an explicit variant /
// sound_variant / type key always overrides the rolled value.

// spawnVariantEntry is one registry entry's spawn condition row.
type spawnVariantEntry struct {
	name string
	pri  int
	// biomes is nil for unconditional entries; otherwise the set of
	// short biome names the condition accepts.
	biomes map[string]bool
}

// warmFarmBiomes = #minecraft:spawns_warm_variant_farm_animals with
// is_jungle/is_savanna/is_nether/is_badlands expanded.
var warmFarmBiomes = map[string]bool{
	"desert": true, "warm_ocean": true, "mangrove_swamp": true,
	"deep_lukewarm_ocean": true, "lukewarm_ocean": true,
	"bamboo_jungle": true, "jungle": true, "sparse_jungle": true,
	"savanna": true, "savanna_plateau": true, "windswept_savanna": true,
	"nether_wastes": true, "soul_sand_valley": true, "crimson_forest": true,
	"warped_forest": true, "basalt_deltas": true,
	"badlands": true, "eroded_badlands": true, "wooded_badlands": true,
}

// coldFarmBiomes = #minecraft:spawns_cold_variant_farm_animals with
// is_end expanded.
var coldFarmBiomes = map[string]bool{
	"snowy_plains": true, "ice_spikes": true, "frozen_peaks": true,
	"jagged_peaks": true, "snowy_slopes": true, "frozen_ocean": true,
	"deep_frozen_ocean": true, "grove": true, "deep_dark": true,
	"frozen_river": true, "snowy_taiga": true, "snowy_beach": true,
	"cold_ocean": true, "deep_cold_ocean": true,
	"old_growth_pine_taiga": true, "old_growth_spruce_taiga": true,
	"taiga": true, "windswept_forest": true,
	"windswept_gravelly_hills": true, "windswept_hills": true,
	"stony_peaks": true,
	"the_end":     true, "end_highlands": true, "end_midlands": true,
	"small_end_islands": true, "end_barrens": true,
}

// warmFrogBiomes = #minecraft:spawns_warm_variant_frogs expanded.
var warmFrogBiomes = map[string]bool{
	"desert": true, "warm_ocean": true, "mangrove_swamp": true,
	"bamboo_jungle": true, "jungle": true, "sparse_jungle": true,
	"savanna": true, "savanna_plateau": true, "windswept_savanna": true,
	"nether_wastes": true, "soul_sand_valley": true, "crimson_forest": true,
	"warped_forest": true, "basalt_deltas": true,
	"badlands": true, "eroded_badlands": true, "wooded_badlands": true,
}

// coldFrogBiomes = #minecraft:spawns_cold_variant_frogs expanded.
var coldFrogBiomes = map[string]bool{
	"snowy_plains": true, "ice_spikes": true, "frozen_peaks": true,
	"jagged_peaks": true, "snowy_slopes": true, "frozen_ocean": true,
	"deep_frozen_ocean": true, "grove": true, "deep_dark": true,
	"frozen_river": true, "snowy_taiga": true, "snowy_beach": true,
	"the_end": true, "end_highlands": true, "end_midlands": true,
	"small_end_islands": true, "end_barrens": true,
}

// jungleBiomes/savannaBiomes/badlandsBiomes are the wolf variant
// conditions' tag expansions.
var jungleBiomes = map[string]bool{
	"bamboo_jungle": true, "jungle": true, "sparse_jungle": true,
}
var savannaBiomes = map[string]bool{
	"savanna": true, "savanna_plateau": true, "windswept_savanna": true,
}
var badlandsBiomes = map[string]bool{
	"badlands": true, "eroded_badlands": true, "wooded_badlands": true,
}

// spawnVariantTables is each holder-variant registry's spawn
// conditions, keyed by mc-side type name.
var spawnVariantTables = map[string][]spawnVariantEntry{
	"cat": {
		{name: "all_black"}, {name: "black"}, {name: "british_shorthair"},
		{name: "calico"}, {name: "jellie"}, {name: "persian"},
		{name: "ragdoll"}, {name: "red"}, {name: "siamese"},
		{name: "tabby"}, {name: "white"},
	},
	"frog": {
		{name: "cold", pri: 1, biomes: coldFrogBiomes},
		{name: "temperate"},
		{name: "warm", pri: 1, biomes: warmFrogBiomes},
	},
	"wolf": {
		{name: "ashen", pri: 1, biomes: map[string]bool{"snowy_taiga": true}},
		{name: "black", pri: 1, biomes: map[string]bool{"old_growth_pine_taiga": true}},
		{name: "chestnut", pri: 1, biomes: map[string]bool{"old_growth_spruce_taiga": true}},
		{name: "pale"},
		{name: "rusty", pri: 1, biomes: jungleBiomes},
		{name: "snowy", pri: 1, biomes: map[string]bool{"grove": true}},
		{name: "spotted", pri: 1, biomes: savannaBiomes},
		{name: "striped", pri: 1, biomes: badlandsBiomes},
		{name: "woods", pri: 1, biomes: map[string]bool{"forest": true}},
	},
	"pig": {
		{name: "cold", pri: 1, biomes: coldFarmBiomes},
		{name: "temperate"},
		{name: "warm", pri: 1, biomes: warmFarmBiomes},
	},
	"cow": {
		{name: "cold", pri: 1, biomes: coldFarmBiomes},
		{name: "temperate"},
		{name: "warm", pri: 1, biomes: warmFarmBiomes},
	},
	"chicken": {
		{name: "cold", pri: 1, biomes: coldFarmBiomes},
		{name: "temperate"},
		{name: "warm", pri: 1, biomes: warmFarmBiomes},
	},
}

// spawnSoundVariantNames is each sound-variant registry in order —
// pickRandomSoundVariant is Registry.getRandom, a uniform draw.
var spawnSoundVariantNames = map[string][]string{
	"cat":     {"classic", "royal"},
	"wolf":    {"angry", "big", "classic", "cute", "grumpy", "puglin", "sad"},
	"cow":     {"classic", "moody"},
	"pig":     {"big", "classic", "mini"},
	"chicken": {"classic", "picky"},
}

// rollSpawnVariant emulates VariantUtils.selectVariantToSpawn: filter
// to entries whose condition matches worldBiome, keep the highest
// priority tier, uniform pick. "" when the type has no variant
// registry (salmon).
func rollSpawnVariant(typ, worldBiome string) string {
	entries, ok := spawnVariantTables[typ]
	if !ok {
		return ""
	}
	best := -1 << 30
	var elig []string
	for _, e := range entries {
		if e.biomes != nil && !e.biomes[worldBiome] {
			continue
		}
		if e.pri > best {
			best = e.pri
			elig = elig[:0]
		}
		if e.pri == best {
			elig = append(elig, e.name)
		}
	}
	if len(elig) == 0 {
		return ""
	}
	return elig[rand.Intn(len(elig))]
}

// rollSoundVariant is pickRandomSoundVariant: a flat uniform draw over
// the sound-variant registry. "" for types without one.
func rollSoundVariant(typ string) string {
	names, ok := spawnSoundVariantNames[typ]
	if !ok || len(names) == 0 {
		return ""
	}
	return names[rand.Intn(len(names))]
}
