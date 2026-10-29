package worldgen

import (
	"encoding/json"
	"math"
	"sync"

	"mc-go/internal/world"
)

type geodeConfig struct {
	filling, inner, alt, middle, outer stateProvider
	placements                         []world.Block
	cannotReplace, invalid             map[string]bool

	fillingL, innerL, middleL, outerL float64
	crackChance, crackSize            float64
	crackOffset                       int32

	potentialChance, altChance float64
	requireAlt                 bool
	wallDist, points, ptOffset intProvider
	wallDistMax                int32
	minOff, maxOff             int32
	noiseMul                   float64
	threshold                  int32
}

// intProviderMax is IntProvider.maxInclusive for the providers geode
// configurations use.
func intProviderMax(raw json.RawMessage) int32 {
	if isNumber(raw) {
		return int32(obj{"v": raw}.f64("v"))
	}
	var o obj
	jsonUnmarshal(raw, &o)
	switch trimNS(o.str("type")) {
	case "constant":
		return o.i32("value")
	case "uniform", "biased_to_bottom", "clamped", "trapezoid":
		return o.i32("max_inclusive")
	}
	panic("worldgen: maxInclusive of " + o.str("type"))
}

func optF64(o obj, k string, def float64) float64 {
	if _, ok := o[k]; ok {
		return o.f64(k)
	}
	return def
}

func optI32(o obj, k string, def int32) int32 {
	if _, ok := o[k]; ok {
		return o.i32(k)
	}
	return def
}

func parseGeode(g *Generator, cfg obj) *geodeConfig {
	var blocks, layers, crack obj
	jsonUnmarshal(cfg["blocks"], &blocks)
	jsonUnmarshal(cfg["layers"], &layers)
	jsonUnmarshal(cfg["crack"], &crack)
	c := &geodeConfig{
		filling:       g.compileStateProvider(blocks["filling_provider"]),
		inner:         g.compileStateProvider(blocks["inner_layer_provider"]),
		alt:           g.compileStateProvider(blocks["alternate_inner_layer_provider"]),
		middle:        g.compileStateProvider(blocks["middle_layer_provider"]),
		outer:         g.compileStateProvider(blocks["outer_layer_provider"]),
		placements:    parseStateList(blocks["inner_placements"]),
		cannotReplace: blockNameSet(blocks["cannot_replace"]),
		invalid:       blockNameSet(blocks["invalid_blocks"]),
		// GeodeLayerSettings / GeodeCrackSettings / GeodeConfiguration
		// codec defaults.
		fillingL:        optF64(layers, "filling", 1.7),
		innerL:          optF64(layers, "inner_layer", 2.2),
		middleL:         optF64(layers, "middle_layer", 3.2),
		outerL:          optF64(layers, "outer_layer", 4.2),
		crackChance:     optF64(crack, "generate_crack_chance", 1),
		crackSize:       optF64(crack, "base_crack_size", 2),
		crackOffset:     optI32(crack, "crack_point_offset", 2),
		potentialChance: optF64(cfg, "use_potential_placements_chance", 0.35),
		altChance:       optF64(cfg, "use_alternate_layer0_chance", 0),
		requireAlt:      true,
		minOff:          optI32(cfg, "min_gen_offset", -16),
		maxOff:          optI32(cfg, "max_gen_offset", 16),
		noiseMul:        optF64(cfg, "noise_multiplier", 0.05),
		threshold:       cfg.i32("invalid_blocks_threshold"),
	}
	if v, ok := cfg["placements_require_layer0_alternate"]; ok {
		jsonUnmarshal(v, &c.requireAlt)
	}
	wd := json.RawMessage(`{"type":"minecraft:uniform","min_inclusive":4,"max_inclusive":5}`)
	if v, ok := cfg["outer_wall_distance"]; ok {
		wd = v
	}
	c.wallDist, c.wallDistMax = parseIntProvider(wd), intProviderMax(wd)
	c.points = parseIntProvider(json.RawMessage(`{"type":"minecraft:uniform","min_inclusive":3,"max_inclusive":4}`))
	if v, ok := cfg["distribution_points"]; ok {
		c.points = parseIntProvider(v)
	}
	c.ptOffset = parseIntProvider(json.RawMessage(`{"type":"minecraft:uniform","min_inclusive":1,"max_inclusive":2}`))
	if v, ok := cfg["point_offset"]; ok {
		c.ptOffset = parseIntProvider(v)
	}
	return c
}

func init() {
	featureBuilders["geode"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		gc := parseGeode(g, cfg)
		return func(c *featCtx, r Random, p BlockPos) bool { return placeGeode(c.rg, r, gc, p) }
	}
}

var geodeNoises sync.Map // world seed -> *NormalNoise

// geodeNoise is the NormalNoise GeodeFeature.place builds from the
// world seed: WorldgenRandom(LegacyRandomSource(seed)), first octave
// -4, one amplitude of 1.
func geodeNoise(seed int64) *NormalNoise {
	if v, ok := geodeNoises.Load(seed); ok {
		return v.(*NormalNoise)
	}
	n := NewNormalNoise(NewLegacy(seed), NoiseParams{FirstOctave: -4, Amplitudes: []float64{1}})
	geodeNoises.Store(seed, n)
	return n
}

