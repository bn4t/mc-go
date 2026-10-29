package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// patrolTickStep is PatrolSpawner.tick, the overworld's natural patrol
// cycle. The countdown is vanilla's: constructed at 0, decremented
// every level tick, and when it lands at or below zero it rearms to
// 12000+rand(1200) — so the first roll can't happen before ~10 minutes
// and the pace never depends on where players stand. A rearmed cycle
// then needs daylight (isBrightOutside), a 1-in-5 roll, at least one
// non-spectator overworld player picked at random, and no village POI
// within two sections of them. The spawn square is ±(24..47) away on
// each axis; the member count is ceil(effectiveDifficulty)+1 pillagers,
// the first spawned flagged as the leader.
func (s *Server) patrolTickStep() {
	// ServerChunkCache.tickChunks gates ALL custom spawners on the
	// spawn_mobs gamerule — the countdown doesn't even decrement while
	// it's off — then PatrolSpawner itself gates on spawn_patrols.
	if !s.gameRuleBool("minecraft:spawn_mobs") || !s.gameRuleBool("minecraft:spawn_patrols") {
		return
	}
	s.patrolNext--
	if s.patrolNext > 0 {
		return
	}
	s.patrolNext = 12000 + int64(rand.Intn(1200))
	if !s.isBrightOutside() {
		return
	}
	if rand.Intn(5) != 0 {
		return
	}
	// CustomSpawner.tick(level, spawnEnemies) is additionally gated on
	// the spawn-monsters server property — mc-go has no such prop, the
	// gamerules are its equivalent. players() is the level's own list:
	// only overworld sessions can anchor a patrol.
	var cands []*Session
	s.sessions.Range(func(_, v any) bool {
		t := v.(*Session)
		if t.state.Load() == StatePlay && t.spawned.Load() && t.dimName() == dimOverworld {
			cands = append(cands, t)
		}
		return true
	})
	if len(cands) == 0 {
		return
	}
	sess := cands[rand.Intn(len(cands))]
	if sess.mode() == GameModeSpectator {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	d := s.dims[dimOverworld]
	pos := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
	if d.patrolNearVillage(pos) {
		return
	}
	pos.X += (24 + rand.Intn(24)) * patrolSign()
	pos.Z += (24 + rand.Intn(24)) * patrolSign()
	// Vanilla then checks hasChunksAt(±10) — every column of the flat
	// world is loaded — and the CAN_PILLAGER_PATROL_SPAWN environment
	// attribute: the early_game timeline ANDs it false until the
	// overworld clock passes tick 120000 (5 days in).
	if s.dayTime() < 120000 {
		return
	}
	count := int(math.Ceil(s.effectiveDifficulty())) + 1
	for i := 0; i < count; i++ {
		// getHeightmapPos(MOTION_BLOCKING_NO_LEAVES): feet land on the
		// first air cell over the top solid — TopMotionBlocking returns
		// the solid itself. (Its leaf carve-out can't differ here: the
		// flat world grows no trees.)
		pos.Y = d.w.TopMotionBlocking(pos.X, pos.Z, d.minY, d.minY+d.height) + 1
		if i == 0 {
			if !s.spawnPatrolMember(d, pos, true) {
				break
			}
		} else {
			s.spawnPatrolMember(d, pos, false)
		}
		pos.X += rand.Intn(5) - rand.Intn(5)
		pos.Z += rand.Intn(5) - rand.Intn(5)
	}
}

// spawnPatrolMember is PatrolSpawner.spawnPatrolMember: the feet cell
// must be a valid empty spawn block (non-solid, non-liquid, non-powered)
// and checkPatrollingMonsterSpawnRules requires block light ≤ 8 — the
// flat world carries no light engine, so block light is 0 and the gate
// can't fail — plus checkAnyLightMonsterSpawnRules' difficulty veto and
// its isUnobstructed check: a jittered cell whose pillager AABB
// (~0.6×1.95) overlaps a live entity silently skips that member.
func (s *Server) spawnPatrolMember(d *dimension, pos world.Pos, leader bool) bool {
	if !patrolSpawnBlockFree(d.w.GetBlock(pos)) {
		return false
	}
	if s.difficulty == DifficultyPeaceful {
		return false
	}
	if s.patrolSpawnObstructed(d, pos) {
		return false
	}
	// EntityType.create + setPos: vanilla places the mob at the block
	// corner, not the center.
	e, ok := s.summonMobRaw(d, "pillager", float64(pos.X), float64(pos.Y), float64(pos.Z), false, false)
	if !ok {
		return false
	}
	if leader {
		e.PatrolLeader = true
		s.patrolFindTarget(e)
	}
	// finalizeSpawn(PATROL): no 6% leader roll (patrol/event/structure
	// spawns skip it), the banner lands because PatrolLeader is set,
	// and every member is marked patrolling.
	s.mobDefaultEquipReason(e, spawnPatrol)
	s.broadcastFreshMob(e)
	return true
}

// patrolSpawnObstructed is the isUnobstructed half of vanilla's spawn
// rules: the new pillager's AABB can't overlap a live entity's. Vanilla
// shapes the box from the type's dimensions centered on the cell;
// pillagers are 0.6 wide × 1.95 tall, so ±0.3 on x/z and a 1.95-high
// column is the collision window against entity centers.
func (s *Server) patrolSpawnObstructed(d *dimension, pos world.Pos) bool {
	px, py, pz := float64(pos.X)+0.5, float64(pos.Y), float64(pos.Z)+0.5
	var hit bool
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if !o.Living || o.Dying || o.Dim != d.name {
			continue
		}
		if math.Abs(o.X-px) < 0.6 && math.Abs(o.Z-pz) < 0.6 &&
			o.Y < py+1.95 && o.Y+1.95 > py {
			hit = true
			break
		}
	}
	s.entities.mu.RUnlock()
	return hit
}

