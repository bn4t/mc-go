package server

import (
	"math"

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
// birch blob, the spruce zigzag cone, or the acacia flat umbrella.
type canopyKind int

const (
	canopyBlob canopyKind = iota
	canopySpruce
	canopyAcacia
)

// trunkKind is the species' trunk_placer variant: the straight column
// every small tree shares or the acacia forking trunk (diagonal lean
// plus an optional side branch).
type trunkKind int

const (
	trunkStraight trunkKind = iota
	trunkForking
)

// foliageAttach is one FoliageAttachment: the cell the canopy centers
// on and the radiusOffset the placer adds to its sampled radius.
type foliageAttach struct {
	pos          world.Pos
	radiusOffset int
}

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
	trunk                    trunkKind
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
	"oak_sapling":    {"oak_log", "oak_leaves", 4, 2, 0, 0.1, 1, 1, trunkStraight, canopyBlob, false},
	"birch_sapling":  {"birch_log", "birch_leaves", 5, 2, 0, 0, 1, 1, trunkStraight, canopyBlob, false},
	"spruce_sapling": {"spruce_log", "spruce_leaves", 5, 2, 1, 0, 2, 2, trunkStraight, canopySpruce, false},
	"jungle_sapling": {"jungle_log", "jungle_leaves", 4, 8, 0, 0, 1, 1, trunkStraight, canopyBlob, false},
	"acacia_sapling": {"acacia_log", "acacia_leaves", 5, 2, 2, 0, 1, 2, trunkForking, canopyAcacia, false},
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

// growTree — TreeGrower.growTree: pick the species' feature (oak
// rolls fancy at 10%), clear the sapling, run the feature and restore
// the sapling on a failed place — all inside TreeGrower, before
// TreeFeature.doPlace ever sees the world.
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
	// secondary feature — oak's fancy tree runs FancyTrunkPlacer +
	// FancyFoliagePlacer against its own minsize rule.
	fancy := s.rng.nextDouble() < shape.fancyChance

	// growTree clears the sapling before the feature sees the world,
	// so the origin cell reads air for the whole place.
	s.setSilentBlock(d, pos, world.Air)
	var grew bool
	if fancy {
		grew = s.fancyOakFeature(d, pos)
	} else {
		grew = s.treeFeature(d, pos, shape)
	}
	if !grew {
		s.setSilentBlock(d, pos, blk)
	}
}

// placeFeatureName maps /place feature ids onto the implemented tree
// features and runs doPlace at pos. Every unimplemented feature fails
// like a failed place() — the configured-feature set on the wire
// already includes the whole vanilla registry, so unsupported names
// report "Failed to place feature" the same way vanilla reports a
// real placement that could not run.
func (s *Server) placeFeatureName(d *dimension, name string, pos world.Pos) bool {
	switch name {
	case "fancy_oak":
		return s.fancyOakFeature(d, pos)
	case "oak", "birch", "spruce", "acacia":
		return s.treeFeature(d, pos, treeShapes[name+"_sapling"])
	case "jungle_tree_no_vine":
		return s.treeFeature(d, pos, treeShapes["jungle_sapling"])
	case "jungle_tree":
		// jungle_tree shares no_vine's trunk and canopy and adds the
		// {cocoa, trunk_vine, leave_vine} decorator set.
		shape := treeShapes["jungle_sapling"]
		shape.jungleDecor = true
		return s.treeFeature(d, pos, shape)
	}
	return false
}

