package worldgen

import (
	"mc-go/internal/world"
)

// Small terrain features: forest rocks, blue ice clusters and
// underwater magma.

func init() {
	featureBuilders["block_blob"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		var st obj
		jsonUnmarshal(cfg["state"], &st)
		state := stateFromJSON(st)
		on := g.compilePredicate(cfg["can_place_on"])
		return func(c *featCtx, r Random, p BlockPos) bool { return placeBlockBlob(c.rg, r, state, on, p) }
	}
	featureBuilders["blue_ice"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return placeBlueIce(c.rg, r, p) }
	}
	featureBuilders["desert_well"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return placeDesertWell(c.rg, r, p) }
	}
	featureBuilders["underwater_magma"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		m := &magmaConfig{
			search: cfg.i32("floor_search_range"),
			radius: cfg.i32("placement_radius_around_floor"),
			chance: parseF32(cfg["placement_probability_per_valid_position"]),
		}
		return func(c *featCtx, r Random, p BlockPos) bool { return placeUnderwaterMagma(c.rg, r, m, p) }
	}
}

// placeBlockBlob is BlockBlobFeature.place: sink to the first cell
// whose floor passes can_place_on, then three overlapping balls of
// radius (a+b+c)*0.333+0.5, each shifted down/sideways by a random step.
func placeBlockBlob(rg *Region, r Random, state world.Block, on blockPredicate, p BlockPos) bool {
	for p[1] > rg.MinY()+3 && !on(rg, offset(p, 0, -1, 0)) {
		p = offset(p, 0, -1, 0)
	}
	if p[1] <= rg.MinY()+3 {
		return false
	}
	for i := 0; i < 3; i++ {
		a, b, c := r.NextIntN(2), r.NextIntN(2), r.NextIntN(2)
		f := float32(float32(a+b+c)*0.333) + 0.5
		lim := float64(float32(f * f))
		for z := p[2] - c; z <= p[2]+c; z++ {
			for y := p[1] - b; y <= p[1]+b; y++ {
				for x := p[0] - a; x <= p[0]+a; x++ {
					dx, dy, dz := float64(x-p[0]), float64(y-p[1]), float64(z-p[2])
					if float64(dx*dx)+float64(dy*dy)+float64(dz*dz) <= lim {
						rg.set(BlockPos{x, y, z}, state)
					}
				}
			}
		}
		dx := -1 + r.NextIntN(2)
		dy := -r.NextIntN(2)
		dz := -1 + r.NextIntN(2)
		p = offset(p, dx, dy, dz)
	}
	return true
}

// placeBlueIce is BlueIceFeature.place: below sea level, in or over
// water and touching packed ice (any side but below), a blue ice seed
// grows by 200 random attempts that attach to existing blue ice.
func placeBlueIce(rg *Region, r Random, p BlockPos) bool {
	if p[1] > rg.seaLevel()-1 {
		return false
	}
	if !isBlock(rg.at(p), "water") && !isBlock(rg.at(offset(p, 0, -1, 0)), "water") {
		return false
	}
	touches := false
	for d := 0; d < 6; d++ {
		if d == dirDown {
			continue
		}
		o := dirOffsets[d]
		if isBlock(rg.at(offset(p, o[0], o[1], o[2])), "packed_ice") {
			touches = true
			break
		}
	}
	if !touches {
		return false
	}
	blue := blockNamed("blue_ice")
	rg.set(p, blue)
	for i := 0; i < 200; i++ {
		dy := r.NextIntN(5) - r.NextIntN(6)
		spread := int32(3)
		if dy < 2 {
			spread += dy / 2
		}
		if spread < 1 {
			continue
		}
		dx := r.NextIntN(spread) - r.NextIntN(spread)
		dz := r.NextIntN(spread) - r.NextIntN(spread)
		q := offset(p, dx, dy, dz)
		st := rg.at(q)
		if !isAir(st) && !isBlock(st, "water") && !isBlock(st, "packed_ice") && !isBlock(st, "ice") {
			continue
		}
		for d := 0; d < 6; d++ {
			o := dirOffsets[d]
			if isBlock(rg.at(offset(q, o[0], o[1], o[2])), "blue_ice") {
				rg.set(q, blue)
				break
			}
		}
	}
	return true
}

