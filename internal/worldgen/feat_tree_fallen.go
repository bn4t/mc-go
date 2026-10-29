package worldgen

import "mc-go/internal/world"

// fallenTree is FallenTreeConfiguration.
type fallenTree struct {
	trunk      stateProvider
	length     intProvider
	stumpDecos []treeDecorator
	logDecos   []treeDecorator
}

func init() {
	featureBuilders["fallen_tree"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		ft := &fallenTree{
			trunk:      g.compileStateProvider(cfg["trunk_provider"]),
			length:     parseIntProvider(cfg["log_length"]),
			stumpDecos: parseDecorators(g, cfg["stump_decorators"]),
			logDecos:   parseDecorators(g, cfg["log_decorators"]),
		}
		return func(c *featCtx, r Random, p BlockPos) bool {
			ft.place(c, r, p)
			return true
		}
	}
	featureBuilders["huge_brown_mushroom"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		m := parseHugeMushroom(g, cfg, false)
		return m.place
	}
	featureBuilders["huge_red_mushroom"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		m := parseHugeMushroom(g, cfg, true)
		return m.place
	}
}

// place is FallenTreeFeature.placeFallenTree: a one-block stump, then a
// sideways log two or three blocks away when the whole run fits.
func (ft *fallenTree) place(c *featCtx, r Random, origin BlockPos) {
	rg := c.rg
	stump := ft.placeLog(rg, r, origin, nil)
	ft.decorate(c, r, []BlockPos{stump}, ft.stumpDecos)
	dir := randomHorizontal(r)
	n := ft.length(r) - 2
	m := relDir(origin, dir, 2+r.NextIntN(2))
	// setGroundHeightForFallenLogStartPos
	m = offset(m, 0, 1, 0)
	for i := 0; i < 6; i++ {
		if validTreePos(rg, m) && overSolidGround(rg, m) {
			break
		}
		m = offset(m, 0, -1, 0)
	}
	if !ft.canPlaceLog(rg, n, m, dir) {
		return
	}
	logs := newJSet()
	axis := axisSetter(axisOf(dir))
	for i := int32(0); i < n; i++ {
		logs.add(ft.placeLog(rg, r, m, axis))
		m = relDir(m, dir, 1)
	}
	ft.decorate(c, r, logs.list(), ft.logDecos)
}

func overSolidGround(rg *Region, p BlockPos) bool {
	return sturdyFull(blockAt(rg, offset(p, 0, -1, 0)), dirUp)
}

// canPlaceLog is FallenTreeFeature.canPlaceEntireFallenLog: every cell
// a valid tree position, with at most two cells in a row over a gap.
func (ft *fallenTree) canPlaceLog(rg *Region, n int32, m BlockPos, dir int) bool {
	gap := 0
	for i := int32(0); i < n; i++ {
		if !validTreePos(rg, m) {
			return false
		}
		if !overSolidGround(rg, m) {
			gap++
			if gap > 2 {
				return false
			}
		} else {
			gap = 0
		}
		m = relDir(m, dir, 1)
	}
	return true
}

// placeLog is FallenTreeFeature.placeLogBlock (setBlock flags 3, then
// markAboveForPostProcessing, which mc-go's post-processing-free
// pipeline does not need).
func (ft *fallenTree) placeLog(rg *Region, r Random, p BlockPos, f func(world.Block) world.Block) BlockPos {
	b := ft.trunk.getState(rg, r, p)
	if f != nil {
		b = f(b)
	}
	rg.set(p, b)
	return p
}

func (ft *fallenTree) decorate(c *featCtx, r Random, logs []BlockPos, decos []treeDecorator) {
	if len(decos) == 0 {
		return
	}
	dc := newDecoCtx(c.g, c.rg, r, c.rg.set, logs, nil, nil)
	for _, d := range decos {
		d.place(dc)
	}
}

// hugeMushroom is HugeMushroomFeatureConfiguration with the brown or
// red cap shape.
type hugeMushroom struct {
	red        bool
	cap, stem  stateProvider
	radius     int32
	canPlaceOn blockPredicate
}

func parseHugeMushroom(g *Generator, cfg obj, red bool) *hugeMushroom {
	m := &hugeMushroom{red: red, cap: g.compileStateProvider(cfg["cap_provider"]), stem: g.compileStateProvider(cfg["stem_provider"]), radius: 2,
		canPlaceOn: g.compilePredicate(cfg["can_place_on"])}
	if _, ok := cfg["foliage_radius"]; ok {
		m.radius = cfg.i32("foliage_radius")
	}
	return m
}

