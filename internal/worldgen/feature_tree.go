package worldgen

import (
	"sort"
	"strconv"
)

// Trees: a trunk placer lays the logs and returns foliage attachments, a
// foliage placer grows leaves around each, decorators add their extras,
// then leaf distances are recomputed from the logs and the shape's edge
// updates its neighbours.

func init() {
	featureKinds["minecraft:tree"] = decodeTree
}

type treeConfig struct {
	trunk, foliage, belowTrunk stateProvider
	trunkPlacer                trunkPlacer
	foliagePlacer              foliagePlacer
	size                       featureSize
	decorators                 []treeDecorator
	ignoreVines                bool
	rootPlacer                 rootPlacer // nil for none
}

// tree is one tree's placement: its configuration, the region, the
// random and the four position sets the setters fill.
type tree struct {
	cfg                              *treeConfig
	r                                *decoRegion
	rand                             Random
	roots, logs, leaves, decorations *posSet
}

func (t *tree) setRoot(p Point, s StateID)       { t.roots.add(p); t.r.set(p, s) }
func (t *tree) setLog(p Point, s StateID)        { t.logs.add(p); t.r.set(p, s) }
func (t *tree) setLeaf(p Point, s StateID)       { t.leaves.add(p); t.r.set(p, s) }
func (t *tree) setDecoration(p Point, s StateID) { t.decorations.add(p); t.r.set(p, s) }

type trunkPlacer interface {
	treeHeight(rand Random) int32
	placeTrunk(t *tree, height int32, origin Point) []foliageAttachment
}

type foliageAttachment struct {
	pos          Point
	radiusOffset int32
	doubleTrunk  bool
}

type foliagePlacer interface {
	foliageHeight(rand Random, height int32, cfg *treeConfig) int32
	foliageRadius(rand Random, trunkHeight int32) int32
	// createFoliage samples the placer's offset, then places leaves.
	createFoliage(t *tree, treeHeight int32, a foliageAttachment, foliageHeight, radius int32)
}

type featureSize interface {
	sizeAtHeight(height, y int32) int32
	minClippedHeight() (int32, bool)
}

type treeDecorator interface{ place(t *tree) }

type rootPlacer interface {
	trunkOrigin(origin Point, rand Random) Point
	placeRoots(t *tree, origin, trunkOrigin Point) bool
}

var (
	treeTrunkPlacers   = map[string]func(cfg any) trunkPlacer{}
	treeFoliagePlacers = map[string]func(cfg any) foliagePlacer{}
	treeDecorators     = map[string]func(cfg any) treeDecorator{}
	treeRootPlacers    = map[string]func(cfg any) rootPlacer{}
)

func decodeTree(cfg any) func(*featureCtx) bool {
	c := &treeConfig{
		trunk:       decodeStateProvider(ffield(cfg, "trunk_provider")),
		foliage:     decodeStateProvider(ffield(cfg, "foliage_provider")),
		belowTrunk:  decodeStateProvider(ffield(cfg, "below_trunk_provider")),
		size:        decodeFeatureSize(ffield(cfg, "minimum_size")),
		ignoreVines: fbool(ffield(cfg, "ignore_vines"), false),
	}
	tp := ffield(cfg, "trunk_placer")
	mk, ok := treeTrunkPlacers[ftype(tp)]
	if !ok {
		unsupported("trunk placer %s", ftype(tp))
	}
	c.trunkPlacer = mk(tp)
	fp := ffield(cfg, "foliage_placer")
	mf, ok := treeFoliagePlacers[ftype(fp)]
	if !ok {
		unsupported("foliage placer %s", ftype(fp))
	}
	c.foliagePlacer = mf(fp)
	for _, d := range flist(ffield(cfg, "decorators")) {
		md, ok := treeDecorators[ftype(d)]
		if !ok {
			unsupported("tree decorator %s", ftype(d))
		}
		c.decorators = append(c.decorators, md(d))
	}
	if fhas(cfg, "root_placer") {
		rp := ffield(cfg, "root_placer")
		mr, ok := treeRootPlacers[ftype(rp)]
		if !ok {
			unsupported("root placer %s", ftype(rp))
		}
		c.rootPlacer = mr(rp)
	}
	return func(fc *featureCtx) bool { return c.place(fc) }
}