// patrolSpawnBlockFree is NaturalSpawner.isValidEmptySpawnBlock on the
// flat world: the feet cell must be non-solid and non-liquid.
func patrolSpawnBlockFree(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return true
	}
	if name == "water" || name == "lava" {
		return false
	}
	return name == "" || world.NonSolid(b)
}

// patrolSign is PatrolSpawner's ±1 axis flip.
func patrolSign() int {
	if rand.Intn(2) == 0 {
		return -1
	}
	return 1
}

// patrolNearVillage is ServerLevel.isCloseToVillage(pos, 2): a claimed
// village-tag POI section within two sections (on any axis) of the
// position's section vetoes the patrol.
func (d *dimension) patrolNearVillage(pos world.Pos) bool {
	sx, sy, sz := floorDiv(pos.X, 16), floorDiv(pos.Y, 16), floorDiv(pos.Z, 16)
	for p := range d.claimedPOIs {
		if !d.poiStillClaimed(p) {
			continue
		}
		if absi(floorDiv(p.X, 16)-sx) > 2 ||
			absi(floorDiv(p.Y, 16)-sy) > 2 ||
			absi(floorDiv(p.Z, 16)-sz) > 2 {
			continue
		}
		return true
	}
	return false
}

// patrolClass is the PatrollingMonster hierarchy — the class filter of
// findPatrolCompanions' getEntitiesOfClass. Every subclass returns true
// from canJoinPatrol, so membership alone is the predicate.
var patrolClass = map[string]bool{
	"pillager":   true,
	"vindicator": true,
	"evoker":     true,
	"illusioner": true,
	"witch":      true,
	"ravager":    true,
}

// patrolFindTarget is PatrollingMonster.findPatrolTarget: patrolTarget
// lands up to ±500 blocks out on each axis at the mob's own y, and the
// mob is patrolling from now on.
func (s *Server) patrolFindTarget(e *Entity) {
	e.PatrolTarget = world.Pos{
		X: int(math.Floor(e.X)) - 500 + rand.Intn(1000),
		Y: int(math.Floor(e.Y)),
		Z: int(math.Floor(e.Z)) - 500 + rand.Intn(1000),
	}
	e.Patrolling = true
	e.HasPatrolTarget = true
}

// patrolCompanions is LongDistancePatrolGoal.findPatrolCompanions:
// every PatrollingMonster-class mob whose bounding box sits inside the
// walker's, inflated by 16 — mc-go's ±16 position box is the same
// window up to half a mob width.
func (s *Server) patrolCompanions(e *Entity) []*Entity {
	var out []*Entity
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if o == e || o.Dim != e.Dim {
			continue
		}
		kn, _ := EntityTypeName(o.Type)
		if !patrolClass[kn] {
			continue
		}
		if math.Abs(o.X-e.X) <= 16 && math.Abs(o.Y-e.Y) <= 16 && math.Abs(o.Z-e.Z) <= 16 {
			out = append(out, o)
		}
	}
	s.entities.mu.RUnlock()
	return out
}

