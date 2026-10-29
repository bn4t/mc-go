package server

import (
	"math"
	"strconv"
	"strings"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Block placement, measured from the 26.2 jar: BlockItem.useOn/place,
// BlockPlaceContext, the item classes that override placement
// (StandingAndWallBlockItem, DoubleHighBlockItem, BedItem,
// GameMasterBlockItem, ScaffoldingBlockItem) and every block class's
// getStateForPlacement and canBeReplaced(BlockPlaceContext).
//
//   - The clicked block is replaced in place when its
//     canBeReplaced(context) holds (replaceClicked); otherwise the block
//     goes to the clicked face's neighbour, which must itself be
//     canBeReplaced(context).
//   - The block class turns the context into a state (null fails the
//     place); the item then requires canSurvive and an unobstructed
//     collision shape - a StandingAndWallBlockItem tries the wall and
//     standing variants over the looking directions instead.
//   - The state is written with flags 11; a double-high item first
//     clears the cell above to air (or water) and the block's
//     setPlacedBy places its other half (doors, double plants, beds).

// pdir is a Direction by ordinal: down, up, north, south, west, east.
type pdir int

const (
	pDown pdir = iota
	pUp
	pNorth
	pSouth
	pWest
	pEast
)

var pdirNames = [6]string{"down", "up", "north", "south", "west", "east"}

func (d pdir) String() string   { return pdirNames[d] }
func (d pdir) opp() pdir        { return d ^ 1 }
func (d pdir) horizontal() bool { return d >= pNorth }

// axis is Direction.getAxis: 'y', 'z' or 'x'.
func (d pdir) axis() byte {
	switch d {
	case pDown, pUp:
		return 'y'
	case pNorth, pSouth:
		return 'z'
	}
	return 'x'
}

// cw is Direction.getClockWise (horizontal).
func (d pdir) cw() pdir {
	return [6]pdir{pDown, pUp, pEast, pWest, pNorth, pSouth}[d]
}

// ccw is Direction.getCounterClockWise (horizontal).
func (d pdir) ccw() pdir {
	return [6]pdir{pDown, pUp, pWest, pEast, pSouth, pNorth}[d]
}

func (d pdir) step() (int, int, int) {
	switch d {
	case pDown:
		return 0, -1, 0
	case pUp:
		return 0, 1, 0
	case pNorth:
		return 0, 0, -1
	case pSouth:
		return 0, 0, 1
	case pWest:
		return -1, 0, 0
	}
	return 1, 0, 0
}

func (d pdir) rel(p world.Pos) world.Pos {
	x, y, z := d.step()
	return world.Pos{X: p.X + x, Y: p.Y + y, Z: p.Z + z}
}

// data2d is Direction.get2DDataValue: south 0, west 1, north 2, east 3.
func (d pdir) data2d() int {
	return [6]int{-1, -1, 2, 0, 1, 3}[d]
}

func pdirFromName(s string) (pdir, bool) {
	for i, n := range pdirNames {
		if n == s {
			return pdir(i), true
		}
	}
	return 0, false
}

// pdirFromYRot is Direction.fromYRot: floor(yaw/90 + 0.5) & 3 over the
// 2D data values.
func pdirFromYRot(yaw float64) pdir {
	return [4]pdir{pSouth, pWest, pNorth, pEast}[int(math.Floor(yaw/90+0.5))&3]
}

// wrapDegreesF is Mth.wrapDegrees(float).
func wrapDegreesF(f float32) float32 {
	g := float32(math.Mod(float64(f), 360))
	if g >= 180 {
		g -= 360
	}
	if g < -180 {
		g += 360
	}
	return g
}

// rotationSegment is RotationSegment.convertToSegment(float): sixteen
// segments, Math.round(deg * 16/360) masked.
func rotationSegment(deg float32) int {
	return int(math.Floor(float64(deg*(16.0/360.0))+0.5)) & 15
}

// placeCtx is a BlockPlaceContext.
type placeCtx struct {
	s    *Server
	d    *dimension
	sess *Session
	// hit is the clicked block, face the clicked face, loc the click
	// location (block + cursor), inside the hit result's inside flag.
	hit    world.Pos
	face   pdir
	loc    [3]float64
	inside bool
	// pos is getClickedPos: hit when replaceClicked, else the face's
	// neighbour.
	pos            world.Pos
	replaceClicked bool
	yaw, pitch     float32
	sneak          bool
	held           ItemStack
	item           string // held item name, namespace trimmed
}

func newPlaceCtx(s *Server, d *dimension, sess *Session, hit world.Pos, face pdir, cursor [3]float64, inside bool) *placeCtx {
	return newPlaceCtxWithStack(s, d, sess, hit, face, cursor, inside, sess.heldStack())
}

func newPlaceCtxWithStack(s *Server, d *dimension, sess *Session, hit world.Pos, face pdir, cursor [3]float64, inside bool, held ItemStack) *placeCtx {
	_, _, _, yaw, pitch, _ := sess.Pos()
	c := &placeCtx{s: s, d: d, sess: sess, hit: hit, face: face, inside: inside,
		yaw: wrapDegreesF(yaw), pitch: pitch, sneak: sess.sneaking, held: held}
	c.loc = [3]float64{float64(hit.X) + cursor[0], float64(hit.Y) + cursor[1], float64(hit.Z) + cursor[2]}
	if n, ok := itemNameOf(c.held.ID); ok && !c.held.Empty() {
		c.item = strings.TrimPrefix(n, "minecraft:")
	}
	// The constructor asks the clicked block with replaceClicked still
	// true (getClickedPos is the hit block).
	c.pos, c.replaceClicked = hit, true
	c.replaceClicked = c.canBeReplaced(c.at(hit))
	if !c.replaceClicked {
		c.pos = face.rel(hit)
	}
	return c
}

// at is Level.getBlockState.
func (c *placeCtx) at(p world.Pos) world.Block { return c.d.w.GetBlock(p) }

// horizontal is getHorizontalDirection: the player's facing.
func (c *placeCtx) horizontal() pdir { return pdirFromYRot(float64(c.yaw)) }

// lookingDirs is Direction.orderedByNearest(player): the view vector's
// axes from the most to the least aligned, then their opposites in
// reverse (Mth sin/cos of the float radians).
func (c *placeCtx) lookingDirs() [6]pdir {
	f := c.pitch * 0.017453292
	g := -c.yaw * 0.017453292
	h := worldgen.MthSin(float64(f))
	i := worldgen.MthCos(float64(f))
	j := worldgen.MthSin(float64(g))
	k := worldgen.MthCos(float64(g))
	east, up, south := j > 0, h < 0, k > 0
	l, m, n := -j, h, -k
	if east {
		l = j
	}
	if up {
		m = -h
	}
	if south {
		n = k
	}
	o, p := l*i, n*i
	d1, d2, d3 := pWest, pDown, pNorth
	if east {
		d1 = pEast
	}
	if up {
		d2 = pUp
	}
	if south {
		d3 = pSouth
	}
	mk := func(a, b, c pdir) [6]pdir { return [6]pdir{a, b, c, c.opp(), b.opp(), a.opp()} }
	if l > n {
		if m > o {
			return mk(d2, d1, d3)
		}
		if p > m {
			return mk(d1, d3, d2)
		}
		return mk(d1, d2, d3)
	}
	if m > p {
		return mk(d2, d3, d1)
	}
	if o > m {
		return mk(d3, d1, d2)
	}
	return mk(d3, d2, d1)
}

// nearestLooking is getNearestLookingDirection.
func (c *placeCtx) nearestLooking() pdir { return c.lookingDirs()[0] }

// nearestLookingDirs is getNearestLookingDirections: with a face
// placement (not replacing the clicked block) the clicked face's
// opposite moves to the front.
func (c *placeCtx) nearestLookingDirs() []pdir {
	a := c.lookingDirs()
	out := a[:]
	if c.replaceClicked {
		return out
	}
	want := c.face.opp()
	i := 0
	for i < len(out) && out[i] != want {
		i++
	}
	if i > 0 && i < len(out) {
		copy(out[1:i+1], out[:i])
		out[0] = want
	}
	return out
}

// verticalLooking is getNearestLookingVerticalDirection.
func (c *placeCtx) verticalLooking() pdir {
	if c.pitch < 0 {
		return pUp
	}
	return pDown
}

// signal is Level.hasNeighborSignal.
func (c *placeCtx) signal(p world.Pos) bool { return rsHasNeighborSignal(c.d, p) }

// waterAt is getFluidState(p).is(Fluids.WATER): a still source.
func (c *placeCtx) waterAt(p world.Pos) bool { return worldgen.FluidIsWater(c.at(p)) }

// fullWaterAt is FluidState.is(FluidTags.WATER) && isFull.
func (c *placeCtx) fullWaterAt(p world.Pos) bool {
	b := c.at(p)
	return worldgen.FluidInWaterTag(b) && worldgen.FluidAmount(b) == 8
}

func (c *placeCtx) holding(item string) bool { return c.item != "" && c.item == item }

// blockItemOf is Block.asItem: the item that places a block (a wall
// variant answers its standing item).
func blockItemOf(block string) string {
	placeItemIndexOnce.Do(buildPlaceItemIndex)
	return placeItemByBlock[block]
}

func bstate(b world.Block) string {
	n, _ := world.StateOwner(b)
	return strings.TrimPrefix(n, "minecraft:")
}

func with(b world.Block, k, v string) world.Block {
	if nb, ok := world.WithProperty(b, k, v); ok {
		return nb
	}
	return b
}

func withBool(b world.Block, k string, v bool) world.Block { return with(b, k, boolStr(v)) }

func propInt(b world.Block, k string) int {
	v, _ := world.StateProperty(b, k)
	n, _ := strconv.Atoi(v)
	return n
}

func blockClassOf(name string) []string { return worldgen.BlockClasses(name) }

func hasClassName(name, class string) bool { return worldgen.HasBlockClass(name, class) }

// canBeReplaced is BlockState.canBeReplaced(BlockPlaceContext) with the
// class overrides.
func (c *placeCtx) canBeReplaced(st world.Block) bool {
	name := bstate(st)
	own := blockItemOf(name)
	def := func() bool {
		return worldgen.CanBeReplaced(st) && (c.held.Empty() || !c.holding(own))
	}
	segment := func(prop string) bool {
		return !c.sneak && c.holding(own) && propInt(st, prop) < 4
	}
	for _, cls := range blockClassOf(name) {
		switch cls {
		case "CandleBlock":
			if segment("candles") {
				return true
			}
			return def()
		case "FlowerBedBlock":
			if segment("flower_amount") {
				return true
			}
			return def()
		case "LeafLitterBlock":
			if segment("segment_amount") {
				return true
			}
			return def()
		case "SeaPickleBlock":
			if segment("pickles") {
				return true
			}
			return def()
		case "TurtleEggBlock":
			if segment("eggs") {
				return true
			}
			return def()
		case "GrowingPlantBodyBlock":
			r := def()
			if r && c.holding(blockItemOf(growingHeadOf(name))) {
				return false
			}
			return r
		case "MultifaceBlock":
			if !c.holding(own) {
				return true
			}
			return multifaceVacant(st)
		case "PitcherCropBlock":
			return false
		case "ScaffoldingBlock":
			return c.holding(own)
		case "SlabBlock":
			typ, _ := world.StateProperty(st, "type")
			if typ == "double" || !c.holding(own) {
				return false
			}
			if !c.replaceClicked {
				return true
			}
			upper := c.loc[1]-float64(c.pos.Y) > 0.5
			if typ == "bottom" {
				return c.face == pUp || (upper && c.face.horizontal())
			}
			return c.face == pDown || (!upper && c.face.horizontal())
		case "SnowLayerBlock":
			layers := propInt(st, "layers")
			if c.holding(own) && layers < 8 {
				if c.replaceClicked {
					return c.face == pUp
				}
				return true
			}
			return layers == 1
		case "VineBlock":
			if bstate(c.at(c.pos)) == name {
				return vineFaceCount(c.at(c.pos)) < 5
			}
			return def()
		}
	}
	return def()
}

// growingHeadOf names a growing plant body's head block.
func growingHeadOf(body string) string {
	switch body {
	case "kelp_plant":
		return "kelp"
	case "cave_vines_plant":
		return "cave_vines"
	case "weeping_vines_plant":
		return "weeping_vines"
	case "twisting_vines_plant":
		return "twisting_vines"
	}
	return body
}

// multifaceVacant is MultifaceBlock.hasAnyVacantFace.
func multifaceVacant(st world.Block) bool {
	for _, n := range pdirNames {
		if v, ok := world.StateProperty(st, n); ok && v == "false" {
			return true
		}
	}
	return false
}

// vineFaceCount is VineBlock.countFaces.
func vineFaceCount(st world.Block) int {
	n := 0
	for _, f := range []string{"up", "north", "east", "south", "west"} {
		if v, _ := world.StateProperty(st, f); v == "true" {
			n++
		}
	}
	return n
}

// canSurvive is BlockState.canSurvive at p.
func (c *placeCtx) canSurvive(st world.Block, p world.Pos) bool {
	return blockCanSurvive(c.s, c.d, st, p)
}

// blockCanSurvive is BlockState.canSurvive over a live dimension.
func blockCanSurvive(s *Server, d *dimension, st world.Block, p world.Pos) bool {
	switch bstate(st) {
	case "scaffolding":
		// ScaffoldingBlock.canSurvive: getDistance < 7.
		return s.scaffoldDistance(d, p) < 7
	}
	return worldgen.CanSurvive(levelGetter(d), st, worldgen.BlockPos{int32(p.X), int32(p.Y), int32(p.Z)})
}

// sturdy is BlockState.isFaceSturdy(dir, FULL).
func sturdy(b world.Block, dir pdir) bool {
	return worldgen.FaceSturdy(b, int(dir), worldgen.SupportFull)
}

// stateForPlacement is Block.getStateForPlacement for block `name`.
// The bool is false where vanilla returns null.
func (c *placeCtx) stateForPlacement(name string) (world.Block, bool) {
	def, ok := world.BlockState(name)
	if !ok {
		return 0, false
	}
	for _, cls := range blockClassOf(name) {
		if f, ok := placeByClass[cls]; ok {
			return f(c, name, def)
		}
	}
	return def, true
}

type placeFunc func(c *placeCtx, name string, def world.Block) (world.Block, bool)

var placeByClass map[string]placeFunc

func init() {
	wl := func(c *placeCtx, b world.Block) world.Block { return withBool(b, "waterlogged", c.waterAt(c.pos)) }
	hz := func(prop string, f func(c *placeCtx) pdir) placeFunc {
		return func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
			return with(def, prop, f(c).String()), true
		}
	}
	hzOpp := func(c *placeCtx) pdir { return c.horizontal().opp() }
	lookOpp := func(c *placeCtx) pdir { return c.nearestLooking().opp() }
	wlOnly := func(c *placeCtx, _ string, def world.Block) (world.Block, bool) { return wl(c, def), true }
	wlFull := func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return withBool(def, "waterlogged", c.fullWaterAt(c.pos)), true
	}
	// tryHorizontal walks the horizontal looking directions, setting
	// facing (opposite the look unless keep) and keeping the first state
	// that survives.
	tryHorizontal := func(c *placeCtx, st world.Block, opposite bool) (world.Block, bool) {
		for _, dir := range c.nearestLookingDirs() {
			if !dir.horizontal() {
				continue
			}
			f := dir
			if opposite {
				f = dir.opp()
			}
			st = with(st, "facing", f.String())
			if c.canSurvive(st, c.pos) {
				return st, true
			}
		}
		return 0, false
	}
	axisOf := func(d pdir) string { return string(d.axis()) }
	pillar := func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return with(def, "axis", axisOf(c.face)), true
	}
	segmentable := func(prop string) placeFunc {
		return func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
			if cur := c.at(c.pos); bstate(cur) == name {
				return with(cur, prop, strconv.Itoa(min(4, propInt(cur, prop)+1))), true
			}
			return with(def, "facing", c.horizontal().opp().String()), true
		}
	}
	counted := func(prop string, maxN int, rest placeFunc) placeFunc {
		return func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
			if cur := c.at(c.pos); bstate(cur) == name {
				return with(cur, prop, strconv.Itoa(min(maxN, propInt(cur, prop)+1))), true
			}
			return rest(c, name, def)
		}
	}
	doublePlant := func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		if c.pos.Y < c.d.minY+c.d.height-1 && c.canBeReplaced(c.at(posAbove(c.pos, 1))) {
			return def, true
		}
		return 0, false
	}
	growingPlant := func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		fam := growingFamilies[name]
		next := c.at(fam.growth.rel(c.pos))
		if n := bstate(next); n == fam.head || n == fam.body {
			b, _ := world.BlockState(fam.body)
			return b, true
		}
		// GrowingPlantHeadBlock.getStateForPlacement(random): a random
		// age below 25 from the level random.
		return with(def, "age", strconv.Itoa(c.s.rng.intn(25))), true
	}
	placeByClass = map[string]placeFunc{
		"AbstractFurnaceBlock":        hz("facing", hzOpp),
		"BeehiveBlock":                hz("facing", hzOpp),
		"CarvedPumpkinBlock":          hz("facing", hzOpp),
		"ChiseledBookShelfBlock":      hz("facing", hzOpp),
		"GlazedTerracottaBlock":       hz("facing", hzOpp),
		"LoomBlock":                   hz("facing", hzOpp),
		"StonecutterBlock":            hz("facing", hzOpp),
		"VaultBlock":                  hz("facing", hzOpp),
		"AnvilBlock":                  hz("facing", func(c *placeCtx) pdir { return c.horizontal().cw() }),
		"BarrelBlock":                 hz("facing", lookOpp),
		"CommandBlock":                hz("facing", lookOpp),
		"DispenserBlock":              hz("facing", lookOpp),
		"ObserverBlock":               hz("facing", func(c *placeCtx) pdir { return c.nearestLooking() }),
		"ShulkerBoxBlock":             hz("facing", func(c *placeCtx) pdir { return c.face }),
		"InfestedRotatedPillarBlock":  pillar,
		"RotatedPillarBlock":          pillar,
		"BarrierBlock":                wlOnly,
		"HeavyCoreBlock":              wlOnly,
		"SculkSensorBlock":            wlOnly,
		"SculkShriekerBlock":          wlOnly,
		"WaterloggedTransparentBlock": wlOnly,
		"MangroveRootsBlock":          wlOnly,
		"BaseCoralPlantTypeBlock":     wlFull,
		"ConduitBlock":                wlFull,
		"Block":                       func(_ *placeCtx, _ string, def world.Block) (world.Block, bool) { return def, true },
	}
	placeByClass["PistonBaseBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return withBool(with(def, "facing", lookOpp(c).String()), "extended", false), true
	}
	placeByClass["EndPortalFrameBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return withBool(with(def, "facing", hzOpp(c).String()), "eye", false), true
	}
	placeByClass["EnderChestBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return wl(c, with(def, "facing", hzOpp(c).String())), true
	}
	placeByClass["CopperGolemStatueBlock"] = placeByClass["EnderChestBlock"]
	placeByClass["DriedGhastBlock"] = placeByClass["EnderChestBlock"]
	placeByClass["DecoratedPotBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return withBool(wl(c, with(def, "facing", c.horizontal().String())), "cracked", false), true
	}
	placeByClass["ShelfBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return wl(c, withBool(with(def, "facing", hzOpp(c).String()), "powered", c.signal(c.pos))), true
	}
	placeByClass["LecternBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		// A game master's stack with block entity data holding a Book
		// places with a book; mc-go's stacks carry no such data.
		return withBool(with(def, "facing", hzOpp(c).String()), "has_book", false), true
	}
	placeByClass["AbstractSkullBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return withBool(def, "powered", c.signal(c.pos)), true
	}
	placeByClass["SkullBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st, _ := placeByClass["AbstractSkullBlock"](c, name, def)
		return with(st, "rotation", strconv.Itoa(rotationSegment(c.yaw))), true
	}
	placeByClass["WallSkullBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st, _ := placeByClass["AbstractSkullBlock"](c, name, def)
		for _, dir := range c.nearestLookingDirs() {
			if !dir.horizontal() {
				continue
			}
			st = with(st, "facing", dir.opp().String())
			if !c.canBeReplaced(c.at(dir.rel(c.pos))) {
				return st, true
			}
		}
		return 0, false
	}
	placeByClass["AmethystClusterBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return with(wl(c, def), "facing", c.face.String()), true
	}
	placeByClass["LightningRodBlock"] = placeByClass["AmethystClusterBlock"]
	placeByClass["BambooStalkBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		if worldgen.HasFluid(c.at(c.pos)) {
			return 0, false
		}
		below := c.at(posBelow(c.pos, 1))
		if !worldgen.InBlockTag(below, "supports_bamboo") {
			return 0, false
		}
		switch bstate(below) {
		case "bamboo_sapling":
			return with(def, "age", "0"), true
		case "bamboo":
			age := "0"
			if propInt(below, "age") > 0 {
				age = "1"
			}
			return with(def, "age", age), true
		}
		if above := c.at(posAbove(c.pos, 1)); bstate(above) == "bamboo" {
			return with(def, "age", strconv.Itoa(propInt(above, "age"))), true
		}
		b, _ := world.BlockState("bamboo_sapling")
		return b, true
	}
	placeByClass["BannerBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return with(def, "rotation", strconv.Itoa(rotationSegment(c.yaw+180))), true
	}
	placeByClass["StandingSignBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st, _ := placeByClass["BannerBlock"](c, name, def)
		return wl(c, st), true
	}
	placeByClass["BaseCoralWallFanBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return tryHorizontal(c, withBool(def, "waterlogged", c.fullWaterAt(c.pos)), true)
	}
	placeByClass["BaseRailBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		h := c.horizontal()
		shape := "north_south"
		if h == pEast || h == pWest {
			shape = "east_west"
		}
		return wl(c, with(def, "shape", shape)), true
	}
	placeByClass["BedBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		h := c.horizontal()
		head := h.rel(c.pos)
		if c.canBeReplaced(c.at(head)) && withinWorldBorder(c.s, c.d, head) {
			return with(def, "facing", h.String()), true
		}
		return 0, false
	}
	placeByClass["BellBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		p := c.pos
		if c.face.axis() == 'y' {
			att := "floor"
			if c.face == pDown {
				att = "ceiling"
			}
			st := with(with(def, "attachment", att), "facing", c.horizontal().String())
			if c.canSurvive(st, p) {
				return st, true
			}
			return 0, false
		}
		double := false
		if c.face.axis() == 'x' {
			double = sturdy(c.at(pWest.rel(p)), pEast) && sturdy(c.at(pEast.rel(p)), pWest)
		} else {
			double = sturdy(c.at(pNorth.rel(p)), pSouth) && sturdy(c.at(pSouth.rel(p)), pNorth)
		}
		att := "single_wall"
		if double {
			att = "double_wall"
		}
		st := with(with(def, "facing", c.face.opp().String()), "attachment", att)
		if c.canSurvive(st, p) {
			return st, true
		}
		att = "ceiling"
		if sturdy(c.at(posBelow(p, 1)), pUp) {
			att = "floor"
		}
		st = with(st, "attachment", att)
		if c.canSurvive(st, p) {
			return st, true
		}
		return 0, false
	}
	placeByClass["BigDripleafBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		below := c.at(posBelow(c.pos, 1))
		f := c.horizontal().opp().String()
		if n := bstate(below); n == "big_dripleaf" || n == "big_dripleaf_stem" {
			f, _ = world.StateProperty(below, "facing")
		}
		return with(withBool(def, "waterlogged", c.waterAt(c.pos)), "facing", f), true
	}
	placeByClass["SculkSensorBlock"] = wlOnly
	placeByClass["CalibratedSculkSensorBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		return with(wl(c, def), "facing", c.horizontal().String()), true
	}
	placeByClass["CampfireBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		water := c.waterAt(c.pos)
		st := withBool(def, "waterlogged", water)
		st = withBool(st, "signal_fire", bstate(c.at(posBelow(c.pos, 1))) == "hay_block")
		st = withBool(st, "lit", !water)
		return with(st, "facing", c.horizontal().String()), true
	}
	placeByClass["CandleBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		if cur := c.at(c.pos); bstate(cur) == name {
			return with(cur, "candles", strconv.Itoa(propInt(cur, "candles")%4+1)), true
		}
		return wl(c, def), true
	}
	placeByClass["CeilingHangingSignBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		above := c.at(posAbove(c.pos, 1))
		hanging := worldgen.InBlockTag(above, "all_hanging_signs")
		dir := pdirFromYRot(float64(c.yaw))
		attached := !worldgen.CollisionFaceFull(above, int(pDown)) || c.sneak
		if hanging && !c.sneak {
			if f, ok := world.StateProperty(above, "facing"); ok && hasClassName(bstate(above), "WallHangingSignBlock") {
				if fd, ok := pdirFromName(f); ok && fd.axis() == dir.axis() {
					attached = false
				}
			} else if r, ok := world.StateProperty(above, "rotation"); ok {
				rv, _ := strconv.Atoi(r)
				if rd, ok := rotationToDir(rv); ok && rd.axis() == dir.axis() {
					attached = false
				}
			}
		}
		seg := rotationSegment(c.yaw + 180)
		if !attached {
			seg = dir.opp().data2d() << 2
		}
		st := withBool(def, "attached", attached)
		st = with(st, "rotation", strconv.Itoa(seg))
		return wl(c, st), true
	}
	placeByClass["ChainBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st, _ := pillar(c, name, def)
		return wl(c, st), true
	}
	placeByClass["ChestBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return wl(c, c.s.chestPlaceState(c.d, c.pos, def, c.sess, int32(c.face))), true
	}
	placeByClass["ChorusPlantBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		is := func(b world.Block) bool { n := bstate(b); return n == name || n == "chorus_flower" }
		st := def
		below := c.at(posBelow(c.pos, 1))
		st = withBool(st, "down", is(below) || worldgen.InBlockTag(below, "supports_chorus_plant"))
		st = withBool(st, "up", is(c.at(posAbove(c.pos, 1))))
		for _, d := range []pdir{pNorth, pEast, pSouth, pWest} {
			st = withBool(st, d.String(), is(c.at(d.rel(c.pos))))
		}
		return st, true
	}
	placeByClass["CocoaBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return tryHorizontal(c, def, false)
	}
	placeByClass["ConcretePowderBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		if concreteSolidifies(c, c.at(c.pos)) {
			b, _ := world.BlockState(strings.TrimSuffix(name, "_powder"))
			return b, true
		}
		return def, true
	}
	placeByClass["CoralBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		// The dying tick (60 + random 40 without water nearby) is the
		// coral random/scheduled path's business once placed.
		return def, true
	}
	placeByClass["CrafterBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return c.s.crafterPlaceState(c.d, c.pos, def, c.sess), true
	}
	placeByClass["CreakingHeartBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st := with(def, "axis", axisOf(c.face))
		// updateState: with its logs in place an uprooted heart wakes
		// (AWAKE while the creaking_active environment attribute holds,
		// else DORMANT). The attribute is approximated by night in the
		// sense of isBrightOutside.
		if creakingLogsOK(c, st) && propIs(st, "creaking_heart_state", "uprooted") {
			v := "dormant"
			if !c.s.isBrightOutside() {
				v = "awake"
			}
			st = with(st, "creaking_heart_state", v)
		}
		return st, true
	}
	placeByClass["DiodeBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return with(def, "facing", hzOpp(c).String()), true
	}
	placeByClass["RepeaterBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st := with(def, "facing", hzOpp(c).String())
		return withBool(st, "locked", rsDiodeLocked(c.d, c.pos, name, st)), true
	}
	placeByClass["DirtPathBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		if !c.canSurvive(def, c.pos) {
			b, _ := world.BlockState("dirt")
			return b, true
		}
		return def, true
	}
	placeByClass["FarmlandBlock"] = placeByClass["DirtPathBlock"]
	placeByClass["DoorBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		above := posAbove(c.pos, 1)
		if c.pos.Y >= c.d.minY+c.d.height-1 || !c.canBeReplaced(c.at(above)) {
			return 0, false
		}
		powered := c.signal(c.pos) || c.signal(above)
		st := with(def, "facing", c.horizontal().String())
		st = with(st, "hinge", doorHinge(c))
		st = withBool(st, "powered", powered)
		st = withBool(st, "open", powered)
		return with(st, "half", "lower"), true
	}
	placeByClass["DoublePlantBlock"] = doublePlant
	placeByClass["TallSeagrassBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st, ok := doublePlant(c, name, def)
		if ok && c.fullWaterAt(posAbove(c.pos, 1)) {
			return st, true
		}
		return 0, false
	}
	placeByClass["SmallDripleafBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st, ok := doublePlant(c, name, def)
		if !ok {
			return 0, false
		}
		st = with(st, "facing", hzOpp(c).String())
		return copyWaterlogged(c.d, c.pos, st), true
	}
	placeByClass["PitcherCropBlock"] = func(_ *placeCtx, _ string, def world.Block) (world.Block, bool) { return def, true }
	placeByClass["EndRodBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		back := c.at(c.face.opp().rel(c.pos))
		if bstate(back) == name {
			if f, _ := world.StateProperty(back, "facing"); f == c.face.String() {
				return with(def, "facing", c.face.opp().String()), true
			}
		}
		return with(def, "facing", c.face.String()), true
	}
	placeByClass["FaceAttachedHorizontalDirectionalBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		for _, dir := range c.nearestLookingDirs() {
			var st world.Block
			if dir.axis() == 'y' {
				face := "floor"
				if dir == pUp {
					face = "ceiling"
				}
				st = with(with(def, "face", face), "facing", c.horizontal().String())
			} else {
				st = with(with(def, "face", "wall"), "facing", dir.opp().String())
			}
			if c.canSurvive(st, c.pos) {
				return st, true
			}
		}
		return 0, false
	}
	placeByClass["FenceBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return wl(c, c.s.connectPlaceState(c.d, c.pos, def)), true
	}
	placeByClass["IronBarsBlock"] = placeByClass["FenceBlock"]
	placeByClass["WallBlock"] = placeByClass["FenceBlock"]
	placeByClass["FenceGateBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		p := c.pos
		powered := c.signal(p)
		h := c.horizontal()
		isWall := func(q world.Pos) bool { return worldgen.InBlockTag(c.at(q), "walls") }
		inWall := false
		if h.axis() == 'z' {
			inWall = isWall(pWest.rel(p)) || isWall(pEast.rel(p))
		} else {
			inWall = isWall(pNorth.rel(p)) || isWall(pSouth.rel(p))
		}
		st := with(def, "facing", h.String())
		st = withBool(st, "open", powered)
		st = withBool(st, "powered", powered)
		return withBool(st, "in_wall", inWall), true
	}
	placeByClass["FlowerBedBlock"] = segmentable("flower_amount")
	placeByClass["LeafLitterBlock"] = segmentable("segment_amount")
	placeByClass["GrowingPlantBlock"] = growingPlant
	placeByClass["KelpBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		if !c.fullWaterAt(c.pos) {
			return 0, false
		}
		return growingPlant(c, name, def)
	}
	placeByClass["SeagrassBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		if !c.fullWaterAt(c.pos) {
			return 0, false
		}
		return def, true
	}
	placeByClass["HangingRootsBlock"] = wlOnly
	placeByClass["HopperBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		f := c.face.opp()
		if f.axis() == 'y' {
			f = pDown
		}
		return withBool(with(def, "facing", f.String()), "enabled", true), true
	}
	placeByClass["HugeMushroomBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		st := def
		for _, d := range []pdir{pDown, pUp, pNorth, pEast, pSouth, pWest} {
			st = withBool(st, d.String(), bstate(c.at(d.rel(c.pos))) != name)
		}
		return st, true
	}
	placeByClass["JigsawBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		top := pUp
		if c.face.axis() == 'y' {
			top = c.horizontal().opp()
		}
		return with(def, "orientation", c.face.String()+"_"+top.String()), true
	}
	placeByClass["LadderBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		if !c.replaceClicked {
			back := c.at(c.face.opp().rel(c.pos))
			if bstate(back) == name {
				if f, _ := world.StateProperty(back, "facing"); f == c.face.String() {
					return 0, false
				}
			}
		}
		st, ok := tryHorizontal(c, def, true)
		if !ok {
			return 0, false
		}
		return wl(c, st), true
	}
	placeByClass["LanternBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		for _, dir := range c.nearestLookingDirs() {
			if dir.axis() != 'y' {
				continue
			}
			st := withBool(def, "hanging", dir == pUp)
			if c.canSurvive(st, c.pos) {
				return wl(c, st), true
			}
		}
		return 0, false
	}
	placeByClass["LeavesBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		st := wl(c, withBool(def, "persistent", true))
		return leafPlaceState(c.d, c.pos, st), true
	}
	placeByClass["MangrovePropaguleBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return with(wl(c, def), "age", "4"), true
	}
	placeByClass["MossyCarpetBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return mossyCarpetUpdated(c, def, true), true
	}
	placeByClass["MultifaceBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		st := c.s.multifacePlaceState(c.d, c.pos, def, c.sess, int32(c.face))
		if st == def {
			return 0, false
		}
		return st, true
	}
	placeByClass["NoteBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return noteBlockInstrument(c.d, c.pos, def), true
	}
	placeByClass["PotentSulfurBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return potentSulfurState(c, def), true
	}
	placeByClass["RedStoneWireBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return c.s.rsWireFold(c.d, c.pos, def), true
	}
	placeByClass["RedstoneLampBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return withBool(def, "lit", c.signal(c.pos)), true
	}
	placeByClass["WallTorchBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return tryHorizontal(c, def, true)
	}
	placeByClass["RedstoneWallTorchBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		wt, _ := world.BlockState("wall_torch")
		st, ok := tryHorizontal(c, wt, true)
		if !ok {
			return 0, false
		}
		f, _ := world.StateProperty(st, "facing")
		return with(def, "facing", f), true
	}
	placeByClass["ScaffoldingBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return wl(c, c.s.scaffoldPlaceState(c.d, c.pos, def)), true
	}
	placeByClass["SeaPickleBlock"] = counted("pickles", 4, wlOnly)
	placeByClass["TurtleEggBlock"] = counted("eggs", 4, func(_ *placeCtx, _ string, def world.Block) (world.Block, bool) { return def, true })
	placeByClass["SnowLayerBlock"] = counted("layers", 8, func(_ *placeCtx, _ string, def world.Block) (world.Block, bool) { return def, true })
	placeByClass["SlabBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		if cur := c.at(c.pos); bstate(cur) == name {
			return withBool(with(cur, "type", "double"), "waterlogged", false), true
		}
		st := wl(c, with(def, "type", "bottom"))
		if c.face == pDown || (c.face != pUp && c.loc[1]-float64(c.pos.Y) > 0.5) {
			return with(st, "type", "top"), true
		}
		return st, true
	}
	placeByClass["SnowyBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return c.s.snowyPlaceState(c.d, c.pos, def), true
	}
	placeByClass["SpeleothemBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		want := c.verticalLooking().opp()
		var tip pdir
		switch {
		case speleoValid(c, name, want):
			tip = want
		case speleoValid(c, name, want.opp()):
			tip = want.opp()
		default:
			return 0, false
		}
		step := 1
		if tip == pDown {
			step = -1
		}
		thick := speleoCalcThickness(c.d, c.pos, step, !c.sneak, name)
		st := with(def, "vertical_direction", tip.String())
		st = with(st, "thickness", thick)
		return wl(c, st), true
	}
	placeByClass["StairBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		st := c.s.stairsPlaceState(c.d, c.pos, def, c.sess, int32(c.face), c.loc[1]-float64(c.pos.Y))
		return wl(c, st), true
	}
	placeByClass["TestBlock"] = func(_ *placeCtx, _ string, def world.Block) (world.Block, bool) { return def, true }
	placeByClass["TrapDoorBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		st := def
		if c.replaceClicked || !c.face.horizontal() {
			half := "top"
			if c.face == pUp {
				half = "bottom"
			}
			st = with(with(st, "facing", hzOpp(c).String()), "half", half)
		} else {
			half := "bottom"
			if c.loc[1]-float64(c.pos.Y) > 0.5 {
				half = "top"
			}
			st = with(with(st, "facing", c.face.String()), "half", half)
		}
		if c.signal(c.pos) {
			st = withBool(withBool(st, "open", true), "powered", true)
		}
		return wl(c, st), true
	}
	placeByClass["TripWireBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return c.s.tripwirePlaceState(c.d, c.pos, def), true
	}
	placeByClass["TripWireHookBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		st := withBool(withBool(def, "powered", false), "attached", false)
		return tryHorizontal(c, st, true)
	}
	placeByClass["VineBlock"] = func(c *placeCtx, name string, def world.Block) (world.Block, bool) {
		cur := c.at(c.pos)
		same := bstate(cur) == name
		st := def
		if same {
			st = cur
		}
		for _, dir := range c.nearestLookingDirs() {
			if dir == pDown {
				continue
			}
			if same && propIs(cur, dir.String(), "true") {
				continue
			}
			if c.s.vineCanSupportAtFace(c.d, c.pos, dir.String()) {
				return withBool(st, dir.String(), true), true
			}
		}
		if same {
			return st, true
		}
		return 0, false
	}
	placeByClass["WallBannerBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		return tryHorizontal(c, def, true)
	}
	placeByClass["WallSignBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		st, ok := tryHorizontal(c, def, true)
		if !ok {
			return 0, false
		}
		return wl(c, st), true
	}
	placeByClass["WallHangingSignBlock"] = func(c *placeCtx, _ string, def world.Block) (world.Block, bool) {
		st := def
		for _, dir := range c.nearestLookingDirs() {
			if !dir.horizontal() || dir.axis() == c.face.axis() {
				continue
			}
			st = with(st, "facing", dir.opp().String())
			if c.canSurvive(st, c.pos) && wallHangingCanPlace(c, st) {
				return wl(c, st), true
			}
		}
		return 0, false
	}
}

