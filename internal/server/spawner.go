package server

import (
	"math"
	"math/rand"
	"sort"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// spawnerShort reads one of the spawner's short fields, def when absent.
func spawnerShort(f map[string]any, key string, def int) int {
	switch v := f[key].(type) {
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int8:
		return int(v)
	case int64:
		return int(v)
	}
	return def
}

// spawnerEntityID is the id of the spawner's next SpawnData entity, ""
// when there is none.
func spawnerEntityID(f map[string]any) string {
	sd, _ := f["SpawnData"].(map[string]any)
	ent, _ := sd["entity"].(map[string]any)
	id, _ := ent["id"].(string)
	return id
}

// spawnerAt is the spawner block entity at pos, created at its fresh
// defaults when the block has none yet.
func (s *Server) spawnerAt(d *dimension, pos world.Pos) *genericBlockEntity {
	if bstate(d.w.GetBlock(pos)) != "spawner" {
		return nil
	}
	if g, _, ok := genericBlockEntityAt(d, pos); ok {
		return g
	}
	if d.genericBE == nil {
		d.genericBE = map[world.Pos]*genericBlockEntity{}
	}
	g := &genericBlockEntity{id: "minecraft:mob_spawner", fields: genBlockEntityDefaults("mob_spawner")}
	d.genericBE[pos] = g
	return g
}

// spawnerSetEntity is BaseSpawner.setEntityId (a spawn egg on the
// block): the next SpawnData's entity id, then SpawnerBlockEntity's
// sendBlockUpdated — the update tag to the chunk's watchers.
func (s *Server) spawnerSetEntity(d *dimension, pos world.Pos, ent string) {
	g := s.spawnerAt(d, pos)
	if g == nil {
		return
	}
	sd, _ := g.fields["SpawnData"].(map[string]any)
	if sd == nil {
		sd = map[string]any{}
	}
	e, _ := sd["entity"].(map[string]any)
	if e == nil {
		e = map[string]any{}
	}
	e["id"] = "minecraft:" + strings.TrimPrefix(ent, "minecraft:")
	sd["entity"] = e
	g.fields["SpawnData"] = sd
	s.spawnerSendUpdate(d, pos, g)
}

// spawnerSendUpdate sends the spawner's update tag (its saved fields
// without SpawnPotentials).
func (s *Server) spawnerSendUpdate(d *dimension, pos world.Pos, g *genericBlockEntity) {
	spec, ok := blockEntitySpecForType("mob_spawner")
	if !ok {
		return
	}
	if raw := blockEntityUpdateTag("mob_spawner", genericSaveFields(d, pos, g)); raw != nil {
		s.broadcastBlockEntityData(d, pos, spec.id, raw)
	}
}

// spawnerTickStep runs BaseSpawner.serverTick for every spawner block
// entity in a loaded chunk.
func (s *Server) spawnerTickStep() {
	for _, d := range s.dims {
		var spawners []world.Pos
		for pos, g := range d.genericBE {
			if g.id == "minecraft:mob_spawner" && chunkLoaded(d, pos.X>>4, pos.Z>>4) {
				spawners = append(spawners, pos)
			}
		}
		// Deterministic order for the shared random's draws.
		sort.Slice(spawners, func(i, j int) bool {
			a, b := spawners[i], spawners[j]
			if a.X != b.X {
				return a.X < b.X
			}
			if a.Z != b.Z {
				return a.Z < b.Z
			}
			return a.Y < b.Y
		})
		for _, pos := range spawners {
			if g, _, ok := genericBlockEntityAt(d, pos); ok {
				s.spawnerServerTick(d, pos, g)
			}
		}
	}
}

// spawnerServerTick is BaseSpawner.serverTick. It idles unless an alive
// non-spectator player is within RequiredPlayerRange of the block centre
// and spawner_blocks_work holds; a Delay of -1 re-rolls, a positive one
// counts down. At zero, SpawnCount attempts each pick a cell within
// SpawnRange (x and z by nextDouble - nextDouble, y by nextInt(3) - 1),
// need a collision-free spawn box, the type's SPAWNER placement rule,
// fewer than MaxNearbyEntities of the type around the spawner (else
// the spawner re-delays and stops), a random yaw, the mob's own
// checkSpawnRules and checkSpawnObstruction, finalizeSpawn for a bare
// {id} entity, then the 2004 level event at the spawner and the mob's
// spawn poof (entity event 20). Any spawn re-delays.
func (s *Server) spawnerServerTick(d *dimension, pos world.Pos, g *genericBlockEntity) {
	f := g.fields
	rng := spawnerShort(f, "RequiredPlayerRange", 16)
	if !s.nearbyAlivePlayer(d, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, float64(rng)) ||
		!s.gameRuleBool("minecraft:spawner_blocks_work") {
		return
	}
	delay := spawnerShort(f, "Delay", 20)
	if delay == -1 {
		s.spawnerDelay(d, pos, g)
		delay = spawnerShort(f, "Delay", 20)
	}
	if delay > 0 {
		f["Delay"] = int16(delay - 1)
		return
	}
	s.spawnerNextData(d, pos, g)
	count := spawnerShort(f, "SpawnCount", 4)
	spawnRange := float64(spawnerShort(f, "SpawnRange", 4))
	maxNearby := spawnerShort(f, "MaxNearbyEntities", 6)
	r := s.spawnRng()
	spawned := false
	for i := 0; i < count; i++ {
		id := strings.TrimPrefix(spawnerEntityID(f), "minecraft:")
		et, ok := EntityTypeID(id)
		if !ok {
			s.spawnerDelay(d, pos, g)
			return
		}
		x := float64(pos.X) + (rand.Float64()-rand.Float64())*spawnRange + 0.5
		y := float64(pos.Y + rand.Intn(3) - 1)
		z := float64(pos.Z) + (rand.Float64()-rand.Float64())*spawnRange + 0.5
		w, h := spawnAABBDims(id)
		if s.spawnBoxCollides(d, x-w/2, y, z-w/2, x+w/2, y+h, z+w/2) {
			continue
		}
		bp := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
		if !s.spawnerPlacementOK(d, id, bp, r) {
			continue
		}
		if _, living := entityAttrBases[id]; !living {
			// Only mobs spawn here; another type's spawner re-delays.
			s.spawnerDelay(d, pos, g)
			return
		}
		if s.spawnerNearbyCount(d, pos, et, spawnRange) >= maxNearby {
			s.spawnerDelay(d, pos, g)
			return
		}
		yaw := rand.Float32() * 360
		if s.walkTargetValue(d, id, bp) < 0 || !s.spawnObstructionOk(d, id, x, y, z, true) {
			continue
		}
		e := s.naturalSpawnMob(d, id, x, y, z, yaw, false, r)
		if e == nil {
			s.spawnerDelay(d, pos, g)
			return
		}
		s.broadcastLevelEvent(d.name, 2004, pos, 0)
		s.broadcastEntityEventTo(nil, d.name, e.ID, 20)
		spawned = true
	}
	if spawned {
		s.spawnerDelay(d, pos, g)
	}
}

// spawnerPlacementOK is SpawnPlacements.checkSpawnRules for the SPAWNER
// reason: the registered rule, whose Mob.checkMobSpawnRules half skips
// the valid-spawn block test (EntitySpawnReason.isSpawner); light
// requirements still apply (only TRIAL_SPAWNER ignores them).
func (s *Server) spawnerPlacementOK(d *dimension, name string, p world.Pos, r spawnRandom) bool {
	if notInPeaceful[name] && s.difficulty == DifficultyPeaceful {
		return false
	}
	pl, ok := spawnPlacements[name]
	if !ok {
		return true
	}
	switch pl.rule {
	case ruleMob, ruleAnyLightMonster, ruleEndermite, ruleSilverfish, ruleSkeletonHorse, rulePatrolling:
		return true
	case ruleMonster:
		return s.darkEnoughToSpawn(d, p, r)
	case ruleSurfaceMonsters:
		return s.darkEnoughToSpawn(d, p, r) && s.canSeeSky(d, p)
	}
	return s.spawnRuleHolds(d, pl.rule, name, p, r)
}

// spawnerNextData is getOrCreateNextSpawnData: a SpawnData already set
// stays; otherwise a weighted pick from SpawnPotentials, else an empty
// entity.
func (s *Server) spawnerNextData(d *dimension, pos world.Pos, g *genericBlockEntity) {
	if _, ok := g.fields["SpawnData"].(map[string]any); ok {
		return
	}
	if sd, ok := spawnerPickPotential(g.fields); ok {
		g.fields["SpawnData"] = sd
	} else {
		g.fields["SpawnData"] = map[string]any{"entity": map[string]any{}}
	}
	s.spawnerSendUpdate(d, pos, g)
}

// spawnerPickPotential is WeightedList.getRandom over SpawnPotentials
// ({weight, data} entries).
func spawnerPickPotential(f map[string]any) (map[string]any, bool) {
	l, ok := f["SpawnPotentials"].(nbt.List)
	if !ok || len(l.Items) == 0 {
		return nil, false
	}
	total := 0
	for _, it := range l.Items {
		m, _ := it.(map[string]any)
		total += spawnerShort(m, "weight", 1)
	}
	if total <= 0 {
		return nil, false
	}
	n := rand.Intn(total)
	for _, it := range l.Items {
		m, _ := it.(map[string]any)
		if n -= spawnerShort(m, "weight", 1); n < 0 {
			sd, ok := m["data"].(map[string]any)
			return sd, ok
		}
	}
	return nil, false
}

// spawnerDelay is BaseSpawner.delay: Delay = MinSpawnDelay when the
// range is empty, else MinSpawnDelay + nextInt(Max - Min); a potentials
// pick replaces the next SpawnData; then block event 1 on the spawner.
func (s *Server) spawnerDelay(d *dimension, pos world.Pos, g *genericBlockEntity) {
	f := g.fields
	lo, hi := spawnerShort(f, "MinSpawnDelay", 200), spawnerShort(f, "MaxSpawnDelay", 800)
	if hi <= lo {
		f["Delay"] = int16(lo)
	} else {
		f["Delay"] = int16(lo + rand.Intn(hi-lo))
	}
	if sd, ok := spawnerPickPotential(f); ok {
		f["SpawnData"] = sd
		s.spawnerSendUpdate(d, pos, g)
	}
	s.blockEvents = append(s.blockEvents, rsBlockEvent{d.name, pos, d.w.GetBlock(pos), 1, 0})
}

// spawnerNearbyCount counts non-spectator entities of the exact type
// whose boxes touch the spawner cell inflated by SpawnRange.
func (s *Server) spawnerNearbyCount(d *dimension, pos world.Pos, et int32, r float64) int {
	x0, y0, z0 := float64(pos.X)-r, float64(pos.Y)-r, float64(pos.Z)-r
	x1, y1, z1 := float64(pos.X)+1+r, float64(pos.Y)+1+r, float64(pos.Z)+1+r
	n := 0
	for _, e := range s.entities.All() {
		if e.Dim != d.name || e.Type != et || e.Dying {
			continue
		}
		dims := entityBBDims(&e)
		hw := dims[0] / 2
		if e.X+hw > x0 && e.X-hw < x1 && e.Y+dims[1] > y0 && e.Y < y1 && e.Z+hw > z0 && e.Z-hw < z1 {
			n++
		}
	}
	return n
}

// nearbyAlivePlayer is Level.hasNearbyAlivePlayer: an alive,
// non-spectator player strictly within range of the point.
func (s *Server) nearbyAlivePlayer(d *dimension, x, y, z, r float64) bool {
	found := false
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead || sess.dimName() != d.name || sess.mode() == GameModeSpectator {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if (px-x)*(px-x)+(py-y)*(py-y)+(pz-z)*(pz-z) < r*r {
			found = true
			return false
		}
		return true
	})
	return found
}
