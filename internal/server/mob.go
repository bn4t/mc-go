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
	// bottleAmp is set_ominous_bottle_amplifier — the drop rolls a
	// uniform 0-4 amplifier into an ominous_bottle stack.
	bottleAmp bool
}

// lootPool is one roll group of a mob's entity loot table.
type lootPool struct {
	rollsMin, rollsMax int32
	// playerKill gates pools carrying killed_by_player: the victim's
	// lastHurtByPlayer window must be live.
	playerKill bool
	// captain gates the type_specific/raider {is_captain:true} pools —
	// vanilla's Raider.isCaptain reads the head slot matching the
	// ominous banner, so the check here is Equips[head].Ominous.
	captain bool
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
	// pillager carries only the captain pool — no killed_by_player
	// gate, one bottle, amplifier uniform 0-4.
	"pillager": {
		{captain: true, rollsMin: 1, rollsMax: 1, entries: []lootEntry{
			{item: "ominous_bottle", min: 1, max: 1, bottleAmp: true}}},
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
	byPlayer := e.LastHurtByPlayerTicks > 0
	// dropFromLootTable is gated by shouldDropLoot (not-baby) and the
	// mob_drops gamerule; the equipment pass below is not.
	if !e.Baby && s.gameRuleBool("minecraft:mob_drops") {
		s.spawnMobLootPools(e, killer, name, byPlayer)
	}
	// dropEquipment runs unconditionally — vanilla drops every
	// equipment slot by its dropChance even when mob_drops is false.
	// Only the captain banner's unconditional 2.0 is modeled; the
	// default 0.085 slots are not.
	if head := e.Equips[equipHelmet]; head.Ominous {
		s.spawnMobStack(e, head)
	}
}

// spawnMobLootPools rolls the mob's loot-table drops at its position.
// The type_specific/raider is_captain condition reads the ominous
// head banner.
func (s *Server) spawnMobLootPools(e *Entity, killer *Session, name string, byPlayer bool) {
	looting := int32(0)
	smelts := false
	if killer != nil {
		held := killer.heldStack()
		looting = held.enchLevel("looting")
		smelts = held.enchLevel("fire_aspect") > 0
	}
	spawnItem := func(item string, count int32) {
		s.spawnMobItem(e, item, count)
	}
	for _, p := range mobLoot[name] {
		if p.playerKill && !byPlayer {
			continue
		}
		if p.captain && !mobIsCaptain(e) {
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
			if ent.bottleAmp {
				if id, ok := world.ItemID(item); ok && count > 0 {
					s.spawnMobStack(e, ItemStack{
						ID:           id,
						Count:        count,
						BottleAmpSet: true,
						BottleAmp:    int32(rand.Intn(5)),
					})
				}
				continue
			}
			spawnItem(item, count)
		}
	}
}

// mobIsCaptain is Raider.isCaptain: the head slot carries the ominous
// banner — vanilla checks ItemStack.matches(head, ominousBannerInstance),
// which mc-go's Ominous flag stands in for.
func mobIsCaptain(e *Entity) bool {
	return e.Equips[equipHelmet].Ominous
}

// spawnMobItem drops one item stack at the mob's position — vanilla's
// spawnAtLocation: the entity lands 0.25 above the feet with the
// standard 10-tick pickup delay.
func (s *Server) spawnMobItem(e *Entity, item string, count int32) {
	id, ok := world.ItemID(item)
	if !ok || count <= 0 {
		return
	}
	s.spawnMobStack(e, ItemStack{ID: id, Count: count})
}

// spawnMobStack is spawnMobItem for a fully-formed stack — the captain
// banner and ominous bottle carry components that item+count can't
// express.
func (s *Server) spawnMobStack(e *Entity, st ItemStack) {
	it := s.entities.Spawn(&Entity{
		Type:        mustEntityType("item"),
		Dim:         e.Dim,
		X:           e.X,
		Y:           e.Y + 0.25,
		Z:           e.Z,
		Item:        st,
		PickupDelay: 10,
	})
	s.broadcastEntitySpawn(nil, *it)
	s.broadcastEntityMetadata(it.ID, encodeItemMetadata(it.Item))
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
		OrbValue:    value,
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
	"witch":            26,
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

// mobCrammingStep mirrors LivingEntity.pushEntities' cramming gate:
// count the pushable entities whose bounding boxes intersect this
// mob's (itself and passengers excluded); when more than
// max_entity_cramming-1 share the space, a 1-in-4 tick roll deals
// cramming 6 to the whole pile. Vanilla counts passengers' hosts
// but not the riding passengers themselves.
func (s *Server) mobCrammingStep(e *Entity, maxCram int) {
	ew, eh := mobBounds(e.Type)[0], mobBounds(e.Type)[1]
	count := 0
	for _, o := range s.entities.All() {
		if o.ID == e.ID || o.Dim != e.Dim || o.Riding != 0 || o.Health <= 0 {
			continue
		}
		ow, oh := mobBounds(o.Type)[0], mobBounds(o.Type)[1]
		half := (ew + ow) / 2
		if math.Abs(o.X-e.X) < half && math.Abs(o.Z-e.Z) < half &&
			e.Y < o.Y+oh && o.Y < e.Y+eh {
			count++
		}
	}
	if count > maxCram-1 && rand.Intn(4) == 0 {
		s.damageMob(e, 6, "cramming", nil, &damageInfo{typeName: "cramming"})
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
			// Mob.readAdditionalSaveData — persistenceRequired exempts
			// the mob from every checkDespawn branch.
			if v, ok := tag.vals["PersistenceRequired"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
				e.PersistenceRequired = true
			}
			// UUID:[I;a,b,c,d] — honored like vanilla's summon NBT so
			// tests can mint a known entity id for references
			// (e.g. an item entity's Thrower).
			if u, ok := snbtUUID(tag, "UUID"); ok {
				e.UUID = u
			}
			// AbstractHorse's Tame flag (AbstractHorse.readAdditional
			// SaveData) and AbstractChestedHorse's ChestedHorse.
			if v, ok := tag.vals["Tame"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") && mountScreenTypes[name] {
				e.Tamed = true
			}
			if v, ok := tag.vals["ChestedHorse"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") && chestedMountTypes[name] {
				e.HasChest = true
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
						// A white_banner head carrying banner_patterns +
						// item_name block.minecraft.ominous_banner is the
						// captain patch — Raider.isCaptain matches on it.
						if slot == equipHelmet {
							_, pat := comps.vals["minecraft:banner_patterns"]
							nm := comps.vals["minecraft:item_name"].vals["translate"].str
							if pat && nm == "block.minecraft.ominous_banner" &&
								strings.TrimPrefix(idv.str, "minecraft:") == "white_banner" {
								it.Ominous = true
							}
						}
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
			// Age is AgeableMob's key — a zombie or piglin ignores it.
			if av, ok := tag.vals["Age"]; ok && ageableTypes[name] {
				e.Age = int32(snbtNum(av))
				e.Baby = e.Age < 0
			}
			// Vanilla only reads IsBaby where the class declares the
			// key — zombie family, piglin family, zoglin. An IsBaby
			// summon on an AgeableMob (pig & friends) stays adult.
			if bv, ok := tag.vals["IsBaby"]; ok && snbtNum(bv) > 0 && isBabyTypes[name] {
				e.Baby = true
			}
			// LeftHanded is a Mob key — all living mobs read it.
			if bv, ok := tag.vals["LeftHanded"]; ok && snbtNum(bv) > 0 {
				e.LeftHanded = true
			}
			// Fire is Entity.load's remaining burn ticks — a
			// burning mob ignites on the next baseTick.
			if fv, ok := tag.vals["Fire"]; ok {
				e.FireTicks = int32(snbtNum(fv))
			}
			// Tagged summons skip finalizeSpawn, so an explicit
			// PatrolLeader flag loads the flag without the banner the
			// spawn-time roll would have placed.
			if bv, ok := tag.vals["PatrolLeader"]; ok && snbtNum(bv) > 0 && patrollingTypes[name] {
				e.PatrolLeader = true
			}
			// PatrollingMonster.readAdditionalSaveData's remaining
			// pair: the Patrolling flag and the patrol_target
			// [I;x,y,z] block position.
			if bv, ok := tag.vals["Patrolling"]; ok && snbtNum(bv) > 0 && patrollingTypes[name] {
				e.Patrolling = true
			}
			if lv, ok := tag.vals["patrol_target"]; ok && patrollingTypes[name] && len(lv.items) == 3 {
				e.PatrolTarget = world.Pos{
					X: int(snbtNum(lv.items[0])),
					Y: int(snbtNum(lv.items[1])),
					Z: int(snbtNum(lv.items[2])),
				}
				e.HasPatrolTarget = true
			}
			// Mob.readAdditionalSaveData's home pair — any
			// home_radius != -1 latches hasHome; a non-negative one
			// also reads the home_pos [I;x,y,z] BlockPos.
			if rv, ok := tag.vals["home_radius"]; ok {
				if r := int32(snbtNum(rv)); r != -1 {
					e.HomeRadius = r
					e.HasHome = true
					if r >= 0 {
						if lv, ok := tag.vals["home_pos"]; ok && len(lv.items) == 3 {
							e.HomePos = world.Pos{
								X: int(snbtNum(lv.items[0])),
								Y: int(snbtNum(lv.items[1])),
								Z: int(snbtNum(lv.items[2])),
							}
						}
					}
				}
			}
			if overworldZombifyTypes[name] {
				if bv, ok := tag.vals["IsImmuneToZombification"]; ok && snbtNum(bv) > 0 {
					e.ImmuneToZombification = true
				}
				if bv, ok := tag.vals["TimeInOverworld"]; ok {
					e.TimeInOverworld = int32(snbtNum(bv))
				}
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
			// Raider.readAdditionalSaveData: CanJoinRaid loads as a
			// plain bool — a tagged summon skipped finalizeSpawn, so
			// this is the only way a witch it applies to gets set.
			if cv, ok := tag.vals["CanJoinRaid"]; ok && raidRaiderType(name) {
				e.CanJoinRaid = cv.num == "1b" || cv.num == "1" || cv.str == "true"
			}
			if sv, ok := tag.vals["NoGravity"]; ok && (sv.num == "1b" || sv.num == "1" || sv.str == "true") {
				e.NoGravity = true
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
			// AreaEffectCloud's readAdditionalSaveData keys — Radius/
			// Duration/WaitTime/RadiusOnUse/RadiusPerTick feed the
			// synced radius and the tick loop; potion_contents'
			// custom_effects seed the dose map (vanilla stores them
			// as a PotionContents codec).
			if name == "area_effect_cloud" {
				if v, ok := tag.vals["Radius"]; ok {
					r := float32(snbtNum(v))
					if r < 0 {
						r = 0
					}
					if r > 32 {
						r = 32
					}
					e.CloudRadius = r
				}
				if v, ok := tag.vals["Duration"]; ok {
					e.CloudDuration = int32(snbtNum(v))
				}
				if v, ok := tag.vals["WaitTime"]; ok {
					e.CloudWait = int32(snbtNum(v))
				}
				if v, ok := tag.vals["RadiusOnUse"]; ok {
					e.CloudRadiusOnUse = float32(snbtNum(v))
				}
				if v, ok := tag.vals["RadiusPerTick"]; ok {
					e.CloudRadiusPerTick = float32(snbtNum(v))
				}
				if v, ok := tag.vals["Age"]; ok {
					e.CloudAge = int32(snbtNum(v))
				}
				if pc, ok := tag.vals["potion_contents"]; ok {
					if ce, ok := pc.vals["custom_effects"]; ok {
						for _, inst := range ce.items {
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
							if e.Effects == nil {
								e.Effects = map[int32]mobEffectInst{}
							}
							e.Effects[eid] = mobEffectInst{amplifier: amp, expiry: expiry}
						}
					}
				}
			}
			// Creeper's addAdditionalSaveData keys: powered doubles
			// the blast, Fuse resizes the 30-tick swell window,
			// ExplosionRadius scales it, ignited lights the fuse at
			// spawn.
			if name == "creeper" {
				if v, ok := tag.vals["powered"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
					e.Powered = true
				}
				if v, ok := tag.vals["Fuse"]; ok {
					e.MaxSwell = int32(snbtNum(v))
				}
				if v, ok := tag.vals["ExplosionRadius"]; ok {
					e.BlastPower = int32(snbtNum(v))
				}
				if v, ok := tag.vals["ignited"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
					e.Ignited = true
				}
			}
			// Sheep/Bogged readAdditionalSaveData shear flags and
			// SnowGolem's Pumpkin — each read only where the class
			// declares the key; Pumpkin absent keeps the default-on.
			if name == "sheep" {
				if v, ok := tag.vals["Sheared"]; ok && snbtNum(v) > 0 {
					e.Sheared = true
				}
			}
			if name == "bogged" {
				if v, ok := tag.vals["sheared"]; ok && snbtNum(v) > 0 {
					e.Sheared = true
				}
			}
			if name == "snow_golem" {
				if v, ok := tag.vals["Pumpkin"]; ok {
					e.Pumpkin = snbtNum(v) > 0
				}
			}

		}
		// Vanilla's /summon runs finalizeSpawn only on the bare form:
		// SummonCommand passes shouldFinalizeSpawn=false whenever an
		// NBT tag is present, so a tagged summon never populates
		// equipment or rolls piglin's 20% baby.
		if tag == nil {
			s.mobDefaultEquip(e)
			// Raider.finalizeSpawn: CanJoinRaid = !isWitch || reason
			// != NATURAL — COMMAND is not NATURAL, so every bare-summon
			// raider (witch included) flags true.
			if raidRaiderType(name) {
				e.CanJoinRaid = true
			}
		}
		s.broadcastFreshMob(e)
		return opsEchoReply(translatable("commands.summon.success", entityDisplayName(e)))
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
		return opsEchoReply(translatable("commands.summon.success", entityDisplayName(e)))
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
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
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
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft.arrow")))
	}
	// A small fireball summoned with Motion flies like a dispenser
	// shot — ownerless, no acceleration power needed for the wire.
	if name == "small_fireball" {
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
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// Wind charges fly straight (no gravity) and burst on contact.
	if name == "wind_charge" || name == "breeze_wind_charge" || name == "wither_skull" || name == "fireball" || name == "dragon_fireball" || name == "shulker_bullet" {
		mx, my, mz := 0.0, 0.0, 0.0
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				mx = snbtNum(mv.items[0])
				my = snbtNum(mv.items[1])
				mz = snbtNum(mv.items[2])
			}
		}
		// LargeFireball's ExplosionPower NBT default is the ghast
		// power 1.
		power := float32(1)
		if tag != nil {
			if pv, ok := tag.vals["ExplosionPower"]; ok {
				power = float32(snbtNum(pv))
			}
		}
		e := s.entities.Spawn(&Entity{
			Type: mustEntityType(name),
			Dim:  sess.dimName(),
			X:    x, Y: y, Z: z,
			MotionX: mx, MotionY: my, MotionZ: mz,
			VX: int16(mx * 8000), VY: int16(my * 8000), VZ: int16(mz * 8000),
			ExplosionPower: power,
		})
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
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
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft.lightning_bolt")))
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
		// Supplier-carried max_health doubles as the summon gate:
		// any type with a generated attribute supplier spawns at the
		// supplier's base. mobHealth only overrides where spawn
		// health differs (horse rolls 15-30, sulfur_cube 4*size).
		b, ok := entityAttrBases[name]["max_health"]
		if !ok || b <= 0 {
			return nil, false
		}
		health = float32(b)
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
		Pumpkin:   name == "snow_golem",
	})
	summonAttrTouches(e, name, init)
	s.broadcastFreshMob(e)
	return e, true
}

// summonHealthDefault covers the types whose attribute supplier skips
// max_health because finalizeSpawn randomizes it — they still summon
// at the attribute default (LivingEntity's 20).
var summonHealthDefault = map[string]bool{
	"donkey": true, "mule": true, "llama": true, "trader_llama": true,
}

// summonMobRaw is summonMob without the broadcast: the caller applies
// summon NBT first so the tracker-attach burst carries the final
// metadata and equipment. summonMob stays for callers with no NBT.
func (s *Server) summonMobRaw(d *dimension, name string, x, y, z float64, noAI, init bool) (*Entity, bool) {
	id, ok := EntityTypeID(name)
	if !ok {
		return nil, false
	}
	// AreaEffectCloud is an Entity, not a LivingEntity — the
	// max_health gate below would reject it. Its constructor
	// defaults: duration -1 (infinite), waitTime 20,
	// reapplicationDelay 20, radiusOnUse/radiusPerTick 0.
	if name == "area_effect_cloud" {
		e := s.entities.Spawn(&Entity{
			Type:          id,
			Dim:           d.name,
			X:             x,
			Y:             y,
			Z:             z,
			CloudWait:     20,
			CloudDuration: -1,
			CloudVictims:  map[int32]int32{},
		})
		e.CloudRadius = 3.0
		return e, true
	}
	health, known := mobHealth[name]
	if !known {
		// Supplier-carried max_health doubles as the summon gate:
		// any type with a generated attribute supplier spawns at the
		// supplier's base. mobHealth only overrides where spawn
		// health differs (horse rolls 15-30, sulfur_cube 4*size).
		b, ok := entityAttrBases[name]["max_health"]
		if !ok || b <= 0 {
			// Chested horses randomize max_health in finalizeSpawn
			// (15-30 natural roll); a tagged summon skips the roll
			// and falls back to the attribute's own default of 20.
			if summonHealthDefault[name] {
				health = 20
			} else {
				return nil, false
			}
		} else {
			health = float32(b)
		}
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
		Pumpkin:   name == "snow_golem",
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
	if cn, ok := EntityTypeName(e.Type); ok && cn == "area_effect_cloud" {
		// The cloud's pairing dirty-pack carries only the synced
		// entries its summon NBT touched — radius differs from the
		// 3.0 defineSynchedData default, potion_contents seeds the
		// entity_effect particle holder.
		if meta := encodeCloudMeta(e); len(meta) > 0 {
			s.broadcastEntityMetadata(e.ID, meta)
		}
		e.MetaDirty = true
		e.PosDirty = true
		e.SyncCount = 0
		return
	}
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
		case "sheep":
			// Sheep's DATA_WOOL_ID (index 18, byte): low nibble is
			// the dye color (always white=0), bit 0x10 = sheared.
			if e.Sheared {
				b.Byte(18)
				b.Byte(0) // type: byte
				b.Byte(0x10)
			}
		case "snow_golem":
			// SnowGolem's DATA_PUMPKIN_ID (index 16, byte): bit 1 =
			// pumpkin head. Default on, so it packs only when off.
			if !e.Pumpkin {
				b.Byte(16)
				b.Byte(0) // type: byte
				b.Byte(0)
			}
		case "bogged":
			// Bogged's DATA_SHEARED (index 16, boolean) packs only
			// when the mushrooms are off.
			if e.Sheared {
				b.Byte(16)
				b.Byte(8) // type: boolean
				b.Byte(1)
			}
		case "zombie_villager":
			if e.Baby {
				b.Byte(16)
				b.Byte(8)
				b.Bool(true)
			}
			if e.WaterConv {
				b.Byte(17)
				b.Byte(8)
				b.Bool(true)
			}
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
		case "zombie", "husk", "drowned":
			// 16 = baby (DATA_BABY_ID), 17 = is_under_water_converting
			// — zombie_villager's cure flag already lives at 19.
			if e.Baby {
				b.Byte(16)
				b.Byte(8)
				b.Bool(true)
			}
			if e.WaterConv {
				b.Byte(17)
				b.Byte(8)
				b.Bool(true)
			}
		case "skeleton":
			// 16 = is_freeze_converting (DATA_STRAY_CONVERSION_ID).
			if e.FreezeConv {
				b.Byte(16)
				b.Byte(8)
				b.Bool(true)
			}
		case "creeper":
			// powered (17, bool) and swell_dir (18, int — non-default
			// only while the fuse runs up).
			if e.Powered {
				b.Byte(17)
				b.Byte(8)
				b.Bool(true)
			}
			if e.SwellDir > 0 {
				b.Byte(18)
				b.Byte(1) // type: varint
				b.VarInt(1)
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
	s.broadcastMobSharedFlags(e.ID, true, e.GlowUntil > s.gameTime())
}

// broadcastMobSharedFlags sends the index-0 entity-data byte — the
// bits vanilla's setSharedFlag flips: 0x01 on_fire, 0x40 glowing — to
// every session tracking the mob's dimension.
func (s *Server) broadcastMobSharedFlags(eid int32, onFire, glowing bool) {
	flags := byte(0)
	if onFire {
		flags |= 0x01
	}
	if glowing {
		flags |= 0x40
	}
	var b protocol.Buffer
	b.Byte(0) // index: shared flags
	b.Byte(0) // type: byte
	b.Byte(flags)
	b.Byte(0xFF)
	s.broadcastEntityMetadata(eid, b.Bytes())
}

// mobVillagerAngry is Villager.hurtServer's reputation hook: a
// villager hurt by a player-sourced hit broadcasts VILLAGER_ANGRY
// (entity_event 13) ahead of the damage_event. It doesn't fire when
// the source isn't a player, when the hit lands inside the
// invulnerability grace (hurt returns early), or when it kills.
func (s *Server) mobVillagerAngry(e *Entity, res DamageResult, info *damageInfo) {
	if info == nil || res.Graced || res.Health <= 0 {
		return
	}
	if name, ok := EntityTypeName(e.Type); !ok || name != "villager" {
		return
	}
	if s.sessionByEntityID(info.cause) == nil {
		return
	}
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 13)
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
		// hurt(source, 0) — egg/snowball/xp-bottle impacts — still
		// lands in vanilla: the wire shows the damage_event, the
		// knockback motion, and the hurt sound even though no health
		// moved (measured on a zombie: type thrown, hurt voice, 0x65
		// motion). Graced repeats ship nothing.
		if res.Exists && !res.Graced && res.Health > 0 && info != nil {
			s.mobVillagerAngry(e, res, info)
			s.broadcastDamageEvent(e.ID, *info)
			if info.typeName != "player_attack" && !noKnockback(info.typeName) {
				kbSrc := info.cause
				if kbSrc == 0 {
					kbSrc = info.direct
				}
				if kbSrc != 0 && kbSrc != e.ID {
					s.mobHurtKnockback(e, kbSrc)
				}
			}
			s.mobHurtSound(e, res)
		}
		return 0, res.Graced, false
	}
	// Vanilla stores lastDamageSource on every landed hurt — the
	// witch's fire-resistance roll reads it through IS_FIRE.
	if ep, ok := s.entities.GetPtr(e.ID); ok {
		ep.LastDamageType = typeName
	}
	// The graced-excess branch (hurt returned inside invulnerableTime)
	// sets b8=false in vanilla: no damage_event, no hurt/death sound —
	// only the health meta moves, and die() still runs if it killed.
	if info != nil && !res.Graced {
		s.mobVillagerAngry(e, res, info)
		s.broadcastDamageEvent(e.ID, *info)
	}
	// LivingEntity.hurt's merged tail applies dealDefaultKnockback to
	// every landed entity-sourced hit outside no_knockback — including
	// the graced-excess hit — as a 0.4 push away from the source's
	// getEntity() (cause, falling back to direct like vanilla).
	if info != nil && info.typeName != "player_attack" &&
		!noKnockback(info.typeName) {
		kbSrc := info.cause
		if kbSrc == 0 {
			kbSrc = info.direct
		}
		if kbSrc != 0 && kbSrc != e.ID {
			s.mobHurtKnockback(e, kbSrc)
		}
	}
	health, d := res.Health, res.Dealt
	if health > 0 && !res.Graced {
		s.mobHurtSound(e, res)
	}
	// LivingEntity.playSecondaryHurtSound — a thorns hit also plays
	// enchant.thorns.hit on the HOSTILE source (PLAYERS for player
	// victims, handled in damageWith), dead or alive.
	if typeName == "thorns" && !res.Graced {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, sndThornsHit, srcHostile, 1, 1, nil)
	}
	// Vanilla's killed_by_player window: a player-caused hurt keeps
	// the flag hot for 100 ticks (lastHurtByPlayerMemoryTime).
	if killer != nil {
		// Some call sites hand damageMob a snapshot copy — state
		// writes go through the manager's stored pointer.
		if ep, ok := s.entities.GetPtr(e.ID); ok {
			ep.LastHurtByPlayerTicks = 100
			if ep.Raid != nil {
				// Raid.updateHeroes: landing damage on a raider
				// makes the attacker a hero-of-the-village target.
				ep.Raid.heroes[killer.uuid] = true
			}
			// NeutralMob provocation: the hurt player becomes this
			// mob's anger target. A zombified piglin additionally
			// arms the persistent-anger timer (PERSISTENT_ANGER_TIME,
			// 20-39s) so the anger outlives the goal's target loss.
			ep.AngryAt = killer.uuid
			if kn, _ := EntityTypeName(ep.Type); kn == "zombified_piglin" {
				ep.AngryUntil = s.gameTime() + 400 + rand.Int63n(381)
			}
		}
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
		// Mob.checkDespawn, ticking order matched to serverAiStep:
		// noActionTime counts up first, then the despawn rules run —
		// peaceful purge, persistence exemption, the >128 instant
		// discard (PatrollingMonster's removeWhenFarAway re-keys on
		// patrolling), and the >32 idle 1-in-800 roll.
		e.NoActionTime++
		if s.mobCheckDespawn(e) {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
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
				if vd := s.dimensionByName(e.Dim); vd != nil {
					s.releasePOI(vd, e)
				}
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
				s.broadcastMobSharedFlags(e.ID, false, e.GlowUntil > s.gameTime())
			}
		}
		// The GLOWING instance /raid glow applies expires after its
		// 1000 ticks — same shared-flags byte, bit 0x40 off.
		if e.GlowUntil != 0 && e.GlowUntil <= s.gameTime() {
			e.GlowUntil = 0
			s.broadcastMobSharedFlags(e.ID, e.FireTicks > 0, false)
		}
		if e.invulnTicks > 0 {
			e.invulnTicks--
		}
		if e.loveTicks > 0 {
			e.loveTicks--
		}
		ambName, _ := EntityTypeName(e.Type)
		// Villager brain: AcquirePoi claims the nearest unclaimed bed
		// or bell inside 48 blocks — the raid-start precondition.
		if ambName == "villager" {
			if vd := s.dimensionByName(e.Dim); vd != nil {
				s.villagerPOIStep(vd, e)
			}
		}
		// Raider.aiStep: on the shared gameTime%20 tick an unjoined
		// raider with canJoinRaid inside a raid's radius flags in at
		// the current wave — the /raid spawnleader captain lands in
		// the raid it guards.
		if e.Raid == nil && raidRaiderType(ambName) && e.CanJoinRaid && s.clock.count%20 == 0 {
			if vd := s.dimensionByName(e.Dim); vd != nil {
				if r := vd.raidAt(int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))); r != nil && r.status == raidOngoing {
					s.raidJoin(r, r.spawned, e)
				}
			}
		}
		// AgeableMob.baseTick's age step: babies count up toward grown
		// at 0, adults count a breeding cooldown down.
		if ageableTypes[ambName] && e.Age != 0 {
			if e.Age < 0 {
				e.Age++
				if e.Age == 0 {
					e.Baby = false
					e.MetaDirty = true
				}
			} else {
				e.Age--
			}
		}
		// Mob.baseTick's ambient voice — independent of NoAI: counter
		// climbs from 0, fires when random < count, resets to
		// -interval. playHurtSound also resets the counter.
		if a, ok := mobAmbient[ambName]; ok {
			e.ambientSoundTime++
			if rand.Int31n(1000) < e.ambientSoundTime {
				e.ambientSoundTime = -a.interval
				snd := a.snd
				if e.Baby && a.babySnd != 0 {
					snd = a.babySnd
				}
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd,
					mobVoiceOf(e).src(), a.vol, voicePitchE(e), nil)
			}
		}
		// Mob.aiStep's daylight burn runs on every living tick too —
		// aiStep has no isEffectiveAi gate, so NoAI mobs ignite.
		if burnDaylight[ambName] {
			s.mobSunBurn(e, ambName)
		}
		// SnowGolem.aiStep's trail + melt ride the same every-tick
		// placement — they fire for NoAI golems too.
		if ambName == "snow_golem" {
			s.snowGolemStep(e, s.dims[e.Dim], s.dimWorld(e.Dim))
		}
		// LivingEntity.pushEntities' cramming check is in the same
		// aiStep region — every living tick regardless of NoAI.
		if mc := s.gameRuleInt("minecraft:max_entity_cramming"); mc > 0 {
			s.mobCrammingStep(e, mc)
		}
		// NeutralMob anger bookkeeping: a zombified piglin's
		// persistentAngerEndTime lapses and clears the anger; a
		// provoker who died stops being a target under
		// forgive_dead_players; and an angry piglin alerts its herd
		// on the 80-120 tick ALERT_INTERVAL.
		if e.AngryAt != [16]byte{} {
			if e.AngryUntil > 0 && s.gameTime() >= e.AngryUntil {
				e.AngryAt = [16]byte{}
				e.AngryUntil = 0
			} else if s.provokerDead(e) {
				e.AngryAt = [16]byte{}
				e.AngryUntil = 0
			} else if ambName == "zombified_piglin" && s.gameTime() >= e.nextAlertAt {
				e.nextAlertAt = s.gameTime() + 80 + rand.Int63n(41)
				s.piglinAlert(e)
			}
		}
		// Creeper.tick's fuse bookkeeping sits outside the goal
		// system — an ignited fuse runs even under NoAI.
		if ambName == "creeper" {
			if e.Ignited && e.SwellDir != 1 {
				e.SwellDir = 1
				e.MetaDirty = true
			}
			if e.SwellDir > 0 && e.Swell == 0 {
				// entity.creeper.primed at fuse start, pitch 0.5.
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 481, 5, 1, 0.5, nil)
			}
			e.Swell += e.SwellDir
			if e.Swell < 0 {
				e.Swell = 0
			}
			maxSwell := e.MaxSwell
			if maxSwell <= 0 {
				maxSwell = 30
			}
			if e.Swell >= maxSwell {
				s.explodeCreeper(e)
				continue
			}
		}
		// Witch.aiStep's drink check sits above super.aiStep — it runs
		// even for NoAI witches (isEffectiveAi only gates the goal
		// selector inside customServerAiStep).
		if ambName == "witch" {
			s.witchDrinkStep(e, s.dims[e.Dim])
		}
		if !e.NoAI && e.Riding == 0 {
			// Drowned/stray conversion bookkeeping — inside the NoAI
			// gate like vanilla's aiStep/tick placement.
			if s.mobConvertTick(e, s.dims[e.Dim], ambName) {
				continue
			}
			// Sheep's EatBlockGoal (priority 5) — while the eat
			// animation runs the sheep stands still.
			if ambName == "sheep" && s.sheepEatTick(e, s.dims[e.Dim]) {
				s.mobSyncFlush(e)
				continue
			}
			// A passenger stays put — vanilla's entity rider has no
			// gravity or wander of its own. Hostile melee mobs chase
			// the nearest player and swing on a vanilla-paced cooldown;
			// a successful chase replaces the wander step. Bow-armed
			// AbstractSkeletons run RangedBowAttackGoal; bare-handed
			// ones fall back to MeleeAttackGoal — neither is in
			// mobMelee, so they're dispatched by name here.
			var goalActed bool
			if s.mobFleeStep(e, ambName) {
				goalActed = true
			} else if mobRanged[ambName] {
				if s.mobHoldsBow(e) {
					goalActed = s.mobRangedStep(e)
				} else {
					goalActed = s.mobMeleeStep(e)
				}
			} else if mobSwell[ambName] {
				goalActed = s.mobSwellStep(e, ambName)
			} else if ambName == "witch" {
				goalActed = s.mobWitchAttackStep(e)
			} else if mobCrossbow[ambName] && s.mobHoldsCrossbow(e) {
				goalActed = s.mobCrossbowStep(e, ambName)
			} else if mobBeam[ambName] {
				goalActed = s.mobGuardianStep(e, ambName)
			} else if mobMelee[e.Type] {
				goalActed = s.mobMeleeStep(e)
			}
			if goalActed {
				s.mobSyncFlush(e)
				continue
			}
			w := s.dimWorld(e.Dim)
			if w != nil {
				// Goal priority order once combat has declined:
				// LongDistancePatrolGoal (4) ahead of FleeSunGoal (5)
				// ahead of the ambient wander.
				if !s.mobPatrolStep(e, s.dims[e.Dim], w) &&
					!(e.FireTicks > 0 && mobSunFlees[ambName] && s.mobSunFleeStep(e, s.dims[e.Dim], w)) {
					s.mobMoveStep(e, w)
				}
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
	if !s.mobGrounded(e, w) {
		return
	}
	e.WanderTicks++
	// Change heading every 40 steps; walk otherwise.
	if e.WanderTicks%40 == 1 {
		e.Yaw = mobYawFor(e.ID, e.WanderTicks)
		e.HeadYaw = e.Yaw
	}
	dx, _, dz := lookVector(e.Yaw, 0)
	nx := e.X + dx*mobWalkSpeed
	nz := e.Z + dz*mobWalkSpeed
	// RestrictSunGoal: the AbstractSkeleton family latches avoidSun
	// while daylight is bright and the head slot is bare, and
	// GroundPathNavigation.trimPath then cuts every path at the first
	// sky-visible node — but only while the mob itself is under cover
	// (a sunlit skeleton walks free; FleeSunGoal carries it to shade).
	// mc-go has no path graph, so the wander step refuses the same
	// shade-to-sun crossing: the step is skipped and the heading
	// re-aimed, pinning the skeleton along the shadow's edge.
	if mobSunFlees[func() string { n, _ := EntityTypeName(e.Type); return n }()] &&
		s.isBrightOutside() && e.Equips[equipHelmet].Empty() {
		if d := s.dims[e.Dim]; d != nil && d.skylight {
			midY := int(math.Floor(e.Y + 0.5))
			cur := world.Pos{X: int(math.Floor(e.X)), Y: midY, Z: int(math.Floor(e.Z))}
			nxt := world.Pos{X: int(math.Floor(nx)), Y: midY, Z: int(math.Floor(nz))}
			if !s.cropLit(d, cur) && s.cropLit(d, nxt) {
				e.Yaw += 90
				e.HeadYaw = e.Yaw
				return
			}
		}
	}
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

// mobGrounded is the falling half of a mob tick: gravity until a solid
// block is underfoot — accumulating fallDistance on every downward
// step — then Entity.checkFallDamage's landing edge. True once the
// mob rests on ground — NoGravity mobs hang midair and are treated
// as grounded so ai keeps running.
func (s *Server) mobGrounded(e *Entity, w *world.World) bool {
	if e.NoGravity {
		return true
	}
	below := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y)) - 1,
		Z: int(math.Floor(e.Z)),
	}
	if w.GetBlock(below) == world.Air {
		e.Grounded = false
		prevY := e.Y
		e.MotionY -= 0.08
		if e.MotionY < -1.0 {
			e.MotionY = -1.0
		}
		e.Y += e.MotionY
		// Entity.updateFallDistance: downward travel accumulates.
		if dy := prevY - e.Y; dy > 0 {
			e.FallDist += dy
		}
		return false
	}
	// Landing edge. Vanilla's fallDistance counts the whole drop —
	// the residual down to the floor top is part of the last tick's
	// travel — so fold e.Y's fractional remainder in first. Water
	// contact zeroes it instead (vanilla resets per tick in fluid).
	if !e.Grounded && e.FallDist > 0 {
		if _, _, fluid := fluidOf(w.GetBlock(below)); !fluid {
			e.FallDist += e.Y - math.Floor(e.Y)
			s.mobFallDamage(e, s.dims[e.Dim], below)
		}
		e.FallDist = 0
	}
	e.MotionY = 0
	e.Y = math.Floor(e.Y)
	e.Grounded = true
	return true
}

