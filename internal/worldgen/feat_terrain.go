package worldgen

import (
	"encoding/json"
	"sync"

	"mc-go/internal/world"
)

// Terrain-shaping features: lakes, disks, springs, monster rooms,
// snow/ice freezing. Each place function cites the measured vanilla
// class; the random draws happen in the same order as the bytecode.

var (
	caveAir      = sync.OnceValue(func() world.Block { return blockNamed("cave_air") })
	iceBlock     = sync.OnceValue(func() world.Block { return blockNamed("ice") })
	snowLayer    = sync.OnceValue(func() world.Block { return blockNamed("snow") })
	cobbleBlock  = sync.OnceValue(func() world.Block { return blockNamed("cobblestone") })
	mossyCobble  = sync.OnceValue(func() world.Block { return blockNamed("mossy_cobblestone") })
	chestBlock   = sync.OnceValue(func() world.Block { return blockNamed("chest") })
	spawnerBlock = sync.OnceValue(func() world.Block { return blockNamed("spawner") })
)

func (rg *Region) at(p BlockPos) world.Block { return rg.Block(p[0], p[1], p[2]) }

func (rg *Region) set(p BlockPos, b world.Block) bool { return rg.SetBlock(p[0], p[1], p[2], b) }

// markPostProcess is ChunkAccess.markPosForPostProcessing.
func (rg *Region) markPostProcess(p BlockPos) {
	if c := rg.chunkAt(p[0], p[2]); c != nil {
		c.PostProcess = append(c.PostProcess, p)
	}
}

// markAbove is Feature.markAboveForPostProcessing: up to two non-air
// cells above p are queued for post-processing, stopping at air.
func markAbove(rg *Region, p BlockPos) {
	for i := int32(1); i <= 2; i++ {
		q := offset(p, 0, i, 0)
		if isAir(rg.at(q)) {
			return
		}
		rg.markPostProcess(q)
	}
}

// postProcessAbove lists the blocks whose getPostProcessPos is the
// cell above (Blocks.postProcessAbove); Region.SetBlock records the
// written cell itself, so the post-process pass shifts these marks.
var postProcessAbove = map[string]bool{"magma_block": true, "soul_sand": true}

// PostProcessBubbleColumns runs the LiquidBlock branch of
// LevelChunk.postProcessGeneration for a decorated chunk: every marked
// water source block gets LiquidBlock.tick, which rebuilds the bubble
// column standing on the block below it (BubbleColumnBlock.updateColumn:
// drag-down over magma, push-up over soul sand, continuing upward
// through full water sources). Fluid ticks and neighbour shape updates
// of the same pass are not modeled.
func PostProcessBubbleColumns(c *ProtoChunk) {
	for _, m := range c.PostProcess {
		x, y, z := m[0]&15, m[1], m[2]&15
		if n, _ := world.StateOwner(c.Get(x, y, z)); postProcessAbove[n] {
			y++
		}
		st := c.Get(x, y, z)
		if !isBlock(st, "water") || !bubbleOccupiable(st) {
			continue
		}
		below := c.Get(x, y-1, z)
		var col world.Block
		switch {
		case isBlock(below, "bubble_column"):
			col = below
		case blockInTag(below, "enables_bubble_column_push_up"):
			col, _ = world.BlockStateOf("bubble_column", map[string]string{"drag": "false"})
		case blockInTag(below, "enables_bubble_column_drag_down"):
			col, _ = world.BlockStateOf("bubble_column", map[string]string{"drag": "true"})
		default:
			col = st
		}
		for yy := y; yy < c.MinY+c.Height && bubbleOccupiable(c.Get(x, yy, z)); yy++ {
			c.Set(x, yy, z, col)
		}
	}
}

// bubbleOccupiable is BubbleColumnBlock.canOccupy: a bubble column, or
// a full water source liquid block.
func bubbleOccupiable(b world.Block) bool {
	if isBlock(b, "bubble_column") {
		return true
	}
	return isBlock(b, "water") && isFluidSource(b) && fluidAmount(b) >= 8
}