func invSqrt(d float64) float64 { return 1 / math.Sqrt(d) }

func distSqr(a, b BlockPos) float64 {
	dx, dy, dz := float64(a[0]-b[0]), float64(a[1]-b[1]), float64(a[2]-b[2])
	return float64(dx*dx) + float64(dy*dy) + float64(dz*dz)
}

// placeGeode is GeodeFeature.place: distribution points scattered by
// the outer wall distance (too many air/invalid points abort), an
// optional crack line, then every cell of the 33^3 box is shelled by
// its summed inverse distance to the points (plus seed noise) into
// filling, inner (or budding alternate), middle and outer layers;
// remembered budding cells finally sprout one cluster each.
func placeGeode(rg *Region, r Random, c *geodeConfig, o BlockPos) bool {
	type point struct {
		p   BlockPos
		off int32
	}
	var pts []point
	n := c.points(r)
	noise := geodeNoise(rg.g.RS.Seed)
	var cracks []BlockPos
	d13 := float64(n) / float64(c.wallDistMax)
	dFill := 1 / math.Sqrt(c.fillingL)
	dInner := 1 / math.Sqrt(c.innerL+d13)
	dMiddle := 1 / math.Sqrt(c.middleL+d13)
	dOuter := 1 / math.Sqrt(c.outerL+d13)
	extra := 0.0
	nd := r.NextDouble()
	if n > 3 {
		extra = d13
	}
	dCrack := 1 / math.Sqrt(c.crackSize+nd/2+extra)
	crack := float64(r.NextFloat()) < c.crackChance
	bad := int32(0)
	for i := int32(0); i < n; i++ {
		dx := c.wallDist(r)
		dy := c.wallDist(r)
		dz := c.wallDist(r)
		p := offset(o, dx, dy, dz)
		st := rg.at(p)
		if isAir(st) || inTag(st, c.invalid) {
			bad++
			if bad > c.threshold {
				return false
			}
		}
		pts = append(pts, point{p, c.ptOffset(r)})
	}
	if crack {
		k := r.NextIntN(4)
		m := n*2 + 1
		switch k {
		case 0:
			cracks = append(cracks, offset(o, m, 7, 0), offset(o, m, 5, 0), offset(o, m, 1, 0))
		case 1:
			cracks = append(cracks, offset(o, 0, 7, m), offset(o, 0, 5, m), offset(o, 0, 1, m))
		case 2:
			cracks = append(cracks, offset(o, m, 7, m), offset(o, m, 5, m), offset(o, m, 1, m))
		default:
			cracks = append(cracks, offset(o, 0, 7, 0), offset(o, 0, 5, 0), offset(o, 0, 1, 0))
		}
	}
	var potential []BlockPos
	ok := func(b world.Block) bool { return !inTag(b, c.cannotReplace) }
	for z := o[2] + c.minOff; z <= o[2]+c.maxOff; z++ {
		for y := o[1] + c.minOff; y <= o[1]+c.maxOff; y++ {
			for x := o[0] + c.minOff; x <= o[0]+c.maxOff; x++ {
				p := BlockPos{x, y, z}
				nv := float64(noise.Value(float64(x), float64(y), float64(z)) * c.noiseMul)
				s, cs := 0.0, 0.0
				for _, pt := range pts {
					s += invSqrt(distSqr(p, pt.p)+float64(pt.off)) + nv
				}
				for _, q := range cracks {
					cs += invSqrt(distSqr(p, q)+float64(c.crackOffset)) + nv
				}
				if s < dOuter {
					continue
				}
				switch {
				case crack && cs >= dCrack && s < dFill:
					safeSet(rg, p, world.Air, ok)
				case s >= dFill:
					safeSet(rg, p, c.filling.getState(rg, r, p), ok)
				case s >= dInner:
					alt := float64(r.NextFloat()) < c.altChance
					if alt {
						safeSet(rg, p, c.alt.getState(rg, r, p), ok)
					} else {
						safeSet(rg, p, c.inner.getState(rg, r, p), ok)
					}
					if (!c.requireAlt || alt) && float64(r.NextFloat()) < c.potentialChance {
						potential = append(potential, p)
					}
				case s >= dMiddle:
					safeSet(rg, p, c.middle.getState(rg, r, p), ok)
				default:
					safeSet(rg, p, c.outer.getState(rg, r, p), ok)
				}
			}
		}
	}
	for _, p := range potential {
		st := c.placements[r.NextIntN(int32(len(c.placements)))]
		for d := 0; d < 6; d++ {
			if world.HasProperty(st, "facing") {
				st, _ = world.WithProperty(st, "facing", dirNameOf[d])
			}
			f := dirOffsets[d]
			q := offset(p, f[0], f[1], f[2])
			at := rg.at(q)
			if world.HasProperty(st, "waterlogged") {
				v := "false"
				if hasFluid(at) && isFluidSource(at) {
					v = "true"
				}
				st, _ = world.WithProperty(st, "waterlogged", v)
			}
			if isAir(at) || (isBlock(at, "water") && fluidAmount(at) == 8) {
				safeSet(rg, q, st, ok)
				break
			}
		}
	}
	return true
}