// radiusAt is getTreeRadiusForHeight, called with (-1, -1, radius, y).
func (m *hugeMushroom) radiusAt(y int32) int32 {
	if m.red {
		if y < -1 && y >= -4 {
			return m.radius
		}
		if y == -1 {
			return m.radius
		}
		return 0
	}
	if y <= 3 {
		return 0
	}
	return m.radius
}

// place is AbstractHugeMushroomFeature.place.
func (m *hugeMushroom) place(c *featCtx, r Random, o BlockPos) bool {
	rg := c.rg
	h := r.NextIntN(3) + 4
	if r.NextIntN(12) == 0 {
		h *= 2
	}
	if o[1] < rg.MinY()+1 || o[1]+h+1 > rg.MaxY() {
		return false
	}
	if !m.canPlaceOn(rg, offset(o, 0, -1, 0)) {
		return false
	}
	for y := int32(0); y <= h; y++ {
		rr := m.radiusAt(y)
		for dx := -rr; dx <= rr; dx++ {
			for dz := -rr; dz <= rr; dz++ {
				b := blockAt(rg, offset(o, dx, y, dz))
				if !isAir(b) && !stateInTag(b, "leaves") {
					return false
				}
			}
		}
	}
	if m.red {
		m.redCap(rg, r, o, h)
	} else {
		m.brownCap(rg, r, o, h)
	}
	for i := int32(0); i < h; i++ {
		mushroomBlock(rg, offset(o, 0, i, 0), m.stem.getState(rg, r, o))
	}
	return true
}

// mushroomBlock is placeMushroomBlock: only into air or
// replaceable_by_mushrooms.
func mushroomBlock(rg *Region, p BlockPos, b world.Block) {
	cur := blockAt(rg, p)
	if isAir(cur) || stateInTag(cur, "replaceable_by_mushrooms") {
		rg.set(p, b)
	}
}

func setFaces(b world.Block, faces map[string]bool) world.Block {
	for f, v := range faces {
		s := "false"
		if v {
			s = "true"
		}
		b = withProp(b, f, s)
	}
	return b
}

func hasFaces(b world.Block, names ...string) bool {
	for _, n := range names {
		if !world.HasProperty(b, n) {
			return false
		}
	}
	return true
}

// brownCap is HugeBrownMushroomFeature.makeCap: a flat square without
// corners, its rim faces out.
func (m *hugeMushroom) brownCap(rg *Region, r Random, o BlockPos, h int32) {
	k := m.radius
	for dx := -k; dx <= k; dx++ {
		for dz := -k; dz <= k; dz++ {
			w, e, n, s := dx == -k, dx == k, dz == -k, dz == k
			ex, ez := w || e, n || s
			if ex && ez {
				continue
			}
			p := offset(o, dx, h, dz)
			fw := w || (ez && dx == 1-k)
			fe := e || (ez && dx == k-1)
			fn := n || (ex && dz == 1-k)
			fs := s || (ex && dz == k-1)
			b := m.cap.getState(rg, r, o)
			if hasFaces(b, "west", "east", "north", "south") {
				b = setFaces(b, map[string]bool{"west": fw, "east": fe, "north": fn, "south": fs})
			}
			mushroomBlock(rg, p, b)
		}
	}
}

// redCap is HugeRedMushroomFeature.makeCap: three ring layers and a
// top, faces out.
func (m *hugeMushroom) redCap(rg *Region, r Random, o BlockPos, h int32) {
	for y := h - 3; y <= h; y++ {
		k := m.radius
		if y >= h {
			k = m.radius - 1
		}
		l := m.radius - 2
		for dx := -k; dx <= k; dx++ {
			for dz := -k; dz <= k; dz++ {
				ex := dx == -k || dx == k
				ez := dz == -k || dz == k
				if y < h && ex == ez {
					continue
				}
				p := offset(o, dx, y, dz)
				b := m.cap.getState(rg, r, o)
				if hasFaces(b, "west", "east", "north", "south", "up") {
					b = setFaces(b, map[string]bool{"up": y >= h-1, "west": dx < -l, "east": dx > l, "north": dz < -l, "south": dz > l})
				}
				mushroomBlock(rg, p, b)
			}
		}
	}
}
