package server

import (
	"math"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// isBedBlock reports whether a block state is any of the 16 beds —
// vanilla's BedBlock shares one class, keyed by name suffix.
func isBedBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && strings.HasSuffix(name, "_bed")
}

// bedFacingRel steps pos one block in a bed's facing direction —
// the direction from the foot half toward the head half.
func bedFacingRel(pos world.Pos, facing string) world.Pos {
	switch facing {
	case "north":
		pos.Z--
	case "south":
		pos.Z++
	case "west":
		pos.X--
	case "east":
		pos.X++
	}
	return pos
}

// useBed handles a right-click on a bed. Vanilla's chain is
// BedBlock.useWithoutItem: a foot click redirects to the head and a
// missing head consumes silently; an occupied head shows the
// "bed.occupied" action bar (checked before the sleep window); outside
// the overworld sleep window (daytime mod 24000 in [12542, 23459])
// "bed.no_sleep" shows; otherwise the player enters the bed — spawn
// point set (system chat, not overlay), the arm-swing animation
// broadcast, entity metadata (pose SLEEPING + sleeping_pos head
// position), the HEAD half's occupied flag flipped (a single setBlock,
// riding the section-blocks-update flush), the sleeper's position
// snapped onto the bed (LivingEntity.setPosToBed: center + 0.6875),
// and slept_in_bed fires.
func (s *Server) useBed(sess *Session, clicked world.Pos) {
	d := sess.dimension()
	cur := d.w.GetBlock(clicked)
	facing, _ := world.StateProperty(cur, "facing")
	part, _ := world.StateProperty(cur, "part")
	head, foot := clicked, clicked
	if part == "foot" {
		head = bedFacingRel(clicked, facing)
	} else {
		foot = bedFacingRel(clicked, bedFacingOpposite(facing))
	}
	headB := d.w.GetBlock(head)
	footB := d.w.GetBlock(foot)
	if !isBedBlock(headB) || !isBedBlock(footB) {
		return
	}
	swing := func() {
		// Vanilla emits the arm-swing animation twice for a consumed
		// useWithoutItem — once from the interaction and once from the
		// result echo; both ride EntityAnimation to trackers+self.
		s.broadcastEntityAnimation(nil, sess.entityID.Load(), animSwingMainArm)
		s.broadcastEntityAnimation(nil, sess.entityID.Load(), animSwingMainArm)
	}
	if occ, _ := world.StateProperty(headB, "occupied"); occ == "true" {
		s.sendActionBar(sess, component{translate: "block.minecraft.bed.occupied"})
		swing()
		return
	}
	day := s.dayTime() % 24000
	if day < 12542 || day > 23459 {
		s.sendActionBar(sess, component{translate: "block.minecraft.bed.no_sleep"})
		swing()
		return
	}
	sess.sleeping.Store(true)
	sess.sleepTicks.Store(0)
	sess.sleepingPos.Store(head)
	// "Respawn point set" arrives as system chat with overlay=false in
	// 26.2 — not an action bar — while the skip notice is an action bar.
	s.sendSystemChatComponent(sess, component{translate: "block.minecraft.set_spawn"})
	swing()
	s.broadcastSleepMeta(sess, head, true)
	s.setBedOccupied(d, head, true)
	s.fireSleptInBed(sess)
	// LivingEntity.startSleeping ends with setPosToBed — the sleeper's
	// entity position snaps to the head half's center raised by
	// 0.6875. Trackers learn it through the normal position sync; the
	// sleeper's own client predicts the same snap, so no SyncPos.
	_, _, _, yaw, pitch, og := sess.Pos()
	bx, by, bz := float64(head.X)+0.5, float64(head.Y)+0.6875, float64(head.Z)+0.5
	sess.setPos(bx, by, bz, yaw, pitch, og)
	if eid := sess.entityID.Load(); eid != 0 {
		s.entities.Update(eid, bx, by, bz, yaw, pitch, og)
		s.broadcastEntityTeleport(sess, eid, bx, by, bz, yaw, pitch, og)
	}
}

