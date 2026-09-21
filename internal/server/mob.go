package server

import (
	"math"
	"math/rand"
	"strconv"
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
	"pig":           10,
	"cow":           10,
	"chicken":       4,
	"sheep":         8,
	"zombie":        20,
	"villager":      20,
	"skeleton":      20,
	"creeper":       20,
	"spider":        16,
	"sniffer":       14,
	"iron_golem":    100,
	"wolf":          8,
	"cat":           10,
	"ocelot":        10,
	"parrot":        6,
	"armadillo":     12,
	"cod":           3,
	"salmon":        3,
	"pufferfish":    3,
	"tropical_fish": 3,
	"pillager":      24,
	"ghast":         10,
	"ender_dragon":  200,
	"piglin":        16,
	// sulfur_cube health is 4×size; a bare /summon lands at size 1.
	"sulfur_cube": 4,
	"allay":       20,
	"goat":        10,
	"strider":     20,
}

// mobBoundsTable is the vanilla bounding box (width, height) per
// summonable mob — the box sweep attacks inflate and damage-indicator
// particles ride at half height on.
var mobBoundsTable = map[int32][2]float64{}

func init() {
	for name, dims := range map[string][2]float64{
		"pig":          {0.9, 0.9},
		"cow":          {0.9, 1.4},
		"chicken":      {0.4, 0.7},
		"sheep":        {0.9, 1.3},
		"zombie":       {0.6, 1.95},
		"skeleton":     {0.6, 1.99},
		"creeper":      {0.6, 1.7},
		"spider":       {1.4, 0.9},
		"sniffer":      {1.9, 1.75},
		"iron_golem":   {1.4, 2.7},
		"pillager":     {0.6, 1.95},
		"ghast":        {4.0, 4.0},
		"parrot":       {0.5, 0.9},
		"ender_dragon": {16.0, 8.0},
		"villager":     {0.6, 1.95},
		"piglin":       {0.6, 1.95},
		"sulfur_cube":  {1.04, 1.04},
		"allay":        {0.35, 0.6},
		"goat":         {0.9, 1.3},
		"strider":      {0.9, 1.7},
	} {
		if id, ok := EntityTypeID(name); ok {
			mobBoundsTable[id] = dims
		}
	}
}

// mobBounds returns the (width, height) bounding box for an entity
// type, defaulting to the player's box.
func mobBounds(typeID int32) [2]float64 {
	if dims, ok := mobBoundsTable[typeID]; ok {
		return dims
	}
	return [2]float64{0.6, 1.8}
}

