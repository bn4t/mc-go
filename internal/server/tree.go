package server

import (
	"mc-go/internal/world"
	"strconv"
	"strings"
)

// Tree growth — SaplingBlock.randomTick / advanceTree /
// TreeGrower.growTree with TreeFeature.doPlace for the straight-trunk
// blob-canopy species (small oak, birch). Measured against the 26.2
// jar: SaplingBlock.java, TreeGrower.java, TreeFeature.java,
// TrunkPlacer.java, StraightTrunkPlacer.java, FoliagePlacer.java,
// BlobFoliagePlacer.java, and the oak.json / birch.json configured
// features.

// canopyKind is the species' foliage_placer variant: the small oak /
// birch blob or the spruce zigzag cone.
type canopyKind int

const (
	canopyBlob canopyKind = iota
	canopySpruce
)

// treeShape is one species' single-trunk feature: a straight log column
// of baseHeight + intn(randA+1) + intn(randB+1), the two-layers space
// check (minLimit/minUpper), and one canopy placer. Every small oak and
// birch shares foliage_placer {radius: 2, offset: 0, height: 3}; spruce
// runs SpruceFoliagePlacer {radius U(2..3), offset U(0..2),
// trunk_height U(1..2)}.
type treeShape struct {
	log, leaf                string
	baseHeight, randA, randB int
	fancyChance              float64
	minLimit, minUpper       int // two_layers_feature_size: y < limit → 0, else upper
	canopy                   canopyKind
	jungleDecor              bool // the {cocoa, trunk_vine, leave_vine} decorator set
}

// treeShapes maps each implemented sapling to its feature config.
// Saplings without an entry still advance their stage property (that
// half of advanceTree is species-agnostic) but their growTree finds no
// feature and fails — jungle/acacia/dark-oak/mangrove/cherry/pale-oak
// placers are a measured gap, as is oak's own 10% fancy roll
// (FancyTrunkPlacer + FancyFoliagePlacer): the roll still happens and
// fails like vanilla failing a space check, so under low ceilings the
// absence is invisible and in the open it is the known gap.
var treeShapes = map[string]treeShape{
	"oak_sapling":    {"oak_log", "oak_leaves", 4, 2, 0, 0.1, 1, 1, canopyBlob, false},
	"birch_sapling":  {"birch_log", "birch_leaves", 5, 2, 0, 0, 1, 1, canopyBlob, false},
	"spruce_sapling": {"spruce_log", "spruce_leaves", 5, 2, 1, 0, 2, 2, canopySpruce, false},
	"jungle_sapling": {"jungle_log", "jungle_leaves", 4, 8, 0, 0, 1, 1, canopyBlob, false},
}

// saplingRule — SaplingBlock.randomTick: getMaxLocalRawBrightness of
// the cell above at least 9 (raw sky minus skyDarken, or the block
// light grid — daytime open sky is the bright case) and a 1-in-7 roll.
func saplingRule(s *Server, d *dimension, pos world.Pos, blk world.Block) {
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if s.saplingLight(d, above) < 9 || s.rng.intn(7) != 0 {
		return
	}
	s.advanceTree(d, pos, blk)
}

// saplingLight is getMaxLocalRawBrightness: the brighter of the
// block-light grid cell and the propagated sky value minus skyDarken.
// The sky half reads the engine's BFS grid, so a cell shaded by a
// single floating block still floods in at ~14 from the open columns
// beside it — vanilla's propagation, which a line-of-sight check
// cannot see. skyDarken is the unweathered reading: 0 by day, 4 by
// night — a 15 cell still clears the 9 the rule needs in the dark.
func (s *Server) saplingLight(d *dimension, pos world.Pos) int {
	v := int(d.blockLight[pos])
	if d.skylight {
		if sky := skyEffective(d, pos) - s.skyDarken(); sky > v {
			v = sky
		}
	}
	return v
}

// skyDarken is vanilla's ambient darkness: 0 through the day and 4
// through the night — the flat edges of the real celestial-angle
// curve, with its dawn/dusk ramp folded into the ends. Weather adds
// on top in vanilla and is unmodelled here.
func (s *Server) skyDarken() int {
	dt := s.dayTime() % 24000
	if dt < 0 {
		dt += 24000
	}
	if dt >= 13000 && dt < 23000 {
		return 4
	}
	return 0
}

