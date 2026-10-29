package worldgen

import "mc-go/internal/world"

// vegetation_patch is placed by the pale_moss tree decorator
// (pale_moss_patch). It belongs to the vegetation group; this
// registration only fills the gap when no other implementation is
// registered.

func init() {
	if _, ok := featureBuilders["vegetation_patch"]; ok {
		return
	}
	featureBuilders["vegetation_patch"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		replaceable := blockTagSet(cfg.str("replaceable"))
		ground := g.compileStateProvider(cfg["ground_state"])
		depth := parseIntProvider(cfg["depth"])
		extraBottom := parseF32(cfg["extra_bottom_block_chance"])
		extraEdge := parseF32(cfg["extra_edge_column_chance"])
		vegChance := parseF32(cfg["vegetation_chance"])
		vrange := cfg.i32("vertical_range")
		xz := parseIntProvider(cfg["xz_radius"])
		veg := g.placedRef(cfg["vegetation_feature"])
		dir, opp := dirDown, dirUp
		if cfg.str("surface") == "ceiling" {
			dir, opp = dirUp, dirDown
		}
		// placeGround is VegetationPatchFeature.placeGround.
		placeGround := func(rg *Region, r Random, m BlockPos, n int32) bool {
			for i := int32(0); i < n; i++ {
				s := ground.getState(rg, r, m)
				cur := blockAt(rg, m)
				sn, _ := world.StateOwner(s)
				if isBlock(cur, sn) {
					continue
				}
				if !inTag(cur, replaceable) {
					return i != 0
				}
				rg.set(m, s)
				m = relDir(m, dir, 1)
			}
			return true
		}
		return func(c *featCtx, r Random, o BlockPos) bool {
			rg := c.rg
			xr := xz(r) + 1
			zr := xz(r) + 1
			set := newJSet()
			for dx := -xr; dx <= xr; dx++ {
				ex := dx == -xr || dx == xr
				for dz := -zr; dz <= zr; dz++ {
					ez := dz == -zr || dz == zr
					if ex && ez {
						continue
					}
					if (ex || ez) && (extraEdge == 0 || r.NextFloat() > extraEdge) {
						continue
					}
					m := offset(o, dx, 0, dz)
					for i := int32(0); isAirAt(rg, m) && i < vrange; i++ {
						m = relDir(m, dir, 1)
					}
					for i := int32(0); !isAirAt(rg, m) && i < vrange; i++ {
						m = relDir(m, opp, 1)
					}
					below := relDir(m, dir, 1)
					if !isAirAt(rg, m) || !sturdyFull(blockAt(rg, below), opp) {
						continue
					}
					n := depth(r)
					if extraBottom > 0 && r.NextFloat() < extraBottom {
						n++
					}
					if placeGround(rg, r, below, n) {
						set.add(below)
					}
				}
			}
			for _, p := range set.list() {
				if vegChance > 0 && r.NextFloat() < vegChance {
					veg.placeNested(c, r, relDir(p, opp, 1))
				}
			}
			return set.n > 0
		}
	}
}
