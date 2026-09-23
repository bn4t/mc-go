package server

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// lootEntry is one item outcome inside a pool; a multi-entry pool
// rolls a single entry uniformly per roll.
type lootEntry struct {
	item     string
	min, max int32
	// smeltTo is the furnace_smelt result when the victim was burning
	// or the killer's mainhand carries a #smelts_loot enchantment
	// (fire_aspect).
	smeltTo string
	// looting flags the entry carrying enchanted_count_increase —
	// round(uniform[0,1] * level) is added to the rolled count.
	looting bool
}

// lootPool is one roll group of a mob's entity loot table.
type lootPool struct {
	rollsMin, rollsMax int32
	// playerKill gates pools carrying killed_by_player: the victim's
	// lastHurtByPlayer window must be live.
	playerKill bool
	// rare gates pools carrying random_chance_with_enchanted_bonus:
	// rareUnenchanted at looting 0, else rareBase + rarePer·(l-1).
	rare              bool
	rareUnenchanted   float64
	rareBase, rarePer float64
	entries           []lootEntry
}

// mobLoot transcribes the 26.2 datapack entity tables for the mobs
// mc-go can summon. Entries without enchanted_count_increase (the
// iron_golem) simply carry looting:false.
var mobLoot = map[string][]lootPool{
	"pig": {{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
		{item: "porkchop", min: 1, max: 3, smeltTo: "cooked_porkchop", looting: true}}}},
	"cow": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "leather", min: 0, max: 2, looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "beef", min: 1, max: 3, smeltTo: "cooked_beef", looting: true}}},
	},
	"chicken": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "feather", min: 0, max: 2, looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "chicken", min: 1, max: 1, smeltTo: "cooked_chicken", looting: true}}},
	},
	"sheep": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "mutton", min: 1, max: 2, smeltTo: "cooked_mutton", looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "white_wool", min: 1, max: 1}}},
	},
	"zombie": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "rotten_flesh", min: 0, max: 2, looting: true}}},
		{playerKill: true, rare: true, rareUnenchanted: 0.025, rareBase: 0.035, rarePer: 0.01,
			rollsMin: 1, rollsMax: 1, entries: []lootEntry{
				{item: "iron_ingot", min: 1, max: 1},
				{item: "carrot", min: 1, max: 1},
				{item: "potato", min: 1, max: 1, smeltTo: "baked_potato"}}},
	},
	"skeleton": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "arrow", min: 0, max: 2, looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "bone", min: 0, max: 2, looting: true}}},
	},
	"creeper": {{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
		{item: "gunpowder", min: 0, max: 2, looting: true}}}},
	"spider": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "string", min: 0, max: 2, looting: true}}},
		{playerKill: true, rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "spider_eye", min: -1, max: 1, looting: true}}},
	},
	"cave_spider": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "string", min: 0, max: 2, looting: true}}},
		{playerKill: true, rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "spider_eye", min: -1, max: 1, looting: true}}},
	},
	"drowned": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "rotten_flesh", min: 0, max: 2, looting: true}}},
		{playerKill: true, rare: true, rareUnenchanted: 0.11, rareBase: 0.13, rarePer: 0.02,
			rollsMin: 1, rollsMax: 1, entries: []lootEntry{
				{item: "copper_ingot", min: 1, max: 1}}},
	},
	"husk": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "rotten_flesh", min: 0, max: 2, looting: true}}},
		// rabbit_foot only drops while riding a camel_husk — a bare
		// summon never meets the vehicle predicate.
		{playerKill: true, rare: true, rareUnenchanted: 0.025, rareBase: 0.035, rarePer: 0.01,
			rollsMin: 1, rollsMax: 1, entries: []lootEntry{
				{item: "iron_ingot", min: 1, max: 1},
				{item: "carrot", min: 1, max: 1},
				{item: "potato", min: 1, max: 1, smeltTo: "baked_potato"}}},
	},
	"stray": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "arrow", min: 0, max: 2, looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "bone", min: 0, max: 2, looting: true}}},
		// tipped_arrow keeps its set_potion function — slowness.
		{playerKill: true, rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "tipped_arrow", min: 0, max: 1, looting: true}}},
	},
	"wither_skeleton": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "coal", min: -1, max: 1, looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "bone", min: 0, max: 2, looting: true}}},
		{playerKill: true, rare: true, rareUnenchanted: 0.025, rareBase: 0.035, rarePer: 0.01,
			rollsMin: 1, rollsMax: 1, entries: []lootEntry{
				{item: "wither_skeleton_skull", min: 1, max: 1}}},
	},
	"enderman": {{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
		{item: "ender_pearl", min: 0, max: 1, looting: true}}}},
	"blaze": {{playerKill: true, rollsMin: 1, rollsMax: 1, entries: []lootEntry{
		{item: "blaze_rod", min: 0, max: 1, looting: true}}}},
	"witch": {
		// rolls 1-3 over six entries, then a redstone pool.
		{rollsMin: 1, rollsMax: 3, entries: []lootEntry{
			{item: "glowstone_dust", min: 0, max: 2, looting: true},
			{item: "sugar", min: 0, max: 2, looting: true},
			{item: "spider_eye", min: 0, max: 2, looting: true},
			{item: "glass_bottle", min: 0, max: 2, looting: true},
			{item: "gunpowder", min: 0, max: 2, looting: true},
			{item: "stick", min: 0, max: 2, looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "redstone", min: 4, max: 8, looting: true}}},
	},
	"zombified_piglin": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "rotten_flesh", min: 0, max: 1, looting: true}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "gold_nugget", min: 0, max: 1, looting: true}}},
		{playerKill: true, rare: true, rareUnenchanted: 0.025, rareBase: 0.035, rarePer: 0.01,
			rollsMin: 1, rollsMax: 1, entries: []lootEntry{
				{item: "gold_ingot", min: 1, max: 1}}},
	},
	"iron_golem": {
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "poppy", min: 0, max: 2}}},
		{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "iron_ingot", min: 3, max: 5}}},
	},
}

