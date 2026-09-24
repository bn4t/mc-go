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
// the AABB obstruction check (untestable on an empty surface).
func (s *Server) spawnPatrolMember(d *dimension, pos world.Pos, leader bool) bool {
	if !patrolSpawnBlockFree(d.w.GetBlock(pos)) {
		return false
	}
	if s.difficulty == DifficultyPeaceful {
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
		// findPatrolTarget: patrolTarget is up to ±500 blocks on each
		// axis at the mob's own y, and patrolling starts now.
		e.PatrolTarget = world.Pos{
			X: pos.X - 500 + rand.Intn(1000),
			Y: pos.Y,
			Z: pos.Z - 500 + rand.Intn(1000),
		}
	}
	// finalizeSpawn(PATROL): no 6% leader roll (patrol/event/structure
	// spawns skip it), the banner lands because PatrolLeader is set,
	// and every member is marked patrolling.
	s.mobDefaultEquipReason(e, spawnPatrol)
	s.broadcastFreshMob(e)
	return true
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

// isBrightOutside is ServerLevel.isBrightOutside (skyDarken < 4): the
// same sleep window sunBurnTick uses plus a rain veto.
func (s *Server) isBrightOutside() bool {
	dt := s.dayTime() % 24000
	if dt >= 12542 && dt <= 23459 {
		return false
	}
	return !s.weatherRaining.Load()
}