// replaceableByFeatures is Feature.isReplaceable(FEATURES_CANNOT_REPLACE).
func replaceableByFeatures(b world.Block) bool { return !blockInTag(b, "features_cannot_replace") }

// safeSet is Feature.safeSetBlock.
func safeSet(rg *Region, p BlockPos, b world.Block, ok func(world.Block) bool) {
	if ok(rg.at(p)) {
		rg.set(p, b)
	}
}

func (b *BiomeInfo) hasPrecipitation() bool {
	raw, ok := b.raw["has_precipitation"]
	if !ok {
		return true
	}
	var v bool
	jsonUnmarshal(raw, &v)
	return v
}

func (rg *Region) seaLevel() int32 { return rg.g.RS.settings.seaLevel }

func (rg *Region) inBuildHeight(y int32) bool { return y >= rg.MinY() && y <= rg.MaxY() }

// blockLight is the block-light layer during features: nothing is lit
// before the light stage, so every cell reads 0.
func blockLight(*Region, BlockPos) int32 { return 0 }

// shouldFreeze is Biome.shouldFreeze(level, pos, mustBeAtEdge).
func shouldFreeze(rg *Region, b *BiomeInfo, p BlockPos, edge bool) bool {
	if !b.ColdEnoughToSnow(p[0], p[1], p[2], rg.seaLevel()) {
		return false
	}
	if !rg.inBuildHeight(p[1]) || blockLight(rg, p) >= 10 {
		return false
	}
	st := rg.at(p)
	if !(isBlock(st, "water") && isFluidSource(st)) {
		return false
	}
	if !edge {
		return true
	}
	surrounded := isWaterAt(rg.at(offset(p, -1, 0, 0))) && isWaterAt(rg.at(offset(p, 1, 0, 0))) &&
		isWaterAt(rg.at(offset(p, 0, 0, -1))) && isWaterAt(rg.at(offset(p, 0, 0, 1)))
	return !surrounded
}

// shouldSnow is Biome.shouldSnow.
func shouldSnow(rg *Region, b *BiomeInfo, p BlockPos) bool {
	if !b.hasPrecipitation() || !b.ColdEnoughToSnow(p[0], p[1], p[2], rg.seaLevel()) {
		return false
	}
	if !rg.inBuildHeight(p[1]) || blockLight(rg, p) >= 10 {
		return false
	}
	st := rg.at(p)
	if !isAir(st) && !isBlock(st, "snow") {
		return false
	}
	return canSurvive(rg, snowLayer(), p)
}

// collisionFaceFullUp approximates Block.isFaceFull(collision shape,
// UP): full-cube collision, or a slab whose top half is filled.
func collisionFaceFullUp(b world.Block) bool {
	if fullCollision(b) {
		return true
	}
	if t, ok := world.StateProperty(b, "type"); ok && hasClassState(b, "SlabBlock") {
		return t == "top" || t == "double"
	}
	return false
}

func hasClassState(b world.Block, class string) bool {
	n, _ := world.StateOwner(b)
	return hasClass(n, class)
}