// treeFeature — TreeFeature.doPlace for the small-tree features:
// sample the trunk height, gate on bounds and the two-layers space
// check, write dirt under the trunk, then trunk, canopy, decorators
// and the updateLeaves distance pass. false when place() fails — the
// caller decides whether a sapling comes back.
func (s *Server) treeFeature(d *dimension, pos world.Pos, shape treeShape) bool {
	treeHeight := shape.baseHeight + s.rng.intn(shape.randA+1) + s.rng.intn(shape.randB+1)

	// doPlace's bounds gate: the feature refuses to run when the
	// trunk's own origin or its top would sit outside the world's
	// buildable range.
	if pos.Y < d.minY+1 || pos.Y+treeHeight+1 > d.minY+d.height {
		return false
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
		return false
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
	// collected — the decorators' context.logs() reads them — and the
	// trunk hands back its foliage attachments.
	logState, _ := world.BlockStateOf(shape.log, map[string]string{"axis": "y"})
	var logs []world.Pos
	var attachments []foliageAttach
	switch shape.trunk {
	case trunkForking:
		attachments = s.forkingTrunk(d, pos, treeHeight, logState, &logs)
	default:
		for y := 0; y < treeHeight; y++ {
			p := world.Pos{X: pos.X, Y: pos.Y + y, Z: pos.Z}
			if s.treeCanOccupy(d, p) {
				s.setNaturalBlock(d, p, logState)
				logs = append(logs, p)
			}
		}
		attachments = []foliageAttach{
			{pos: world.Pos{X: pos.X, Y: pos.Y + treeHeight, Z: pos.Z}, radiusOffset: 0},
		}
	}

	// createFoliage: the attachment sits one above the top log.
	top := world.Pos{X: pos.X, Y: pos.Y + treeHeight, Z: pos.Z}
	var leaves []world.Pos
	leafState, _ := world.BlockStateOf(shape.leaf, map[string]string{
		"distance": "7", "persistent": "false", "waterlogged": "false",
	})
	placeLeaf := func(p world.Pos) {
		s.placeTreeLeaf(d, p, leafState, &leaves)
	}
	switch shape.canopy {
	case canopySpruce:
		s.spruceCanopy(d, top, treeHeight, placeLeaf)
	case canopyAcacia:
		s.acaciaCanopy(d, attachments, placeLeaf)
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

	// updateLeaves: BFS from every written log assigns
	// DISTANCE=min(neighbour+1, 7) on the leaf cells at once — the same
	// result the leaf distance ticks would converge to over ticks.
	s.treeLeafDistances(d, logs, leaves)
	return true
}

// placeTreeLeaf is FoliagePlacer.tryPlaceLeaf: never overwrites
// persistent leaves or anything outside REPLACEABLE_BY_TREES (logs
// included), and folds waterlogged from the cell's fluid into the
// written state.
func (s *Server) placeTreeLeaf(d *dimension, p world.Pos, leafState world.Block, leaves *[]world.Pos) {
	cur := d.w.GetBlock(p)
	if pv, ok := world.StateProperty(cur, "persistent"); ok && pv == "true" {
		return
	}
	if !s.treeCanOccupy(d, p) {
		return
	}
	st := leafState
	if k, _, ok := fluidOf(cur); ok && k == fluidWater {
		if nb, ok := world.WithProperty(st, "waterlogged", "true"); ok {
			st = nb
		}
	}
	s.setNaturalBlock(d, p, st)
	*leaves = append(*leaves, p)
}

// fancyOakFeature — fancy_oak.json: the oak secondary feature. Trunk
// {base 3, randA 11, randB 0}, FancyFoliagePlacer {radius 2, offset 4,
// height 4}, decorators [], and two_layers {limit 0, upper_size 0,
// min_clipped_height 4} — the single-column space check may clip the
// tree to as little as 4 cells, and a clipped fancy oak still grows
// (unlike the main feature which fails when it can't reach its full
// sampled height).
func (s *Server) fancyOakFeature(d *dimension, pos world.Pos) bool {
	treeHeight := 3 + s.rng.intn(12) + s.rng.intn(1)
	if pos.Y < d.minY+1 || pos.Y+treeHeight+1 > d.minY+d.height {
		return false
	}
	// getMaxFreeTreeHeight with getSizeAtHeight → 0 at every layer.
	clipped := treeHeight
	for y := 0; y <= treeHeight+1; y++ {
		if !s.treeSpaceFree(d, pos, y, 0) {
			clipped = y - 2
			break
		}
	}
	// clippedTreeHeight >= treeHeight || clippedTreeHeight >= 4.
	if clipped < treeHeight && clipped < 4 {
		return false
	}

	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if bn, _ := world.StateOwner(d.w.GetBlock(below)); !dirtTagName(strings.TrimPrefix(bn, "minecraft:")) {
		if dirt, ok := world.BlockStateOf("dirt", nil); ok {
			s.setNaturalBlock(d, below, dirt)
		}
	}

	logState, _ := world.BlockStateOf("oak_log", map[string]string{"axis": "y"})
	var logs []world.Pos
	attachments := s.fancyTrunk(d, pos, clipped, logState, &logs)

	leafState, _ := world.BlockStateOf("oak_leaves", map[string]string{
		"distance": "7", "persistent": "false", "waterlogged": "false",
	})
	var leaves []world.Pos
	// FancyFoliagePlacer.createFoliage per attachment: five rows at
	// yo = offset(4)..offset-foliageHeight(0) with radius 2 on the top
	// and bottom rows and 3 between, and the circular skip
	// (dx+.5)^2 + (dz+.5)^2 > r^2.
	for _, at := range attachments {
		for yo := 4; yo >= 0; yo-- {
			r := 2
			if yo != 4 && yo != 0 {
				r = 3
			}
			for dx := -r; dx <= r; dx++ {
				for dz := -r; dz <= r; dz++ {
					fx, fz := float64(dx)+0.5, float64(dz)+0.5
					if fx*fx+fz*fz > float64(r*r) {
						continue
					}
					s.placeTreeLeaf(d, world.Pos{X: at.pos.X + dx, Y: at.pos.Y + yo, Z: at.pos.Z + dz}, leafState, &leaves)
				}
			}
		}
	}
	s.treeLeafDistances(d, logs, leaves)
	return true
}

// fancyCoord is FancyTrunkPlacer.FoliageCoords: an attachment cell and
// the trunk-column Y its branch leaves from.
type fancyCoord struct {
	attach      world.Pos
	branchBaseY int
}

// fancyTrunk — FancyTrunkPlacer.placeTrunk. height = clipped+2,
// trunkHeight = floor(height*0.618): candidate foliage spots are
// scanned from the crown down while two limbs pass collision checks
// (isFree — log cells count as free), then the trunk column and one
// limb per kept coord are written; coords whose branch base is below
// 20% of the tree are trimmed away.
func (s *Server) fancyTrunk(d *dimension, pos world.Pos, clipped int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	height := clipped + 2
	trunkHeight := int(math.Floor(float64(height) * 0.618))
	trunkTop := pos.Y + trunkHeight

	coords := []fancyCoord{{
		attach:      world.Pos{X: pos.X, Y: pos.Y + height - 5, Z: pos.Z},
		branchBaseY: trunkTop,
	}}
	// clustersPerY = min(1, floor(1.382 + (height/13)^2)) is 1 for every
	// treeHeight fancy reaches (height <= 16).
	for relY := height - 5; relY >= 0; relY-- {
		ts := fancyTreeShape(height, relY)
		if ts < 0 {
			continue
		}
		radius := float64(ts) * (s.rng.nextDouble() + 0.328)
		angle := s.rng.nextDouble() * 2 * math.Pi
		x := radius*math.Sin(angle) + 0.5
		z := radius*math.Cos(angle) + 0.5
		checkStart := world.Pos{
			X: pos.X + int(math.Floor(x)),
			Y: pos.Y + relY - 1,
			Z: pos.Z + int(math.Floor(z)),
		}
		checkEnd := world.Pos{X: checkStart.X, Y: checkStart.Y + 5, Z: checkStart.Z}
		if !s.fancyLimbFree(d, checkStart, checkEnd) {
			continue
		}
		dx := pos.X - checkStart.X
		dz := pos.Z - checkStart.Z
		branchTop := int(float64(checkStart.Y) - math.Sqrt(float64(dx*dx+dz*dz))*0.381)
		if branchTop > trunkTop {
			branchTop = trunkTop
		}
		base := world.Pos{X: pos.X, Y: branchTop, Z: pos.Z}
		if !s.fancyLimbFree(d, base, checkStart) {
			continue
		}
		coords = append(coords, fancyCoord{attach: checkStart, branchBaseY: base.Y})
	}

	s.fancyLimb(d, pos, world.Pos{X: pos.X, Y: pos.Y + trunkHeight, Z: pos.Z}, logState, logs)
	for _, c := range coords {
		base := world.Pos{X: pos.X, Y: c.branchBaseY, Z: pos.Z}
		if base != c.attach && fancyTrim(height, base.Y-pos.Y) {
			s.fancyLimb(d, base, c.attach, logState, logs)
		}
	}

	var attachments []foliageAttach
	for _, c := range coords {
		if fancyTrim(height, c.branchBaseY-pos.Y) {
			attachments = append(attachments, foliageAttach{pos: c.attach})
		}
	}
	return attachments
}

// fancyTreeShape — FancyTrunkPlacer.treeShape: -1 below 30% of the
// tree (never tries foliage there), 0 outside the ellipsoid shell,
// else half the chord radius at that layer.
func fancyTreeShape(height, y int) float64 {
	if float64(y) < float64(height)*0.3 {
		return -1
	}
	r := float64(height) / 2
	adj := r - float64(y)
	if adj == 0 {
		return r * 0.5
	}
	if math.Abs(adj) >= r {
		return 0
	}
	return math.Sqrt(r*r-adj*adj) * 0.5
}

// fancyTrim — FancyTrunkPlacer.trimBranches.
func fancyTrim(height, localY int) bool {
	return float64(localY) >= float64(height)*0.2
}

// fancyLimbFree — makeLimb with doPlace=false: every interpolated cell
// must be isFree (validTreePos or a #logs cell); a zero-length limb is
// always free.
func (s *Server) fancyLimbFree(d *dimension, start, end world.Pos) bool {
	if start == end {
		return true
	}
	dx, dy, dz := end.X-start.X, end.Y-start.Y, end.Z-start.Z
	steps := max(absi(dx), absi(dy), absi(dz))
	for i := 0; i <= steps; i++ {
		bp := fancyLimbCell(start, dx, dy, dz, steps, i)
		if !s.treeFree(d, bp) {
			return false
		}
	}
	return true
}

// fancyLimbCell is the interpolated limb cell at step i: Java's
// (int)(0.5 + i*d/steps) floor, where a zero-length limb keeps the
// start cell (Java's (int)NaN → 0).
func fancyLimbCell(start world.Pos, dx, dy, dz, steps, i int) world.Pos {
	if steps == 0 {
		return start
	}
	return world.Pos{
		X: start.X + int(math.Floor(0.5+float64(i)*float64(dx)/float64(steps))),
		Y: start.Y + int(math.Floor(0.5+float64(i)*float64(dy)/float64(steps))),
		Z: start.Z + int(math.Floor(0.5+float64(i)*float64(dz)/float64(steps))),
	}
}

// fancyLimb — makeLimb with doPlace=true: each interpolated cell writes
// a log when validTreePos, with the axis of the dominant direction of
// the limb's start point (the branch limbs' logs lie horizontal).
func (s *Server) fancyLimb(d *dimension, start, end world.Pos, logState world.Block, logs *[]world.Pos) {
	dx, dy, dz := end.X-start.X, end.Y-start.Y, end.Z-start.Z
	steps := max(absi(dx), absi(dy), absi(dz))
	for i := 0; i <= steps; i++ {
		bp := fancyLimbCell(start, dx, dy, dz, steps, i)
		if !s.treeCanOccupy(d, bp) {
			continue
		}
		st := logState
		ax, az := absi(bp.X-start.X), absi(bp.Z-start.Z)
		switch {
		case ax == 0 && az == 0:
		case ax >= az:
			st, _ = world.WithProperty(st, "axis", "x")
		default:
			st, _ = world.WithProperty(st, "axis", "z")
		}
		s.setNaturalBlock(d, bp, st)
		*logs = append(*logs, bp)
	}
}

// treeFree — TrunkPlacer.isFree: validTreePos or a #logs cell — trunks
// weave through other logs on both the space check and limb checks.
func (s *Server) treeFree(d *dimension, p world.Pos) bool {
	if s.treeCanOccupy(d, p) {
		return true
	}
	n, _ := world.StateOwner(d.w.GetBlock(p))
	return treeLogTag(strings.TrimPrefix(n, "minecraft:"))
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

// acaciaCanopy — AcaciaFoliagePlacer.createFoliage: one flat
// umbrella per trunk attachment. The skirt row sits one below the
// attachment at radius leafRadius(2) + radiusOffset (3 on the main
// top, 2 on the branch tip) with its corners skipped; the cap row at
// the attachment level is radius leafRadius-1 = 1 where the skip
// predicate (dx>1||dz>1)&&dx!=0&&dz!=0 never fires → a full 3x3.
func (s *Server) acaciaCanopy(d *dimension, attachments []foliageAttach, placeLeaf func(world.Pos)) {
	for _, at := range attachments {
		// skirt: attach-1, radius 2+radiusOffset, corners skipped.
		r := 2 + at.radiusOffset
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if absi(dx) == r && absi(dz) == r && r > 0 {
					continue
				}
				placeLeaf(world.Pos{X: at.pos.X + dx, Y: at.pos.Y - 1, Z: at.pos.Z + dz})
			}
		}
		// cap: attach level, radius 1, no cell ever skipped.
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				placeLeaf(world.Pos{X: at.pos.X + dx, Y: at.pos.Y, Z: at.pos.Z + dz})
			}
		}
	}
}

