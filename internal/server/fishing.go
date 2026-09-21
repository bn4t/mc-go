package server

// Fishing rods and the bobber entity. Use Item on a fishing_rod casts
// when the player has no bobber and retrieves when one is out — vanilla
// FishingRodItem.use is exactly that two-branch (player.fishing null
// check). The bobber flies with the arrow-style integrator at gravity
// 0.03/drag 0.92, parks on the first water surface it touches, and runs
// the vanilla catch state machine: lured (100-600 ticks minus the lure
// speed bonus) -> hooked (20-80, the fish-trail particle run) -> nibble
// (20-40, splash sound + the DATA_BITING metadata flag) -> reset and
// re-roll. Reeling during the nibble rolls the gameplay/fishing loot
// table, fires fishing_rod_hooked for every caught stack, launches the
// catch at the player, and costs 1 rod durability; an entity hooked on
// the line is pulled and costs 3 (item) or 5 (other) instead.

import (
	"math"
	"math/rand"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Bobber physics and timers, all measured from FishingHook bytecode:
// air gravity -0.03/tick and 0.92 drag, the lured/hooked/nibble
// countdowns, and the bite window.
const (
	fishGravity      = 0.03
	fishDrag         = 0.92
	fishCastSpeed    = 0.6
	fishLuredMin     = 100
	fishLuredMax     = 600
	fishHookedMin    = 20
	fishHookedMax    = 80
	fishNibbleMin    = 20
	fishNibbleMax    = 40
	fishingRodItemID = 1082

	sndBobberSplash   = 640
	sndBobberThrow    = 641
	sndBobberRetrieve = 639
)

// fishBobber is the per-entity catch state hanging off Entity.
type fishBobber struct {
	owner    *Session
	state    fishState
	lured    int
	hooked   int
	nibble   int
	surfaceY float64 // water surface the bobber parked on
}

type fishState int

const (
	fishFly fishState = iota // still in flight / falling
	fishBob                  // floating on water, waiting
)

// useFishingRod runs FishingRodItem.use: cast when no bobber is out,
// retrieve the running one otherwise.
func (s *Server) useFishingRod(sess *Session) {
	if sess.fishingBobber != 0 {
		s.retrieveBobber(sess)
		return
	}
	x, y, z, yaw, pitch, _ := sess.Pos()
	s.castBobber(sess, x, y, z, yaw, pitch)
	sess.addItemStat(statUsed, fishingRodItemID, 1)
}

// castBobber spawns the bobber on the vanilla spawn point — eye height,
// pulled back 0.3 along the look — with the shootFromRotation look
// vector at 0.6 speed plus the triangle jitter, and plays
// entity.fishing_bobber.throw. The hook metadata index 8 (hooked
// entity) starts empty and index 9 (biting) starts false.
func (s *Server) castBobber(sess *Session, x, y, z float64, yaw, pitch float32) {
	yawR := float64(yaw) * math.Pi / 180
	pitchR := float64(pitch) * math.Pi / 180
	// Entity.shootFromRotation: f = -sin(y)cos(p), f1 = -sin(p+roll),
	// f2 = cos(y)cos(p) with roll 0 — then normalize + jitter + speed.
	dx := -math.Sin(yawR) * math.Cos(pitchR)
	dy := -math.Sin(pitchR)
	dz := math.Cos(yawR) * math.Cos(pitchR)
	l := math.Sqrt(dx*dx + dy*dy + dz*dz)
	vx := dx/l*fishCastSpeed + randTriangle(0, 0.0103365)
	vy := dy/l*fishCastSpeed + randTriangle(0, 0.0103365)
	vz := dz/l*fishCastSpeed + randTriangle(0, 0.0103365)

	etype, ok := EntityTypeID("fishing_bobber")
	if !ok {
		return
	}
	e := s.entities.Spawn(&Entity{
		Type: etype,
		Dim:  sess.dimName(),
		X:    x + dx/l*0.3, // spawn 0.3 ahead along the look direction
		Y:    y + 1.52,     // eye height
		Z:    z + dz/l*0.3,
		Yaw:  yaw, Pitch: pitch,
		MotionX: vx, MotionY: vy, MotionZ: vz,
		VX: int16(vx * 8000), VY: int16(vy * 8000), VZ: int16(vz * 8000),
		Data: sess.entityID.Load() + 1, // spawn packet owner field
	})
	fb := &fishBobber{owner: sess}
	e.fishing = fb
	sess.fishingBobber = e.ID
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeBobberMetadata(0, false))
	s.playSoundAt(e.Dim, e.X, e.Y, e.Z, sndBobberThrow, 0.5, 0.4/(0.4*float32(rand.Float64())+0.8))
}