// advanceTree — SaplingBlock.advanceTree: stage 0 cycles to 1, stage 1
// runs the tree feature. The stage write is silent in vanilla (flags
// 260 — no neighbour updates); setSilentBlock mirrors that.
func (s *Server) advanceTree(d *dimension, pos world.Pos, blk world.Block) {
	if st, ok := world.StateProperty(blk, "stage"); ok && st == "0" {
		if nb, ok := world.WithProperty(blk, "stage", "1"); ok {
			s.setSilentBlock(d, pos, nb)
		}
		return
	}
	s.growTree(d, pos, blk)
}

// growTree — TreeGrower.growTree + TreeFeature.doPlace: pick the
// species' feature (oak rolls fancy at 10%, which has no placer yet),
// clear the sapling, space-check, write the trunk, the blob canopy and
// the dirt-under-trunk, then BFS the leaf distances like updateLeaves.
func (s *Server) growTree(d *dimension, pos world.Pos, blk world.Block) {
	name, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	shape, ok := treeShapes[strings.TrimPrefix(name, "minecraft:")]
	if !ok {
		return
	}
	// getConfiguredFeature: rand.nextFloat() < secondaryChance picks the
	// secondary feature — oak's fancy tree. It has no placer yet and
	// fails like a failed place().
	if s.rng.nextDouble() < shape.fancyChance {
		return
	}

	// growTree clears the sapling before the feature sees the world,
	// so the origin cell reads air for the whole place.
	s.setSilentBlock(d, pos, world.Air)

	treeHeight := shape.baseHeight + s.rng.intn(shape.randA+1) + s.rng.intn(shape.randB+1)

	// doPlace's bounds gate: the feature refuses to run when the
	// trunk's own origin or its top would sit outside the world's
	// buildable range.
	if pos.Y < d.minY+1 || pos.Y+treeHeight+1 > d.minY+d.height {
		s.setSilentBlock(d, pos, blk)
		return
	}

	// getMaxFreeTreeHeight with two_layers_feature_size:
	// getSizeAtHeight is lowerSize(0) below the limit and upperSize at
	// it — oak/birch check the trunk column at y=0 and the whole 3x3
	// from y=1 up; spruce widens to the whole 5x5 from y=2 up.
	clipped := treeHeight
	for y := 0; y <= treeHeight+1; y++ {
		r := shape.minUpper
		if y < shape.minLimit {
			r = 0
		}
		if !s.treeSpaceFree(d, pos, y, r) {
			clipped = y - 2
			break
		}
	}
	if clipped < treeHeight {
		s.setSilentBlock(d, pos, blk) // failed place: vanilla restores the sapling
		return
	}

	// placeBelowTrunkBlock: the provider writes dirt under the trunk
	// when the below block isn't already in
	// cannot_replace_below_tree_trunk — grass/mycelium/regular blocks
	// under a tree turn to dirt.
	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if bn, _ := world.StateOwner(d.w.GetBlock(below)); !dirtTagName(strings.TrimPrefix(bn, "minecraft:")) {
		if dirt, ok := world.BlockStateOf("dirt", nil); ok {
			s.setNaturalBlock(d, below, dirt)
		}
	}

	// placeTrunk: each trunk cell only writes when validTreePos — a
	// log (free for space but not a placement target) truncates the
	// visible column without failing the tree. The written cells are
	// collected — the decorators' context.logs() reads them.
	logState, _ := world.BlockStateOf(shape.log, map[string]string{"axis": "y"})
	var logs []world.Pos
	for y := 0; y < treeHeight; y++ {
		p := world.Pos{X: pos.X, Y: pos.Y + y, Z: pos.Z}
		if s.treeCanOccupy(d, p) {
			s.setNaturalBlock(d, p, logState)
			logs = append(logs, p)
		}
	}

	// createFoliage: the attachment sits one above the top log.
	top := world.Pos{X: pos.X, Y: pos.Y + treeHeight, Z: pos.Z}
	var leaves []world.Pos
	leafState, _ := world.BlockStateOf(shape.leaf, map[string]string{
		"distance": "7", "persistent": "false", "waterlogged": "false",
	})
	placeLeaf := func(p world.Pos) {
		// tryPlaceLeaf: never overwrites persistent leaves or anything
		// outside REPLACEABLE_BY_TREES (logs included).
		cur := d.w.GetBlock(p)
		if pv, ok := world.StateProperty(cur, "persistent"); ok && pv == "true" {
			return
		}
		if !s.treeCanOccupy(d, p) {
			return
		}
		st := leafState
		// The foliage provider folds waterlogged from the cell's
		// fluid — a water source waterlogs the leaf.
		if k, _, ok := fluidOf(cur); ok && k == fluidWater {
			if nb, ok := world.WithProperty(st, "waterlogged", "true"); ok {
				st = nb
			}
		}
		s.setNaturalBlock(d, p, st)
		leaves = append(leaves, p)
	}
	switch shape.canopy {
	case canopySpruce:
		s.spruceCanopy(d, top, treeHeight, placeLeaf)
	default:
		// blob (radius 2, offset 0, height 3): four layers at yo
		// 0..-3 with radius max(2 - 1 - yo/2, 0) → 1,1,2,2 (Java's
		// yo/2 truncates toward zero; Go's integer division does too).
		for yo := 0; yo >= -3; yo-- {
			r := 2 - 1 - yo/2
			if r < 0 {
				r = 0
			}
			for dx := -r; dx <= r; dx++ {
				for dz := -r; dz <= r; dz++ {
					// shouldSkipLocation: the corner of the square,
					// always skipped at yo=0 and half the time elsewhere.
					if absi(dx) == r && absi(dz) == r && (s.rng.intn(2) == 0 || yo == 0) {
						continue
					}
					placeLeaf(world.Pos{X: top.X + dx, Y: top.Y + yo, Z: top.Z + dz})
				}
			}
		}
	}

	// Tree decorators run after foliage placement and before
	// updateLeaves. The set exists for jungle_tree-shaped features —
	// vanilla's own 1x1 jungle sapling grows jungle_tree_no_vine
	// (decorators: []) so no shape entry currently sets the flag; it
	// is wired for the 2x2 mega_jungle path (trunk_vine + leave_vine).
	if shape.jungleDecor {
		s.jungleDecorators(d, logs, leaves)
	}

	// updateLeaves: BFS from every log inside the bounding box assigns
	// DISTANCE=min(neighbour+1, 7) on the leaf cells at once — the same
	// result the leaf distance ticks would converge to over ticks.
	s.treeLeafDistances(d, pos, treeHeight, leaves)
}