// bedFacingOpposite flips a bed facing across the axis.
func bedFacingOpposite(facing string) string {
	switch facing {
	case "north":
		return "south"
	case "south":
		return "north"
	case "west":
		return "east"
	case "east":
		return "west"
	}
	return facing
}

// setBedOccupied flips the occupied property on the HEAD half only —
// vanilla's LivingEntity.startSleeping / the stopSleeping lambda each
// do a single setBlock at the sleeping position (the head); the foot
// half keeps its flag untouched. The change rides the tick-end
// section-blocks-update flush like every other block write.
func (s *Server) setBedOccupied(d *dimension, head world.Pos, occupied bool) {
	val := "false"
	if occupied {
		val = "true"
	}
	b := d.w.GetBlock(head)
	nb, ok := world.WithProperty(b, "occupied", val)
	if !ok || nb == b {
		return
	}
	d.w.SetBlock(head, nb)
	s.broadcastBlockState(d, head, nb)
}

// stopSleeping wakes one session. Vanilla's LivingEntity.stopSleeping
// first runs the bed lambda — only when the head half is still a bed:
// the head's occupied flag clears, findStandUpPosition picks a spot,
// and the sleeper's position snaps there with the yaw swung to face
// the bed (xRot zeroed). Then pose returns to standing and
// sleeping_pos clears — the meta broadcast carries both.
func (s *Server) stopSleeping(sess *Session) {
	if !sess.sleeping.Load() {
		return
	}
	sess.sleeping.Store(false)
	sess.sleepTicks.Store(0)
	h, _ := sess.sleepingPos.Load().(world.Pos)
	sess.sleepingPos.Store(world.Pos{})
	d := sess.dimension()
	headB := d.w.GetBlock(h)
	if isBedBlock(headB) {
		facing, _ := world.StateProperty(headB, "facing")
		s.setBedOccupied(d, h, false)
		_, _, _, yaw, _, og := sess.Pos()
		stand, ok := s.findStandUpPosition(d, h, facing, yaw)
		sx, sy, sz := float64(h.X)+0.5, float64(h.Y)+1.1, float64(h.Z)+0.5
		if ok {
			sx, sy, sz = stand[0], stand[1], stand[2]
		}
		// The wake yaw swings the view toward the bed center:
		// wrapDegrees(atan2(dirZ, dirX)*deg - 90) with dir = bedCenter
		// minus stand position normalized.
		dx, dz := float64(h.X)+0.5-sx, float64(h.Z)+0.5-sz
		n := math.Hypot(dx, dz)
		newYaw := yaw
		if n > 0 {
			newYaw = float32(math.Mod(math.Atan2(dz/n, dx/n)*180/math.Pi-90, 360))
			if newYaw > 180 {
				newYaw -= 360
			} else if newYaw < -180 {
				newYaw += 360
			}
		}
		sess.setPos(sx, sy, sz, newYaw, 0, og)
		if eid := sess.entityID.Load(); eid != 0 {
			s.entities.Update(eid, sx, sy, sz, newYaw, 0, og)
			s.broadcastEntityTeleport(sess, eid, sx, sy, sz, newYaw, 0, og)
		}
	}
	s.broadcastSleepMeta(sess, world.Pos{}, false)
}

// dirStep maps a horizontal facing name to its unit step — vanilla's
// Direction normals (north = -z, south = +z, west = -x, east = +x).
var dirStep = map[string][2]int{
	"north": {0, -1}, "south": {0, 1}, "west": {-1, 0}, "east": {1, 0},
}

// dirClockWise is Direction.getClockWise on the horizontal ring.
var dirClockWise = map[string]string{
	"north": "east", "east": "south", "south": "west", "west": "north",
}

// dirToYRot maps a horizontal direction to vanilla's yaw degrees —
// the angle Direction.get2DDataValue family uses (S=0, W=90, N=180,
// E=270/-90).
func dirToYRot(d string) float64 {
	switch d {
	case "south":
		return 0
	case "west":
		return 90
	case "north":
		return 180
	case "east":
		return -90
	}
	return 0
}