// mobFallDamageImmune is the 26.2 #fall_damage_immune entity_type
// tag — these types return early from Entity.causeFallDamage.
var mobFallDamageImmune = map[string]bool{
	"copper_golem": true, "iron_golem": true, "snow_golem": true,
	"shulker": true, "allay": true, "bat": true, "bee": true,
	"blaze": true, "cat": true, "chicken": true, "ghast": true,
	"happy_ghast": true, "phantom": true, "magma_cube": true,
	"ocelot": true, "parrot": true, "wither": true, "breeze": true,
}

// mobFallDamage is Entity.checkFallDamage's damage tail for mobs:
// LivingEntity.causeFallDamage turns fallDistance into
// floor((fallDistance + 1e-6 - safe_fall_distance) * multiplier *
// fall_damage_multiplier), plays the type's Fallsounds pair (damage
// >4 picks big), then runs the normal hurt tail (damage_event, hurt
// voice, death on a lethal landing).
func (s *Server) mobFallDamage(e *Entity, dm *dimension, below world.Pos) {
	name, _ := EntityTypeName(e.Type)
	if mobFallDamageImmune[name] {
		return
	}
	// Block.fallOn dispatches on the landed block: slime nullifies the
	// landing (multiplier 0 — mobs never suppress bounce), honey and
	// hay soften it to 0.2 (honey additionally slides and re-plays
	// its fall sound), beds halve the fallDistance, an up-pointing
	// dripstone tip impales with (d+2.5, mult 2, stalagmite), and
	// powder snow harms nothing — it just plays the Fallsounds pair.
	fallDist := e.FallDist
	dmgMult := 1.0
	dmgType := "fall"
	honeyReplay := false
	// `below` is the support block — mobGrounded's landing edge fires
	// the tick floor(feet)-1 is non-air, so feet stop exactly on its
	// top and vanilla's floor(y-0.2) resolves to the same pos.
	landedName, _ := blockNameAt(dm, below)
	switch {
	case landedName == "slime_block":
		return
	case landedName == "honey_block":
		if snd, ok := soundEventIDs["block.honey_block.slide"]; ok {
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, mobVoiceOf(e).src(), 1, 1, nil)
		}
		dmgMult = 0.2
		honeyReplay = true
	case landedName == "hay_block":
		dmgMult = 0.2
	case strings.HasSuffix(landedName, "_bed"):
		fallDist *= 0.5
	case landedName == "pointed_dripstone":
		if blk := dm.w.GetBlock(below); blk != 0 {
			thick, _ := world.StateProperty(blk, "thickness")
			dir, _ := world.StateProperty(blk, "vertical_direction")
			if thick == "tip" && dir == "up" {
				fallDist += 2.5
				dmgMult = 2.0
				dmgType = "stalagmite"
			}
		}
	case landedName == "powder_snow":
		if fallDist >= 4 {
			small, big := int32(702), int32(694)
			if mobVoiceOf(e).hostile {
				small, big = 874, 871
			}
			snd := small
			if fallDist >= 7 {
				snd = big
			}
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, mobVoiceOf(e).src(), 1, 1, nil)
		}
		return
	}
	safe := 3.0
	if v, ok := entityAttrBases[name]["safe_fall_distance"]; ok {
		safe = v
	}
	mult := dmgMult
	if v, ok := entityAttrBases[name]["fall_damage_multiplier"]; ok {
		mult = v
	}
	dmg := int(math.Floor((fallDist + 1e-6 - safe) * mult))
	if dmg <= 0 {
		return
	}
	// LivingEntity.getFallSounds: Monsters get the hostile pair,
	// everything else the generic one.
	small, big := int32(702), int32(694) // entity.generic.small/big_fall
	if mobVoiceOf(e).hostile {
		small, big = 874, 871 // entity.hostile.small/big_fall
	}
	snd := small
	if dmg > 4 {
		snd = big
	}
	// Entity.playSound's coordinate overload — the positional
	// clientbound sound packet at the mob's feet.
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, mobVoiceOf(e).src(), 1, 1, nil)
	s.playBlockFallSound(e.Dim, e.X, e.Y, e.Z, mobVoiceOf(e).src(), below)
	if honeyReplay {
		// HoneyBlock.fallOn re-plays its SoundType fall sound when
		// causeFallDamage actually hurt, on top of LivingEntity's.
		if fsnd, ok := blockFallSounds["honey_block"]; ok {
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, fsnd, mobVoiceOf(e).src(), 0.5, 0.75, nil)
		}
	}
	s.damageMob(e, float32(dmg), dmgType, nil, &damageInfo{typeName: dmgType})
}

