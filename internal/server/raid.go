package server

// Raids: /raid plus the raid lifecycle — wave spawns, the ServerBossEvent
// bar, victory/defeat — measured against vanilla 26.2's
// net.minecraft.world.entity.raid.{Raid,Raids,RaiderType} and
// net.minecraft.server.commands.RaidCommand bytecode.
//
// Village detection rides on claimed POIs: villager brains claim beds
// (and bells) they discover, and a raid can only start where occupied
// POIs sit within 64 blocks of the starter — the same precondition
// Raids.createOrExtendRaid applies.

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Constants lifted from vanilla's Raid fields: the 96-block viewer
// radius (VALID_RAID_RADIUS_SQR), the 112-block raider drop distance
// (RAID_REMOVAL_THRESHOLD_SQR), the 300-tick inter-wave cooldown, the
// 40-tick post-clear pause before victory, the 600-tick celebration
// window after it, and the 48000-tick hero_of_the_village effect.
const (
	raidViewerRadiusSq = 9216.0
	raidDropRadiusSq   = 12544.0
	raidCooldownTicks  = 300
	raidPostTicks      = 40
	raidCelebration    = 600
	raidHeroTicks      = 48000
	raidOmenMax        = 5
	raidSpawnRing      = 32.0
	raidHornDistance   = 64.0
	raidHornDistanceSq = raidHornDistance * raidHornDistance
	raidSpawnRetries   = 5
	// raidPoiRadiusSq bounds the claimed-POI centroid search — vanilla's
	// getInRange is a 64-block Euclidean sphere (distSqr, Y included).
	raidPoiRadiusSq = 64 * 64
	// Raid horn: entity-variant horn played to every bar viewer within
	// 64 blocks of the spawn pos at the spawn's xz and the hearer's y.
	raidHornSound = "event.raid.horn"
)

// raidWaveTable is RaiderType.spawnsPerWaveBeforeBonus — index order is
// the enum order VINDICATOR, EVOKER, PILLAGER, WITCH, RAVAGER and the
// inner index is the 1-based wave number (slot 0 is an unused dummy).
var raidWaveTable = [5][8]int32{
	{0, 0, 2, 0, 1, 4, 2, 5}, // vindicator
	{0, 0, 0, 0, 0, 1, 1, 2}, // evoker
	{0, 4, 3, 3, 4, 4, 4, 2}, // pillager
	{0, 0, 0, 0, 3, 0, 0, 1}, // witch
	{0, 0, 0, 1, 0, 1, 0, 2}, // ravager
}

var raidRaiderNames = [5]string{"vindicator", "evoker", "pillager", "witch", "ravager"}

// raidNumGroups is Raid.getNumGroups: peaceful raids never start, easy
// runs 3 waves, normal 5, hard 7. The bonus group rides on top.
func raidNumGroups(diff int32) int32 {
	switch diff {
	case 0:
		return 0
	case 1:
		return 3
	case 2:
		return 5
	default:
		return 7
	}
}

// raidEnchantOdds is Raid.getEnchantOdds: the wave-weapon enchantment
// probability per raid omen level.
func raidEnchantOdds(omen int32) float64 {
	switch omen {
	case 2:
		return 0.1
	case 3:
		return 0.25
	case 4:
		return 0.5
	case 5:
		return 0.75
	}
	return 0
}

type raidStatus int32

const (
	raidOngoing raidStatus = iota
	raidVictory
	raidLoss
	raidStopped
)

// raid is one active Raid — the center is the centroid of the village's
// claimed POIs (or the /raid starter's own block when no claimed POI is
// in range), raiders is the flat eid set across waves, and heroes
// records the players who damaged raiders (hero_of_the_village targets).
type raid struct {
	id          int
	center      world.Pos
	omen        int32
	numGroups   int32
	spawned     int32 // groupsSpawned
	status      raidStatus
	started     bool
	cooldown    int32
	postTicks   int32
	celebrate   int32
	ticksActive int64
	spawnPos    world.Pos
	hasSpawnPos bool
	tries       int32
	raiders     map[int32]bool
	leaders     map[int32]int32 // wave number -> leader eid
	totalHealth float32
	heroes      map[[16]byte]bool
	barUUID     [16]byte
	viewers     map[*Session]bool
	barProg     float32
	barName     component
	barAlive    int32 // alive count baked into the current bar name
}

func (r *raid) alive() int32 { return int32(len(r.raiders)) }

// Vanilla's wave predicates, verbatim from Raid.
func (r *raid) hasBonusWave() bool        { return r.omen > 1 }
func (r *raid) hasSpawnedBonusWave() bool { return r.spawned > r.numGroups }
func (r *raid) isFinalWave() bool         { return r.spawned == r.numGroups }

// hasMoreWaves is Raid.hasMoreWaves: bonus raids keep going until the
// bonus group has spawned; plain raids until the last group spawns.
func (r *raid) hasMoreWaves() bool {
	if r.hasBonusWave() {
		return !r.hasSpawnedBonusWave()
	}
	return !r.isFinalWave()
}

// shouldSpawnBonusGroup is Raid.shouldSpawnBonusGroup: the last group
// died and a bonus wave is owed.
func (r *raid) shouldSpawnBonus() bool {
	return r.isFinalWave() && r.alive() == 0 && r.hasBonusWave()
}