// isFacingAngle mirrors Direction.isFacingAngle: whether the entity's
// look vector (x=-sin yaw, z=cos yaw) points with a positive dot into
// the direction's normal.
func isFacingAngle(dir string, yawDeg float32) bool {
	n := dirStep[dir]
	r := float64(yawDeg) * math.Pi / 180
	return -math.Sin(r)*float64(n[0])+math.Cos(r)*float64(n[1]) > 0
}

// bedWakeOffsets reproduces BedBlock's bedStandUpOffsets: the ten
// around-the-bed candidates of bedSurroundStandUpOffsets (walked from
// the stand side d around the foot toward the head side f) then the
// two bedAboveStandUpOffsets fallbacks (on the head, on the foot).
// Offsets are (dx,dz) relative to the head half at bed level.
func bedWakeOffsets(f, d [2]int) [][2]int {
	return [][2]int{
		{d[0], d[1]},
		{d[0] - f[0], d[1] - f[1]},
		{d[0] - 2*f[0], d[1] - 2*f[1]},
		{-2 * f[0], -2 * f[1]},
		{-d[0] - 2*f[0], -d[1] - 2*f[1]},
		{-d[0] - f[0], -d[1] - f[1]},
		{-d[0], -d[1]},
		{-d[0] + f[0], -d[1] + f[1]},
		{f[0], f[1]},
		{d[0] + f[0], d[1] + f[1]},
		{0, 0},
		{-f[0], -f[1]},
	}
}

// findStandUpPosition is BedBlock.findStandUpPosition, bunk branch
// included. The stand side starts at the bed's clockwise side and
// flips to its opposite when the sleeper's yaw faces that side; each
// candidate walks DismountHelper.findSafeDismountLocation, first with
// the strict danger checks on, then without. isBunkBed (a bed cell
// directly under the head) swaps the scan order — the vanilla bunk
// pass tries the surround ring at the head and one level down, then
// the above cells, before the loose repeats.
func (s *Server) findStandUpPosition(d *dimension, head world.Pos, facing string, yaw float32) ([3]float64, bool) {
	f := dirStep[facing]
	side := dirClockWise[facing]
	if isFacingAngle(side, yaw) {
		side = bedFacingOpposite(side)
	}
	offs := bedWakeOffsets(f, dirStep[side])
	surround, above := offs[:10], offs[10:]
	scan := func(base world.Pos, cand [][2]int, strict bool) ([3]float64, bool) {
		for _, o := range cand {
			p := world.Pos{X: base.X + o[0], Y: base.Y, Z: base.Z + o[1]}
			if v, ok := s.findSafeDismountLocation(d, p, strict); ok {
				return v, true
			}
		}
		return [3]float64{}, false
	}
	belowHead := world.Pos{X: head.X, Y: head.Y - 1, Z: head.Z}
	if isBedBlock(d.w.GetBlock(belowHead)) {
		// findBunkBedStandUpPosition — surround at head, surround at
		// the bunk below, above at head, all strict; then the loose
		// repeats of the first two, then the loose above.
		if v, ok := scan(head, surround, true); ok {
			return v, true
		}
		if v, ok := scan(belowHead, surround, true); ok {
			return v, true
		}
		if v, ok := scan(head, above, true); ok {
			return v, true
		}
		if v, ok := scan(head, surround, false); ok {
			return v, true
		}
		if v, ok := scan(belowHead, surround, false); ok {
			return v, true
		}
		return scan(head, above, false)
	}
	for _, strict := range []bool{true, false} {
		for _, o := range offs {
			p := world.Pos{X: head.X + o[0], Y: head.Y, Z: head.Z + o[1]}
			if v, ok := s.findSafeDismountLocation(d, p, strict); ok {
				return v, true
			}
		}
	}
	return [3]float64{}, false
}

