package worldgen

import "math"

// Cave features: speleothems (pointed dripstone and sulfur spikes)
// growing from a patch of their base block, and large dripstone
// columns between a cave's floor and ceiling.

func init() {
	featureKinds["minecraft:speleothem"] = decodeSpeleothem
	featureKinds["minecraft:large_dripstone"] = decodeLargeDripstone
	featureKinds["minecraft:speleothem_cluster"] = decodeSpeleothemCluster
}

// column is Column: the floor and ceiling bounding an open run of
// blocks, either of which may be missing.
type column struct {
	floor, ceiling       int32
	hasFloor, hasCeiling bool
}

func (c column) isRange() bool { return c.hasFloor && c.hasCeiling }

// height is a range's open height.
func (c column) height() int32 { return c.ceiling - c.floor - 1 }

// scanColumn is Column.scan: from p, which must satisfy inside, at most
// searchRange-1 steps each way while inside holds; each end counts if
// the block reached satisfies edge.
func scanColumn(r *decoRegion, p Point, searchRange int32, inside, edge func(StateID) bool) (column, bool) {
	if !inside(r.get(p)) {
		return column{}, false
	}
	var c column
	scan := func(dir int) (int32, bool) {
		q := p
		for i := int32(1); i < searchRange && inside(r.get(q)); i++ {
			q = q.offset(dir)
		}
		return q.Y, edge(r.get(q))
	}
	c.ceiling, c.hasCeiling = scan(dirUp)
	c.floor, c.hasFloor = scan(dirDown)
	return c, true
}

type speleothemBlocks struct {
	base, pointed string
	replaceable   map[string]bool
}

func (b speleothemBlocks) isBase(s StateID) bool { return s.is(b.base) || b.replaceable[s.block()] }

func isEmptyOrWater(s StateID) bool { return s.isAir() || s.is("water") }

// placeBase turns a replaceable block into the base block.
func (b speleothemBlocks) placeBase(r *decoRegion, p Point) bool {
	if !b.replaceable[r.get(p).block()] {
		return false
	}
	r.set(p, defaultState(b.base))
	return true
}

