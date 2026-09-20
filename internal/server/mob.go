package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// mobLoot is the vanilla drop table for the mobs mc-go can summon,
// reduced to a single guaranteed item per entry (counts beyond one are
// rolled uniformly in [min,max]).
type lootEntry struct {
	item     string
	min, max int32
}

var mobLoot = map[string][]lootEntry{
	"pig":      {{item: "porkchop", min: 1, max: 3}},
	"cow":      {{item: "beef", min: 1, max: 3}, {item: "leather", min: 0, max: 2}},
	"chicken":  {{item: "chicken", min: 1, max: 1}, {item: "feather", min: 0, max: 2}},
	"sheep":    {{item: "mutton", min: 1, max: 2}, {item: "white_wool", min: 1, max: 1}},
	"zombie":   {{item: "rotten_flesh", min: 0, max: 2}},
	"skeleton": {{item: "bone", min: 0, max: 2}, {item: "arrow", min: 0, max: 2}},
	"creeper":  {{item: "gunpowder", min: 0, max: 2}},
	"spider":   {{item: "string", min: 0, max: 2}},
}

// spawnMobLoot rolls and spawns a mob's drops at its position in the
// mob's dimension.
func (s *Server) spawnMobLoot(dim, name string, x, y, z float64) {
	for _, entry := range mobLoot[name] {
		id, ok := world.ItemID(entry.item)
		if !ok {
			continue
		}
		count := entry.min
		if entry.max > entry.min {
			count += int32(rand.Intn(int(entry.max-entry.min) + 1))
		}
		if count <= 0 {
			continue
		}
		e := s.entities.Spawn(&Entity{
			Type:        mustEntityType("item"),
			Dim:         dim,
			X:           x,
			Y:           y + 0.25,
			Z:           z,
			Item:        ItemStack{ID: id, Count: count},
			PickupDelay: 10,
		})
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	}
}

// mobXP is the experience orb count range for each mob, and hostile
// mobs additionally award a flat bonus.
var mobXP = map[string][2]int32{
	"pig":      {1, 3},
	"cow":      {1, 3},
	"chicken":  {1, 3},
	"sheep":    {1, 3},
	"zombie":   {5, 5},
	"skeleton": {5, 5},
	"creeper":  {5, 5},
	"spider":   {5, 5},
}

// spawnXPOrb creates one experience orb entity with its value metadata
// (index 8, VarInt), immediately collectable.
func (s *Server) spawnXPOrb(dim string, x, y, z float64, value int32) *Entity {
	if value <= 0 {
		return nil
	}
	e := s.entities.Spawn(&Entity{
		Type:        mustEntityType("experience_orb"),
		Dim:         dim,
		X:           x,
		Y:           y,
		Z:           z,
		PickupDelay: 0,
		Data:        value,
	})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeOrbMetadata(value))
	return e
}

// encodeOrbMetadata builds the Set Entity Data body for an experience
// orb: index 8, type 1 (VarInt), the value.
func encodeOrbMetadata(value int32) []byte {
	var b protocol.Buffer
	b.Byte(8)
	b.Byte(1)
	b.VarInt(value)
	b.Byte(0xFF)
	return b.Bytes()
}

// spawnMobXP rolls and spawns a mob's experience orbs.
func (s *Server) spawnMobXP(dim, name string, x, y, z float64) {
	rng, ok := mobXP[name]
	if !ok {
		return
	}
	value := rng[0]
	if rng[1] > rng[0] {
		value += int32(rand.Intn(int(rng[1]-rng[0]) + 1))
	}
	s.spawnXPOrb(dim, x, y, z, value)
}

// addXP grants experience points, recomputing level and bar progress
// with vanilla's thresholds, then syncs Set Experience.
func (sess *Session) addXP(points int32) {
	sess.xpTotal += points
	level, progress := experienceLevel(sess.xpTotal)
	sess.xpLevel = level
	sess.xpProgress = progress
	_ = sess.server.sendSetExperience(sess, progress, level, sess.xpTotal)
}

// experienceLevel converts total XP to the vanilla level/progress pair.
// XP needed to advance from level l: 2l+7 (0-15), 5l-38 (16-30),
// 9l-158 (31+); the bar fraction is progress within the current level.
func experienceLevel(total int32) (int32, float32) {
	level := int32(0)
	remaining := total
	for {
		need := int32(2*level + 7)
		switch {
		case level >= 31:
			need = 9*level - 158
		case level >= 16:
			need = 5*level - 38
		}
		if remaining < need {
			return level, float32(remaining) / float32(need)
		}
		remaining -= need
		level++
	}
}

func mustEntityType(name string) int32 {
	id, _ := EntityTypeID(name)
	return id
}

// mobHealth is the vanilla max health for the mobs mc-go can summon.
// Mobs beyond this table arrive with the broader entity milestone.
var mobHealth = map[string]float32{
	"pig":        10,
	"cow":        10,
	"chicken":    4,
	"sheep":      8,
	"zombie":     20,
	"skeleton":   20,
	"creeper":    20,
	"spider":     16,
	"sniffer":    14,
	"iron_golem": 100,
}