// playBlockFallSound mirrors LivingEntity.playBlockFallSound: the
// support block — when not air — plays its SoundType.fallSound
// positionally at the entity's coordinates at SoundType volume*0.5,
// pitch*0.75 (blockFallSoundNames/blockFallSoundScale come from the
// generated per-block table).
func (s *Server) playBlockFallSound(dim string, x, y, z float64, src int32, pos world.Pos) {
	name, ok := blockNameAt(s.dims[dim], pos)
	if !ok || name == "air" || name == "void_air" || name == "cave_air" {
		return
	}
	snd, ok := blockFallSounds[name]
	if !ok {
		return
	}
	vol, pitch := float32(1), float32(1)
	if sc, has := blockFallSoundScale[name]; has {
		vol, pitch = sc[0], sc[1]
	}
	s.playSoundSrc(dim, x, y, z, snd, src, vol*0.5, pitch*0.75, nil)
}

// mobMeleeTypes are the hostile mobs mc-go's chase-melee AI drives.
// The swung damage is the supplier's attack_damage — the generated
// entityAttrBases table — not a hand list (that had the spider at 3
// vs vanilla's 2 and the zombified piglin at 8 vs 5).
// mobNeutral is the melee-capable set vanilla gates on provocation —
// NeutralMob types plus the iron golem's retaliate-only targeting.
var mobNeutral = map[string]bool{
	"zombified_piglin": true,
	"iron_golem":       true,
	"spider":           true,
	"cave_spider":      true,
}

// mobSpiderDark is the subset that also hunts unprovoked in the dark
// — Spider$SpiderTargetGoal's getLightLevelDependentMagicValue<0.5
// gate (night, or shade at noon).
var mobSpiderDark = map[string]bool{
	"spider":      true,
	"cave_spider": true,
}

var mobMelee = map[int32]bool{}

func init() {
	for _, name := range []string{
		"zombie", "husk", "drowned", "zombie_villager",
		"zombified_piglin", "spider", "cave_spider", "iron_golem",
	} {
		if id, ok := EntityTypeID(name); ok {
			mobMelee[id] = true
		}
	}
}

// mobMeleeDamage reads the attack_damage supplier base, with the iron
// golem's doHurtTarget override: vanilla rolls
// attackDamage/2 + random.nextInt(attackDamage) per swing — 7.5..22.5
// at the 15 base, not a flat 15.
func mobMeleeDamage(name string) float32 {
	base, _ := entityAttrBases[name]["attack_damage"]
	if name == "iron_golem" {
		return float32(base)/2 + float32(rand.Intn(int(base)))
	}
	return float32(base)
}

// mobChaseSpeed is the vanilla zombie-family movement speed in
// blocks per tick (movementSpeed attribute 0.23).
const mobChaseSpeed = 0.23

// nearestMobTarget picks the nearest survival-mode session inside the
// mob's follow_range — the shared pick for the melee and ranged goals.
// Neutral mobs only see the player they're angry at; spiders also see
// everyone when the eye block is dark.
func (s *Server) nearestMobTarget(e *Entity, kn string) *Session {
	var target *Session
	// Vanilla's MeleeAttackGoal targets inside followRange — the
	// generated supplier base (35 for the zombie family, 16 spider).
	best := 35.0 * 35.0
	if fr, ok := entityAttrBases[kn]["follow_range"]; ok {
		best = fr * fr
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.spawned.Load() || sess.dead ||
			sess.mode() == GameModeCreative || sess.mode() == GameModeSpectator {
			return true
		}
		if sess.dimension().name != e.Dim {
			return true
		}
		// Neutrals chase only the player they're angry at — and only
		// while an anger timer still runs (piglins roll 400-780 ticks;
		// a 0 timer is the golem/spider's open-ended retaliation).
		// Spiders also target unprovoked when the eye block is dark —
		// SpiderTargetGoal's light<0.5 gate.
		if mobNeutral[kn] {
			angry := e.AngryAt == sess.uuid &&
				(e.AngryUntil == 0 || s.gameTime() < e.AngryUntil)
			if !angry && !(mobSpiderDark[kn] && s.spiderCanHunt(e)) {
				return true
			}
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		if d2 := dx*dx + dz*dz + dy*dy*0.25; d2 < best {
			best = d2
			target = sess
		}
		return true
	})
	return target
}

// mobMeleeStep runs one hostile-melee tick: chase the nearest living
// survival player inside follow range; within reach, swing (the animate
// broadcast) and land attackDamage on the vanilla 20-tick cooldown.
// Reports whether the mob had a target — true replaces wander.
func (s *Server) mobMeleeStep(e *Entity) bool {
	kn, _ := EntityTypeName(e.Type)
	dmg := mobMeleeDamage(kn)
	target := s.nearestMobTarget(e, kn)
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
		if kn == "iron_golem" {
			// IronGolem.doHurtTarget plays the attack whoosh on every
			// swing — landed or not.
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 897, srcNeutral, 1, 1, nil)
		}
		dealt := s.damageWith(target, dmg, translatable("death.attack.mob",
			literal(target.getName()), literal(mobDisplayName(kn))), damageSource{
			typeName: "mob_attack",
			cause:    e.ID,
			direct:   e.ID,
		})
		if dealt > 0 {
			s.mobPostAttack(e, kn, target)
		}
	}
	return true
}

// mobRanged is the AbstractSkeleton family: with a bow in mainhand
// they run RangedBowAttackGoal, bare-handed they run MeleeAttackGoal
// (reassessWeaponGoal swaps the two on equipment change).
var mobRanged = map[string]bool{
	"skeleton": true,
	"stray":    true,
	"bogged":   true,
}

// mobFlees is the measured AvoidEntityGoal table — feared class
// ("player" and "monster" are predicates, the rest entity types),
// trigger distance, and sprint speed modifier. Verified on the 26.2
// jar: creeper→ocelot+cat 6.0/1.0/1.2, skeletons→wolf 6.0/1.0/1.2,
// rabbit→player 8 / wolf 10 / monster 4 at 2.2, fox→player 16 /
// wolf+polar_bear 8 at 1.4 sprint.
type fleeRule struct {
	feared string
	dist   float64
	sprint float64
}

var mobFlees = map[string][]fleeRule{
	"skeleton": {{"wolf", 6.0, 1.2}},
	"stray":    {{"wolf", 6.0, 1.2}},
	"bogged":   {{"wolf", 6.0, 1.2}},
	"creeper":  {{"ocelot", 6.0, 1.2}, {"cat", 6.0, 1.2}},
	"rabbit":   {{"player", 8.0, 2.2}, {"wolf", 10.0, 2.2}, {"monster", 4.0, 2.2}},
	"fox":      {{"player", 16.0, 1.4}, {"wolf", 8.0, 1.4}, {"polar_bear", 8.0, 1.4}},
}

