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
	"pig":              10,
	"cow":              10,
	"chicken":          4,
	"sheep":            8,
	"zombie":           20,
	"zombie_villager":  20,
	"villager":         20,
	"wandering_trader": 20,
	"skeleton":         20,
	"creeper":          20,
	"spider":           16,
	"sniffer":          14,
	"iron_golem":       100,
	"wolf":             8,
	"cat":              10,
	"ocelot":           10,
	"parrot":           6,
	"armadillo":        12,
	"cod":              3,
	"salmon":           3,
	"pufferfish":       3,
	"tropical_fish":    3,
	"pillager":         24,
	"ghast":            10,
	"ender_dragon":     200,
	"piglin":           16,
	// sulfur_cube health is 4×size; a bare /summon lands at size 1.
	"sulfur_cube": 4,
	"allay":       20,
	"goat":        10,
	"strider":     20,
	"armor_stand": 20,
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
	if e, ok := s.summonMobRaw(sess.dimension(), name, x, y, z, noAI); ok {
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
				// equipment.<slot> {id,count} fills the armor stand's
				// (and any mob's) shared equipment table.
				for slot, key := range map[int32]string{
					equipMainHand: "mainhand", equipOffHand: "offhand",
					equipBoots: "feet", equipLegs: "legs",
					equipChest: "chest", equipHelmet: "head",
				} {
					sd, ok := eq.vals[key]
					if !ok {
						continue
					}
					idv, ok := sd.vals["id"]
					if !ok || idv.str == "" {
						continue
					}
					iid, ok := world.ItemID(strings.TrimPrefix(idv.str, "minecraft:"))
					if !ok {
						continue
					}
					cnt := int32(1)
					if cv, ok := sd.vals["count"]; ok {
						if n, err := strconv.Atoi(strings.TrimRight(cv.num, "bBsSiIlLfFdD")); err == nil && n > 0 {
							cnt = int32(n)
						}
					}
					if e.Equips == nil {
						e.Equips = map[int32]ItemStack{}
					}
					e.Equips[slot] = ItemStack{ID: iid, Count: cnt}
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
			// VillagerData.profession + Offers.Recipes drive the
			// merchant menu; Age<0 marks a baby (no trading). The
			// registry-id trio also packs into entity metadata —
			// presence of VillagerData (or VillagerDataFinalized) sets
			// the finalized flag per VillagerDataHolder's NBT read.
			if vd, ok := tag.vals["VillagerData"]; ok {
				if pv, ok := vd.vals["profession"]; ok && pv.str != "" {
					e.Profession = strings.TrimPrefix(pv.str, "minecraft:")
					if id, ok2 := VillagerProfessionID(e.Profession); ok2 {
						e.VillagerProfID = id
					}
					e.VillagerFinalized = true
				}
				if tv, ok := vd.vals["type"]; ok && tv.str != "" {
					if id, ok2 := VillagerTypeID(strings.TrimPrefix(tv.str, "minecraft:")); ok2 {
						e.VillagerTypeID = id
					}
					e.VillagerFinalized = true
				}
				if lv, ok := vd.vals["level"]; ok {
					e.VillagerLevel = int32(snbtNum(lv))
					e.VillagerFinalized = true
				}
			}
			if fv, ok := tag.vals["VillagerDataFinalized"]; ok && (fv.num == "1b" || fv.num == "1" || fv.str == "true") {
				e.VillagerFinalized = true
			}
			if ov, ok := tag.vals["Offers"]; ok {
				if rc, ok := ov.vals["Recipes"]; ok {
					e.Offers = parseMerchantOffers(rc.items)
				}
			}
			if av, ok := tag.vals["Age"]; ok && snbtNum(av) < 0 {
				e.Baby = true
			}
			// active_effects loads the mob's live status effects —
			// {id, duration (ticks; -1 infinite), amplifier}; lazily
			// expiry-checked like session effects.
			if ae, ok := tag.vals["active_effects"]; ok {
				for _, inst := range ae.items {
					if inst == nil || inst.vals == nil {
						continue
					}
					iv, ok := inst.vals["id"]
					if !ok || iv.str == "" {
						continue
					}
					eid, ok := MobEffectID(strings.TrimPrefix(iv.str, "minecraft:"))
					if !ok {
						continue
					}
					dur := int64(-1)
					if dv, ok := inst.vals["duration"]; ok {
						dur = int64(snbtNum(dv))
					}
					var amp int32
					if av, ok := inst.vals["amplifier"]; ok {
						amp = int32(snbtNum(av))
					}
					expiry := int64(-1)
					if dur >= 0 {
						expiry = s.clock.count + dur
					}
					mobAddEffect(e, eid, amp, expiry)
				}
			}
			// ConversionTime != -1 starts the zombie-villager cure
			// countdown; ConversionPlayer is the EntityReference the
			// trigger fires for. Silent mutes the cure sound. Vanilla's
			// startConverting also strips weakness and grants strength
			// for the duration — its entity_event(16) broadcast fires
			// before the tracker attach so no client ever sees it.
			if cv, ok := tag.vals["ConversionTime"]; ok && int32(snbtNum(cv)) != -1 {
				e.Converting = true
				e.ConversionTime = int32(snbtNum(cv))
				if u, ok := snbtUUID(tag, "ConversionPlayer"); ok {
					e.ConversionStarter = u
					e.HasConvStarter = true
				}
				if w, ok2 := MobEffectID("weakness"); ok2 {
					mobRemoveEffect(e, w)
				}
				if st, ok2 := MobEffectID("strength"); ok2 {
					mobAddEffect(e, st, 0, s.clock.count+int64(e.ConversionTime))
				}
			}
			if sv, ok := tag.vals["Silent"]; ok && (sv.num == "1b" || sv.num == "1" || sv.str == "true") {
				e.Silent = true
			}
			// Armor stand NBT: Small halves the click-height bands,
			// ShowArms unlocks the hand slots, DisabledSlots is the
			// replace/take/place bitmask.
			if v, ok := tag.vals["Small"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
				e.Small = true
			}
			if v, ok := tag.vals["ShowArms"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
				e.ShowArms = true
			}
			if v, ok := tag.vals["DisabledSlots"]; ok {
				e.DisabledSlots = int32(snbtNum(v))
			}

		}
		s.broadcastFreshMob(e)
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
	// Wind charges fly straight (no gravity) and burst on contact.
	if name == "wind_charge" || name == "breeze_wind_charge" {
		mx, my, mz := 0.0, 0.0, 0.0
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				mx = snbtNum(mv.items[0])
				my = snbtNum(mv.items[1])
				mz = snbtNum(mv.items[2])
			}
		}
		e := s.entities.Spawn(&Entity{
			Type: mustEntityType(name),
			Dim:  sess.dimName(),
			X:    x, Y: y, Z: z,
			MotionX: mx, MotionY: my, MotionZ: mz,
			VX: int16(mx * 8000), VY: int16(my * 8000), VZ: int16(mz * 8000),
		})
		s.broadcastEntitySpawn(nil, *e)
		return componentReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
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
	s.broadcastFreshMob(e)
	return e, true
}