// grow builds a speleothem of the given height from p toward dir, base
// first and tip last, when the block behind p is a base block. Pointed
// blocks take the water at their spot.
func (b speleothemBlocks) grow(r *decoRegion, p Point, dir int, height int32, merge bool) {
	if !b.isBase(r.get(p.offset(dir ^ 1))) {
		return
	}
	vertical := "down"
	if dir == dirUp {
		vertical = "up"
	}
	pointed := defaultState(b.pointed).with("vertical_direction", vertical)
	put := func(thickness string) {
		s := pointed.with("thickness", thickness).with("waterlogged", boolStr(r.isWaterAt(p)))
		r.set(p, s)
		p = p.offset(dir)
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

func decodeSpeleothem(cfg any) func(*featureCtx) bool {
	b := speleothemBlocks{
		base:        blockStateOf(ffield(cfg, "base_block")).block(),
		pointed:     blockStateOf(ffield(cfg, "pointed_block")).block(),
		replaceable: blockSet(ffield(cfg, "replaceable_blocks")),
	}
	chance := func(key string, def float32) float32 {
		if fhas(cfg, key) {
			return ffloat(ffield(cfg, key), def)
		}
		return def
	}
	taller := chance("chance_of_taller_generation", 0.2)
	spread := chance("chance_of_directional_spread", 0.7)
	radius2 := chance("chance_of_spread_radius2", 0.5)
	radius3 := chance("chance_of_spread_radius3", 0.5)
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		up, down := b.isBase(r.get(o.offset(dirUp))), b.isBase(r.get(o.offset(dirDown)))
		var tip int
		switch {
		case up && down:
			tip = dirUp
			if rand.NextBool() {
				tip = dirDown
			}
		case up:
			tip = dirDown
		case down:
			tip = dirUp
		default:
			return false
		}
		// A patch of base blocks behind the speleothem, reaching out up
		// to three random steps on each side.
		base := o.offset(tip ^ 1)
		b.placeBase(r, base)
		for _, d := range horizontalDirs {
			if rand.NextFloat() > spread {
				continue
			}
			p := base.offset(d)
			b.placeBase(r, p)
			if rand.NextFloat() > radius2 {
				continue
			}
			p = p.offset(int(rand.NextIntn(6)))
			b.placeBase(r, p)
			if rand.NextFloat() > radius3 {
				continue
			}
			p = p.offset(int(rand.NextIntn(6)))
			b.placeBase(r, p)
		}
		height := int32(1)
		if rand.NextFloat() < taller && isEmptyOrWater(r.get(o.offset(tip))) {
			height = 2
		}
		b.grow(r, o, tip, height, false)
		return true
	}
}

func isEmptyOrWaterOrLava(s StateID) bool { return s.isAir() || s.is("water") || s.is("lava") }

// speleothemHeight is the height of a speleothem's profile at distance
// d from its axis: a curve over radius and scale, floored by bluntness.
func speleothemHeight(d, radius, scale, bluntness float64) float64 {
	if d < bluntness {
		d = bluntness
	}
	h := d / radius * 0.384
	a := 0.75 * math.Pow(h, 4.0/3.0)
	b := math.Pow(h, 2.0/3.0)
	c := 1.0 / 3.0 * math.Log(h)
	v := math.Max(float64(scale*(a-b-c)), 0)
	return v / 0.384 * radius
}

// windOffsetter leans a dripstone column: positions shift by the wind
// times their distance from the origin's Y, within maxOffset.
type windOffsetter struct {
	originY, maxOffset int32
	wind               bool
	wx, wz             float64
}

func (w windOffsetter) offset(p Point) Point {
	if !w.wind {
		return p
	}
	dy := float64(w.originY - p.Y)
	clamp := func(v float64) int32 { return max(-w.maxOffset, min(w.maxOffset, int32(math.Floor(v)))) }
	return Point{p.X + clamp(w.wx*dy), p.Y, p.Z + clamp(w.wz*dy)}
}

type largeDripstone struct {
	root             Point
	up               bool
	radius           int32
	bluntness, scale float64
}

func (d *largeDripstone) heightAt(dist float32) int32 {
	return int32(speleothemHeight(float64(dist), float64(d.radius), d.scale, d.bluntness))
}

func circleInStone(r *decoRegion, p Point, radius int32) bool {
	if isEmptyOrWaterOrLava(r.get(p)) {
		return false
	}
	step := 6 / float32(radius)
	for a := float32(0); a < 6.2831855; a += step {
		x := int32(mthCos(float64(a)) * float32(radius))
		z := int32(mthSin(float64(a)) * float32(radius))
		if isEmptyOrWaterOrLava(r.get(Point{p.X + x, p.Y, p.Z + z})) {
			return false
		}
	}
	return true
}

// settle moves the root back into the rock, up to ten blocks (fewer for
// a short column), until a circle of its radius is mostly embedded,
// halving the radius each time that fails; lava on the way stops it.
func (d *largeDripstone) settle(r *decoRegion, w windOffsetter) bool {
	back := dirUp
	if d.up {
		back = dirDown
	}
	for d.radius > 1 {
		m := d.root
		n := min32(10, d.heightAt(0))
		for i := int32(0); i < n; i++ {
			if r.get(m).is("lava") {
				return false
			}
			if circleInStone(r, w.offset(m), d.radius) {
				d.root = m
				return true
			}
			m = m.offset(back)
		}
		d.radius /= 2
	}
	return false
}

// place fills the column's profile with dripstone, a fifth of the
// columns of cells cut to 80–100%, a stalagmite never above the
// surface; a run stops at rock once it has placed anything.
func (d *largeDripstone) place(r *decoRegion, rand Random, w windOffsetter) {
	grow := dirDown
	if d.up {
		grow = dirUp
	}
	block := defaultState("dripstone_block")
	for dx := -d.radius; dx <= d.radius; dx++ {
		for dz := -d.radius; dz <= d.radius; dz++ {
			dist := float32(math.Sqrt(float64(float32(dx*dx + dz*dz))))
			if dist > float32(d.radius) {
				continue
			}
			h := d.heightAt(dist)
			if h <= 0 {
				continue
			}
			if float64(rand.NextFloat()) < 0.2 {
				h = int32(float32(h) * randomBetween(rand, 0.8, 1))
			}
			m := Point{d.root.X + dx, d.root.Y, d.root.Z + dz}
			top := int32(math.MaxInt32)
			if d.up {
				top = r.heightAt(hmWorldSurfaceWG, m.X, m.Z)
			}
			placed := false
			for i := int32(0); i < h && m.Y < top; i++ {
				p := w.offset(m)
				if isEmptyOrWaterOrLava(r.get(p)) {
					placed = true
					r.set(p, block)
				} else if placed && r.get(p).inTag("base_stone_overworld") {
					break
				}
				m = m.offset(grow)
			}
		}
	}
}

func decodeLargeDripstone(cfg any) func(*featureCtx) bool {
	search := int32(30)
	if fhas(cfg, "floor_to_ceiling_search_range") {
		search = fint(ffield(cfg, "floor_to_ceiling_search_range"), 30)
	}
	replaceable := blockSet(ffield(cfg, "replaceable_blocks"))
	columnRadius := decodeIntProvider(ffield(cfg, "column_radius"))
	heightScale := decodeFloatProvider(ffield(cfg, "height_scale"))
	ratio := ffloat(ffield(cfg, "max_column_radius_to_cave_height_ratio"), 0)
	stalactiteBlunt := decodeFloatProvider(ffield(cfg, "stalactite_bluntness"))
	stalagmiteBlunt := decodeFloatProvider(ffield(cfg, "stalagmite_bluntness"))
	windSpeed := decodeFloatProvider(ffield(cfg, "wind_speed"))
	minRadius := fint(ffield(cfg, "min_radius_for_wind"), 0)
	minBlunt := ffloat(ffield(cfg, "min_bluntness_for_wind"), 0)
	edge := func(s StateID) bool { return s.is("dripstone_block") || replaceable[s.block()] || s.is("lava") }
	return func(fc *featureCtx) bool {
		r, o, rand := fc.r, fc.origin, fc.rand
		if !isEmptyOrWater(r.get(o)) {
			return false
		}
		c, ok := scanColumn(r, o, search, isEmptyOrWater, edge)
		if !ok || !c.isRange() || c.height() < 4 {
			return false
		}
		maxR := int32(float32(c.height()) * ratio)
		lo := columnRadius.minValue()
		radius := randomBetweenInclusive(rand, lo, max(lo, min(maxR, columnRadius.maxValue())))
		newDrip := func(y int32, up bool, blunt FloatProvider) *largeDripstone {
			b := float64(blunt.sample(rand))
			return &largeDripstone{root: Point{o.X, y, o.Z}, up: up, radius: radius, bluntness: b, scale: float64(heightScale.sample(rand))}
		}
		stalactite := newDrip(c.ceiling-1, false, stalactiteBlunt)
		stalagmite := newDrip(c.floor+1, true, stalagmiteBlunt)
		suits := func(d *largeDripstone) bool { return d.radius >= minRadius && d.bluntness >= float64(minBlunt) }
		var w windOffsetter
		if suits(stalactite) && suits(stalagmite) {
			speed := windSpeed.sample(rand)
			angle := rand.NextFloat() * math.Pi
			w = windOffsetter{originY: o.Y, maxOffset: 16 - radius, wind: true,
				wx: float64(mthCos(float64(angle)) * speed), wz: float64(mthSin(float64(angle)) * speed)}
		}
		upper, lower := stalactite.settle(r, w), stalagmite.settle(r, w)
		if upper {
			stalactite.place(r, rand, w)
		}
		if lower {
			stalagmite.place(r, rand, w)
		}
		return true
	}
}

// clampedMapF is the float clamped map: v from [a, b] onto [lo, hi],
// held at the ends.
func clampedMapF(v, a, b, lo, hi float32) float32 {
	t := (v - a) / (b - a)
	switch {
	case t < 0:
		return lo
	case t > 1:
		return hi
	}
	return lo + float32(t*(hi-lo))
}

type speleothemCluster struct {
	speleothemBlocks
	search                                    int32
	height, radius, layerThickness            intProvider
	density, wetness                          FloatProvider
	maxDiff, deviation, edgeDistance, biasMax int32
	edgeChance                                float32
}

func decodeSpeleothemCluster(cfg any) func(*featureCtx) bool {
	c := &speleothemCluster{
		speleothemBlocks: speleothemBlocks{
			base:        blockStateOf(ffield(cfg, "base_block")).block(),
			pointed:     blockStateOf(ffield(cfg, "pointed_block")).block(),
			replaceable: blockSet(ffield(cfg, "replaceable_blocks")),
		},
		search:         fint(ffield(cfg, "floor_to_ceiling_search_range"), 0),
		height:         decodeIntProvider(ffield(cfg, "height")),
		radius:         decodeIntProvider(ffield(cfg, "radius")),
		layerThickness: decodeIntProvider(ffield(cfg, "speleothem_block_layer_thickness")),
		density:        decodeFloatProvider(ffield(cfg, "density")),
		wetness:        decodeFloatProvider(ffield(cfg, "wetness")),
		maxDiff:        fint(ffield(cfg, "max_stalagmite_stalactite_height_diff"), 0),
		deviation:      fint(ffield(cfg, "height_deviation"), 0),
		edgeDistance:   fint(ffield(cfg, "max_distance_from_edge_affecting_chance_of_speleothem"), 0),
		biasMax:        fint(ffield(cfg, "max_distance_from_center_affecting_height_bias"), 0),
		edgeChance:     ffloat(ffield(cfg, "chance_of_speleothem_at_max_distance_from_center"), 0),
	}
	return c.place
}

// place runs a column at every cell of a rectangle of two sampled
// radii, cells nearer the edge less likely to grow speleothems.
func (c *speleothemCluster) place(fc *featureCtx) bool {
	r, o, rand := fc.r, fc.origin, fc.rand
	if !isEmptyOrWater(r.get(o)) {
		return false
	}
	height := c.height.sample(rand)
	wetness := c.wetness.sample(rand)
	density := c.density.sample(rand)
	rx, rz := c.radius.sample(rand), c.radius.sample(rand)
	for dx := -rx; dx <= rx; dx++ {
		for dz := -rz; dz <= rz; dz++ {
			edge := min32(rx-abs(dx), rz-abs(dz))
			chance := float64(clampedMapF(float32(edge), 0, float32(c.edgeDistance), c.edgeChance, 1))
			c.column(r, rand, Point{o.X + dx, o.Y, o.Z + dz}, dx, dz, wetness, chance, height, density)
		}
	}
	return true
}

// speleothemLength is a speleothem's length at offset (dx, dz): none
// past the density roll, otherwise a normal draw biased toward half the
// limit near the centre and toward zero further out.
func (c *speleothemCluster) speleothemLength(rand Random, dx, dz int32, density float32, limit int32) int32 {
	if rand.NextFloat() > density {
		return 0
	}
	d := abs(dx) + abs(dz)
	bias := float32(clampedMap(float64(d), 0, float64(c.biasMax), float64(limit)/2, 0))
	return int32(clampedNormal(rand, bias, float32(c.deviation), 0, float32(limit)))
}

func (c *speleothemCluster) layerBase(r *decoRegion, p Point, n int32, dir int) {
	for i := int32(0); i < n && c.placeBase(r, p); i++ {
		p = p.offset(dir)
	}
}

func (c *speleothemCluster) canPlacePool(r *decoRegion, p Point) bool {
	s := r.get(p)
	if s.is("water") || s.is(c.base) || s.is(c.pointed) || r.isWaterAt(p.offset(dirUp)) {
		return false
	}
	nextToWater := func(q Point) bool { return r.get(q).inTag("base_stone_overworld") || r.isWaterAt(q) }
	for _, d := range horizontalDirs {
		if !nextToWater(p.offset(d)) {
			return false
		}
	}
	return nextToWater(p.offset(dirDown))
}

// column scans the cave above and below p and grows a stalactite and a
// stalagmite there over layers of base block, maybe with a pool of
// water on the floor; the two meet (and maybe merge) when they would
// cross.
func (c *speleothemCluster) column(r *decoRegion, rand Random, p Point, dx, dz int32, wetness float32, chance float64, height int32, density float32) {
	notEmpty := func(s StateID) bool { return !isEmptyOrWater(s) }
	col, ok := scanColumn(r, p, c.search, isEmptyOrWater, notEmpty)
	if !ok || !col.hasCeiling && !col.hasFloor {
		return
	}
	ceiling, hasCeiling := col.ceiling, col.hasCeiling
	pool := rand.NextFloat() < wetness
	if pool && col.hasFloor && c.canPlacePool(r, Point{p.X, col.floor, p.Z}) {
		r.set(Point{p.X, col.floor, p.Z}, defaultState("water"))
		col.floor--
	}
	floor, hasFloor := col.floor, col.hasFloor
	stalactite := int32(0)
	if rand.NextDouble() < chance && hasCeiling && !r.get(Point{p.X, ceiling, p.Z}).is("lava") {
		c.layerBase(r, Point{p.X, ceiling, p.Z}, c.layerThickness.sample(rand), dirUp)
		limit := height
		if hasFloor {
			limit = min32(height, ceiling-floor)
		}
		stalactite = c.speleothemLength(rand, dx, dz, density, limit)
	}
	stalagmite := int32(0)
	if rand.NextDouble() < chance && hasFloor && !r.get(Point{p.X, floor, p.Z}).is("lava") {
		c.layerBase(r, Point{p.X, floor, p.Z}, c.layerThickness.sample(rand), dirDown)
		if hasCeiling {
			stalagmite = max32(0, stalactite+randomBetweenInclusive(rand, -c.maxDiff, c.maxDiff))
		} else {
			stalagmite = c.speleothemLength(rand, dx, dz, density, height)
		}
	}
	down, up := stalactite, stalagmite
	if hasCeiling && hasFloor && ceiling-stalactite <= floor+stalagmite {
		lo := max32(ceiling-stalactite, floor+1)
		hi := min32(floor+stalagmite, ceiling-1)
		k := randomBetweenInclusive(rand, lo, hi+1)
		down, up = ceiling-k, k-1-floor
	}
	merge := rand.NextBool() && down > 0 && up > 0 && col.isRange() && down+up == col.height()
	if hasCeiling {
		c.grow(r, Point{p.X, ceiling - 1, p.Z}, dirDown, down, merge)
	}
	if hasFloor {
		c.grow(r, Point{p.X, floor + 1, p.Z}, dirUp, up, merge)
	}
}