func init() {
	// SnowLayerBlock.canSurvive.
	surviveByClass["SnowLayerBlock"] = func(rg *Region, state world.Block, p BlockPos) bool {
		below := rg.at(offset(p, 0, -1, 0))
		if blockInTag(below, "cannot_support_snow_layer") {
			return false
		}
		if blockInTag(below, "support_override_snow_layer") {
			return true
		}
		if collisionFaceFullUp(below) {
			return true
		}
		l, _ := world.StateProperty(below, "layers")
		return isBlock(below, "snow") && l == "8"
	}

	featureBuilders["freeze_top_layer"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return placeFreezeTopLayer(c.rg, p) }
	}
	featureBuilders["disk"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		d := &diskConfig{
			state:  g.compileStateProvider(cfg["state_provider"]),
			target: g.compilePredicate(cfg["target"]),
			radius: parseIntProvider(cfg["radius"]),
			half:   cfg.i32("half_height"),
		}
		return func(c *featCtx, r Random, p BlockPos) bool { return placeDisk(c.rg, r, d, p) }
	}
	featureBuilders["lake"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		l := &lakeConfig{
			fluid:           g.compileStateProvider(cfg["fluid"]),
			barrier:         g.compileStateProvider(cfg["barrier"]),
			canPlace:        g.compilePredicate(cfg["can_place_feature"]),
			canReplaceFluid: g.compilePredicate(cfg["can_replace_with_air_or_fluid"]),
			canReplaceWall:  g.compilePredicate(cfg["can_replace_with_barrier"]),
		}
		return func(c *featCtx, r Random, p BlockPos) bool { return placeLake(c.rg, r, l, p) }
	}
	featureBuilders["spring_feature"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		s := &springConfig{requiresBelow: true, rocks: 4, holes: 1, valid: blockNameSet(cfg["valid_blocks"])}
		var st obj
		jsonUnmarshal(cfg["state"], &st)
		s.state = fluidLegacyBlock(st)
		if v, ok := cfg["requires_block_below"]; ok {
			jsonUnmarshal(v, &s.requiresBelow)
		}
		if _, ok := cfg["rock_count"]; ok {
			s.rocks = cfg.i32("rock_count")
		}
		if _, ok := cfg["hole_count"]; ok {
			s.holes = cfg.i32("hole_count")
		}
		return func(c *featCtx, r Random, p BlockPos) bool { return placeSpring(c.rg, s, p) }
	}
	featureBuilders["monster_room"] = func(g *Generator, cfg obj) func(*featCtx, Random, BlockPos) bool {
		return func(c *featCtx, r Random, p BlockPos) bool { return placeMonsterRoom(c.rg, r, p) }
	}
}

// placeFreezeTopLayer is SnowAndFreezeFeature.place: per column of the
// chunk, the cell under the MOTION_BLOCKING top freezes to ice and the
// top cell takes a snow layer, judged by the biome at the top cell.
func placeFreezeTopLayer(rg *Region, origin BlockPos) bool {
	for i := int32(0); i < 16; i++ {
		for j := int32(0); j < 16; j++ {
			x, z := origin[0]+i, origin[2]+j
			top := BlockPos{x, rg.Height(HMMotionBlocking, x, z), z}
			below := offset(top, 0, -1, 0)
			b := rg.g.RS.BiomeInfo(rg.Biome(top[0], top[1], top[2]))
			if shouldFreeze(rg, b, below, false) {
				rg.set(below, iceBlock())
			}
			if shouldSnow(rg, b, top) {
				rg.set(top, snowLayer())
				st := rg.at(below)
				if world.HasProperty(st, "snowy") {
					if s, ok := world.WithProperty(st, "snowy", "true"); ok {
						rg.set(below, s)
					}
				}
			}
		}
	}
	return true
}

type diskConfig struct {
	state  stateProvider
	target blockPredicate
	radius intProvider
	half   int32
}

// placeDisk is DiskFeature.place: a radius drawn once, then every
// column within the circle (BlockPos.betweenClosed order: x fastest,
// then z) replaced top-down where the target predicate holds.
func placeDisk(rg *Region, r Random, d *diskConfig, p BlockPos) bool {
	placed := false
	top := p[1] + d.half
	bottom := p[1] - d.half - 1
	rad := d.radius(r)
	for z := p[2] - rad; z <= p[2]+rad; z++ {
		for x := p[0] - rad; x <= p[0]+rad; x++ {
			dx, dz := x-p[0], z-p[2]
			if dx*dx+dz*dz > rad*rad {
				continue
			}
			if diskColumn(rg, r, d, top, bottom, x, z) {
				placed = true
			}
		}
	}
	return placed
}