func propIs(b world.Block, k, v string) bool {
	got, ok := world.StateProperty(b, k)
	return ok && got == v
}

// rotationToDir is RotationSegment.convertToDirection.
func rotationToDir(seg int) (pdir, bool) {
	switch seg {
	case 0:
		return pNorth, true
	case 4:
		return pEast, true
	case 8:
		return pSouth, true
	case 12:
		return pWest, true
	}
	return 0, false
}

// wallHangingCanPlace is WallHangingSignBlock.canPlace: either side
// (clockwise or counter-clockwise of facing) attaches - a wall hanging
// sign on the same axis, or a full sturdy face toward the sign.
func wallHangingCanPlace(c *placeCtx, st world.Block) bool {
	f, _ := world.StateProperty(st, "facing")
	fd, _ := pdirFromName(f)
	attach := func(p world.Pos, toward pdir) bool {
		b := c.at(p)
		if worldgen.InBlockTag(b, "wall_hanging_signs") {
			bf, _ := world.StateProperty(b, "facing")
			bd, _ := pdirFromName(bf)
			return bd.axis() == fd.axis()
		}
		return sturdy(b, toward)
	}
	cw, ccw := fd.cw(), fd.ccw()
	return attach(cw.rel(c.pos), ccw) || attach(ccw.rel(c.pos), cw)
}