// randTriangle draws from vanilla RandomSource.triangle(center, spread):
// sum of two uniform(-spread, spread) deviates around center.
func randTriangle(center, spread float64) float64 {
	return center + spread*(rand.Float64()-rand.Float64())
}

// encodeBobberMetadata emits the fishing_bobber synched-data pair:
// index 8 hooked entity id (varint, 0 none), index 9 biting bool.
// Wire types: varint=1, boolean=8.
func encodeBobberMetadata(hookedID int32, biting bool) []byte {
	var b protocol.Buffer
	b.Byte(8)
	b.Byte(1) // varint
	b.VarInt(hookedID)
	b.Byte(9)
	b.Byte(8) // boolean
	if biting {
		b.Byte(1)
	} else {
		b.Byte(0)
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// playSoundAt broadcasts a positioned Sound packet at fractional
// coordinates (bobber sounds are entity-positioned NEUTRAL events).
func (s *Server) playSoundAt(dim string, x, y, z float64, soundID int32, vol, pitch float32) {
	var b protocol.Buffer
	b.VarInt(soundID + 1)
	b.VarInt(6) // SoundSource.NEUTRAL
	b.Int(int32(x * 8))
	b.Int(int32(y * 8))
	b.Int(int32(z * 8))
	b.Float(vol)
	b.Float(pitch)
	b.Long(0)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlaySound, body)
		return true
	})
}