// mobMonsterType approximates vanilla's Monster superclass for the
// rabbit's predator check.
var mobMonsterType = map[string]bool{
	"zombie": true, "husk": true, "drowned": true, "zombie_villager": true,
	"zombified_piglin": true, "skeleton": true, "stray": true, "bogged": true,
	"wither_skeleton": true, "spider": true, "cave_spider": true, "creeper": true,
	"witch": true, "slime": true, "magma_cube": true, "silverfish": true,
	"enderman": true, "blaze": true, "ghast": true, "phantom": true,
	"hoglin": true, "zoglin": true, "piglin": true, "piglin_brute": true,
	"vex": true, "vindicator": true, "evoker": true, "pillager": true,
	"ravager": true, "warden": true, "guardian": true, "elder_guardian": true,
	"shulker": true, "endermite": true, "breeze": true, "creaking": true,
	"wolf": true,
}

// mobFleeStep is AvoidEntityGoal: the nearest feared entity inside the
// rule's trigger distance sends the mob away at sprint speed; the goal
// stays armed a beat past the trigger window so the fleer overshoots —
// vanilla's posAvoider paths to a point ~16 blocks off.
func (s *Server) mobFleeStep(e *Entity, kn string) bool {
	rules, ok := mobFlees[kn]
	if !ok {
		return false
	}
	var fx, fz float64
	best := -1.0
	sprint := 1.0
	now := s.gameTime()
	for _, r := range rules {
		d2 := r.dist * r.dist
		switch r.feared {
		case "player":
			s.sessions.Range(func(_, v any) bool {
				p := v.(*Session)
				if p.state.Load() != StatePlay || p.dead ||
					p.mode() == GameModeCreative || p.mode() == GameModeSpectator {
					return true
				}
				if p.dimName() != e.Dim {
					return true
				}
				px, py, pz, _, _, _ := p.Pos()
				dd := (px-e.X)*(px-e.X) + (py-e.Y)*(py-e.Y) + (pz-e.Z)*(pz-e.Z)
				if dd < d2 && (best < 0 || dd < best) {
					best = dd
					fx, fz = px, pz
					sprint = r.sprint
				}
				return true
			})
		default:
			fearedID, fok := EntityTypeID(r.feared)
			for _, o := range s.entities.All() {
				if o.Dim != e.Dim || o.ID == e.ID {
					continue
				}
				if r.feared == "monster" {
					on, _ := EntityTypeName(o.Type)
					if !mobMonsterType[on] {
						continue
					}
				} else if !fok || o.Type != fearedID {
					continue
				}
				dd := (o.X-e.X)*(o.X-e.X) + (o.Y-e.Y)*(o.Y-e.Y) + (o.Z-e.Z)*(o.Z-e.Z)
				if dd < d2 && (best < 0 || dd < best) {
					best = dd
					fx, fz = o.X, o.Z
					sprint = r.sprint
				}
			}
		}
	}
	if best >= 0 {
		e.fleeUntil = now + 40
		e.fleeX, e.fleeZ, e.fleeSprint = fx, fz, sprint
	} else if e.fleeUntil == 0 || now >= e.fleeUntil {
		return false
	}
	dx, dz := e.X-e.fleeX, e.Z-e.fleeZ
	dist := math.Hypot(dx, dz)
	if dist == 0 {
		dx, dz, dist = 1, 0, 1
	}
	speed := mobChaseSpeed
	if mv, ok := entityAttrBases[kn]["movement_speed"]; ok {
		speed = mv
	}
	speed *= e.fleeSprint
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw
	e.X += dx / dist * speed
	e.Z += dz / dist * speed
	return true
}

// mobWaterConverts is Zombie.doUnderWaterConversion's chain — husk
// hops to zombie first (Husk overrides convertToZombieType with
// ZOMBIE), zombies and zombie villagers go straight to drowned. The
// eye must sit in water for 600 ticks before the 300-tick conversion
// starts; once started it finishes even out of water.
var mobWaterConverts = map[string]string{
	"zombie":          "drowned",
	"zombie_villager": "drowned",
	"husk":            "zombie",
}

// mobFreezeConverts is Skeleton.tick's powder-snow chain — 140 ticks
// in snow, then a 300-tick conversion that cancels if the mob steps
// out.
var mobFreezeConverts = map[string]string{"skeleton": "stray"}

// mobConvertTick runs both environment conversions' bookkeeping —
// inside the NoAI gate like vanilla's aiStep/tick placement. Returns
// true when the entity was replaced (the caller must skip it).
func (s *Server) mobConvertTick(e *Entity, d *dimension, ambName string) bool {
	if dst, ok := mobWaterConverts[ambName]; ok {
		if e.WaterConv {
			e.ConversionTime--
			if e.ConversionTime < 0 {
				s.mobConvert(e, dst, 1040)
				return true
			}
		} else {
			eyeH := mobSunEyeHeights[ambName]
			if eyeH == 0 {
				eyeH = 1.74
			}
			eye := world.Pos{
				X: int(math.Floor(e.X)),
				Y: int(math.Floor(e.Y + eyeH)),
				Z: int(math.Floor(e.Z)),
			}
			w := s.dimWorld(e.Dim)
			if w != nil {
				if k, _, ok := fluidOf(w.GetBlock(eye)); ok && k == fluidWater {
					e.inWaterTime++
					if e.inWaterTime >= 600 {
						e.WaterConv = true
						e.ConversionTime = 300
						e.MetaDirty = true
					}
				} else {
					e.inWaterTime = -1
				}
			}
		}
	}
	// AbstractPiglin/Hoglin.customServerAiStep: a zombifiable mob in
	// the overworld (PIGLINS_ZOMBIFY/HOGLINS_ZOMBIFY are set there)
	// ticks TimeInOverworld toward 300, then converts keeping
	// equipment; a non-converting tick resets the counter to 0.
	if conv, ok := mobOverworldZombifies[ambName]; ok {
		if e.Dim == dimOverworld && !e.ImmuneToZombification {
			e.TimeInOverworld++
			if e.TimeInOverworld > 300 {
				if !e.Silent {
					s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, conv.snd, 5, 1, 1, nil)
				}
				if ne := s.mobConvert(e, conv.dst, 0); ne != nil {
					if nid, ok := MobEffectID("nausea"); ok {
						mobAddEffect(ne, nid, 0, s.clock.count+200)
					}
				}
				return true
			}
		} else {
			e.TimeInOverworld = 0
		}
	}
	if dst, ok := mobFreezeConverts[ambName]; ok {
		w := s.dimWorld(e.Dim)
		feet := world.Pos{
			X: int(math.Floor(e.X)),
			Y: int(math.Floor(e.Y)),
			Z: int(math.Floor(e.Z)),
		}
		inSnow := false
		if w != nil {
			bn, _ := blockNameAt(d, feet)
			inSnow = bn == "powder_snow"
		}
		if inSnow {
			if e.FreezeConv {
				e.ConversionTime--
				if e.ConversionTime < 0 {
					s.mobConvert(e, dst, 1048)
					return true
				}
			} else {
				e.inPowderSnowTime++
				if e.inPowderSnowTime >= 140 {
					e.FreezeConv = true
					e.ConversionTime = 300
					e.MetaDirty = true
				}
			}
		} else {
			e.inPowderSnowTime = -1
			if e.FreezeConv {
				e.FreezeConv = false
				e.MetaDirty = true
			}
		}
	}
	return false
}

// witchDrinkStep is Witch.aiStep's potion state machine: while
// drinking, usingTime counts down from the 32-tick use duration and
// the finish applies the potion, empties the mainhand, and lifts the
// SPEED_MODIFIER_DRINKING slowdown; while idle the four rolls run in
// vanilla's order — water-breathing under water (0.15), fire-
// resistance when burning (0.15), healing when hurt (0.05), and
// swiftness when the target stands over eleven blocks out (0.5).
func (s *Server) witchDrinkStep(e *Entity, d *dimension) {
	if e.WitchDrink {
		e.WitchDrinkTime--
		if e.WitchDrinkTime > 0 {
			return
		}
		e.WitchDrink = false
		it := e.Equips[equipMainHand]
		delete(e.Equips, equipMainHand)
		e.markEquipDelta(0)
		if it.Potion != "" {
			if pid, ok := world.ItemID("potion"); !ok || it.ID == pid {
				s.witchApplyPotion(e, it.Potion)
			}
		}
		s.mobAttrModRemove(e, "movement_speed", "minecraft:drinking")
		// Witch.setUsingItem(false): DATA_USING_ITEM (idx 17) clears
		// with the potion — the witch never touches the shared
		// living-flags byte for drinking.
		s.broadcastWitchUsing(e, false)
		return
	}
	var potion string
	switch {
	case rand.Float64() < 0.15 && s.mobEyeInWater(e, d) &&
		!s.mobHasEff(e, "water_breathing"):
		potion = "water_breathing"
	case rand.Float64() < 0.15 &&
		(e.FireTicks > 0 || isFireDamage[e.LastDamageType]) &&
		!s.mobHasEff(e, "fire_resistance"):
		potion = "fire_resistance"
	case rand.Float64() < 0.05 && e.Health < float32(mobAttrSupplierBase("witch", "max_health")):
		potion = "healing"
	case rand.Float64() < 0.5 && s.witchFarTarget(e) &&
		!s.mobHasEff(e, "speed"):
		potion = "swiftness"
	}
	if potion == "" {
		return
	}
	pid, ok := world.ItemID("potion")
	if !ok {
		return
	}
	if e.Equips == nil {
		e.Equips = map[int32]ItemStack{}
	}
	e.Equips[equipMainHand] = ItemStack{ID: pid, Count: 1, Potion: potion}
	e.markEquipDelta(0)
	e.WitchDrink = true
	e.WitchDrinkTime = 32 // ItemStack.getUseDuration for a potion
	// setUsingItem(true) raises the witch's own DATA_USING_ITEM
	// flag at metadata index 17.
	s.broadcastWitchUsing(e, true)
	if !e.Silent {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 1777, mobVoiceOf(e).src(), 1,
			float32(0.8+rand.Float64()*0.4), nil)
	}
	s.mobAttrModAdd(e, "movement_speed", "minecraft:drinking", -0.25, 0)
}

// witchApplyPotion applies the finished potion's contents the way
// PotionContents.forEachEffect does: the timed effects get their
// potion duration, instant_health applies immediately and heals
// four hearts' halves (4 damage points at amplifier 0).
func (s *Server) witchApplyPotion(e *Entity, potion string) {
	switch potion {
	case "water_breathing":
		if id, ok := MobEffectID("water_breathing"); ok {
			mobAddEffect(e, id, 0, s.clock.count+3600)
		}
	case "fire_resistance":
		if id, ok := MobEffectID("fire_resistance"); ok {
			mobAddEffect(e, id, 0, s.clock.count+3600)
		}
	case "healing":
		max := float32(mobAttrSupplierBase("witch", "max_health"))
		if e.MaxHealth > 0 {
			max = e.MaxHealth
		}
		e.Health += 4
		if e.Health > max {
			e.Health = max
		}
		s.broadcastEntityMetadata(e.ID, encodeLivingHealth(e.Health))
	case "swiftness":
		if id, ok := MobEffectID("speed"); ok {
			mobAddEffect(e, id, 0, s.clock.count+3600)
		}
	}
	e.MetaDirty = true
}

// witchFarTarget is the swiftness roll's target test: a target from
// the raider's player acquisition more than eleven blocks away.
func (s *Server) witchFarTarget(e *Entity) bool {
	t := s.nearestMobTarget(e, "witch")
	if t == nil {
		return false
	}
	px, py, pz, _, _, _ := t.Pos()
	dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
	return dx*dx+dy*dy+dz*dz > 121
}

// mobWitchAttackStep runs the witch's RangedAttackGoal(1.0, 60, 10):
// the attackTime countdown starts unset (-1 → 60-tick arm), fires one
// splash potion at zero when the target is inside the ten-block
// radius, and the throw skips while a drink is running
// (performRangedAttack returns early on isDrinkingPotion).
//
// Witch.aiStep ticks NearestHealableRaiderTargetGoal's cooldown first;
// while it runs the player target goal is barred (setCanAttack false).
// Off cooldown, an in-raid witch coin-flips a scan each tick for the
// nearest non-witch Raider inside follow_range 16 — a hit becomes the
// attack target (priority 2 beats the player goal's 3) and starts the
// 200-tick cooldown.
func (s *Server) mobWitchAttackStep(e *Entity) bool {
	e.witchHealCd--
	if rt := e.witchHealTgt; rt != 0 {
		if m := s.entityByID(rt); m != nil && !m.Dying && m.Dim == e.Dim {
			return s.witchRaiderStep(e, m)
		}
		e.witchHealTgt = 0
	}
	if e.witchHealCd <= 0 && e.Raid != nil && e.Raid.status == raidOngoing && rand.Intn(2) == 0 {
		if rt := s.witchHealTarget(e); rt != nil {
			e.witchHealTgt = rt.ID
			e.witchHealCd = 200
			return s.witchRaiderStep(e, rt)
		}
	}
	if e.witchHealCd > 0 {
		return false
	}
	t := s.nearestMobTarget(e, "witch")
	if t == nil {
		return false
	}
	px, py, pz, _, _, _ := t.Pos()
	dx, dz := px-e.X, pz-e.Z
	dist := math.Hypot(dx, dz)
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw
	const radius = 10.0
	if dist > radius {
		step := mobChaseSpeed
		if mv, ok := entityAttrBases["witch"]["movement_speed"]; ok {
			step = mv
		}
		if step > dist-radius {
			step = dist - radius
		}
		e.X += dx / dist * step
		e.Z += dz / dist * step
	}
	e.rangedAttack--
	if e.rangedAttack < 0 {
		e.rangedAttack = 60
	} else if e.rangedAttack == 0 {
		if !e.WitchDrink && dist <= radius {
			s.witchThrow(e, t, px, py, pz, dist)
		}
		e.rangedAttack = 60
	}
	return true
}

// witchHealTarget is the heal goal's findTarget: the nearest living
// non-witch Raider inside the witch's 16-block follow_range. The
// candidate's own raid membership is not required — the selector only
// gates on the witch's hasActiveRaid plus !is(WITCH).
func (s *Server) witchHealTarget(e *Entity) *Entity {
	var best *Entity
	bestD := 16.0 * 16.0
	s.entities.mu.RLock()
	for _, m := range s.entities.byID {
		if m.ID == e.ID || m.Dying || !m.Living || m.Dim != e.Dim {
			continue
		}
		name, _ := EntityTypeName(m.Type)
		if name == "witch" || !raidRaiderType(name) {
			continue
		}
		dx, dy, dz := m.X-e.X, m.Y-e.Y, m.Z-e.Z
		if d := dx*dx + dy*dy + dz*dz; d < bestD {
			best, bestD = m, d
		}
	}
	s.entities.mu.RUnlock()
	return best
}

// witchRaiderStep drives the locked heal target's RangedAttackGoal at
// it — same 60-tick timer and 10-block throw radius as the player
// path; the raider pick is healing below five hearts, regeneration
// otherwise, and the throw clears the target (setTarget(null)).
func (s *Server) witchRaiderStep(e, rt *Entity) bool {
	dx, dz := rt.X-e.X, rt.Z-e.Z
	dist := math.Hypot(dx, dz)
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw
	const radius = 10.0
	if dist > radius {
		step := mobChaseSpeed
		if mv, ok := entityAttrBases["witch"]["movement_speed"]; ok {
			step = mv
		}
		if step > dist-radius {
			step = dist - radius
		}
		e.X += dx / dist * step
		e.Z += dz / dist * step
	}
	e.rangedAttack--
	if e.rangedAttack < 0 {
		e.rangedAttack = 60
	} else if e.rangedAttack == 0 {
		if !e.WitchDrink && dist <= radius {
			potion := "regeneration"
			if rt.Health <= 4 {
				potion = "healing"
			}
			bb := mobBounds(rt.Type)
			s.witchThrowAt(e, rt.X, rt.Y+bb[1]*0.85, rt.Z, dist, potion)
			e.witchHealTgt = 0
		}
		e.rangedAttack = 60
	}
	return true
}