// summonMobRaw is summonMob without the broadcast: the caller applies
// summon NBT first so the tracker-attach burst carries the final
// metadata and equipment. summonMob stays for callers with no NBT.
func (s *Server) summonMobRaw(d *dimension, name string, x, y, z float64, noAI bool) (*Entity, bool) {
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
	// Villager carriers start at their defined default: a zombie
	// villager's DATA_VILLAGER_DATA is randomized at construction
	// (plains/random-profession/1) while a villager's is fixed
	// plains/none/1 — the difference in which value packs on the wire.
	if name == "zombie_villager" || name == "villager" {
		e.VillagerTypeID, _ = VillagerTypeID("plains")
		e.VillagerLevel = 1
		if name == "zombie_villager" {
			e.VillagerProfID = int32(rand.Intn(len(villagerProfessionIDs)))
		}
		e.VillagerProfDefault = e.VillagerProfID
	}
	return e, true
}

// broadcastFreshMob is vanilla's tracker-attach burst: spawn packet,
// entity metadata, and every occupied equipment slot. When equipment
// was loaded from summon NBT each slot is still dirty, so the next
// detectEquipmentUpdates tick resends the whole set — EquipDirty
// replays that second burst on the next mob tick.
func (s *Server) broadcastFreshMob(e *Entity) {
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeLivingMetadata(e, nil))
	if mobPairingAttrTypes[e.Type] {
		s.broadcastMobAttrs(e)
	}
	e.MetaDirty = true
	e.AttrDirty = true
	e.PosDirty = true
	e.SyncCount = 0
	e.ParticlesSent = nil
	if slots := armorStandEquipAll(e); len(slots) > 0 {
		e.EquipDirty = true
		body := encodeEquipment(e.ID, slots)
		s.sessions.Range(func(_, v any) bool {
			other := v.(*Session)
			if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
				return true
			}
			_ = other.write(other.ids().PlaySetEquipment, body)
			return true
		})
	}
}

// encodeArmorStandFlags packs ArmorStand's DATA_CLIENT_FLAGS byte
// (metadata index 15): 0x01 small, 0x04 show arms, 0x08 no baseplate,
// 0x10 marker. mc-go reads Small and ShowArms from summon NBT.
func encodeArmorStandFlags(e *Entity) byte {
	var flags byte
	if e.Small {
		flags |= 0x01
	}
	if e.ShowArms {
		flags |= 0x04
	}
	return flags
}