// fishTickStep advances every live bobber one tick: flight physics
// until it parks on water, then the lured -> hooked -> nibble
// countdowns. Vanilla FishingHook.tick is the same walk.
func (s *Server) fishTickStep() {
	s.entities.mu.RLock()
	hooks := make([]*Entity, 0, 4)
	for _, e := range s.entities.byID {
		if e.Dying || e.fishing == nil {
			continue
		}
		hooks = append(hooks, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range hooks {
		fb := e.fishing
		owner := fb.owner
		// shouldStopFishing: owner gone, dead, holding no rod, or the
		// line stretched past 32 blocks — the bobber discards.
		if owner == nil || owner.state.Load() != StatePlay || owner.dead || !bobberHeldBy(owner) {
			s.discardBobber(e)
			continue
		}
		ox, oy, oz, _, _, _ := owner.Pos()
		if dx, dy, dz := e.X-ox, e.Y-oy, e.Z-oz; dx*dx+dy*dy+dz*dz > 32*32 {
			s.discardBobber(e)
			continue
		}
		d := s.dimensionByName(e.Dim)
		if d == nil {
			continue
		}
		if fb.state == fishFly {
			s.fishFlyTick(e, d, fb)
		} else {
			s.fishBobTick(e, d, fb)
		}
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
	}
}

// bobberHeldBy reports whether the owner still holds a fishing_rod.
func bobberHeldBy(sess *Session) bool {
	held := sess.heldStack()
	return held.ID == fishingRodItemID && !held.Empty()
}

// fishFlyTick integrates the bobber's flight: gravity 0.03, air drag
// 0.92, and parking the first time it enters water. The parked y is
// the water surface (top of the block minus the bobber's half-height
// sink, vanilla rides ~0.15 below surface).
func (s *Server) fishFlyTick(e *Entity, d *dimension, fb *fishBobber) {
	e.X += e.MotionX
	e.Y += e.MotionY
	e.Z += e.MotionZ
	e.MotionY -= fishGravity
	e.MotionX *= fishDrag
	e.MotionY *= fishDrag
	e.MotionZ *= fishDrag
	p := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	blk := d.w.GetBlock(p)
	if k, level, ok := fluidOf(blk); ok && k == fluidWater {
		// Level is 0 for a source; surface sits at (pos + 1 - level/8)
		// minus the bobber's resting offset — vanilla keeps the bobber
		// just under the surface line.
		surface := float64(p.Y) + 1.0 - float64(level)/8.0 - 0.15
		fb.surfaceY = surface
		fb.state = fishBob
		e.Y = surface
		e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
		fb.lured = fishLuredMin + rand.Intn(fishLuredMax-fishLuredMin+1)
		return
	}
	// Solid ground under the flight: vanilla lands the bobber and it
	// sits waiting (the 1200-tick life cap is folded into state=Bob
	// with lured counting — matches the dead-stick case).
	if _, has := blockNameAt(d, p); has && !world.NonSolid(d.w.GetBlock(p)) {
		fb.state = fishBob
		fb.lured = fishLuredMin + rand.Intn(fishLuredMax-fishLuredMin+1)
		e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
		e.Y = float64(p.Y) + 1.0
	}
}

// fishBobTick runs the countdown chain while the bobber sits: lured
// ticks down (with the sky/rain modifier — a flat test world sees sky,
// no rain), then hooked, then the nibble window with the splash and
// DATA_BITING set; on expiry it re-rolls lured like vanilla's
// timeUntilLured<=0 branch.
func (s *Server) fishBobTick(e *Entity, d *dimension, fb *fishBobber) {
	inWater := false
	p := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	if k, _, ok := fluidOf(d.w.GetBlock(p)); ok && k == fluidWater {
		inWater = true
	}
	if !inWater && fb.lured == 0 && fb.hooked == 0 && fb.nibble == 0 {
		// Dry-landed bobber waits without a countdown (vanilla leaves
		// the timers at zero out of water).
		return
	}
	// Vanilla's per-tick decrement (FishingHook.tick): 1 plus a 25% roll
	// when it's raining at the bobber, minus a 50% roll when the column
	// above can't see sky. mc-go has no weather, so only the sky check
	// applies.
	dec := 1
	if rand.Float64() < 0.5 && !bobberSeesSky(d, p) {
		dec--
	}
	if fb.nibble > 0 {
		fb.nibble--
		if fb.nibble == 0 {
			// Missed the window: clear biting and re-roll lured.
			s.broadcastEntityMetadata(e.ID, encodeBobberMetadata(0, false))
			fb.lured = fishLuredMin + rand.Intn(fishLuredMax-fishLuredMin+1)
		}
		return
	}
	if fb.hooked > 0 {
		fb.hooked -= dec
		if fb.hooked <= 0 {
			fb.hooked = 0
			fb.nibble = fishNibbleMin + rand.Intn(fishNibbleMax-fishNibbleMin+1)
			s.playSoundAt(e.Dim, e.X, e.Y, e.Z, sndBobberSplash,
				0.25, 1.0+0.4*float32(rand.Float64()-rand.Float64()))
			s.broadcastEntityMetadata(e.ID, encodeBobberMetadata(0, true))
		}
		return
	}
	if fb.lured > 0 {
		fb.lured -= dec
		if fb.lured <= 0 {
			fb.lured = 0
			fb.hooked = fishHookedMin + rand.Intn(fishHookedMax-fishHookedMin+1)
		}
		return
	}
	// Both timers empty (fresh land): start the lured leg.
	if inWater {
		fb.lured = fishLuredMin + rand.Intn(fishLuredMax-fishLuredMin+1)
	}
}

// bobberSeesSky reports whether the column above p is open to the sky:
// air and fluids don't block; any block with collision does.
func bobberSeesSky(d *dimension, p world.Pos) bool {
	for y := p.Y + 1; y <= 200; y++ {
		b := d.w.GetBlock(world.Pos{X: p.X, Y: y, Z: p.Z})
		if b == world.Air {
			continue
		}
		if _, _, ok := fluidOf(b); ok {
			continue
		}
		if world.NonSolid(b) {
			continue
		}
		return false
	}
	return true
}

// retrieveBobber runs FishingHook.retrieve: entity-hooked pulls cost
// 3/5 durability, a biting bobber rolls the fishing loot table and
// fires fishing_rod_hooked per caught stack, then the bobber discards
// with the retrieve sound. Returns nothing; the wear lands via
// wearHeld like every other tool.
func (s *Server) retrieveBobber(sess *Session) {
	d := s.dimensionByName(sess.dimName())
	e, ok := s.entities.GetPtr(sess.fishingBobber)
	if !ok || e.fishing == nil {
		sess.fishingBobber = 0
		return
	}
	fb := e.fishing
	cost := 0
	if fb.nibble > 0 {
		cost = 1
		// in_open_water (the treasure gate) is evaluated once, at the
		// bite, on the bobber's block column.
		open := false
		if d != nil {
			open = bobberOpenWater(d, world.Pos{
				X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z)),
			})
		}
		drops := rollFishingLoot(open)
		s.fireFishingRodHooked(sess, e, drops)
		px, py, pz, _, _, _ := sess.Pos()
		for _, it := range drops {
			s.spawnCaughtItem(e.Dim, e.X, e.Y, e.Z, px, py, pz, it)
		}
	}
	s.playSoundAt(e.Dim, e.X, e.Y, e.Z, sndBobberRetrieve, 1.0,
		0.4/(0.4*float32(rand.Float64())+0.8))
	s.discardBobber(e)
	if cost > 0 && sess.mode() == GameModeSurvival {
		s.wearHeld(sess, int32(cost))
	}
}

