package worldgen

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/world"
)

// treeConfig is TreeConfiguration.
type treeConfig struct {
	trunkProvider, foliageProvider, belowTrunk stateProvider
	trunk                                      trunkPlacer
	foliage                                    foliagePlacer
	root                                       *mangroveRootPlacer
	size                                       featureSize
	decorators                                 []treeDecorator
	ignoreVines                                bool
}

// featureSize is TwoLayersFeatureSize / ThreeLayersFeatureSize.
type featureSize struct {
	three                                   bool
	limit, upperLimit, lower, middle, upper int32
	minClipped                              int32
	hasMinClipped                           bool
}

func parseFeatureSize(raw json.RawMessage) featureSize {
	var o obj
	jsonUnmarshal(raw, &o)
	opt := func(k string, def int32) int32 {
		if _, ok := o[k]; ok {
			return o.i32(k)
		}
		return def
	}
	fs := featureSize{three: trimNS(o.str("type")) == "three_layers_feature_size"}
	fs.limit = opt("limit", 1)
	fs.upperLimit = opt("upper_limit", 1)
	fs.lower = opt("lower_size", 0)
	fs.middle = opt("middle_size", 1)
	fs.upper = opt("upper_size", 1)
	if _, ok := o["min_clipped_height"]; ok {
		fs.hasMinClipped = true
		fs.minClipped = o.i32("min_clipped_height")
	}
	return fs
}

// sizeAtHeight is FeatureSize.getSizeAtHeight.
func (fs featureSize) sizeAtHeight(treeHeight, y int32) int32 {
	if fs.three {
		if y < fs.limit {
			return fs.lower
		}
		if y >= treeHeight-fs.upperLimit {
			return fs.upper
		}
		return fs.middle
	}
	if y < fs.limit {
		return fs.lower
	}
	return fs.upper
}

// tagTable caches a block tag as a per-state lookup.
var tagTables sync.Map // tag -> []bool

func stateInTag(b world.Block, tag string) bool {
	v, ok := tagTables.Load(tag)
	if !ok {
		set := blockTagSet(tag)
		t := make([]bool, world.StateCount())
		for i := range t {
			n, _ := world.StateOwner(world.Block(i))
			t[i] = set[n]
		}
		v, _ = tagTables.LoadOrStore(tag, t)
	}
	t := v.([]bool)
	return int(b) < len(t) && t[b]
}

// withProp is BlockState.trySetValue: unchanged when the block lacks
// the property.
func withProp(b world.Block, prop, val string) world.Block {
	if nb, ok := world.WithProperty(b, prop, val); ok {
		return nb
	}
	return b
}

func propIs(b world.Block, prop, val string) bool {
	v, ok := world.StateProperty(b, prop)
	return ok && v == val
}

// validTreePos is TreeFeature.validTreePos: air or replaceable_by_trees.
func validTreePos(rg *Region, p BlockPos) bool {
	b := rg.Block(p[0], p[1], p[2])
	return isAir(b) || stateInTag(b, "replaceable_by_trees")
}

func isAirAt(rg *Region, p BlockPos) bool { return isAir(rg.Block(p[0], p[1], p[2])) }

func blockAt(rg blockGetter, p BlockPos) world.Block { return rg.Block(p[0], p[1], p[2]) }

func relDir(p BlockPos, d int, n int32) BlockPos {
	o := dirOffsets[d]
	return BlockPos{p[0] + o[0]*n, p[1] + o[1]*n, p[2] + o[2]*n}
}

func stepX(d int) int32 { return dirOffsets[d][0] }
func stepZ(d int) int32 { return dirOffsets[d][2] }

// treeclockWise is Direction.getClockWise for horizontal directions.
func treeClockWise(d int) int {
	switch d {
	case dirNorth:
		return dirEast
	case dirEast:
		return dirSouth
	case dirSouth:
		return dirWest
	}
	return dirNorth
}

func axisOf(d int) string {
	switch d {
	case dirWest, dirEast:
		return "x"
	case dirNorth, dirSouth:
		return "z"
	}
	return "y"
}

func distManhattan(a, b BlockPos) int32 {
	return absI(a[0]-b[0]) + absI(a[1]-b[1]) + absI(a[2]-b[2])
}

