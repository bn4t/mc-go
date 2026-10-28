package server

import (
	"strconv"

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
//   - Snow only lands where Biome.shouldSnow allows (see shouldSnow);
//     existing snow piles until layers reach min(maxAcc, 8) and an
//     empty top cell takes the snow default.
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
	if biomeFreezes(s.biomeAt(d, pos1), pos1.Y, int(d.seaLevel)) {
		cur := d.w.GetBlock(pos1)
		name, _ := world.StateOwner(cur)
		// shouldFreeze only takes source water (level 0), not flowing.
		if lvl, _ := world.StateProperty(cur, "level"); name == "water" && lvl == "0" {
			ice, _ := world.BlockStateOf("ice", nil)
			s.setNaturalBlock(d, pos1, ice)
		}
	}
	if !s.weatherRaining.Load() {
		return
	}
	maxAcc := s.gameRuleInt("minecraft:max_snow_accumulation_height")
	if maxAcc > 0 && s.shouldSnow(d, pos2) {
		cur := d.w.GetBlock(pos2)
		if name, _ := world.StateOwner(cur); name == "snow" {
			layers, _ := intProp(cur, "layers")
			if layers < min(maxAcc, 8) {
				if nb, ok := world.WithProperty(cur, "layers", strconv.Itoa(layers+1)); ok {
					s.setNaturalBlock(d, pos2, nb)
				}
			}
		} else {
			snow, _ := world.BlockStateOf("snow", nil)
			s.setNaturalBlock(d, pos2, snow)
		}
	}
	// The cauldron leg acts on the surface cell itself: any biome with
	// precipitation rolls to fill a cauldron exposed there.
	s.cauldronPrecipitation(d, pos1)
}

// shouldSnow is Biome.shouldSnow: snow falls at pos only where the
// biome snows at that height, inside the build height, under block light
// 10, into air or existing snow, and where a snow layer can survive — so
// a cauldron's hollow top or a torch-lit spot stays clear.
func (s *Server) shouldSnow(d *dimension, pos world.Pos) bool {
	if !biomeSnows(s.biomeAt(d, pos), pos.Y, int(d.seaLevel)) {
		return false
	}
	if pos.Y < d.minY || pos.Y >= d.minY+d.height || int(d.blockLight[pos]) >= 10 {
		return false
	}
	cur := d.w.GetBlock(pos)
	switch name, _ := world.StateOwner(cur); name {
	case "air", "cave_air", "void_air", "snow":
	default:
		return false
	}
	return snowLayerCanSurvive(s, d, pos, cur)
}

// cauldronPrecipitation is the handlePrecipitation dispatch on the
// surface block: an empty cauldron turns into water (rain, 5% roll)
// or powder_snow (snow, 10% roll); a layered one matching the
// precipitation type gains a level up to 3.
func (s *Server) cauldronPrecipitation(d *dimension, pos world.Pos) {
	prec := s.precipitationAt(d, pos)
	if prec == "none" {
		return
	}
	chance := 0.05
	if prec == "snow" {
		chance = 0.1
	}
	if s.rng.nextDouble() >= chance {
		return
	}
	cur := d.w.GetBlock(pos)
	name, _ := world.StateOwner(cur)
	switch name {
	case "cauldron":
		target := "water_cauldron"
		if prec == "snow" {
			target = "powder_snow_cauldron"
		}
		nb, _ := world.BlockStateOf(target, nil)
		s.setNaturalBlock(d, pos, nb)
	case "water_cauldron", "powder_snow_cauldron":
		want := "rain"
		if name == "powder_snow_cauldron" {
			want = "snow"
		}
		if prec != want {
			return
		}
		level, _ := intProp(cur, "level")
		if level >= 3 {
			return
		}
		if nb, ok := world.WithProperty(cur, "level", strconv.Itoa(level+1)); ok {
			s.setNaturalBlock(d, pos, nb)
		}
	}
}

// precipitationAt is Biome.getPrecipitationAt: none when the biome has
// no precipitation, snow when it is cold enough at this height, else
// rain. The height adjustment skips vanilla's +-8-block noise term —
// it moves the boundary by under a degree-hundredth of temperature.
func (s *Server) precipitationAt(d *dimension, pos world.Pos) string {
	c, ok := biomeClimates[s.biomeAt(d, pos)]
	if !ok || !c.precip {
		return "none"
	}
	if adjustedTemp(c.temp, pos.Y, int(d.seaLevel)) < 0.15 {
		return "snow"
	}
	return "rain"
}

// adjustedTemp is Biome.getHeightAdjustedTemperature: above seaLevel+17
// the temperature decays 0.00125 per block climbed.
func adjustedTemp(temp float32, y, seaLevel int) float32 {
	boundary := seaLevel + 17
	if y > boundary {
		temp -= float32(y-boundary) * 0.00125
	}
	return temp
}

// biomeSnows is Biome.shouldSnow: precipitation exists and the
// height-adjusted temperature falls under the 0.15 freezing line.
func biomeSnows(biome string, y, seaLevel int) bool {
	c, ok := biomeClimates[biome]
	return ok && c.precip && adjustedTemp(c.temp, y, seaLevel) < 0.15
}

// biomeFreezes reports whether the biome turns surface water to ice —
// vanilla's shouldFreeze uses the same cold test.
func biomeFreezes(biome string, y, seaLevel int) bool {
	return biomeSnows(biome, y, seaLevel)
}