// diskColumn is DiskFeature.placeColumn.
func diskColumn(rg *Region, r Random, d *diskConfig, top, bottom, x, z int32) bool {
	placed, prevPlaced := false, false
	for y := top; y > bottom; y-- {
		q := BlockPos{x, y, z}
		if !d.target(rg, q) {
			prevPlaced = false
			continue
		}
		st, ok := d.state(rg, r, q)
		if !ok {
			continue
		}
		rg.set(q, st)
		if !prevPlaced {
			markAbove(rg, q)
		}
		placed, prevPlaced = true, true
	}
	return placed
}

type lakeConfig struct {
	fluid, barrier                            stateProvider
	canPlace, canReplaceFluid, canReplaceWall blockPredicate
}

// placeLake is LakeFeature.place: 4-7 random ellipsoids carve a 16x8x16
// mask; the mask must be enclosed (no liquid above y=4, solid or the
// fluid itself below it, can_place_feature everywhere on its shell),
// then the lower half fills with the fluid and the upper half with
// cave air, the shell becomes the barrier (above y=4 only half the
// time) and water lakes freeze at y=4 where the biome freezes.
func placeLake(rg *Region, r Random, l *lakeConfig, origin BlockPos) bool {
	if origin[1] <= rg.MinY()+4 {
		return false
	}
	o := offset(origin, -8, -4, -8)
	var mask [2048]bool
	idx := func(x, y, z int32) int32 { return (x*16+z)*8 + y }
	n := r.NextIntN(4) + 4
	for i := int32(0); i < n; i++ {
		sx := float64(r.NextDouble()*6) + 3
		sy := float64(r.NextDouble()*4) + 2
		sz := float64(r.NextDouble()*6) + 3
		cx := float64(float64(r.NextDouble()*float64(16-sx-2))+1) + sx/2
		cy := float64(float64(r.NextDouble()*float64(8-sy-4))+2) + sy/2
		cz := float64(float64(r.NextDouble()*float64(16-sz-2))+1) + sz/2
		for x := int32(1); x < 15; x++ {
			for z := int32(1); z < 15; z++ {
				for y := int32(1); y < 7; y++ {
					dx := (float64(x) - cx) / (sx / 2)
					dy := (float64(y) - cy) / (sy / 2)
					dz := (float64(z) - cz) / (sz / 2)
					if float64(dx*dx)+float64(dy*dy)+float64(dz*dz) < 1 {
						mask[idx(x, y, z)] = true
					}
				}
			}
		}
	}
	shell := func(x, y, z int32) bool {
		if mask[idx(x, y, z)] {
			return false
		}
		return (x < 15 && mask[idx(x+1, y, z)]) || (x > 0 && mask[idx(x-1, y, z)]) ||
			(z < 15 && mask[idx(x, y, z+1)]) || (z > 0 && mask[idx(x, y, z-1)]) ||
			(y < 7 && mask[idx(x, y+1, z)]) || (y > 0 && mask[idx(x, y-1, z)])
	}
	fluid := l.fluid.getState(rg, r, o)
	for x := int32(0); x < 16; x++ {
		for z := int32(0); z < 16; z++ {
			for y := int32(0); y < 8; y++ {
				if !shell(x, y, z) {
					continue
				}
				q := offset(o, x, y, z)
				st := rg.at(q)
				if y >= 4 && props(st)&(1<<bpLiquidBlock) != 0 {
					return false
				}
				if y < 4 && !isSolidState(st) && st != fluid {
					return false
				}
				if !l.canPlace(rg, q) {
					return false
				}
			}
		}
	}
	for x := int32(0); x < 16; x++ {
		for z := int32(0); z < 16; z++ {
			for y := int32(0); y < 8; y++ {
				if !mask[idx(x, y, z)] {
					continue
				}
				q := offset(o, x, y, z)
				if !l.canReplaceFluid(rg, q) {
					continue
				}
				air := y >= 4
				if air {
					rg.set(q, caveAir())
					markAbove(rg, q)
				} else {
					rg.set(q, fluid)
				}
			}
		}
	}
	wall := l.barrier.getState(rg, r, o)
	if !isAir(wall) {
		for x := int32(0); x < 16; x++ {
			for z := int32(0); z < 16; z++ {
				for y := int32(0); y < 8; y++ {
					if !shell(x, y, z) {
						continue
					}
					if y >= 4 && r.NextIntN(2) == 0 {
						continue
					}
					q := offset(o, x, y, z)
					if isSolidState(rg.at(q)) && l.canReplaceWall(rg, q) {
						rg.set(q, wall)
						markAbove(rg, q)
					}
				}
			}
		}
	}
	if inFluidTag(fluid, "water") {
		for x := int32(0); x < 16; x++ {
			for z := int32(0); z < 16; z++ {
				q := offset(o, x, 4, z)
				b := rg.g.RS.BiomeInfo(rg.Biome(q[0], q[1], q[2]))
				if shouldFreeze(rg, b, q, false) && l.canReplaceFluid(rg, q) {
					rg.set(q, iceBlock())
				}
			}
		}
	}
	return true
}