func (c *treeConfig) place(fc *featureCtx) bool {
	t := &tree{cfg: c, r: fc.r, rand: fc.rand, roots: &posSet{}, logs: &posSet{}, leaves: &posSet{}, decorations: &posSet{}}
	if !c.doPlace(t, fc.origin) || (t.logs.size == 0 && t.leaves.size == 0) {
		return false
	}
	for _, d := range c.decorators {
		d.place(t)
	}
	var all []Point
	for _, s := range []*posSet{t.roots, t.logs, t.leaves, t.decorations} {
		all = append(all, s.list()...)
	}
	if len(all) == 0 {
		return false
	}
	min, max := all[0], all[0]
	for _, p := range all[1:] {
		min = Point{min32(min.X, p.X), min32(min.Y, p.Y), min32(min.Z, p.Z)}
		max = Point{max32(max.X, p.X), max32(max.Y, p.Y), max32(max.Z, p.Z)}
	}
	shape := updateLeaves(t, newVoxelBox(min, max))
	updateShapeAtEdge(t.r, shape)
	return true
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func (c *treeConfig) doPlace(t *tree, origin Point) bool {
	height := c.trunkPlacer.treeHeight(t.rand)
	foliageHeight := c.foliagePlacer.foliageHeight(t.rand, height, c)
	radius := c.foliagePlacer.foliageRadius(t.rand, height-foliageHeight)
	trunkOrigin := origin
	if c.rootPlacer != nil {
		trunkOrigin = c.rootPlacer.trunkOrigin(origin, t.rand)
	}
	lo := min32(origin.Y, trunkOrigin.Y)
	hi := max32(origin.Y, trunkOrigin.Y) + height + 1
	if lo < t.r.minY+1 || hi > t.r.minY+t.r.height {
		return false
	}
	free := c.maxFreeHeight(t, height, trunkOrigin)
	if free < height {
		if clip, ok := c.size.minClippedHeight(); !ok || free < clip {
			return false
		}
	}
	if c.rootPlacer != nil && !c.rootPlacer.placeRoots(t, origin, trunkOrigin) {
		return false
	}
	for _, a := range c.trunkPlacer.placeTrunk(t, free, trunkOrigin) {
		c.foliagePlacer.createFoliage(t, free, a, foliageHeight, radius)
	}
	return true
}

// maxFreeHeight is the height up to which the column around the trunk
// (as wide as the feature size says at each height) is free, two below
// the first blocked layer.
func (c *treeConfig) maxFreeHeight(t *tree, height int32, p Point) int32 {
	for y := int32(0); y <= height+1; y++ {
		size := c.size.sizeAtHeight(height, y)
		for dx := -size; dx <= size; dx++ {
			for dz := -size; dz <= size; dz++ {
				q := Point{p.X + dx, p.Y + y, p.Z + dz}
				if !t.isFree(q) || (!c.ignoreVines && t.r.get(q).is("vine")) {
					return y - 2
				}
			}
		}
	}
	return height
}

// validTreePos is air or a block trees may replace.
func validTreePos(r *decoRegion, p Point) bool {
	s := r.get(p)
	return s.isAir() || s.inTag("replaceable_by_trees")
}

// growsThrough is a trunk placer that also grows through blocks of its
// own set (TrunkPlacer.validTreePos overridden).
type growsThrough interface{ growsThrough(s StateID) bool }

// validTrunkPos is the trunk placer's validTreePos.
func (t *tree) validTrunkPos(p Point) bool {
	if validTreePos(t.r, p) {
		return true
	}
	if g, ok := t.cfg.trunkPlacer.(growsThrough); ok {
		return g.growsThrough(t.r.get(p))
	}
	return false
}

// isFree is TrunkPlacer.isFree: a valid trunk position or a log.
func (t *tree) isFree(p Point) bool { return t.validTrunkPos(p) || t.r.get(p).inTag("logs") }

// placeLog sets a trunk log at p if it is a valid trunk position.
func (t *tree) placeLog(p Point, f func(StateID) StateID) bool {
	if !t.validTrunkPos(p) {
		return false
	}
	s := t.cfg.trunk.get(t.r, t.rand, p)
	if f != nil {
		s = f(s)
	}
	t.setLog(p, s)
	return true
}

// placeBelowTrunk puts the below-trunk block (dirt where the rules
// allow) at p through the trunk setter.
func (t *tree) placeBelowTrunk(p Point) {
	if s, ok := t.cfg.belowTrunk.optional(t.r, t.rand, p); ok {
		t.setLog(p, s)
	}
}

// trunkHeights are the three height parameters every trunk placer has.
type trunkHeights struct{ base, randA, randB int32 }

func decodeTrunkHeights(cfg any) trunkHeights {
	return trunkHeights{fint(ffield(cfg, "base_height"), 0), fint(ffield(cfg, "height_rand_a"), 0), fint(ffield(cfg, "height_rand_b"), 0)}
}

func (h trunkHeights) treeHeight(rand Random) int32 {
	return h.base + rand.NextIntn(h.randA+1) + rand.NextIntn(h.randB+1)
}

type straightTrunk struct{ trunkHeights }

func (s straightTrunk) placeTrunk(t *tree, height int32, origin Point) []foliageAttachment {
	t.placeBelowTrunk(origin.offset(dirDown))
	for i := int32(0); i < height; i++ {
		t.placeLog(Point{origin.X, origin.Y + i, origin.Z}, nil)
	}
	return []foliageAttachment{{pos: Point{origin.X, origin.Y + height, origin.Z}}}
}

// foliageBase holds the radius and offset every foliage placer samples.
type foliageBase struct {
	radius, offset intProvider
}

func decodeFoliageBase(cfg any) foliageBase {
	return foliageBase{decodeIntProvider(ffield(cfg, "radius")), decodeIntProvider(ffield(cfg, "offset"))}
}

func (f foliageBase) foliageRadius(rand Random, _ int32) int32 { return f.radius.sample(rand) }

// leafSkip is a foliage placer's shouldSkipLocationSigned.
type leafSkip interface {
	skipSigned(rand Random, dx, y, dz, rng int32, large bool) bool
}

// absSkip is a shouldSkipLocation, seen through the default signed
// form: offsets folded to their distance from the trunk (from either
// trunk column of a double trunk).
type absSkip func(rand Random, dx, y, dz, rng int32, large bool) bool

func (f absSkip) skipSigned(rand Random, dx, y, dz, rng int32, large bool) bool {
	ax, az := abs(dx), abs(dz)
	if large {
		ax, az = min32(abs(dx), abs(dx-1)), min32(abs(dz), abs(dz-1))
	}
	return f(rand, ax, y, az, rng, large)
}

func abs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// placeLeavesRow places a square layer of leaves y above pos, of the
// given range (one wider for a double trunk), skipping what skip says.
func (t *tree) placeLeavesRow(skip leafSkip, pos Point, rng, y int32, large bool) {
	extra := int32(0)
	if large {
		extra = 1
	}
	for dx := -rng; dx <= rng+extra; dx++ {
		for dz := -rng; dz <= rng+extra; dz++ {
			if skip.skipSigned(t.rand, dx, y, dz, rng, large) {
				continue
			}
			t.tryPlaceLeaf(Point{pos.X + dx, pos.Y + y, pos.Z + dz})
		}
	}
}

// placeLeavesRowHanging is placeLeavesRowWithHangingLeavesBelow: the
// row, then along each side of the layer below it leaves hang under
// placed ones by chance, a second block further by the extension chance.
func (t *tree) placeLeavesRowHanging(skip leafSkip, pos Point, rng, y int32, large bool, chance, extension float32) {
	t.placeLeavesRow(skip, pos, rng, y, large)
	extra := int32(0)
	if large {
		extra = 1
	}
	below := pos.offset(dirDown)
	for _, d := range horizontalDirs {
		cw := clockwise[d]
		off := rng
		if cw == dirEast || cw == dirSouth {
			off = rng + extra
		}
		m := Point{pos.X, pos.Y + y - 1, pos.Z}
		m = m.add(scaleDir(cw, off)).add(scaleDir(d, -rng))
		for i := -rng; i < rng+extra; i++ {
			if t.leaves.contains(m.offset(dirUp)) && t.tryPlaceExtension(chance, below, m) {
				t.tryPlaceExtension(extension, below, m.offset(dirDown))
			}
			m = m.offset(d)
		}
	}
}

func scaleDir(d int, n int32) Point {
	o := dirOffsets[d]
	return Point{o[0] * n, o[1] * n, o[2] * n}
}

func (t *tree) tryPlaceExtension(chance float32, origin, p Point) bool {
	if manhattan(p, origin) >= 7 {
		return false
	}
	if t.rand.NextFloat() > chance {
		return false
	}
	return t.tryPlaceLeaf(p)
}

// tryPlaceLeaf places a foliage block at p unless the block there is
// persistent or not a valid tree position; waterlogging follows source
// water there.
func (t *tree) tryPlaceLeaf(p Point) bool {
	cur := t.r.get(p)
	if cur.prop("persistent") == "true" || !validTreePos(t.r, p) {
		return false
	}
	s := t.cfg.foliage.get(t.r, t.rand, p)
	if s.hasProp("waterlogged") {
		s = s.with("waterlogged", boolStr(cur.isWaterFluid() && cur.isSourceFluid()))
	}
	t.setLeaf(p, s)
	return true
}

type blobFoliage struct {
	foliageBase
	height int32
}

func (b blobFoliage) foliageHeight(Random, int32, *treeConfig) int32 { return b.height }

func blobSkip(rand Random, dx, y, dz, rng int32, _ bool) bool {
	return dx == rng && dz == rng && (rand.NextIntn(2) == 0 || y == 0)
}

func (b blobFoliage) createFoliage(t *tree, _ int32, a foliageAttachment, foliageHeight, radius int32) {
	offset := b.offset.sample(t.rand)
	for y := offset; y >= offset-foliageHeight; y-- {
		r := max32(radius+a.radiusOffset-1-y/2, 0)
		t.placeLeavesRow(absSkip(blobSkip), a.pos, r, y, a.doubleTrunk)
	}
}

type twoLayers struct {
	limit, lower, upper int32
	clip                int32
	hasClip             bool
}

func (s twoLayers) sizeAtHeight(_, y int32) int32 {
	if y < s.limit {
		return s.lower
	}
	return s.upper
}

func (s twoLayers) minClippedHeight() (int32, bool) { return s.clip, s.hasClip }

type threeLayers struct {
	limit, upperLimit, lower, middle, upper int32
	clip                                    int32
	hasClip                                 bool
}

func (s threeLayers) sizeAtHeight(height, y int32) int32 {
	if y < s.limit {
		return s.lower
	}
	if y >= height-s.upperLimit {
		return s.upper
	}
	return s.middle
}

func (s threeLayers) minClippedHeight() (int32, bool) { return s.clip, s.hasClip }

func decodeFeatureSize(v any) featureSize {
	clip, hasClip := int32(0), fhas(v, "min_clipped_height")
	if hasClip {
		clip = fint(ffield(v, "min_clipped_height"), 0)
	}
	get := func(key string, def int32) int32 {
		if fhas(v, key) {
			return fint(ffield(v, key), def)
		}
		return def
	}
	switch ftype(v) {
	case "minecraft:two_layers_feature_size":
		return twoLayers{get("limit", 1), get("lower_size", 0), get("upper_size", 1), clip, hasClip}
	case "minecraft:three_layers_feature_size":
		return threeLayers{get("limit", 1), get("upper_limit", 1), get("lower_size", 0), get("middle_size", 1), get("upper_size", 1), clip, hasClip}
	}
	unsupported("feature size %s", ftype(v))
	return nil
}

func init() {
	treeTrunkPlacers["minecraft:straight_trunk_placer"] = func(cfg any) trunkPlacer { return straightTrunk{decodeTrunkHeights(cfg)} }
	treeFoliagePlacers["minecraft:blob_foliage_placer"] = func(cfg any) foliagePlacer {
		return blobFoliage{decodeFoliageBase(cfg), fint(ffield(cfg, "height"), 0)}
	}
}

// leafDistance is LeavesBlock.getOptionalDistanceAt: 0 for blocks that
// keep leaves alive, a leaf's distance, nothing for anything else.
func leafDistance(s StateID) (int32, bool) {
	if s.inTag("prevents_nearby_leaf_decay") {
		return 0, true
	}
	if v := s.prop("distance"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return int32(n), true
		}
	}
	return 0, false
}