// shouldSpawnGroup mirrors Raid.shouldSpawnGroup: cooldown spent, a
// group remains (regular or bonus), and every raider is dead.
func (r *raid) shouldSpawnGroup() bool {
	if r.cooldown != 0 {
		return false
	}
	if r.spawned < r.numGroups || r.shouldSpawnBonus() {
		return r.alive() == 0
	}
	return false
}

// healthOfLivingRaiders sums the health of raiders still flagged into
// the raid — Raid.updateBossbar's dividend.
func (s *Server) healthOfRaiders(r *raid) float32 {
	var f float32
	for eid := range r.raiders {
		if e, ok := s.entities.GetPtr(eid); ok && !e.Dying {
			f += e.Health
		}
	}
	return f
}

// --- bossbar wire ----------------------------------------------------

// raidBarPacket writes a ClientboundBossEvent packet: uuid, op, body.
func raidBarPacket(r *raid, op int32, payload []byte) []byte {
	var p protocol.Buffer
	p.UUID(r.barUUID)
	p.VarInt(op)
	p.Raw(payload)
	return p.Bytes()
}

// raidBarAdd is the op-0 body — name component, progress, color
// (red=2), style (notched_10=2), flag byte.
func raidBarAdd(r *raid) []byte {
	var p protocol.Buffer
	p.Raw(r.barName.content())
	p.Float(r.barProg)
	p.VarInt(2) // red
	p.VarInt(2) // notched_10
	p.Byte(0)
	return p.Bytes()
}

func raidBarProgressPayload(prog float32) []byte {
	var p protocol.Buffer
	p.Float(prog)
	return p.Bytes()
}

func raidBarNamePayload(name component) []byte {
	var p protocol.Buffer
	p.Raw(name.content())
	return p.Bytes()
}

func (s *Server) raidBarSend(r *raid, op int32, payload []byte) {
	for t := range r.viewers {
		if t.state.Load() != StatePlay {
			continue
		}
		_ = t.write(t.ids().PlayBossEvent, raidBarPacket(r, op, payload))
	}
}

func (s *Server) raidBarProgress(r *raid, prog float32) {
	if prog > 1 {
		prog = 1
	}
	if prog < 0 {
		prog = 0
	}
	if prog == r.barProg {
		return // ServerBossEvent.setProgress only broadcasts on change
	}
	r.barProg = prog
	s.raidBarSend(r, 2, raidBarProgressPayload(prog))
}

// raidBarName applies a name when it differs — setName broadcasts an
// op-3 only on a real change. barAlive distinguishes the two
// raiders_remaining forms (the with-arg isn't in barName's translate).
func (s *Server) raidBarName(r *raid, name component) {
	s.raidBarNameAlive(r, name, -1)
}

func (s *Server) raidBarNameAlive(r *raid, name component, alive int32) {
	if r.barName.translate == name.translate && r.barAlive == alive {
		return
	}
	r.barName = name
	r.barAlive = alive
	s.raidBarSend(r, 3, raidBarNamePayload(name))
}

var (
	raidBarRaid    = component{translate: "event.minecraft.raid"}
	raidBarVictory = component{translate: "event.minecraft.raid.victory"}
	raidBarDefeat  = component{translate: "event.minecraft.raid.defeat"}
)

// --- lifecycle -------------------------------------------------------

// raidAt returns the active raid whose center covers pos — vanilla's
// ServerLevel.getRaidAt uses the 96-block valid radius.
// raidAt is Raids.getNearbyRaid(pos, 9216): the nearest non-stopped
// raid whose center is within 96 blocks — full 3D distSqr on block
// positions, closest wins when raids overlap.
func (d *dimension) raidAt(px, py, pz int) *raid {
	var best *raid
	bestDist := float64(raidViewerRadiusSq)
	for _, r := range d.raids {
		if r.status == raidStopped {
			continue
		}
		dx := float64(px - r.center.X)
		dy := float64(py - r.center.Y)
		dz := float64(pz - r.center.Z)
		dist := dx*dx + dy*dy + dz*dz
		if dist < bestDist {
			best, bestDist = r, dist
		}
	}
	return best
}

func (d *dimension) isRaided(px, py, pz int) bool {
	return d.raidAt(px, py, pz) != nil
}

// villageCenter is createOrExtendRaid's anchor pass: the centroid of
// occupied village-tag POI records within a 64-block Euclidean radius
// (PoiManager.getInRange) of the trigger position. With none in range
// the anchor is the position itself — the raid's isVillage tick check
// resolves whether it survives.
func (d *dimension) villageCenter(px, py, pz int) world.Pos {
	var sx, sy, sz, n int
	for pos := range d.claimedPOIs {
		if !d.poiStillClaimed(pos) {
			continue
		}
		dx, dy, dz := pos.X-px, pos.Y-py, pos.Z-pz
		if dx*dx+dy*dy+dz*dz > raidPoiRadiusSq {
			continue
		}
		sx, sy, sz, n = sx+pos.X, sy+pos.Y, sz+pos.Z, n+1
	}
	if n == 0 {
		return world.Pos{X: px, Y: py, Z: pz}
	}
	// BlockPos.containing floors the centroid.
	return world.Pos{X: sx / n, Y: sy / n, Z: sz / n}
}