// noCollisionBlocks approximates the empty-collision set beyond the
// fluid-flow "non solid" decorations: attachables and plates that have
// no collision shape in vanilla.
var noCollisionBlocks = map[string]bool{
	"oak_sign": true, "spruce_sign": true, "birch_sign": true, "jungle_sign": true,
	"acacia_sign": true, "dark_oak_sign": true, "mangrove_sign": true,
	"cherry_sign": true, "pale_oak_sign": true, "bamboo_sign": true,
	"crimson_sign": true, "warped_sign": true, "oak_wall_sign": true,
	"spruce_wall_sign": true, "birch_wall_sign": true, "jungle_wall_sign": true,
	"acacia_wall_sign": true, "dark_oak_wall_sign": true, "mangrove_wall_sign": true,
	"cherry_wall_sign": true, "pale_oak_wall_sign": true, "bamboo_wall_sign": true,
	"crimson_wall_sign": true, "warped_wall_sign": true,
	"oak_hanging_sign": true, "spruce_hanging_sign": true, "birch_hanging_sign": true,
	"jungle_hanging_sign": true, "acacia_hanging_sign": true,
	"dark_oak_hanging_sign": true, "mangrove_hanging_sign": true,
	"cherry_hanging_sign": true, "pale_oak_hanging_sign": true,
	"bamboo_hanging_sign": true, "crimson_hanging_sign": true,
	"warped_hanging_sign":   true,
	"oak_wall_hanging_sign": true, "spruce_wall_hanging_sign": true,
	"birch_wall_hanging_sign": true, "jungle_wall_hanging_sign": true,
	"acacia_wall_hanging_sign": true, "dark_oak_wall_hanging_sign": true,
	"mangrove_wall_hanging_sign": true, "cherry_wall_hanging_sign": true,
	"pale_oak_wall_hanging_sign": true, "bamboo_wall_hanging_sign": true,
	"crimson_wall_hanging_sign": true, "warped_wall_hanging_sign": true,
	"white_banner": true, "orange_banner": true, "magenta_banner": true,
	"light_blue_banner": true, "yellow_banner": true, "lime_banner": true,
	"pink_banner": true, "gray_banner": true, "light_gray_banner": true,
	"cyan_banner": true, "purple_banner": true, "blue_banner": true,
	"brown_banner": true, "green_banner": true, "red_banner": true,
	"black_banner": true, "white_wall_banner": true, "orange_wall_banner": true,
	"magenta_wall_banner": true, "light_blue_wall_banner": true,
	"yellow_wall_banner": true, "lime_wall_banner": true, "pink_wall_banner": true,
	"gray_wall_banner": true, "light_gray_wall_banner": true,
	"cyan_wall_banner": true, "purple_wall_banner": true, "blue_wall_banner": true,
	"brown_wall_banner": true, "green_wall_banner": true, "red_wall_banner": true,
	"black_wall_banner": true,
	"stone_button":      true, "oak_button": true, "spruce_button": true,
	"birch_button": true, "jungle_button": true, "acacia_button": true,
	"dark_oak_button": true, "mangrove_button": true, "cherry_button": true,
	"pale_oak_button": true, "bamboo_button": true, "crimson_button": true,
	"warped_button": true, "polished_blackstone_button": true,
	"stone_pressure_plate": true, "oak_pressure_plate": true,
	"spruce_pressure_plate": true, "birch_pressure_plate": true,
	"jungle_pressure_plate": true, "acacia_pressure_plate": true,
	"dark_oak_pressure_plate": true, "mangrove_pressure_plate": true,
	"cherry_pressure_plate": true, "pale_oak_pressure_plate": true,
	"bamboo_pressure_plate": true, "crimson_pressure_plate": true,
	"warped_pressure_plate": true, "polished_blackstone_pressure_plate": true,
	"light_weighted_pressure_plate": true, "heavy_weighted_pressure_plate": true,
	"ladder": true, "end_rod": true, "lightning_rod": true,
	"flower_pot": true, "potted_oak_sapling": true, "potted_spruce_sapling": true,
	"potted_birch_sapling": true, "potted_jungle_sapling": true,
	"potted_acacia_sapling": true, "potted_dark_oak_sapling": true,
	"potted_mangrove_propagule": true, "potted_cherry_sapling": true,
	"potted_pale_oak_sapling": true, "potted_bamboo": true,
	"potted_crimson_fungus": true, "potted_warped_fungus": true,
	"potted_crimson_roots": true, "potted_warped_roots": true,
	"potted_red_mushroom": true, "potted_brown_mushroom": true,
	"potted_fern": true, "potted_dead_bush": true, "potted_cactus": true,
	"potted_dandelion": true, "potted_poppy": true, "potted_blue_orchid": true,
	"potted_allium": true, "potted_azure_bluet": true, "potted_red_tulip": true,
	"potted_orange_tulip": true, "potted_white_tulip": true,
	"potted_pink_tulip": true, "potted_oxeye_daisy": true,
	"potted_cornflower": true, "potted_lily_of_the_valley": true,
	"potted_wither_rose": true, "potted_torchflower": true,
	"potted_open_eyeblossom": true, "potted_closed_eyeblossom": true,
	"pale_hanging_moss": true, "torchflower_crop": true,
	"candle": true, "white_candle": true, "orange_candle": true,
	"magenta_candle": true, "light_blue_candle": true, "yellow_candle": true,
	"lime_candle": true, "pink_candle": true, "gray_candle": true,
	"light_gray_candle": true, "cyan_candle": true, "purple_candle": true,
	"blue_candle": true, "brown_candle": true, "green_candle": true,
	"red_candle": true, "black_candle": true,
	"candle_cake": true, "white_candle_cake": true, "orange_candle_cake": true,
	"magenta_candle_cake": true, "light_blue_candle_cake": true,
	"yellow_candle_cake": true, "lime_candle_cake": true,
	"pink_candle_cake": true, "gray_candle_cake": true,
	"light_gray_candle_cake": true, "cyan_candle_cake": true,
	"purple_candle_cake": true, "blue_candle_cake": true,
	"brown_candle_cake": true, "green_candle_cake": true,
	"red_candle_cake": true, "black_candle_cake": true,
	"conduit": true, "structure_void": true, "pointed_dripstone": false,
	"redstone_wall_torch": true, "soul_wall_torch": true, "wall_torch": true,
	"amethyst_cluster": true, "small_amethyst_bud": true,
	"medium_amethyst_bud": true, "large_amethyst_bud": true,
	"sculk_sensor": false, "calibrated_sculk_sensor": false,
	"sculk_shrieker": false, "turtle_egg": false, "frogspawn": true,
	"sea_pickle": false, "sniffer_egg": false,
}