// forkingTrunk — ForkingTrunkPlacer.placeTrunk: the column leans one
// horizontal step per level for the top leanSteps(1-3) levels from
// leanHeight(treeHeight - U(0..3) - 1), and a second staircase branch
// climbs from leanHeight - U(0..1) - 1 for 1-3 logs when its random
// direction differs from the lean. Attachments: the main tip carries
// radiusOffset 1, the branch tip 0.
var horizDirs = [4]world.Pos{
	{X: 0, Z: -1}, {X: 0, Z: 1}, {X: -1, Z: 0}, {X: 1, Z: 0},
}

func (s *Server) forkingTrunk(d *dimension, pos world.Pos, treeHeight int, logState world.Block, logs *[]world.Pos) []foliageAttach {
	var attachments []foliageAttach
	lean := horizDirs[s.rng.intn(4)]
	leanHeight := treeHeight - s.rng.intn(4) - 1
	leanSteps := 3 - s.rng.intn(3)
	tx, tz := pos.X, pos.Z
	var top world.Pos
	hasTop := false
	for yo := 0; yo < treeHeight; yo++ {
		yy := pos.Y + yo
		if yo >= leanHeight && leanSteps > 0 {
			tx += lean.X
			tz += lean.Z
			leanSteps--
		}
		p := world.Pos{X: tx, Y: yy, Z: tz}
		if s.treeCanOccupy(d, p) {
			s.setNaturalBlock(d, p, logState)
			*logs = append(*logs, p)
			top = world.Pos{X: tx, Y: yy + 1, Z: tz}
			hasTop = true
		}
	}
	if hasTop {
		attachments = append(attachments, foliageAttach{
			pos: top, radiusOffset: 1})
	}

	tx, tz = pos.X, pos.Z
	branch := horizDirs[s.rng.intn(4)]
	if branch != lean {
		branchPos := leanHeight - s.rng.intn(2) - 1
		branchSteps := 1 + s.rng.intn(3)
		hasTop = false
		for yo := branchPos; yo < treeHeight && branchSteps > 0; yo++ {
			if yo >= 1 {
				yy := pos.Y + yo
				tx += branch.X
				tz += branch.Z
				p := world.Pos{X: tx, Y: yy, Z: tz}
				if s.treeCanOccupy(d, p) {
					s.setNaturalBlock(d, p, logState)
					*logs = append(*logs, p)
					top = world.Pos{X: tx, Y: yy + 1, Z: tz}
					hasTop = true
				}
			}
			branchSteps--
		}
		if hasTop {
			attachments = append(attachments, foliageAttach{
				pos: top, radiusOffset: 0})
		}
	}
	return attachments
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
// every written log cell walks the written leaf cells and sets
// distance = min(neighbour+1, 7). The seed is the collected trunk set —
// leaning/forked trunks leave the origin column, so scanning the column
// would miss them.
func (s *Server) treeLeafDistances(d *dimension, logs, leaves []world.Pos) {
	leafSet := make(map[world.Pos]bool, len(leaves))
	for _, p := range leaves {
		leafSet[p] = true
	}
	var queue []world.Pos
	dist := make(map[world.Pos]int, len(leaves))
	for _, p := range logs {
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