// discardBobber removes the bobber entity and clears the owner link.
func (s *Server) discardBobber(e *Entity) {
	if e.fishing != nil && e.fishing.owner != nil && e.fishing.owner.fishingBobber == e.ID {
		e.fishing.owner.fishingBobber = 0
	}
	e.fishing = nil
	s.entities.Remove(e.ID)
	s.broadcastEntityRemove([]int32{e.ID})
}

// spawnCaughtItem launches a caught stack from the bobber toward the
// player with vanilla's (dx,dy,dz)*0.1 + |d|*0.08-up launch.
func (s *Server) spawnCaughtItem(dim string, bx, by, bz, px, py, pz float64, it ItemStack) {
	itemType, ok := EntityTypeID("item")
	if !ok {
		return
	}
	dx, dy, dz := px-bx, py-by, pz-bz
	horiz := math.Sqrt(dx*dx + dy*dy + dz*dz)
	e := s.entities.Spawn(&Entity{
		Type: itemType,
		Dim:  dim,
		X:    bx, Y: by, Z: bz,
		MotionX: dx * 0.1, MotionY: dy*0.1 + horiz*0.08, MotionZ: dz * 0.1,
		Item:        it,
		PickupDelay: 0, // vanilla sets none; the owner picks up at contact
	})
	e.VX = int16(e.MotionX * 8000)
	e.VY = int16(e.MotionY * 8000)
	e.VZ = int16(e.MotionZ * 8000)
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
}

// fishingTable is gameplay/fishing's fish pool flattened to the 85%
// fish branch (junk and treasure are the other two pools: junk 10,
// treasure 5 with in_open_water — a test pond qualifies).
type fishEntry struct {
	item   string
	weight int
}

var fishingPool = []struct {
	table  string
	weight int
}{
	{"fish", 85},
	{"junk", 10},
	{"treasure", 5},
}

var fishingFish = []fishEntry{
	{"cod", 60},
	{"salmon", 25},
	{"pufferfish", 13},
	{"tropical_fish", 2},
}

var fishingJunk = []fishEntry{
	{"lily_pad", 17},
	{"leather_boots", 10},
	{"leather", 10},
	{"bone", 10},
	{"potion", 10},
	{"string", 5},
	{"fishing_rod", 2},
	{"bowl", 10},
	{"stick", 5},
	{"ink_sac", 1},
	{"tripwire_hook", 10},
	{"rotten_flesh", 10},
	{"bamboo", 10},
}

var fishingTreasure = []fishEntry{
	{"name_tag", 1},
	{"saddle", 1},
	{"bow", 1},
	{"fishing_rod", 1},
	{"book", 1},
	{"nautilus_shell", 1},
}