// collisionInterval approximates a block collision shape as a single
// vertical interval inside its cell — [minY, maxY] in block fractions.
// ok=false means an empty collision shape (vanilla's
// VoxelShape.isEmpty). This models the shapes the stand-up search and
// the bounding-box check actually meet; complex multi-part shapes
// (stairs, hoppers, cauldrons) are approximated by their outer
// vertical extent.
func collisionInterval(b world.Block) ([2]float64, bool) {
	if world.NonSolid(b) {
		return [2]float64{}, false
	}
	name, ok := world.StateOwner(b)
	if !ok {
		return [2]float64{}, false
	}
	if noCollisionBlocks[name] {
		return [2]float64{}, false
	}
	if strings.HasSuffix(name, "_slab") {
		if t, _ := world.StateProperty(b, "type"); t == "top" {
			return [2]float64{0.5, 1.0}, true
		} else if t == "double" {
			return [2]float64{0, 1.0}, true
		}
		return [2]float64{0, 0.5}, true
	}
	if strings.HasSuffix(name, "_carpet") {
		return [2]float64{0, 1.0 / 16}, true
	}
	if name == "snow" {
		if l, _ := world.StateProperty(b, "layers"); l != "" {
			var n int
			for _, c := range l {
				n = n*10 + int(c-'0')
			}
			if n > 0 {
				return [2]float64{0, float64(n) / 8}, true
			}
		}
		return [2]float64{0, 1.0 / 8}, true
	}
	switch name {
	case "farmland", "dirt_path", "honey_block", "cactus":
		return [2]float64{0, 15.0 / 16}, true
	case "soul_sand", "mud":
		return [2]float64{0, 14.0 / 16}, true
	case "chest", "trapped_chest", "ender_chest":
		return [2]float64{0, 14.0 / 16}, true
	case "enchanting_table":
		return [2]float64{0, 12.0 / 16}, true
	case "end_portal_frame":
		return [2]float64{0, 13.0 / 16}, true
	case "daylight_detector":
		return [2]float64{0, 6.0 / 16}, true
	case "lily_pad":
		return [2]float64{0, 1.5 / 16}, true
	case "bed":
	}
	if isBedBlock(b) {
		return [2]float64{0, 9.0 / 16}, true
	}
	if strings.HasSuffix(name, "_fence") || strings.HasSuffix(name, "_wall") ||
		strings.HasSuffix(name, "_fence_gate") || strings.HasSuffix(name, "_pane") ||
		name == "iron_bars" || name == "glass_pane" {
		return [2]float64{0, 1.5}, true
	}
	if strings.HasSuffix(name, "_trapdoor") {
		if o, _ := world.StateProperty(b, "open"); o == "true" {
			return [2]float64{}, false
		}
		return [2]float64{0, 3.0 / 16}, true
	}
	return [2]float64{0, 1.0}, true
}