// fluidLegacyBlock is FluidState.createLegacyBlock for a fluid state
// given as {"Name": fluid, "Properties": {...}}: sources become the
// liquid block at level 0.
func fluidLegacyBlock(o obj) world.Block {
	name := trimNS(o.str("Name"))
	switch name {
	case "flowing_water", "flowing_lava":
		var props map[string]string
		if raw, ok := o["Properties"]; ok {
			jsonUnmarshal(raw, &props)
		}
		amount := 0
		if v, ok := props["level"]; ok {
			json.Unmarshal([]byte(v), &amount)
		}
		lvl := 8 - min(amount, 8)
		if props["falling"] == "true" {
			lvl += 8
		}
		b, _ := world.BlockStateOf(name[len("flowing_"):], map[string]string{"level": itoa(lvl)})
		return b
	}
	return blockNamed(name)
}

type springConfig struct {
	state         world.Block
	requiresBelow bool
	rocks, holes  int32
	valid         map[string]bool
}

// placeSpring is SpringFeature.place: the cell above (and, when
// required, below) must be a valid rock, the cell itself air or rock,
// and exactly rock_count rock neighbours and hole_count air
// neighbours among west, east, north, south and below.
func placeSpring(rg *Region, s *springConfig, p BlockPos) bool {
	if !inTag(rg.at(offset(p, 0, 1, 0)), s.valid) {
		return false
	}
	if s.requiresBelow && !inTag(rg.at(offset(p, 0, -1, 0)), s.valid) {
		return false
	}
	st := rg.at(p)
	if !isAir(st) && !inTag(st, s.valid) {
		return false
	}
	near := [5]BlockPos{offset(p, -1, 0, 0), offset(p, 1, 0, 0), offset(p, 0, 0, -1), offset(p, 0, 0, 1), offset(p, 0, -1, 0)}
	rocks, holes := int32(0), int32(0)
	for _, q := range near {
		if inTag(rg.at(q), s.valid) {
			rocks++
		}
	}
	for _, q := range near {
		if isAir(rg.at(q)) {
			holes++
		}
	}
	if rocks == s.rocks && holes == s.holes {
		rg.set(p, s.state)
		return true
	}
	return false
}

