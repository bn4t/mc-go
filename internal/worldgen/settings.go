package worldgen

import (
	"fmt"
	"sync"
)

// BlockState names a block state by identifier and properties.
type BlockState struct {
	Name  string
	Props map[string]string
}

// Router holds a dimension's density functions by role.
type Router struct {
	Barrier, FluidLevelFloodedness, FluidLevelSpread, Lava *Spec
	Temperature, Vegetation, Continents, Erosion, Depth    *Spec
	Ridges, PreliminarySurfaceLevel, FinalDensity          *Spec
	VeinToggle, VeinRidged, VeinGap                        *Spec
}

// NoiseSettings is a noise-based generator's configuration.
type NoiseSettings struct {
	MinY, Height                 int
	SizeHorizontal, SizeVertical int
	SeaLevel                     int
	DefaultBlock, DefaultFluid   BlockState
	AquifersEnabled              bool
	OreVeinsEnabled              bool
	LegacyRandomSource           bool
	DisableMobGeneration         bool
	Router                       Router
}

// Settings looks up registered noise settings, e.g. "minecraft:overworld".
func Settings(id string) (*NoiseSettings, bool) {
	s, ok := noiseSettings[id]
	return s, ok
}

// BoundRouter is a router whose functions are bound to one seed.
type BoundRouter struct {
	Barrier, FluidLevelFloodedness, FluidLevelSpread, Lava Func
	Temperature, Vegetation, Continents, Erosion, Depth    Func
	Ridges, PreliminarySurfaceLevel, FinalDensity          Func
	VeinToggle, VeinRidged, VeinGap                        Func
}

// RandomState is the per-seed state of a noise generator: its noises,
// its bound router and the positional sources later stages fork from.
type RandomState struct {
	Seed     int64
	Settings *NoiseSettings
	Random   Positional
	Aquifer  Positional
	Ore      Positional
	Router   BoundRouter

	mu     sync.Mutex
	noises map[string]*Normal
}

// NewRandomState seeds a generator's noises and binds its router.
func NewRandomState(s *NoiseSettings, seed int64) *RandomState {
	rs := &RandomState{Seed: seed, Settings: s, noises: map[string]*Normal{}}
	var root Random
	if s.LegacyRandomSource {
		root = NewLegacy(seed)
	} else {
		root = NewXoroshiro(seed)
	}
	rs.Random = root.ForkPositional()
	rs.Aquifer = rs.Random.FromHash("minecraft:aquifer").ForkPositional()
	rs.Ore = rs.Random.FromHash("minecraft:ore").ForkPositional()
	rs.Router = rs.bindRouter(nil)
	return rs
}

// bindRouter binds the settings' router for this seed; marker, when
// set, replaces cache and interpolation markers.
func (rs *RandomState) bindRouter(marker func(Kind, Func) Func) BoundRouter {
	s := rs.Settings
	b := &binder{noises: rs, refs: densityFunctions, bound: map[string]Func{}, seed: rs.Seed,
		legacy: s.LegacyRandomSource, root: rs.Random, marker: marker}
	r := &s.Router
	return BoundRouter{
		Barrier: b.bind(r.Barrier), FluidLevelFloodedness: b.bind(r.FluidLevelFloodedness),
		FluidLevelSpread: b.bind(r.FluidLevelSpread), Lava: b.bind(r.Lava),
		Temperature: b.bind(r.Temperature), Vegetation: b.bind(r.Vegetation),
		Continents: b.bind(r.Continents), Erosion: b.bind(r.Erosion), Depth: b.bind(r.Depth),
		Ridges: b.bind(r.Ridges), PreliminarySurfaceLevel: b.bind(r.PreliminarySurfaceLevel),
		FinalDensity: b.bind(r.FinalDensity), VeinToggle: b.bind(r.VeinToggle),
		VeinRidged: b.bind(r.VeinRidged), VeinGap: b.bind(r.VeinGap),
	}
}

// Noise returns the world's instance of a registered noise, seeded from
// the identifier's hash — except that legacy-seeded generators draw the
// nether climate noises in sequence from the world seed (+1 for
// vegetation), as the pre-1.18 nether biome source did.
func (rs *RandomState) Noise(id string) *Normal {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if n, ok := rs.noises[id]; ok {
		return n
	}
	p, ok := noiseParams[id]
	if !ok {
		panic(fmt.Sprintf("worldgen: unknown noise %s", id))
	}
	var n *Normal
	switch {
	case rs.Settings.LegacyRandomSource && id == "minecraft:nether/temperature":
		n = NewNormal(NewLegacy(rs.Seed), p, true)
	case rs.Settings.LegacyRandomSource && id == "minecraft:nether/vegetation":
		n = NewNormal(NewLegacy(rs.Seed+1), p, true)
	default:
		n = NewNormal(rs.Random.FromHash(id), p, false)
	}
	rs.noises[id] = n
	return n
}