// doorHinge is DoorBlock.getHinge: weigh full-collision neighbours on
// each side (both halves), then neighbouring lower door halves, then
// the click position across the door.
func doorHinge(c *placeCtx) string {
	p := c.pos
	h := c.horizontal()
	above := posAbove(p, 1)
	left, right := h.ccw(), h.cw()
	full := func(q world.Pos) int {
		if worldgen.FullCollision(c.at(q)) {
			return 1
		}
		return 0
	}
	score := -full(left.rel(p)) - full(left.rel(above)) + full(right.rel(p)) + full(right.rel(above))
	lowerDoor := func(q world.Pos) bool {
		b := c.at(q)
		return hasClassName(bstate(b), "DoorBlock") && propIs(b, "half", "lower")
	}
	leftDoor, rightDoor := lowerDoor(left.rel(p)), lowerDoor(right.rel(p))
	if (leftDoor && !rightDoor) || score > 0 {
		return "right"
	}
	if (rightDoor && !leftDoor) || score < 0 {
		return "left"
	}
	sx, _, sz := h.step()
	dx, dz := c.loc[0]-float64(p.X), c.loc[2]-float64(p.Z)
	if (sx < 0 && dz < 0.5) || (sx > 0 && dz > 0.5) || (sz < 0 && dx > 0.5) || (sz > 0 && dx < 0.5) {
		return "right"
	}
	return "left"
}