// poiStillClaimed drops stale claims — a broken bed or removed bell
// frees its POI, mirroring the occupancy bit PoiManager clears when the
// block stops matching the poi type.
func (d *dimension) poiStillClaimed(pos world.Pos) bool {
	if _, ok := d.claimedPOIs[pos]; !ok {
		return false
	}
	if !villagePOIBlock(d.w.GetBlock(pos)) {
		delete(d.claimedPOIs, pos)
		return false
	}
	return true
}

// raidIsVillage is ServerLevel.isVillage → sectionsToVillage(pos) <= 1:
// a claimed village-tag POI in the position's 16³ section or one of its
// 26 neighbours marks the section a village centre.
func (d *dimension) raidIsVillage(pos world.Pos) bool {
	sx, sy, sz := floorDiv(pos.X, 16), floorDiv(pos.Y, 16), floorDiv(pos.Z, 16)
	for p := range d.claimedPOIs {
		if !d.poiStillClaimed(p) {
			continue
		}
		if absi(floorDiv(p.X, 16)-sx) > 1 ||
			absi(floorDiv(p.Y, 16)-sy) > 1 ||
			absi(floorDiv(p.Z, 16)-sz) > 1 {
			continue
		}
		return true
	}
	return false
}

// createOrExtendRaid is vanilla Raids.createOrExtendRaid gated to the
// observable precondition set: non-spectator, the raids gamerule, and
// the can_start_raid environment attribute (only the nether datapack
// disables it — the overworld default is true). pos is the recorded
// omen position; the anchor snaps to the claimed-POI centroid.
func (s *Server) createOrExtendRaid(sess *Session, d *dimension, pos world.Pos) *raid {
	if sess.gameMode == 3 || !s.gameRuleBool("minecraft:raids") {
		return nil
	}
	center := d.villageCenter(pos.X, pos.Y, pos.Z)
	// getOrCreateRaid looks the raid up by the village centroid — a
	// second /raid start on the same village extends that raid.
	if ex := d.raidAt(center.X, center.Y, center.Z); ex != nil {
		s.absorbRaidOmen(sess, ex)
		return ex
	}
	var u [16]byte
	for i := range u {
		u[i] = byte(rand.Intn(256))
	}
	r := &raid{
		center:    center,
		numGroups: raidNumGroups(int32(s.difficulty)),
		cooldown:  raidCooldownTicks,
		raiders:   map[int32]bool{},
		leaders:   map[int32]int32{},
		heroes:    map[[16]byte]bool{},
		barUUID:   u,
		viewers:   map[*Session]bool{},
		barName:   raidBarRaid,
	}
	if d.raids == nil {
		d.raids = map[int]*raid{}
	}
	d.raidNext++
	r.id = d.raidNext
	d.raids[d.raidNext] = r
	s.absorbRaidOmen(sess, r)
	return r
}

// raidWaveCount is getDefaultNumSpawns + getPotentialBonusSpawns: the
// 1-based wave's table count (the bonus wave reads the numGroups slot)
// plus the difficulty/type bonus roll — bonus=rand(0,cap) with cap
// vindicator/pillager easy:rand(0,2) normal:1 hard:2, witch one on
// non-easy waves >2 except wave 4, ravager one on a non-easy bonus wave.
func (s *Server) raidWaveCount(r *raid, ord int, wave int32, bonus bool) int32 {
	n := raidWaveTable[ord][wave]
	if bonus {
		n = raidWaveTable[ord][r.numGroups]
	}
	diff := int32(s.difficulty)
	easy, normal := diff == 1, diff == 2
	var cap int32
	switch ord {
	case 0, 2: // vindicator, pillager
		switch {
		case easy:
			cap = int32(rand.Intn(2))
		case normal:
			cap = 1
		default:
			cap = 2
		}
	case 3: // witch
		if !easy && wave > 2 && wave != 4 {
			cap = 1
		}
	case 4: // ravager
		if !easy && bonus {
			cap = 1
		}
	}
	if cap > 0 {
		n += int32(rand.Intn(int(cap) + 1))
	}
	return n
}

// raidValidSpawnPos is getValidSpawnPos — findRandomSpawnPos at eight
// tries: a candidate ring whose radius shrinks with the cooldown
// (f4 = 0.22*(cooldown/20) - 0.24), eight angles π/8 apart from a random
// start, plus a nextInt(3)*floor(f4) jitter per axis. A candidate needs
// a surface within 96 of the center's y, must leave the village while
// the cooldown is early (i3>7), and must pass the ravager's ground
// placement (feet air over solid — or snow-layer air). Chunk-loadedness
// and entity ticking are always true on the flat test world.
func (s *Server) raidValidSpawnPos(d *dimension, r *raid) (world.Pos, bool) {
	i3 := r.cooldown / 20
	f4 := 0.22*float64(i3) - 0.24
	angle := rand.Float64() * 2 * math.Pi
	jitter := int32(math.Floor(f4))
	for i := int32(0); i < 8; i++ {
		a := angle + math.Pi*float64(i)/8
		x := r.center.X + int(math.Floor(math.Cos(a)*raidSpawnRing*f4)) + rand.Intn(3)*int(jitter)
		z := r.center.Z + int(math.Floor(math.Sin(a)*raidSpawnRing*f4)) + rand.Intn(3)*int(jitter)
		y := d.w.TopMotionBlocking(x, z, d.minY, d.minY+d.height)
		if y <= d.minY {
			continue
		}
		// getHeight returns the air cell over the top block.
		pos := world.Pos{X: x, Y: y + 1, Z: z}
		if absi(pos.Y-r.center.Y) > 96 {
			continue
		}
		if d.raidIsVillage(pos) && i3 > 7 {
			continue
		}
		below := d.w.GetBlock(world.Pos{X: x, Y: y, Z: z})
		feet := d.w.GetBlock(pos)
		if !raidGroundSpawnable(below, feet) {
			continue
		}
		return pos, true
	}
	return world.Pos{}, false
}

