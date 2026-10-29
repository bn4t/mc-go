package worldgen

import (
	"mc-go/internal/world"
)

// speleothem / speleothem_cluster (SpeleothemFeature,
// SpeleothemClusterFeature, SpeleothemUtils): pointed dripstone and
// sulfur spikes hanging from or rising off a base block, single or in
// floor-to-ceiling clusters with an optional water pool.

// speleothemKind is the base/pointed block pair and the replaceable set.
type speleothemKind struct {
	base, pointed string
	baseState     world.Block
	replace       map[string]bool
}

func (g *Generator) speleothemKind(cfg obj) speleothemKind {
	var b, p obj
	jsonUnmarshal(cfg["base_block"], &b)
	jsonUnmarshal(cfg["pointed_block"], &p)
	k := speleothemKind{base: trimNS(b.str("Name")), pointed: trimNS(p.str("Name"))}
	k.baseState = blockNamed(k.base)
	k.replace = blockNameSet(cfg["replaceable_blocks"])
	return k
}

// isBase is SpeleothemUtils.isBase.
func (k speleothemKind) isBase(b world.Block) bool { return isBlock(b, k.base) || inTag(b, k.replace) }

// placeBase is SpeleothemUtils.placeBaseBlockIfPossible.
func (k speleothemKind) placeBase(rg *Region, p BlockPos) bool {
	if !inTag(rg.Block(p[0], p[1], p[2]), k.replace) {
		return false
	}
	rg.SetBlock(p[0], p[1], p[2], k.baseState)
	return true
}

// grow is SpeleothemUtils.growSpeleothem: a base-to-tip column of
// height cells from p toward dir, if the block behind p is a base.
func (k speleothemKind) grow(rg *Region, p BlockPos, dir int, height int32, merge bool) {
	o := dirOffsets[dir]
	if !k.isBase(rg.Block(p[0]-o[0], p[1]-o[1], p[2]-o[2])) {
		return
	}
	put := func(thick string) {
		st := blockNamed(k.pointed)
		st = carpetWith(st, "vertical_direction", multifaceFaceProps[dir])
		st = carpetWith(st, "thickness", thick)
		st = carpetWith(st, "waterlogged", boolStr(isWaterAt(rg.Block(p[0], p[1], p[2]))))
		rg.SetBlock(p[0], p[1], p[2], st)
		p = offset(p, o[0], o[1], o[2])
	}
	if height >= 3 {
		put("base")
		for i := int32(0); i < height-3; i++ {
			put("middle")
		}
	}
	if height >= 2 {
		put("frustum")
	}
	if height >= 1 {
		if merge {
			put("tip_merge")
		} else {
			put("tip")
		}
	}
}

func emptyOrWater(b world.Block) bool         { return isAir(b) || isBlock(b, "water") }
func emptyOrWaterOrLava(b world.Block) bool   { return emptyOrWater(b) || isBlock(b, "lava") }
func neitherEmptyNorWater(b world.Block) bool { return !emptyOrWater(b) }