// witchThrow is Witch.performRangedAttack's player branch: the potion
// pick — slowness for a far one, poison for a healthy one, a 25%
// weakness roll up close, harming otherwise.
func (s *Server) witchThrow(e *Entity, t *Session, px, py, pz, dist float64) {
	potion := "harming"
	switch {
	case dist >= 8 && !sessMobHasEff(t, "slowness"):
		potion = "slowness"
	case t.health >= 8 && !sessMobHasEff(t, "poison"):
		potion = "poison"
	case dist <= 3 && !sessMobHasEff(t, "weakness") && rand.Float64() < 0.25:
		potion = "weakness"
	}
	s.witchThrowAt(e, px, py+1.62, pz, dist, potion)
}

// witchThrowAt is the throw half of performRangedAttack: the
// splash_potion spawns a hair below the witch's eye on the vanilla lob
// (dy + horizontal*0.2 past the target eye) at 0.75 blocks/tick
// (0.45 under two blocks).
func (s *Server) witchThrowAt(e *Entity, tx, eyeTy, tz, dist float64, potion string) {
	pt, ok := EntityTypeID("splash_potion")
	if !ok {
		return
	}
	eyeY := e.Y + 1.55 // getEyeHeight - 0.1 like spawnProjectileUsingShoot
	dx, dz := tx-e.X, tz-e.Z
	dy := eyeTy - 1.1 - eyeY
	aimY := dy + dist*0.2
	vel := 0.75
	if dist <= 2 {
		vel = 0.45
	}
	mag := math.Sqrt(dx*dx + aimY*aimY + dz*dz)
	if mag < 1e-6 {
		return
	}
	vx, vy, vz := dx/mag*vel, aimY/mag*vel, dz/mag*vel
	pe := s.entities.Spawn(&Entity{
		Type: pt,
		Dim:  e.Dim,
		X:    e.X, Y: eyeY, Z: e.Z,
		Yaw:     e.Yaw,
		MotionX: vx, MotionY: vy, MotionZ: vz,
		VX: int16(vx * 8000), VY: int16(vy * 8000), VZ: int16(vz * 8000),
		Data: e.ID,
	})
	pe.arrowShooter = e.ID
	pe.splashPotion = potion
	s.broadcastEntitySpawn(nil, *pe)
	if it, ok := world.ItemID("splash_potion"); ok {
		s.broadcastEntityMetadata(pe.ID, encodeItemMetadata(
			ItemStack{ID: it, Count: 1, Potion: potion}))
	}
	if !e.Silent {
		s.playSoundSrc(e.Dim, e.X, eyeY, e.Z, 1779, mobVoiceOf(e).src(), 1,
			float32(0.8+rand.Float64()*0.4), nil)
	}
}

// sessMobHasEff is sessHasEffect by potion-name.
func sessMobHasEff(t *Session, name string) bool {
	id, ok := MobEffectID(name)
	return ok && sessHasEffect(t, id)
}

// mobEyeInWater is isEyeInFluid(FluidTags.WATER): the block at the
// entity's eye height is water. Witch eye height is its standing
// dimensions (1.95) times 0.85.
func (s *Server) mobEyeInWater(e *Entity, d *dimension) bool {
	if d == nil {
		return false
	}
	eye := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y + 1.95*0.85)),
		Z: int(math.Floor(e.Z)),
	}
	k, _, ok := fluidOf(d.w.GetBlock(eye))
	return ok && k == fluidWater
}

// mobHasEff is mobHasEffect by name.
func (s *Server) mobHasEff(e *Entity, name string) bool {
	id, ok := MobEffectID(name)
	return ok && mobHasEffect(s, e, id)
}

// mobAttrModAdd/Remove mirror AttributeInstance's modifier set on the
// entity's pack state; the wire resend rides the next attr sync.
func (s *Server) mobAttrModAdd(e *Entity, attr, id string, value float64, op int) {
	e.markAttrTouched(attr)
	if e.AttrMods == nil {
		e.AttrMods = map[string]map[string]attrMod{}
	}
	if e.AttrMods[attr] == nil {
		e.AttrMods[attr] = map[string]attrMod{}
	}
	e.AttrMods[attr][id] = attrMod{value: value, op: op}
	e.AttrDirty = true
}

func (s *Server) mobAttrModRemove(e *Entity, attr, id string) {
	if e.AttrMods == nil {
		return
	}
	delete(e.AttrMods[attr], id)
	e.AttrDirty = true
}

// mobConvert is Mob.convertTo with ConversionParams.single(mob, true,
// true) — spawn the converted entity first (addFreshEntity), then
// discard the old, keeping equipment, custom name, baby flag, and
// health the target type allows. levelEvent carries the conversion
// sound (0 = none) — suppressed when the mob is silent.
func (s *Server) mobConvert(e *Entity, targetName string, levelEvent int32) *Entity {
	nt, ok := EntityTypeID(targetName)
	if !ok {
		return nil
	}
	ne := &Entity{
		Type: nt,
		Dim:  e.Dim,
		X:    e.X, Y: e.Y, Z: e.Z,
		Yaw: e.Yaw, Pitch: e.Pitch, HeadYaw: e.HeadYaw,
		Health:     e.Health,
		MaxHealth:  e.MaxHealth,
		CustomName: e.CustomName,
		Baby:       e.Baby,
		Silent:     e.Silent,
		NoAI:       e.NoAI,
		Powered:    e.Powered,
	}
	if e.Equips != nil {
		ne.Equips = map[int32]ItemStack{}
		for k, v := range e.Equips {
			ne.Equips[k] = v
		}
	}
	if ne.MaxHealth > 0 && ne.Health > ne.MaxHealth {
		ne.Health = ne.MaxHealth
	}
	s.entities.Spawn(ne)
	s.entities.Remove(e.ID)
	s.broadcastFreshMob(ne)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	if !e.Silent && levelEvent != 0 {
		s.broadcastLevelEvent(e.Dim, levelEvent, world.Pos{
			X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z)),
		}, 0)
	}
	return ne
}

// mobOverworldZombifies maps the overworld-timer mob to its
// conversion target and converted sound event id.
var mobOverworldZombifies = map[string]struct {
	dst string
	snd int32
}{
	"piglin":       {"zombified_piglin", 1289},
	"piglin_brute": {"zombified_piglin", 1295},
	"hoglin":       {"zoglin", 828},
}

// mobSunFlees is FleeSunGoal's type set — only the four
// AbstractSkeleton subclasses register it in 26.2; zombies, drowned,
// and every other #burn_in_daylight mob burns where it stands.
var mobSunFlees = map[string]bool{
	"skeleton":        true,
	"stray":           true,
	"bogged":          true,
	"wither_skeleton": true,
}

// mobSunFleeStep is FleeSunGoal: a burning, bare-headed skeleton
// standing under open sky paths to a shaded block — getHidePos takes
// up to ten random samples in a ±10 XZ / ±3 Y box and the first one
// with no sky view and a standable floor wins. The walk runs at the
// goal's speedModifier 1.0 and the latched shelter holds until the
// path ends, matching canContinueToUse = !navigation.isDone. Returns
// false when no shelter exists — the goal can't start and the mob
// wanders while it burns.
func (s *Server) mobSunFleeStep(e *Entity, d *dimension, w *world.World) bool {
	// canUse's daylight gate is level.isBrightOutside — the same
	// sleep-window that powers sunBurnTick plus a rain veto.
	if !s.isBrightOutside() {
		return false
	}
	if !e.Equips[equipHelmet].Empty() {
		return false
	}
	if e.hideLatched {
		if math.Hypot(e.hideX-e.X, e.hideZ-e.Z) <= 1 {
			e.hideLatched = false
			return true // path done — canContinueToUse ends here
		}
		s.mobWalkTo(e, e.hideX, e.hideZ)
		return true
	}
	feet := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y)),
		Z: int(math.Floor(e.Z)),
	}
	if !s.cropLit(d, feet) {
		return true // already shaded — the goal holds position
	}
	for i := 0; i < 10; i++ {
		cand := world.Pos{
			X: feet.X + int(rand.Int31n(20)) - 10,
			Y: feet.Y + int(rand.Int31n(6)) - 3,
			Z: feet.Z + int(rand.Int31n(20)) - 10,
		}
		if s.cropLit(d, cand) {
			continue
		}
		if world.NonSolid(w.GetBlock(cand)) && world.NonSolid(w.GetBlock(world.Pos{X: cand.X, Y: cand.Y + 1, Z: cand.Z})) && !world.NonSolid(w.GetBlock(world.Pos{X: cand.X, Y: cand.Y - 1, Z: cand.Z})) {
			e.hideLatched = true
			e.hideX = float64(cand.X) + 0.5
			e.hideY = float64(cand.Y)
			e.hideZ = float64(cand.Z) + 0.5
			s.mobWalkTo(e, e.hideX, e.hideZ)
			return true
		}
	}
	return false
}

// mobWalkTo steps the mob one walk-speed tick toward (x,z), facing
// it — the shared PathNavigation shim for goals with a point target.
func (s *Server) mobWalkTo(e *Entity, x, z float64) {
	kn, _ := EntityTypeName(e.Type)
	speed := mobChaseSpeed
	if mv, ok := entityAttrBases[kn]["movement_speed"]; ok {
		speed = mv
	}
	s.mobWalkToSpeed(e, x, z, speed)
}

// mobWalkToSpeed is mobWalkTo with a caller-picked stride — goals whose
// speedModifier scales movement_speed (patrol's 0.595/0.7) step at that
// product instead of the raw attribute.
func (s *Server) mobWalkToSpeed(e *Entity, x, z, speed float64) {
	dx, dz := x-e.X, z-e.Z
	dist := math.Hypot(dx, dz)
	if dist == 0 {
		return
	}
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw
	if dist <= speed {
		e.X, e.Z = x, z
		return
	}
	e.X += dx / dist * speed
	e.Z += dz / dist * speed
}

// mobSwell is the SwellGoal set — creepers only.
var mobSwell = map[string]bool{"creeper": true}

// mobSwellStep is the SwellGoal half of the creeper pair. Vanilla arms
// the fuse only inside 3 blocks (canUse: dist²<9), keeps it lit while
// the target stays within 7 (tick defuses beyond 49), and claims the
// MOVE flag the whole time — navigation.stop() freezes the creeper in
// place as it hisses. A defused creeper inside the 3-block arm radius
// stays frozen too (canUse re-triggers until the target leaves). The
// fuse counter itself is tick bookkeeping, run for every creeper
// before the NoAI gate; flat-world LOS is always clear.
func (s *Server) mobSwellStep(e *Entity, kn string) bool {
	target := s.nearestMobTarget(e, kn)
	var px, py, pz float64
	d2 := math.MaxFloat64
	if target != nil {
		px, py, pz, _, _, _ = target.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		d2 = dx*dx + dy*dy + dz*dz
	}
	if !e.Ignited {
		switch {
		case target == nil || d2 > 49:
			e.SwellDir = -1
		case e.SwellDir > 0 || d2 < 9:
			e.SwellDir = 1
		}
		e.MetaDirty = true
	}
	if e.SwellDir > 0 || d2 < 9 {
		// SwellGoal is in use (swelling, or primed inside 3 blocks):
		// the MOVE flag is claimed so the creeper stands its ground
		// and just faces the target — LookAtPlayerGoal.
		if target != nil {
			dx, dz := px-e.X, pz-e.Z
			e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
			e.HeadYaw = e.Yaw
		}
		return true
	}
	if target == nil {
		return false
	}
	// Fuse cold and target outside the arm radius — MeleeAttackGoal
	// pathing walks the creeper in (they never swing).
	dx, dz := px-e.X, pz-e.Z
	dist := math.Hypot(dx, dz)
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw
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
	return true
}

// explodeCreeper is Creeper.explodeCreeper: explosionRadius (NBT,
// default 3) doubled when powered, centered on the feet, MOB
// interaction — then discard.
func (s *Server) explodeCreeper(e *Entity) {
	power := float32(3)
	if e.BlastPower > 0 {
		power = float32(e.BlastPower)
	}
	if e.Powered {
		power *= 2
	}
	s.explodePower(e, e.X, e.Y, e.Z, power, s.gameRuleBool("minecraft:mob_griefing"))
	s.spawnLingeringCloud(e)
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
}

// spawnLingeringCloud is Creeper.explodeCreeper's effect rider: a
// creeper carrying active potion effects leaves an AreaEffectCloud at
// the blast point — radius 2.5 shrinking to zero over the 300-tick
// duration, 10-tick wait, potionDurationScale 0.25, and a copy of
// each active effect.
func (s *Server) spawnLingeringCloud(e *Entity) {
	cloudType, ok := EntityTypeID("area_effect_cloud")
	if !ok {
		return
	}
	effects := make(map[int32]mobEffectInst, len(e.Effects))
	for id, inst := range e.Effects {
		effects[id] = inst
	}
	cloud := s.entities.Spawn(&Entity{
		Type:               cloudType,
		Dim:                e.Dim,
		X:                  e.X,
		Y:                  e.Y,
		Z:                  e.Z,
		CloudWait:          10,
		CloudDuration:      300,
		CloudRadius:        2.5,
		CloudRadiusPerTick: -2.5 / 300,
		CloudRadiusOnUse:   -0.5,
		CloudVictims:       map[int32]int32{},
		Effects:            effects,
		// explodeCreeper's cloud.setOwner(this) — the creeper is a
		// LivingEntity, so it lands as the dose's indirectMagic cause.
		CloudOwnerID: e.ID,
	})
	s.broadcastEntitySpawn(nil, *cloud)
	if meta := encodeCloudMeta(cloud); len(meta) > 0 {
		s.broadcastEntityMetadata(cloud.ID, meta)
	}
}

// chargedCreeperHeads is the CHARGED_CREEPER loot table's victim→head
// map (minecraft:charged_creeper/root.json, 26.2 datapack).
var chargedCreeperHeads = map[string]string{
	"creeper":         "creeper_head",
	"skeleton":        "skeleton_skull",
	"zombie":          "zombie_head",
	"piglin":          "piglin_head",
	"wither_skeleton": "wither_skeleton_skull",
}

// chargedCreeperSkull is Creeper.killedEntity's rider: a powered
// creeper whose explosion kills a mapped victim drops that victim's
// head at the victim's position, once per creeper (droppedSkulls).
// shouldDropLoot — the creeper's own !baby && doMobLoot — gates it.
func (s *Server) chargedCreeperSkull(src, victim *Entity) {
	cn, ok := EntityTypeName(src.Type)
	if !ok || cn != "creeper" || !src.Powered || src.DroppedSkulls {
		return
	}
	if src.Baby || !s.gameRuleBool("minecraft:mob_drops") {
		return
	}
	vn, ok := EntityTypeName(victim.Type)
	if !ok {
		return
	}
	head, ok := chargedCreeperHeads[vn]
	if !ok {
		return
	}
	s.spawnMobItem(victim, head, 1)
	src.DroppedSkulls = true
}