// copyWaterlogged is DoublePlantBlock.copyWaterloggedFrom: waterlogged
// from the water tag at p.
func copyWaterlogged(d *dimension, p world.Pos, st world.Block) world.Block {
	if !world.HasProperty(st, "waterlogged") {
		return st
	}
	return withBool(st, "waterlogged", worldgen.FluidInWaterTag(d.w.GetBlock(p)))
}

// concreteSolidifies is ConcretePowderBlock.shouldSolidify: the cell's
// own fluid is water, or a neighbour with water whose face toward the
// powder is not sturdy (the block below only when it holds water).
func concreteSolidifies(c *placeCtx, here world.Block) bool {
	if worldgen.FluidInWaterTag(here) {
		return true
	}
	for d := pDown; d <= pEast; d++ {
		b := c.at(d.rel(c.pos))
		if d == pDown && !worldgen.FluidInWaterTag(b) {
			continue
		}
		if worldgen.FluidInWaterTag(b) && !sturdy(b, d.opp()) {
			return true
		}
	}
	return false
}

// creakingLogsOK is CreakingHeartBlock.hasRequiredLogs: both cells
// along the axis are pale oak logs on the same axis.
func creakingLogsOK(c *placeCtx, st world.Block) bool {
	ax, _ := world.StateProperty(st, "axis")
	var dirs [2]pdir
	switch ax {
	case "x":
		dirs = [2]pdir{pWest, pEast}
	case "y":
		dirs = [2]pdir{pDown, pUp}
	default:
		dirs = [2]pdir{pNorth, pSouth}
	}
	for _, d := range dirs {
		b := c.at(d.rel(c.pos))
		if !worldgen.InBlockTag(b, "pale_oak_logs") || !propIs(b, "axis", ax) {
			return false
		}
	}
	return true
}