// spawnMobLoot rolls the mob's drops at its position. Vanilla's
// gates: shouldDropLoot is !baby && mob_drops; a pool's
// killed_by_player reads the lastHurtByPlayer window; furnace_smelt
// fires on a burning victim or a #smelts_loot weapon;
// enchanted_count_increase adds round(uniform[0,1]·looting);
// random_chance_with_enchanted_bonus scales the rare pools.
func (s *Server) spawnMobLoot(e *Entity, killer *Session) {
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return
	}
	if e.Baby || !s.gameRuleBool("minecraft:mob_drops") {
		return
	}
	byPlayer := e.LastHurtByPlayerTicks > 0
	looting := int32(0)
	smelts := false
	if killer != nil {
		held := killer.heldStack()
		looting = held.enchLevel("looting")
		smelts = held.enchLevel("fire_aspect") > 0
	}
	spawnItem := func(item string, count int32) {
		id, ok := world.ItemID(item)
		if !ok || count <= 0 {
			return
		}
		it := s.entities.Spawn(&Entity{
			Type:        mustEntityType("item"),
			Dim:         e.Dim,
			X:           e.X,
			Y:           e.Y + 0.25,
			Z:           e.Z,
			Item:        ItemStack{ID: id, Count: count},
			PickupDelay: 10,
		})
		s.broadcastEntitySpawn(nil, *it)
		s.broadcastEntityMetadata(it.ID, encodeItemMetadata(it.Item))
	}
	for _, p := range mobLoot[name] {
		if p.playerKill && !byPlayer {
			continue
		}
		if p.rare {
			ch := p.rareUnenchanted
			if looting > 0 {
				ch = p.rareBase + float64(looting-1)*p.rarePer
			}
			if rand.Float64() >= ch {
				continue
			}
		}
		rolls := p.rollsMin
		if p.rollsMax > p.rollsMin {
			rolls += int32(rand.Intn(int(p.rollsMax-p.rollsMin) + 1))
		}
		for i := int32(0); i < rolls; i++ {
			ent := p.entries[rand.Intn(len(p.entries))]
			count := ent.min
			if ent.max > ent.min {
				count += int32(rand.Intn(int(ent.max-ent.min) + 1))
			}
			if ent.looting && looting > 0 {
				count += int32(rand.Intn(2)) * looting
			}
			item := ent.item
			if ent.smeltTo != "" && (e.FireTicks > 0 || smelts) {
				item = ent.smeltTo
			}
			spawnItem(item, count)
		}
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
	"horse":            25, // vanilla rolls 15-30 at spawn
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
	"sulfur_cube":      4,
	"allay":            20,
	"goat":             10,
	"strider":          20,
	"armor_stand":      20,
	"husk":             20,
	"drowned":          20,
	"stray":            20,
	"wither_skeleton":  20,
	"cave_spider":      12,
	"enderman":         40,
	"witch":            24,
	"blaze":            20,
	"phantom":          20,
	"zombified_piglin": 20,
	"turtle":           30,
	"guardian":         30,
	"elder_guardian":   80,
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
		// Selector volumes use these too — cover the rest of the
		// summonable hostile set and the common non-living types.
		"husk":             {0.6, 1.95},
		"drowned":          {0.6, 1.95},
		"stray":            {0.6, 1.99},
		"wither_skeleton":  {0.7, 2.4},
		"zombie_villager":  {0.6, 1.95},
		"zombified_piglin": {0.6, 1.95},
		"turtle":           {1.2, 0.4},
		"cave_spider":      {0.7, 0.5},
		"enderman":         {0.6, 2.9},
		"witch":            {0.6, 1.95},
		"wolf":             {0.6, 0.85},
		"cat":              {0.6, 0.7},
		"bee":              {0.7, 0.6},
		"slime":            {1.04, 1.04},
		"blaze":            {0.6, 1.8},
		"phantom":          {0.9, 0.5},
		"armor_stand":      {0.5, 1.975},
		"item":             {0.25, 0.25},
		"experience_orb":   {0.5, 0.5},
		"arrow":            {0.5, 0.5},
		"boat":             {1.375, 0.5625},
		"minecart":         {0.98, 0.7},
		"falling_block":    {0.98, 0.98},
		"tnt":              {0.98, 0.98},
		"snow_golem":       {0.7, 1.9},
		"guardian":         {0.85, 0.85},
		"elder_guardian":   {1.9975, 1.9975},
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
	if e, ok := s.summonMobRaw(sess.dimension(), name, x, y, z, noAI, tag == nil); ok {
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
					it := ItemStack{ID: iid, Count: cnt}
					// components.minecraft:enchantments rides as either
					// the bare level map or wrapped under "levels" —
					// accept both NBT spellings.
					if comps, ok := sd.vals["components"]; ok {
						if en, ok := comps.vals["minecraft:enchantments"]; ok {
							lvls := en.vals
							if lm, ok := en.vals["levels"]; ok {
								lvls = lm.vals
							}
							parts := make([]string, 0, len(lvls))
							for ename, lv := range lvls {
								if n, err := strconv.Atoi(strings.TrimRight(lv.num, "bBsSiIlLfFdD")); err == nil && n > 0 {
									parts = append(parts, strings.TrimPrefix(ename, "minecraft:")+":"+strconv.Itoa(n))
								}
							}
							if len(parts) > 0 {
								sort.Strings(parts)
								it.Ench = strings.Join(parts, ",")
							}
						}
					}
					e.Equips[slot] = it
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
			// CustomName stores the component string verbatim — vanilla
			// keeps the raw SNBT value and displays it literally.
			if cv, ok := tag.vals["CustomName"]; ok && cv.str != "" {
				e.CustomName = cv.str
			}
			// Health and Motion land like vanilla's Entity.load — the
			// spawn's dirty pack carries the moved entries after the
			// summon reply.
			if hv, ok := tag.vals["Health"]; ok {
				e.Health = float32(snbtNum(hv))
			}
			if mv, ok := tag.vals["Motion"]; ok && mv.kind == snbtList && len(mv.items) == 3 {
				e.MotionX = snbtNum(mv.items[0])
				e.MotionY = snbtNum(mv.items[1])
				e.MotionZ = snbtNum(mv.items[2])
				e.VX = int16(e.MotionX * 8000)
				e.VY = int16(e.MotionY * 8000)
				e.VZ = int16(e.MotionZ * 8000)
			}
			// Tags list — @e[tag=...] selection and the save map;
			// mobTagList reads either storage form.
			if tv, ok := tag.vals["Tags"]; ok && tv.kind == snbtList {
				var items []any
				for _, it := range tv.items {
					if it != nil && it.kind == snbtString {
						items = append(items, it.str)
					}
				}
				if len(items) > 0 {
					if e.ExtraNBT == nil {
						e.ExtraNBT = map[string]any{}
					}
					e.ExtraNBT["Tags"] = nbt.List{Elem: nbt.TagString, Items: items}
				}
			}
			if ov, ok := tag.vals["Offers"]; ok {
				if rc, ok := ov.vals["Recipes"]; ok {
					e.Offers = parseMerchantOffers(rc.items)
				}
			}
			if av, ok := tag.vals["Age"]; ok && snbtNum(av) < 0 {
				e.Baby = true
			}
			if bv, ok := tag.vals["IsBaby"]; ok && snbtNum(bv) > 0 {
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
		return componentReply(translatable("commands.summon.success", entityDisplayName(e)))
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
		return componentReply(translatable("commands.summon.success", entityDisplayName(e)))
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
	return s.summonMobInit(d, name, x, y, z, noAI, true)
}

func (s *Server) summonMobInit(d *dimension, name string, x, y, z float64, noAI, init bool) (*Entity, bool) {
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
	summonAttrTouches(e, name, init)
	s.broadcastFreshMob(e)
	return e, true
}

// summonMobRaw is summonMob without the broadcast: the caller applies
// summon NBT first so the tracker-attach burst carries the final
// metadata and equipment. summonMob stays for callers with no NBT.
func (s *Server) summonMobRaw(d *dimension, name string, x, y, z float64, noAI, init bool) (*Entity, bool) {
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
	summonAttrTouches(e, name, init)
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
	if e.CustomName != "" {
		b.Byte(2) // index: custom_name
		b.Byte(6) // type: optional component
		b.Byte(1) // present
		writeComponentTag(&b, e.CustomName)
	}
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

// writeComponentTag writes a component's serialized form inside the
// optional-component serializer: a TAG_String prefix, u16 length,
// then the raw component string.
func writeComponentTag(b *protocol.Buffer, s string) {
	b.Byte(nbt.TagString)
	b.Byte(byte(len(s) >> 8))
	b.Byte(byte(len(s)))
	b.Raw([]byte(s))
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

// encodeDyingLivingMetadata is the dirty pack a living entity's die()
// pushes: pose flips to DYING (index 6, pose serializer 20, value 7)
// and health lands on zero.
func encodeDyingLivingMetadata() []byte {
	var b protocol.Buffer
	b.Byte(6)
	b.Byte(20)
	b.VarInt(7)
	b.Byte(9)
	b.Byte(3)
	b.Float(0)
	b.Byte(0xFF)
	return b.Bytes()
}

// damageMob applies damage to a living mob and syncs metadata. It
// reports the damage actually dealt — zero when the hit was swallowed
// by the post-hit invulnerability window — whether the hit ran inside
// that window (vanilla then skips the damage event and knockback), and
// whether the hit killed the mob.
// mobFireImmune mirrors vanilla's per-type fireImmune flag (EntityType
// registration): the nether natives and bosses never ignite.
var mobFireImmune = map[string]bool{
	"blaze": true, "ghast": true, "magma_cube": true, "strider": true,
	"wither": true, "wither_skeleton": true, "zombified_piglin": true,
	"hoglin": true, "zoglin": true,
}

// igniteMob sets a living mob alight for the given ticks (fire_aspect's
// post_attack ignite): the shared-flags byte picks up the on-fire bit
// and the burn countdown starts dealing a point at every multiple of
// twenty.
func (s *Server) igniteMob(e *Entity, ticks int32) {
	if name, ok := EntityTypeName(e.Type); ok && mobFireImmune[name] {
		return
	}
	s.entities.SetFireTicks(e.ID, ticks)
	s.broadcastMobSharedFlags(e.ID, true)
}

// broadcastMobSharedFlags sends the index-0 entity-data byte — the
// bit set vanilla's setSharedFlagOnFire flips — to every session
// tracking the mob's dimension.
func (s *Server) broadcastMobSharedFlags(eid int32, onFire bool) {
	flags := byte(0)
	if onFire {
		flags |= 0x01
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(0) // index: shared flags
	b.Byte(0) // type: byte
	b.Byte(flags)
	b.Byte(0xFF)
	s.broadcastEntityMetadata(eid, b.Bytes())
}

// damageMob runs vanilla's LivingEntity.hurt tail for a mob hit:
// armor (natural base + worn pieces) absorbs unless the damage type
// bypasses it, then the health loss lands inside the grace rules.
// info carries the damage_event broadcast — vanilla emits it inside
// hurtServer's accepted branch, AFTER the absorb/wear side effects
// and BEFORE hurt()'s playHurtSound.
func (s *Server) damageMob(e *Entity, amount float32, typeName string, killer *Session, info *damageInfo) (dealt float32, graced, died bool) {
	var breach float32
	if info != nil {
		breach = s.weaponBreach(info.direct)
	}
	res := s.entities.Damage(e.ID, amount, typeName, breach)
	s.mobHurtWireFX(e, res)
	// A graced hit whose excess is zero returns false in vanilla's
	// hurt — none of the hurt tail (event, sound, retaliation) runs.
	// The wolf-armor absorb path DOES count as a landed hit.
	if !res.Exists || (res.Dealt <= 0 && !res.Absorbed) {
		return 0, res.Graced, false
	}
	// The graced-excess branch (hurt returned inside invulnerableTime)
	// sets b8=false in vanilla: no damage_event, no hurt/death sound —
	// only the health meta moves, and die() still runs if it killed.
	if info != nil && !res.Graced {
		s.broadcastDamageEvent(e.ID, *info)
	}
	health, d := res.Health, res.Dealt
	if health > 0 && !res.Graced {
		s.mobHurtSound(e, res)
	}
	// Vanilla's killed_by_player window: a player-caused hurt keeps
	// the flag hot for 100 ticks (lastHurtByPlayerMemoryTime).
	if killer != nil {
		e.LastHurtByPlayerTicks = 100
		// doPostAttackEffects, enchanted=victim: thorns on the mob's
		// equipment retaliates against the attacker (runs even when
		// this hit is lethal).
		s.thornsMobWear(e, killer)
	}
	if health <= 0 {
		s.mobDie(e, killer, res.Graced)
		return d, res.Graced, true
	}
	if d > 0 {
		s.broadcastEntityMetadata(e.ID, encodeLivingHealth(health))
	}
	return d, res.Graced, false
}

// mobDie runs the vanilla die() tail for a mob at zero health: pose
// DYING + health 0 on the tracker, the corpse event, loot/XP, then a
// twenty-tick corpse linger before removal — all inside mobTickStep's
// per-mob pass. It fires from damageMob and from a /data merge that
// pushes Health to zero.
// silent is hurt()'s b8=false arm: a graced hit that kills runs die()
// without makeSound(getDeathSound)/playSecondaryHurtSound.
func (s *Server) mobDie(e *Entity, killer *Session, silent bool) {
	if !silent {
		s.mobDeathSound(e)
	}
	s.entities.SetDying(e.ID)
	// The death pack rides the next tick's sendChanges like any dirty
	// metadata — a /kill reply reaches the client first.
	e.dataSync = encodeDyingLivingMetadata()
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 3)
	s.spawnMobLoot(e, killer)
	// dropExperience: recent player hurt + not a baby + mob_drops.
	if e.LastHurtByPlayerTicks > 0 && !e.Baby &&
		s.gameRuleBool("minecraft:mob_drops") {
		if name, ok := EntityTypeName(e.Type); ok {
			s.spawnMobXP(e.Dim, name, e.X, e.Y, e.Z)
		}
	}
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
		// Entities outside a non-spectator's simulation-distance chunk
		// window are never ticked on vanilla — gravity, fire, the
		// invulnerability countdown and the death linger all pause.
		if !s.mobInSimulationRange(e) {
			continue
		}
		if e.Dying {
			if len(e.dataSync) > 0 {
				data := e.dataSync
				e.dataSync = nil
				s.broadcastEntityMetadata(e.ID, data)
			}
			if s.entities.TickDeath(e.ID) {
				s.broadcastEntityEventTo(nil, e.Dim, e.ID, 60)
				s.entities.Remove(e.ID)
				s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			}
			continue
		}
		if e.LastHurtByPlayerTicks > 0 {
			e.LastHurtByPlayerTicks--
		}
		// detectEquipmentUpdates: a loaded-from-NBT set replays whole
		// (EquipDirty) once after the tracker-attach burst; after that
		// only the slots marked by a write or wear go out.
		if e.EquipDirty {
			e.EquipDirty = false
			e.EquipDelta = nil
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
		} else if len(e.EquipDelta) > 0 {
			wires := make([]byte, 0, len(e.EquipDelta))
			for slot := range e.EquipDelta {
				wires = append(wires, slot)
			}
			sort.Slice(wires, func(i, j int) bool { return wires[i] < wires[j] })
			e.EquipDelta = nil
			var slots []struct {
				slot  byte
				stack ItemStack
			}
			for _, w := range wires {
				slots = append(slots, struct {
					slot  byte
					stack ItemStack
				}{w, mobSlotStack(e, w)})
			}
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
		// LivingEntity.travel reads movement_speed every living tick —
		// the lazy AttributeMap instantiates it on that first read, so
		// the attribute joins the save pack once the mob has ticked.
		e.markAttrTouched("movement_speed")
		// Entity.baseTick's fire pass: while the countdown runs, the
		// multiples of twenty each deal a point of on_fire damage and
		// the counter decrements; reaching zero relights the shared
		// flags byte without the on-fire bit.
		if e.FireTicks > 0 {
			if e.FireTicks%20 == 0 {
				s.damageMob(e, 1, "on_fire", nil, &damageInfo{typeName: "on_fire"})
			}
			ticks := e.FireTicks - 1
			s.entities.SetFireTicks(e.ID, ticks)
			if ticks <= 0 {
				s.broadcastMobSharedFlags(e.ID, false)
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

// mobInSimulationRange reports whether the mob's chunk sits inside
// any in-play, non-spectator player's simulation distance — the
// Chebyshev chunk window that carries entity-ticking tickets.
func (s *Server) mobInSimulationRange(e *Entity) bool {
	limit := s.SimulationDistance
	if limit == 0 {
		limit = 10
	}
	// Vanilla's entity-ticking ticket lands on the chunk at Chebyshev
	// distance <= simulationDistance; measured: chunk 8 ticks at
	// sim=8, chunk 9 never does.
	inRange, sawEligible := false, false
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || p.mode() == GameModeSpectator {
			return true
		}
		sawEligible = true
		if p.dimName() != e.Dim {
			return true
		}
		px, _, pz, _, _, _ := p.Pos()
		dx := math.Abs(math.Floor(e.X/16) - math.Floor(px/16))
		dz := math.Abs(math.Floor(e.Z/16) - math.Floor(pz/16))
		if int(math.Max(dx, dz)) <= limit {
			inRange = true
			return false
		}
		return true
	})
	if !sawEligible {
		// No live players (unit-test worlds, pre-login): nothing gates.
		return true
	}
	return inRange
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
		if !sess.spawned.Load() || sess.dead ||
			sess.mode() == GameModeCreative || sess.mode() == GameModeSpectator {
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

// mobHurtWireFX emits the absorb-side wire feedback of a mob hit —
// the body-equipment break event (vanilla's onEquippedItemBroken runs
// inside hurtAndBreak) and the wolf-armor crack sound + scute
// particle burst on a crackiness transition. These precede the
// damage_event broadcast on the wire.
func (s *Server) mobHurtWireFX(e *Entity, res DamageResult) {
	if res.BodyBroke {
		// onEquippedItemBroken: entity event 65 = equipment break, body.
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 65)
	}
	if res.CrackChanged {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, sndWolfArmorCrack, srcNeutral, 1, 1, nil)
		s.broadcastItemParticles(e.Dim, "armadillo_scute",
			e.X, e.Y+1, e.Z, 0.2, 0.1, 0.2, 0.1, 20)
	}
}

// mobVoice is one summonable type's hurt/death voice — getHurtSound /
// getDeathSound registry ids, getSoundVolume, and the SoundSource
// (HOSTILE for Enemy types, NEUTRAL otherwise). secondDeath is the
// playSecondaryHurtSound extra vanilla emits on death (the iron
// golem's damage groan). A zero hurt id means the type is silent on
// hurt (armor stands). All values measured live on vanilla 26.2;
// ambient voices remain open.
type mobVoice struct {
	hurt, death int32
	// Guardians and elder guardians split all voices on isInWater —
	// hurtLand/deathLand are the dry variants, zero when the type
	// has none.
	hurtLand, deathLand int32
	secondDeath         int32
	vol                 float32
	hostile             bool
}

var mobVoices = map[string]mobVoice{
	"pig":              {hurt: 1269, death: 1270},
	"cow":              {hurt: 451, death: 452, vol: 0.4},
	"chicken":          {hurt: 357, death: 358},
	"sheep":            {hurt: 1440, death: 1439},
	"zombie":           {hurt: 1881, death: 1874, hostile: true},
	"zombie_villager":  {hurt: 1904, death: 1903, hostile: true},
	"villager":         {hurt: 1698, death: 1697},
	"wandering_trader": {hurt: 1730, death: 1726},
	"skeleton":         {hurt: 1490, death: 1481, hostile: true},
	"creeper":          {hurt: 480, death: 479, hostile: true},
	"spider":           {hurt: 1580, death: 1579, hostile: true},
	"sniffer":          {hurt: 1555, death: 1556},
	"iron_golem":       {hurt: 900, death: 899, secondDeath: 898},
	"horse":            {hurt: 862, death: 857, vol: 0.8},
	"wolf":             {hurt: 1806, death: 1804, vol: 0.4},
	"cat":              {hurt: 291, death: 292},
	"ocelot":           {hurt: 1187, death: 1189},
	"parrot":           {hurt: 1213, death: 1210},
	"armadillo":        {hurt: 55, death: 59},
	"cod":              {hurt: 382, death: 380},
	"salmon":           {hurt: 1395, death: 1393},
	"pufferfish":       {hurt: 1346, death: 1344},
	"tropical_fish":    {hurt: 1640, death: 1638},
	"pillager":         {hurt: 1299, death: 1298, hostile: true},
	"ghast":            {hurt: 707, death: 706, vol: 5, hostile: true},
	"piglin":           {hurt: 1286, death: 1284, hostile: true},
	"sulfur_cube":      {hurt: 1964, death: 1963, vol: 0.4},
	"allay":            {hurt: 3, death: 2, vol: 0.4},
	"goat":             {hurt: 738, death: 736},
	"strider":          {hurt: 1541, death: 1540},
	"husk":             {hurt: 880, death: 879, hostile: true},
	"drowned":          {hurt: 566, death: 564, hostile: true},
	"stray":            {hurt: 1607, death: 1606, hostile: true},
	"wither_skeleton":  {hurt: 1787, death: 1786, hostile: true},
	"cave_spider":      {hurt: 1580, death: 1579, hostile: true},
	"enderman":         {hurt: 596, death: 595, hostile: true},
	"witch":            {hurt: 1778, death: 1776, hostile: true},
	"blaze":            {hurt: 177, death: 176, hostile: true},
	"phantom":          {hurt: 1259, death: 1257, hostile: true},
	"zombified_piglin": {hurt: 1898, death: 1897, hostile: true},
	"turtle":           {hurt: 1662, death: 1657},
	"guardian":         {hurt: 773, death: 770, hurtLand: 774, deathLand: 771, hostile: true},
	"elder_guardian":   {hurt: 579, death: 576, hurtLand: 580, deathLand: 577, hostile: true},
	// The ender dragon's /damage and /kill paths are silent — its
	// hurt voice never reaches playHurtSound (part-based damage) and
	// its death is a custom despawn sequence.
	"ender_dragon": {},
}

// mobVoiceSrc is the packet source: HOSTILE (5) for Enemy types,
// NEUTRAL (6) for everything else.
func (v mobVoice) src() int32 {
	if v.hostile {
		return 5
	}
	return srcNeutral
}

// mobVoiceOf resolves a mob's hurt/death voice with a default volume.
func mobVoiceOf(e *Entity) mobVoice {
	name, _ := EntityTypeName(e.Type)
	v := mobVoices[name]
	if v.vol == 0 {
		v.vol = 1
	}
	return v
}

// mobHurtSound is LivingEntity.playHurtSound — hurt() runs it after
// hurtServer accepts, so a wolf-armor break is already visible to
// getHurtSound: an absorb hit with the armor still on plays
// item.wolf_armor.damage, the breaking hit reads an empty body slot
// and plays entity.wolf.hurt. Vanilla sends the sound as a positional
// packet at the entity's feet.
func (s *Server) mobHurtSound(e *Entity, res DamageResult) {
	v := mobVoiceOf(e)
	snd := v.hurt
	if snd == 0 {
		return
	}
	if v.hurtLand != 0 && !s.mobInWater(e) {
		snd = v.hurtLand
	}
	if mobName, _ := EntityTypeName(e.Type); mobName == "wolf" &&
		res.Absorbed && !res.BodyBroke {
		snd = sndWolfArmorDamage
	}
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, v.src(), v.vol, voicePitch(), nil)
}

// mobDeathSound is the dying arm of hurt(): makeSound(getDeathSound)
// then playSecondaryHurtSound, in place of the hurt sound.
func (s *Server) mobDeathSound(e *Entity) {
	v := mobVoiceOf(e)
	death := v.death
	if v.deathLand != 0 && !s.mobInWater(e) {
		death = v.deathLand
	}
	if death != 0 {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, death, v.src(), v.vol, voicePitch(), nil)
	}
	if v.secondDeath != 0 {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, v.secondDeath, v.src(), v.vol, voicePitch(), nil)
	}
}

// mobInWater is Entity.isInWater approximated at the feet block —
// the mob's fluid contact is water when the block under its origin
// is a water column.
func (s *Server) mobInWater(e *Entity) bool {
	d := s.dims[e.Dim]
	if d == nil {
		return false
	}
	k, _, ok := fluidOf(d.w.GetBlock(world.Pos{
		X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z)),
	}))
	return ok && k == fluidWater
}

// voicePitch is LivingEntity.getVoicePitch: (rng - rng) * 0.2 + 1.
func voicePitch() float32 {
	return (rand.Float32()-rand.Float32())*0.2 + 1
}