// cloudTickStep is AreaEffectCloud.serverTick: the entity discards
// when its age passes wait+duration, waits out waitTime, then shrinks
// by radiusPerTick every tick. Every five ticks it doses living
// entities whose XZ distance sits inside the radius — the victim
// cooldown (reapplicationDelay 20) and the radiusOnUse shrink apply
// per dose; the potion contents run at potionDurationScale 0.25.
func (s *Server) cloudTickStep() {
	cloudType, cok := EntityTypeID("area_effect_cloud")
	if !cok {
		return
	}
	for _, e := range s.entities.All() {
		if e.Type != cloudType || e.CloudDuration == 0 {
			continue
		}
		ep, ok := s.entities.GetPtr(e.ID)
		if !ok {
			continue
		}
		discard := func() {
			s.entities.Remove(ep.ID)
			s.broadcastEntityRemoveIn(ep.Dim, nil, []int32{ep.ID})
		}
		ep.CloudAge++
		// Vanilla's duration default is -1 — an infinite cloud never
		// ages out (tickCount - waitTime >= duration only when set).
		if ep.CloudDuration >= 0 && ep.CloudAge-ep.CloudWait >= ep.CloudDuration {
			discard()
			continue
		}
		// DATA_WAITING (idx 9) flips on for the wait window and back
		// off the first active tick — each transition is a dirty pack.
		waiting := ep.CloudAge < ep.CloudWait
		if waiting != ep.CloudWaiting {
			ep.CloudWaiting = waiting
			s.broadcastEntityMetadata(ep.ID, encodeCloudWaiting(waiting))
		}
		if waiting {
			continue
		}
		ep.CloudRadius += ep.CloudRadiusPerTick
		if ep.CloudRadius < 0.5 {
			discard()
			continue
		}
		if ep.CloudAge%5 != 0 {
			continue
		}
		for id, until := range ep.CloudVictims {
			if ep.CloudAge >= until {
				delete(ep.CloudVictims, id)
			}
		}
		if len(ep.Effects) == 0 {
			continue
		}
		r2 := float64(ep.CloudRadius) * float64(ep.CloudRadius)
		now := s.gameTime()
		dose := func(mobOrSess *Entity) bool {
			dx, dz := mobOrSess.X-ep.X, mobOrSess.Z-ep.Z
			if dx*dx+dz*dz > r2 {
				return true
			}
			ep.CloudVictims[mobOrSess.ID] = ep.CloudAge + 20
			// The dose hurts through damageSources.indirectMagic(cloud,
			// owner): indirect_magic with the cloud as direct entity and
			// the spawning owner as cause (0 for ownerless clouds).
			src := &damageInfo{typeName: "indirect_magic", cause: ep.CloudOwnerID, direct: ep.ID}
			for id, inst := range ep.Effects {
				ticks := int32(-1)
				if inst.expiry >= 0 {
					ticks = int32((inst.expiry - now) / 4) // 0.25 scale
				}
				s.mobGiveEffect(mobOrSess, id, inst.amplifier, ticks, inst.hide, src)
			}
			ep.CloudRadius += ep.CloudRadiusOnUse
			return ep.CloudRadius >= 0.5
		}
		dead := false
		for _, v := range s.entities.All() {
			if !v.Living || v.Dying || v.Dim != ep.Dim || v.Type == cloudType {
				continue
			}
			if _, onCd := ep.CloudVictims[v.ID]; onCd {
				continue
			}
			vp, ok := s.entities.GetPtr(v.ID)
			if !ok {
				continue
			}
			if !dose(vp) {
				dead = true
				break
			}
		}
		if dead || ep.CloudRadius < 0.5 {
			discard()
			continue
		}
		s.sessions.Range(func(_, vs any) bool {
			sess := vs.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != ep.Dim || sess.mode() == GameModeSpectator || sess.dead {
				return true
			}
			eid := sess.entityID.Load()
			if _, onCd := ep.CloudVictims[eid]; onCd {
				return true
			}
			px, _, pz, _, _, _ := sess.Pos()
			dx, dz := px-ep.X, pz-ep.Z
			if dx*dx+dz*dz > r2 {
				return true
			}
			ep.CloudVictims[eid] = ep.CloudAge + 20
			psrc := damageSource{typeName: "indirect_magic", cause: ep.CloudOwnerID, direct: ep.ID}
			for id, inst := range ep.Effects {
				ticks := int32(-1)
				if inst.expiry >= 0 {
					ticks = int32((inst.expiry - now) / 4)
				}
				s.playerGiveEffect(sess, id, inst.amplifier, ticks, inst.hide, psrc)
			}
			ep.CloudRadius += ep.CloudRadiusOnUse
			return ep.CloudRadius >= 0.5
		})
		if ep.CloudRadius < 0.5 {
			discard()
		}
	}
}

// mobHoldsBow is vanilla's isHoldingBow check on the main-hand stack.
func (s *Server) mobHoldsBow(e *Entity) bool {
	bow, ok := world.ItemID("bow")
	return ok && e.Equips[equipMainHand].ID == bow
}

// mobArmorMaterials is getEquipmentForSlot's six-tier ladder: the
// rolled tier index picks one material used for every armor slot.
var mobArmorMaterials = []string{"leather", "copper", "golden", "chainmail", "iron", "diamond"}

var mobArmorSuffix = map[int32]string{
	equipHelmet: "helmet", equipChest: "chestplate",
	equipLegs: "leggings", equipBoots: "boots",
}

// specialMultiplier is DifficultyInstance.getSpecialMultiplier: the
// effective difficulty is id·(0.75 + day + inhabited·k + moon) with
// day = clamp((dayTime−72000)/1440000)·0.25 and the moon term capped
// by the day term; below 2 the multiplier is 0 (fresh flat worlds
// never roll gear), above 4 it saturates at 1. mc-go tracks no
// per-chunk inhabited time, so that term stays 0.
// effectiveDifficulty is DifficultyInstance.getEffectiveDifficulty —
// difficulty * (0.75 + dayfactor + moonclamp). (Vanilla also blends a
// per-chunk inhabited-time term; mc-go's flat world doesn't track
// inhabited time yet.)
func (s *Server) effectiveDifficulty() float64 {
	if s.difficulty == DifficultyPeaceful {
		return 0
	}
	day := float64(s.dayTime())
	g := math.Min(1, math.Max(0, (day-72000)/1440000)) * 0.25
	moon := [...]float64{1, 0.75, 0.5, 0.25, 0, 0.25, 0.5, 0.75}[int(s.dayTime()/24000/8)%8]
	h := math.Min(math.Max(moon*0.25, 0), g)
	if s.difficulty == DifficultyEasy {
		h *= 0.5
	}
	return float64(s.difficulty) * (0.75 + g + h)
}

func (s *Server) specialMultiplier() float64 {
	eff := s.effectiveDifficulty()
	switch {
	case eff < 2:
		return 0
	case eff > 4:
		return 1
	default:
		return (eff - 2) / 2
	}
}

// mobEquipSlot sets a slot to a stack when it is still empty —
// setItemSlot in populate paths never overwrites NBT-placed gear.
func mobEquipSlot(e *Entity, slot int32, item string) {
	iid, ok := world.ItemID(item)
	if !ok {
		return
	}
	if e.Equips == nil {
		e.Equips = map[int32]ItemStack{}
	}
	if e.Equips[slot].Empty() {
		e.Equips[slot] = ItemStack{ID: iid, Count: 1}
	}
}

// mobDefaultEquip is Mob.finalizeSpawn's equipment tail:
// populateDefaultEquipmentSlots then populateDefaultEquipment-
// Enchantments. Per-type layouts measured on 26.2:
//
//   - zombie/husk/zombie_villager/skeleton/stray/bogged run the
//     generic armor cascade — a 0.15·special gate, a tier roll
//     (0-2 plus two 10.87% upgrades), then HEAD→CHEST→LEGS→FEET
//     each continuing with 1−0.1(hard)|0.25 — before the family's
//     own mainhand draw (skeleton bow, zombie none, drowned skips
//     the cascade entirely and rolls trident/rod instead).
//   - drowned: 10% weapon (10/16 trident else fishing rod), 3%
//     offhand nautilus shell, never armor.
//   - piglin: adults only — each armor slot a 10% golden piece and
//     a mainhand of crossbow 50% / golden spear 1-in-10 of the rest /
//     golden sword.
//   - pillager/vindicator/piglin_brute/wither_skeleton: fixed weapon
//     only, no armor cascade.
//
// Enchantment rolls ride the same special multiplier (mainhand
// 0.25·special, each armor piece 0.5·special, cost 5+rand(0,
// special·17) through the on_mob_spawn_equipment table); on a fresh
// world special is 0 and nothing enchants. Pillagers add a flat
// 1/300 piercing-1 crossbow upgrade.
func (s *Server) mobDefaultEquip(e *Entity) {
	s.mobDefaultEquipReason(e, spawnDefault)
}

// mobDefaultEquipOpt is kept for callers that only distinguish raid
// spawns: EVENT maps to vanilla's EntitySpawnReason.EVENT.
func (s *Server) mobDefaultEquipOpt(e *Entity, eventSpawn bool) {
	if eventSpawn {
		s.mobDefaultEquipReason(e, spawnEvent)
		return
	}
	s.mobDefaultEquipReason(e, spawnDefault)
}

// spawnKind mirrors the EntitySpawnReason values that change
// PatrollingMonster.finalizeSpawn: the 6% patrol-leader roll runs on
// every reason EXCEPT patrol/event/structure, the leader's head takes
// the ominous banner whenever the flag is already set, and PATROL
// additionally marks the mob patrolling.
type spawnKind byte

const (
	spawnDefault spawnKind = iota // command/natural path — the roll applies
	spawnEvent                    // raid waves: no roll
	spawnPatrol                   // PatrolSpawner: no roll, patrolling=true
)

// mobDefaultEquipReason is mobDefaultEquip with vanilla's spawn-reason
// split threaded through.
func (s *Server) mobDefaultEquipReason(e *Entity, reason spawnKind) {
	name, _ := EntityTypeName(e.Type)
	special := s.specialMultiplier()

	cascade := false
	switch name {
	case "zombie", "husk", "zombie_villager", "zombified_piglin",
		"skeleton", "stray", "bogged":
		cascade = true
	}
	// PatrollingMonster.finalizeSpawn: when the spawn reason is not
	// patrol/event/structure a leader-capable type rolls 6% into a
	// patrol leader; witch and ravager canBeLeader()=false.
	if reason == spawnDefault {
		switch name {
		case "pillager", "vindicator", "evoker", "illusioner":
			if rand.Float64() < 0.06 {
				e.PatrolLeader = true
			}
		}
	}
	// isPatrolLeader() → head takes the ominous banner with its 2.0
	// drop chance — this runs for forced leaders too (PatrolSpawner's
	// first member, raid setLeader), not only the roll's winners.
	if e.PatrolLeader {
		if wb, ok := world.ItemID("white_banner"); ok {
			if e.Equips == nil {
				e.Equips = map[int32]ItemStack{}
			}
			e.Equips[equipHelmet] = ItemStack{ID: wb, Count: 1, Ominous: true}
		}
	}
	if reason == spawnPatrol {
		e.Patrolling = true
	}

	if cascade && rand.Float64() < 0.15*special {
		tier := rand.Intn(3)
		for i := 0; i < 2 && rand.Float64() < 0.1087; i++ {
			tier++
		}
		stop := 0.25
		if s.difficulty == DifficultyHard {
			stop = 0.1
		}
		first := true
		for _, slot := range []int32{equipHelmet, equipChest, equipLegs, equipBoots} {
			if !first && rand.Float64() < stop {
				break
			}
			first = false
			if tier <= 5 {
				mobEquipSlot(e, slot, mobArmorMaterials[tier]+"_"+mobArmorSuffix[slot])
			}
		}
	}

	switch name {
	case "zombie", "husk", "zombie_villager":
		// Zombie.populateDefaultEquipmentSlots adds a rare weapon:
		// 5% on hard, 1% otherwise, then nextInt(6) picks sword /
		// spear / shovel (4/6). Drowned overrides the whole method.
		weaponChance := 0.01
		if s.difficulty == DifficultyHard {
			weaponChance = 0.05
		}
		if rand.Float64() < weaponChance {
			switch rand.Intn(6) {
			case 0:
				mobEquipSlot(e, equipMainHand, "iron_sword")
			case 1:
				mobEquipSlot(e, equipMainHand, "iron_spear")
			default:
				mobEquipSlot(e, equipMainHand, "iron_shovel")
			}
		}
	case "skeleton", "stray", "bogged":
		mobEquipSlot(e, equipMainHand, "bow")
	case "zombified_piglin":
		if rand.Intn(20) == 0 {
			mobEquipSlot(e, equipMainHand, "golden_spear")
		} else {
			mobEquipSlot(e, equipMainHand, "golden_sword")
		}
	case "wither_skeleton":
		mobEquipSlot(e, equipMainHand, "stone_sword")
	case "pillager":
		mobEquipSlot(e, equipMainHand, "crossbow")
	case "piglin_brute":
		mobEquipSlot(e, equipMainHand, "golden_axe")
	case "vindicator":
		mobEquipSlot(e, equipMainHand, "iron_axe")
	case "drowned":
		if rand.Float64() > 0.9 {
			if rand.Intn(16) < 10 {
				mobEquipSlot(e, equipMainHand, "trident")
			} else {
				mobEquipSlot(e, equipMainHand, "fishing_rod")
			}
		}
		if rand.Float64() < 0.03 {
			mobEquipSlot(e, equipOffHand, "nautilus_shell")
		}
	case "piglin":
		// Piglin.finalizeSpawn (reason != STRUCTURE): 20% baby roll
		// first; only when the roll misses and the piglin is an adult
		// does the weapon land. Armor populate is adult-gated too.
		if rand.Float64() < 0.2 {
			e.Baby = true
		} else if !e.Baby {
			if rand.Float64() < 0.5 {
				mobEquipSlot(e, equipMainHand, "crossbow")
			} else if rand.Intn(10) == 0 {
				mobEquipSlot(e, equipMainHand, "golden_spear")
			} else {
				mobEquipSlot(e, equipMainHand, "golden_sword")
			}
		}
		if !e.Baby {
			for slot, piece := range map[int32]string{
				equipHelmet: "golden_helmet", equipChest: "golden_chestplate",
				equipLegs: "golden_leggings", equipBoots: "golden_boots",
			} {
				if rand.Float64() < 0.1 {
					mobEquipSlot(e, slot, piece)
				}
			}
		}
	}

	// populateDefaultEquipmentEnchantments: mainhand 0.25·special,
	// each armor slot 0.5·special, cost 5+rand(0, special·17)
	// through the on_mob_spawn_equipment table.
	cost := func() int {
		return 5 + rand.Intn(1+int(special*17))
	}
	if st := e.Equips[equipMainHand]; !st.Empty() && rand.Float64() < 0.25*special {
		e.Equips[equipMainHand] = enchantApply(st, selectEnchantment(rand.New(rand.NewSource(rand.Int63())), st, cost()))
	}
	for _, slot := range []int32{equipHelmet, equipChest, equipLegs, equipBoots} {
		if st := e.Equips[slot]; !st.Empty() && rand.Float64() < 0.5*special {
			e.Equips[slot] = enchantApply(st, selectEnchantment(rand.New(rand.NewSource(rand.Int63())), st, cost()))
		}
	}
	// Pillager.enchantSpawnedWeapon adds a 1/300 piercing-1 crossbow.
	if name == "pillager" {
		if st := e.Equips[equipMainHand]; !st.Empty() && rand.Intn(300) == 0 {
			cb, _ := world.ItemID("crossbow")
			if st.ID == cb {
				e.Equips[equipMainHand] = enchantApply(st, []enchantInstance{{"piercing", 1}})
			}
		}
	}
}