// placeMonsterRoom is MonsterRoomFeature.place (block states only; the
// chest loot seed and spawner mob draws are consumed but not stored).
func placeMonsterRoom(rg *Region, r Random, o BlockPos) bool {
	ok := replaceableByFeatures
	rx := r.NextIntN(2) + 2
	x0, x1 := -rx-1, rx+1
	rz := r.NextIntN(2) + 2
	z0, z1 := -rz-1, rz+1
	holes := 0
	for dx := x0; dx <= x1; dx++ {
		for dy := int32(-1); dy <= 4; dy++ {
			for dz := z0; dz <= z1; dz++ {
				q := offset(o, dx, dy, dz)
				solid := isSolidState(rg.at(q))
				if dy == -1 && !solid {
					return false
				}
				if dy == 4 && !solid {
					return false
				}
				if (dx == x0 || dx == x1 || dz == z0 || dz == z1) && dy == 0 &&
					isAir(rg.at(q)) && isAir(rg.at(offset(q, 0, 1, 0))) {
					holes++
				}
			}
		}
	}
	if holes < 1 || holes > 5 {
		return false
	}
	for dx := x0; dx <= x1; dx++ {
		for dy := int32(3); dy >= -1; dy-- {
			for dz := z0; dz <= z1; dz++ {
				q := offset(o, dx, dy, dz)
				st := rg.at(q)
				if dx == x0 || dy == -1 || dz == z0 || dx == x1 || dy == 4 || dz == z1 {
					if q[1] >= rg.MinY() && !isSolidState(rg.at(offset(q, 0, -1, 0))) {
						rg.set(q, caveAir())
					} else if isSolidState(st) && !isBlock(st, "chest") {
						if dy == -1 && r.NextIntN(4) != 0 {
							safeSet(rg, q, mossyCobble(), ok)
						} else {
							safeSet(rg, q, cobbleBlock(), ok)
						}
					}
				} else if !isBlock(st, "chest") && !isBlock(st, "spawner") {
					safeSet(rg, q, caveAir(), ok)
				}
			}
		}
	}
	for i := 0; i < 2; i++ {
		for k := 0; k < 3; k++ {
			x := o[0] + r.NextIntN(rx*2+1) - rx
			z := o[2] + r.NextIntN(rz*2+1) - rz
			q := BlockPos{x, o[1], z}
			if !isAir(rg.at(q)) {
				continue
			}
			walls := 0
			for _, d := range horizontalDirs {
				off := dirOffsets[d]
				if isSolidState(rg.at(offset(q, off[0], off[1], off[2]))) {
					walls++
				}
			}
			if walls != 1 {
				continue
			}
			safeSet(rg, q, reorientChest(rg, q, chestBlock()), ok)
			// RandomizableContainer.setBlockEntityLootTable: the chest's
			// block entity takes a loot seed.
			if isBlock(rg.at(q), "chest") {
				r.NextLong()
			}
			break
		}
	}
	safeSet(rg, o, spawnerBlock(), ok)
	// MonsterRoomFeature.randomEntityId: one of four mobs for the
	// spawner's block entity.
	if isBlock(rg.at(o), "spawner") {
		r.NextIntN(4)
	}
	return true
}

var dirNameOf = [6]string{"down", "up", "north", "south", "west", "east"}

// clockwise is Direction.getClockWise for horizontal directions.
func clockwise(d int) int {
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

func solidRender(b world.Block) bool { return props(b)&(1<<bpSolidRender) != 0 }

// reorientChest is StructurePiece.reorient: a chest faces away from its
// single solid-render neighbour; with none or several, it turns from
// its default facing until it stops facing a solid-render block.
func reorientChest(rg *Region, p BlockPos, st world.Block) world.Block {
	found := -1
	for _, d := range horizontalDirs {
		off := dirOffsets[d]
		n := rg.at(offset(p, off[0], off[1], off[2]))
		if isBlock(n, "chest") {
			return st
		}
		if solidRender(n) {
			if found < 0 {
				found = d
			} else {
				found = -1
				break
			}
		}
	}
	if found >= 0 {
		b, _ := world.WithProperty(st, "facing", dirNameOf[oppositeDir(found)])
		return b
	}
	f := dirNames["north"]
	if s, ok := world.StateProperty(st, "facing"); ok {
		f = dirNames[s]
	}
	rel := func(d int) world.Block {
		off := dirOffsets[d]
		return rg.at(offset(p, off[0], off[1], off[2]))
	}
	if solidRender(rel(f)) {
		f = oppositeDir(f)
	}
	if solidRender(rel(f)) {
		f = clockwise(f)
	}
	if solidRender(rel(f)) {
		f = oppositeDir(f)
	}
	b, _ := world.WithProperty(st, "facing", dirNameOf[f])
	return b
}