// encodeLivingMetadata builds the Set Entity Data body for a living
// entity's non-default entries, index-ascending like vanilla's
// SynchedEntityData.pack: health (9, float), effect particles (10 —
// only in tick resends, never in pairing — pass nil there), Mob's
// DATA_MOB_FLAGS (15, byte — bit0 = NoAI), then per-type entries:
// zombie_villager packs converting (19), villagerdata (20 — only when
// it differs from the per-entity random default), finalized (21);
// villager packs villagerdata (19) and finalized (20). The armor
// stand keeps its CLIENT_FLAGS at index 15 instead (it isn't a Mob).
func encodeLivingMetadata(e *Entity, particles []byte) []byte {
	var b protocol.Buffer
	b.Byte(9) // index: health
	b.Byte(3) // type: float
	b.Float(e.Health)
	if e.Type == mustEntityType("armor_stand") {
		b.Byte(15) // index: armor-stand client flags
		b.Byte(0)  // type: byte
		b.Byte(encodeArmorStandFlags(e))
		b.Byte(0xFF)
		return b.Bytes()
	}
	if len(particles) > 0 {
		b.Byte(10)
		b.Byte(17) // PARTICLES serializer
		b.Raw(particles)
	}
	if flags := mobFlagsByte(e); flags != 0 {
		b.Byte(15)
		b.Byte(0) // type: byte
		b.Byte(flags)
	}
	if name, ok := EntityTypeName(e.Type); ok {
		switch name {
		case "zombie_villager":
			if e.Converting {
				b.Byte(19)
				b.Byte(8) // type: bool
				b.Bool(true)
			}
			if !villagerDataIsDefault(e, e.VillagerProfDefault) {
				b.Byte(20)
				b.Byte(18) // type: villager_data
				encodeVillagerDataValue(&b, e)
			}
			if e.VillagerFinalized {
				b.Byte(21)
				b.Byte(8)
				b.Bool(true)
			}
		case "villager":
			// A villager's defined default is fixed plains/none/1;
			// a conversion always carries finalized data.
			if !villagerDataIsDefault(e, 0) {
				b.Byte(19)
				b.Byte(18)
				encodeVillagerDataValue(&b, e)
			}
			if e.VillagerFinalized {
				b.Byte(20)
				b.Byte(8)
				b.Bool(true)
			}
		}
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// mobFlagsByte is Mob's DATA_MOB_FLAGS byte: bit0 = NoAI.
func mobFlagsByte(e *Entity) byte {
	var flags byte
	if e.NoAI {
		flags |= 0x01
	}
	return flags
}

// villagerDataIsDefault reports whether the entity's villagerdata still
// equals its defined default — {plains, definedProf, 1} — so the entry
// stays unpacked like vanilla's non-default-only pack().
func villagerDataIsDefault(e *Entity, definedProf int32) bool {
	return e.VillagerTypeID == 2 && e.VillagerProfID == definedProf && e.VillagerLevel == 1
}

// encodeVillagerDataValue writes the villager_data serializer value:
// three VarInts — villager_type, villager_profession, level.
func encodeVillagerDataValue(b *protocol.Buffer, e *Entity) {
	b.VarInt(e.VillagerTypeID)
	b.VarInt(e.VillagerProfID)
	b.VarInt(e.VillagerLevel)
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
		// detectEquipmentUpdates: equipment loaded from NBT stays
		// dirty until the first tick after the tracker-attach burst.
		if e.EquipDirty {
			e.EquipDirty = false
			if slots := armorStandEquipAll(e); len(slots) > 0 {
				body := encodeEquipment(e.ID, slots)
				s.sessions.Range(func(_, v any) bool {
					other := v.(*Session)
					if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
						return true
					}
					_ = other.write(other.ids().PlaySetEquipment, body)
					return true
				})
			}
		}
		if e.invulnTicks > 0 {
			e.invulnTicks--
		}
		if e.loveTicks > 0 {
			e.loveTicks--
		}
		if !e.NoAI && e.Riding == 0 {
			// A passenger stays put — vanilla's entity rider has no
			// gravity or wander of its own. Hostile melee mobs chase
			// the nearest player and swing on a vanilla-paced cooldown;
			// a successful chase replaces the wander step.
			if dmg, ok := mobMelee[e.Type]; ok && s.mobMeleeStep(e, dmg) {
				s.mobSyncFlush(e)
				continue
			}
			w := s.dimWorld(e.Dim)
			if w != nil {
				s.mobMoveStep(e, w)
			}
		}
		s.mobSyncFlush(e)
	}
}

// mobMoveStep is the gravity + wander half of a mob tick: falling until
// a solid block is underfoot, then a deterministic wander step that
// refuses to walk off a ledge. It only mutates entity state — the wire
// emission is mobSyncFlush's sendChanges selection.
func (s *Server) mobMoveStep(e *Entity, w *world.World) {
	below := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y)) - 1,
		Z: int(math.Floor(e.Z)),
	}
	grounded := w.GetBlock(below) != world.Air
	if !grounded {
		e.Grounded = false
		e.MotionY -= 0.08
		if e.MotionY < -1.0 {
			e.MotionY = -1.0
		}
		e.Y += e.MotionY
		return
	}
	e.MotionY = 0
	e.Y = math.Floor(e.Y)
	e.Grounded = true
	e.WanderTicks++
	// Change heading every 40 steps; walk otherwise.
	if e.WanderTicks%40 == 1 {
		e.Yaw = mobYawFor(e.ID, e.WanderTicks)
		e.HeadYaw = e.Yaw
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
		e.HeadYaw = e.Yaw
		return
	}
	e.X, e.Z = nx, nz
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
	e.HeadYaw = e.Yaw
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