// spruceCanopy — SpruceFoliagePlacer.createFoliage: the zigzag cone.
// foliageHeight = max(4, treeHeight - U(1..2)) and leafRadius = U(2..3)
// are sampled up front; the row loop runs yo = offset(U(0..2)) down to
// -foliageHeight. The row radius starts at U(0..1), counts up while
// under maxRadius, then drops to the running minimum while maxRadius
// grows toward the sampled leaf radius — the classic spruce spire.
// Corners are skipped whenever the radius is positive (there is no
// random corner roll like the blob's).
func (s *Server) spruceCanopy(d *dimension, top world.Pos, treeHeight int, placeLeaf func(world.Pos)) {
	fH := treeHeight - 1 - s.rng.intn(2) // treeHeight - U(1..2), floored at 4
	if fH < 4 {
		fH = 4
	}
	leafR := 2 + s.rng.intn(2)
	off := s.rng.intn(3)
	cur, maxR, minR := s.rng.intn(2), 1, 0
	for yo := off; yo >= -fH; yo-- {
		for dx := -cur; dx <= cur; dx++ {
			for dz := -cur; dz <= cur; dz++ {
				if absi(dx) == cur && absi(dz) == cur && cur > 0 {
					continue
				}
				placeLeaf(world.Pos{X: top.X + dx, Y: top.Y + yo, Z: top.Z + dz})
			}
		}
		if cur >= maxR {
			cur = minR
			minR = 1
			maxR = min(maxR+1, leafR)
		} else {
			cur++
		}
	}
}

// vineDir is one horizontal neighbor cell and the vine face written
// there so the vine attaches back toward the source cell.
type vineDir struct {
	dx, dz int
	face   string
}

// trunkVineDirs — the four directions both TrunkVineDecorator and
// LeaveVineDecorator walk (west, east, north, south in code order).
var trunkVineDirs = [4]vineDir{
	{-1, 0, "east"}, {1, 0, "west"}, {0, -1, "south"}, {0, 1, "north"},
}

// cocoaDirs — Plane.HORIZONTAL order (north, south, west, east): the
// pod lands on the opposite side of the direction and faces back.
var cocoaDirs = [4]vineDir{
	{0, -1, "north"}, {0, 1, "south"}, {-1, 0, "west"}, {1, 0, "east"},
}