// burningBlock mirrors NodeEvaluator.isBurningBlock: the FIRE tag
// (fire, soul_fire), lava, magma_block, a lit campfire, or a lava
// cauldron.
func burningBlock(b world.Block) bool {
	name, _ := world.StateOwner(b)
	switch name {
	case "fire", "soul_fire", "lava", "magma_block", "lava_cauldron":
		return true
	case "campfire", "soul_campfire":
		lit, _ := world.StateProperty(b, "lit")
		return lit == "true"
	}
	return false
}

// playerBlockDangerous mirrors EntityType.isBlockDangerous for the
// player type: burning blocks plus wither_rose, sweet_berry_bush,
// cactus and powder_snow (the player has no block immunity and is not
// fire-immune).
func playerBlockDangerous(b world.Block) bool {
	if burningBlock(b) {
		return true
	}
	name, _ := world.StateOwner(b)
	switch name {
	case "wither_rose", "sweet_berry_bush", "cactus", "powder_snow":
		return true
	}
	return false
}

// invalidSpawnInside is the minecraft:invalid_spawn_inside block tag —
// a player can never stand up inside end_portal or end_gateway.
func invalidSpawnInside(b world.Block) bool {
	name, _ := world.StateOwner(b)
	return name == "end_portal" || name == "end_gateway"
}

// findSafeDismountLocation mirrors
// DismountHelper.findSafeDismountLocation for the player type at a
// single candidate cell at bed level. strict carries vanilla's
// requireSafe flag: dangerous-cell and dangerous-floor checks run only
// on the first (strict) pass. The floor height comes from the
// collision top in the candidate cell — or, when that is empty, the
// cell below's top pushed up one (getBlockFloorHeight's two-level
// supplier). A floor at exactly 1.0 or absent fails, then the player
// bounding box (0.6 x 1.8) must clear every block collision shape it
// overlaps, and neither the cell nor the one above may be
// invalid_spawn_inside.
func (s *Server) findSafeDismountLocation(d *dimension, p world.Pos, strict bool) ([3]float64, bool) {
	st := d.w.GetBlock(p)
	if strict && playerBlockDangerous(st) {
		return [3]float64{}, false
	}
	var floor float64
	if iv, ok := collisionInterval(st); ok {
		floor = iv[1]
	} else if iv, ok := collisionInterval(d.w.GetBlock(world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z})); ok {
		floor = iv[1] - 1.0
	} else {
		floor = -1.0
	}
	if floor < 0 || floor >= 1.0 {
		return [3]float64{}, false
	}
	if strict && floor <= 0 && playerBlockDangerous(d.w.GetBlock(world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z})) {
		return [3]float64{}, false
	}
	// standPos = Vec3.upFromBottomCenterOf(p, floor): the cell's
	// bottom center lifted by the floor fraction.
	sx, sy, sz := float64(p.X)+0.5, float64(p.Y)+floor, float64(p.Z)+0.5
	// The player's standing bounding box at standPos — 0.6 wide, 1.8
	// tall — must overlap no block collision shape.
	minX, maxX := sx-0.3, sx+0.3
	minY, maxY := sy, sy+1.8
	minZ, maxZ := sz-0.3, sz+0.3
	for bx := int(math.Floor(minX)); bx <= int(math.Floor(maxX)); bx++ {
		for by := int(math.Floor(minY)); by <= int(math.Floor(maxY)); by++ {
			for bz := int(math.Floor(minZ)); bz <= int(math.Floor(maxZ)); bz++ {
				iv, ok := collisionInterval(d.w.GetBlock(world.Pos{X: bx, Y: by, Z: bz}))
				if !ok {
					continue
				}
				cMin, cMax := float64(by)+iv[0], float64(by)+iv[1]
				if cMin < maxY && cMax > minY {
					return [3]float64{}, false
				}
			}
		}
	}
	if invalidSpawnInside(st) || invalidSpawnInside(d.w.GetBlock(world.Pos{X: p.X, Y: p.Y + 1, Z: p.Z})) {
		return [3]float64{}, false
	}
	return [3]float64{sx, sy, sz}, true
}

