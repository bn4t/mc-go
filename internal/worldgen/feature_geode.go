package worldgen

import "math"

// Geodes: layered shells around a few random points, measured from
// GeodeFeature (26.2). A block's "sum" adds, per point, the inverse
// distance to it (offset by the point's own offset) plus the shell
// noise; the sum against the layer thresholds picks filling, the inner,
// middle or outer layer. An optional crack is a second sum over three
// points on one side that, past the crack size, leaves air inside the
// filling threshold.

func init() {
	featureKinds["minecraft:geode"] = decodeGeode
}

type geodeConfig struct {
	filling, inner, alternate, middle, outer stateProvider
	placements                               []StateID
	cannotReplace, invalid                   map[string]bool
	fillingLayer, innerLayer                 float64
	middleLayer, outerLayer                  float64
	crackChance, baseCrack                   float64
	crackPointOffset                         int32
	potentialChance, alternateChance         float64
	requireAlternate                         bool
	wallDistance, points, pointOffset        intProvider
	minOffset, maxOffset                     int32
	noiseMultiplier                          float64
	invalidThreshold                         int32
}

func decodeGeode(cfg any) func(*featureCtx) bool {
	blocks, layers, crack := ffield(cfg, "blocks"), ffield(cfg, "layers"), ffield(cfg, "crack")
	intOr := func(v any, lo, hi int32) intProvider {
		if v == nil {
			return uniformInt{lo, hi}
		}
		return decodeIntProvider(v)
	}
	g := &geodeConfig{
		filling:          decodeStateProvider(ffield(blocks, "filling_provider")),
		inner:            decodeStateProvider(ffield(blocks, "inner_layer_provider")),
		alternate:        decodeStateProvider(ffield(blocks, "alternate_inner_layer_provider")),
		middle:           decodeStateProvider(ffield(blocks, "middle_layer_provider")),
		outer:            decodeStateProvider(ffield(blocks, "outer_layer_provider")),
		cannotReplace:    blockSet(ffield(blocks, "cannot_replace")),
		invalid:          blockSet(ffield(blocks, "invalid_blocks")),
		fillingLayer:     fnum(ffield(layers, "filling"), 1.7),
		innerLayer:       fnum(ffield(layers, "inner_layer"), 2.2),
		middleLayer:      fnum(ffield(layers, "middle_layer"), 3.2),
		outerLayer:       fnum(ffield(layers, "outer_layer"), 4.2),
		crackChance:      fnum(ffield(crack, "generate_crack_chance"), 1),
		baseCrack:        fnum(ffield(crack, "base_crack_size"), 2),
		crackPointOffset: fint(ffield(crack, "crack_point_offset"), 2),
		potentialChance:  fnum(ffield(cfg, "use_potential_placements_chance"), 0.35),
		alternateChance:  fnum(ffield(cfg, "use_alternate_layer0_chance"), 0),
		requireAlternate: fbool(ffield(cfg, "placements_require_layer0_alternate"), true),
		wallDistance:     intOr(ffield(cfg, "outer_wall_distance"), 4, 5),
		points:           intOr(ffield(cfg, "distribution_points"), 3, 4),
		pointOffset:      intOr(ffield(cfg, "point_offset"), 1, 2),
		minOffset:        fint(ffield(cfg, "min_gen_offset"), -16),
		maxOffset:        fint(ffield(cfg, "max_gen_offset"), 16),
		noiseMultiplier:  fnum(ffield(cfg, "noise_multiplier"), 0.05),
		invalidThreshold: fint(ffield(cfg, "invalid_blocks_threshold"), 0),
	}
	for _, s := range flist(ffield(blocks, "inner_placements")) {
		g.placements = append(g.placements, blockStateOf(s))
	}
	return g.place
}

// clusterCanGrowAt is where a bud can sit: air, or a water block whose
// fluid is full.
func clusterCanGrowAt(s StateID) bool { return s.isAir() || s.is("water") && s.isFullWater() }