// mobRangedStep runs RangedBowAttackGoal's tick: approach while the
// target is outside the 15-block attack radius or unseen 20 ticks,
// then navigation.stop and strafe; a draw pulls for 20 ticks and
// releases one arrow, and the next draw waits the attack interval —
// 40 ticks on normal (AbstractSkeleton.getAttackInterval), 20 hard.
func (s *Server) mobRangedStep(e *Entity) bool {
	kn, _ := EntityTypeName(e.Type)
	target := s.nearestMobTarget(e, kn)
	if target == nil {
		return false
	}
	px, py, pz, _, _, _ := target.Pos()
	dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
	d2 := dx*dx + dy*dy + dz*dz
	// hasLineOfSight on the flat harness world is always clear.
	los := true
	if los != (e.rangedSee > 0) {
		e.rangedSee = 0
	}
	if los {
		e.rangedSee++
	} else {
		e.rangedSee--
	}
	dist := math.Hypot(dx, dz)
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw
	const radius2 = 15.0 * 15.0
	speed := mobChaseSpeed
	if mv, ok := entityAttrBases[kn]["movement_speed"]; ok {
		speed = mv
	}
	if d2 <= radius2 && e.rangedSee >= 20 {
		e.rangedStrafe++
	} else if dist > 0 {
		step := speed
		if step > dist*0.8 {
			step = dist * 0.8
		}
		e.X += dx / dist * step
		e.Z += dz / dist * step
		e.rangedStrafe = -1
	}
	if e.rangedStrafe >= 20 {
		if rand.Float32() < 0.3 {
			e.rangedCW = !e.rangedCW
		}
		if rand.Float32() < 0.3 {
			e.rangedBack = !e.rangedBack
		}
		e.rangedStrafe = 0
	}
	if e.rangedStrafe > -1 && dist > 0 {
		if d2 > radius2*0.75 {
			e.rangedBack = false
		}
		if d2 < radius2*0.25 {
			e.rangedBack = true
		}
		nx, nz := dx/dist, dz/dist
		sgn := 1.0
		if !e.rangedCW {
			sgn = -1
		}
		// moveControl.strafe(±0.5 sideways, ±0.5 forward) — the
		// diagonal input normalizes to a speed-length step.
		f := 0.5
		if e.rangedBack {
			f = -0.5
		}
		vx := -nz*sgn*0.5 + nx*f
		vz := nx*sgn*0.5 + nz*f
		if m := math.Hypot(vx, vz); m > 0 {
			e.X += vx / m * speed
			e.Z += vz / m * speed
		}
	}
	if e.rangedDraw > 0 {
		e.rangedDraw++
		if e.rangedDraw > 20 && los {
			e.rangedDraw = 0
			s.broadcastLivingUse(e, false)
			s.mobShootArrow(e, px, py, pz)
			e.rangedAttack = s.rangedAttackInterval()
		}
		return true
	}
	e.rangedAttack--
	if e.rangedAttack <= 0 && e.rangedSee >= -60 {
		e.rangedDraw = 1
		s.broadcastLivingUse(e, true)
	}
	return true
}

// rangedAttackInterval is AbstractSkeleton's goal interval: 20 ticks
// on hard difficulty, 40 otherwise.
func (s *Server) rangedAttackInterval() int32 {
	if s.difficulty == DifficultyHard {
		return 20
	}
	return 40
}

// broadcastLivingUse writes the LivingEntity index-8 byte — bit 0x01
// "hand active" sets while a bow draw runs, cleared on release.
func (s *Server) broadcastLivingUse(e *Entity, active bool) {
	var v byte
	if active {
		v = 0x01
	}
	var b protocol.Buffer
	b.Byte(8) // index: living entity flags
	b.Byte(0) // type: byte
	b.Byte(v)
	b.Byte(0xFF)
	s.broadcastEntityMetadata(e.ID, b.Bytes())
}

// broadcastWitchUsing emits the witch's DATA_USING_ITEM — metadata
// index 17, boolean serializer — which vanilla toggles around the
// 32-tick potion drink.
func (s *Server) broadcastWitchUsing(e *Entity, on bool) {
	var v byte
	if on {
		v = 1
	}
	var b protocol.Buffer
	b.Byte(17)
	b.Byte(8) // boolean serializer
	b.Byte(v)
	b.Byte(0xFF)
	s.broadcastEntityMetadata(e.ID, b.Bytes())
}

// mobShootArrow is AbstractSkeleton.shootArrow's release: the arrow
// spawns a hair below the mob's eye, aims at the target's lower third
// (getY(0.333…) ≈ feet+0.6 for a player) plus the +0.2 per horizontal
// block gravity compensation, at 1.6 blocks/tick — a full 20-tick
// pull gives getPowerForTime 1.0.
func (s *Server) mobShootArrow(e *Entity, tx, ty, tz float64) {
	arrowID, ok := EntityTypeID("arrow")
	if !ok {
		return
	}
	eyeY := e.Y + 1.64
	dx, dz := tx-e.X, tz-e.Z
	dy := ty + 0.6 - eyeY
	vy := dy + math.Hypot(dx, dz)*0.2
	mag := math.Sqrt(dx*dx + vy*vy + dz*dz)
	if mag < 1e-6 {
		return
	}
	const spd = 1.6
	vx, vyN, vz := dx/mag*spd, vy/mag*spd, dz/mag*spd
	ae := s.entities.Spawn(&Entity{
		Type:    arrowID,
		Dim:     e.Dim,
		X:       e.X,
		Y:       eyeY,
		Z:       e.Z,
		Yaw:     float32(math.Atan2(-dx, dz) * 180 / math.Pi),
		Pitch:   float32(math.Asin(-vy/mag) * 180 / math.Pi),
		MotionX: vx, MotionY: vyN, MotionZ: vz,
		VX: int16(vx * 8000), VY: int16(vyN * 8000), VZ: int16(vz * 8000),
		Data: e.ID,
	})
	ae.arrowShooter = e.ID
	ae.arrowWeapon = "bow"
	// Stray/Bogged getArrow each addEffect on the fresh arrow —
	// slowness 600 / poison 100 ticks, applied at full strength on
	// hit (mob arrows carry no potion_duration_scale).
	kn, _ := EntityTypeName(e.Type)
	switch kn {
	case "stray":
		if id, ok := MobEffectID("slowness"); ok {
			ae.arrowEffects = []arrowEffect{{id, 600}}
		}
	case "bogged":
		if id, ok := MobEffectID("poison"); ok {
			ae.arrowEffects = []arrowEffect{{id, 100}}
		}
	}
	s.broadcastEntitySpawn(nil, *ae)
	if len(ae.arrowEffects) > 0 {
		// Arrow.DATA_ID_EFFECT_COLOR (idx 9, int): the customEffects
		// blend — vanilla sends it with the first sync.
		var mb protocol.Buffer
		mb.VarInt(ae.ID)
		mb.Byte(9)
		mb.Byte(1) // type: int (varint)
		mb.VarInt(arrowEffectColor(ae.arrowEffects))
		s.broadcastEntityMetadata(ae.ID, mb.Bytes())
	}
	// entity.skeleton.shoot — the bow-release sound the mob plays,
	// source HOSTILE (SoundSource 5).
	s.playSoundSrc(e.Dim, e.X, eyeY, e.Z, 1491, 5, 1, 1, nil)
}

// arrowEffectColor is PotionContents.getColor — with a single custom
// effect it's the effect's own mob-effect color.
var arrowEffectColors = map[int32]int32{
	// slowness 9154528, poison 8889187 — MobEffects constants.
	1: 9154528, 18: 8889187,
}

func arrowEffectColor(effs []arrowEffect) int32 {
	for _, e := range effs {
		if c, ok := arrowEffectColors[e.id]; ok {
			return c
		}
	}
	return -1
}

// mobPostAttack is the post-damage tail of vanilla's doHurtTarget
// overrides — the innate per-type rider that runs when the hit landed
// (excess-inside-iframe hits count: hurtServer's flag is still true).
func (s *Server) mobPostAttack(e *Entity, name string, target *Session) {
	switch name {
	case "husk":
		// Husk.doHurtTarget: bare hands only — hunger for
		// round(effectiveDifficulty * 140) ticks. The flat harness
		// world's regional factor resolves the effective difficulty
		// to half the difficulty id (140 ticks at normal).
		dur := int32(math.Round(float64(s.difficulty) * 0.5 * 140))
		if dur > 0 {
			id, _ := MobEffectID("hunger")
			s.playerGiveEffect(target, id, 0, dur, false)
		}
	case "cave_spider":
		// CaveSpider.doHurtTarget: poison only on normal+ —
		// 7 seconds at NORMAL, 15 at HARD.
		var dur int32
		switch s.difficulty {
		case DifficultyNormal:
			dur = 140
		case DifficultyHard:
			dur = 300
		}
		if dur > 0 {
			id, _ := MobEffectID("poison")
			s.playerGiveEffect(target, id, 0, dur, false)
		}
	case "iron_golem":
		// IronGolem.doHurtTarget: attackAnimationTick=10 + the
		// entity-event-4 broadcast, then dv.y += 0.4 * (1 - kbr) — a
		// launch, not a push. Player knockback_resistance is 0.
		s.broadcastEntityEvent(e.ID, 4)
		s.broadcastEntityMotion(target.dimName(), target.entityID.Load(), 0, 0.4, 0)
	}
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
	// playHurtSound's first act is resetAmbientSoundTime — an ambient
	// voice can't overlap the hurt bark.
	if a, ok := mobAmbient[func() string { n, _ := EntityTypeName(e.Type); return n }()]; ok {
		e.ambientSoundTime = -a.interval
	}
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
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, death, v.src(), v.vol, voicePitchE(e), nil)
	}
	if v.secondDeath != 0 {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, v.secondDeath, v.src(), v.vol, voicePitchE(e), nil)
	}
}

// ageableTypes is vanilla 26.2's AgeableMob leaf set — every type
// whose readAdditionalSaveData honors Age (Animal, AbstractVillager,
// AgeableWaterCreature, cubemobs, nautiluses). Babies of these types
// count Age up toward 0 and grow up on arrival.
var ageableTypes = map[string]bool{
	"armadillo": true, "axolotl": true, "bee": true, "camel": true,
	"camel_husk": true, "chicken": true, "cow": true, "mooshroom": true,
	"dolphin": true, "donkey": true, "horse": true, "llama": true,
	"mule": true, "skeleton_horse": true, "trader_llama": true,
	"zombie_horse": true, "cat": true, "ocelot": true, "fox": true,
	"frog": true, "goat": true, "happy_ghast": true, "nautilus": true,
	"zombie_nautilus": true, "panda": true, "parrot": true, "pig": true,
	"polar_bear": true, "rabbit": true, "sheep": true, "sniffer": true,
	"glow_squid": true, "squid": true, "turtle": true, "wolf": true,
	"strider": true, "magma_cube": true, "slime": true,
	"sulfur_cube": true, "hoglin": true, "villager": true,
	"wandering_trader": true,
}

// isBabyTypes is the set of types whose readAdditionalSaveData honors
// the IsBaby key on 26.2 — the Zombie and AbstractPiglin hierarchies
// plus Zoglin. AgeableMob types (and zombie_nautilus, an animal) read
// Age instead.
var isBabyTypes = map[string]bool{
	"zombie":          true,
	"zombie_villager": true,
	"drowned":         true,
	"husk":            true,
	"piglin":          true,
	"piglin_brute":    true,
	"zoglin":          true,
}

// burnDaylight is vanilla 26.2's #minecraft:burn_in_daylight entity
// tag — husk is deliberately absent (desert zombies never burn).
var burnDaylight = map[string]bool{
	"skeleton":        true,
	"stray":           true,
	"wither_skeleton": true,
	"bogged":          true,
	"zombie":          true,
	"zombie_horse":    true,
	"zombie_villager": true,
	"drowned":         true,
	"zombie_nautilus": true,
	"phantom":         true,
}

// mobSunEyeHeights is the eye height per #burn_in_daylight type —
// vanilla's canSeeSky check reads the block containing the eye, not
// the feet. Values are the type's EntityDimensions eye height where
// it differs from height*0.85.
var mobSunEyeHeights = map[string]float64{
	"zombie":          1.74,
	"zombie_villager": 1.74,
	"drowned":         1.74,
	"skeleton":        1.74,
	"stray":           1.74,
	"bogged":          1.74,
	"wither_skeleton": 2.1,
	"zombie_horse":    1.52 * 0.85,
	"zombie_nautilus": 1.0,
	"phantom":         0.175,
}

// mobSunBurn is Mob.burnUndead: when a sun tick rolls true a bare
// head ignites for eight seconds — igniteForSeconds keeps the larger
// of the remaining and new countdowns — while a helmet absorbs the
// burn at nextInt(2) durability per tick and eventually snaps with
// the HEAD break event (49 on 26.2's entityEventForEquipmentBreak).
func (s *Server) mobSunBurn(e *Entity, name string) {
	if !s.sunBurnTick(e, name) {
		return
	}
	helm := e.Equips[equipHelmet]
	if !helm.Empty() {
		if name, ok := itemNameOf(helm.ID); ok {
			if maxDmg, dmgOK := damageableItems[name]; dmgOK {
				helm.Damage += rand.Int31n(2)
				if helm.Damage >= maxDmg {
					delete(e.Equips, equipHelmet)
					e.EquipDirty = true
					s.broadcastEntityEvent(e.ID, 49)
				} else {
					e.Equips[equipHelmet] = helm
				}
			}
		}
		return
	}
	if e.FireTicks < 160 {
		s.igniteMob(e, 160)
	}
}

// sunBurnTick is Mob.isSunBurnTick: the MONSTERS_BURN environment
// attribute (overworld daylight, rain off), sky light over the eye
// above 0.5, a nextFloat*30 < (light-0.4)*2 roll, not standing in
// water/rain/powder snow, and open sky above the eye block. mc-go's
// lighting pass is unmodelled, so sky visibility stands in for the
// light level: under open daylight sky the value is 1.
func (s *Server) sunBurnTick(e *Entity, name string) bool {
	d := s.dims[e.Dim]
	if d == nil || !d.skylight {
		return false
	}
	// MONSTERS_BURN is off over the same window the overworld lets
	// players sleep — daytime 12542..23459 (bed.go's bounds).
	dt := s.dayTime() % 24000
	if dt < 0 {
		dt += 24000
	}
	if dt >= 12542 && dt <= 23459 {
		return false
	}
	if s.weatherRaining.Load() {
		return false
	}
	if rand.Float32()*30 >= 1.2 {
		return false
	}
	eye := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y + mobSunEyeHeights[name])),
		Z: int(math.Floor(e.Z)),
	}
	if !s.cropLit(d, eye) {
		return false
	}
	return !s.mobInWater(e) && !s.fireRainingAt(d, eye)
}

// spiderCanHunt is the SpiderTargetGoal brightness gate: a spider
// picks a target only when the light level at its eye is under 0.5 —
// the skylight-less dark of night or an opaque roof's shadow. A dim
// without skylight counts as dark.
func (s *Server) spiderCanHunt(e *Entity) bool {
	d := s.dims[e.Dim]
	if d == nil {
		return false
	}
	if !d.skylight {
		return true
	}
	dt := s.dayTime() % 24000
	if dt < 0 {
		dt += 24000
	}
	if dt >= 12542 && dt <= 23459 {
		return true
	}
	eye := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y + 0.65)),
		Z: int(math.Floor(e.Z)),
	}
	return !s.cropLit(d, eye)
}

// provokerDead implements NeutralMob's forgive_dead_players check:
// the angered mob calms when the session carrying AngryAt is dead.
// An offline provoker keeps the anger (the uuid target stays armed).
func (s *Server) provokerDead(e *Entity) bool {
	if !s.gameRuleBool("minecraft:forgive_dead_players") {
		return false
	}
	dead := false
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.uuid == e.AngryAt {
			if sess.dead {
				dead = true
			}
			return false
		}
		return true
	})
	return dead
}