// raidGroundSpawnable is the ravager's spawn placement check on the
// flat world: solid ground below and non-solid feet, with vanilla's
// snow-layer carve-out (snow below + air feet still places).
func raidGroundSpawnable(below, feet world.Block) bool {
	belowName, _ := world.StateOwner(below)
	feetName, _ := world.StateOwner(feet)
	if feetName != "" && feetName != "air" && feetName != "snow" {
		return false
	}
	if feetName == "" || feetName == "air" {
		if belowName == "" || belowName == "air" {
			return false
		}
		return !raidNoGroundBlocks(belowName)
	}
	// feet is snow: vanilla requires the block below to be snow too.
	return belowName == "snow"
}

// raidNoGroundBlocks is the ravager's no-spawn ground set — fluids and
// non-full solids that fail ON_GROUND placement.
func raidNoGroundBlocks(name string) bool {
	switch name {
	case "air", "water", "lava", "snow", "ice", "packed_ice", "blue_ice",
		"frosted_ice", "barrier":
		return true
	}
	return strings.HasSuffix(name, "_leaves") || strings.HasSuffix(name, "_sapling")
}

// raidApplyBuffs is Raider.applyRaidBuffs: at the omen-level enchant
// odds the weapon is replaced by a fresh stack enchanted through the
// raid's enchantment provider — pillager crossbows get quick_charge 1
// past wave 3 and 2 past wave 5 (a failed roll keeps the default
// crossbow), vindicator axes get sharpness 1, or 2 past wave 5. Other
// raiders are untouched.
func (s *Server) raidApplyBuffs(e *Entity, name string, wave int32, r *raid) {
	odds := raidEnchantOdds(r.omen)
	if odds <= 0 || rand.Float64() >= odds {
		return
	}
	switch name {
	case "pillager":
		if wave <= 5 {
			if wave <= 3 {
				return
			}
			s.raidEnchantWeapon(e, "crossbow", "quick_charge", 1)
			return
		}
		s.raidEnchantWeapon(e, "crossbow", "quick_charge", 2)
	case "vindicator":
		lv := int32(1)
		if wave > 5 {
			lv = 2
		}
		s.raidEnchantWeapon(e, "iron_axe", "sharpness", lv)
	}
}

// raidEnchantWeapon swaps the raider's mainhand for a fresh item with
// the single provider enchantment baked in.
func (s *Server) raidEnchantWeapon(e *Entity, item, ench string, lv int32) {
	id, ok := world.ItemID(item)
	if !ok {
		return
	}
	if e.Equips == nil {
		e.Equips = map[int32]ItemStack{}
	}
	e.Equips[equipMainHand] = ItemStack{ID: id, Count: 1, Ench: ench + ":" + strconv.Itoa(int(lv))}
}

// spawnRaidGroup is Raid.spawnGroup: wave = groupsSpawned+1 (riders
// take the same wave), the first leader-capable raider flagged becomes
// the wave's leader with the ominous banner, and each ravager picks a
// rider — a pillager on the wave equal to the normal raid size, an
// evoker on the first ravager and vindicators on the rest once the wave
// number reaches the hard raid size. totalHealth resets and re-grows
// through joinRaid.
func (s *Server) spawnRaidGroup(d *dimension, r *raid, pos world.Pos) {
	wave := r.spawned + 1
	bonus := r.shouldSpawnBonus()
	// totalHealth resets each group — the bar's divisor is the new
	// wave's spawn health, not the raid's running total.
	r.totalHealth = 0
	leaderDone := false
	spawnX := float64(pos.X) + 0.5
	spawnY := float64(pos.Y) + 1 // joinRaid's setPos is pos.y+1
	spawnZ := float64(pos.Z) + 0.5
	ravagerRiders := 0

	for ord, name := range raidRaiderNames {
		n := s.raidWaveCount(r, ord, wave, bonus)
		for i := int32(0); i < n; i++ {
			e, ok := s.summonMobRaw(d, name, spawnX, spawnY, spawnZ, false, false)
			if !ok {
				break
			}
			s.mobDefaultEquipOpt(e, true)
			if !leaderDone && raidCanBeLeader(name) {
				e.PatrolLeader = true
				s.raidSetLeader(r, wave, e)
				leaderDone = true
			}
			s.raidJoin(r, wave, e)
			s.raidApplyBuffs(e, name, wave, r)
			if name == "ravager" {
				var rider *Entity
				switch {
				case wave == raidNumGroups(2): // getNumGroups(NORMAL)
					rider, _ = s.summonMobRaw(d, "pillager", spawnX, spawnY, spawnZ, false, false)
				case wave >= raidNumGroups(3): // getNumGroups(HARD)
					rn := "vindicator"
					if ravagerRiders == 0 {
						rn = "evoker"
					}
					rider, _ = s.summonMobRaw(d, rn, spawnX, spawnY, spawnZ, false, false)
				}
				ravagerRiders++
				if rider != nil {
					rn, _ := EntityTypeName(rider.Type)
					s.mobDefaultEquipOpt(rider, true)
					s.raidJoin(r, wave, rider)
					s.raidApplyBuffs(rider, rn, wave, r)
					e.Passengers = append(e.Passengers, rider.ID)
					s.broadcastSetPassengers(e)
					s.broadcastFreshMob(rider)
				}
			}
			s.broadcastFreshMob(e)
		}
	}
	r.hasSpawnPos = false
	r.spawned++
	s.raidBarUpdate(r)
}