func (g *geodeConfig) place(fc *featureCtx) bool {
	r, random, o := fc.r, fc.rand, fc.origin
	n := g.points.sample(random)
	// The shell noise is seeded from the world seed alone, so every
	// geode of a world shares it.
	noise := NewNormal(NewLegacy(r.seed), NoiseParams{FirstOctave: -4, Amplitudes: []float64{1}}, false)
	d := float64(n) / float64(g.wallDistance.maxValue())
	fillingT := 1 / math.Sqrt(g.fillingLayer)
	innerT := 1 / math.Sqrt(g.innerLayer+d)
	middleT := 1 / math.Sqrt(g.middleLayer+d)
	outerT := 1 / math.Sqrt(g.outerLayer+d)
	extra := 0.0
	if n > 3 {
		extra = d
	}
	crackSize := 1 / math.Sqrt(g.baseCrack+random.NextDouble()/2+extra)
	cracked := float64(random.NextFloat()) < g.crackChance

	type point struct {
		p      Point
		offset int32
	}
	var points []point
	invalid := int32(0)
	for range n {
		dx := g.wallDistance.sample(random)
		dy := g.wallDistance.sample(random)
		dz := g.wallDistance.sample(random)
		p := o.add(Point{dx, dy, dz})
		if s := r.get(p); s.isAir() || g.invalid[s.block()] {
			invalid++
			if invalid > g.invalidThreshold {
				return false
			}
		}
		points = append(points, point{p, g.pointOffset.sample(random)})
	}
	var crackPoints []Point
	if cracked {
		k := 2*n + 1
		var dx, dz int32
		switch random.NextIntn(4) {
		case 0:
			dx = k
		case 1:
			dz = k
		case 2:
			dx, dz = k, k
		}
		for _, y := range [3]int32{7, 5, 1} {
			crackPoints = append(crackPoints, o.add(Point{dx, y, dz}))
		}
	}

	safeSet := func(p Point, s StateID) {
		if !g.cannotReplace[r.get(p).block()] {
			r.set(p, s)
		}
	}
	var potential []Point
	for z := o.Z + g.minOffset; z <= o.Z+g.maxOffset; z++ {
		for y := o.Y + g.minOffset; y <= o.Y+g.maxOffset; y++ {
			for x := o.X + g.minOffset; x <= o.X+g.maxOffset; x++ {
				p := Point{x, y, z}
				nv := noise.Value(float64(x), float64(y), float64(z)) * g.noiseMultiplier
				sum, crackSum := 0.0, 0.0
				for _, q := range points {
					sum += 1/math.Sqrt(distSqr(p, q.p)+float64(q.offset)) + nv
				}
				for _, q := range crackPoints {
					crackSum += 1/math.Sqrt(distSqr(p, q)+float64(g.crackPointOffset)) + nv
				}
				switch {
				case sum < outerT:
				case cracked && crackSum >= crackSize && sum < fillingT:
					safeSet(p, stAir)
					for dir := range 6 {
						q := p.offset(dir)
						if f := r.get(q); f.hasFluid() {
							r.scheduleFluidTick(q, f.fluidName())
						}
					}
				case sum >= fillingT:
					safeSet(p, g.filling.get(r, random, p))
				case sum >= innerT:
					alt := float64(random.NextFloat()) < g.alternateChance
					if alt {
						safeSet(p, g.alternate.get(r, random, p))
					} else {
						safeSet(p, g.inner.get(r, random, p))
					}
					if (!g.requireAlternate || alt) && float64(random.NextFloat()) < g.potentialChance {
						potential = append(potential, p)
					}
				case sum >= middleT:
					safeSet(p, g.middle.get(r, random, p))
				default:
					safeSet(p, g.outer.get(r, random, p))
				}
			}
		}
	}

	for _, p := range potential {
		s := g.placements[random.NextIntn(int32(len(g.placements)))]
		for dir := range 6 {
			if s.hasProp("facing") {
				s = s.with("facing", faceProps[dir])
			}
			q := p.offset(dir)
			at := r.get(q)
			if s.hasProp("waterlogged") {
				s = s.with("waterlogged", boolStr(at.isSourceFluid()))
			}
			if clusterCanGrowAt(at) {
				safeSet(q, s)
				break
			}
		}
	}
	return true
}