// placeDesertWell is DesertWellFeature.place: drop to the first
// non-empty cell, require sand with no 2-deep hole under the 5x5 rim,
// then build the sandstone basin, water cross, slab rim, four pillars
// and roof, and bury two suspicious sand blocks under random cells of
// the water cross (one and two below).
func placeDesertWell(rg *Region, r Random, o BlockPos) bool {
	o = offset(o, 0, 1, 0)
	for isAir(rg.at(o)) && o[1] > rg.MinY()+2 {
		o = offset(o, 0, -1, 0)
	}
	if !isBlock(rg.at(o), "sand") {
		return false
	}
	for dx := int32(-2); dx <= 2; dx++ {
		for dz := int32(-2); dz <= 2; dz++ {
			if isAir(rg.at(offset(o, dx, -1, dz))) && isAir(rg.at(offset(o, dx, -2, dz))) {
				return false
			}
		}
	}
	sand, sandstone, slab, water := blockNamed("sand"), blockNamed("sandstone"), blockNamed("sandstone_slab"), blockNamed("water")
	for dy := int32(-2); dy <= 0; dy++ {
		for dx := int32(-2); dx <= 2; dx++ {
			for dz := int32(-2); dz <= 2; dz++ {
				rg.set(offset(o, dx, dy, dz), sandstone)
			}
		}
	}
	rg.set(o, water)
	for _, d := range horizontalDirs {
		f := dirOffsets[d]
		rg.set(offset(o, f[0], 0, f[2]), water)
	}
	below := offset(o, 0, -1, 0)
	rg.set(below, sand)
	for _, d := range horizontalDirs {
		f := dirOffsets[d]
		rg.set(offset(below, f[0], 0, f[2]), sand)
	}
	for dx := int32(-2); dx <= 2; dx++ {
		for dz := int32(-2); dz <= 2; dz++ {
			if dx == -2 || dx == 2 || dz == -2 || dz == 2 {
				rg.set(offset(o, dx, 1, dz), sandstone)
			}
		}
	}
	rg.set(offset(o, 2, 1, 0), slab)
	rg.set(offset(o, -2, 1, 0), slab)
	rg.set(offset(o, 0, 1, 2), slab)
	rg.set(offset(o, 0, 1, -2), slab)
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				rg.set(offset(o, dx, 4, dz), sandstone)
			} else {
				rg.set(offset(o, dx, 4, dz), slab)
			}
		}
	}
	for dy := int32(1); dy <= 3; dy++ {
		rg.set(offset(o, -1, dy, -1), sandstone)
		rg.set(offset(o, -1, dy, 1), sandstone)
		rg.set(offset(o, 1, dy, -1), sandstone)
		rg.set(offset(o, 1, dy, 1), sandstone)
	}
	cells := [5]BlockPos{o, offset(o, 1, 0, 0), offset(o, 0, 0, 1), offset(o, -1, 0, 0), offset(o, 0, 0, -1)}
	// DesertWellFeature.placeSusSand: the sand's brushable block entity
	// takes archaeology/desert_well seeded with the position itself.
	placeSus := func(q BlockPos) {
		rg.set(q, blockNamed("suspicious_sand"))
		if be := rg.blockEntity(q); be != nil {
			setLootTable(be, "minecraft:archaeology/desert_well", blockPosAsLong(q))
		}
	}
	placeSus(offset(cells[r.NextIntN(5)], 0, -1, 0))
	placeSus(offset(cells[r.NextIntN(5)], 0, -2, 0))
	return true
}

type magmaConfig struct {
	search, radius int32
	chance         float32
}

// occludesFace approximates the face-occlusion test of
// UnderwaterMagmaFeature.isVisibleFromOutside (the face's occlusion
// shape is the full block): solid-render blocks occlude every face.
func occludesFace(b world.Block, dir int) bool { return solidRender(b) }

// placeUnderwaterMagma is UnderwaterMagmaFeature.place: find the floor
// under a water column (Column.scan over floor_search_range), then each
// cell of the cube around the floor, in betweenClosed order, rolls the
// placement chance and turns to magma when it is a buried non-water
// block (no face open to water/air above or sideways).
func placeUnderwaterMagma(rg *Region, r Random, m *magmaConfig, p BlockPos) bool {
	if !isBlock(rg.at(p), "water") {
		return false
	}
	y := p[1]
	for i := int32(1); i < m.search && isBlock(rg.at(BlockPos{p[0], y, p[2]}), "water"); i++ {
		y--
	}
	if isBlock(rg.at(BlockPos{p[0], y, p[2]}), "water") {
		return false
	}
	f := BlockPos{p[0], y, p[2]}
	n := 0
	visible := func(q BlockPos, dir int) bool { return !occludesFace(rg.at(q), dir) }
	magma := blockNamed("magma_block")
	for z := f[2] - m.radius; z <= f[2]+m.radius; z++ {
		for yy := f[1] - m.radius; yy <= f[1]+m.radius; yy++ {
			for x := f[0] - m.radius; x <= f[0]+m.radius; x++ {
				if !(r.NextFloat() < m.chance) {
					continue
				}
				q := BlockPos{x, yy, z}
				st := rg.at(q)
				if isBlock(st, "water") || isAir(st) || visible(offset(q, 0, -1, 0), dirUp) {
					continue
				}
				ok := true
				for _, d := range horizontalDirs {
					o := dirOffsets[d]
					if visible(offset(q, o[0], o[1], o[2]), oppositeDir(d)) {
						ok = false
						break
					}
				}
				if !ok {
					continue
				}
				rg.set(q, magma)
				n++
			}
		}
	}
	return n > 0
}