// runSummon implements /summon <entity>: the mob spawns at the
// player's feet. Success replies with vanilla's translatable
// commands.summon.success, carrying the entity type's display name.
func (s *Server) runSummon(player string, args []string) reply {
	if len(args) == 0 {
		return textReply("Usage: /summon <entity>")
	}
	sess := s.findSessionByName(player)
	if sess == nil {
		return textReply("Unknown player: " + player)
	}
	x, y, z, _, _, _ := sess.Pos()
	if len(args) >= 4 {
		var err error
		if x, err = parseDouble(args[1], x, true); err != nil {
			return textReply("Invalid position " + args[1])
		}
		if y, err = parseDouble(args[2], y, false); err != nil {
			return textReply("Invalid position " + args[2])
		}
		if z, err = parseDouble(args[3], z, true); err != nil {
			return textReply("Invalid position " + args[3])
		}
	}
	noAI := false
	var tag *snbtTag
	if len(args) >= 5 {
		if tg, _, err := parseSNBTTag(args[4], 0); err == nil && tg != nil {
			tag = tg
			if v, ok := tg.vals["NoAI"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
				noAI = true
			}
		}
	}
	name := strings.TrimPrefix(args[0], "minecraft:")
	if e, ok := s.summonMob(sess.dimension(), name, x, y, z, noAI); ok {
		if tag != nil {
			if v, ok := tag.vals["CanPickUpLoot"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
				e.CanPickUpLoot = true
			}
			// UUID:[I;a,b,c,d] — honored like vanilla's summon NBT so
			// tests can mint a known entity id for references
			// (e.g. an item entity's Thrower).
			if u, ok := snbtUUID(tag, "UUID"); ok {
				e.UUID = u
			}
			// Saddleable mounts take the saddle either by the legacy
			// Saddle flag or by the 1.21.6+ equipment.saddle slot.
			if v, ok := tag.vals["Saddle"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
				e.Saddle = true
			}
			if eq, ok := tag.vals["equipment"]; ok {
				if sd, ok := eq.vals["saddle"]; ok && sd.vals["id"].str != "" {
					e.Saddle = true
				}
				if bd, ok := eq.vals["body"]; ok {
					if idv, ok := bd.vals["id"]; ok && idv.str != "" {
						if iid, ok := world.ItemID(strings.TrimPrefix(idv.str, "minecraft:")); ok {
							cnt := int32(1)
							if cv, ok := bd.vals["count"]; ok {
								if n, err := strconv.Atoi(strings.TrimRight(cv.num, "bBsSiIlLfFdD")); err == nil && n > 0 {
									cnt = int32(n)
								}
							}
							e.BodyItem = ItemStack{ID: iid, Count: cnt}
						}
					}
				}
			}
			// Owner on a tameable marks it tamed by that player —
			// TamableAnimal reads the same EntityReference, and
			// Wolf.canShearEquipment checks ownership through it.
			if u, ok := snbtUUID(tag, "Owner"); ok {
				if ownerSess := s.sessionByUUID(u); ownerSess != nil {
					e.tamed = true
					e.owner = ownerSess.getName()
				}
			}
		}
		return componentReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// A dropped stack on the ground: Item holds the stack, Thrower
	// the EntityReference getOwner resolves, PickupDelay the no-grab
	// window.
	if name == "item" {
		it := ItemStack{Count: 1}
		if tag != nil {
			if iv, ok := tag.vals["Item"]; ok {
				if idv, ok := iv.vals["id"]; ok && idv.str != "" {
					if iid, ok := world.ItemID(strings.TrimPrefix(idv.str, "minecraft:")); ok {
						it.ID = iid
					}
				}
				if cv, ok := iv.vals["count"]; ok {
					if n := int32(snbtNum(cv)); n > 0 {
						it.Count = n
					}
				}
			}
		}
		if it.ID == 0 {
			return textReply("Can't spawn empty item")
		}
		e := &Entity{
			Type: mustEntityType("item"),
			Dim:  sess.dimName(),
			X:    x, Y: y, Z: z,
			Item: it,
		}
		if tag != nil {
			if pv, ok := tag.vals["PickupDelay"]; ok {
				e.PickupDelay = int(snbtNum(pv))
			}
			if u, ok := snbtUUID(tag, "Thrower"); ok {
				e.Thrower = s.entityIDByUUID(u)
			}
		}
		s.entities.Spawn(e)
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(it))
		return componentReply(translatable("commands.summon.success", translatable("entity.minecraft.item")))
	}
	// Boats/rafts are vehicles, not living mobs: Passengers NBT
	// recursively spawns riders the way vanilla's entity load does.
	if isBoatEntityName(name) {
		id, ok := EntityTypeID(name)
		if !ok {
			return textReply("Unknown entity: " + args[0])
		}
		e := s.entities.Spawn(&Entity{
			Type: id,
			Dim:  sess.dimName(),
			X:    x, Y: y, Z: z,
		})
		if tag != nil {
			if pv, ok := tag.vals["Passengers"]; ok {
				for _, sub := range pv.items {
					pname := strings.TrimPrefix(sub.vals["id"].str, "minecraft:")
					if pname == "" {
						continue
					}
					if g, ok := s.summonMob(sess.dimension(), pname, x, y, z, true); ok {
						g.Riding = e.ID
						e.Passengers = append(e.Passengers, g.ID)
					}
				}
			}
		}
		s.broadcastEntitySpawn(nil, *e)
		return componentReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// Non-living summons the command still accepts: projectiles take
	// their Motion list and fly like a bow release.
	if name == "arrow" {
		mx, my, mz := 0.0, -0.1, 0.0
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				mx = snbtNum(mv.items[0])
				my = snbtNum(mv.items[1])
				mz = snbtNum(mv.items[2])
			}
		}
		e := &Entity{
			Type: mustEntityType("arrow"),
			Dim:  sess.dimName(),
			X:    x, Y: y, Z: z,
			MotionX: mx, MotionY: my, MotionZ: mz,
			VX: int16(mx * 8000), VY: int16(my * 8000), VZ: int16(mz * 8000),
		}
		// Owner:[I;a,b,c,d] stores an EntityReference to the shooter —
		// its kills credit that player (advancements, death messages).
		if tag != nil {
			if u, ok := snbtUUID(tag, "Owner"); ok {
				if owner := s.sessionByUUID(u); owner != nil {
					e.arrowShooter = owner.entityID.Load()
				}
			}
		}
		s.entities.Spawn(e)
		s.broadcastEntitySpawn(nil, *e)
		return componentReply(translatable("commands.summon.success", translatable("entity.minecraft.arrow")))
	}
	if name == "lightning_bolt" {
		e := s.entities.Spawn(&Entity{
			Type: mustEntityType("lightning_bolt"),
			Dim:  sess.dimName(),
			X:    x, Y: y, Z: z,
		})
		s.broadcastEntitySpawn(nil, *e)
		// A bolt that lands on a lightning rod sets no fires — vanilla's
		// type_specific/lightning.blocks_set_on_fire stays zero and the
		// Surge Protector predicate matches.
		fires := 1
		if n, ok := blockNameAt(sess.dimension(), world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)) - 1, Z: int(math.Floor(z))}); ok && strings.HasSuffix(n, "lightning_rod") {
			fires = 0
		}
		if n, ok := blockNameAt(sess.dimension(), world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}); ok && strings.HasSuffix(n, "lightning_rod") {
			fires = 0
		}
		s.fireLightningStrike(sess.dimension(), x, y, z, fires)
		s.entities.Remove(e.ID)
		s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
		return componentReply(translatable("commands.summon.success", translatable("entity.minecraft.lightning_bolt")))
	}
	return textReply("Unknown entity: " + args[0])
}

