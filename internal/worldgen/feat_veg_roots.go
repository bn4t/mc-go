package worldgen

// root_system (RootSystemFeature): climb from a cave origin to the
// first spot with room for the tree feature, place it, fill the column
// below with rooted dirt and hang roots under the origin.

func init() {
	featureBuilders["root_system"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		tree := g.placedRef(cfg["feature"])
		maxHeight := cfg.i32("root_column_max_height")
		allowed := g.compilePredicate(cfg["allowed_tree_position"])
		space := cfg.i32("required_vertical_space_for_tree")
		water := cfg.i32("allowed_vertical_water_for_tree")
		levelDist := cfg.i32("level_test_distance")
		levelDev := cfg.i32("max_level_deviation")
		rootRadius := cfg.i32("root_radius")
		rootAttempts := cfg.i32("root_placement_attempts")
		rootReplace := blockTagSet(cfg.str("root_replaceable"))
		rootState := g.compileStateProvider(cfg["root_state_provider"])
		hangRadius := cfg.i32("hanging_root_radius")
		hangSpan := cfg.i32("hanging_roots_vertical_span")
		hangAttempts := cfg.i32("hanging_root_placement_attempts")
		hangState := g.compileStateProvider(cfg["hanging_root_state_provider"])

		// spaceForTree: room above (air, or water within the allowed
		// depth) and, with a level test, ground below/air above at the
		// four compass points (Direction.from2DDataValue: S, W, N, E).
		spaceForTree := func(rg *Region, p BlockPos) bool {
			for i := int32(1); i <= space; i++ {
				b := rg.Block(p[0], p[1]+i, p[2])
				if !isAir(b) && !(i+1 <= water && isWaterAt(b)) {
					return false
				}
			}
			if levelDist > 0 {
				for _, d := range [4]int{dirSouth, dirWest, dirNorth, dirEast} {
					o := dirOffsets[d]
					q := offset(p, o[0]*levelDist, 0, o[2]*levelDist)
					if isAir(rg.Block(q[0], q[1]-levelDev, q[2])) || !isAir(rg.Block(q[0], q[1]+levelDev, q[2])) {
						return false
					}
				}
			}
			return true
		}
		return func(c *featCtx, r Random, p BlockPos) bool {
			rg := c.rg
			if !isAir(rg.Block(p[0], p[1], p[2])) {
				return false
			}
			q := p
			grown := false
			for i := int32(0); i < maxHeight; i++ {
				q[1]++
				if rg.Height(HMWorldSurface, q[0], q[2]) < q[1] {
					break
				}
				if !allowed(rg, q) || !spaceForTree(rg, q) {
					continue
				}
				below := rg.Block(q[0], q[1]-1, q[2])
				if inFluidTag(below, "lava") || !isSolidState(below) {
					break
				}
				if tree.placeNested(c, r, q) {
					// placeDirt: rooted dirt attempts on every layer from
					// the origin up to below the tree.
					for y := p[1]; y < p[1]+i; y++ {
						for k := int32(0); k < rootAttempts; k++ {
							dx := r.NextIntN(rootRadius) - r.NextIntN(rootRadius)
							dz := r.NextIntN(rootRadius) - r.NextIntN(rootRadius)
							t := BlockPos{p[0] + dx, y, p[2] + dz}
							if inTag(rg.Block(t[0], t[1], t[2]), rootReplace) {
								rg.SetBlock(t[0], t[1], t[2], rootState.getState(rg, r, t))
							}
						}
					}
					grown = true
					break
				}
				// A failed tree keeps climbing.
			}
			if !grown {
				return true
			}
			// placeRoots: hanging roots around the origin.
			for k := int32(0); k < hangAttempts; k++ {
				dx := r.NextIntN(hangRadius) - r.NextIntN(hangRadius)
				dy := r.NextIntN(hangSpan) - r.NextIntN(hangSpan)
				dz := r.NextIntN(hangRadius) - r.NextIntN(hangRadius)
				t := offset(p, dx, dy, dz)
				if !isAir(rg.Block(t[0], t[1], t[2])) {
					continue
				}
				st := hangState.getState(rg, r, t)
				if canSurvive(rg, st, t) && sturdyFull(rg.Block(t[0], t[1]+1, t[2]), dirDown) {
					rg.SetBlock(t[0], t[1], t[2], st)
				}
			}
			return true
		}
	}
}