// mobPatrolStep is PatrollingMonster.LongDistancePatrolGoal (goal
// priority 4 — it only owns the tick once every combat goal above it
// has declined, so it sits between them and the ambient wander).
// canUse: patrolling, a live patrolTarget, no controlling passenger
// (Riding is vetted by the caller). tick: vanilla runs the body when
// navigation reports done; mc-go's walk is stepwise, so the body runs
// every tick. A patrol alone in its companion box stops patrolling; a
// leader within 10 of the target's center re-rolls it ±500 via
// findPatrolTarget. Otherwise the step heads for a waypoint pushed 10
// blocks toward the target with the rotated 0.4 skew — yRot(90) is
// radians in vanilla (a ~117° sweep, not the 90° the literal suggests)
// — at 0.595 for the leader and 0.7 for companions, each scaled by
// movement_speed. Every leader step writes the waypoint block back to
// each companion's patrolTarget. Vanilla's moveRandomly fallback and
// its 200-tick cooldown have no analogue: this walk can't fail.
func (s *Server) mobPatrolStep(e *Entity, d *dimension, w *world.World) bool {
	if !e.Patrolling || !e.HasPatrolTarget {
		return false
	}
	companions := s.patrolCompanions(e)
	if len(companions) == 0 {
		e.Patrolling = false
		return true
	}
	if !s.mobGrounded(e, w) {
		return true
	}
	// atBottomCenterOf: x/z get +0.5, y stays the block's own y.
	pvx, pvy, pvz := float64(e.PatrolTarget.X)+0.5, float64(e.PatrolTarget.Y), float64(e.PatrolTarget.Z)+0.5
	if e.PatrolLeader {
		dx, dy, dz := pvx-e.X, pvy+0.5-e.Y, pvz-e.Z
		if dx*dx+dy*dy+dz*dz < 100 {
			s.patrolFindTarget(e)
			return true
		}
	}
	dirx, diry, dirz := e.X-pvx, e.Y-pvy, e.Z-pvz
	c, sn := math.Cos(90.0), math.Sin(90.0)
	wx := pvx + (dirx*c+dirz*sn)*0.4
	wy := pvy + diry*0.4
	wz := pvz + (dirz*c-dirx*sn)*0.4
	tx, ty, tz := wx-e.X, wy-e.Y, wz-e.Z
	if n := math.Sqrt(tx*tx + ty*ty + tz*tz); n > 0 {
		tx, ty, tz = e.X+tx/n*10, e.Y+ty/n*10, e.Z+tz/n*10
	} else {
		tx, ty, tz = e.X, e.Y, e.Z
	}
	bp := world.Pos{X: int(math.Floor(tx)), Y: int(math.Floor(ty)), Z: int(math.Floor(tz))}
	bp.Y = d.w.TopMotionBlocking(bp.X, bp.Z, d.minY, d.minY+d.height) + 1
	speed := 0.7
	if e.PatrolLeader {
		speed = 0.595
	}
	kn, _ := EntityTypeName(e.Type)
	if mv, ok := entityAttrBases[kn]["movement_speed"]; ok {
		speed *= mv
	}
	s.mobWalkToSpeed(e, float64(bp.X)+0.5, float64(bp.Z)+0.5, speed)
	if e.PatrolLeader {
		for _, o := range companions {
			o.PatrolTarget = bp
			o.HasPatrolTarget = true
		}
	}
	return true
}

// despawnAnimalTypes are the Animal subclasses whose
// removeWhenFarAway is Animal's unconditional false, so neither
// distance branch of checkDespawn ever discards them. Overrides
// measured on 26.2 (javap): Chicken answers isChickenJockey, Ocelot
// and Cat !trusting/!tame && tickCount > 2400, Axolotl !fromBucket &&
// !hasCustomName, Hoglin/ZombieHorse/CamelHusk/AbstractNautilus true.
var despawnAnimalTypes = map[string]bool{
	"pig": true, "cow": true, "sheep": true, "mooshroom": true,
	"sniffer": true, "horse": true, "donkey": true, "mule": true,
	"llama": true, "trader_llama": true, "skeleton_horse": true,
	"camel": true, "wolf": true, "parrot": true, "armadillo": true,
	"goat": true, "turtle": true, "strider": true, "rabbit": true,
	"polar_bear": true, "fox": true, "panda": true, "bee": true,
	"frog": true, "happy_ghast": true,
}

// mobDespawnKinds are the Living types that are Entity subclasses of
// Mob — checkDespawn only runs on them (armor stands and clouds are
// LivingEntities, not Mobs).
var mobDespawnKinds = map[string]bool{}