// mossyCarpetUpdated is MossyCarpetBlock.getUpdatedState with
// createSides: each horizontal side low where the carpet can attach
// (tall under a pale moss carpet that has that side and no base), cut
// to none over a base-less carpet below without it.
func mossyCarpetUpdated(c *placeCtx, st world.Block, create bool) world.Block {
	create = create || propIs(st, "bottom", "true")
	var above, below world.Block
	haveAbove, haveBelow := false, false
	for _, d := range []pdir{pNorth, pEast, pSouth, pWest} {
		side := "none"
		// MossyCarpetBlock.canSupportAtFace: MultifaceBlock.canAttachTo.
		if multifaceAttaches(c.d, c.pos, d) {
			if create {
				side = "low"
			} else {
				side, _ = world.StateProperty(st, d.String())
			}
		}
		if side == "low" {
			if !haveAbove {
				above, haveAbove = c.at(posAbove(c.pos, 1)), true
			}
			if bstate(above) == "pale_moss_carpet" && !propIs(above, d.String(), "none") && !propIs(above, "bottom", "true") {
				side = "tall"
			}
			if !propIs(st, "bottom", "true") {
				if !haveBelow {
					below, haveBelow = c.at(posBelow(c.pos, 1)), true
				}
				if bstate(below) == "pale_moss_carpet" && propIs(below, d.String(), "none") {
					side = "none"
				}
			}
		}
		st = with(st, d.String(), side)
	}
	return st
}