func absI(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// randomHorizontal is Direction.Plane.HORIZONTAL.getRandomDirection.
func randomHorizontal(r Random) int { return horizontalDirs[r.NextIntN(4)] }

// treeCtx carries one TreeFeature.place call: the region, the random,
// and the root/trunk/foliage/decoration position sets the setters fill.
type treeCtx struct {
	g      *Generator
	rg     *Region
	r      Random
	cfg    *treeConfig
	roots  *jset
	logs   *jset
	leaves *jset
	decos  map[BlockPos]bool
	decoL  []BlockPos
}

// The four setters TreeFeature.place builds: record the position, then
// setBlock with flags 19 (the record happens even when the write zone
// refuses the block).
func (t *treeCtx) setRoot(p BlockPos, b world.Block) { t.roots.add(p); t.rg.set(p, b) }
func (t *treeCtx) setLog(p BlockPos, b world.Block)  { t.logs.add(p); t.rg.set(p, b) }
func (t *treeCtx) setLeaf(p BlockPos, b world.Block) { t.leaves.add(p); t.rg.set(p, b) }
func (t *treeCtx) setDeco(p BlockPos, b world.Block) {
	if !t.decos[p] {
		t.decos[p] = true
		t.decoL = append(t.decoL, p)
	}
	t.rg.set(p, b)
}

func parseTreeConfig(g *Generator, cfg obj) *treeConfig {
	tc := &treeConfig{
		trunkProvider:   g.compileStateProvider(cfg["trunk_provider"]),
		foliageProvider: g.compileStateProvider(cfg["foliage_provider"]),
		belowTrunk:      g.compileStateProvider(cfg["below_trunk_provider"]),
		trunk:           parseTrunkPlacer(cfg["trunk_placer"]),
		foliage:         parseFoliagePlacer(cfg["foliage_placer"]),
		size:            parseFeatureSize(cfg["minimum_size"]),
	}
	if raw, ok := cfg["root_placer"]; ok {
		tc.root = parseRootPlacer(g, raw)
	}
	if raw, ok := cfg["ignore_vines"]; ok {
		jsonUnmarshal(raw, &tc.ignoreVines)
	}
	if raw, ok := cfg["decorators"]; ok {
		tc.decorators = parseDecorators(g, raw)
	}
	return tc
}

func init() {
	featureBuilders["tree"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		tc := parseTreeConfig(g, cfg)
		return func(c *featCtx, r Random, p BlockPos) bool {
			return tc.place(c, r, p)
		}
	}
}

// place is TreeFeature.place.
func (tc *treeConfig) place(c *featCtx, r Random, origin BlockPos) bool {
	t := &treeCtx{g: c.g, rg: c.rg, r: r, cfg: tc, roots: newJSet(), logs: newJSet(), leaves: newJSet(), decos: map[BlockPos]bool{}}
	if !tc.doPlace(t, origin) || (t.logs.n == 0 && t.leaves.n == 0) {
		return false
	}
	if len(tc.decorators) > 0 {
		dc := newDecoCtx(t.g, t.rg, r, t.setDeco, t.logs.list(), t.leaves.list(), t.roots.list())
		for _, d := range tc.decorators {
			d.place(dc)
		}
	}
	all := [][]BlockPos{t.roots.list(), t.logs.list(), t.leaves.list(), t.decoL}
	box, ok := encapsulate(all...)
	if !ok {
		return false
	}
	shape := t.updateLeaves(box)
	updateShapeAtEdge(t.rg, shape)
	return true
}

// doPlace is TreeFeature.doPlace.
func (tc *treeConfig) doPlace(t *treeCtx, origin BlockPos) bool {
	r := t.r
	height := tc.trunk.treeHeight(r)
	fh := tc.foliage.foliageHeight(r, height)
	fr := tc.foliage.foliageRadius(r, height-fh)
	trunkOrigin := origin
	if tc.root != nil {
		trunkOrigin = tc.root.trunkOrigin(origin, r)
	}
	minY := min(origin[1], trunkOrigin[1])
	maxY := max(origin[1], trunkOrigin[1]) + height + 1
	if minY < t.rg.MinY()+1 || maxY > t.rg.MaxY()+1 {
		return false
	}
	free := tc.maxFreeTreeHeight(t, height, trunkOrigin)
	if free < height && (!tc.size.hasMinClipped || free < tc.size.minClipped) {
		return false
	}
	if tc.root != nil && !tc.root.placeRoots(t, origin, trunkOrigin) {
		return false
	}
	for _, att := range tc.trunk.placeTrunk(t, free, trunkOrigin) {
		createFoliage(tc.foliage, t, free, att, fh, fr)
	}
	return true
}

// maxFreeTreeHeight is TreeFeature.getMaxFreeTreeHeight.
func (tc *treeConfig) maxFreeTreeHeight(t *treeCtx, height int32, origin BlockPos) int32 {
	for y := int32(0); y <= height+1; y++ {
		s := tc.size.sizeAtHeight(height, y)
		for dx := -s; dx <= s; dx++ {
			for dz := -s; dz <= s; dz++ {
				p := offset(origin, dx, y, dz)
				if !trunkIsFree(tc.trunk, t.rg, p) || (!tc.ignoreVines && isBlock(blockAt(t.rg, p), "vine")) {
					return y - 2
				}
			}
		}
	}
	return height
}

// tbox is a BoundingBox.
type tbox struct{ minX, minY, minZ, maxX, maxY, maxZ int32 }

func encapsulate(lists ...[]BlockPos) (tbox, bool) {
	var b tbox
	first := true
	for _, l := range lists {
		for _, p := range l {
			if first {
				b = tbox{p[0], p[1], p[2], p[0], p[1], p[2]}
				first = false
				continue
			}
			b.minX, b.minY, b.minZ = min(b.minX, p[0]), min(b.minY, p[1]), min(b.minZ, p[2])
			b.maxX, b.maxY, b.maxZ = max(b.maxX, p[0]), max(b.maxY, p[1]), max(b.maxZ, p[2])
		}
	}
	return b, !first
}

func (b tbox) inside(p BlockPos) bool {
	return p[0] >= b.minX && p[0] <= b.maxX && p[1] >= b.minY && p[1] <= b.maxY && p[2] >= b.minZ && p[2] <= b.maxZ
}

// voxShape is the BitSetDiscreteVoxelShape spanning a tree's box.
type voxShape struct {
	box        tbox
	sx, sy, sz int32
	bits       []bool
}

func newVoxShape(b tbox) *voxShape {
	s := &voxShape{box: b, sx: b.maxX - b.minX + 1, sy: b.maxY - b.minY + 1, sz: b.maxZ - b.minZ + 1}
	s.bits = make([]bool, s.sx*s.sy*s.sz)
	return s
}

func (s *voxShape) idx(x, y, z int32) int32 { return (x*s.sy+y)*s.sz + z }

func (s *voxShape) fillAt(p BlockPos) {
	s.bits[s.idx(p[0]-s.box.minX, p[1]-s.box.minY, p[2]-s.box.minZ)] = true
}

func (s *voxShape) full(x, y, z int32) bool {
	if x < 0 || y < 0 || z < 0 || x >= s.sx || y >= s.sy || z >= s.sz {
		return false
	}
	return s.bits[s.idx(x, y, z)]
}

// leafDistance is LeavesBlock.getOptionalDistanceAt: 0 for
// prevents_nearby_leaf_decay (the logs), the distance property of
// leaves, none otherwise.
func leafDistance(b world.Block) (int32, bool) {
	if stateInTag(b, "prevents_nearby_leaf_decay") {
		return 0, true
	}
	if !isLeaves(b) {
		return 0, false
	}
	v, ok := world.StateProperty(b, "distance")
	if !ok {
		return 0, false
	}
	n, _ := strconv.Atoi(v)
	return int32(n), true
}

// updateLeaves is TreeFeature.updateLeaves: decorations and roots are
// pre-filled into the shape, the logs seed distance layer 0, and the
// layers are drained lowest-first, each popped position (in HashSet
// order) taking its layer as its leaf distance. Measured quirks kept:
// a position re-queued in a higher layer before it pops is popped (and
// re-written) again from that layer, and the current layer index drops
// mid-scan when a lower-distance neighbour is found.
func (t *treeCtx) updateLeaves(box tbox) *voxShape {
	shape := newVoxShape(box)
	var layers [7]*jset
	for i := range layers {
		layers[i] = newJSet()
	}
	for _, p := range t.decoL {
		if box.inside(p) {
			shape.fillAt(p)
		}
	}
	for _, p := range t.roots.list() {
		if box.inside(p) {
			shape.fillAt(p)
		}
	}
	for _, p := range t.logs.list() {
		layers[0].add(p)
	}
	i := int32(0)
	for {
		for i < 7 && layers[i].n == 0 {
			i++
		}
		if i >= 7 {
			break
		}
		p := layers[i].first()
		layers[i].remove(p)
		if !box.inside(p) {
			continue
		}
		if i != 0 {
			b := blockAt(t.rg, p)
			t.rg.set(p, withProp(b, "distance", strconv.Itoa(int(i))))
		}
		shape.fillAt(p)
		for d := 0; d < 6; d++ {
			q := relDir(p, d, 1)
			if !box.inside(q) {
				continue
			}
			if shape.full(q[0]-box.minX, q[1]-box.minY, q[2]-box.minZ) {
				continue
			}
			dist, ok := leafDistance(blockAt(t.rg, q))
			if !ok {
				continue
			}
			k := min(dist, i+1)
			if k < 7 {
				layers[k].add(q)
				i = min(i, k)
			}
		}
	}
	return shape
}

// updateShapeAtEdge is StructureTemplate.updateShapeAtEdge over
// DiscreteVoxelShape.forAllFaces: the z-axis faces (x, y outer), then
// the y-axis faces (z, x outer), then the x-axis faces (y, z outer);
// each face updates the inside block against its outside neighbour and
// then the neighbour against the (possibly updated) inside block.
func updateShapeAtEdge(rg *Region, s *voxShape) {
	face := func(dir int, x, y, z int32) {
		p := BlockPos{s.box.minX + x, s.box.minY + y, s.box.minZ + z}
		q := relDir(p, dir, 1)
		st := blockAt(rg, p)
		nb := blockAt(rg, q)
		ns := treeUpdateShape(rg, st, p, dir, q, nb)
		if ns != st {
			rg.set(p, ns)
		}
		nn := treeUpdateShape(rg, nb, q, oppositeDir(dir), p, ns)
		if nn != nb {
			rg.set(q, nn)
		}
	}
	// z faces
	for x := int32(0); x < s.sx; x++ {
		for y := int32(0); y < s.sy; y++ {
			prev := false
			for z := int32(0); z <= s.sz; z++ {
				cur := z != s.sz && s.full(x, y, z)
				if !prev && cur {
					face(dirNorth, x, y, z)
				}
				if prev && !cur {
					face(dirSouth, x, y, z-1)
				}
				prev = cur
			}
		}
	}
	// y faces
	for z := int32(0); z < s.sz; z++ {
		for x := int32(0); x < s.sx; x++ {
			prev := false
			for y := int32(0); y <= s.sy; y++ {
				cur := y != s.sy && s.full(x, y, z)
				if !prev && cur {
					face(dirDown, x, y, z)
				}
				if prev && !cur {
					face(dirUp, x, y-1, z)
				}
				prev = cur
			}
		}
	}
	// x faces
	for y := int32(0); y < s.sy; y++ {
		for z := int32(0); z < s.sz; z++ {
			prev := false
			for x := int32(0); x <= s.sx; x++ {
				cur := x != s.sx && s.full(x, y, z)
				if !prev && cur {
					face(dirWest, x, y, z)
				}
				if prev && !cur {
					face(dirEast, x-1, y, z)
				}
				prev = cur
			}
		}
	}
}

// sortByY is ObjectArrayList.sort(comparingInt(getY)) (a stable sort).
func sortByY(l []BlockPos) []BlockPos {
	out := append([]BlockPos(nil), l...)
	sort.SliceStable(out, func(i, j int) bool { return out[i][1] < out[j][1] })
	return out
}

// parseHolderBlocks parses a HolderSet<Block> into a membership test.
func parseHolderBlocks(raw json.RawMessage) func(world.Block) bool {
	set := blockNameSet(raw)
	return func(b world.Block) bool { return inTag(b, set) }
}

// parseUniformNoType reads a UniformInt given without a type field.
func parseUniformNoType(raw json.RawMessage) intProvider {
	var o obj
	jsonUnmarshal(raw, &o)
	if _, ok := o["type"]; ok {
		return parseIntProvider(raw)
	}
	lo, hi := o.i32("min_inclusive"), o.i32("max_inclusive")
	return func(r Random) int32 { return randBetweenInclusive(r, lo, hi) }
}

func parseDirList(raw json.RawMessage) []int {
	var names []string
	jsonUnmarshal(raw, &names)
	out := make([]int, len(names))
	for i, n := range names {
		out[i] = dirNames[strings.ToLower(n)]
	}
	return out
}