func init() {
	for name := range mobHealth {
		if name != "armor_stand" {
			mobDespawnKinds[name] = true
		}
	}
	// The AbstractHorse family spawns off entityAttrBases (they used
	// to ride summonHealthDefault when the supplier skipped
	// max_health) — keep them in the summonable set.
	for _, name := range []string{
		"horse", "donkey", "mule", "llama", "trader_llama",
		"zombie_horse", "skeleton_horse", "camel", "camel_husk",
	} {
		mobDespawnKinds[name] = true
	}
	for name := range patrollingTypes {
		mobDespawnKinds[name] = true
	}
	// Every categorized type is a Mob too (the bosses keep whatever
	// the supplier table gave them: WitherBoss overrides checkDespawn).
	for name := range entityCategories {
		if name != "wither" && name != "ender_dragon" {
			mobDespawnKinds[name] = true
		}
	}
}

// mobRemoveWhenFarAway is Mob.removeWhenFarAway: the base answer is
// unconditional true, with the measured overrides — Animal false;
// PatrollingMonster !patrolling || dist² > 16384 (a Raider in a raid
// is persistent instead); villagers, wandering traders, golems,
// allays and wardens false; AbstractFish/Axolotl kept when bucketed
// or named; ZombieVillager kept while converting.
func (s *Server) mobRemoveWhenFarAway(e *Entity, distSqr float64) bool {
	name, _ := EntityTypeName(e.Type)
	if patrollingTypes[name] {
		if e.Raid != nil {
			return false
		}
		return !e.Patrolling || distSqr > 16384
	}
	switch name {
	case "chicken":
		return e.ChickenJockey
	case "ocelot", "cat":
		return !e.Tamed && e.TickCount > 2400
	case "cod", "salmon", "pufferfish", "tropical_fish", "tadpole", "axolotl":
		return !e.FromBucket && e.CustomName == ""
	case "zombie_villager":
		return !e.Converting
	case "piglin":
		return !e.PersistenceRequired
	case "villager", "wandering_trader", "iron_golem", "snow_golem",
		"copper_golem", "shulker", "allay", "warden":
		return false
	case "hoglin", "zombie_horse", "camel_husk", "nautilus", "zombie_nautilus":
		return true
	}
	if despawnAnimalTypes[name] {
		return false
	}
	return true
}

// mobCheckDespawn is Mob.checkDespawn, run once per tick for every mob
// inside the simulation-range gate: peaceful discards
// non-peaceful-allowed types outright; persistence (flag, passenger,
// leash) clears the idle counter and returns; otherwise the nearest
// non-spectator player's distance decides — beyond the category's
// despawnDistance (128, 64 for water ambient) the mob discards when
// removeWhenFarAway allows it, and beyond 32 the 1-in-800-per-tick
// idle roll applies once noActionTime passes 600. Within 32 the idle
// counter resets.
func (s *Server) mobCheckDespawn(e *Entity) bool {
	name, _ := EntityTypeName(e.Type)
	if !mobDespawnKinds[name] {
		return false
	}
	if s.difficulty == DifficultyPeaceful && notInPeaceful[name] {
		return true
	}
	// isPersistenceRequired || requiresCustomPersistence (passenger
	// or leashed, plus the per-type overrides).
	if e.PersistenceRequired || mobRequiresCustomPersistence(e, name) {
		e.NoActionTime = 0
		return false
	}
	// level.getNearestPlayer(this, -1): nearest live non-spectator in
	// the dimension, unbounded.
	nearest, found := math.MaxFloat64, false
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || p.mode() == GameModeSpectator || p.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := p.Pos()
		dx, dy, dz := e.X-px, e.Y-py, e.Z-pz
		if d := dx*dx + dy*dy + dz*dz; d < nearest {
			nearest = d
		}
		found = true
		return true
	})
	if !found {
		return false
	}
	// MobCategory.getDespawnDistance: 64 for WATER_AMBIENT, else 128.
	despawnDist := float64(mobCategoryInfo[entityCategoryOf(name)].despawn)
	if nearest > despawnDist*despawnDist && s.mobRemoveWhenFarAway(e, nearest) {
		return true
	}
	if e.NoActionTime > 600 && rand.Intn(800) == 0 && nearest > 32*32 && s.mobRemoveWhenFarAway(e, nearest) {
		return true
	}
	if nearest < 32*32 {
		e.NoActionTime = 0
	}
	return false
}

// isBrightOutside is ServerLevel.isBrightOutside (skyDarken < 4): the
// same sleep window sunBurnTick uses plus a rain veto.
func (s *Server) isBrightOutside() bool {
	dt := s.dayTime() % 24000
	if dt >= 12542 && dt <= 23459 {
		return false
	}
	return !s.weatherRaining.Load()
}
