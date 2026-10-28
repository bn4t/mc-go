package worldgen

// Root placers: the trunk starts some blocks above the origin and roots
// fill the space in between, spreading outward and down.

func init() {
	treeRootPlacers["minecraft:mangrove_root_placer"] = decodeMangroveRoots
}

// rootBase is the shared root placer: the trunk's offset, the root
// state, and an optional block placed on top of roots with air above.
type rootBase struct {
	offset      intProvider
	provider    stateProvider
	above       stateProvider // nil for none
	aboveChance float32
}

func decodeRootBase(cfg any) rootBase {
	b := rootBase{
		offset:   decodeIntProvider(ffield(cfg, "trunk_offset_y")),
		provider: decodeStateProvider(ffield(cfg, "root_provider")),
	}
	if fhas(cfg, "above_root_placement") {
		a := ffield(cfg, "above_root_placement")
		b.above = decodeStateProvider(ffield(a, "above_root_provider"))
		b.aboveChance = ffloat(ffield(a, "above_root_placement_chance"), 0)
	}
	return b
}

func (b *rootBase) trunkOrigin(origin Point, rand Random) Point {
	return Point{origin.X, origin.Y + b.offset.sample(rand), origin.Z}
}

// waterlogged sets a state's waterlogged property from the fluid at p:
// any water, source or flowing.
func rootWaterlogged(r *decoRegion, p Point, s StateID) StateID {
	if s.hasProp("waterlogged") {
		return s.with("waterlogged", boolStr(r.isWaterAt(p)))
	}
	return s
}

// place sets a root at p when the placer may, then possibly the block
// above it.
func (b *rootBase) place(t *tree, p Point, canPlace func(Point) bool) {
	if !canPlace(p) {
		return
	}
	t.setRoot(p, rootWaterlogged(t.r, p, b.provider.get(t.r, t.rand, p)))
	if b.above == nil {
		return
	}
	up := p.offset(dirUp)
	if t.rand.NextFloat() < b.aboveChance && t.r.get(up).isAir() {
		t.setRoot(up, rootWaterlogged(t.r, up, b.above.get(t.r, t.rand, up)))
	}
}

type mangroveRoots struct {
	rootBase
	growThrough, muddyIn map[string]bool
	muddy                stateProvider
	maxWidth, maxLength  int32
	skew                 float32
}

func decodeMangroveRoots(cfg any) rootPlacer {
	m := ffield(cfg, "mangrove_root_placement")
	return &mangroveRoots{
		rootBase:    decodeRootBase(cfg),
		growThrough: blockSet(ffield(m, "can_grow_through")),
		muddyIn:     blockSet(ffield(m, "muddy_roots_in")),
		muddy:       decodeStateProvider(ffield(m, "muddy_roots_provider")),
		maxWidth:    fint(ffield(m, "max_root_width"), 0),
		maxLength:   fint(ffield(m, "max_root_length"), 0),
		skew:        ffloat(ffield(m, "random_skew_chance"), 0),
	}
}

func (m *mangroveRoots) canPlace(r *decoRegion, p Point) bool {
	return validTreePos(r, p) || m.growThrough[r.get(p).block()]
}

// placeRoots needs the column from the origin up to the trunk free; the
// roots below the trunk and out from each side are all worked out
// before any is placed, and one side that runs too long fails the tree.
func (m *mangroveRoots) placeRoots(t *tree, origin, trunk Point) bool {
	for y := origin.Y; y < trunk.Y; y++ {
		if !m.canPlace(t.r, Point{origin.X, y, origin.Z}) {
			return false
		}
	}
	roots := []Point{trunk.offset(dirDown)}
	for _, d := range horizontalDirs {
		var side []Point
		if !m.simulate(t, trunk.offset(d), d, trunk, &side, 0) {
			return false
		}
		roots = append(roots, side...)
		roots = append(roots, trunk.offset(d))
	}
	for _, p := range roots {
		if m.muddyIn[t.r.get(p).block()] {
			t.setRoot(p, rootWaterlogged(t.r, p, m.muddy.get(t.r, t.rand, p)))
			continue
		}
		m.place(t, p, func(q Point) bool { return m.canPlace(t.r, q) })
	}
	return true
}

func (m *mangroveRoots) simulate(t *tree, p Point, d int, trunk Point, out *[]Point, depth int32) bool {
	if depth == m.maxLength || int32(len(*out)) > m.maxLength {
		return false
	}
	for _, q := range m.next(t.rand, p, d, trunk) {
		if !m.canPlace(t.r, q) {
			continue
		}
		*out = append(*out, q)
		if !m.simulate(t, q, d, trunk, out, depth+1) {
			return false
		}
	}
	return true
}

// next is where a root at p grows: straight down once far from the
// trunk, sometimes also down-and-out near the edge of its width, and
// otherwise down or out at random.
func (m *mangroveRoots) next(rand Random, p Point, d int, trunk Point) []Point {
	below, out := p.offset(dirDown), p.offset(d)
	dist := manhattan(p, trunk)
	switch {
	case dist > m.maxWidth-3 && dist <= m.maxWidth:
		if rand.NextFloat() < m.skew {
			return []Point{below, out.offset(dirDown)}
		}
		return []Point{below}
	case dist > m.maxWidth:
		return []Point{below}
	case rand.NextFloat() < m.skew:
		return []Point{below}
	case rand.NextBool():
		return []Point{out}
	}
	return []Point{below}
}