// runSummon implements /summon <entity>: the mob spawns at the
// player's feet.
func (s *Server) runSummon(player string, args []string) string {
	if len(args) == 0 {
		return "Usage: /summon <entity>"
	}
	sess := s.findSessionByName(player)
	if sess == nil {
		return "Unknown player: " + player
	}
	x, y, z, _, _, _ := sess.Pos()
	if len(args) >= 4 {
		var err error
		if x, err = parseDouble(args[1], x, true); err != nil {
			return "Invalid position " + args[1]
		}
		if y, err = parseDouble(args[2], y, false); err != nil {
			return "Invalid position " + args[2]
		}
		if z, err = parseDouble(args[3], z, true); err != nil {
			return "Invalid position " + args[3]
		}
	}
	noAI := false
	if len(args) >= 5 {
		if tag, _, err := parseSNBTTag(args[4], 0); err == nil && tag != nil {
			if v, ok := tag.vals["NoAI"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
				noAI = true
			}
		}
	}
	if _, ok := s.summonMob(sess.dimension(), strings.TrimPrefix(args[0], "minecraft:"), x, y, z, noAI); !ok {
		return "Unknown entity: " + args[0]
	}
	return "Summoned " + args[0]
}

// summonMob spawns one living entity at pos in dimension d and fans it
// out with its health metadata. Unknown entity types are rejected.
func (s *Server) summonMob(d *dimension, name string, x, y, z float64, noAI bool) (*Entity, bool) {
	id, ok := EntityTypeID(name)
	if !ok {
		return nil, false
	}
	health, known := mobHealth[name]
	if !known {
		return nil, false
	}
	e := s.entities.Spawn(&Entity{
		Type:      id,
		Dim:       d.name,
		X:         x,
		Y:         y,
		Z:         z,
		Living:    true,
		Health:    health,
		MaxHealth: health,
		NoAI:      noAI,
	})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeLivingMetadata(e))
	return e, true
}

// encodeLivingMetadata builds the Set Entity Data body for a living
// entity: health at index 9 (float), terminated.
func encodeLivingMetadata(e *Entity) []byte {
	return encodeLivingHealth(e.Health)
}

// encodeLivingHealth encodes the health metadata entry alone.
func encodeLivingHealth(health float32) []byte {
	var b protocol.Buffer
	b.Byte(9) // index: health
	b.Byte(3) // type: float
	b.Float(health)
	b.Byte(0xFF)
	return b.Bytes()
}

// damageMob applies damage to a living mob and syncs metadata; a lethal
// hit removes the entity. It reports the damage actually dealt — zero
// when the hit was swallowed by the post-hit invulnerability window.
func (s *Server) damageMob(e *Entity, amount float32) float32 {
	health, dealt, ok := s.entities.Damage(e.ID, amount)
	if !ok {
		return 0
	}
	if dealt <= 0 {
		return 0
	}
	if health <= 0 {
		// die(): the corpse event and loot/XP fire immediately, the
		// entity lingers twenty ticks, then poofs (event 60) and is
		// removed — all inside mobTickStep's per-mob pass.
		s.entities.SetDying(e.ID)
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 3)
		if name, ok := EntityTypeName(e.Type); ok {
			s.spawnMobLoot(e.Dim, name, e.X, e.Y, e.Z)
			s.spawnMobXP(e.Dim, name, e.X, e.Y, e.Z)
		}
		return dealt
	}
	s.broadcastEntityMetadata(e.ID, encodeLivingHealth(health))
	return dealt
}

// mobWalkSpeed is the blocks-per-tick stride of the wander AI.
const mobWalkSpeed = 0.1

// mobYawFor derives a stable wander angle from the mob id and step
// count, so tests observe the same path every run.
func mobYawFor(id int32, step int64) float32 {
	const a, c = int64(1103515245), int64(12345)
	x := (int64(id)*a + step*c) & 0x7FFFFFFF
	return float32(x%360) - 180
}

// mobTickStep advances living mobs one tick: gravity until they rest on
// a solid block, then a deterministic wander step that refuses to walk
// off a ledge. Fleeing, pathfinding, and mob-specific behaviour arrive
// with the entity-AI milestone.
func (s *Server) mobTickStep() {
	s.entities.mu.RLock()
	mobs := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		if e.Living {
			mobs = append(mobs, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range mobs {
		if e.Dying {
			if s.entities.TickDeath(e.ID) {
				s.broadcastEntityEventTo(nil, e.Dim, e.ID, 60)
				s.entities.Remove(e.ID)
				s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			}
			continue
		}
		if e.invulnTicks > 0 {
			e.invulnTicks--
		}
		if e.NoAI {
			continue
		}
		w := s.dimWorld(e.Dim)
		if w == nil {
			continue
		}
		below := world.Pos{
			X: int(math.Floor(e.X)),
			Y: int(math.Floor(e.Y)) - 1,
			Z: int(math.Floor(e.Z)),
		}
		grounded := w.GetBlock(below) != world.Air
		if !grounded {
			e.MotionY -= 0.08
			if e.MotionY < -1.0 {
				e.MotionY = -1.0
			}
			e.Y += e.MotionY
			s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, 0, false)
			continue
		}
		e.MotionY = 0
		e.Y = math.Floor(e.Y)
		e.Grounded = true
		e.WanderTicks++
		// Change heading every 40 steps; walk otherwise.
		if e.WanderTicks%40 == 1 {
			e.Yaw = mobYawFor(e.ID, e.WanderTicks)
		}
		dx, _, dz := lookVector(e.Yaw, 0)
		nx := e.X + dx*mobWalkSpeed
		nz := e.Z + dz*mobWalkSpeed
		nextBelow := world.Pos{
			X: int(math.Floor(nx)),
			Y: int(math.Floor(e.Y)) - 1,
			Z: int(math.Floor(nz)),
		}
		nextSpace := world.Pos{
			X: int(math.Floor(nx)),
			Y: int(math.Floor(e.Y)),
			Z: int(math.Floor(nz)),
		}
		if w.GetBlock(nextBelow) == world.Air || w.GetBlock(nextSpace) != world.Air {
			// Ledge or obstacle: pick a new heading next step.
			e.Yaw += 90
			continue
		}
		e.X, e.Z = nx, nz
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, 0, true)
	}
}