// raidSetLeader is Raid.setLeader: the leader's head slot takes the
// ominous banner — a white_banner with the 8-layer pattern list, the
// ominous-banner item name, uncommon rarity, and banner_patterns hidden
// from the tooltip (getBannerComponentPatch).
func (s *Server) raidSetLeader(r *raid, wave int32, e *Entity) {
	r.leaders[wave] = e.ID
	if wb, ok := world.ItemID("white_banner"); ok {
		if e.Equips == nil {
			e.Equips = map[int32]ItemStack{}
		}
		e.Equips[equipHelmet] = ItemStack{ID: wb, Count: 1, Ominous: true}
	}
}

func raidCanBeLeader(name string) bool {
	switch name {
	case "pillager", "vindicator", "evoker", "illusioner":
		return true
	}
	return false
}

// raidRaiderType is Raider's joinRaid-able family: the five raider
// types plus the illusioner (leader-capable, never in wave tables).
func raidRaiderType(name string) bool {
	switch name {
	case "vindicator", "evoker", "pillager", "witch", "ravager", "illusioner":
		return true
	}
	return false
}

// raidJoin is Raid.joinRaid: the raider flags into the wave set and the
// wave's total health grows by its spawn health.
func (s *Server) raidJoin(r *raid, wave int32, e *Entity) {
	e.Raid = r
	e.Wave = wave
	e.CanJoinRaid = true // joinRaid latches the flag itself
	r.raiders[e.ID] = true
	r.totalHealth += e.Health
}

// raidBarUpdate is Raid.updateBossbar: progress is living-raider health
// over the wave's total.
func (s *Server) raidBarUpdate(r *raid) {
	var prog float32
	if r.totalHealth > 0 {
		prog = s.healthOfRaiders(r) / r.totalHealth
	}
	s.raidBarProgress(r, prog)
}

// raidUpdatePlayers is Raid.updatePlayers: the bar reaches a session
// when getRaidAt(playerPos) is this raid — alive and this is the
// nearest non-stopped raid within 96 blocks; leaving that set drops it.
func (s *Server) raidUpdatePlayers(d *dimension, r *raid) {
	s.sessions.Range(func(_, v any) bool {
		t := v.(*Session)
		inside := false
		if t.state.Load() == StatePlay && t.spawned.Load() && t.dimName() == d.name {
			x, y, z, _, _, _ := t.Pos()
			inside = d.raidAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))) == r
		}
		if inside && !r.viewers[t] {
			r.viewers[t] = true
			_ = t.write(t.ids().PlayBossEvent, raidBarPacket(r, 0, raidBarAdd(r)))
		} else if !inside && r.viewers[t] {
			delete(r.viewers, t)
			_ = t.write(t.ids().PlayBossEvent, raidBarPacket(r, 1, nil))
		}
		return true
	})
}

// raidPlayHorn is Raid.playSound's outside-the-village branch: the horn
// lands at the spawn pos' xz but each hearer's own y, volume 64.
func (s *Server) raidPlayHorn(d *dimension, r *raid, pos world.Pos) {
	s.sessions.Range(func(_, v any) bool {
		t := v.(*Session)
		if t.state.Load() != StatePlay || t.dimName() != d.name {
			return true
		}
		x, y, z, _, _, _ := t.Pos()
		dx := x - (float64(pos.X) + 0.5)
		dz := z - (float64(pos.Z) + 0.5)
		if dx*dx+dz*dz > raidHornDistanceSq {
			return true
		}
		var p protocol.Buffer
		p.Byte(0)
		p.String(raidHornSound)
		p.Byte(0) // no fixed range in direct SoundEvent
		p.VarInt(soundSources["neutral"])
		p.Int(int32((float64(pos.X) + 0.5) * 8))
		p.Int(int32(y * 8))
		p.Int(int32((float64(pos.Z) + 0.5) * 8))
		p.Float(64)
		p.Float(1)
		p.Long(rand.Int63())
		_ = t.write(t.ids().PlaySound, p.Bytes())
		return true
	})
}

// raidUpdateRaiders drops raiders that left the valid radius (a
// distance-based removeFromRaid): 112² blocks squared from center.
func (s *Server) raidUpdateRaiders(r *raid) {
	var dead []int32
	for eid := range r.raiders {
		e, ok := s.entities.GetPtr(eid)
		if !ok || e.Dying {
			dead = append(dead, eid)
			continue
		}
		dx := e.X - (float64(r.center.X) + 0.5)
		dz := e.Z - (float64(r.center.Z) + 0.5)
		if dx*dx+dz*dz >= raidDropRadiusSq {
			dead = append(dead, eid)
		}
	}
	for _, eid := range dead {
		if e, ok := s.entities.GetPtr(eid); ok && e.Raid == r {
			e.Raid = nil
		}
		delete(r.raiders, eid)
	}
}