// noteBlockInstrument is NoteBlock.setInstrument: the block above's
// instrument when it works above a note block, else the block below's
// (harp in place of a head instrument).
func noteBlockInstrument(d *dimension, p world.Pos, st world.Block) world.Block {
	if ins, ok := blockInstruments[bstate(d.w.GetBlock(posAbove(p, 1)))]; ok && ins.above {
		return with(st, "instrument", ins.name)
	}
	ins := blockInstruments[bstate(d.w.GetBlock(posBelow(p, 1)))]
	name := ins.name
	if ins.above || name == "" {
		name = "harp"
	}
	return with(st, "instrument", name)
}

// potentSulfurState is PotentSulfurBlock.validBlockState: dry without a
// water source above; continuous over a continuous-geyser block that is
// a source if fluid; dormant over a periodic one; else wet.
func potentSulfurState(c *placeCtx, st world.Block) world.Block {
	above := c.at(posAbove(c.pos, 1))
	if !(worldgen.FluidIsWater(above) && worldgen.FluidAmount(above) == 8) {
		return with(st, "state", "dry")
	}
	below := c.at(posBelow(c.pos, 1))
	sourceIfFluid := !worldgen.HasFluid(below) || worldgen.FluidAmount(below) == 8
	if worldgen.InBlockTag(below, "causes_continuous_geyser_eruptions") && sourceIfFluid {
		return with(st, "state", "continuous")
	}
	if worldgen.InBlockTag(below, "causes_periodic_geyser_eruptions") && sourceIfFluid {
		if propIs(st, "state", "erupting") {
			return st
		}
		return with(st, "state", "dormant")
	}
	return with(st, "state", "wet")
}

