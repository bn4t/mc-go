package server

import (
	"strconv"
	"strings"

	"mc-go/internal/world"
)

// Precipitation, measured off the 26.2 jar (ServerLevel.tickPrecipitation):
//
//   - Called once per 1/48 roll of each random-tick-speed iteration per
//     ticking chunk, on a random column.
//   - The top of the MOTION_BLOCKING column (fluids count) decides: the
//     cell below it freezes to ice when the biome freezes, and snow
//     forms/piles at the top cell when it is raining, the biome snows,
//     and max_snow_accumulation_height allows it.
//   - Existing snow piles until layers reach min(maxAcc, 8); an empty
//     top cell takes the snow default; anything else there is
//     overwritten (vanilla writes unconditionally).
//
// mc-go models one biome per dimension, so the biome checks collapse to
// a lookup. The fluid-in-heightmap rule means a water cell can freeze
// in place while a snow layer lands above the surface of a dry column.
func (s *Server) tickPrecipitation(d *dimension, cx, cz int) {
	x := cx*16 + s.rng.intn(16)
	z := cz*16 + s.rng.intn(16)
	// The shard's cached MOTION_BLOCKING top is the column surface;
	// pos2 sits on it and pos1 is the surface cell itself.
	surface := d.w.TopMotionBlocking(x, z, d.minY, d.minY+d.height-1)
	pos1 := world.Pos{X: x, Y: surface, Z: z}
	pos2 := posAbove(pos1, 1)
	if biomeFreezes(s.biomeAt(d, pos1)) {
		if name, _ := world.StateOwner(d.w.GetBlock(pos1)); name == "water" {
			ice, _ := world.BlockStateOf("ice", nil)
			s.setNaturalBlock(d, pos1, ice)
		}
	}
	if !s.weatherRaining.Load() || !biomeSnows(s.biomeAt(d, pos2)) {
		return
	}
	maxAcc := s.gameRuleInt("minecraft:max_snow_accumulation_height")
	if maxAcc <= 0 {
		return
	}
	cur := d.w.GetBlock(pos2)
	if name, _ := world.StateOwner(cur); name == "snow" {
		layers, _ := intProp(cur, "layers")
		if layers < min(maxAcc, 8) {
			if nb, ok := world.WithProperty(cur, "layers", strconv.Itoa(layers+1)); ok {
				s.setNaturalBlock(d, pos2, nb)
			}
		}
		return
	}
	snow, _ := world.BlockStateOf("snow", nil)
	s.setNaturalBlock(d, pos2, snow)
}

// biomeSnows reports whether the dimension's biome accumulates snow —
// vanilla's shouldSnow is the cold half of the precipitation table
// (snowy plains and colder, plus the frozen/mountain set).
func biomeSnows(biome string) bool {
	switch strings.TrimPrefix(biome, "minecraft:") {
	case "snowy_plains", "ice_spikes", "snowy_taiga", "snowy_beach",
		"frozen_ocean", "deep_frozen_ocean", "frozen_river",
		"grove", "snowy_slopes", "jagged_peaks", "frozen_peaks":
		return true
	}
	return false
}

// biomeFreezes reports whether the biome turns surface water to ice —
// the same cold set drives shouldFreeze.
func biomeFreezes(biome string) bool {
	return biomeSnows(biome)
}