// updateLeaves is TreeFeature.updateLeaves: from the logs (distance 0)
// outward, each leaf takes the layer it is first popped from, layers
// held in position sets popped first element first; decoration and root
// positions block the walk. The returned shape is every cell visited.
func updateLeaves(t *tree, shape *voxelBox) *voxelBox {
	t.decorations.each(func(p Point) {
		if shape.inside(p) {
			shape.fill(p)
		}
	})
	t.roots.each(func(p Point) {
		if shape.inside(p) {
			shape.fill(p)
		}
	})
	var layers [7]posSet
	t.logs.each(func(p Point) { layers[0].add(p) })
	i := int32(0)
	for {
		for i < 7 && layers[i].size == 0 {
			i++
		}
		if i >= 7 {
			break
		}
		p, _ := layers[i].pop()
		if !shape.inside(p) {
			continue
		}
		if i != 0 {
			t.r.set(p, t.r.get(p).with("distance", itoa(i)))
		}
		shape.fill(p)
		for d := 0; d < 6; d++ {
			q := p.offset(d)
			if !shape.inside(q) || shape.isFull(q) {
				continue
			}
			dist, ok := leafDistance(t.r.get(q))
			if !ok {
				continue
			}
			k := min32(dist, i+1)
			if k < 7 {
				layers[k].add(q)
				i = min32(i, k)
			}
		}
	}
	return shape
}

// sortedByY is the set's iteration order stably sorted by Y, as the
// decorator context lists its logs, leaves and roots.
func sortedByY(s *posSet) []Point {
	l := s.list()
	sort.SliceStable(l, func(a, b int) bool { return l[a].Y < l[b].Y })
	return l
}