// raidBarRaidersName applies the "Raid - raiders remaining" name while
// the living raider count is one or two.
func raidBarRaidersName(alive int32) component {
	if alive >= 1 && alive <= 2 {
		return component{
			translate: "event.minecraft.raid",
			extra: []component{
				{text: " - "},
				{translate: "event.minecraft.raid.raiders_remaining",
					with: []any{alive}},
			},
		}
	}
	return raidBarRaid
}

// raidVictory finishes a cleared raid: hero_of_the_village lands on
// every hero (omen-1 amplifier, 48000 ticks), the raid_win stat and the
// hero_of_the_village trigger fire, and the bar turns to victory for
// the 600-tick celebration.
func (s *Server) raidVictory(r *raid) {
	r.status = raidVictory
	s.raidBarProgress(r, 0)
	s.raidBarName(r, raidBarVictory)
	for u := range r.heroes {
		s.sessions.Range(func(_, v any) bool {
			t := v.(*Session)
			if t.getUUID() == u {
				// MobEffectInstance(HERO_OF_THE_VILLAGE, 48000, omen-1,
				// ambient=false, visible=false, showIcon=true): flags 0x04.
				s.applyMobEffectWire(t, effectHeroOfVillage, r.omen-1, raidHeroTicks, false, 0x04)
				t.addCustomStat("raid_win", 1)
				s.fireHeroOfVillage(t)
				return false
			}
			return true
		})
	}
}

// raidStop is Raid.stop(): the event bar leaves every viewer.
func (s *Server) raidStop(d *dimension, r *raid) {
	r.status = raidStopped
	for t := range r.viewers {
		if t.state.Load() == StatePlay {
			_ = t.write(t.ids().PlayBossEvent, raidBarPacket(r, 1, nil))
		}
	}
	r.viewers = map[*Session]bool{}
	for eid := range r.raiders {
		if e, ok := s.entities.GetPtr(eid); ok && e.Raid == r {
			e.Raid = nil
		}
	}
}

// raidTickStep runs every dimension's raids once per GameTick — vanilla
// ticks Raids once per ServerLevel tick.
func (s *Server) raidTickStep() {
	for _, d := range s.dims {
		if len(d.raids) == 0 {
			continue
		}
		for id, r := range d.raids {
			s.raidTick(d, id, r)
		}
	}
}

func (s *Server) raidTick(d *dimension, id int, r *raid) {
	if r.status == raidStopped {
		delete(d.raids, id)
		return
	}
	if r.status != raidOngoing {
		// Victory/defeat celebration: the final bar state reasserts each
		// tick (victory→0, loss→1) and the raid tears down after 600.
		r.celebrate++
		if r.celebrate >= raidCelebration {
			s.raidStop(d, r)
			delete(d.raids, id)
			return
		}
		if r.celebrate%20 == 0 {
			s.raidUpdatePlayers(d, r)
		}
		if r.status == raidVictory {
			s.raidBarProgress(r, 0)
			s.raidBarName(r, raidBarVictory)
		} else {
			s.raidBarProgress(r, 1)
			s.raidBarName(r, raidBarDefeat)
		}
		return
	}
	// Peaceful difficulty stops an ongoing raid outright.
	if int32(s.difficulty) == 0 {
		s.raidStop(d, r)
		return
	}
	// The raid only lives while its center is in a village; once the
	// claims dissolve, a started raid flips to LOSS and an unstarted
	// one stops silently.
	if !d.raidIsVillage(r.center) {
		if r.spawned > 0 {
			r.status = raidLoss
		} else {
			s.raidStop(d, r)
		}
		return
	}
	r.ticksActive++
	if r.ticksActive >= raidHeroTicks {
		s.raidStop(d, r)
		return
	}
	alive := r.alive()
	if alive == 0 && r.hasMoreWaves() {
		if r.cooldown > 0 {
			// waveSpawnPos maintenance: re-roll the candidate every 5
			// ticks while empty (the entity-ticking invalidation is a
			// no-op on the flat world).
			if !r.hasSpawnPos && r.cooldown%5 == 0 {
				if pos, ok := s.raidValidSpawnPos(d, r); ok {
					r.spawnPos, r.hasSpawnPos = pos, true
				}
			}
			if r.cooldown == raidCooldownTicks || r.cooldown%20 == 0 {
				s.raidUpdatePlayers(d, r)
			}
			r.cooldown--
			s.raidBarProgress(r, float32(raidCooldownTicks-r.cooldown)/raidCooldownTicks)
		} else if r.spawned > 0 {
			// A cleared wave refills the cooldown; the spawn itself
			// fires on the tick the countdown reaches zero.
			r.cooldown = raidCooldownTicks
			s.raidBarNameAlive(r, raidBarRaid, 0)
			return
		}
		// cooldown==0 && spawned==0 falls through to the shared block.
	}
	// Shared block: the 20-tick viewer/raider refresh, the bar's
	// raiders_remaining name, the spawn loop, and the victory check.
	if r.ticksActive%20 == 0 {
		s.raidUpdatePlayers(d, r)
		s.raidUpdateRaiders(r)
		alive = r.alive()
	}
	if alive >= 1 && alive <= 2 {
		s.raidBarNameAlive(r, raidBarRaidersName(alive), alive)
	} else {
		s.raidBarNameAlive(r, raidBarRaid, 0)
	}
	horned := false
	for r.shouldSpawnGroup() {
		pos, ok := r.spawnPos, r.hasSpawnPos
		if !ok {
			pos, ok = s.raidValidSpawnPos(d, r)
		}
		if !ok {
			r.tries++
			if r.tries > raidSpawnRetries {
				s.raidStop(d, r)
				delete(d.raids, id)
				return
			}
			continue
		}
		r.started = true
		s.spawnRaidGroup(d, r, pos)
		if !horned {
			s.raidPlayHorn(d, r, pos)
			horned = true
		}
	}
	if r.started && !r.hasMoreWaves() && alive == 0 {
		r.postTicks++
		if r.postTicks >= raidPostTicks {
			s.raidVictory(r)
		}
	}
}

