package worldgen

// Root systems, measured from RootSystemFeature (26.2): from an air
// origin, climb to the first spot that admits the tree feature with room
// above it (and, when tested, level ground around it), place the tree
// there, then root the column below it with rooted blocks and hang
// roots under the origin.

func init() {
	featureKinds["minecraft:root_system"] = decodeRootSystem
}

type rootSystem struct {
	tree                                *placedFeature
	treeSpace, treeWater                int32
	levelDistance, levelDeviation       int32
	rootRadius, rootAttempts, columnMax int32
	rootReplaceable                     map[string]bool
	rootState                           stateProvider
	hangRadius, hangSpan, hangAttempts  int32
	hangState                           stateProvider
	allowed                             blockPredicate
}

func decodeRootSystem(cfg any) func(*featureCtx) bool {
	g := func(k string) int32 { return fint(ffield(cfg, k), 0) }
	rs := &rootSystem{
		tree:      decodePlaced(ffield(cfg, "feature")),
		treeSpace: g("required_vertical_space_for_tree"), treeWater: g("allowed_vertical_water_for_tree"),
		levelDistance: g("level_test_distance"), levelDeviation: g("max_level_deviation"),
		rootRadius: g("root_radius"), rootAttempts: g("root_placement_attempts"), columnMax: g("root_column_max_height"),
		rootReplaceable: blockSet(ffield(cfg, "root_replaceable")),
		rootState:       decodeStateProvider(ffield(cfg, "root_state_provider")),
		hangRadius:      g("hanging_root_radius"), hangSpan: g("hanging_roots_vertical_span"),
		hangAttempts: g("hanging_root_placement_attempts"),
		hangState:    decodeStateProvider(ffield(cfg, "hanging_root_state_provider")),
		allowed:      decodeBlockPredicate(ffield(cfg, "allowed_tree_position")),
	}
	return rs.place
}

func (rs *rootSystem) place(fc *featureCtx) bool {
	r, o := fc.r, fc.origin
	if !r.get(o).isAir() {
		return false
	}
	if top, ok := rs.placeTree(fc); ok {
		rs.hangRoots(fc, top)
	}
	return true
}

// spaceForTree is whether the column above p is open, water allowed
// only near its foot, and the ground the level test reaches each way is
// neither falling away nor rising.
func (rs *rootSystem) spaceForTree(r *decoRegion, p Point) bool {
	for i := int32(1); i <= rs.treeSpace; i++ {
		s := r.get(p.add(Point{0, i, 0}))
		if !s.isAir() && !(i+1 <= rs.treeWater && s.fluidInTag("water")) {
			return false
		}
	}
	if rs.levelDistance > 0 {
		// Direction.from2DDataValue order: south, west, north, east.
		for _, d := range []int{dirSouth, dirWest, dirNorth, dirEast} {
			q := p.add(scaleDir(d, rs.levelDistance))
			if r.get(q.add(Point{0, -rs.levelDeviation, 0})).isAir() || !r.get(q.add(Point{0, rs.levelDeviation, 0})).isAir() {
				return false
			}
		}
	}
	return true
}

// placeTree climbs from the origin to place the tree; it reports the
// position the climb ended on.
func (rs *rootSystem) placeTree(fc *featureCtx) (Point, bool) {
	r, random, o := fc.r, fc.rand, fc.origin
	p := o
	for i := int32(0); i < rs.columnMax; i++ {
		p = p.offset(dirUp)
		if r.heightAt(hmWorldSurface, p.X, p.Z) < p.Y {
			return p, false
		}
		if !rs.allowed(r, p) || !rs.spaceForTree(r, p) {
			continue
		}
		below := p.offset(dirDown)
		if b := r.get(below); b.fluidInTag("lava") || !b.isSolid() {
			return p, false
		}
		if rs.tree.placeIn(&featureCtx{r: r, rand: random}, p) {
			for y := o.Y; y < o.Y+i; y++ {
				rs.rootDirt(fc, o.X, o.Z, Point{o.X, y, o.Z})
			}
			return p, true
		}
	}
	return p, false
}

func (rs *rootSystem) rootDirt(fc *featureCtx, x, z int32, at Point) {
	r, random := fc.r, fc.rand
	p := at
	for range rs.rootAttempts {
		p = p.add(Point{random.NextIntn(rs.rootRadius) - random.NextIntn(rs.rootRadius), 0,
			random.NextIntn(rs.rootRadius) - random.NextIntn(rs.rootRadius)})
		if rs.rootReplaceable[r.get(p).block()] {
			r.set(p, rs.rootState.get(r, random, p))
		}
		p.X, p.Z = x, z
	}
}

// hangRoots tries hanging roots around the origin under sturdy ceilings.
func (rs *rootSystem) hangRoots(fc *featureCtx, _ Point) {
	r, random, o := fc.r, fc.rand, fc.origin
	for range rs.hangAttempts {
		dx := random.NextIntn(rs.hangRadius) - random.NextIntn(rs.hangRadius)
		dy := random.NextIntn(rs.hangSpan) - random.NextIntn(rs.hangSpan)
		dz := random.NextIntn(rs.hangRadius) - random.NextIntn(rs.hangRadius)
		p := o.add(Point{dx, dy, dz})
		if !r.get(p).isAir() {
			continue
		}
		s := rs.hangState.get(r, random, p)
		if canSurvive(r, s, p) && r.get(p.offset(dirUp)).sturdy(dirDown) {
			r.set(p, s)
		}
	}
}
