package worldgen

// Huge mushrooms: a stem of four to six blocks (doubled one time in
// twelve) under a red dome or a flat brown cap.

func init() {
	featureKinds["minecraft:huge_red_mushroom"] = func(cfg any) func(*featureCtx) bool { return decodeHugeMushroom(cfg, true) }
	featureKinds["minecraft:huge_brown_mushroom"] = func(cfg any) func(*featureCtx) bool { return decodeHugeMushroom(cfg, false) }
}

type hugeMushroom struct {
	red        bool
	cap, stem  stateProvider
	radius     int32
	canPlaceOn blockPredicate
}

func decodeHugeMushroom(cfg any, red bool) func(*featureCtx) bool {
	m := &hugeMushroom{
		red:        red,
		cap:        decodeStateProvider(ffield(cfg, "cap_provider")),
		stem:       decodeStateProvider(ffield(cfg, "stem_provider")),
		radius:     fint(ffield(cfg, "foliage_radius"), 2),
		canPlaceOn: decodeBlockPredicate(ffield(cfg, "can_place_on")),
	}
	return m.place
}

// radiusAt is the cap radius checked for free space at height y. The
// check passes a height of -1, so a red mushroom only checks its stem
// column and a brown one its cap layers above 3.
func (m *hugeMushroom) radiusAt(y int32) int32 {
	if m.red {
		const height = -1
		if y < height && y >= height-3 || y == height {
			return m.radius
		}
		return 0
	}
	if y <= 3 {
		return 0
	}
	return m.radius
}

func placeMushroomBlock(r *decoRegion, p Point, s StateID) {
	if cur := r.get(p); cur.isAir() || cur.inTag("replaceable_by_mushrooms") {
		r.set(p, s)
	}
}

func (m *hugeMushroom) place(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	height := rand.NextIntn(3) + 4
	if rand.NextIntn(12) == 0 {
		height *= 2
	}
	if o.Y < r.minY+1 || o.Y+height+1 > r.maxY() || !m.canPlaceOn(r, o.offset(dirDown)) {
		return false
	}
	for y := int32(0); y <= height; y++ {
		rad := m.radiusAt(y)
		for dx := -rad; dx <= rad; dx++ {
			for dz := -rad; dz <= rad; dz++ {
				if s := r.get(Point{o.X + dx, o.Y + y, o.Z + dz}); !s.isAir() && !s.inTag("leaves") {
					return false
				}
			}
		}
	}
	if m.red {
		m.redCap(r, rand, o, height)
	} else {
		m.brownCap(r, rand, o, height)
	}
	for i := int32(0); i < height; i++ {
		placeMushroomBlock(r, Point{o.X, o.Y + i, o.Z}, m.stem.get(r, rand, o))
	}
	return true
}

func hasMushroomFaces(s StateID) bool {
	return s.hasProp("west") && s.hasProp("east") && s.hasProp("north") && s.hasProp("south")
}

// redCap is a dome: four layers below the top, sides only below the
// top layer, inner faces hidden.
func (m *hugeMushroom) redCap(r *decoRegion, rand Random, o Point, height int32) {
	inner := m.radius - 2
	for y := height - 3; y <= height; y++ {
		rad := m.radius
		if y >= height {
			rad--
		}
		for dx := -rad; dx <= rad; dx++ {
			for dz := -rad; dz <= rad; dz++ {
				xEdge, zEdge := dx == -rad || dx == rad, dz == -rad || dz == rad
				if y < height && xEdge == zEdge {
					continue
				}
				s := m.cap.get(r, rand, o)
				if hasMushroomFaces(s) && s.hasProp("up") {
					s = s.with("up", boolStr(y >= height-1)).with("west", boolStr(dx < -inner)).
						with("east", boolStr(dx > inner)).with("north", boolStr(dz < -inner)).with("south", boolStr(dz > inner))
				}
				placeMushroomBlock(r, Point{o.X + dx, o.Y + y, o.Z + dz}, s)
			}
		}
	}
}

// brownCap is one flat layer with its corners cut, sides showing round
// the rim.
func (m *hugeMushroom) brownCap(r *decoRegion, rand Random, o Point, height int32) {
	rad := m.radius
	for dx := -rad; dx <= rad; dx++ {
		for dz := -rad; dz <= rad; dz++ {
			minX, maxX, minZ, maxZ := dx == -rad, dx == rad, dz == -rad, dz == rad
			xEdge, zEdge := minX || maxX, minZ || maxZ
			if xEdge && zEdge {
				continue
			}
			west := minX || zEdge && dx == 1-rad
			east := maxX || zEdge && dx == rad-1
			north := minZ || xEdge && dz == 1-rad
			south := maxZ || xEdge && dz == rad-1
			s := m.cap.get(r, rand, o)
			if hasMushroomFaces(s) {
				s = s.with("west", boolStr(west)).with("east", boolStr(east)).with("north", boolStr(north)).with("south", boolStr(south))
			}
			placeMushroomBlock(r, Point{o.X + dx, o.Y + height, o.Z + dz}, s)
		}
	}
}