// --- villager POI claims ----------------------------------------------

// villagerPOIStep is the villager brain's POI-claim pass, run inside
// mobTickStep: every 400 ticks an unclaimed villager claims the nearest
// unclaimed bed or bell within 48 blocks (vanilla's AcquirePoi range).
func (s *Server) villagerPOIStep(d *dimension, e *Entity) {
	if e.POITicks > 0 {
		e.POITicks--
		return
	}
	e.POITicks = 400
	if e.HasPOI {
		return
	}
	if d.claimedPOIs == nil {
		d.claimedPOIs = map[world.Pos]int32{}
	}
	bx, by, bz := int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))
	var best world.Pos
	bestD := math.MaxFloat64
	found := false
	for dy := -16; dy <= 16; dy++ {
		for dx := -48; dx <= 48; dx++ {
			for dz := -48; dz <= 48; dz++ {
				pos := world.Pos{X: bx + dx, Y: by + dy, Z: bz + dz}
				b := d.w.GetBlock(pos)
				if !villagePOIBlock(b) {
					continue
				}
				if _, claimed := d.claimedPOIs[pos]; claimed {
					continue
				}
				dd := float64(dx*dx + dy*dy + dz*dz)
				if dd < bestD {
					best, bestD, found = pos, dd, true
				}
			}
		}
	}
	if !found {
		return
	}
	d.claimedPOIs[best] = e.ID
	e.POIPos, e.HasPOI = best, true
}

// villagePOIBlock is the raid POI predicate: beds (villager home) and
// the bell (meeting point) are the claimable set vanilla tests for the
// village location.
func villagePOIBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	return strings.HasSuffix(name, "_bed") || name == "bell"
}

// releasePOI frees a villager's claim when it dies or despawns.
func (s *Server) releasePOI(d *dimension, e *Entity) {
	if !e.HasPOI {
		return
	}
	if d.claimedPOIs[e.POIPos] == e.ID {
		delete(d.claimedPOIs, e.POIPos)
	}
	e.HasPOI = false
}

// --- /raid ------------------------------------------------------------

// registerRaidCommand wires /raid's vanilla surface:
//
//	raid start <omenlvl>
//	raid stop
//	raid check
//	raid sound <type>
//	raid spawnleader
//	raid setomen <level>
//	raid glow
func (s *Server) registerRaidCommand() {
	s.registerComponentCommand("raid", func(player string, args []string) reply {
		return s.runRaid(player, args)
	})
}