// snbtUUID reads a [I;a,b,c,d] int array tag into a 16-byte UUID.
func snbtUUID(t *snbtTag, key string) ([16]byte, bool) {
	v, ok := t.vals[key]
	if !ok || len(v.items) != 4 {
		return [16]byte{}, false
	}
	var u [16]byte
	for i := 0; i < 4; i++ {
		n := int32(snbtNum(v.items[i]))
		u[i*4] = byte(n >> 24)
		u[i*4+1] = byte(n >> 16)
		u[i*4+2] = byte(n >> 8)
		u[i*4+3] = byte(n)
	}
	return u, true
}

// entityIDByUUID resolves a UUID to a live entity id — a spawned
// entity, or a player session's entity. Zero when unresolvable.
func (s *Server) entityIDByUUID(u [16]byte) int32 {
	s.entities.mu.RLock()
	for id, e := range s.entities.byID {
		if e.UUID == u {
			s.entities.mu.RUnlock()
			return id
		}
	}
	s.entities.mu.RUnlock()
	if sess := s.sessionByUUID(u); sess != nil {
		return sess.entityID.Load()
	}
	return 0
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

// damageMob applies damage to a living mob and syncs metadata. It
// reports the damage actually dealt — zero when the hit was swallowed
// by the post-hit invulnerability window — whether the hit ran inside
// that window (vanilla then skips the damage event and knockback), and
// whether the hit killed the mob.
func (s *Server) damageMob(e *Entity, amount float32) (dealt float32, graced, died bool) {
	health, d, ok, gr := s.entities.Damage(e.ID, amount)
	if !ok || d <= 0 {
		return 0, gr, false
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
		return d, gr, true
	}
	s.broadcastEntityMetadata(e.ID, encodeLivingHealth(health))
	return d, gr, false
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
		if e.loveTicks > 0 {
			e.loveTicks--
		}
		if e.NoAI || e.Riding != 0 {
			// A passenger stays put — vanilla's entity rider has no
			// gravity or wander of its own.
			continue
		}
		// Hostile melee mobs chase the nearest player and swing on a
		// vanilla-paced cooldown; a successful chase replaces the wander
		// step for this tick.
		if dmg, ok := mobMelee[e.Type]; ok && s.mobMeleeStep(e, dmg) {
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

// mobMelee is the melee attack damage (vanilla attackDamage) for the
// hostile mobs mc-go simulates. Follow range is vanilla's
// followRange (35 for zombies) — skeletons shoot rather than melee and
// creepers explode, so neither is in the table.
var mobMelee = map[int32]float32{}

func init() {
	for name, dmg := range map[string]float32{
		"zombie":           3,
		"husk":             3,
		"drowned":          3,
		"zombie_villager":  3,
		"zombified_piglin": 8,
		"spider":           3,
		"cave_spider":      3,
		"iron_golem":       15,
	} {
		if id, ok := EntityTypeID(name); ok {
			mobMelee[id] = dmg
		}
	}
}

// mobChaseSpeed is the vanilla zombie-family movement speed in
// blocks per tick (movementSpeed attribute 0.23).
const mobChaseSpeed = 0.23

// mobMeleeStep runs one hostile-melee tick: chase the nearest living
// survival player inside follow range; within reach, swing (the animate
// broadcast) and land attackDamage on the vanilla 20-tick cooldown.
// Reports whether the mob had a target — true replaces wander.
func (s *Server) mobMeleeStep(e *Entity, dmg float32) bool {
	var target *Session
	best := 35.0 * 35.0
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.spawned.Load() || sess.dead || sess.mode() != GameModeSurvival {
			return true
		}
		if sess.dimension().name != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		if d2 := dx*dx + dz*dz + dy*dy*0.25; d2 < best {
			best = d2
			target = sess
		}
		return true
	})
	if target == nil {
		return false
	}
	px, py, pz, _, _, _ := target.Pos()
	dx, dz := px-e.X, pz-e.Z
	dist := math.Hypot(dx, dz)
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	// Vanilla's attack reach is the mob's inflated box vs the target's;
	// for the zombie's 0.6 box vs a player that lands ~1.5 blocks out.
	reach := mobBounds(e.Type)[0] + 0.6 + 0.3
	if dist > reach {
		step := mobChaseSpeed
		if step > dist-reach*0.8 {
			step = dist - reach*0.8
		}
		if step > 0 {
			e.X += dx / dist * step
			e.Z += dz / dist * step
		}
	}
	s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, 0, true)
	if e.attackCooldown > 0 {
		e.attackCooldown--
		return true
	}
	dy := math.Abs(py - e.Y)
	if dist <= reach && dy < 2.5 {
		e.attackCooldown = 20
		s.broadcastEntityAnimation(nil, e.ID, 0) // swing main hand
		kn, _ := EntityTypeName(e.Type)
		s.damageWith(target, dmg, translatable("death.attack.mob",
			literal(target.getName()), literal(mobDisplayName(kn))), damageSource{
			typeName: "mob_attack",
			cause:    e.ID,
			direct:   e.ID,
		})
	}
	return true
}

// mobDisplayName is the entity type's death-message name — vanilla
// renders the translatable entity name; mc-go's death strings carry the
// English form.
func mobDisplayName(typ string) string {
	if typ == "" {
		return "something"
	}
	parts := strings.Split(typ, "_")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}