// speleoValid is SpeleothemBlock.isValidSpeleothemPlacement for a tip
// pointing dir.
func speleoValid(c *placeCtx, name string, dir pdir) bool {
	st, _ := world.BlockState(name)
	st = with(st, "vertical_direction", dir.String())
	return c.canSurvive(st, c.pos)
}

// growingFamily names a GrowingPlantBlock's head, body and growth
// direction.
type growingFamily struct {
	head, body string
	growth     pdir
}

var growingFamilies = map[string]growingFamily{
	"kelp": {"kelp", "kelp_plant", pUp}, "kelp_plant": {"kelp", "kelp_plant", pUp},
	"cave_vines": {"cave_vines", "cave_vines_plant", pDown}, "cave_vines_plant": {"cave_vines", "cave_vines_plant", pDown},
	"weeping_vines": {"weeping_vines", "weeping_vines_plant", pDown}, "weeping_vines_plant": {"weeping_vines", "weeping_vines_plant", pDown},
	"twisting_vines": {"twisting_vines", "twisting_vines_plant", pUp}, "twisting_vines_plant": {"twisting_vines", "twisting_vines_plant", pUp},
}

// multifaceAttaches is MultifaceBlock.canAttachTo toward dir from p.
func multifaceAttaches(d *dimension, p world.Pos, dir pdir) bool {
	return worldgen.MultifaceCanAttach(levelGetter(d), worldgen.BlockPos{int32(p.X), int32(p.Y), int32(p.Z)}, int(dir))
}

// levelGetter adapts a dimension for worldgen's block facts.
func levelGetter(d *dimension) worldgen.FuncGetter {
	return worldgen.FuncGetter{
		Get: func(x, y, z int32) world.Block { return d.w.GetBlock(world.Pos{X: int(x), Y: int(y), Z: int(z)}) },
		Light: func(q worldgen.BlockPos) int32 {
			wp := world.Pos{X: int(q[0]), Y: int(q[1]), Z: int(q[2])}
			return int32(max(skyEffective(d, wp), blkComputedAt(d, wp)))
		},
	}
}

// withinWorldBorder is WorldBorder.isWithinBounds(BlockPos): the
// block's x/z inside [min, max) of the dimension's border.
func withinWorldBorder(s *Server, d *dimension, p world.Pos) bool {
	b := s.borderFor(d.name)
	if b == nil {
		return true
	}
	s.borderMu.Lock()
	half, cx, cz := b.size/2, b.centerX, b.centerZ
	s.borderMu.Unlock()
	x, z := float64(p.X), float64(p.Z)
	return x >= cx-half && x < cx+half && z >= cz-half && z < cz+half
}