// piglinAlert is ZombifiedPiglin.alertOthers: every piglin in the
// 10-block alert box that isn't angry yet picks up the same target —
// vanilla also wants the alerter to see the target, which in the
// harness's open flat world is always true.
func (s *Server) piglinAlert(e *Entity) {
	zeID, ok := EntityTypeID("zombified_piglin")
	if !ok {
		return
	}
	for _, snap := range s.entities.All() {
		if snap.Type != zeID || snap.Dim != e.Dim || snap.ID == e.ID ||
			snap.AngryAt != [16]byte{} {
			continue
		}
		if math.Abs(snap.X-e.X) <= 10 && math.Abs(snap.Y-e.Y) <= 10 &&
			math.Abs(snap.Z-e.Z) <= 10 {
			if other, ok := s.entities.GetPtr(snap.ID); ok {
				other.AngryAt = e.AngryAt
				other.AngryUntil = e.AngryUntil
			}
		}
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

// voicePitchE is getVoicePitch for an entity: babies center at 1.5.
func voicePitchE(e *Entity) float32 {
	if e != nil && e.Baby {
		return (rand.Float32()-rand.Float32())*0.2 + 1.5
	}
	return voicePitch()
}

// Crossbow goal states — RangedCrossbowAttackGoal$CrossbowState and
// PiglinAi's CrossbowAttack$CrossbowState share the same machine.
const (
	cbUncharged = iota
	cbCharging
	cbCharged
	cbReady
)

// mobCrossbow is the crossbow-armed set: pillager registers
// RangedCrossbowAttackGoal(this, 1.0, 8.0) and PiglinAi's
// CrossbowAttack behavior mirrors it — charge floor(1.25*20)=25
// ticks, hold 20+rand(20), then performShooting at speed 1.6 and
// 14-difficulty*4 divergence.
var mobCrossbow = map[string]bool{"pillager": true, "piglin": true}

// mobHoldsCrossbow is ProjectileUtil.getWeaponHoldingHand's main-hand
// check — the goal only runs while the crossbow is held.
func (s *Server) mobHoldsCrossbow(e *Entity) bool {
	cb, ok := world.ItemID("crossbow")
	return ok && e.Equips[equipMainHand].ID == cb
}

// piglinTarget is PiglinAi's attackable predicate folded into the
// nearest-player pick: only adults hunt, and a player wearing any
// golden armor piece isn't a target (isPlayerHoldingLovedItem's
// inverse — golden armor counts as "loved").
func (s *Server) piglinTarget(e *Entity) *Session {
	if e.Baby {
		return nil
	}
	fr := 16.0
	if v, ok := entityAttrBases["piglin"]["follow_range"]; ok {
		fr = v
	}
	best := fr * fr
	var target *Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.spawned.Load() || sess.dead ||
			sess.mode() == GameModeCreative || sess.mode() == GameModeSpectator ||
			sess.dimension().name != e.Dim || wearingGoldArmor(sess) {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		if d2 := dx*dx + dz*dz + dy*dy*0.25; d2 < best {
			best, target = d2, sess
		}
		return true
	})
	return target
}

// wearingGoldArmor is PiglinAi.isWearingGold: any of the four armor
// slots carrying a golden_ piece.
func wearingGoldArmor(sess *Session) bool {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	for slot := slotArmorStart; slot < slotArmorStart+4; slot++ {
		it := sess.inv[slot]
		if it.Empty() {
			continue
		}
		if nm, ok := world.ItemName(it.ID); ok && strings.HasPrefix(nm, "golden_") {
			return true
		}
	}
	return false
}

// mobCrossbowStep runs the shared crossbow state machine for both
// goal users (pillager) and brain users (adult crossbow piglin):
// approach while outside the 8-block radius or freshly seeing the
// target (<5 ticks), then charge 25 ticks, hold 20+rand(20), fire.
// The living-use broadcast is startUsingItem's hand-active flag.
func (s *Server) mobCrossbowStep(e *Entity, kn string) bool {
	var target *Session
	if kn == "piglin" {
		target = s.piglinTarget(e)
	} else {
		target = s.nearestMobTarget(e, kn)
	}
	if target == nil {
		if e.cbState != cbUncharged {
			e.cbState = cbUncharged
			s.broadcastLivingUse(e, false)
		}
		e.rangedSee = 0
		return false
	}
	px, py, pz, _, _, _ := target.Pos()
	dx, dz := px-e.X, pz-e.Z
	d2 := dx*dx + dz*dz
	dist := math.Hypot(dx, dz)
	// Flat harness world — hasLineOfSight is always clear.
	los := true
	if los != (e.rangedSee > 0) {
		e.rangedSee = 0
	}
	if los {
		e.rangedSee++
	} else {
		e.rangedSee--
	}
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw

	// The approach gate: out of radius or fresh on the see counter —
	// while set, the state machine never starts charging.
	needsApproach := (d2 > 64 || e.rangedSee < 5) && e.cbDelay == 0
	if needsApproach && dist > 0 {
		speed := mobChaseSpeed
		if mv, ok := entityAttrBases[kn]["movement_speed"]; ok {
			speed = mv
		}
		step := speed
		if step > dist*0.8 {
			step = dist * 0.8
		}
		e.X += dx / dist * step
		e.Z += dz / dist * step
	}
	switch e.cbState {
	case cbUncharged:
		if !needsApproach {
			e.cbState = cbCharging
			e.cbTime = 0
			s.broadcastLivingUse(e, true)
		}
	case cbCharging:
		e.cbTime++
		if e.cbTime >= 25 { // CrossbowItem.getChargeDuration unenchanted
			e.cbState = cbCharged
			e.cbDelay = int32(20 + rand.Intn(20))
			s.broadcastLivingUse(e, false)
		}
	case cbCharged:
		e.cbDelay--
		if e.cbDelay == 0 {
			e.cbState = cbReady
		}
	case cbReady:
		if e.rangedSee > 0 {
			s.mobShootArrow(e, px, py, pz)
			e.cbState = cbUncharged
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 491, 5, 1.0, 1.0, nil) // item.crossbow.shoot
		}
	}
	return true
}

// mobBeam is the guardian family — the only mobs running
// GuardianAttackGoal.
var mobBeam = map[string]bool{"guardian": true, "elder_guardian": true}

// broadcastGuardianBeam syncs DATA_ID_ATTACK_TARGET — metadata index
// 17, int serializer — the client renders the beam to the target id.
func (s *Server) broadcastGuardianBeam(e *Entity, targetID int32) {
	var b protocol.Buffer
	b.Byte(17)
	b.VarInt(1) // int serializer
	b.VarInt(targetID)
	b.Byte(0xFF)
	s.broadcastEntityMetadata(e.ID, b.Bytes())
}

// elderCurseStep runs the ElderGuardian.customServerAiStep curse: every
// 1200 ticks phased by the entity id ((tickCount+id)%1200==0) each
// survival-mode player within 50 blocks gets Mining Fatigue III for
// 6000 ticks and the GUARDIAN_ELDER_EFFECT game event (param 0 when
// the elder is silent). addEffectToPlayersAround refreshes only a
// weaker instance or one ending within 1199 ticks — a fresh amp-2
// cast isn't re-pushed.
func (s *Server) elderCurseStep(e *Entity) {
	if (e.SyncCount+int64(e.ID))%1200 != 0 {
		return
	}
	id, ok := MobEffectID("mining_fatigue")
	if !ok {
		return
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() ||
			sess.dead || sess.dimName() != e.Dim ||
			(sess.mode() != GameModeSurvival && sess.mode() != GameModeAdventure) {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		if dx*dx+dy*dy+dz*dz >= 2500 { // closerThan(50) is strict
			return true
		}
		if cur, live := sessEffect(sess, id, s.clock.count); live &&
			cur.amplifier >= 2 && cur.expiry-s.clock.count > 1199 {
			return true
		}
		s.playerGiveEffect(sess, id, 2, 6000, false)
		param := float32(1)
		if e.Silent {
			param = 0
		}
		_ = s.sendGameEvent(sess, 10, param) // GUARDIAN_ELDER_EFFECT
		return true
	})
}

// mobGuardianStep runs GuardianAttackGoal: on acquiring a living target
// the beam charges for getAttackDuration ticks (80, elder 60) then
// zaps — an indirect_magic hit (1.0, +2 on hard, +2 for the elder)
// plus a same-tick doHurtTarget melee swing — and releases the target.
// Vanilla gates the whole goal past three blocks: GuardianAttackSelector
// requires dist²>9 to acquire and the regular guardian's
// canContinueToUse drops the beam inside it. The wire-visible beats are
// entity event 21 and the index-17 target id at beam start (attackTime
// -10→0), the zap, and the id clearing to 0 on release.
// GuardianAttackSelector's allowed classes — Player or Squid/Axolotl
// living entities. The >3-block gate lives on the selector itself.
var mobBeamMobKinds = map[string]bool{"squid": true, "axolotl": true}

func beamMobKind(m *Entity) bool {
	name, _ := EntityTypeName(m.Type)
	return mobBeamMobKinds[name]
}

func (s *Server) mobGuardianStep(e *Entity, kn string) bool {
	elder := kn == "elder_guardian"
	if elder {
		s.elderCurseStep(e)
		// customServerAiStep's tail: an elder without a home claims
		// its block position with radius 16 — latches, so only the
		// first tick plants it.
		if !e.HasHome {
			e.HomePos = world.Pos{
				X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z)),
			}
			e.HomeRadius = 16
			e.HasHome = true
		}
	}
	var target *Session
	var mobTarget *Entity
	if e.beamTarget != 0 {
		if t := s.sessionByEntityID(e.beamTarget); t != nil {
			if !t.dead && t.mode() != GameModeCreative &&
				t.mode() != GameModeSpectator && t.dimName() == e.Dim {
				target = t
			}
		} else if m := s.entityByID(e.beamTarget); m != nil &&
			m.Dim == e.Dim && m.Health > 0 && beamMobKind(m) {
			mobTarget = m
		}
	}
	if target == nil && mobTarget == nil {
		// stop(): setActiveAttackTarget(0) only dirties the sync if it
		// changed — SynchedEntityData.set skips equal values.
		if e.beamMeta {
			s.broadcastGuardianBeam(e, 0)
			e.beamMeta = false
		}
		e.beamTarget = 0
		e.beamTime = 0
		// NearestAttackableTargetGoal picks the nearest selector match
		// inside follow_range — players and squid/axolotl compete by
		// distanceToSqr, and each candidate must sit past 3 blocks.
		best := 16.0 * 16.0
		if fr, ok := entityAttrBases[kn]["follow_range"]; ok {
			best = fr * fr
		}
		if t := s.nearestMobTarget(e, kn); t != nil {
			px, py, pz, _, _, _ := t.Pos()
			dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
			if dx*dx+dy*dy+dz*dz > 9.0 {
				e.beamTarget = t.entityID.Load()
				e.beamTime = -10 // start()
			}
		}
		s.entities.mu.RLock()
		for _, m := range s.entities.byID {
			if m == e || !beamMobKind(m) || m.Dim != e.Dim || m.Health <= 0 {
				continue
			}
			dx, dy, dz := m.X-e.X, m.Y-e.Y, m.Z-e.Z
			if d2 := dx*dx + dy*dy + dz*dz; d2 > 9.0 && d2 < best {
				best = d2
				mobTarget = m
			}
		}
		s.entities.mu.RUnlock()
		if mobTarget != nil {
			if target != nil {
				px, py, pz, _, _, _ := target.Pos()
				dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
				if dx*dx+dy*dy+dz*dz <= best {
					mobTarget = nil
				}
			}
			if mobTarget != nil {
				e.beamTarget = mobTarget.ID
				e.beamTime = -10
			}
		}
		return e.beamTarget != 0
	}
	var tx, ty, tz float64
	if target != nil {
		tx, ty, tz, _, _, _ = target.Pos()
	} else {
		tx, ty, tz = mobTarget.X, mobTarget.Y, mobTarget.Z
	}
	dx, dy, dz := tx-e.X, ty-e.Y, tz-e.Z
	if !elder && dx*dx+dy*dy+dz*dz <= 9.0 {
		e.beamTarget = 0
		e.beamTime = 0
		if e.beamMeta {
			s.broadcastGuardianBeam(e, 0)
			e.beamMeta = false
		}
		return false
	}
	// hasLineOfSight always holds on the flat harness world.
	e.Yaw = float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	e.HeadYaw = e.Yaw
	e.beamTime++
	if e.beamTime == 0 {
		s.broadcastGuardianBeam(e, e.beamTarget)
		e.beamMeta = true
		if !e.Silent {
			s.broadcastEntityEvent(e.ID, 21) // guardian.attack cue
		}
	}
	dur := int32(80)
	if elder {
		dur = 60
	}
	if e.beamTime >= dur {
		beam := float32(1.0)
		if s.difficulty == DifficultyHard {
			beam += 2
		}
		if elder {
			beam += 2
		}
		if target != nil {
			s.damageWith(target, beam, translatable("death.attack.indirectMagic",
				literal(target.getName()), literal(mobDisplayName(kn))),
				damageSource{typeName: "indirect_magic", cause: e.ID, direct: e.ID})
			// doHurtTarget has no range gate — the melee swing lands in
			// the same tick, reduced to its delta over the beam hit by the
			// target's i-frames.
			s.damageWith(target, mobMeleeDamage(kn), translatable("death.attack.mob",
				literal(target.getName()), literal(mobDisplayName(kn))),
				damageSource{typeName: "mob_attack", cause: e.ID, direct: e.ID})
		} else {
			s.damageMob(mobTarget, beam, "indirect_magic", nil,
				&damageInfo{typeName: "indirect_magic", cause: e.ID, direct: e.ID})
			s.damageMob(mobTarget, mobMeleeDamage(kn), "mob_attack", nil,
				&damageInfo{typeName: "mob_attack", cause: e.ID, direct: e.ID})
		}
		e.beamTarget = 0
		e.beamTime = 0
		if e.beamMeta {
			s.broadcastGuardianBeam(e, 0)
			e.beamMeta = false
		}
	}
	return true
}

// sheepEatTick is Sheep's EatBlockGoal: canUse rolls nextInt(50) for
// babies / nextInt(1000) for adults per tick, requiring an
// edible_for_sheep block underfoot or a grass_block below. start()
// sets a 40-tick animation (entity event 10); when the counter hits
// adjustedTickDelay(4) the eat lands — the underfoot edible is
// destroyed (or grass_block below turns to dirt), both gated on
// mob_griefing, then Mob.ate → Sheep.ate clears Sheared (+60 age for
// babies).
func (s *Server) sheepEatTick(e *Entity, d *dimension) bool {
	w := s.dimWorld(e.Dim)
	if w == nil {
		return false
	}
	if e.EatAnim > 0 {
		e.EatAnim--
		if e.EatAnim == 4 {
			s.sheepEat(e, d, w)
		}
		return true
	}
	roll := 1000
	if e.Baby {
		roll = 50
	}
	if s.rng.intn(roll) != 0 {
		return false
	}
	pos := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	if !sheepEdibleBlock(w.GetBlock(pos)) {
		below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		bn, _ := world.StateOwner(w.GetBlock(below))
		if strings.TrimPrefix(bn, "minecraft:") != "grass_block" {
			return false
		}
	}
	e.EatAnim = 40
	s.broadcastEntityEvent(e.ID, 10)
	return true
}

// sheepEdibleBlock is the edible_for_sheep block tag.
func sheepEdibleBlock(b world.Block) bool {
	n, _ := world.StateOwner(b)
	switch strings.TrimPrefix(n, "minecraft:") {
	case "short_grass", "short_dry_grass", "tall_dry_grass", "fern":
		return true
	}
	return false
}

// sheepEat resolves the eat tick: destroy the underfoot edible block
// (mob_griefing-gated) or convert the grass_block below to dirt, then
// ate() un-shears the sheep.
func (s *Server) sheepEat(e *Entity, d *dimension, w *world.World) {
	pos := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	if sheepEdibleBlock(w.GetBlock(pos)) {
		if s.gameRuleBool("minecraft:mob_griefing") {
			old := w.GetBlock(pos)
			s.broadcastLevelEvent(d.name, 2001, pos, int32(old))
			s.setNaturalBlock(d, pos, world.Air)
		}
	} else {
		below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		bn, _ := world.StateOwner(w.GetBlock(below))
		if strings.TrimPrefix(bn, "minecraft:") == "grass_block" && s.gameRuleBool("minecraft:mob_griefing") {
			if dirt, ok := world.BlockStateOf("dirt", nil); ok {
				s.setNaturalBlock(d, below, dirt)
			}
		}
	}
	if e.Sheared {
		e.Sheared = false
		s.broadcastEntityMetadata(e.ID, encodeLivingMetadata(e, nil))
	}
	if e.Baby {
		e.Age += 60
	}
}