func (s *Server) runRaid(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	sess := s.findSessionByName(player)
	if sess == nil {
		// getPlayerOrException: console/non-player sources can't run
		// /raid at all.
		return componentReply(commandError("argument.player.missing"))
	}
	d := sess.dimension()
	x, y, z, _, _, _ := sess.Pos()

	switch args[0] {
	case "start":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		omen, err := strconv.Atoi(args[1])
		if err != nil {
			return errToken("parsing.int.expected", 2)
		}
		if omen < 0 {
			return errToken("argument.integer.low", 2, int32(0), int32(omen))
		}
		bp := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
		if d.isRaided(bp.X, bp.Y, bp.Z) {
			return componentReply(commandError("Raid already started close by"))
		}
		r := s.createOrExtendRaid(sess, d, bp)
		if r == nil {
			return componentReply(commandError("Failed to create a raid in your local village"))
		}
		r.omen = int32(omen)
		return componentReply(literal("Created a raid in your local village"))
	case "stop":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		r := d.raidAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
		if r == nil {
			return componentReply(commandError("No raid here"))
		}
		s.raidStop(d, r)
		return componentReply(literal("Stopped raid"))
	case "check":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		r := d.raidAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
		if r == nil {
			return componentReply(commandError("Found no started raids"))
		}
		// Vanilla's check sends two system messages: the banner line,
		// then the stats line.
		return componentReplyWith(literal("Found a started raid!"),
			literal(fmt.Sprintf(
				"Num groups spawned:%d Raid omen level:%d Num mobs:%d Raid health:%f /%f",
				r.spawned, r.omen, r.alive(),
				s.healthOfRaiders(r), r.totalHealth)))
	case "sound":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		// The <type> arg is a text component; only one whose getString()
		// is "local" sounds the horn — every other component is a silent
		// success (RaidCommand.playSound).
		comp, _, cerr := parseComponentArg(strings.Join(args[1:], " "))
		if cerr != nil {
			return errToken("argument.component.invalid", 2)
		}
		if raidCompString(comp) == "local" {
			s.playSeededSoundAt(d, x+5, y, z, raidHornSound, soundSources["neutral"], 2, 1)
		}
		return reply{result: 1, hasResult: true}
	case "spawnleader":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		// Vanilla announces first, then spawns — a failed create still
		// leaves the success line in the client's feed.
		replyErr := componentReply(literal("Spawned a raid captain"))
		e, ok := s.summonMobRaw(d, "pillager", x, y, z, false, false)
		if !ok {
			return componentReplyWith(
				literal("Spawned a raid captain"),
				commandError("Pillager failed to spawn"))
		}
		e.PatrolLeader = true
		if wb, ok := world.ItemID("white_banner"); ok {
			if e.Equips == nil {
				e.Equips = map[int32]ItemStack{}
			}
			e.Equips[equipHelmet] = ItemStack{ID: wb, Count: 1, Ominous: true}
		}
		s.broadcastFreshMob(e)
		return replyErr
	case "setomen":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		r := d.raidAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z)))
		if r == nil {
			return componentReply(commandError("No raid found here"))
		}
		level, err := strconv.Atoi(args[1])
		if err != nil {
			return errToken("parsing.int.expected", 2)
		}
		if level < 0 {
			return errToken("argument.integer.low", 2, int32(0), int32(level))
		}
		if level > raidOmenMax {
			return componentReply(commandError(fmt.Sprintf(
				"Sorry, the max raid omen level you can set is %d", raidOmenMax)))
		}
		old := r.omen
		r.omen = int32(level)
		return componentReply(literal(fmt.Sprintf(
			"Changed village's raid omen level from %d to %d", old, level)))
	case "glow":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		// addEffect(GLOWING, 1000, 1) on every raider — the glowing
		// shared flag does the visual; the instance rides e.Effects so
		// splash clears and milk see it.
		if r := d.raidAt(int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))); r != nil {
			for eid := range r.raiders {
				if e, ok := s.entities.GetPtr(eid); ok && !e.Dying {
					s.mobGiveEffect(e, effectGlowing, 1, 1000, false)
					e.GlowUntil = s.gameTime() + 1000
					s.broadcastMobSharedFlags(e.ID, e.FireTicks > 0, true)
				}
			}
		}
		return reply{result: 1, hasResult: true}
	}
	return errToken("command.unknown.argument", 1)
}

// raidCompString is MutableComponent.getString() for the parsed arg —
// literal text plus extra children; a translatable contributes its key
// (the dedicated server resolves no translations).
func raidCompString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case map[string]any:
		var sb strings.Builder
		if t, ok := c["text"].(string); ok {
			sb.WriteString(t)
		}
		if k, ok := c["translate"].(string); ok {
			sb.WriteString(k)
		}
		if ex, ok := c["extra"].(nbt.List); ok {
			for _, it := range ex.Items {
				sb.WriteString(raidCompString(compValue(it)))
			}
		}
		return sb.String()
	case nbt.List:
		var sb strings.Builder
		for _, it := range c.Items {
			sb.WriteString(raidCompString(compValue(it)))
		}
		return sb.String()
	}
	return ""
}

// playSeededSoundAt writes the entity-positioned sound packet to every
// session tracking the dimension.
func (s *Server) playSeededSoundAt(d *dimension, x, y, z float64, name string, cat int32, vol, pitch float32) {
	var p protocol.Buffer
	p.Byte(0)
	p.String(name)
	p.Byte(0) // no fixed range in direct SoundEvent
	p.VarInt(cat)
	p.Int(int32(x * 8))
	p.Int(int32(y * 8))
	p.Int(int32(z * 8))
	p.Float(vol)
	p.Float(pitch)
	p.Long(rand.Int63())
	body := p.Bytes()
	s.sessions.Range(func(_, v any) bool {
		t := v.(*Session)
		if t.state.Load() == StatePlay && t.dimName() == d.name {
			_ = t.write(t.ids().PlaySound, body)
		}
		return true
	})
}

// absorbRaidOmen is the raid_omen hand-off inside createOrExtendRaid:
// an unstarted (or under-level) raid absorbs the starter's effect,
// bumping its omen level by amplifier+1 clamped to the max and firing
// raid_trigger + the voluntary_exile trigger.
func (s *Server) absorbRaidOmen(sess *Session, r *raid) {
	if r.started && r.omen >= raidOmenMax {
		return
	}
	sess.effMu.Lock()
	inst, ok := sess.effects[effectRaidOmen]
	if !ok {
		sess.effMu.Unlock()
		return
	}
	delete(sess.effects, effectRaidOmen)
	sess.effMu.Unlock()
	s.finishRemovedMobEffect(sess, effectRaidOmen)
	r.omen += inst.amplifier + 1
	if r.omen > raidOmenMax {
		r.omen = raidOmenMax
	}
	if r.omen < 0 {
		r.omen = 0
	}
	if r.spawned == 0 {
		sess.addCustomStat("raid_trigger", 1)
	}
	s.fireRaidOmen(sess)
}