// jungleDecorators — the jungle decorator family: CocoaDecorator
// (0.2 tree gate + 0.25 per bottom-log face — jungle_tree only,
// which no sapling reaches in 26.2), TrunkVineDecorator (nextInt(3)>0
// per log face), LeaveVineDecorator (0.25 per leaf face, hanging down
// through air up to 4). The 2x2 mega_jungle_tree feature uses the two
// vine decorators; jungle_tree adds cocoa.
func (s *Server) jungleDecorators(d *dimension, logs, leaves []world.Pos) {
	isAir := func(p world.Pos) bool {
		n, _ := world.StateOwner(d.w.GetBlock(p))
		return n == "minecraft:air" || n == "minecraft:cave_air" || n == "minecraft:void_air"
	}
	placeVine := func(p world.Pos, face string) {
		if v, ok := world.BlockStateOf("vine", map[string]string{face: "true"}); ok {
			s.setNaturalBlock(d, p, v)
		}
	}

	// CocoaDecorator: one probability gate for the whole tree, then
	// every log within three of the base rolls 0.25 per direction.
	if s.rng.nextDouble() < 0.2 && len(logs) > 0 {
		treeY := logs[0].Y
		for _, lp := range logs {
			if lp.Y-treeY > 2 {
				continue
			}
			for _, cd := range cocoaDirs {
				if s.rng.nextDouble() <= 0.25 {
					cp := world.Pos{X: lp.X - cd.dx, Y: lp.Y, Z: lp.Z - cd.dz}
					if isAir(cp) {
						if c, ok := world.BlockStateOf("cocoa", map[string]string{
							"age": strconv.Itoa(s.rng.intn(3)), "facing": cd.face}); ok {
							s.setNaturalBlock(d, cp, c)
						}
					}
				}
			}
		}
	}

	// TrunkVineDecorator: each direction rolls nextInt(3) > 0.
	for _, lp := range logs {
		for _, vd := range trunkVineDirs {
			if s.rng.intn(3) > 0 {
				vp := world.Pos{X: lp.X + vd.dx, Y: lp.Y, Z: lp.Z + vd.dz}
				if isAir(vp) {
					placeVine(vp, vd.face)
				}
			}
		}
	}

	// LeaveVineDecorator: 0.25 per leaf per direction, then the vine
	// hangs downward through air up to four more cells.
	for _, lf := range leaves {
		for _, vd := range trunkVineDirs {
			if s.rng.nextDouble() < 0.25 {
				vp := world.Pos{X: lf.X + vd.dx, Y: lf.Y, Z: lf.Z + vd.dz}
				if isAir(vp) {
					placeVine(vp, vd.face)
					down := world.Pos{X: vp.X, Y: vp.Y - 1, Z: vp.Z}
					for i := 0; i < 4 && isAir(down); i++ {
						placeVine(down, vd.face)
						down.Y--
					}
				}
			}
		}
	}
}

// treeSpaceFree runs one layer of getMaxFreeTreeHeight: every cell in
// the ±r box at (pos + y) must satisfy isFree — validTreePos (air or
// REPLACEABLE_BY_TREES) or a log.
func (s *Server) treeSpaceFree(d *dimension, pos world.Pos, y, r int) bool {
	for dx := -r; dx <= r; dx++ {
		for dz := -r; dz <= r; dz++ {
			p := world.Pos{X: pos.X + dx, Y: pos.Y + y, Z: pos.Z + dz}
			if s.treeCanOccupy(d, p) {
				continue
			}
			if n, _ := world.StateOwner(d.w.GetBlock(p)); treeLogTag(strings.TrimPrefix(n, "minecraft:")) {
				continue
			}
			return false
		}
	}
	return true
}

// treeLogTag is the minecraft:logs tag contents — TrunkPlacer.isFree
// counts these as space-free without being placement targets. The tag
// expands to every *_log, *_wood, *_stem and *_hyphae state including
// the stripped variants (mangrove_roots is not a member).
func treeLogTag(name string) bool {
	return strings.HasSuffix(name, "_log") || strings.HasSuffix(name, "_wood") ||
		strings.HasSuffix(name, "_stem") || strings.HasSuffix(name, "_hyphae")
}

