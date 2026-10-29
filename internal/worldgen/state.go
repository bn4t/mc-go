package worldgen

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// settings is the part of NoiseGeneratorSettings the noise stage reads.
type settings struct {
	name                             string
	minY, height                     int32
	sizeHorizontal, sizeVertical     int32
	seaLevel                         int32
	aquifersEnabled, oreVeinsEnabled bool
	legacyRandom                     bool
	defaultBlock                     string
	defaultFluid                     Substance
	raw                              obj
}

// RandomState is vanilla's RandomState: the seeded noise router plus
// the positional randoms the aquifer and ore veins draw from.
type RandomState struct {
	Seed          int64
	data          Data
	settings      settings
	random        PositionalRandom
	aquiferRandom PositionalRandom
	oreRandom     PositionalRandom
	router        Router
	markers       []*markerDF
	b             *builder
	surfOnce      sync.Once
	surf          *surfaceSystem
	biomeMu       sync.Mutex
	biomes        map[string]*BiomeInfo
	carverMu      sync.Mutex
	carvers       map[string]*carverConfig
}

// NewRandomState seeds noise settings ("overworld", "nether", ...)
// from data for the world seed.
func NewRandomState(data Data, settingsName string, seed int64) (*RandomState, error) {
	raw, ok := data.Get("noise_settings/" + settingsName)
	if !ok {
		return nil, fmt.Errorf("worldgen: unknown noise settings %q", settingsName)
	}
	var o obj
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	var noise obj
	json.Unmarshal(o["noise"], &noise)
	s := settings{
		name:           settingsName,
		minY:           noise.i32("min_y"),
		height:         noise.i32("height"),
		sizeHorizontal: noise.i32("size_horizontal"),
		sizeVertical:   noise.i32("size_vertical"),
		seaLevel:       o.i32("sea_level"),
		raw:            o,
	}
	json.Unmarshal(o["aquifers_enabled"], &s.aquifersEnabled)
	json.Unmarshal(o["ore_veins_enabled"], &s.oreVeinsEnabled)
	json.Unmarshal(o["legacy_random_source"], &s.legacyRandom)
	var blk struct {
		Name string `json:"Name"`
	}
	json.Unmarshal(o["default_block"], &blk)
	s.defaultBlock = strings.TrimPrefix(blk.Name, "minecraft:")
	json.Unmarshal(o["default_fluid"], &blk)
	switch strings.TrimPrefix(blk.Name, "minecraft:") {
	case "lava":
		s.defaultFluid = SubLava
	case "water":
		s.defaultFluid = SubWater
	default:
		s.defaultFluid = SubAir
	}

	rs := &RandomState{Seed: seed, data: data, settings: s}
	var base Random
	if s.legacyRandom {
		base = NewLegacy(seed)
	} else {
		base = NewXoroshiro(seed)
	}
	rs.random = base.ForkPositional()
	rs.aquiferRandom = rs.random.FromHashOf("minecraft:aquifer").ForkPositional()
	rs.oreRandom = rs.random.FromHashOf("minecraft:ore").ForkPositional()

	b := &builder{data: data, random: rs.random, seed: seed, legacy: s.legacyRandom,
		named: map[string]DF{}, noises: map[string]*NormalNoise{}}
	rs.b = b
	var router obj
	json.Unmarshal(o["noise_router"], &router)
	var perr error
	parse := func(k string) DF {
		if perr != nil {
			return constDF(0)
		}
		defer func() {
			if r := recover(); r != nil {
				perr = fmt.Errorf("worldgen: router %s: %v", k, r)
			}
		}()
		return b.parse(router[k])
	}
	rs.router = Router{
		Barrier:                 parse("barrier"),
		FluidFloodedness:        parse("fluid_level_floodedness"),
		FluidSpread:             parse("fluid_level_spread"),
		Lava:                    parse("lava"),
		Temperature:             parse("temperature"),
		Vegetation:              parse("vegetation"),
		Continents:              parse("continents"),
		Erosion:                 parse("erosion"),
		Depth:                   parse("depth"),
		Ridges:                  parse("ridges"),
		PreliminarySurfaceLevel: parse("preliminary_surface_level"),
		FinalDensity:            parse("final_density"),
		VeinToggle:              parse("vein_toggle"),
		VeinRidged:              parse("vein_ridged"),
		VeinGap:                 parse("vein_gap"),
	}
	if perr != nil {
		return nil, perr
	}
	rs.markers = b.markers
	return rs, nil
}

// MinY and Height are the settings' generation bounds.
func (rs *RandomState) MinY() int32   { return rs.settings.minY }
func (rs *RandomState) Height() int32 { return rs.settings.height }

// SeaLevel is the settings' sea level.
func (rs *RandomState) SeaLevel() int32 { return rs.settings.seaLevel }

// DefaultBlock is the settings' default block name.
func (rs *RandomState) DefaultBlock() string { return rs.settings.defaultBlock }