// broadcastSleepMeta writes the two-field entity metadata vanilla
// emits on bed enter/leave: idx 6 pose (SLEEPING/STANDING) and idx 14
// sleeping_pos as an optional block position.
func (s *Server) broadcastSleepMeta(sess *Session, head world.Pos, asleep bool) {
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	dim := sess.dimName()
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(metaPoseIndex)
	b.Byte(metaPoseType)
	if asleep {
		b.VarInt(poseSleeping)
	} else {
		b.VarInt(poseStanding)
	}
	b.Byte(metaSleepingPosIndex)
	b.Byte(metaSleepingPosType)
	b.Bool(asleep)
	if asleep {
		b.Long(int64(protocol.EncodePosition(int32(head.X), int32(head.Y), int32(head.Z))))
	}
	b.Byte(0xFF)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayEntityMetadata, body)
		return true
	})
}

// sleepTickStep runs once per game tick. When every play-state session
// in a dimension is asleep the skip starts: each player sees the
// "skipping night" action bar (announced once at the edge), and after
// the vanilla 100-tick sleep duration the world jumps to the next
// morning and everyone wakes.
func (s *Server) sleepTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.sleeping.Load() {
			sess.sleepTicks.Add(1)
		}
		return true
	})
	// Group sleeping players by dimension — the skip is per-level.
	type dimCount struct{ sleep, awake int }
	counts := map[string]*dimCount{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		d := sess.dimension()
		c := counts[d.name]
		if c == nil {
			c = &dimCount{}
			counts[d.name] = c
		}
		if sess.sleeping.Load() {
			if sess.sleepTicks.Load() >= 100 {
				c.sleep++
			}
		} else {
			c.awake++
		}
		return true
	})
	for dim, c := range counts {
		allAsleep := c.awake == 0 && s.anySleepingIn(dim)
		if !allAsleep {
			delete(s.sleepSkipNotified, dim)
			continue
		}
		if !s.sleepSkipNotified[dim] {
			s.sleepSkipNotified[dim] = true
			s.sessions.Range(func(_, v any) bool {
				sess := v.(*Session)
				if sess.state.Load() != StatePlay || sess.dimension().name != dim {
					return true
				}
				s.sendActionBar(sess, component{translate: "sleep.skipping_night"})
				return true
			})
		}
		if c.sleep == 0 {
			continue
		}
		s.setDayTime((s.dayTime()/24000 + 1) * 24000)
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimension().name != dim {
				return true
			}
			s.stopSleeping(sess)
			return true
		})
		delete(s.sleepSkipNotified, dim)
	}
}

// anySleepingIn reports whether at least one play-stage session in the
// dimension currently sleeps — the skip needs the whole dimension in
// bed, never an empty one.
func (s *Server) anySleepingIn(dim string) bool {
	found := false
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay && sess.dimension().name == dim && sess.sleeping.Load() {
			found = true
			return false
		}
		return true
	})
	return found
}
