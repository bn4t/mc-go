package worldgen

import "mc-go/internal/world"

// vegetation_patch / waterlogged_vegetation_patch
// (VegetationPatchFeature, WaterloggedVegetationPatchFeature): a
// rounded-rectangle ground patch found by scanning each column toward
// the surface, replaced to a sampled depth, then vegetation placed on a
// chance per ground cell. The ground cells are a HashSet<BlockPos>, so
// vegetation is visited in Java hash order.

func init() {
	featureBuilders["vegetation_patch"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return g.compileVegPatch(cfg, false)
	}
	featureBuilders["waterlogged_vegetation_patch"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return g.compileVegPatch(cfg, true)
	}
}

func (g *Generator) compileVegPatch(cfg obj, waterlogged bool) func(*featCtx, Random, BlockPos) bool {
	replaceable := blockTagSet(cfg.str("replaceable"))
	ground := g.compileStateProvider(cfg["ground_state"])
	surfDir := dirDown // CaveSurface.FLOOR
	if cfg.str("surface") == "ceiling" {
		surfDir = dirUp
	}
	opp := oppositeDir(surfDir)
	depth := parseIntProvider(cfg["depth"])
	vertical := cfg.i32("vertical_range")
	vegChance := parseF32(cfg["vegetation_chance"])
	radius := parseIntProvider(cfg["xz_radius"])
	edgeChance := parseF32(cfg["extra_edge_column_chance"])
	bottomChance := parseF32(cfg["extra_bottom_block_chance"])
	veg := g.placedRef(cfg["vegetation_feature"])
	water := blockNamed("water")
	sd := dirOffsets[surfDir]
	od := dirOffsets[opp]

	// placeGround: up to n cells replaced from p toward the surface; a
	// cell already of the ground block uses up a step without moving.
	placeGround := func(rg *Region, r Random, p BlockPos, n int32) bool {
		for i := int32(0); i < n; i++ {
			st := ground.getState(rg, r, p)
			cur := rg.Block(p[0], p[1], p[2])
			sn, _ := world.StateOwner(st)
			if isBlock(cur, sn) {
				continue
			}
			if !inTag(cur, replaceable) {
				return i != 0
			}
			rg.SetBlock(p[0], p[1], p[2], st)
			p = offset(p, sd[0], sd[1], sd[2])
		}
		return true
	}

	groundPatch := func(rg *Region, r Random, origin BlockPos, rx, rz int32) []BlockPos {
		var cells []BlockPos
		for dx := -rx; dx <= rx; dx++ {
			edgeX := dx == -rx || dx == rx
			for dz := -rz; dz <= rz; dz++ {
				edgeZ := dz == -rz || dz == rz
				if edgeX && edgeZ {
					continue
				}
				if (edgeX || edgeZ) && (edgeChance == 0 || r.NextFloat() > edgeChance) {
					continue
				}
				q := offset(origin, dx, 0, dz)
				for i := int32(0); isAir(rg.Block(q[0], q[1], q[2])) && i < vertical; i++ {
					q = offset(q, sd[0], sd[1], sd[2])
				}
				for i := int32(0); !isAir(rg.Block(q[0], q[1], q[2])) && i < vertical; i++ {
					q = offset(q, od[0], od[1], od[2])
				}
				gp := offset(q, sd[0], sd[1], sd[2])
				below := rg.Block(gp[0], gp[1], gp[2])
				if !isAir(rg.Block(q[0], q[1], q[2])) || !sturdyFull(below, opp) {
					continue
				}
				n := depth(r)
				if bottomChance > 0 && r.NextFloat() < bottomChance {
					n++
				}
				if placeGround(rg, r, gp, n) {
					cells = append(cells, gp)
				}
			}
		}
		cells = javaHashSetOrder(cells)
		if !waterlogged {
			return cells
		}
		// WaterloggedVegetationPatchFeature.placeGroundPatch: the cells
		// enclosed on every side but up turn to water.
		var kept []BlockPos
		for _, p := range cells {
			if !patchExposed(rg, p) {
				kept = append(kept, p)
			}
		}
		kept = javaHashSetOrder(kept)
		for _, p := range kept {
			rg.SetBlock(p[0], p[1], p[2], water)
		}
		return kept
	}

	return func(c *featCtx, r Random, p BlockPos) bool {
		rx := radius(r) + 1
		rz := radius(r) + 1
		cells := groundPatch(c.rg, r, p, rx, rz)
		for _, q := range cells {
			if vegChance > 0 && r.NextFloat() < vegChance {
				if waterlogged {
					// The base placement from the cell below: on a floor,
					// the water cell itself; then waterlogged.
					if veg.placeNested(c, r, offset(q, od[0], od[1]-1, od[2])) {
						st := c.rg.Block(q[0], q[1], q[2])
						if v, ok := world.StateProperty(st, "waterlogged"); ok && v == "false" {
							c.rg.SetBlock(q[0], q[1], q[2], carpetWith(st, "waterlogged", "true"))
						}
					}
					continue
				}
				veg.placeNested(c, r, offset(q, od[0], od[1], od[2]))
			}
		}
		return len(cells) > 0
	}
}

// patchExposed is WaterloggedVegetationPatchFeature.isExposed: some
// neighbour among N, E, S, W and below lacks a sturdy face toward p.
func patchExposed(rg *Region, p BlockPos) bool {
	for _, d := range [5]int{dirNorth, dirEast, dirSouth, dirWest, dirDown} {
		o := dirOffsets[d]
		if !sturdyFull(rg.Block(p[0]+o[0], p[1]+o[1], p[2]+o[2]), oppositeDir(d)) {
			return true
		}
	}
	return false
}