func init() {
	featureBuilders["speleothem"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		k := g.speleothemKind(cfg)
		chance := func(key string, def float32) float32 {
			if raw, ok := cfg[key]; ok {
				return parseF32(raw)
			}
			return def
		}
		taller := chance("chance_of_taller_generation", 0.2)
		spread := chance("chance_of_directional_spread", 0.7)
		r2 := chance("chance_of_spread_radius2", 0.5)
		r3 := chance("chance_of_spread_radius3", 0.5)
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			up := k.isBase(rg.Block(p[0], p[1]+1, p[2]))
			down := k.isBase(rg.Block(p[0], p[1]-1, p[2]))
			var tip int
			switch {
			case up && down:
				tip = dirUp
				if r.NextBool() {
					tip = dirDown
				}
			case up:
				tip = dirDown
			case down:
				tip = dirUp
			default:
				return false
			}
			o := dirOffsets[tip]
			base := offset(p, -o[0], -o[1], -o[2])
			// createPatchOfBaseBlocks: the base, then per horizontal
			// direction a chance of a 1-3 step random walk of bases.
			k.placeBase(rg, base)
			for _, d := range horizontalDirs {
				if r.NextFloat() > spread {
					continue
				}
				do := dirOffsets[d]
				q := offset(base, do[0], do[1], do[2])
				k.placeBase(rg, q)
				if r.NextFloat() > r2 {
					continue
				}
				ro := dirOffsets[r.NextIntN(6)]
				q = offset(q, ro[0], ro[1], ro[2])
				k.placeBase(rg, q)
				if r.NextFloat() > r3 {
					continue
				}
				ro = dirOffsets[r.NextIntN(6)]
				q = offset(q, ro[0], ro[1], ro[2])
				k.placeBase(rg, q)
			}
			h := int32(1)
			if r.NextFloat() < taller && emptyOrWater(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2])) {
				h = 2
			}
			k.grow(rg, p, tip, h, false)
			return true
		}
	}
	featureBuilders["speleothem_cluster"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		k := g.speleothemKind(cfg)
		search := cfg.i32("floor_to_ceiling_search_range")
		height := parseIntProvider(cfg["height"])
		radius := parseIntProvider(cfg["radius"])
		maxDiff := cfg.i32("max_stalagmite_stalactite_height_diff")
		deviation := cfg.i32("height_deviation")
		layer := parseIntProvider(cfg["speleothem_block_layer_thickness"])
		density := parseFloatProvider(cfg["density"])
		wetness := parseFloatProvider(cfg["wetness"])
		chanceAtMax := parseF32(cfg["chance_of_speleothem_at_max_distance_from_center"])
		edgeDist := cfg.i32("max_distance_from_edge_affecting_chance_of_speleothem")
		biasDist := cfg.i32("max_distance_from_center_affecting_height_bias")

		// getSpeleothemHeight: zero unless the density roll passes, else a
		// clamped normal biased toward half height near the center.
		spHeight := func(r Random, dx, dz int32, dens float32, h int32) int32 {
			if r.NextFloat() > dens {
				return 0
			}
			d := absI(dx) + absI(dz)
			mean := float32(clampedMap(float64(d), 0, float64(biasDist), float64(float64(h)/2.0), 0))
			v := mean + float32(float32(r.NextGaussian())*float32(deviation))
			return int32(clampF(v, 0, float32(h)))
		}
		replaceRun := func(rg *Region, p BlockPos, n int32, dir int) {
			o := dirOffsets[dir]
			for i := int32(0); i < n; i++ {
				if !k.placeBase(rg, p) {
					return
				}
				p = offset(p, o[0], o[1], o[2])
			}
		}
		adjacentOK := func(b world.Block) bool { return blockInTag(b, "base_stone_overworld") || isWaterAt(b) }
		canPool := func(rg *Region, p BlockPos) bool {
			b := rg.Block(p[0], p[1], p[2])
			if isBlock(b, "water") || isBlock(b, k.base) || isBlock(b, k.pointed) {
				return false
			}
			if isWaterAt(rg.Block(p[0], p[1]+1, p[2])) {
				return false
			}
			for _, d := range horizontalDirs {
				o := dirOffsets[d]
				if !adjacentOK(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2])) {
					return false
				}
			}
			return adjacentOK(rg.Block(p[0], p[1]-1, p[2]))
		}
		// scan is Column.scan with SpeleothemUtils::isEmptyOrWater inside
		// and ::isNeitherEmptyNorWater edges.
		scan := func(rg *Region, p BlockPos, dir int) (int32, bool) {
			y := p[1]
			step := dirOffsets[dir][1]
			for i := int32(1); i < search && emptyOrWater(rg.Block(p[0], y, p[2])); i++ {
				y += step
			}
			if neitherEmptyNorWater(rg.Block(p[0], y, p[2])) {
				return y, true
			}
			return 0, false
		}
		column := func(rg *Region, r Random, p BlockPos, dx, dz int32, wet float32, chance float64, h int32, dens float32) {
			if !emptyOrWater(rg.Block(p[0], p[1], p[2])) {
				return
			}
			ceil, hasCeil := scan(rg, p, dirUp)
			floor, hasFloor := scan(rg, p, dirDown)
			if !hasCeil && !hasFloor {
				return
			}
			if r.NextFloat() < wet && hasFloor && canPool(rg, BlockPos{p[0], floor, p[2]}) {
				rg.SetBlock(p[0], floor, p[2], blockNamed("water"))
				floor--
			}
			var hc, hf int32
			if r.NextDouble() < chance && hasCeil && !isBlock(rg.Block(p[0], ceil, p[2]), "lava") {
				replaceRun(rg, BlockPos{p[0], ceil, p[2]}, layer(r), dirUp)
				lim := h
				if hasFloor {
					lim = min(h, ceil-floor)
				}
				hc = spHeight(r, dx, dz, dens, lim)
			}
			if r.NextDouble() < chance && hasFloor && !isBlock(rg.Block(p[0], floor, p[2]), "lava") {
				replaceRun(rg, BlockPos{p[0], floor, p[2]}, layer(r), dirDown)
				if hasCeil {
					hf = max(0, hc+randBetweenInclusive(r, -maxDiff, maxDiff))
				} else {
					hf = spHeight(r, dx, dz, dens, h)
				}
			}
			if hasCeil && hasFloor && ceil-hc <= floor+hf {
				lo := max(ceil-hc, floor+1)
				hi := min(floor+hf, ceil-1)
				m := randBetweenInclusive(r, lo, hi+1)
				hc = ceil - m
				hf = m - 1 - floor
			}
			merge := r.NextBool() && hc > 0 && hf > 0 && hasCeil && hasFloor && hc+hf == ceil-floor-1
			if hasCeil {
				k.grow(rg, BlockPos{p[0], ceil - 1, p[2]}, dirDown, hc, merge)
			}
			if hasFloor {
				k.grow(rg, BlockPos{p[0], floor + 1, p[2]}, dirUp, hf, merge)
			}
		}
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			if !emptyOrWater(rg.Block(p[0], p[1], p[2])) {
				return false
			}
			h := height(r)
			wet := wetness(r)
			dens := density(r)
			rx := radius(r)
			rz := radius(r)
			for dx := -rx; dx <= rx; dx++ {
				for dz := -rz; dz <= rz; dz++ {
					// getChanceOfStalagmiteOrStalactite (float clampedMap).
					edge := min(rx-absI(dx), rz-absI(dz))
					chance := float64(clampedMapF(float32(edge), 0, float32(edgeDist), chanceAtMax, 1))
					column(rg, r, offset(p, dx, 0, dz), dx, dz, wet, chance, h, dens)
				}
			}
			return true
		}
	}
}

// clampedMapF is Mth.clampedMap over floats.
func clampedMapF(v, a, b, c, d float32) float32 {
	t := (v - a) / (b - a)
	if t < 0 {
		return c
	}
	if t > 1 {
		return d
	}
	return c + float32(t*(d-c))
}