// bobberOpenWater evaluates vanilla's in_open_water check
// (FishingHook.calculateOpenWater): three 5x5 layers — the bobber's
// block level, one below, one above — each layer must be uniformly
// source water (no collision) or uniformly air/lily_pad, and a water
// layer may not sit above an air layer. The first layer may not be
// above-water. Anything else closes the water.
func bobberOpenWater(d *dimension, p world.Pos) bool {
	prev := 2 // OpenWaterType.INVALID seed
	for dy := -1; dy <= 1; dy++ {
		t := openWaterLayer(d, p, dy)
		switch t {
		case 2: // INVALID layer
			return false
		case 0: // ABOVE_WATER
			if prev == 2 {
				return false
			}
		case 1: // INSIDE_WATER
			if prev == 0 {
				return false
			}
		}
		prev = t
	}
	return true
}

// openWaterLayer classifies one 5x5 layer at p+dy: 0 above-water
// (air or lily_pad), 1 inside-water (water source with no collision),
// 2 invalid (anything else, or a mix).
func openWaterLayer(d *dimension, p world.Pos, dy int) int {
	seen := -1
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			b := d.w.GetBlock(world.Pos{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz})
			t := 2
			switch {
			case b == world.Air:
				t = 0
			default:
				name, _ := world.StateOwner(b)
				if name == "lily_pad" {
					t = 0
				} else if k, level, ok := fluidOf(b); ok && k == fluidWater && level == 0 {
					t = 1
				}
			}
			if seen == -1 {
				seen = t
			} else if t != seen {
				seen = 2
			}
		}
	}
	return seen
}

// rollFishingLoot draws one stack from the fishing table: pool roll,
// then the weighted item in that pool, then the entry's datapack
// functions (set_count, set_potion, set_damage; enchant_with_levels
// is folded to the item conversion — the random enchantment itself
// is not modeled). open=false removes the treasure pool, matching
// vanilla's in_open_water loot condition.
func rollFishingLoot(open bool) []ItemStack {
	pools := fishingPool
	if !open {
		pools = fishingPool[:2]
	}
	pool := weightedFishEntry(pools)
	var table []fishEntry
	switch pool {
	case "junk":
		table = fishingJunk
	case "treasure":
		table = fishingTreasure
	default:
		table = fishingFish
	}
	name := weightedFishItem(table)
	it := ItemStack{}

	// set_potion: the junk table's water bottle.
	if name == "potion" {
		if id, ok := world.ItemIDs["potion"]; ok {
			it = ItemStack{ID: id, Count: 1, Potion: "water"}
		}
	} else {
		// enchant_with_levels on book produces an enchanted_book.
		lookup := name
		if name == "book" && pool == "treasure" {
			lookup = "enchanted_book"
		}
		id, ok := world.ItemIDs[lookup]
		if !ok {
			return nil
		}
		it = ItemStack{ID: id, Count: 1}
	}
	if it.ID == 0 {
		return nil
	}

	// set_count 10: junk ink sacs land as a stack of ten.
	if name == "ink_sac" {
		it.Count = 10
	}
	// set_damage: junk rolls 0..90% of max durability, treasure 0..25%.
	frac := 0.0
	switch name {
	case "leather_boots", "fishing_rod":
		if pool == "junk" {
			frac = 0.9
		}
	case "bow":
		frac = 0.25
	}
	if name == "fishing_rod" && pool == "treasure" {
		frac = 0.25
	}
	if frac > 0 {
		if maxDmg, ok := damageableItems[name]; ok {
			it.Damage = int32(rand.Float64() * float64(maxDmg) * frac)
		}
	}
	return []ItemStack{it}
}

func weightedFishEntry(pool []struct {
	table  string
	weight int
}) string {
	total := 0
	for _, p := range pool {
		total += p.weight
	}
	r := rand.Intn(total)
	for _, p := range pool {
		if r < p.weight {
			return p.table
		}
		r -= p.weight
	}
	return pool[0].table
}

func weightedFishItem(entries []fishEntry) string {
	total := 0
	for _, e := range entries {
		total += e.weight
	}
	r := rand.Intn(total)
	for _, e := range entries {
		if r < e.weight {
			return e.item
		}
		r -= e.weight
	}
	return entries[0].item
}