// treeCanOccupy — TreeFeature.validTreePos: air or anything in the
// replaceable_by_trees tag (measured from the 26.2 tag data: the whole
// #leaves and #small_flowers tags plus the grass/vine/water families).
func (s *Server) treeCanOccupy(d *dimension, p world.Pos) bool {
	n, _ := world.StateOwner(d.w.GetBlock(p))
	return treeReplaceable[strings.TrimPrefix(n, "minecraft:")]
}

var treeReplaceable = map[string]bool{
	"air": true, "cave_air": true, "void_air": true,
	"water": true, "seagrass": true, "tall_seagrass": true,
	"short_grass": true, "tall_grass": true, "fern": true, "large_fern": true,
	"dead_bush": true, "vine": true, "glow_lichen": true,
	"sunflower": true, "lilac": true, "rose_bush": true, "peony": true,
	"hanging_roots": true, "pitcher_plant": true, "bush": true,
	"firefly_bush": true, "warped_roots": true, "nether_sprouts": true,
	"crimson_roots": true, "leaf_litter": true, "pale_moss_carpet": true,
	"short_dry_grass": true, "tall_dry_grass": true,
	"allium": true, "azure_bluet": true, "blue_orchid": true,
	"closed_eyeblossom": true, "cornflower": true, "dandelion": true,
	"golden_dandelion": true, "lily_of_the_valley": true,
	"open_eyeblossom": true, "orange_tulip": true, "oxeye_daisy": true,
	"pink_tulip": true, "poppy": true, "red_tulip": true,
	"torchflower": true, "white_tulip": true, "wither_rose": true,
	"acacia_leaves": true, "azalea_leaves": true, "birch_leaves": true,
	"cherry_leaves": true, "dark_oak_leaves": true,
	"flowering_azalea_leaves": true, "jungle_leaves": true,
	"mangrove_leaves": true, "oak_leaves": true, "pale_oak_leaves": true,
	"spruce_leaves": true,
}

// dirtTagName — the cannot_replace_below_tree_trunk tag (#dirt {dirt,
// coarse_dirt, rooted_dirt}, #mud {mud, muddy_mangrove_roots},
// #moss_blocks {moss_block, pale_moss_block}, podzol). Grass is NOT in
// it — a tree roots grass into dirt.
func dirtTagName(n string) bool {
	switch n {
	case "dirt", "coarse_dirt", "rooted_dirt", "mud",
		"muddy_mangrove_roots", "moss_block", "pale_moss_block", "podzol":
		return true
	}
	return false
}

// treeLeafDistances replays TreeFeature.updateLeaves: a BFS seeded at
// every written log walks the written leaf cells and sets
// distance = min(neighbour+1, 7).
func (s *Server) treeLeafDistances(d *dimension, origin world.Pos, treeHeight int, leaves []world.Pos) {
	leafSet := make(map[world.Pos]bool, len(leaves))
	for _, p := range leaves {
		leafSet[p] = true
	}
	var queue []world.Pos
	dist := make(map[world.Pos]int, len(leaves))
	for y := 0; y < treeHeight; y++ {
		p := world.Pos{X: origin.X, Y: origin.Y + y, Z: origin.Z}
		if n, _ := world.StateOwner(d.w.GetBlock(p)); !strings.HasSuffix(strings.TrimPrefix(n, "minecraft:"), "_log") {
			continue
		}
		for _, nb := range posNeighbors(p) {
			if leafSet[nb] {
				delete(leafSet, nb)
				dist[nb] = 1
				queue = append(queue, nb)
			}
		}
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		dv := dist[p]
		if blk := d.w.GetBlock(p); dv < 7 {
			if nb, ok := world.WithProperty(blk, "distance", strconv.Itoa(dv)); ok {
				s.setSilentBlock(d, p, nb)
			}
		}
		if dv+1 >= 7 {
			continue
		}
		for _, nb := range posNeighbors(p) {
			if leafSet[nb] {
				delete(leafSet, nb)
				dist[nb] = dv + 1
				queue = append(queue, nb)
			}
		}
	}
}

func posNeighbors(p world.Pos) [6]world.Pos {
	return [6]world.Pos{
		{X: p.X + 1, Y: p.Y, Z: p.Z}, {X: p.X - 1, Y: p.Y, Z: p.Z},
		{X: p.X, Y: p.Y + 1, Z: p.Z}, {X: p.X, Y: p.Y - 1, Z: p.Z},
		{X: p.X, Y: p.Y, Z: p.Z + 1}, {X: p.X, Y: p.Y, Z: p.Z - 1},
	}
}
