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
	// killerType/killerVariant carry
	// damage_source_properties.source_entity — the entity_type
	// sub-predicate plus the frog/variant component. killerNot
	// inverts the entity_type half alone (magma_cream drops on
	// every non-frog kill, and on kills with no source entity).
	killerType, killerVariant, killerNot string
	// minCubeSize carries entity_properties(this)'s
	// type_specific/cube_mob {size:{min:N}} predicate.
	minCubeSize int32
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
	// noSheared gates the wool pool: every color entry carries
	// type_specific/sheep {sheared:false}, so a shorn sheep drops
	// no wool.
	noSheared bool
	// cubeSizeExact is the pool-level entity_properties(this)
	// type_specific/cube_mob {size:N} exact-size gate (slime).
	cubeSizeExact int32
	entries       []lootEntry
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
		{rollsMin: 1, rollsMax: 1, noSheared: true, entries: []lootEntry{
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
	// slime.json: the whole pool is entity_properties(this)
	// type_specific/cube_mob {size:1} — bigger cubes drop nothing.
	// The entries split on the killer: a non-frog cause rolls
	// slime_ball 0-2 + looting; a frog kill deterministically
	// drops exactly one (the inverted frog gate kills the first
	// entry, leaving the unconditional-by-variant second).
	"slime": {{rollsMin: 1, rollsMax: 1, cubeSizeExact: 1, entries: []lootEntry{
		{item: "slime_ball", min: 0, max: 2, killerNot: "frog", looting: true},
		{item: "slime_ball", min: 1, max: 1, killerType: "frog"}}}},
	// magma_cube.json: one pool whose four entries each try the
	// draw — on a frog kill exactly one froglight matches the
	// variant component and drops deterministically; on any other
	// cause only magma_cream is valid (and only for size ≥2
	// cubes), rolling a uniform −2..1 that is usually nothing.
	"magma_cube": {{rollsMin: 1, rollsMax: 1, entries: []lootEntry{
		{item: "magma_cream", min: -2, max: 1, killerNot: "frog", minCubeSize: 2, looting: true},
		{item: "pearlescent_froglight", min: 1, max: 1, killerType: "frog", killerVariant: "warm"},
		{item: "verdant_froglight", min: 1, max: 1, killerType: "frog", killerVariant: "cold"},
		{item: "ochre_froglight", min: 1, max: 1, killerType: "frog", killerVariant: "temperate"}}}},
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
	// dropCustomDeathLoot rolls every equipment slot by its
	// dropChance (Mob.dropCustomDeathLoot — it runs regardless of
	// mob_drops): 0 never drops, >1.0 preserves (drops
	// unconditionally and undamaged), otherwise the kill must be a
	// player's and rand < chance+0.01·looting. A dropped damageable
	// item lands nearly worn: damage = maxD − rand(1 + rand(max(1,
	// maxD−3))).
	var looting int32
	if killer != nil {
		looting = killer.heldStack().enchLevel("looting")
	}
	for _, ds := range equipDropSlots {
		st, ok := e.Equips[ds.slot]
		if !ok || st.Empty() {
			continue
		}
		f := mobDropChance(e, ds.name)
		if f == 0 {
			continue
		}
		preserved := f > 1.0
		if !byPlayer && !preserved {
			continue
		}
		eff := f
		if byPlayer && looting > 0 {
			eff += 0.01 * float64(looting)
		}
		if !preserved && rand.Float64() >= eff {
			continue
		}
		if !preserved {
			if maxD := MaxDamageOfID(st.ID); maxD > 0 {
				st.Damage = maxD - int32(rand.Intn(1+rand.Intn(int(max(1, maxD-3)))))
			}
		}
		delete(e.Equips, ds.slot)
		s.spawnMobStack(e, st)
	}
	// AbstractHorse.dropEquipment drops every non-empty inventory
	// cell — saddle, body slot, chest cells — and
	// AbstractChestedHorse then drops the chest block itself and
	// clears the flag.
	if mountScreenTypes[name] {
		if e.Saddle {
			if sid, ok := world.ItemID("saddle"); ok {
				s.spawnMobStack(e, ItemStack{ID: sid, Count: 1})
			}
		}
		if !e.BodyItem.Empty() {
			s.spawnMobStack(e, e.BodyItem)
		}
		for _, st := range e.MountInv {
			if !st.Empty() {
				s.spawnMobStack(e, st)
			}
		}
		if e.HasChest {
			if cid, ok := world.ItemID("chest"); ok {
				s.spawnMobStack(e, ItemStack{ID: cid, Count: 1})
			}
			e.HasChest = false
		}
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
	// damage_source_properties.source_entity resolves through
	// lastDamageSource's responsible entity — the frog behind an
	// eat-kill, or empty when nothing caused the last hurt.
	var killerEnt *Entity
	if e.LastDamageCause != 0 {
		killerEnt, _ = s.entities.GetPtr(e.LastDamageCause)
	}
	killerName := ""
	killerVariant := ""
	if killerEnt != nil {
		killerName, _ = EntityTypeName(killerEnt.Type)
		killerVariant = killerEnt.VariantName
	}
	for _, p := range mobLoot[name] {
		if p.playerKill && !byPlayer {
			continue
		}
		if p.captain && !mobIsCaptain(e) {
			continue
		}
		if p.noSheared && e.Sheared {
			continue
		}
		if p.cubeSizeExact > 0 && cubeSizeOf(e) != p.cubeSizeExact {
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
		// LootPool.addRandomItem filters every entry's conditions
		// before the weighted pick — a conditioned entry that
		// fails simply isn't in the draw.
		valid := make([]lootEntry, 0, len(p.entries))
		for _, ent := range p.entries {
			if ent.killerType != "" && killerName != ent.killerType {
				continue
			}
			if ent.killerNot != "" && killerName == ent.killerNot {
				continue
			}
			if ent.killerVariant != "" && killerVariant != ent.killerVariant {
				continue
			}
			if ent.minCubeSize > 0 && cubeSizeOf(e) < ent.minCubeSize {
				continue
			}
			valid = append(valid, ent)
		}
		if len(valid) == 0 {
			continue
		}
		rolls := p.rollsMin
		if p.rollsMax > p.rollsMin {
			rolls += int32(rand.Intn(int(p.rollsMax-p.rollsMin) + 1))
		}
		for i := int32(0); i < rolls; i++ {
			ent := valid[rand.Intn(len(valid))]
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

// equipSlotNames is EquipmentSlot.VALUES' name order — summon parse
// and the drop_chances save emit both walk all eight.
var equipSlotNames = []string{
	"mainhand", "offhand", "feet", "legs", "chest", "head", "body", "saddle",
}

// equipDropSlots pairs the six shared Equips wire ids with their NBT
// slot names in VALUES order (body/saddle live outside Equips).
var equipDropSlots = []struct {
	slot int32
	name string
}{
	{equipMainHand, "mainhand"}, {equipOffHand, "offhand"},
	{equipBoots, "feet"}, {equipLegs, "legs"},
	{equipChest, "chest"}, {equipHelmet, "head"},
}

const mobDefaultDropChance = 0.085

// mobDropChance is DropChances.byEquipment: the summon override when
// present, else the patrol captain's guaranteed head drop, else the
// 0.085 default.
func mobDropChance(e *Entity, name string) float64 {
	if e.DropChances != nil {
		if v, ok := e.DropChances[name]; ok {
			return v
		}
	}
	if name == "head" && e.PatrolLeader {
		return 2.0
	}
	return mobDefaultDropChance
}

// mobDropChancesOverridden reports whether any slot's chance differs
// from DEFAULT — vanilla emits drop_chances only then (DropChances
// equals check before the codec put).
func mobDropChancesOverridden(e *Entity) bool {
	for _, name := range equipSlotNames {
		if mobDropChance(e, name) != mobDefaultDropChance {
			return true
		}
	}
	return false
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
	// The pairing bundle already ships the item stack — vanilla's
	// sendPairingData carries the one set_entity_data inside it.
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
		OrbCount:    1,
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

// addXP grants experience points the way ExperienceOrb.playerTouch
// does — ServerPlayer.giveExperiencePoints.
func (sess *Session) addXP(points int32) {
	sess.giveExperiencePoints(points)
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
	"horse":            53, // createBaseHorseAttributes' base; natural/init spawns re-roll 15-30
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
	"tadpole":          6,
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
		// Aquatics — sized() off EntityTypes' 26.2 clinits. Missing
		// entries fell back to the 0.6x1.8 player box, whose eye probe
		// reads a cell well above the mob and mis-fires the in_wall
		// suffocation arm near terrain (killed hatched tadpoles).
		"tadpole":         {0.4, 0.3},
		"cod":             {0.5, 0.3},
		"salmon":          {0.7, 0.4},
		"tropical_fish":   {0.5, 0.4},
		"pufferfish":      {0.7, 0.7},
		"squid":           {0.8, 0.8},
		"glow_squid":      {0.8, 0.8},
		"axolotl":         {0.75, 0.42},
		"frog":            {0.5, 0.5},
		"dolphin":         {0.9, 0.6},
		"nautilus":        {0.875, 0.95},
		"zombie_nautilus": {0.875, 0.95},
	} {
		if id, ok := EntityTypeID(name); ok {
			mobBoundsTable[id] = dims
		}
	}
	// 26.2 vehicles carry the family in the type name — every boat,
	// raft, and chest variant shares AbstractBoat's 1.375x0.5625 box
	// and every minecart shares 0.98x0.7 — so the names can't be
	// enumerated one by one in the table above.
	for _, name := range vehicleBoundNames() {
		dims := [2]float64{0.98, 0.7}
		if isBoatEntityName(name) {
			dims = [2]float64{1.375, 0.5625}
		}
		if id, ok := EntityTypeID(name); ok {
			mobBoundsTable[id] = dims
		}
	}
}

// vehicleBoundNames lists every summonable VehicleEntity type name in
// the registry so mobBounds covers them all.
func vehicleBoundNames() []string {
	var names []string
	for name := range map[string]struct{}{
		"oak_boat": {}, "spruce_boat": {}, "birch_boat": {}, "jungle_boat": {},
		"acacia_boat": {}, "dark_oak_boat": {}, "cherry_boat": {}, "mangrove_boat": {},
		"pale_oak_boat": {}, "bamboo_raft": {},
		"oak_chest_boat": {}, "spruce_chest_boat": {}, "birch_chest_boat": {},
		"jungle_chest_boat": {}, "acacia_chest_boat": {}, "dark_oak_chest_boat": {},
		"cherry_chest_boat": {}, "mangrove_chest_boat": {}, "pale_oak_chest_boat": {},
		"bamboo_chest_raft": {}, "chest_boat": {},
		"minecart": {}, "chest_minecart": {}, "furnace_minecart": {},
		"tnt_minecart": {}, "hopper_minecart": {}, "command_block_minecart": {},
		"spawner_minecart": {},
	} {
		names = append(names, name)
	}
	return names
}

// mobPushStep is LivingEntity.pushEntities' separation half: for
// each entity whose bounding box intersects this mob's, vanilla's
// Entity.push normalizes the x/z separation by its abs-max, scales
// it by min(1, 1/absMax)*0.05, and shoves both sides apart along it
// — once per entity per tick, so a pair is pushed twice. The shove
// lands on deltaMovement (MotionX/Z) and the position; a vehicle or
// same-ride pair is skipped, and near-perfect overlap (absMax<0.01)
// doesn't push at all.
func (s *Server) mobPushStep(e *Entity) {
	ew, eh := entityBBDims(e)[0], entityBBDims(e)[1]
	for _, o := range s.entities.Live() {
		if o.ID == e.ID || o.Dim != e.Dim {
			continue
		}
		oname, _ := EntityTypeName(o.Type)
		oCart := isMinecartName(oname)
		if !oCart && (!o.Living || o.Health <= 0) {
			continue
		}
		if o.Riding != 0 || e.Riding != 0 {
			continue
		}
		ow, oh := entityBBDims(o)[0], entityBBDims(o)[1]
		half := (ew + ow) / 2
		if !(math.Abs(o.X-e.X) < half && math.Abs(o.Z-e.Z) < half &&
			e.Y < o.Y+oh && o.Y < e.Y+eh) {
			continue
		}
		if oCart {
			// cart.push(this): AbstractMinecart.push's non-cart
			// branch — absMax normalization is replaced by the
			// length² gate, the cart takes the full 0.05 repel, the
			// mob a quarter of it.
			dx, dz := e.X-o.X, e.Z-o.Z
			d6 := dx*dx + dz*dz
			if d6 < 1e-4 {
				continue
			}
			l := math.Sqrt(d6)
			dx, dz = dx/l, dz/l
			inv := 1.0 / l
			if inv > 1 {
				inv = 1
			}
			px, pz := dx*inv*0.05, dz*inv*0.05
			if op, ok := s.entities.GetPtr(o.ID); ok {
				op.MotionX -= px
				op.MotionZ -= pz
				op.PosDirty = true
				op.NeedsSync = true
			}
			e.MotionX += px / 4
			e.MotionZ += pz / 4
			e.PosDirty = true
			e.NeedsSync = true
			continue
		}
		dx, dz := o.X-e.X, o.Z-e.Z
		m := math.Max(math.Abs(dx), math.Abs(dz))
		if m < 0.01 {
			continue
		}
		m = math.Sqrt(m)
		dx, dz = dx/m, dz/m
		inv := 1.0 / m
		if inv > 1 {
			inv = 1
		}
		dx, dz = dx*inv*0.05, dz*inv*0.05
		// push(DDD) lands on deltaMovement only — the position moves
		// when travel's move() integrates it, which NoAI mobs never
		// run: overlapping NoAI entities sit forever while their
		// delta grows (and sends) every tick. A vehicle doesn't take
		// the shove itself; a passenger pair is filtered out above.
		if len(e.Passengers) == 0 {
			e.MotionX -= dx
			e.MotionZ -= dz
			// Entity.push flags needsSync on the pushed side.
			e.PosDirty = true
			e.NeedsSync = true
		}
		if len(o.Passengers) == 0 {
			// All() hands out copies — the neighbour's half of the
			// shove has to go through the live pointer.
			if op, ok := s.entities.GetPtr(o.ID); ok {
				op.MotionX += dx
				op.MotionZ += dz
				op.PosDirty = true
				op.NeedsSync = true
			}
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
	ew, eh := entityBBDims(e)[0], entityBBDims(e)[1]
	count := 0
	for _, o := range s.entities.Live() {
		if o.ID == e.ID || o.Dim != e.Dim || o.Riding != 0 || o.Health <= 0 {
			continue
		}
		ow, oh := entityBBDims(o)[0], entityBBDims(o)[1]
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
// type. Existing entries retain their runtime policies; measured positive
// type records fill missing entries before the legacy player-box fallback.
func mobBounds(typeID int32) [2]float64 {
	if dims, ok := mobBoundsTable[typeID]; ok {
		return dims
	}
	if name, ok := EntityTypeName(typeID); ok {
		if dims, ok := entityTypeBaseDims[name]; ok && dims[0] > 0 && dims[1] > 0 {
			return [2]float64{float64(dims[0]), float64(dims[1])}
		}
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
	// Resolve a real command frame directly; a server or command-block
	// source does not have a player session.
	d := s.dimOf(player)
	var x, y, z float64
	if frame := s.commandFrame(player); frame != nil {
		x, y, z = frame.pos[0], frame.pos[1], frame.pos[2]
	} else {
		sess := s.findSessionByName(player)
		if sess == nil {
			return textReply("Unknown player: " + player)
		}
		x, y, z, _, _, _ = sess.Pos()
		if sess.execPos != nil {
			x, y, z = sess.execPos[0], sess.execPos[1], sess.execPos[2]
		} else if sess.execEnt != nil && sess.execEnt.mob != nil {
			x, y, z = sess.execEnt.mob.X, sess.execEnt.mob.Y, sess.execEnt.mob.Z
		}
	}
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
	// EntityType.Builder.noSummon: player (0.6×1.8 eye 1.62) and
	// fishing_bobber (0.25×0.25) are the only two registered types
	// vanilla refuses, answered with entity.not_summonable in red.
	if name == "player" || name == "fishing_bobber" {
		return componentReply(commandError("entity.not_summonable", "minecraft:"+name))
	}
	// EndCrystal and EnderDragon carry their own entity state
	// (endcrystal.go, enderdragon.go).
	if name == "end_crystal" || name == "ender_dragon" {
		s.summonEndEntity(d, name, x, y, z, tag)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	if e, ok := s.summonMobRaw(d, name, x, y, z, noAI, tag == nil); ok {
		// The variant rolls live in Mob.finalizeSpawn, which the
		// summon command only calls for the no-NBT overload
		// (initialize=true): a bare summon rolls the biome-eligible
		// variant (highest spawn_conditions priority, uniform within
		// the tier) and a uniform sound_variant; a tagged summon
		// keeps its define defaults unless the NBT keys below set
		// them explicitly.
		if tag == nil {
			if v := rollSpawnVariant(name, strings.TrimPrefix(d.w.Biome(), "minecraft:")); v != "" {
				e.VariantName = v
			}
			e.SoundVariant = rollSoundVariant(name)
		}
		if tag != nil {
			// Rotation:[yaw,pitch] overrides create's random snapTo yaw.
			if rot, ok := tag.vals["Rotation"]; ok && len(rot.items) >= 2 {
				e.Yaw = float32(snbtNum(rot.items[0]))
				e.Pitch = float32(snbtNum(rot.items[1]))
				e.HeadYaw, e.LookYaw = e.Yaw, e.Yaw
			}
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
			// FireworkRocketEntity.readAdditionalSaveData — Life,
			// LifeTime, ShotAtAngle, and the FireworksItem stack.
			if name == "firework_rocket" {
				applyRocketSummonNBT(e, tag)
			}
			if bs, ok := tag.vals["carriedBlockState"]; ok && name == "enderman" {
				// EnderMan.readAdditionalSaveData drops an air
				// carriedBlockState back to the empty Optional;
				// a Properties map carries the non-default state.
				if bn, ok := bs.vals["Name"]; ok && bn.str != "" {
					cn := strings.TrimPrefix(bn.str, "minecraft:")
					if cn != "air" {
						e.EnderCarryName = cn
						if pv, ok := bs.vals["Properties"]; ok {
							props := map[string]string{}
							for pk, pt := range pv.vals {
								if pt.str != "" {
									props[pk] = pt.str
								}
							}
							e.EnderCarryProps = props
						}
					}
				}
			}
			// AbstractHorse.readAdditionalSaveData: Tame/Bred/
			// EatingHaystack flip DATA_ID_FLAGS bits; Temper is the
			// plain int; Owner is an EntityReference to the tamer.
			if mountScreenTypes[name] {
				if v, ok := tag.vals["Tame"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
					e.Tamed = true
				}
				if v, ok := tag.vals["Bred"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
					e.Bred = true
				}
				if v, ok := tag.vals["EatingHaystack"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
					e.EatingHaystack = true
				}
				if v, ok := tag.vals["Temper"]; ok {
					e.Temper = int32(snbtNum(v))
				}
				if u, ok := snbtUUID(tag, "Owner"); ok {
					e.ownerUUID = u
					if ownerSess := s.sessionByUUID(u); ownerSess != nil {
						e.owner = ownerSess.getName()
					}
				}
			}
			if v, ok := tag.vals["ChestedHorse"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") && chestedMountTypes[name] {
				e.HasChest = true
			}
			// Horse.readAdditionalSaveData's Variant int — the packed
			// type|markings value riding DATA_TYPE_VARIANT (idx19).
			if name == "horse" {
				if v, ok := tag.vals["Variant"]; ok {
					if n, err := strconv.Atoi(strings.TrimRight(v.num, "bBsSiIlLfFdD")); err == nil {
						e.Variant = int32(n)
					}
				}
			}
			// The llama family's Strength (setStrength clamps [1,5])
			// and Variant enum id; finalizeSpawn runs on /summon too,
			// so absent keys randomize: strength is 1+nextInt(3) 96%
			// of the time and 1+nextInt(5) on the 0.04 tail, variant
			// a uniform pick of the four.
			if name == "trader_llama" || name == "wandering_trader" {
				// TraderLlama/WanderingTrader.readAdditionalSaveData —
				// a tagged summon overrides the type's countdown init.
				if v, ok := tag.vals["DespawnDelay"]; ok {
					if n, err := strconv.Atoi(strings.TrimRight(v.num, "bBsSiIlLfFdD")); err == nil {
						e.DespawnDelay = int32(n)
					}
				}
			}
			if llamaDecorTypes[name] {
				hasStr, hasVar := false, false
				if v, ok := tag.vals["Strength"]; ok {
					if n, err := strconv.Atoi(strings.TrimRight(v.num, "bBsSiIlLfFdD")); err == nil {
						e.Strength = int32(max(1, min(5, n)))
						hasStr = true
					}
				}
				if v, ok := tag.vals["Variant"]; ok {
					if n, err := strconv.Atoi(strings.TrimRight(v.num, "bBsSiIlLfFdD")); err == nil {
						e.LlamaVariant = int32(n & 3)
						hasVar = true
					}
				}
				if !hasStr {
					maxS := int32(3)
					if rand.Float64() < 0.04 {
						maxS = 5
					}
					e.Strength = 1 + rand.Int31n(maxS)
				}
				if !hasVar {
					e.LlamaVariant = rand.Int31n(4)
				}
			}
			// A chested mount's Items list is the flat
			// ItemStackWithSlot form ({Slot:N,id,count}): slot 0 is
			// the saddle cell, slot 1 the body cell, 2.. the chest
			// cells. Applied only when ChestedHorse, matching
			// readAdditionalSaveData's hasChest gate.
			if e.HasChest {
				if cells := 3 * mountInventoryColumns(e); len(e.MountInv) != cells {
					e.MountInv = make([]ItemStack, cells)
				}
				if l, ok := tag.vals["Items"]; ok {
					for _, it := range l.items {
						if it.vals == nil {
							continue
						}
						slot := -1
						if sv, ok := it.vals["Slot"]; ok {
							if n, err := strconv.Atoi(strings.TrimRight(sv.num, "bBsSiIlLfFdD")); err == nil {
								slot = n
							}
						}
						idv, ok := it.vals["id"]
						if !ok || idv.str == "" {
							continue
						}
						iid, ok := world.ItemID(strings.TrimPrefix(idv.str, "minecraft:"))
						if !ok {
							continue
						}
						cnt := int32(1)
						if cv, ok := it.vals["count"]; ok {
							if n, err := strconv.Atoi(strings.TrimRight(cv.num, "bBsSiIlLfFdD")); err == nil {
								cnt = int32(n)
							}
						}
						switch {
						case slot == 0:
							if iname, _ := itemNameOf(iid); iname == "saddle" {
								e.Saddle = true
							}
						case slot == 1:
							e.BodyItem = ItemStack{ID: iid, Count: cnt}
						case slot >= 2 && slot-2 < len(e.MountInv):
							e.MountInv[slot-2] = ItemStack{ID: iid, Count: cnt}
						}
					}
				}
			}
			// The legacy Saddle flag is NOT read on summon: 26.2 moves
			// it to equipment.saddle only through EntityHorseSaddleFix's
			// datafixer on old saves — summon NBT never sees it.
			if name == "experience_orb" {
				if save, ok := snbtToValue(tag).(map[string]any); ok {
					loadOrbSavedFields(e, save)
				}
			}
			// Allay.readAdditionalSaveData's DuplicationCooldown read:
			// getIntOr(0) → setDuplicationCooldown.
			if v, ok := tag.vals["DuplicationCooldown"]; ok && name == "allay" {
				if n, err := strconv.Atoi(strings.TrimRight(v.num, "bBsSiIlLfFdD")); err == nil {
					e.DupCooldown = int64(n)
				}
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
			if dc, ok := tag.vals["drop_chances"]; ok && e.Living {
				for _, slot := range equipSlotNames {
					if v, ok := dc.vals[slot]; ok {
						if e.DropChances == nil {
							e.DropChances = map[string]float64{}
						}
						e.DropChances[slot] = snbtNum(v)
					}
				}
			}
			// Owner on a tameable marks it tamed by that player —
			// TamableAnimal reads the same EntityReference, and
			// Wolf.canShearEquipment checks ownership through it.
			if u, ok := snbtUUID(tag, "Owner"); ok {
				e.tamed = true
				e.ownerUUID = u
				if ownerSess := s.sessionByUUID(u); ownerSess != nil {
					e.owner = ownerSess.getName()
				}
			}
			// TamableAnimal.readAdditionalSaveData's Sitting byte —
			// orders the tame sit flag on wolf/cat/parrot.
			if v, ok := tag.vals["Sitting"]; ok {
				e.Sitting = snbtNum(v) > 0
				// readAdditionalSaveData: setInSittingPose(
				// orderedToSit) — pose mirrors the field on load.
				e.SittingPose = e.Sitting
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
			// CustomName decodes through ComponentSerialization's
			// CODEC: a STRING tag lands as Component.literal text
			// (CustomNameComp clear, shown literally), a compound or
			// list decodes into a real component.
			if cv, ok := tag.vals["CustomName"]; ok {
				if cv.str != "" {
					e.CustomName = cv.str
					e.CustomNameComp = false
				} else if cv.vals != nil || cv.items != nil {
					var sb strings.Builder
					writeSNBTCompact(&sb, snbtToValue(cv))
					e.CustomName = sb.String()
					e.CustomNameComp = true
				}
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
			if fv, ok := tag.vals["ForcedAge"]; ok && ageableTypes[name] {
				e.ForcedAge = int32(snbtNum(fv))
			}
			if lv, ok := tag.vals["AgeLocked"]; ok && ageableTypes[name] && snbtNum(lv) > 0 {
				e.AgeLocked = true
			}
			// InLove is Animal's key — the non-Animal AgeableMobs
			// (fish, villagers, cubes) ignore it.
			if iv, ok := tag.vals["InLove"]; ok && animalTypes[name] {
				e.InLove = int32(snbtNum(iv))
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
			// Entity.load's remaining flag trio: CustomNameVisible
			// (the name-tag render bit), Invulnerable and Zombie's
			// CanBreakDoors.
			if bv, ok := tag.vals["CustomNameVisible"]; ok && snbtNum(bv) > 0 {
				e.CustomNameVisible = true
			}
			if bv, ok := tag.vals["Invulnerable"]; ok && snbtNum(bv) > 0 {
				e.Invulnerable = true
			}
			if bv, ok := tag.vals["CanBreakDoors"]; ok && snbtNum(bv) > 0 && zombieTypes[name] {
				e.CanBreakDoors = true
			}
			// Zombie.readAdditionalSaveData's water pair: InWaterTime
			// loads the wet counter, and DrownedConversionTime != -1
			// restarts the countdown via startUnderWaterConversion —
			// it then runs to completion even out of water.
			if zombieTypes[name] {
				if iv, ok := tag.vals["InWaterTime"]; ok {
					e.inWaterTime = int32(snbtNum(iv))
				}
				if iv, ok := tag.vals["DrownedConversionTime"]; ok {
					if c := int32(snbtNum(iv)); c != -1 {
						e.WaterConv = true
						e.ConversionTime = c
						e.MetaDirty = true
					}
				}
			}
			// Skeleton.readAdditionalSaveData's StrayConversionTime
			// restarts the freeze countdown — it converts only while
			// the mob stays in powder snow (vanilla cancels it on
			// the first dry tick).
			if bv, ok := tag.vals["StrayConversionTime"]; ok && name == "skeleton" {
				e.FreezeConv = true
				e.ConversionTime = int32(snbtNum(bv))
				e.MetaDirty = true
			}
			// Fire is Entity.load's remaining burn ticks — a
			// burning mob ignites on the next baseTick.
			if fv, ok := tag.vals["Fire"]; ok {
				e.FireTicks = int32(snbtNum(fv))
			}
			// TicksFrozen is Entity.load's DATA_TICKS_FROZEN read.
			if fv, ok := tag.vals["TicksFrozen"]; ok {
				e.TicksFrozen = int32(snbtNum(fv))
			}
			// PortalCooldown is Entity.load's portalCooldown read —
			// every entity type carries it.
			if pv, ok := tag.vals["PortalCooldown"]; ok {
				e.PortalCooldown = int32(snbtNum(pv))
			}
			// HurtTime is LivingEntity.load's hurt flash countdown.
			if fv, ok := tag.vals["HurtTime"]; ok {
				e.HurtTime = int16(snbtNum(fv))
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
			// Turtle.readAdditionalSaveData reads home_pos on its
			// own (not the Mob restriction pair), orElse'ing the
			// entity's block position — already seeded at spawn.
			// has_egg is the turtle's second write.
			if name == "turtle" {
				if lv, ok := tag.vals["home_pos"]; ok && len(lv.items) == 3 {
					e.TurtleHome = world.Pos{
						X: int(snbtNum(lv.items[0])),
						Y: int(snbtNum(lv.items[1])),
						Z: int(snbtNum(lv.items[2])),
					}
				}
				if bv, ok := tag.vals["has_egg"]; ok && snbtNum(bv) > 0 {
					e.HasEgg = true
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
				e.Effects = mobEffectsFromNBT(snbtToValue(ae), s.clock.count)
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
					mobAddEffect(e, st, 0, s.clock.count+int64(e.ConversionTime), s.clock.count)
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
			if name == "armor_stand" {
				if v, ok := tag.vals["Marker"]; ok {
					e.ArmorStandMarker = v.num == "1b" || v.num == "1" || v.str == "true"
				}
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
				if v, ok := tag.vals["DurationOnUse"]; ok {
					e.CloudDurationOnUse = int32(snbtNum(v))
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
					scale := float32(1)
					if v, ok := tag.vals["potion_duration_scale"]; ok {
						scale = float32(snbtNum(v))
					}
					setCloudPotionContents(e, snbtToValue(pc), scale, s.clock.count)
				}
			}
			// PrimedTnt's readAdditionalSaveData keys: fuse replaces the
			// 80-tick default, explosion_power overrides the 4-power
			// blast.
			if name == "tnt" {
				if v, ok := tag.vals["fuse"]; ok {
					e.Fuse = int32(snbtNum(v))
				}
				if v, ok := tag.vals["explosion_power"]; ok {
					e.BlastPower = int32(snbtNum(v))
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
					e.MaxSwell = int32(itemSavedShort(snbtToValue(v), 30))
				}
				if v, ok := tag.vals["ExplosionRadius"]; ok {
					e.BlastPower = int32(int8(itemSavedShort(snbtToValue(v), 3)))
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
				// Sheep.readAdditionalSaveData's Color key — the
				// legacy numeric dye id, low nibble of the wool byte.
				if v, ok := tag.vals["Color"]; ok {
					e.Color = int8(snbtNum(v)) & 15
				}
			}
			if name == "bogged" {
				if v, ok := tag.vals["sheared"]; ok && snbtNum(v) > 0 {
					e.Sheared = true
				}
			}
			// MushroomCow.readAdditionalSaveData: Type names the
			// variant ("red"/"brown" — any other value falls to
			// red), stew_effects the pending suspicious-stew list.
			if name == "mooshroom" {
				if v, ok := tag.vals["Type"]; ok && v.str == "brown" {
					e.MooshroomVariant = 1
				}
				if v, ok := tag.vals["stew_effects"]; ok && v.kind == snbtList {
					var effs []string
					for _, it := range v.items {
						if it.kind != snbtCompound {
							continue
						}
						eid := strings.TrimPrefix(it.vals["id"].str, "minecraft:")
						effs = append(effs, eid+":"+it.vals["duration"].num)
					}
					e.StewEffects = strings.Join(effs, ",")
				}
			}
			if name == "snow_golem" {
				if v, ok := tag.vals["Pumpkin"]; ok {
					e.Pumpkin = snbtNum(v) > 0
				}
			}
			// Axolotl/Parrot/Rabbit/TropicalFish readAdditionalSaveData:
			// each decodes its variant via the enum's LEGACY_CODEC —
			// axolotl's continuous-ZERO drops out-of-range ids to LUCY,
			// parrot's CLAMPs to [0,4], the fish's repacks through the
			// (pattern|base|patternColor) codec.
			if name == "axolotl" || name == "parrot" || name == "tropical_fish" {
				if v, ok := tag.vals["Variant"]; ok {
					e.Variant = int32(snbtNum(v))
					if name == "axolotl" && (e.Variant < 0 || e.Variant > 4) {
						e.Variant = 0
					}
					if name == "parrot" {
						e.Variant = max(0, min(4, e.Variant))
					}
					if name == "tropical_fish" {
						e.Variant = repackTropicalVariant(e.Variant)
					}
				}
			}
			// Horse.readAdditionalSaveData's Variant int — the packed
			// TypeAndMarking value (type | markings<<8) that rides
			// DATA_TYPE_VARIANT (idx21) on the wire.
			if name == "horse" {
				if v, ok := tag.vals["Variant"]; ok {
					e.Variant = int32(snbtNum(v))
				}
			}
			if name == "axolotl" || abstractFishTypes[name] {
				if v, ok := tag.vals["FromBucket"]; ok {
					e.FromBucket = snbtNum(v) > 0
				}
			}
			if name == "pufferfish" {
				if v, ok := tag.vals["PuffState"]; ok {
					// readAdditionalSaveData clamps the read at 2 —
					// negatives pass through.
					if n := snbtNum(v); n < 2 {
						e.PuffState = int32(n)
					} else {
						e.PuffState = 2
					}
				}
			}
			if name == "rabbit" {
				if v, ok := tag.vals["RabbitType"]; ok {
					e.Variant = int32(snbtNum(v))
					// ByIdMap.sparse on {0..5, 99} — unknown ids
					// fall to DEFAULT (BROWN).
					switch e.Variant {
					case 0, 1, 2, 3, 4, 5, 99:
					default:
						e.Variant = 0
					}
					if e.Variant == 99 {
						// setRabbitType auto-names the evil morph
						// with a translate component.
						e.CustomName = `{"translate":"entity.minecraft.killer_bunny"}`
						e.CustomNameComp = true
					}
				}
				if v, ok := tag.vals["MoreCarrotTicks"]; ok {
					e.MoreCarrotTicks = int32(snbtNum(v))
				}
			}
			// Fox's Type serializes the enum NAME via Variant.CODEC —
			// "red"/"snow", not the id — plus its three pose bools.
			if name == "fox" {
				if v, ok := tag.vals["Type"]; ok && v.str == "snow" {
					e.Variant = 1
				}
				if v, ok := tag.vals["Sleeping"]; ok {
					e.Sleeping = snbtNum(v) > 0
				}
				if v, ok := tag.vals["Sitting"]; ok {
					e.Sitting = snbtNum(v) > 0
				}
				if v, ok := tag.vals["Crouching"]; ok {
					e.Crouching = snbtNum(v) > 0
				}
				// Trusted reads a LIST of [I;a,b,c,d] int-array
				// uuids (TRUSTED_LIST_CODEC = listOf) into the two
				// entity-reference slots — the flat [I;] form is
				// rejected by the codec.
				if v, ok := tag.vals["Trusted"]; ok && v.kind == snbtList {
					for _, it := range v.items {
						if it == nil || len(it.items) != 4 {
							continue
						}
						var u [16]byte
						for j := 0; j < 4; j++ {
							n := int32(snbtNum(it.items[j]))
							u[j*4] = byte(n >> 24)
							u[j*4+1] = byte(n >> 16)
							u[j*4+2] = byte(n >> 8)
							u[j*4+3] = byte(n)
						}
						e.FoxTrusted = appendFoxTrusted(e.FoxTrusted, u)
					}
				}
			}
			// Camel.readAdditionalSaveData: LastPoseTick<0 sets
			// Pose.SITTING(10); the tick itself always lands on
			// DATA_LAST_POSE_CHANGE_TICK.
			if name == "camel" || name == "camel_husk" {
				// /summon replays the NBT verbatim — Camel's
				// finalizeSpawn (which would restamp the tick
				// max(0, t-53)) does not run on the summon path:
				// a {LastPoseTick:-5} summon is a real sitter.
				if v, ok := tag.vals["LastPoseTick"]; ok {
					e.LastPoseTick = int64(snbtNum(v))
					if e.LastPoseTick < 0 {
						e.Pose = 10
					}
				}
			}
			// AbstractCubeMob.readAdditionalSaveData: Size is
			// getSize()-1 (NBT 0 = the small edible slime) — health
			// follows setcubeMobHealth's size² max.
			if name == "slime" || name == "magma_cube" {
				e.CubeSize = 1
				if v, ok := tag.vals["Size"]; ok {
					e.CubeSize = int32(snbtNum(v)) + 1
					if e.CubeSize < 1 {
						e.CubeSize = 1
					}
					if e.CubeSize > 127 {
						e.CubeSize = 127
					}
				}
				if e.AttrBase == nil {
					e.AttrBase = map[string]float64{}
				}
				e.AttrBase["max_health"] = float64(e.CubeSize * e.CubeSize)
				e.Health = float32(e.CubeSize * e.CubeSize)
			}
			// Ghast ExplosionPower / Phantom size (ghast.go).
			s.flyerSummonNBT(e, name, tag)
			// Panda's genes write as strings via Gene.CODEC.
			if name == "panda" {
				if v, ok := tag.vals["MainGene"]; ok {
					e.GeneMain = v.str
				}
				if v, ok := tag.vals["HiddenGene"]; ok {
					e.GeneHidden = v.str
				}
			}
			// Holder-codec variants: cat/frog/wolf/pig/cow/chicken
			// read a namespaced `variant` key (plus `sound_variant`
			// where the type registers one — frog has none);
			// salmon's `type` is a StringRepresentable name sharing
			// the same field.
			switch name {
			case "cat", "frog", "wolf", "pig", "cow", "chicken":
				if v, ok := tag.vals["variant"]; ok && v.str != "" {
					e.VariantName = strings.TrimPrefix(v.str, "minecraft:")
				}
			}
			switch name {
			case "cat", "wolf", "pig", "cow", "chicken":
				if v, ok := tag.vals["sound_variant"]; ok && v.str != "" {
					e.SoundVariant = strings.TrimPrefix(v.str, "minecraft:")
				}
			}
			if name == "salmon" {
				if v, ok := tag.vals["type"]; ok && v.str != "" {
					e.VariantName = v.str
				}
			}
			// Cat and Wolf read CollarColor as a dye legacy-id byte
			// (DyeColor.LEGACY_ID_CODEC); absent means RED(14) —
			// the defined default — so the set bit only flips on
			// an explicit key.
			if name == "cat" || name == "wolf" {
				if v, ok := tag.vals["CollarColor"]; ok {
					e.CollarColor = int32(snbtNum(v))
					e.CollarColorSet = true
				}
			}
			// NeutralMob's persistent anger: anger_end_time long,
			// angry_at entity-reference uuid, plus the legacy
			// AngerTime int which converts to gameTime+value on
			// read (measured: 400 → end = current game time + 400).
			if neutralAngryTypes[name] {
				if v, ok := tag.vals["anger_end_time"]; ok {
					e.AngryUntil = int64(snbtNum(v))
					e.AngryUntilSet = true
				} else if v, ok := tag.vals["AngerTime"]; ok {
					e.AngryUntil = s.gameTime() + int64(snbtNum(v))
					e.AngryUntilSet = true
				}
				if u, ok := snbtUUID(tag, "angry_at"); ok {
					e.AngryAt = u
					e.AngryAtSet = true
				}
			}
			// Bee's own fields: the flag byte bits ride HasNectar/
			// HasStung (rolling has no NBT input), the pollination
			// ints take anything, and hive_pos/flower_pos read a
			// BlockPos [I;3] int-array via BlockPos.CODEC's
			// storeNullable.
			if name == "bee" {
				if v, ok := tag.vals["HasNectar"]; ok {
					e.HasNectar = snbtNum(v) != 0
				}
				if v, ok := tag.vals["HasStung"]; ok {
					e.Stung = snbtNum(v) != 0
				}
				if v, ok := tag.vals["TicksSincePollination"]; ok {
					e.TicksSincePollination = int32(snbtNum(v))
				}
				if v, ok := tag.vals["CannotEnterHiveTicks"]; ok {
					e.CannotEnterHive = int32(snbtNum(v))
				}
				if v, ok := tag.vals["CropsGrownSincePollination"]; ok {
					e.CropsGrownSincePoll = int32(snbtNum(v))
				}
				if v, ok := tag.vals["hive_pos"]; ok && len(v.items) == 3 {
					for i := 0; i < 3; i++ {
						e.BeeHivePos[i] = int32(snbtNum(v.items[i]))
					}
					e.BeeHivePosSet = true
				}
				if v, ok := tag.vals["flower_pos"]; ok && len(v.items) == 3 {
					for i := 0; i < 3; i++ {
						e.BeeFlowerPos[i] = int32(snbtNum(v.items[i]))
					}
					e.BeeFlowerPosSet = true
				}
			}
			// Variant defaults when the summon NBT left the key out —
			// each type's own spawn roll: axolotl uniform common +
			// 1/1200 rare, parrot uniform, rabbit's biome roll
			// (plains → brown/salt/black), tropical fish a
			// COMMON_VARIANTS pick (packed), fox red (snow in
			// snowy biomes), panda NORMAL/NORMAL.
			switch name {
			case "axolotl":
				if _, ok := tag.vals["Variant"]; !ok {
					e.Variant = rand.Int31n(4)
					if rand.Float64() < 1.0/1200 {
						e.Variant = 4 // blue — the rare morph
					}
				}
			case "parrot":
				if _, ok := tag.vals["Variant"]; !ok {
					e.Variant = rand.Int31n(5)
				}
			case "rabbit":
				if _, ok := tag.vals["RabbitType"]; !ok {
					// getRandomRabbitVariant's default branch:
					// i<3 → BROWN/SALT/BLACK.
					switch rand.Int31n(3) {
					case 0:
						e.Variant = 0 // BROWN
					case 1:
						e.Variant = 5 // SALT
					default:
						e.Variant = 2 // BLACK
					}
				}
			case "tropical_fish":
				if _, ok := tag.vals["Variant"]; !ok {
					// COMMON_VARIANTS is a fixed table of packed
					// (pattern|base|patternColor) ids — any valid
					// packed value keeps the wire/NBT shape.
					e.Variant = 917505 // kob/orange/white packed
				}
			case "panda":
				if e.GeneMain == "" {
					e.GeneMain = "normal"
				}
				if e.GeneHidden == "" {
					e.GeneHidden = "normal"
				}
			}
			// Chicken.readAdditionalSaveData: IsChickenJockey gates the
			// aiStep egg lay, EggLayTime loads the countdown.
			if name == "chicken" {
				if v, ok := tag.vals["IsChickenJockey"]; ok && snbtNum(v) > 0 {
					e.ChickenJockey = true
				}
				if v, ok := tag.vals["EggLayTime"]; ok {
					e.EggLayTime = int32(snbtNum(v))
				}
			}
			// Vex life_ticks/bound_pos, Evoker SpellTicks, Ravager
			// AttackTick/StunTick/RoarTick (hostile.go).
			if hostileGoalTypes[name] {
				s.hostileSummonNBT(e, name, tag)
			}
			// Entity.load's Passengers list: each compound is an entity
			// NBT spawned recursively — the rider mounts like
			// startRiding, so the summon ends with the vehicle's
			// set_passengers packet behind its own bundle.
			if pv, ok := tag.vals["Passengers"]; ok && pv.kind == snbtList {
				s.summonPassengers(d, e, pv.items)
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
		if !e.Dormant {
			s.broadcastFreshMob(e)
		}
		// Leashable.restoreLeashFromSave: a 3-int array is the knot's
		// block pos (the knot is created when absent), a 4-int array the
		// holder's UUID int-array. Only Leashable entities carry the
		// field. The attach runs after the pairing broadcast went out —
		// the leash is unresolved at sendPairingData time on vanilla, so
		// set_entity_link rides loose behind the knot's own bundle.
		if tag != nil {
			if lv, ok := tag.vals["leash"]; ok && lv.kind == snbtList && s.isLeashable(e) {
				switch len(lv.items) {
				case 3:
					pos := world.Pos{X: int(snbtNum(lv.items[0])), Y: int(snbtNum(lv.items[1])), Z: int(snbtNum(lv.items[2]))}
					knot := s.leashKnotAt(d, pos)
					if knot == nil {
						knot = s.leashKnotCreate(d, pos)
					}
					if knot != nil {
						s.leashSetTo(e, knot.ID)
					}
				case 4:
					var u [16]byte
					for i, it := range lv.items {
						v := uint32(int32(snbtNum(it)))
						u[i*4] = byte(v >> 24)
						u[i*4+1] = byte(v >> 16)
						u[i*4+2] = byte(v >> 8)
						u[i*4+3] = byte(v)
					}
					for _, snap := range s.entities.All() {
						if snap.Dim == d.name && snap.UUID == u {
							s.leashSetTo(e, snap.ID)
							break
						}
					}
				}
			}
		}
		return opsEchoReply(translatable("commands.summon.success", entityDisplayName(e)))
	}
	// A dropped stack on the ground: Item holds the stack, Thrower
	// the EntityReference getOwner resolves, PickupDelay the no-grab
	// window.
	if name == "item" {
		it := ItemStack{Count: 1}
		if tag != nil {
			if iv, ok := tag.vals["Item"]; ok {
				if v, ok := snbtToValue(iv).(map[string]any); ok {
					it = itemFromNBT(v)
				}
			}
		}
		// A bare summon carries no Item key: vanilla still accepts —
		// ItemEntity spawns with the air stack ("Summoned new Air")
		// and its own tick discards it next tick.
		e := &Entity{
			Type: mustEntityType("item"),
			Dim:  d.name,
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
			// Entity.load reads Motion and NoGravity for every type —
			// a {Motion:[..]} summon throws the item like a toss.
			if mv, ok := tag.vals["Motion"]; ok && mv.kind == snbtList && len(mv.items) == 3 {
				e.MotionX = snbtNum(mv.items[0])
				e.MotionY = snbtNum(mv.items[1])
				e.MotionZ = snbtNum(mv.items[2])
				e.VX = int16(e.MotionX * 8000)
				e.VY = int16(e.MotionY * 8000)
				e.VZ = int16(e.MotionZ * 8000)
			}
			if nv, ok := tag.vals["NoGravity"]; ok && (nv.num == "1b" || nv.num == "1" || nv.str == "true") {
				e.NoGravity = true
			}
		}
		s.entities.Spawn(e)
		if tag != nil {
			if tv, ok := tag.vals["Tags"]; ok && tv.kind == snbtList {
				var tags []any
				for _, v := range tv.items {
					if v != nil && v.kind == snbtString {
						tags = append(tags, v.str)
					}
				}
				if len(tags) > 0 {
					e.ExtraNBT = map[string]any{"Tags": nbt.List{Elem: nbt.TagString, Items: tags}}
				}
			}
			if v, ok := tag.vals["Health"]; ok {
				e.ItemHP = int32(itemSavedShort(snbtToValue(v), 5))
			}
			if v, ok := tag.vals["Invulnerable"]; ok {
				e.Invulnerable = snbtNum(v) != 0
			}
			if v, ok := tag.vals["Silent"]; ok {
				e.Silent = snbtNum(v) != 0
			}
			if v, ok := tag.vals["Fire"]; ok {
				e.FireTicks = int32(int16(snbtNum(v)))
			}
		}
		if tag != nil {
			if save, ok := snbtToValue(tag).(map[string]any); ok {
				applyItemTouchSave(e, save)
			}
		}
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", entityDisplayName(e)))
	}
	// Boats/rafts and minecarts are vehicles, not living mobs:
	// Passengers NBT recursively spawns riders the way vanilla's
	// entity load does, and Rotation feeds the dismount scan.
	if isBoatEntityName(name) || isMinecartName(name) {
		id, ok := EntityTypeID(name)
		if !ok {
			return textReply("Unknown entity: " + args[0])
		}
		e := s.entities.Spawn(&Entity{
			Type: id,
			Dim:  d.name,
			X:    x, Y: y, Z: z,
		})
		if tag != nil {
			if rv, ok := tag.vals["Rotation"]; ok && len(rv.items) >= 2 {
				e.Yaw = float32(snbtNum(rv.items[0]))
				e.Pitch = float32(snbtNum(rv.items[1]))
				// vanilla's setRot latches yRotO too: without it the
				// off-rail tick's wrap-flip treats a fresh 180-degree
				// yaw as a 170-plus turn and instantly flips the cart.
				e.CartYawO = e.Yaw
			}
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				e.MotionX = snbtNum(mv.items[0])
				e.MotionY = snbtNum(mv.items[1])
				e.MotionZ = snbtNum(mv.items[2])
				e.VX = int16(e.MotionX * 8000)
				e.VY = int16(e.MotionY * 8000)
				e.VZ = int16(e.MotionZ * 8000)
			}
			if pv, ok := tag.vals["PortalCooldown"]; ok {
				e.PortalCooldown = int32(snbtNum(pv))
			}
			if pv, ok := tag.vals["Passengers"]; ok {
				for _, sub := range pv.items {
					pname := strings.TrimPrefix(sub.vals["id"].str, "minecraft:")
					if pname == "" {
						continue
					}
					if g, ok := s.summonMob(d, pname, x, y, z, true); ok {
						g.Riding = e.ID
						e.Passengers = append(e.Passengers, g.ID)
					}
				}
			}
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
		}
		if name == "command_block_minecart" {
			s.commandMinecartLoadTag(e, tag)
		} else {
			save := entitySaveMap(e, s.gameTime(), s.entities).m
			if tag != nil {
				if values, ok := snbtToValue(tag).(map[string]any); ok {
					for key, value := range values {
						save[key] = cloneNBTValue(value)
					}
				}
			}
			s.applyEntitySave(e, save)
		}
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// Non-living summons the command still accepts: projectiles take
	// their Motion list and fly like a bow release.
	if name == "arrow" || name == "spectral_arrow" || name == "trident" {
		mx, my, mz := 0.0, -0.1, 0.0
		e := &Entity{
			Type: mustEntityType(name),
			Dim:  d.name,
			X:    x, Y: y, Z: z,
		}
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				mx = snbtNum(mv.items[0])
				my = snbtNum(mv.items[1])
				mz = snbtNum(mv.items[2])
			}
			// Entity.load reads NoGravity for every type — a NoGravity
			// summon flies flat while air drag still applies.
			if nv, ok := tag.vals["NoGravity"]; ok && (nv.num == "1b" || nv.num == "1" || nv.str == "true") {
				e.NoGravity = true
			}
		}
		e.MotionX, e.MotionY, e.MotionZ = mx, my, mz
		e.VX, e.VY, e.VZ = int16(mx*8000), int16(my*8000), int16(mz*8000)
		// Owner:[I;a,b,c,d] stores an EntityReference to the shooter —
		// its kills credit that player (advancements, death messages).
		if tag != nil {
			if u, ok := snbtUUID(tag, "Owner"); ok {
				if owner := s.sessionByUUID(u); owner != nil {
					e.arrowShooter = owner.entityID.Load()
				}
			}
			// AbstractArrow.readAdditionalSaveData's tail: a stuck
			// arrow resumes through inGround+inBlockState; the rest
			// round-trips plain fields into the save block.
			if lv, ok := tag.vals["life"]; ok {
				e.arrowLife = int32(snbtNum(lv))
			}
			if sv, ok := tag.vals["shake"]; ok {
				e.arrowShake = int32(snbtNum(sv))
			}
			if gv, ok := tag.vals["inGround"]; ok && (gv.num == "1b" || gv.num == "1" || gv.str == "true") {
				e.arrowStuck = true
			}
			if bs, ok := tag.vals["inBlockState"]; ok {
				if bn, ok := bs.vals["Name"]; ok && bn.str != "" {
					e.arrowStuckBlock = strings.TrimPrefix(bn.str, "minecraft:")
				}
			}
			if dv, ok := tag.vals["damage"]; ok {
				e.arrowDamage = snbtNum(dv)
			}
			if pv, ok := tag.vals["pickup"]; ok {
				e.arrowPickup = int32(snbtNum(pv))
			}
			if cv, ok := tag.vals["crit"]; ok && (cv.num == "1b" || cv.num == "1" || cv.str == "true") {
				e.arrowCrit = true
			}
			if pv, ok := tag.vals["PierceLevel"]; ok {
				e.arrowPierce = int32(snbtNum(pv))
			}
			if sv, ok := tag.vals["SoundEvent"]; ok && sv.str != "" {
				e.arrowSound = strings.TrimPrefix(sv.str, "minecraft:")
			}
			if iv, ok := tag.vals["item"]; ok {
				if in, ok := iv.vals["id"]; ok && in.str != "" {
					e.arrowItem = strings.TrimPrefix(in.str, "minecraft:")
				}
			}
		}
		s.entities.Spawn(e)
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// Throwable projectiles: the same Motion-NBT arm as the arrow —
	// snowballs, eggs, pearls, potions and llama spit all spawn with
	// the summon SNBT's Motion list and fly on their own gravities.
	if summonThrownNames[name] {
		mx, my, mz := 0.0, 0.0, 0.0
		e := &Entity{
			Type: mustEntityType(name),
			Dim:  d.name,
			X:    x, Y: y, Z: z,
		}
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				mx = snbtNum(mv.items[0])
				my = snbtNum(mv.items[1])
				mz = snbtNum(mv.items[2])
			}
			if nv, ok := tag.vals["NoGravity"]; ok && (nv.num == "1b" || nv.num == "1" || nv.str == "true") {
				e.NoGravity = true
			}
		}
		e.MotionX, e.MotionY, e.MotionZ = mx, my, mz
		e.VX, e.VY, e.VZ = int16(mx*8000), int16(my*8000), int16(mz*8000)
		s.entities.Spawn(e)
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// The rest of the registry: vanilla's EntityType.create accepts
	// every remaining type — each one spawns at its constructor
	// defaults and runs its own tick fate in ephemeralTickStep.
	if summonPlainNames[name] {
		e := &Entity{
			Type: mustEntityType(name),
			Dim:  d.name,
			X:    x, Y: y, Z: z,
		}
		switch name {
		case "evoker_fangs":
			// Constructor defaults: warmupDelayTicks 0, lifeTicks 22.
			e.FangWarmup, e.FangLife = 0, 22
		case "ominous_item_spawner":
			// spawnItemAfterTicks stays 0 on the summon path — the
			// 60-120 roll only runs in the static create() factory a
			// trial spawner calls — so a bare summon kills tick 1.
			e.OminousSpawnAfter = 0
		case "falling_block":
			// FallingBlockEntity's constructor uses sand when the
			// summon SNBT carries no BlockState.Name.
			e.FallingBlockName = "sand"
		}
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				e.MotionX = snbtNum(mv.items[0])
				e.MotionY = snbtNum(mv.items[1])
				e.MotionZ = snbtNum(mv.items[2])
				e.VX = int16(e.MotionX * 8000)
				e.VY = int16(e.MotionY * 8000)
				e.VZ = int16(e.MotionZ * 8000)
			}
			if bs, ok := tag.vals["BlockState"]; ok && name == "falling_block" {
				if bn, ok := bs.vals["Name"]; ok && bn.str != "" {
					e.FallingBlockName = strings.TrimPrefix(bn.str, "minecraft:")
				}
			}
			if name == "ominous_item_spawner" {
				if v, ok := tag.vals["spawn_item_after_ticks"]; ok {
					e.OminousSpawnAfter = int64(snbtNum(v))
				}
			}
		}
		s.entities.Spawn(e)
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// A small fireball summoned with Motion flies like a dispenser
	// shot — ownerless; acceleration_power rides the bundled motion
	// sync.
	if name == "small_fireball" {
		mx, my, mz := 0.0, 0.0, 0.0
		accel := 0.1
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				mx = snbtNum(mv.items[0])
				my = snbtNum(mv.items[1])
				mz = snbtNum(mv.items[2])
			}
			if av, ok := tag.vals["acceleration_power"]; ok {
				accel = snbtNum(av)
			}
		}
		e := s.entities.Spawn(&Entity{
			Type: mustEntityType(name),
			Dim:  d.name,
			X:    x, Y: y, Z: z,
			MotionX: mx, MotionY: my, MotionZ: mz,
			VX: int16(mx * 8000), VY: int16(my * 8000), VZ: int16(mz * 8000),
			AccelPower: accel,
		})
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	// Wind charges fly straight (no gravity) and burst on contact.
	if name == "wind_charge" || name == "breeze_wind_charge" || name == "wither_skull" || name == "fireball" || name == "dragon_fireball" || name == "small_fireball" || name == "shulker_bullet" {
		mx, my, mz := 0.0, 0.0, 0.0
		dangerous := false
		noGrav := false
		yaw, pitch := float32(0), float32(0)
		if tag != nil {
			if mv, ok := tag.vals["Motion"]; ok && len(mv.items) == 3 {
				mx = snbtNum(mv.items[0])
				my = snbtNum(mv.items[1])
				mz = snbtNum(mv.items[2])
			}
			// Entity.load reads Rotation through setRot — each axis
			// lands as value % 360 (truncated, like Java's frem).
			if rot, ok := tag.vals["Rotation"]; ok && len(rot.items) >= 2 {
				yaw = float32(math.Mod(snbtNum(rot.items[0]), 360))
				pitch = float32(math.Mod(snbtNum(rot.items[1]), 360))
			}
			if name == "wither_skull" {
				if dv, ok := tag.vals["dangerous"]; ok && (dv.num == "1b" || dv.num == "1" || dv.str == "true") {
					dangerous = true
				}
			}
			if nv, ok := tag.vals["NoGravity"]; ok && (nv.num == "1b" || nv.num == "1" || nv.str == "true") {
				noGrav = true
			}
		}
		// LargeFireball's ExplosionPower NBT default is the ghast
		// power 1; acceleration_power's is 0.1.
		power := float32(1)
		accel := 0.1
		if tag != nil {
			if pv, ok := tag.vals["ExplosionPower"]; ok {
				power = float32(snbtNum(pv))
			}
			if av, ok := tag.vals["acceleration_power"]; ok {
				accel = snbtNum(av)
			}
		}
		e := s.entities.Spawn(&Entity{
			Type: mustEntityType(name),
			Dim:  d.name,
			X:    x, Y: y, Z: z,
			Yaw:     yaw,
			Pitch:   pitch,
			MotionX: mx, MotionY: my, MotionZ: mz,
			VX: int16(mx * 8000), VY: int16(my * 8000), VZ: int16(mz * 8000),
			ExplosionPower: power,
			AccelPower:     accel,
			Dangerous:      dangerous,
			NoGravity:      noGrav,
		})
		// Projectile.load's Owner — an EntityReference to the
		// shooter; mc-go resolves it to a live session player.
		if tag != nil {
			if u, ok := snbtUUID(tag, "Owner"); ok {
				if owner := s.sessionByUUID(u); owner != nil {
					e.ProjOwner = u
					e.HasProjOwner = true
				}
			}
		}
		// readAdditionalSaveData routes dangerous through
		// entityData.set — a non-default write dirties the synched
		// data, which re-opens sendChanges for one extra bundle.
		if dangerous {
			e.MetaDirty = true
		}
		s.broadcastEntitySpawn(nil, *e)
		return opsEchoReply(translatable("commands.summon.success", translatable("entity.minecraft."+name)))
	}
	if name == "lightning_bolt" {
		e := s.entities.Spawn(&Entity{
			Type: mustEntityType("lightning_bolt"),
			Dim:  d.name,
			X:    x, Y: y, Z: z,
		})
		s.broadcastEntitySpawn(nil, *e)
		// A bolt that lands on a lightning rod sets no fires — vanilla's
		// type_specific/lightning.blocks_set_on_fire stays zero and the
		// Surge Protector predicate matches.
		fires := 1
		if n, ok := blockNameAt(d, world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)) - 1, Z: int(math.Floor(z))}); ok && strings.HasSuffix(n, "lightning_rod") {
			fires = 0
		}
		if n, ok := blockNameAt(d, world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}); ok && strings.HasSuffix(n, "lightning_rod") {
			fires = 0
		}
		s.fireLightningStrike(d, x, y, z, fires)
		s.thunderStrikeEntities(d, e, x, y, z, nil)
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
	e, ok := s.summonMobInitSpawn(d, name, x, y, z, noAI, init)
	if !ok {
		return nil, false
	}
	// Vanilla parks an entity spawned into a dimension holding no
	// players: its chunk never tickets, so it sits dormant —
	// invisible to @e selectors and the tracker — until a player
	// arrives (measured: `execute in nether run summon` produces an
	// entity no selector can read).
	e.Dormant = s.shouldPark(d.name)
	if !e.Dormant {
		s.broadcastFreshMob(e)
	}
	return e, true
}

func (s *Server) summonMobInitSpawn(d *dimension, name string, x, y, z float64, noAI, init bool) (*Entity, bool) {
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
	if name == "turtle" {
		// Turtle.readAdditionalSaveData's home_pos read orElse's
		// the entity's block position — every spawn path lands it.
		e.TurtleHome = world.Pos{X: int(e.X), Y: int(e.Y), Z: int(e.Z)}
	}
	if name == "trader_llama" {
		// TraderLlama's ctor field-init — readAdditionalSaveData
		// keeps it when the summon NBT omits DespawnDelay.
		e.DespawnDelay = 47999
	}
	summonAttrTouches(e, name, init)
	return e, true
}

// summonMobRaw is summonMob without the broadcast: the caller applies
// summon NBT first so the tracker-attach burst carries the final
// metadata and equipment. summonMob stays for callers with no NBT.
func (s *Server) summonMobRaw(d *dimension, name string, x, y, z float64, noAI, init bool) (*Entity, bool) {
	e, ok := s.summonMobRawSpawn(d, name, x, y, z, noAI, init)
	if !ok {
		return nil, false
	}
	// A summon landing in a player-less dimension parks like vanilla's
	// unloaded-chunk entity: `execute in nether run summon` creates a
	// mob no selector resolves until someone travels there.
	e.Dormant = s.shouldPark(d.name)
	return e, true
}

func (s *Server) summonMobRawSpawn(d *dimension, name string, x, y, z float64, noAI, init bool) (*Entity, bool) {
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
	// Marker is a plain Entity — no physics, no living attributes.
	if name == "marker" {
		e := s.entities.Spawn(&Entity{Type: id, Dim: d.name, X: x, Y: y, Z: z})
		return e, true
	}
	// ExperienceOrb likewise: count starts at 1 (merge accounting),
	// OrbValue comes from the summon tag's Value key.
	if name == "experience_orb" {
		e := s.entities.Spawn(&Entity{Type: id, Dim: d.name, X: x, Y: y, Z: z, OrbCount: 1})
		return e, true
	}
	// FireworkRocketEntity likewise — life and lifetime stay 0
	// without NBT, so a bare summon pops on the first tick like
	// vanilla's summoned rocket.
	if name == "firework_rocket" {
		e := s.entities.Spawn(&Entity{Type: id, Dim: d.name, X: x, Y: y, Z: z})
		return e, true
	}
	// PrimedTnt likewise — a summoned one starts at
	// DEFAULT_FUSE_TICKS (80) with no prime kick; {fuse:n} and
	// {explosion_power:n} override below.
	if name == "tnt" {
		e := s.entities.Spawn(&Entity{Type: id, Dim: d.name, X: x, Y: y, Z: z, Fuse: tntFuseTicks})
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
	// /summon defaults Rotation to [0,0] — vanilla's EntityType.create
	// keeps the fresh entity's zeroed rotation when the SNBT has none.
	// The random facing on natural spawns is the mob's wander goal
	// picking a heading, not the summon.
	e.HeadYaw, e.LookYaw = e.Yaw, e.Yaw
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
	// Chicken's constructor seeds eggTime — the lay countdown runs
	// from creation like any spawned chicken.
	if name == "chicken" {
		e.EggLayTime = 6000 + rand.Int31n(6000)
	}
	if name == "trader_llama" {
		// TraderLlama's ctor field-init — readAdditionalSaveData
		// keeps it when the summon NBT omits DespawnDelay.
		e.DespawnDelay = 47999
	}
	summonAttrTouches(e, name, init)
	if init && name == "phantom" {
		// Phantom.finalizeSpawn's anchor (phantom.go).
		s.phantomSummonInit(e)
	}
	return e, true
}

// summonPassengers is Entity.load's Passengers recursion for the
// living-mob summon path: each compound loads its own id + NBT
// (mirroring the top-level keys this summon arm reads) and rides the
// new vehicle like startRiding — the rider welds to the rotated
// PASSENGER attachment immediately, then set_passengers ships.
func (s *Server) summonPassengers(d *dimension, veh *Entity, items []*snbtTag) {
	for _, sub := range items {
		if sub == nil {
			continue
		}
		pname := strings.TrimPrefix(sub.vals["id"].str, "minecraft:")
		if pname == "" {
			continue
		}
		noAI := false
		if v, ok := sub.vals["NoAI"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
			noAI = true
		}
		g, ok := s.summonMobRaw(d, pname, veh.X, veh.Y, veh.Z, noAI, false)
		if !ok {
			continue
		}
		// The passenger key set mirrors the rider-load keys vanilla's
		// Entity.load passes through: IsBaby where the class reads it,
		// Age/InLove on AgeableMobs, Health, Motion, Rotation, Silent,
		// PersistenceRequired — then a nested Passengers list stacks
		// another vehicle level.
		if v, ok := sub.vals["IsBaby"]; ok && snbtNum(v) > 0 && isBabyTypes[pname] {
			g.Baby = true
		}
		if v, ok := sub.vals["Age"]; ok && ageableTypes[pname] {
			g.Age = int32(snbtNum(v))
			g.Baby = g.Age < 0
		}
		if v, ok := sub.vals["Health"]; ok {
			g.Health = float32(snbtNum(v))
		}
		if v, ok := sub.vals["PersistenceRequired"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
			g.PersistenceRequired = true
		}
		if v, ok := sub.vals["Silent"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
			g.Silent = true
		}
		if v, ok := sub.vals["IsChickenJockey"]; ok && snbtNum(v) > 0 && pname == "chicken" {
			g.ChickenJockey = true
		}
		// Entity.load's Tags list applies to nested riders the same
		// as the top-level summon — @e[tag=...] must see them.
		if tv, ok := sub.vals["Tags"]; ok && tv.kind == snbtList {
			var items []any
			for _, it := range tv.items {
				if it != nil && it.kind == snbtString {
					items = append(items, it.str)
				}
			}
			if len(items) > 0 {
				if g.ExtraNBT == nil {
					g.ExtraNBT = map[string]any{}
				}
				g.ExtraNBT["Tags"] = nbt.List{Elem: nbt.TagString, Items: items}
			}
		}
		if pv, ok := sub.vals["Passengers"]; ok && pv.kind == snbtList {
			s.summonPassengers(d, g, pv.items)
		}
		// startRiding + positionRider: link both ends, weld the rider
		// at vehicle pos + rotated PASSENGER attach − rider's own
		// VEHICLE attach (AT_FEET → 0).
		g.Riding = veh.ID
		veh.Passengers = append(veh.Passengers, g.ID)
		g.X, g.Y, g.Z = passengerSeatPos(veh, g)
		g.WeldedPos = &[3]float64{g.X, g.Y, g.Z}
		s.broadcastFreshMob(g)
	}
	if len(veh.Passengers) > 0 {
		s.broadcastSetPassengers(veh)
	}
}

// spawnChickenJockey is Zombie.finalizeSpawn's jockey block: gated
// on ZombieGroupData(isBaby && canSpawnJockey), a first 5% roll
// grabs the first non-ridden chicken inside the rider's
// inflate(5,3,5) bounding box; when it misses a second 5% spawns a
// fresh JOCKEY-reason chicken and rides it. Both paths flag
// setChickenJockey so the mount stops laying eggs.
func (s *Server) spawnChickenJockey(e *Entity) {
	const w, h = 0.6, 1.95 // zombie bounding box
	if rand.Float64() < 0.05 {
		// getEntitiesOfClass(Chicken, bb.inflate(5,3,5), NOT_RIDDEN):
		// AABB intersect — the candidate's own box overlaps the
		// inflated zombie box.
		var mountID int32
		found := false
		for _, o := range s.entities.All() {
			oname, _ := EntityTypeName(o.Type)
			if oname != "chicken" || o.Dim != e.Dim || o.Dying ||
				o.Riding != 0 || len(o.Passengers) > 0 {
				continue
			}
			const cw, ch = 0.4, 0.7
			if o.X+cw/2 < e.X-w/2-5 || o.X-cw/2 > e.X+w/2+5 ||
				o.Z+cw/2 < e.Z-w/2-5 || o.Z-cw/2 > e.Z+w/2+5 ||
				o.Y+ch < e.Y-3 || o.Y > e.Y+h+3 {
				continue
			}
			mountID = o.ID
			found = true
			break
		}
		if !found {
			return
		}
		if mp, ok := s.entities.GetPtr(mountID); ok {
			s.rideMobOnto(e, mp)
		}
		return
	}
	if rand.Float64() >= 0.05 {
		return
	}
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	chicken, ok := s.summonMobRaw(d, "chicken", e.X, e.Y, e.Z, false, true)
	if !ok {
		return
	}
	chicken.Yaw = e.Yaw
	chicken.HeadYaw, chicken.LookYaw = e.Yaw, e.Yaw
	s.broadcastFreshMob(chicken)
	s.rideMobOnto(e, chicken)
}

// rideMobOnto is the mount side of startRiding for a mob passenger:
// flag the chicken mount, link both ends, weld the rider onto the
// rotated PASSENGER attachment, ship set_passengers.
func (s *Server) rideMobOnto(rider, mount *Entity) {
	if mname, _ := EntityTypeName(mount.Type); mname == "chicken" {
		mount.ChickenJockey = true
	}
	// Mob.startRiding: a leashed rider drops its leash first —
	// dropLeash() drops the lead item unconditionally.
	if rider.LeashHolder != 0 {
		s.leashDrop(rider, true)
	}
	rider.Riding = mount.ID
	mount.Passengers = append(mount.Passengers, rider.ID)
	rider.X, rider.Y, rider.Z = passengerSeatPos(mount, rider)
	rider.WeldedPos = &[3]float64{rider.X, rider.Y, rider.Z}
	rider.PosDirty = true
	rider.NeedsSync = true
	s.mobSyncFlush(rider)
	s.broadcastSetPassengers(mount)
}

// chickenLayEgg is the dropFromGiftLootTable(CHICKEN_LAY) half of
// Chicken.aiStep: an egg ItemEntity at the mob's own position
// (spawnAtLocation lifts it half the bounding height), the
// entity.chicken.egg sound, and a pickup delay of 40.
func (s *Server) chickenLayEgg(e *Entity) {
	egg, ok := world.ItemID("egg")
	if !ok {
		return
	}
	itemType, ok := EntityTypeID("item")
	if !ok {
		return
	}
	ie := s.entities.Spawn(&Entity{
		Type:        itemType,
		Dim:         e.Dim,
		X:           e.X,
		Y:           e.Y + entityBBDims(e)[1]*0.5,
		Z:           e.Z,
		MotionX:     rand.Float64()*0.2 - 0.1,
		MotionY:     0.2,
		MotionZ:     rand.Float64()*0.2 - 0.1,
		Item:        ItemStack{ID: egg, Count: 1},
		PickupDelay: 40,
	})
	s.broadcastEntitySpawn(nil, *ie)
	// ENTITY_PLACE's game-event half is a no-op on the flat harness
	// (no listeners) — the sound is the observable half.
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 352, mobVoiceOf(e).src(), 1.0, voicePitchE(e), nil)
}

// mobRemovedPassengerCleanup is Entity.remove's passenger half:
// ejectPassengers drops each rider with the 60-tick boardingCooldown
// (rider position already equals the welded seat — no lift), and
// removePassenger pulls the dying entity out of its vehicle's
// set_passengers list.
func (s *Server) mobRemovedPassengerCleanup(e *Entity) {
	for _, pid := range e.Passengers {
		if rp, ok := s.entities.GetPtr(pid); ok {
			// LivingEntity.dismountVehicle for a removed vehicle:
			// the rider keeps (riderX, max(riderY, vehY), riderZ) —
			// welded already — and falls from there.
			rp.Riding = 0
			if e.Y > rp.Y {
				rp.Y = e.Y
			}
			// The welded box persists at the dismount spot until the
			// rider moves (a NoAI rider floats there forever).
			rp.WeldedPos = &[3]float64{rp.X, rp.Y, rp.Z}
			rp.BoardingCooldown = 60
			rp.PosDirty = true
			s.mobSyncFlush(rp)
		}
	}
	e.Passengers = e.Passengers[:0]
	if e.Riding != 0 {
		if veh, ok := s.entities.GetPtr(e.Riding); ok {
			keep := veh.Passengers[:0]
			for _, pid := range veh.Passengers {
				if pid != e.ID {
					keep = append(keep, pid)
				}
			}
			veh.Passengers = keep
			s.broadcastSetPassengers(veh)
		}
		e.Riding = 0
	}
}

// broadcastFreshMob is vanilla's tracker-attach burst: spawn packet,
// entity metadata, and every occupied equipment slot. When equipment
// was loaded from summon NBT each slot is still dirty, so the next
// detectEquipmentUpdates tick resends the whole set — EquipDirty
// replays that second burst on the next mob tick.
func (s *Server) broadcastFreshMob(e *Entity) {
	if e.Dormant {
		// Parked in an unticketed chunk — nothing tracks it, so the
		// attach burst has no destination.
		return
	}
	if cn, ok := EntityTypeName(e.Type); ok && cn == "marker" {
		// Marker is a server-side-only entity — vanilla sends the
		// client no spawn or metadata packet for it.
		e.MetaDirty = true
		e.PosDirty = true
		e.NeedsSync = true
		return
	}
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
		e.NeedsSync = true
		e.SyncCount = 0
		return
	}
	if cn, ok := EntityTypeName(e.Type); ok && cn == "experience_orb" {
		s.broadcastEntityMetadata(e.ID, encodeOrbMetadata(e.OrbValue))
		e.MetaDirty = true
		e.PosDirty = true
		e.NeedsSync = true
		e.SyncCount = 0
		return
	}
	// No separate set_entity_data: evalPair's sendEntityTo already
	// shipped the living meta inside the attach bundle — vanilla's
	// sendPairingData carries exactly one entity data packet.
	// sendPairingData also flushes the instantiated attribute table at
	// pair time (pairingAttrs inside the attach bundle). The dirty set
	// stays latched so the next tracker tick's sendChanges replays it —
	// vanilla emits the load-time dirty attrs a second time there —
	// and movement_speed's first-travel touch lands the tick after.
	e.MetaDirty = true
	e.AttrDirty = true
	e.PosDirty = true
	e.NeedsSync = true
	e.SyncCount = 0
	e.ParticlesSent = nil
	if len(armorStandEquipAll(e)) > 0 {
		// Equipment rides the pairing bundle (sendEntityTo) and
		// replays once via the first tick's EquipDirty flush —
		// vanilla's sendPairingData + the empty-baseline
		// detectEquipmentUpdates emit — so no separate emit here.
		e.EquipDirty = true
	}
}

// encodeArmorStandFlags packs ArmorStand's DATA_CLIENT_FLAGS byte
// (metadata index 15): 0x01 small, 0x04 show arms, 0x08 no baseplate,
// 0x10 marker. Native flags are read by summon and entity load.
func encodeArmorStandFlags(e *Entity) byte {
	var flags byte
	if e.ArmorStandMarker {
		flags |= 0x10
	}
	if e.Small {
		flags |= 0x01
	}
	if e.ShowArms {
		flags |= 0x04
	}
	return flags
}

// livingMeta snapshots the metadata-field values a pack carries —
// the mc-go side of SynchedEntityData's dirty set. A packDirty emit
// (encodeLivingMetadataDirty) sends only the entries whose current
// value differs from the last sent one; a pack emit
// (encodeLivingMetadata, the attach burst) sends non-default entries
// like before. Either way the emitted state is stamped back into
// e.metaSent so the next diff measures from what the client saw.
type livingMeta struct {
	air        int16
	name       string
	nameVis    bool
	frozen     int32
	health     float32
	asFlags    byte
	mobFlags   byte
	sheared    bool // sheep wool byte / bogged mushrooms
	color      int8 // sheep wool byte low nibble
	moving     bool
	beam       int32
	pumpkin    bool
	baby       bool
	waterConv  bool
	converting bool
	villType   int32
	villProf   int32
	villLevel  int32
	villFinal  bool
	freezeConv bool
	powered    bool
	ignited    bool
	swell      int32
	mooVar     int32
	interested bool
	collar     int32
	angerEnd   int64
	beeFlags   byte
	beeAnger   int64
	hVarID     int32
	hSoundID   int32
	boost      int32
	suffoc     bool
	dancing    bool
	canDup     bool
	catLying   bool
	catRelax   bool
	pose       int32
	tongueTgt  int32
	cubeSize   int32
	tameFlags  byte
	ownerUUID  [16]byte
	eqFlags    byte
	dash       bool
	eqVar      int32
	llamaStr   int32
	llamaVar   int32
	ageLock    bool
	bucket     bool
	puff       int32
	foxFlags   byte
	foxTrusted [][16]byte
	poseTick   int64
	creepy     bool
	stared     bool
	carry      int32
}

// snapLivingMeta captures the metadata-field values a fresh pack would
// encode — the post-emit baseline the next dirty diff compares to.
func snapLivingMeta(e *Entity) livingMeta {
	return livingMeta{
		air:        e.AirSupply,
		name:       e.CustomName,
		nameVis:    e.CustomNameVisible,
		frozen:     e.TicksFrozen,
		health:     e.Health,
		asFlags:    encodeArmorStandFlags(e),
		mobFlags:   mobFlagsByte(e),
		sheared:    e.Sheared,
		color:      e.Color,
		moving:     e.Moving,
		beam:       e.beamTarget,
		pumpkin:    e.Pumpkin,
		baby:       e.Baby,
		waterConv:  e.WaterConv,
		converting: e.Converting,
		villType:   e.VillagerTypeID,
		villProf:   e.VillagerProfID,
		villLevel:  e.VillagerLevel,
		villFinal:  e.VillagerFinalized,
		freezeConv: e.FreezeConv,
		powered:    e.Powered,
		ignited:    e.Ignited,
		swell:      e.SwellDir,
		interested: e.Interested,
		collar:     collarColorValue(e),
		angerEnd:   angerEndSynced(e),
		beeFlags:   beeFlagsValue(e),
		beeAnger:   angerEndSynced(e),
		hVarID:     holderVariantIDOf(e),
		hSoundID:   holderSoundVariantIDOf(e),
		boost:      int32(e.BoostTimeTotal),
		suffoc:     e.Suffocating,
		dancing:    e.Dancing,
		canDup:     e.DupCooldown <= 0,
		catLying:   e.CatLying,
		catRelax:   e.CatRelax,
		pose:       e.Pose,
		tongueTgt:  e.FrogTongueTarget,
		cubeSize:   cubeSizeOf(e),
		tameFlags:  tamableFlagsByte(e),
		ownerUUID:  e.ownerUUID,
		eqFlags:    equineFlagsByte(e),
		dash:       e.Dashing,
		eqVar:      e.Variant,
		llamaStr:   e.Strength,
		llamaVar:   e.LlamaVariant,
		ageLock:    e.AgeLocked,
		bucket:     e.FromBucket,
		puff:       e.PuffState,
		foxFlags:   foxFlagsByte(e),
		foxTrusted: append([][16]byte{}, e.FoxTrusted...),
		poseTick:   e.LastPoseTick,
		creepy:     e.EnderCreepy,
		stared:     e.EnderStared,
		carry:      enderCarryState(e),
	}
}

// foxFlagsByte is Fox's DATA_FLAGS_ID byte (idx19): 0x01 sitting,
// 0x04 crouching, 0x20 sleeping.
func foxFlagsByte(e *Entity) byte {
	var v byte
	if e.Sitting {
		v |= 0x01
	}
	if e.Crouching {
		v |= 0x04
	}
	if e.Sleeping {
		v |= 0x20
	}
	return v
}

// tamableFlagsByte is TamableAnimal's DATA_FLAGS_ID byte: 0x01
// inSittingPose + 0x04 tamed, packed at idx18 on wolf/cat/parrot.
// orderedToSit (e.Sitting) is an NBT-only field in 26.2 — it moves
// no wire bits; only the sit goal's inSittingPose does.
func tamableFlagsByte(e *Entity) byte {
	var v byte
	if e.SittingPose {
		v |= 0x01
	}
	if e.tamed {
		v |= 0x04
	}
	return v
}

// tamableMetaEmit diffs the tamable pair on the dirty path: idx18
// flags byte + idx19 DATA_OWNERUUID_ID (optional entity reference —
// present bool then 16-byte uuid). A zero uuid is Optional.empty.
func tamableMetaEmit(b *protocol.Buffer, e *Entity, last *livingMeta) {
	if fl := tamableFlagsByte(e); fl != last.tameFlags {
		b.Byte(18)
		b.Byte(0)
		b.Byte(fl)
	}
	if e.ownerUUID != last.ownerUUID {
		b.Byte(19)
		b.Byte(13)
		if e.ownerUUID != ([16]byte{}) {
			b.Bool(true)
			b.UUID(e.ownerUUID)
		} else {
			b.Bool(false)
		}
	}
}

// tamableMetaPack is the attach path: the defined defaults (flags 0,
// empty owner) stay unpacked.
func tamableMetaPack(b *protocol.Buffer, e *Entity) {
	if fl := tamableFlagsByte(e); fl != 0 {
		b.Byte(18)
		b.Byte(0)
		b.Byte(fl)
	}
	if e.ownerUUID != ([16]byte{}) {
		b.Byte(19)
		b.Byte(13)
		b.Bool(true)
		b.UUID(e.ownerUUID)
	}
}

// holderVariantIDOf resolves the entity's holder-variant id with the
// type's defined default on an empty name.
func holderVariantIDOf(e *Entity) int32 {
	name, _ := EntityTypeName(e.Type)
	return holderVariantID(name, e.VariantName)
}

// holderSoundVariantIDOf does the same for the sound-variant holder.
func holderSoundVariantIDOf(e *Entity) int32 {
	name, _ := EntityTypeName(e.Type)
	return holderSoundVariantID(name, e.SoundVariant)
}

// ensureMetaInit seeds the dirty baseline at the defined defaults the
// way SynchedEntityData.define does: entries start clean at the define
// value — air supply 300, health 1.0, a snow golem's pumpkin on, and
// the type's villagerdata default (zombie villager's is the random
// defined profession; a villager's is none) — so the first packDirty
// re-emits exactly the summon-time non-default set like vanilla's
// post-load sendChanges, not nothing or everything.
func (e *Entity) ensureMetaInit() {
	if e.metaInited {
		return
	}
	e.metaInited = true
	e.metaSent = livingMeta{air: 300, health: 1.0, swell: -1, pumpkin: true, villType: 2, villLevel: 1}
	e.metaSent.villProf = e.VillagerProfDefault
	if n, _ := EntityTypeName(e.Type); n == "villager" {
		e.metaSent.villProf = 0
	}
	// The wolf/bee baselines are the DEFINE defaults, not the resolved
	// values: vanilla's dirty flags survive the attach pack, so the
	// first post-pair sendChanges replays every non-default entry a
	// second time — matching what the other fields' fixed seeds do.
	if n, _ := EntityTypeName(e.Type); n != "" {
		e.metaSent.collar = 14 // COLLAR default: red
		e.metaSent.angerEnd = -1
		e.metaSent.beeAnger = -1
		e.metaSent.hVarID = holderVariantID(n, "")
		e.metaSent.hSoundID = holderSoundVariantID(n, "")
		// Equine DEFINE defaults: DATA_ID_FLAGS 0, DASH false,
		// STRENGTH/VARIANT/type-variant all 0 — a summoned-with-NBT
		// mount replays each non-default value after the attach.
		e.metaSent.eqFlags = 0
		e.metaSent.dash = false
		e.metaSent.eqVar = 0
		e.metaSent.llamaStr = 0
		e.metaSent.llamaVar = 0
		e.metaSent.ageLock = false
		e.metaSent.bucket = false
		e.metaSent.foxFlags = 0
		e.metaSent.foxTrusted = nil
		e.metaSent.poseTick = 0
	}
}

// ageableBabyTypes are the AgeableMob subclasses whose inherited
// DATA_BABY_ID rides metadata index 16 as a boolean — measured on pig,
// cow, sheep, chicken, wolf and villager. Types with their own
// per-type cases below (zombie family, zombie_villager) pack the same
// index through those cases instead.
var ageableBabyTypes = map[string]bool{
	"pig": true, "cow": true, "mooshroom": true, "sheep": true,
	"chicken": true, "wolf": true, "cat": true, "ocelot": true,
	"fox": true, "panda": true, "polar_bear": true, "rabbit": true,
	"goat": true, "turtle": true, "axolotl": true,
	"bee": true, "dolphin": true, "hoglin": true, "sniffer": true,
	"camel": true, "armadillo": true, "horse": true, "donkey": true,
	"mule": true, "llama": true, "trader_llama": true,
	"squid": true, "glow_squid": true,
}

// packsBabyAt16 reports whether the type's metadata layout carries a
// baby flag at index 16 — the AgeableMob family plus the zombie line
// (zombie/husk/drowned/zombie_villager) and villager, which define
// their own DATA_BABY at the same index.
func packsBabyAt16(name string) bool {
	if ageableBabyTypes[name] {
		return true
	}
	switch name {
	case "zombie", "husk", "drowned", "zombie_villager", "villager", "sheep":
		return true
	}
	return false
}

// encodeLivingMetadataDirty is the packDirty counterpart of
// encodeLivingMetadata: it emits only the entries whose value changed
// since the last pack the client saw, records the new baseline, and
// returns nil when nothing moved — vanilla's sendChanges skips the
// entity-data packet entirely on an empty dirty set. Particles ride
// the pack like the full encoder's, gated on a value change.
func encodeLivingMetadataDirty(e *Entity, particles []byte) []byte {
	var b protocol.Buffer
	e.ensureMetaInit()
	last := e.metaSent
	if e.AirSupply != last.air {
		b.Byte(1)
		b.Byte(1) // type: varint
		b.VarInt(int32(e.AirSupply))
	}
	if e.CustomName != last.name {
		b.Byte(2)
		b.Byte(6) // type: optional component
		if e.CustomName == "" {
			b.Byte(0)
		} else {
			b.Byte(1)
			writeComponentTag(&b, e.CustomName, e.CustomNameComp)
		}
	}
	if e.CustomNameVisible != last.nameVis {
		b.Byte(3)
		b.Byte(8) // type: boolean
		b.Bool(e.CustomNameVisible)
	}
	entityNoGravityDirty(&b, e)
	// DATA_POSE (6, pose serializer) — shared base-entity slot; it
	// rides the diff on every pose transition (camel sit/stand, frog
	// croak/tongue).
	if e.Pose != last.pose {
		b.Byte(6)
		b.Byte(20) // type: pose
		b.VarInt(e.Pose)
	}
	if e.TicksFrozen != last.frozen {
		b.Byte(7)
		b.Byte(1) // type: varint
		b.VarInt(e.TicksFrozen)
	}
	if e.Health != last.health {
		b.Byte(9)
		b.Byte(3) // type: float
		b.Float(e.Health)
	}
	if e.Type == mustEntityType("armor_stand") {
		if fl := encodeArmorStandFlags(e); fl != last.asFlags {
			b.Byte(15)
			b.Byte(0) // type: byte
			b.Byte(fl)
		}
		e.metaSent = snapLivingMeta(e)
		if len(b.Bytes()) == 0 {
			return nil
		}
		b.Byte(0xFF)
		return b.Bytes()
	}
	if len(particles) > 0 && !bytesEq(particles, e.ParticlesSent) {
		b.Byte(10)
		b.Byte(17) // PARTICLES serializer
		b.Raw(particles)
	}
	if fl := mobFlagsByte(e); fl != last.mobFlags {
		b.Byte(15)
		b.Byte(0) // type: byte
		b.Byte(fl)
	}
	dragonMetaDirty(e, &b)
	if name, ok := EntityTypeName(e.Type); ok {
		// AgeableMob's pair leads every ageable chain — DATA_BABY_ID
		// (16) then AGE_LOCKED (17); each subtype's own data starts
		// at 18+, so the hoist keeps packDirty's ascending order.
		if ageableTypes[name] {
			if e.Baby != last.baby {
				b.Byte(16)
				b.Byte(8)
				b.Bool(e.Baby)
			}
			if e.AgeLocked != last.ageLock {
				b.Byte(17)
				b.Byte(8)
				b.Bool(e.AgeLocked)
			}
		}
		if abstractFishTypes[name] && e.FromBucket != last.bucket {
			b.Byte(16)
			b.Byte(8)
			b.Bool(e.FromBucket)
		}
		switch {
		case name == "sheep":
			if e.Sheared != last.sheared || e.Color != last.color {
				b.Byte(18)
				b.Byte(0) // type: byte
				v := byte(e.Color & 15)
				if e.Sheared {
					v |= 0x10
				}
				b.Byte(v)
			}
		case name == "mooshroom":
			// MushroomCow DATA_TYPE (index 18, int): 0 red / 1 brown.
			if e.MooshroomVariant != last.mooVar {
				b.Byte(18)
				b.Byte(1) // type: varint
				b.VarInt(e.MooshroomVariant)
			}
		case flyerMobs[name]:
			flyerMetaDirty(&b, e, name)
		case name == "guardian" || name == "elder_guardian":
			if e.Moving != last.moving {
				b.Byte(16)
				b.Byte(8) // type: boolean
				b.Bool(e.Moving)
			}
			if e.beamTarget != last.beam {
				b.Byte(17)
				b.Byte(1) // type: varint
				b.VarInt(e.beamTarget)
			}
		case name == "snow_golem":
			if e.Pumpkin != last.pumpkin {
				b.Byte(16)
				b.Byte(0) // type: byte
				v := byte(0)
				if e.Pumpkin {
					v = 1
				}
				b.Byte(v)
			}
		case name == "bogged":
			if e.Sheared != last.sheared {
				b.Byte(16)
				b.Byte(8) // type: boolean
				b.Bool(e.Sheared)
			}
		case name == "zombie_villager":
			// Zombie's own DATA_BABY_ID (16) — ZombieVillager is a
			// Zombie, not an AgeableMob.
			if e.Baby != last.baby {
				b.Byte(16)
				b.Byte(8)
				b.Bool(e.Baby)
			}
			if e.WaterConv != last.waterConv {
				b.Byte(17)
				b.Byte(8)
				b.Bool(e.WaterConv)
			}
			if e.Converting != last.converting {
				b.Byte(19)
				b.Byte(8)
				b.Bool(e.Converting)
			}
			if e.VillagerTypeID != last.villType || e.VillagerProfID != last.villProf || e.VillagerLevel != last.villLevel {
				b.Byte(20)
				b.Byte(18) // type: villager_data
				encodeVillagerDataValue(&b, e)
			}
			if e.VillagerFinalized != last.villFinal {
				b.Byte(21)
				b.Byte(8)
				b.Bool(e.VillagerFinalized)
			}
		case name == "villager":
			if e.VillagerTypeID != last.villType || e.VillagerProfID != last.villProf || e.VillagerLevel != last.villLevel {
				b.Byte(19)
				b.Byte(18)
				encodeVillagerDataValue(&b, e)
			}
			if e.VillagerFinalized != last.villFinal {
				b.Byte(20)
				b.Byte(8)
				b.Bool(e.VillagerFinalized)
			}
		case name == "zombie" || name == "husk" || name == "drowned":
			// Zombie's own DATA_BABY_ID (16).
			if e.Baby != last.baby {
				b.Byte(16)
				b.Byte(8)
				b.Bool(e.Baby)
			}
			if e.WaterConv != last.waterConv {
				b.Byte(17)
				b.Byte(8)
				b.Bool(e.WaterConv)
			}
		case name == "skeleton":
			if e.FreezeConv != last.freezeConv {
				b.Byte(16)
				b.Byte(8)
				b.Bool(e.FreezeConv)
			}
		case name == "parrot":
			tamableMetaEmit(&b, e, &last)
			// DATA_VARIANT_ID (20, int): 0 red … 4 gray.
			if e.Variant != last.eqVar {
				b.Byte(20)
				b.Byte(1)
				b.VarInt(e.Variant)
			}
		case name == "wolf":
			tamableMetaEmit(&b, e, &last)
			if e.Interested != last.interested {
				b.Byte(20)
				b.Byte(8)
				b.Bool(e.Interested)
			}
			if c := collarColorValue(e); c != last.collar {
				b.Byte(21)
				b.Byte(1)
				b.VarInt(c)
			}
			if end := angerEndSynced(e); end != last.angerEnd {
				b.Byte(22)
				b.Byte(2)
				b.VarLong(end)
			}
			if id := holderVariantIDOf(e); id != last.hVarID {
				b.Byte(23)
				b.Byte(25)
				b.VarInt(id)
			}
			if id := holderSoundVariantIDOf(e); id != last.hSoundID {
				b.Byte(24)
				b.Byte(26)
				b.VarInt(id)
			}
		case name == "bee":
			if v := beeFlagsValue(e); v != last.beeFlags {
				b.Byte(18)
				b.Byte(0)
				b.Byte(v)
			}
			if end := angerEndSynced(e); end != last.beeAnger {
				b.Byte(19)
				b.Byte(2)
				b.VarLong(end)
			}
		case name == "cat":
			tamableMetaEmit(&b, e, &last)
			if id := holderVariantIDOf(e); id != last.hVarID {
				b.Byte(20)
				b.Byte(21)
				b.VarInt(id)
			}
			// IS_LYING (21, boolean) / RELAX_STATE_ONE (22, boolean)
			// — CatRelaxOnOwnerGoal's synced flags.
			if e.CatLying != last.catLying {
				b.Byte(21)
				b.Byte(8)
				b.Bool(e.CatLying)
			}
			if e.CatRelax != last.catRelax {
				b.Byte(22)
				b.Byte(8)
				b.Bool(e.CatRelax)
			}
			if c := collarColorValue(e); c != last.collar {
				b.Byte(23)
				b.Byte(1)
				b.VarInt(c)
			}
			if id := holderSoundVariantIDOf(e); id != last.hSoundID {
				b.Byte(24)
				b.Byte(22)
				b.VarInt(id)
			}
		case name == "frog":
			// DATA_POSE (6) — ShootTongue's USING_TONGUE(9) during
			// the catch/eat states — emitted by the shared prelude.
			if id := holderVariantIDOf(e); id != last.hVarID {
				b.Byte(18)
				b.Byte(27)
				b.VarInt(id)
			}
			// DATA_TONGUE_TARGET_ID (19, OPTIONAL_UNSIGNED_INT) —
			// the codec writes eid+1, 0 for empty.
			if e.FrogTongueTarget != last.tongueTgt {
				b.Byte(19)
				b.Byte(19)
				tv := e.FrogTongueTarget
				if tv > 0 {
					tv++
				}
				b.VarInt(tv)
			}
		case name == "enderman":
			// DATA_CARRY_STATE (16, OPTIONAL_BLOCK_STATE — VarInt,
			// 0 empty) never leaves the defined default until a
			// block is carried. DATA_CREEPY (17) latches on
			// setTarget; DATA_STARED_AT (18) rides the
			// LookForPlayerGoal's setBeingStaredAt — setTarget(null)
			// drops both.
			if cv := enderCarryState(e); cv != last.carry {
				b.Byte(16)
				b.Byte(15) // serializer: optional_blockstate — VarInt
				b.VarInt(cv)
			}
			if e.EnderCreepy != last.creepy {
				b.Byte(17)
				b.Byte(8)
				b.Bool(e.EnderCreepy)
			}
			if e.EnderStared != last.stared {
				b.Byte(18)
				b.Byte(8)
				b.Bool(e.EnderStared)
			}
		case hostileGoalTypes[name]:
			// Evoker DATA_SPELL_CASTING_ID (17) / vex and spider
			// DATA_FLAGS_ID (16) — hostile.go.
			hostileMetaDirty(&b, e, name)
		case name == "slime" || name == "magma_cube":
			// AbstractCubeMob's DATA_SIZE (18, int) — emits only
			// when it moves off the defined 1.
			if cs := cubeSizeOf(e); cs != last.cubeSize {
				b.Byte(18)
				b.Byte(1)
				b.VarInt(cs)
			}
		case name == "allay":
			// DATA_DANCING (16, boolean) — the JukeboxListener's
			// synced dance flag.
			if e.Dancing != last.dancing {
				b.Byte(16)
				b.Byte(8)
				b.Bool(e.Dancing)
			}
			// DATA_CAN_DUPLICATE (17, boolean): packs the value (not
			// the cooldown) — true until duplicationCooldown counts.
			if cd := e.DupCooldown <= 0; cd != last.canDup {
				b.Byte(17)
				b.Byte(8)
				b.Bool(cd)
			}
		case name == "pig":
			// DATA_BOOST_TIME (18, int): the steering boost's rolled
			// total — set once per boost, never reset.
			if int32(e.BoostTimeTotal) != last.boost {
				b.Byte(18)
				b.Byte(1)
				b.VarInt(int32(e.BoostTimeTotal))
			}
			if id := holderVariantIDOf(e); id != last.hVarID {
				b.Byte(19)
				b.Byte(28)
				b.VarInt(id)
			}
			if id := holderSoundVariantIDOf(e); id != last.hSoundID {
				b.Byte(20)
				b.Byte(29)
				b.VarInt(id)
			}
		case name == "strider":
			// DATA_BOOST_TIME (18, int) — same ItemBasedSteering
			// contract as the pig.
			if int32(e.BoostTimeTotal) != last.boost {
				b.Byte(18)
				b.Byte(1)
				b.VarInt(int32(e.BoostTimeTotal))
			}
			// DATA_SUFFOCATING (19, boolean).
			if e.Suffocating != last.suffoc {
				b.Byte(19)
				b.Byte(8)
				b.Bool(e.Suffocating)
			}
		case equineTypes[name]:
			// AbstractHorse's DATA_ID_FLAGS (18, byte) — TAME 0x02;
			// the saddle rides equipment.saddle, not this byte.
			if f := equineFlagsByte(e); f != last.eqFlags {
				b.Byte(18)
				b.Byte(0)
				b.Byte(f)
			}
			switch name {
			case "horse":
				// DATA_TYPE_VARIANT (19, int = type|markings<<8).
				if e.Variant != last.eqVar {
					b.Byte(19)
					b.Byte(1)
					b.VarInt(e.Variant)
				}
			case "llama", "trader_llama":
				// DATA_STRENGTH_ID (20, int) + DATA_VARIANT_ID (21).
				if e.Strength != last.llamaStr {
					b.Byte(20)
					b.Byte(1)
					b.VarInt(e.Strength)
				}
				if e.LlamaVariant != last.llamaVar {
					b.Byte(21)
					b.Byte(1)
					b.VarInt(e.LlamaVariant)
				}
			case "camel", "camel_husk":
				// DATA_DASH (19, bool) — arms on START_RIDING_JUMP,
				// clears on the next grounded tick.
				if e.Dashing != last.dash {
					b.Byte(19)
					b.Byte(8)
					b.Bool(e.Dashing)
				}
				// DATA_LAST_POSE_CHANGE_TICK (20, long) — the
				// pose-transition timestamp; negative while sitting.
				if e.LastPoseTick != last.poseTick {
					b.Byte(20)
					b.Byte(2)
					b.VarLong(e.LastPoseTick)
				}
			}
		case name == "cow":
			if id := holderVariantIDOf(e); id != last.hVarID {
				b.Byte(18)
				b.Byte(23)
				b.VarInt(id)
			}
			if id := holderSoundVariantIDOf(e); id != last.hSoundID {
				b.Byte(19)
				b.Byte(24)
				b.VarInt(id)
			}
		case name == "chicken":
			if id := holderVariantIDOf(e); id != last.hVarID {
				b.Byte(18)
				b.Byte(30)
				b.VarInt(id)
			}
			if id := holderSoundVariantIDOf(e); id != last.hSoundID {
				b.Byte(19)
				b.Byte(31)
				b.VarInt(id)
			}
		case name == "creeper":
			if e.Ignited != last.ignited {
				b.Byte(18)
				b.Byte(8)
				b.Bool(e.Ignited)
			}
			if e.Powered != last.powered {
				b.Byte(17)
				b.Byte(8)
				b.Bool(e.Powered)
			}
			if e.SwellDir != last.swell {
				b.Byte(16)
				b.Byte(1) // type: varint
				b.VarInt(e.SwellDir)
			}
		case name == "axolotl":
			// DATA_VARIANT (18, int) + DATA_FROM_BUCKET (20, bool) —
			// DATA_PLAYING_DEAD (19) is untracked.
			if e.Variant != last.eqVar {
				b.Byte(18)
				b.Byte(1)
				b.VarInt(e.Variant)
			}
			if e.FromBucket != last.bucket {
				b.Byte(20)
				b.Byte(8)
				b.Bool(e.FromBucket)
			}
		case name == "rabbit":
			// DATA_TYPE_ID (18, int): 0 brown … 99 evil.
			if e.Variant != last.eqVar {
				b.Byte(18)
				b.Byte(1)
				b.VarInt(e.Variant)
			}
		case name == "tropical_fish":
			// DATA_ID_TYPE_VARIANT (17, int) — the packed
			// (pattern|base|patternColor) blob.
			if e.Variant != last.eqVar {
				b.Byte(17)
				b.Byte(1)
				b.VarInt(e.Variant)
			}
		case name == "pufferfish":
			// DATA_PUFF_STATE (17, int): 0 deflated → 2 full.
			if e.PuffState != last.puff {
				b.Byte(17)
				b.Byte(1)
				b.VarInt(e.PuffState)
			}
		case name == "fox":
			// DATA_TYPE_ID (18, int): 0 red / 1 snow; DATA_FLAGS_ID
			// (19, byte): 0x01 sitting, 0x04 crouching, 0x20 sleeping.
			if e.Variant != last.eqVar {
				b.Byte(18)
				b.Byte(1)
				b.VarInt(e.Variant)
			}
			if f := foxFlagsByte(e); f != last.foxFlags {
				b.Byte(19)
				b.Byte(0)
				b.Byte(f)
			}
			for i := 0; i < 2; i++ {
				var u [16]byte
				set := i < len(e.FoxTrusted)
				if set {
					u = e.FoxTrusted[i]
				}
				var lu [16]byte
				lset := i < len(last.foxTrusted)
				if lset {
					lu = last.foxTrusted[i]
				}
				if e.foxDirty[i] || set != lset || u != lu {
					b.Byte(byte(20 + i))
					b.Byte(13) // optional_living_entity_reference
					b.Bool(set)
					if set {
						b.UUID(u)
					}
				}
				e.foxDirty[i] = false
			}
		}
	}
	e.metaSent = snapLivingMeta(e)
	if len(b.Bytes()) == 0 {
		return nil
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// encodeLivingMetadata builds the Set Entity Data body for a living
// entity's non-default entries, index-ascending like vanilla's
// SynchedEntityData.pack: health (9, float), effect particles (10 —
// only in tick resends, never in pairing — pass nil there), Mob's
// DATA_MOB_FLAGS (15, byte — bit0 = NoAI), then per-type entries:
// DATA_BABY_ID (16, boolean) for the animal family; zombie_villager
// packs converting (19), villagerdata (20 — only when it differs from
// the per-entity random default), finalized (21); villager packs
// villagerdata (19) and finalized (20). The armor stand keeps its
// CLIENT_FLAGS at index 15 instead (it isn't a Mob). Every call also
// stamps e.metaSent — this is a pack emit, so the dirty baseline
// becomes exactly what the client was shown.
func encodeLivingMetadata(e *Entity, particles []byte) []byte {
	var b protocol.Buffer
	// DATA_AIR_SUPPLY_ID (index 1, int) rides the pack only while the
	// supply sits off its 300 default — vanilla's attach burst omits
	// the index; a drain/regen tick dirty-resends it.
	if e.AirSupply != 300 {
		b.Byte(1)
		b.Byte(1) // type: varint
		b.VarInt(int32(e.AirSupply))
	}
	if e.CustomName != "" {
		b.Byte(2) // index: custom_name
		b.Byte(6) // type: optional component
		b.Byte(1) // present
		writeComponentTag(&b, e.CustomName, e.CustomNameComp)
	}
	if e.CustomNameVisible {
		b.Byte(3) // index: custom_name_visible
		b.Byte(8) // type: boolean
		b.Byte(1)
	}
	entityNoGravityPack(&b, e)
	// DATA_POSE (6) — emitted when the pose sits off STANDING(0):
	// a camel loaded with LastPoseTick<0 arrives SITTING(10).
	if e.Pose != 0 {
		b.Byte(6)
		b.Byte(20) // type: pose
		b.VarInt(e.Pose)
	}
	if e.TicksFrozen > 0 {
		b.Byte(7) // index: DATA_TICKS_FROZEN
		b.Byte(1) // type: varint
		b.VarInt(e.TicksFrozen)
	}
	b.Byte(9) // index: health
	b.Byte(3) // type: float
	b.Float(e.Health)
	if e.Type == mustEntityType("armor_stand") {
		// Pairing ships only non-default entries: a flagless stand
		// (the item-placed one) leaves DATA_CLIENT_FLAGS out.
		if fl := encodeArmorStandFlags(e); fl != 0 {
			b.Byte(15) // index: armor-stand client flags
			b.Byte(0)  // type: byte
			b.Byte(fl)
		}
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
	// EnderDragon.DATA_PHASE (16).
	dragonMetaPairing(e, &b)
	if name, ok := EntityTypeName(e.Type); ok {
		// AgeableMob's pair leads every ageable chain — DATA_BABY_ID
		// (16) then AGE_LOCKED (17); each subtype's own data starts
		// at 18+ (AbstractHorse's DATA_ID_FLAGS is 18 too), so the
		// hoist keeps packDirty's ascending order.
		if ageableTypes[name] {
			if e.Baby {
				b.Byte(16)
				b.Byte(8)
				b.Bool(true)
			}
			if e.AgeLocked {
				b.Byte(17)
				b.Byte(8)
				b.Bool(true)
			}
		}
		// AbstractFish's FROM_BUCKET (16, bool) leads the small-fish
		// chain the same way — cod/salmon/tropical_fish/pufferfish/
		// tadpole all start their own data at 17.
		if abstractFishTypes[name] && e.FromBucket {
			b.Byte(16)
			b.Byte(8)
			b.Bool(true)
		}
		switch name {
		case "sheep":
			// Sheep's DATA_WOOL_ID (index 18, byte): low nibble is
			// the dye color, bit 0x10 = sheared.
			if e.Sheared || e.Color != 0 {
				b.Byte(18)
				b.Byte(0) // type: byte
				v := byte(e.Color & 15)
				if e.Sheared {
					v |= 0x10
				}
				b.Byte(v)
			}
		case "mooshroom":
			// MushroomCow DATA_TYPE (index 18, int): 0 red / 1 brown;
			// the default red stays unpacked.
			if e.MooshroomVariant != 0 {
				b.Byte(18)
				b.Byte(1) // type: varint
				b.VarInt(e.MooshroomVariant)
			}
		case "blaze", "ghast", "phantom":
			flyerMetaPack(&b, e, name)
		case "guardian", "elder_guardian":
			// Guardian defines DATA_ID_MOVING (16, boolean) and
			// DATA_ID_ATTACK_TARGET (17, int) — both ride the pack,
			// defaults included.
			b.Byte(16)
			b.Byte(8) // type: boolean
			b.Bool(e.Moving)
			b.Byte(17)
			b.Byte(1) // type: varint
			b.VarInt(e.beamTarget)
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
		case "allay":
			// DATA_DANCING (16, boolean): the JukeboxListener's dance
			// state — the false define default stays unpacked.
			if e.Dancing {
				b.Byte(16)
				b.Byte(8) // serializer: boolean
				b.Bool(true)
			}
			// DATA_CAN_DUPLICATE (17, boolean): true is the define
			// default, so only the inverted value packs — the 6000
			// ticks after a duplication while the cooldown counts.
			if e.DupCooldown > 0 {
				b.Byte(17)
				b.Byte(8) // serializer: boolean
				b.Bool(false)
			}
		case "axolotl":
			// Axolotl's DATA_VARIANT (18, int); DATA_PLAYING_DEAD
			// sits at 19 (untracked) and DATA_FROM_BUCKET (20,
			// boolean) packs 01 when the bucket flag is set.
			if e.Variant != 0 {
				b.Byte(18)
				b.Byte(1) // type: varint
				b.VarInt(e.Variant)
			}
			if e.FromBucket {
				b.Byte(20)
				b.Byte(8)
				b.Bool(true)
			}
		case "parrot":
			tamableMetaPack(&b, e)
			// Parrot's DATA_VARIANT_ID (20, int): 0 red … 4 gray.
			if e.Variant != 0 {
				b.Byte(20)
				b.Byte(1) // type: varint
				b.VarInt(e.Variant)
			}
		case "rabbit":
			// Rabbit's DATA_TYPE_ID (18, int): 0 brown … 99 evil.
			if e.Variant != 0 {
				b.Byte(18)
				b.Byte(1) // type: varint
				b.VarInt(e.Variant)
			}
		case "tropical_fish":
			// DATA_ID_TYPE_VARIANT (17, int) — the packed
			// (pattern|base|patternColor) blob.
			if e.Variant != 0 {
				b.Byte(17)
				b.Byte(1) // type: varint
				b.VarInt(e.Variant)
			}
		case "pufferfish":
			// DATA_PUFF_STATE (17, int): 0 deflated → 2 full.
			if e.PuffState != 0 {
				b.Byte(17)
				b.Byte(1) // type: varint
				b.VarInt(e.PuffState)
			}
		case "fox":
			// Fox's DATA_TYPE_ID (18, int): 0 red / 1 snow;
			// DATA_FLAGS_ID (19, byte): 0x01 sitting, 0x04
			// crouching, 0x20 sleeping. Trusted references at
			// 20/21 stay unpacked while empty.
			if e.Variant != 0 {
				b.Byte(18)
				b.Byte(1) // type: varint
				b.VarInt(e.Variant)
			}
			var fflags byte
			if e.Sitting {
				fflags |= 0x01
			}
			if e.Crouching {
				fflags |= 0x04
			}
			if e.Sleeping {
				fflags |= 0x20
			}
			if fflags != 0 {
				b.Byte(19)
				b.Byte(0) // type: byte
				b.Byte(fflags)
			}
			// DATA_TRUSTED_ID_0/1 (20/21, optional_living_entity_reference
			// serializer 13): bool + 16-byte uuid — vanilla packs
			// only populated slots.
			for i, u := range e.FoxTrusted {
				if i > 1 {
					break
				}
				b.Byte(byte(20 + i))
				b.Byte(13)
				b.Bool(true)
				b.UUID(u)
			}
		case "panda":
			// Panda packs both genes as Panda$Gene ordinal bytes
			// at 21 (main) and 22 (hidden); normal/normal is the
			// defined default and stays unpacked.
			if g := pandaGeneOrdinal(e.GeneMain); g != 0 {
				b.Byte(21)
				b.Byte(0) // type: byte
				b.Byte(g)
			}
			if g := pandaGeneOrdinal(e.GeneHidden); g != 0 {
				b.Byte(22)
				b.Byte(0) // type: byte
				b.Byte(g)
			}
		case "cat":
			tamableMetaPack(&b, e)
			// Cat's DATA_VARIANT_ID (20, cat_variant holder) packs
			// only off the black default; DATA_SOUND_VARIANT_ID
			// sits at 24 after the feline-behavior slots.
			if id := holderVariantID("cat", e.VariantName); id != 1 {
				b.Byte(20)
				b.Byte(21) // serializer: cat_variant
				b.VarInt(id)
			}
			// IS_LYING (21) / RELAX_STATE_ONE (22): defineSynchedData
			// defaults false — pack only the true value.
			if e.CatLying {
				b.Byte(21)
				b.Byte(8) // type: boolean
				b.Bool(true)
			}
			if e.CatRelax {
				b.Byte(22)
				b.Byte(8) // type: boolean
				b.Bool(true)
			}
			// DATA_COLLAR_COLOR (23, int): dye legacy id — the
			// RED(14) defined default stays unpacked.
			if c := collarColorValue(e); c != 14 {
				b.Byte(23)
				b.Byte(1) // type: varint
				b.VarInt(c)
			}
			if id := holderSoundVariantID("cat", e.SoundVariant); id != 0 {
				b.Byte(24)
				b.Byte(22) // serializer: cat_sound_variant
				b.VarInt(id)
			}
		case "frog":
			// DATA_POSE (6) rides the shared prelude; the variant
			// holder and tongue target stay per-type.
			// Frog's DATA_VARIANT_ID (18, frog_variant holder).
			if id := holderVariantID("frog", e.VariantName); id != 1 {
				b.Byte(18)
				b.Byte(27) // serializer: frog_variant
				b.VarInt(id)
			}
			// DATA_TONGUE_TARGET_ID (19): eid+1, 0 empty.
			if e.FrogTongueTarget != 0 {
				b.Byte(19)
				b.Byte(19)
				b.VarInt(e.FrogTongueTarget + 1)
			}
		case "enderman":
			// DATA_CARRY_STATE (16, optional_blockstate) packs the
			// default-state id once carried; the empty Optional (0)
			// is the defined default and stays unpacked.
			if cv := enderCarryState(e); cv != 0 {
				b.Byte(16)
				b.Byte(15)
				b.VarInt(cv)
			}
			// DATA_CREEPY (17) / DATA_STARED_AT (18): the false
			// defaults stay unpacked.
			if e.EnderCreepy {
				b.Byte(17)
				b.Byte(8)
				b.Bool(true)
			}
			if e.EnderStared {
				b.Byte(18)
				b.Byte(8)
				b.Bool(true)
			}
		case "slime", "magma_cube":
			// AbstractCubeMob's DATA_SIZE (18) — the defined 1 packs
			// only when the summon grew it.
			if cs := cubeSizeOf(e); cs != 1 {
				b.Byte(18)
				b.Byte(1)
				b.VarInt(cs)
			}
		case "wolf":
			tamableMetaPack(&b, e)
			// Wolf's DATA_VARIANT_ID (23, wolf_variant) and
			// DATA_SOUND_VARIANT_ID (24, wolf_sound_variant) pack
			// off pale/classic.
			// DATA_INTERESTED_ID (20, boolean): BegGoal's flag —
			// the false default stays unpacked.
			if e.Interested {
				b.Byte(20)
				b.Byte(8) // serializer: boolean
				b.Bool(true)
			}
			// DATA_COLLAR_COLOR (21, int): dye legacy id — RED(14)
			// is the defined default and stays unpacked.
			if c := collarColorValue(e); c != 14 {
				b.Byte(21)
				b.Byte(1) // type: varint
				b.VarInt(c)
			}
			// DATA_ANGER_END_TIME (22, long): the -1L defined
			// default (no persistent end) stays unpacked.
			if end := angerEndSynced(e); end != -1 {
				b.Byte(22)
				b.Byte(2) // serializer: long
				b.VarLong(end)
			}
			if id := holderVariantID("wolf", e.VariantName); id != 3 {
				b.Byte(23)
				b.Byte(25) // serializer: wolf_variant
				b.VarInt(id)
			}
			if id := holderSoundVariantID("wolf", e.SoundVariant); id != 2 {
				b.Byte(24)
				b.Byte(26) // serializer: wolf_sound_variant
				b.VarInt(id)
			}
		case "bee":
			// Bee's DATA_FLAGS_ID (18, byte): 0x08 nectar |
			// 0x04 stung | 0x02 rolling — the zero default
			// stays unpacked.
			if bflags := beeFlagsValue(e); bflags != 0 {
				b.Byte(18)
				b.Byte(0) // type: byte
				b.Byte(bflags)
			}
			// DATA_ANGER_END_TIME (19, long): the -1L defined
			// default (no persistent end) stays unpacked.
			if end := angerEndSynced(e); end != -1 {
				b.Byte(19)
				b.Byte(2) // serializer: long
				b.VarLong(end)
			}
		case "pig":
			// DATA_BOOST_TIME (18, int): ItemBasedSteering's boost
			// total — 0 while never boosted, then the last rolled
			// total (it is never reset to 0 on the wire).
			if e.BoostTimeTotal != 0 {
				b.Byte(18)
				b.Byte(1) // serializer: int
				b.VarInt(int32(e.BoostTimeTotal))
			}
			// Pig's DATA_VARIANT_ID (19) and DATA_SOUND_VARIANT_ID
			// (20).
			if id := holderVariantID("pig", e.VariantName); id != 1 {
				b.Byte(19)
				b.Byte(28) // serializer: pig_variant
				b.VarInt(id)
			}
			if id := holderSoundVariantID("pig", e.SoundVariant); id != 1 {
				b.Byte(20)
				b.Byte(29) // serializer: pig_sound_variant
				b.VarInt(id)
			}
		case "strider":
			// DATA_BOOST_TIME (18, int) — same contract as the pig.
			if e.BoostTimeTotal != 0 {
				b.Byte(18)
				b.Byte(1) // serializer: int
				b.VarInt(int32(e.BoostTimeTotal))
			}
			// DATA_SUFFOCATING (19, boolean): the false define
			// default stays unpacked.
			if e.Suffocating {
				b.Byte(19)
				b.Byte(8) // serializer: boolean
				b.Bool(true)
			}
		case "cow":
			// Cow's DATA_VARIANT_ID (18) and DATA_SOUND_VARIANT_ID
			// (19) — temperate/classic are the defined defaults.
			if id := holderVariantID("cow", e.VariantName); id != 1 {
				b.Byte(18)
				b.Byte(23) // serializer: cow_variant
				b.VarInt(id)
			}
			if id := holderSoundVariantID("cow", e.SoundVariant); id != 0 {
				b.Byte(19)
				b.Byte(24) // serializer: cow_sound_variant
				b.VarInt(id)
			}
		case "chicken":
			// Chicken's DATA_VARIANT_ID (18) and
			// DATA_SOUND_VARIANT_ID (19).
			if id := holderVariantID("chicken", e.VariantName); id != 1 {
				b.Byte(18)
				b.Byte(30) // serializer: chicken_variant
				b.VarInt(id)
			}
			if id := holderSoundVariantID("chicken", e.SoundVariant); id != 0 {
				b.Byte(19)
				b.Byte(31) // serializer: chicken_sound_variant
				b.VarInt(id)
			}
		case "salmon":
			// Salmon's DATA_TYPE (17, plain varint serializer):
			// small 0 / medium 1 / large 2, medium the default.
			if id := salmonVariantID(e.VariantName); id != 1 {
				b.Byte(17)
				b.Byte(1) // type: varint
				b.VarInt(id)
			}
		case "zombie_villager":
			// Zombie's own DATA_BABY_ID (16) — not AgeableMob's.
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
			// 16 = baby (Zombie's own DATA_BABY_ID), 17 =
			// is_under_water_converting.
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
			if e.Ignited {
				b.Byte(18)
				b.Byte(8)
				b.Bool(true)
			}
			// swell_dir (16, int — non-default only while the fuse
			// runs up) and powered (17, bool).
			if e.Powered {
				b.Byte(17)
				b.Byte(8)
				b.Bool(true)
			}
			if e.SwellDir > 0 {
				b.Byte(16)
				b.Byte(1) // type: varint
				b.VarInt(1)
			}
		case "horse", "zombie_horse", "skeleton_horse", "donkey",
			"mule", "llama", "trader_llama", "camel":
			// The AbstractHorse family: DATA_ID_FLAGS (18) via
			// equineMetaPack, then the per-subtype slots (horse
			// type-variant 19, llama strength+variant 20/21, camel
			// DASH 19).
			equineMetaPack(&b, e, name)
		}
		// hostile.go's family byte (evoker spell id 17, vex/spider
		// DATA_FLAGS_ID 16) — a no-op for unregistered types.
		hostileMetaPack(&b, e, name)
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// writeComponentTag writes a component's serialized form inside the
// optional-component serializer: ComponentSerialization's
// either-codec — literal-text components go out as a TAG_String of
// the raw JSON text, structured contents as the full compound, the
// same split componentNBTValue applies on the save side. A literal
// stored name (comp clear) is already the tag's string payload.
func writeComponentTag(b *protocol.Buffer, s string, comp bool) {
	if comp {
		if text := componentLiteralText(s); text != "" {
			b.Byte(nbt.TagString)
			b.Byte(byte(len(text) >> 8))
			b.Byte(byte(len(text)))
			b.Raw([]byte(text))
			return
		}
		t := strings.TrimSpace(s)
		if t != "" && (t[0] == '{' || t[0] == '[') {
			if err := nbt.WriteUnnamed(b, componentNBTValue(s, comp)); err == nil {
				return
			}
		}
	}
	b.Byte(nbt.TagString)
	b.Byte(byte(len(s) >> 8))
	b.Byte(byte(len(s)))
	b.Raw([]byte(s))
}

// pandaGeneOrdinal maps a Panda$Gene name to its enum ordinal — the
// wire packs genes as ordinal bytes, not strings.
func pandaGeneOrdinal(g string) byte {
	switch g {
	case "lazy":
		return 1
	case "worried":
		return 2
	case "playful":
		return 3
	case "brown":
		return 4
	case "weak":
		return 5
	case "aggressive":
		return 6
	}
	return 0
}

// holderVariantIDs maps each holder-variant registry's short names to
// their wire holder ids — the registration order measured off the 26.2
// jar's datapack registries (alphabetical).
var holderVariantIDs = map[string]map[string]int32{
	"cat":     {"all_black": 0, "black": 1, "british_shorthair": 2, "calico": 3, "jellie": 4, "persian": 5, "ragdoll": 6, "red": 7, "siamese": 8, "tabby": 9, "white": 10},
	"frog":    {"cold": 0, "temperate": 1, "warm": 2},
	"wolf":    {"ashen": 0, "black": 1, "chestnut": 2, "pale": 3, "rusty": 4, "snowy": 5, "spotted": 6, "striped": 7, "woods": 8},
	"pig":     {"cold": 0, "temperate": 1, "warm": 2},
	"cow":     {"cold": 0, "temperate": 1, "warm": 2},
	"chicken": {"cold": 0, "temperate": 1, "warm": 2},
}

// holderSoundVariantIDs does the same for the sound-variant registries.
var holderSoundVariantIDs = map[string]map[string]int32{
	"cat":     {"classic": 0, "royal": 1},
	"wolf":    {"angry": 0, "big": 1, "classic": 2, "cute": 3, "grumpy": 4, "puglin": 5, "sad": 6},
	"cow":     {"classic": 0, "moody": 1},
	"pig":     {"big": 0, "classic": 1, "mini": 2},
	"chicken": {"classic": 0, "picky": 1},
}

// holderVariantDefault is each type's defined-default variant name —
// the value defineSynchedData registers, which stays unpacked on the
// wire and is what /data emits for an unmodified summon.
var holderVariantDefault = map[string]string{
	"cat": "black", "frog": "temperate", "wolf": "pale",
	"pig": "temperate", "cow": "temperate", "chicken": "temperate",
	"salmon": "medium",
}

// holderVariantID resolves a variant name against the type's registry
// table, applying the defined default on empty.
func holderVariantID(typ, name string) int32 {
	if name == "" {
		name = holderVariantDefault[typ]
	}
	if m, ok := holderVariantIDs[typ]; ok {
		if id, ok := m[name]; ok {
			return id
		}
	}
	return holderVariantIDs[typ][holderVariantDefault[typ]]
}

// holderSoundVariantID resolves a sound-variant name, defaulting to
// "classic" — every sound-holding type's defined default.
func holderSoundVariantID(typ, name string) int32 {
	if name == "" {
		name = "classic"
	}
	if m, ok := holderSoundVariantIDs[typ]; ok {
		if id, ok := m[name]; ok {
			return id
		}
	}
	return holderSoundVariantIDs[typ]["classic"]
}

// salmonVariantID maps Salmon$Variant names to ids — 0 small, 1
// medium (defined default), 2 large.
func salmonVariantID(name string) int32 {
	switch name {
	case "small":
		return 0
	case "large":
		return 2
	}
	return 1
}

// collarColorValue is the effective dye legacy id for Cat's/Wolf's
// CollarColor: the summon tag's explicit byte, else the defined
// default RED(14) vanilla's read applies to a missing key.
func collarColorValue(e *Entity) int32 {
	if !e.CollarColorSet {
		return 14
	}
	return e.CollarColor
}

// beeFlagsValue is Bee's DATA_FLAGS_ID byte: 0x08 hasNectar,
// 0x04 hasStung, 0x02 isRolling (the rolling bit stays
// runtime-only for now).
func beeFlagsValue(e *Entity) byte {
	var v byte
	if e.HasNectar {
		v |= 0x08
	}
	if e.Stung {
		v |= 0x04
	}
	if e.Rolling {
		v |= 0x02
	}
	return v
}

// angerEndSynced is the effective anger_end_time: the summon tag's
// explicit long (kept verbatim, including 0), a runtime-set
// expiry, or -1 — vanilla's defined default meaning "no
// persistent end", which also covers mc-go's open-ended
// AngryUntil==0 sentinel.
func angerEndSynced(e *Entity) int64 {
	if e.AngryUntilSet || e.AngryUntil != 0 {
		return e.AngryUntil
	}
	return -1
}

// mobFlagsByte is Mob's DATA_MOB_FLAGS byte: bit0 = NoAI, bit1 =
// left-handed, bit2 = the melee goal's aggressive flag.
func mobFlagsByte(e *Entity) byte {
	var flags byte
	if e.NoAI {
		flags |= 0x01
	}
	if e.Aggressive {
		flags |= 0x04
	}
	return flags
}

// equineTypes is the AbstractHorse family — they all carry the
// DATA_ID_FLAGS byte at metadata index 18 (AbstractHorse extends
// Animal — the TamableAnimal slots don't exist here). Horse adds
// DATA_TYPE_VARIANT (19, int = type | markings<<8); the llama family
// adds DATA_STRENGTH_ID (20, int) and DATA_VARIANT_ID (21, int);
// Camel adds DATA_DASH (19, bool) and DATA_LAST_POSE_CHANGE_TICK (20,
// long — the pose-transition timestamp mc-go does not model).
var equineTypes = map[string]bool{
	"horse": true, "zombie_horse": true, "skeleton_horse": true,
	"donkey": true, "mule": true, "llama": true,
	"trader_llama": true, "camel": true, "camel_husk": true,
}

// equineFlagsByte packs AbstractHorse's DATA_ID_FLAGS byte (idx18):
// 0x02 TAME | 0x08 BRED | 0x10 EATING | 0x20 STANDING | 0x40
// OPEN_MOUTH. Standing and mouth pulse on the makeMad/feed arms and
// clear on tick()'s counters; eating pulses on the graze roll.
func equineFlagsByte(e *Entity) byte {
	var flags byte
	if e.Tamed {
		flags |= 0x02
	}
	if e.Bred {
		flags |= 0x08
	}
	if e.EatingHaystack {
		flags |= 0x10
	}
	if e.HorseStanding {
		flags |= 0x20
	}
	if e.HorseMouth {
		flags |= 0x40
	}
	return flags
}

// equineMetaPack writes the AbstractHorse family's non-default
// metadata entries into an attach/sendChanges pack.
func equineMetaPack(b *protocol.Buffer, e *Entity, name string) {
	if f := equineFlagsByte(e); f != 0 {
		b.Byte(18)
		b.Byte(0) // serializer: byte
		b.Byte(f)
	}
	switch name {
	case "horse":
		if e.Variant != 0 {
			b.Byte(19)
			b.Byte(1) // serializer: int
			b.VarInt(e.Variant)
		}
	case "llama", "trader_llama":
		if e.Strength != 0 {
			b.Byte(20)
			b.Byte(1)
			b.VarInt(e.Strength)
		}
		if e.LlamaVariant != 0 {
			b.Byte(21)
			b.Byte(1)
			b.VarInt(e.LlamaVariant)
		}
	case "camel", "camel_husk":
		if e.Dashing {
			b.Byte(19)
			b.Byte(8) // serializer: boolean
			b.Bool(true)
		}
		// DATA_LAST_POSE_CHANGE_TICK (20, long) — the 0L defined
		// default stays unpacked.
		if e.LastPoseTick != 0 {
			b.Byte(20)
			b.Byte(2) // serializer: long
			b.VarLong(e.LastPoseTick)
		}
	}
}

// villagerProfessionNames inverts villagerProfessionIDs — the
// generated table is name→id only, and a villager's display name key
// needs the profession's registry name (entity.minecraft.villager.none).
var villagerProfessionNames = func() map[int32]string {
	m := make(map[int32]string, len(villagerProfessionIDs))
	for name, id := range villagerProfessionIDs {
		m[id] = name
	}
	return m
}()

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
	// EntityType END_CRYSTAL / ENDER_DRAGON are fireImmune too.
	"end_crystal": true, "ender_dragon": true,
}

// mobBreathesWater is vanilla's can_breathe_under_water entity-type
// tag (canBreatheUnderwater): the flat members plus #undead, which
// itself expands to #skeletons + #zombies + wither + phantom.
var mobBreathesWater = map[string]bool{
	"axolotl": true, "frog": true, "guardian": true,
	"elder_guardian": true, "turtle": true, "glow_squid": true,
	"cod": true, "pufferfish": true, "salmon": true, "squid": true,
	"tropical_fish": true, "tadpole": true, "armor_stand": true,
	"copper_golem": true, "nautilus": true,
	// #undead
	"wither": true, "phantom": true,
	// #skeletons
	"skeleton": true, "stray": true, "wither_skeleton": true,
	"skeleton_horse": true, "bogged": true, "parched": true,
	// #zombies
	"zombie": true, "zombie_villager": true, "zombified_piglin": true,
	"zoglin": true, "drowned": true, "husk": true,
	"zombie_horse": true, "camel_husk": true, "zombie_nautilus": true,
}

// mobWaterAnimalAir is the WaterAnimal subclass set whose own
// handleAirSupply overrides LivingEntity's: out of water they drain
// one air per tick, in water they snap to 300. The dolphin's override
// is empty (it uses DATA_MOISTNESS instead) and the turtle is a plain
// Animal, so neither is listed.
var mobWaterAnimalAir = map[string]bool{
	"axolotl": true, "nautilus": true,
	"cod": true, "salmon": true, "tropical_fish": true, "pufferfish": true,
	"squid": true, "glow_squid": true, "tadpole": true,
}

// abstractFishTypes is the AbstractFish subclass set — the types that
// carry the shared FROM_BUCKET accessor (idx16): the axolotl has its
// own FROM_BUCKET (idx20) and rides a different chain.
var abstractFishTypes = map[string]bool{
	"cod": true, "salmon": true, "pufferfish": true,
	"tropical_fish": true, "tadpole": true,
}

// isFireDamageType is vanilla's is_fire damage-type tag — every
// source a fireImmune entity is invulnerable to.
var isFireDamageType = map[string]bool{
	"in_fire": true, "campfire": true, "on_fire": true, "lava": true,
	"hot_floor": true, "sulfur_cube_hot": true,
	"unattributed_fireball": true, "fireball": true,
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

// markHurt is vanilla's Entity.markHurt: it latches hurtMarked on
// every landed hurt outside the damage_type: no_impact tag (drown
// only). sendChanges' tail then flushes the entity's current delta
// unconditionally — even when the drift gate would suppress it.
func (s *Server) markHurt(e *Entity, typeName string) {
	if typeName == "drown" {
		return
	}
	if ep, ok := s.entities.GetPtr(e.ID); ok {
		ep.HurtMarked = true
	} else {
		e.HurtMarked = true
	}
}

// damageMob runs vanilla's LivingEntity.hurt tail for a mob hit:
// armor (natural base + worn pieces) absorbs unless the damage type
// bypasses it, then the health loss lands inside the grace rules.
// info carries the damage_event broadcast — vanilla emits it inside
// hurtServer's accepted branch, AFTER the absorb/wear side effects
// and BEFORE hurt()'s playHurtSound.
// damageMob routes a mob hurt through the type's hurtServer —
// EnderMan's override handles the projectile-teleport tries and the
// 9/10 contact teleport after non-projectile damage.
func (s *Server) damageMob(e *Entity, amount float32, typeName string, killer *Session, info *damageInfo) (dealt float32, graced, died bool) {
	if s.mobInvulnerableTo(e, typeName, info) {
		return 0, true, false
	}
	if kn, _ := EntityTypeName(e.Type); kn == "enderman" {
		return s.enderHurtServer(e, amount, typeName, killer, info)
	}
	if e.dragon != nil {
		return s.dragonHurtServer(e, amount, typeName, killer, info)
	}
	return s.mobHurtCore(e, amount, typeName, killer, info)
}

// mobHurtCore is LivingEntity.hurtServer — the damage apply plus the
// wire tail shared by every type's own hurtServer.
func (s *Server) mobHurtCore(e *Entity, amount float32, typeName string, killer *Session, info *damageInfo) (dealt float32, graced, died bool) {
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
		s.markHurt(e, typeName)
		return 0, res.Graced, false
	}
	// Vanilla stores lastDamageSource on every landed hurt — the
	// witch's fire-resistance roll reads it through IS_FIRE, and
	// mob loot's damage_source_properties gates read the cause.
	if ep, ok := s.entities.GetPtr(e.ID); ok {
		ep.LastDamageType = typeName
		ep.LastDamageCause = 0
		if info != nil {
			ep.LastDamageCause = info.cause
		}
	}
	s.markHurt(e, typeName)
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
	// LivingEntity.actuallyHurt posts ENTITY_DAMAGE at the hurt
	// position — runs for every landed hit, lethal or not.
	if d := s.dimensionByName(e.Dim); d != nil {
		s.emitGameEvent(d, "entity_damage", e.X, e.Y, e.Z, nil, "")
	}
	// Camel.actuallyHurt's first arm is standUpInstantly — every
	// landed hurt stands a sitting camel silently, lethal or not.
	if n, _ := EntityTypeName(e.Type); n == "camel" || n == "camel_husk" {
		s.camelStand(e, true)
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
			ep.LastHurtByPlayerUUID = killer.uuid
			if ep.Raid != nil {
				// Raid.updateHeroes: landing damage on a raider
				// makes the attacker a hero-of-the-village target.
				ep.Raid.heroes[killer.uuid] = true
			}
			// NeutralMob provocation: the hurt player becomes this
			// mob's anger target. NeutralMob types additionally arm
			// the persistent-anger timer (PERSISTENT_ANGER_TIME,
			// rangeOfSeconds(20,39) everywhere) so the anger outlives
			// the goal's target loss.
			ep.AngryAt = killer.uuid
			kn, _ := EntityTypeName(ep.Type)
			if kn == "enderman" {
				// doHurtTarget → setTarget: the provoke records
				// targetChangeTime, suppressing the daylight flee
				// for 600 ticks.
				ep.EnderTargetChange = ep.TickCount
			}
			if neutralAngryTypes[kn] && !ep.NoAI {
				ep.AngryUntil = s.gameTime() + 400 + rand.Int63n(381)
			}
			// LlamaHurtByTargetGoal → RangedAttackGoal(this,1.25,40,20):
			// the attacker becomes the target for one spit 40 ticks
			// out (attackTime lerps dist/radius into [min,max] —
			// min==max here); didSpit drops the target afterward, so
			// each injury arms exactly one shot. TamableAnimal's
			// isOwnedBy gate spares a tamed llama's owner, and a
			// targetless goal never starts (NoAI).
			if (kn == "llama" || kn == "trader_llama") && !ep.NoAI && d > 0 &&
				!(ep.Tamed && ep.ownerUUID == killer.uuid) {
				ep.LlamaSpitTarget = killer.uuid
				if ep.LlamaSpitTicks == 0 {
					ep.LlamaSpitTicks = 40
				}
			}
			// DATA_ANGER_END_TIME arms on the provoke — the idx19
			// emit only fires on the dirty gate.
			ep.MetaDirty = true
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
	// PanicGoal.shouldPanic: a panic-capable mob whose last damage
	// source is in PANIC_CAUSES arms the goal for the whole 40-tick
	// getLastDamageSource window (LivingEntity.java:1430 clears the
	// source once the stamp ages past 40). NoAI never runs the goal
	// selector, so it never panics. Each goal fire — and every re-fire
	// after a run ends inside the window — rolls a fresh
	// DefaultRandomPos target; a running goal is NOT retargeted by
	// further damage, the window only extends.
	if d > 0 && panicCauseTypes[typeName] {
		if kn, _ := EntityTypeName(e.Type); mobPanicSpeed[kn] > 0 {
			if ep, ok := s.entities.GetPtr(e.ID); ok && !ep.NoAI {
				ep.PanicUntil = s.gameTime() + 40
				if !ep.PanicActive {
					s.panicRollTarget(ep)
					ep.PanicSteps = 0
					ep.Strolling = false
					// PanicGoal (priority 1) preempts AvoidEntityGoal
					// (3): goalSelector calls avoid's stop(), so the
					// escape run does not resume after the panic — it
					// re-rolls a fresh target once panic ends.
					ep.AvoidActive = false
				}
			}
		}
	}
	// The dragon's health rides its own tracker flush (enderdragon.go).
	if d > 0 && e.dragon == nil {
		s.broadcastEntityMetadata(e.ID, encodeLivingHealth(health))
		e.ensureMetaInit()
		e.metaSent.health = health
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
	// Hurt callers may hold a Manager.Get copy. Death reads the committed
	// victim, including the player credit just recorded by the lethal hit,
	// and writes its death metadata back to that same authoritative entity.
	if current, ok := s.entities.GetPtr(e.ID); ok {
		e = current
	}
	if e.dragon != nil {
		// EnderDragon.handleKillingBlow keeps the dragon alive for
		// its death flight (enderdragon.go).
		s.dragonDie(e, killer)
		return
	}
	// LivingEntity.die posts ENTITY_DEATH at the body's position.
	if d := s.dimensionByName(e.Dim); d != nil {
		s.emitGameEvent(d, "entity_die", e.X, e.Y, e.Z, nil, "")
	}
	if !silent {
		s.mobDeathSound(e)
	}
	s.entities.SetDying(e.ID)
	// The death pack rides the next tick's sendChanges like any dirty
	// metadata — a /kill reply reaches the client first.
	e.dataSync = encodeDyingLivingMetadata()
	e.ensureMetaInit()
	e.metaSent.health = 0
	e.dataSyncTick = s.dataSyncReadyTick()
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 3)
	s.spawnMobLoot(e, killer)
	// dropExperience: recent player hurt + not a baby + mob_drops.
	if e.LastHurtByPlayerTicks > 0 && !e.Baby &&
		s.gameRuleBool("minecraft:mob_drops") {
		if name, ok := EntityTypeName(e.Type); ok {
			s.spawnMobXP(e.Dim, name, e.X, e.Y, e.Z)
		}
	}
	// getKillCredit()'s award pair: the killed:<type> stat lands on
	// the credited player first (die() bytecode: awardStat then
	// awardKillScore), then the scoreboard kill criteria — the last
	// player to hurt within the 100-tick window earns both, whoever
	// landed the final blow — Entity's empty override means mob
	// killers never bump scores.
	if e.LastHurtByPlayerTicks > 0 {
		if src := s.sessionByUUID(e.LastHurtByPlayerUUID); src != nil {
			src.addStat(statKilled, e.Type, 1)
			s.awardKillScore(src, sprintUUID(e.UUID), false)
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
// nonMobLiving are LivingEntity subclasses that are NOT Mob — vanilla
// ticks them through LivingEntity.tick only (gravity, effects, leash),
// never Mob.aiStep's goal selector: an armor stand never wanders,
// chases, or turns its own head.
var nonMobLiving = map[string]bool{
	"armor_stand": true,
}

func (s *Server) mobTickStep() {
	s.entities.mu.RLock()
	mobs := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		// The ender dragon runs its own tick (enderdragon.go).
		if e.Living && e.dragon == nil {
			mobs = append(mobs, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range mobs {
		// Entities outside a non-spectator's simulation-distance chunk
		// window are never ticked on vanilla — gravity, fire, the
		// invulnerability countdown and the death linger all pause.
		// A dormant entity is parked in an unticketed chunk and never
		// ticks either, even when no player gates the range check.
		if e.Dormant || !s.mobInSimulationRange(e) {
			continue
		}
		sampleMobKnownSpeed(e)
		// Mob.checkDespawn, ticking order matched to serverAiStep:
		// noActionTime counts up first, then the despawn rules run —
		// peaceful purge, persistence exemption, the >128 instant
		// discard (PatrollingMonster's removeWhenFarAway re-keys on
		// patrolling), and the >32 idle 1-in-800 roll.
		e.NoActionTime++
		e.TickCount++
		e.livingEffectTickSerial++
		// Entity.tick's boardingCooldown countdown — a just-dismounted
		// mob can't be scooped again until it reaches zero.
		if e.BoardingCooldown > 0 {
			e.BoardingCooldown--
		}
		if s.mobCheckDespawn(e) {
			s.mobRemovedPassengerCleanup(e)
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			continue
		}
		if e.Dying {
			// Leashable.tickLeash keeps running for a dying mob: the
			// moment it can't interact with the level the leash
			// drops — lead item only under entity_drops.
			if e.LeashHolder != 0 {
				s.leashDrop(e, s.gameRuleBool("minecraft:entity_drops"))
			}
			if len(e.dataSync) > 0 && s.gameTime() >= e.dataSyncTick {
				data := e.dataSync
				e.dataSync = nil
				s.broadcastEntityMetadata(e.ID, data)
			}
			if s.entities.TickDeath(e.ID) {
				s.broadcastEntityEventTo(nil, e.Dim, e.ID, 60)
				// AbstractCubeMob.remove runs the SPLIT_ON_DEATH
				// spawns inside remove() — on the wire the poof
				// event leads, then each child's add_entity, and the
				// parent's remove_entities closes the bundle.
				s.cubeMobSplit(e)
				if vd := s.dimensionByName(e.Dim); vd != nil {
					s.releasePOI(vd, e)
				}
				s.mobRemovedPassengerCleanup(e)
				s.entities.Remove(e.ID)
				s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			}
			continue
		}
		if e.LastHurtByPlayerTicks > 0 {
			e.LastHurtByPlayerTicks--
		}
		// Leashable.tickLeash: holder liveness → snap >12 → elastic
		// pull >6. Runs inside aiStep for every living mob.
		s.tickLeash(e)
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
		// On the spawn tick the touch is deferred: vanilla's sendChanges
		// runs before the living tick, so a freshly-paired mob replays
		// only its load-time dirty attrs and emits movement_speed's own
		// update_attributes a tracker tick later.
		if !e.firstTick {
			e.markAttrTouched("movement_speed")
		}
		// Entity.baseTick's fire pass: a fire-immune entity clearFire()s
		// instead of burning; otherwise the multiples of twenty each
		// deal a point of on_fire damage — skipped while the entity is
		// in lava — and the counter decrements; reaching zero relights
		// the shared flags byte without the on-fire bit.
		fireName, _ := EntityTypeName(e.Type)
		if e.FireTicks > 0 {
			if mobFireImmune[fireName] {
				e.FireTicks = 0
				s.broadcastMobSharedFlags(e.ID, false, e.GlowUntil > s.gameTime())
			} else {
				if e.FireTicks%20 == 0 && !entityInLavaWorld(s.dimWorld(e.Dim), e) {
					s.damageMob(e, 1, "on_fire", nil, &damageInfo{typeName: "on_fire"})
				}
				ticks := e.FireTicks - 1
				s.entities.SetFireTicks(e.ID, ticks)
				if ticks <= 0 {
					s.broadcastMobSharedFlags(e.ID, false, e.GlowUntil > s.gameTime())
				}
			}
		}
		// The GLOWING instance /raid glow applies expires after its
		// 1000 ticks — same shared-flags byte, bit 0x40 off.
		if e.GlowUntil != 0 && e.GlowUntil <= s.gameTime() {
			e.GlowUntil = 0
			s.broadcastMobSharedFlags(e.ID, e.FireTicks > 0, false)
		}
		// Entity.baseTick's lavaHurt: a non-fire-immune entity whose
		// box touches lava reignites for 15 seconds every tick and
		// takes a 4.0 lava hit — invulnerability frames inside
		// damageMob thin that to an effective 4 per 20 ticks, so a
		// 10-health pig dies to the contact in under two seconds.
		if !mobFireImmune[fireName] && entityInLavaWorld(s.dimWorld(e.Dim), e) {
			if e.FireTicks <= 0 {
				s.broadcastMobSharedFlags(e.ID, true, e.GlowUntil > s.gameTime())
			}
			s.entities.SetFireTicks(e.ID, 300)
			s.damageMob(e, 4, "lava", nil, &damageInfo{typeName: "lava"})
		}
		if e.invulnTicks > 0 {
			e.invulnTicks--
		}
		// LivingEntity.baseTick's isAlive block opens with the
		// suffocation arm — isInWall's eye-plane probe for a
		// suffocating non-air cell lands a 1.0 hit every tick, thinned
		// to the twenty-tick invulnerability window inside damageMob.
		if s.mobInWall(e, s.dimWorld(e.Dim)) {
			s.damageMob(e, 1, "in_wall", nil, &damageInfo{typeName: "in_wall"})
		}
		// LivingEntity.baseTick's hurtTime countdown.
		if e.HurtTime > 0 {
			e.HurtTime--
		}
		if e.loveTicks > 0 {
			e.loveTicks--
		}
		ambName, _ := EntityTypeName(e.Type)
		// LivingEntity.baseTick's freeze block: an entity out of
		// powder snow — or one that can't freeze — thaws at two ticks
		// per tick; a fully frozen freezable living entity takes the
		// freeze damage every forty ticks (×5 for
		// freeze_hurts_extra_types, folded by hurtServer's source
		// adjustment).
		if !e.IsInPowderSnow || freezeImmuneTypes[ambName] {
			if e.TicksFrozen > 0 {
				e.TicksFrozen -= 2
				if e.TicksFrozen < 0 {
					e.TicksFrozen = 0
				}
				e.MetaDirty = true
			}
		}
		// Player records' freeze damage runs session-side in
		// playerFluidTickStep — a record-level damageMob here would
		// drain the record's health instead of the session's.
		if e.SyncCount%40 == 0 && e.TicksFrozen >= 140 && !freezeImmuneTypes[ambName] && ambName != "player" {
			dmg := float32(1)
			if freezeHurtsExtraTypes[ambName] {
				dmg = 5
			}
			s.damageMob(e, dmg, "freeze", nil, &damageInfo{typeName: "freeze"})
		}
		// LivingEntity.baseTick's frost pass: removeFrost drops the
		// modifier unconditionally, then tryAddFrost re-adds it while
		// the entity is frozen and its on-block is non-air — the
		// -0.05*getPercentFrozen ADD_VALUE on MOVEMENT_SPEED.
		s.frostPass(e, s.dimWorld(e.Dim))
		// LivingEntity.baseTick's air block: an eye-level water cell
		// (a bubble column counts as out-of-water here) drains
		// airSupply one per tick while the mob can't breathe under
		// water; reaching -20 resets it to zero with the drown-particle
		// event (67) and 2 drown damage. decreaseAirSupply's
		// oxygen_bonus branch is unreachable — no mob path grants the
		// attribute. Everywhere else the supply climbs back by
		// increaseAirSupply up to the species getMaxAirSupply —
		// inside the drain cell only while one of the refilling
		// effects is on.
		maxAir := mobMaxAirSupply(ambName)
		if airD := s.dims[e.Dim]; airD != nil && mobWaterAnimalAir[ambName] {
			// WaterAnimal.baseTick captures the pre-tick supply, lets
			// LivingEntity's air block run, then overrides the supply:
			// in water it snaps to the species max, out of water it drops
			// one per tick and at -20 resets to zero with 2 damage
			// (dryOut on the axolotl and nautilus, drown elsewhere —
			// the axolotl's check is isInWaterOrRain). Axolotl and
			// nautilus gate handleAirSupply on !isNoAi; the fish,
			// squids, and tadpole run it unconditionally. The active override
			// overwrites it; NoAI still receives the ordinary refill.
			if waGate := ambName == "axolotl" || ambName == "nautilus"; !waGate || !e.NoAI {
				wet := s.mobInWater(e)
				if ambName == "axolotl" && !wet {
					feet := world.Pos{
						X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)),
						Z: int(math.Floor(e.Z)),
					}
					wet = s.fireRainingAt(airD, feet)
					if !wet {
						feet.Y = int(math.Floor(e.Y + entityBBDims(e)[1]))
						wet = s.fireRainingAt(airD, feet)
					}
				}
				if wet {
					if e.AirSupply != maxAir {
						e.AirSupply = maxAir
						e.MetaDirty = true
					}
				} else {
					e.AirSupply--
					e.MetaDirty = true
					if e.AirSupply <= -20 {
						e.AirSupply = 0
						src := "drown"
						if waGate {
							src = "dry_out"
						}
						s.damageMob(e, 2, src, nil, &damageInfo{typeName: src})
					}
				}
			} else if e.AirSupply < maxAir && (!s.mobEyeInWater(e, airD) || s.mobEyeInBubbleColumn(e, airD) || mobHasAirEffect(s, e)) {
				// NoAI skips only handleAirSupply, not LivingEntity's refill.
				e.AirSupply = mobIncreaseAirSupply(ambName, e.AirSupply)
				e.MetaDirty = true
			}
		} else if airD := s.dims[e.Dim]; s.mobEyeInWater(e, airD) && !s.mobEyeInBubbleColumn(e, airD) {
			if !mobBreathesWater[ambName] && !mobHasAirEffect(s, e) {
				e.AirSupply--
				e.MetaDirty = true
				if e.AirSupply <= -20 {
					e.AirSupply = 0
					s.broadcastEntityEventTo(nil, e.Dim, e.ID, 67)
					s.damageMob(e, 2, "drown", nil, &damageInfo{typeName: "drown"})
				}
			} else if e.AirSupply < maxAir && mobHasAirEffect(s, e) {
				e.AirSupply = mobIncreaseAirSupply(ambName, e.AirSupply)
				e.MetaDirty = true
			}
		} else if e.AirSupply < maxAir {
			e.AirSupply = mobIncreaseAirSupply(ambName, e.AirSupply)
			e.MetaDirty = true
		}
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
		// AgeableMob.aiStep's age step: babies count up toward grown
		// at 0 while canAgeUp (AgeLocked freezes the counter),
		// adults count a breeding cooldown down.
		if ageableTypes[ambName] && e.Age != 0 {
			if e.Age < 0 && !e.AgeLocked {
				e.Age++
				if e.Age == 0 {
					e.Baby = false
					e.MetaDirty = true
				}
			} else if e.Age > 0 {
				e.Age--
			}
		}
		// Mob.baseTick's ambient voice — independent of NoAI: counter
		// climbs from 0, fires when random < count, resets to
		// -interval. playHurtSound also resets the counter.
		// Breeze.playAmbientSound uses playLocalSound — a server-side
		// no-op (the client schedules it locally) — so no wire emit.
		if a, ok := mobAmbient[ambName]; ok && ambName != "breeze" {
			e.ambientSoundTime++
			if rand.Int31n(1000) < e.ambientSoundTime {
				e.ambientSoundTime = -a.interval
				snd := mobAmbientSoundID(e, ambName)
				// this.playSound → level.playSound(null, x,y,z,…) —
				// the positional clientbound sound packet.
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
		// LivingEntity.pushEntities runs every living tick regardless
		// of NoAI: the physical separation first, then the cramming
		// damage roll on the same overlap list.
		s.mobPushStep(e)
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
				e.MetaDirty = true
			} else if s.provokerDead(e) {
				e.AngryAt = [16]byte{}
				e.AngryUntil = 0
				e.MetaDirty = true
			} else if ambName == "zombified_piglin" && s.gameTime() >= e.nextAlertAt {
				e.nextAlertAt = s.gameTime() + 80 + rand.Int63n(41)
				s.piglinAlert(e)
			}
		}
		// Bee.customServerAiStep: the post-sting death clock ticks
		// even under NoAI — every 5th tick a
		// nextInt(clamp(1200-t,1,1200))==0 roll hurts the bee its own
		// health (damageSources().generic). stayOutOfHiveCountdown
		// counts down alongside.
		if ambName == "bee" {
			// Bee.customServerAiStep also counts
			// ticksWithoutNectarSinceExitingHive up while the bee
			// carries no nectar — the NBT-visible
			// TicksSincePollination counter. customServerAiStep is
			// NoAI-gated on vanilla (unlike aiStep's
			// stayOutOfHiveCountdown below), so the counter freezes
			// on a NoAI summon.
			if !e.HasNectar && !e.NoAI {
				e.TicksSincePollination++
			}
			if e.CannotEnterHive > 0 {
				e.CannotEnterHive--
			}
			// Bee.customServerAiStep's setRolling: rolls while angry,
			// un-stung, and the anger target is inside 2 blocks.
			roll := e.AngryAt != [16]byte{} && !e.Stung
			if roll {
				roll = false
				s.sessions.Range(func(_, v any) bool {
					sess := v.(*Session)
					if sess.uuid == e.AngryAt && sess.state.Load() == StatePlay && sess.dimName() == e.Dim {
						px, py, pz, _, _, _ := sess.Pos()
						dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
						roll = dx*dx+dy*dy+dz*dz < 4.0
						return false
					}
					return true
				})
			}
			if roll != e.Rolling {
				e.Rolling = roll
				e.MetaDirty = true
			}
			// Bee.aiStep's tail: every 20th tick a bee whose saved
			// hive position no longer names a hive block entity
			// drops the pointer (flower_pos has no equivalent
			// check).
			if e.BeeHivePosSet && s.clock.count%20 == 0 {
				bp := e.BeeHivePos
				w := s.dimWorld(e.Dim)
				if w == nil || !isBeeNestBlock(w.GetBlock(world.Pos{X: int(bp[0]), Y: int(bp[1]), Z: int(bp[2])})) {
					e.BeeHivePosSet = false
				}
			}
			if e.Stung {
				e.timeSinceSting++
				if e.timeSinceSting%5 == 0 {
					rem := 1200 - int(e.timeSinceSting)
					if rem < 1 {
						rem = 1
					}
					if rand.Intn(rem) == 0 {
						s.damageMob(e, e.Health, "generic", nil, nil)
						continue
					}
				}
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
			if e.Swell >= e.MaxSwell {
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
		// Vex.tick's noGravity + limited-life tail and Ravager.aiStep's
		// attack/stun/roar counters sit outside isEffectiveAi.
		if hostileTickTypes[ambName] && s.hostileTickPre(e, ambName) {
			continue
		}
		// Guardian.aiStep's flop prelude is the same placement — above
		// super.aiStep, so NoAI guardians flop too.
		if mobBeam[ambName] {
			s.guardianFlopStep(e, s.dimWorld(e.Dim))
		}
		// Camel.tick's dash tail: DASH clears once the cooldown is
		// below 50 AND the camel registers ground (onGround ||
		// isInLiquid || isPassenger — isInLiquid unmodeled);
		// the cooldown decays independently and fires the
		// dash_ready positional sound at 0. A ridden camel's
		// Grounded only reflects the driver's move_vehicle
		// packets, so a mounted NoAI camel keeps the flag on.
		// CamelHusk inherits the whole arm with its own sounds.
		if ambName == "camel" || ambName == "camel_husk" {
			// Camel.tick's water arm — tick()-level, so it stands a
			// sitting camel even under NoAI: isCamelSitting &&
			// isInWater → standUpInstantly (silent, tick-53 stamp).
			if e.LastPoseTick < 0 {
				if w := s.dimWorld(e.Dim); w != nil {
					px, py, pz := int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))
					if blockAt(w, world.Pos{X: px, Y: py, Z: pz}) == "water" {
						s.camelStand(e, true)
					}
				}
			}
			// Camel.tick's dash clear: once the cooldown dips past
			// the minimum-5-tick window, any of grounded / in-liquid /
			// isPassenger drops the flag (DASH=false emit).
			inLiquid := false
			if w := s.dimWorld(e.Dim); w != nil {
				feet := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
				_, _, inLiquid = fluidOf(w.GetBlock(feet))
			}
			if e.Dashing && e.DashCooldown < 50 && (e.Grounded || e.Riding != 0 || inLiquid) {
				e.Dashing = false
				e.MetaDirty = true
				if e.DashCooldown == 0 {
					e.DashCooldown = 55
				}
			}
			if e.DashCooldown > 0 {
				e.DashCooldown--
				if e.DashCooldown == 0 {
					readySnd := sndCamelDashReady
					if ambName == "camel_husk" {
						readySnd = sndCamelHuskDashReady
					}
					s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, readySnd, srcNeutral, 1.0, 1.0, nil)
				}
			}
		}
		// RangedAttackGoal.tick's attackTime countdown for the llama
		// family: armed to 40 by the hurt retaliation, it fires the
		// lone LlamaSpit at 0 — LlamaHurtByTargetGoal.canContinueToUse
		// then sees didSpit and drops the target, so no second shot
		// follows. A target that left the session just un-arms.
		if (ambName == "llama" || ambName == "trader_llama") && e.LlamaSpitTicks > 0 {
			e.LlamaSpitTicks--
			// lookControl keeps yBodyRot tracking the armed target at
			// ~30°/tick — the spit spawns 0.95 along it, so the turn
			// must finish before attackTime runs out or the shot
			// launches off-axis. LlamaSpitMob (a hunted entity eid)
			// wins over the player-attacker uuid arm when both exist.
			if m, ok := s.entities.GetPtr(e.LlamaSpitMob); ok && !m.Dying &&
				m.Dim == e.Dim {
				mobTurnToward(e, m.X-e.X, m.Z-e.Z, 30)
			} else if t := s.sessionByUUID(e.LlamaSpitTarget); t != nil &&
				!t.dead && t.spawned.Load() {
				tx, _, tz, _, _, _ := t.Pos()
				mobTurnToward(e, tx-e.X, tz-e.Z, 30)
			}
			if e.LlamaSpitTicks == 0 {
				if m, ok := s.entities.GetPtr(e.LlamaSpitMob); ok && !m.Dying &&
					m.Dim == e.Dim {
					s.llamaSpitAtEntity(e, m)
					// RangedAttackGoal re-arms through the hunter's
					// next tick — the mob target itself stays marked.
				} else if t := s.sessionByUUID(e.LlamaSpitTarget); t != nil &&
					!t.dead && t.spawned.Load() && t.dimension().name == e.Dim {
					s.llamaSpitAt(e, t)
				}
				e.LlamaSpitTarget = [16]byte{}
			}
		}
		// Chicken.aiStep's egg block: isAlive && !isBaby &&
		// !isChickenJockey && --eggTime <= 0 lays an egg at the mob's
		// position (CHICKEN_LAY's single-egg gift), plays
		// entity.chicken.egg, then resets to 6000+rand(6000).
		if ambName == "chicken" && !e.Baby && !e.ChickenJockey {
			e.EggLayTime--
			if e.EggLayTime <= 0 {
				s.chickenLayEgg(e)
				e.EggLayTime = 6000 + rand.Int31n(6000)
			}
		}
		// AbstractHorse.aiStep's eating block — canEatGrass is
		// unconditionally true, so the counter decay runs even on
		// NoAI mounts: isEating && ++eatingCounter > 50 clears the
		// 0x10 flag. The ambient roll (1/300, grass below, not
		// eating, not a vehicle) re-arms it.
		if equineTypes[ambName] {
			if !e.EatingHaystack && e.Riding == 0 && s.rng.intn(300) == 0 {
				if w := s.dimWorld(e.Dim); w != nil {
					pos := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y) - 1), Z: int(math.Floor(e.Z))}
					if bn, _ := world.StateOwner(w.GetBlock(pos)); strings.TrimPrefix(bn, "minecraft:") == "grass_block" {
						e.EatingHaystack = true
						e.MetaDirty = true
					}
				}
			}
			if e.EatingHaystack {
				e.eatingCounter++
				if e.eatingCounter > 50 {
					e.eatingCounter = 0
					e.EatingHaystack = false
					e.MetaDirty = true
				}
			}
			// AbstractHorse.tick's transient countdowns: mouth open
			// (flag 0x40) clears at mouthCounter >30, standing (0x20)
			// when standCounter ticks down to 0. Both run regardless of
			// NoAI — tick is not gated by isEffectiveAi.
			if e.mouthCounter > 0 {
				e.mouthCounter++
				if e.mouthCounter > 30 {
					e.mouthCounter = 0
					e.HorseMouth = false
					e.MetaDirty = true
				}
			}
			if e.standCounter > 0 {
				e.standCounter--
				if e.standCounter <= 0 {
					e.standCounter = 0
					e.HorseStanding = false
					e.MetaDirty = true
				}
			}
			// RunAroundLikeCrazyGoal ticks in the goalSelector —
			// isEffectiveAi gate. The step self-gates NoAI; an
			// untamed equine with a rider flees and rolls the
			// temper check per tick.
			s.equineRideTameStep(e, ambName)
		}
		// LivingEntity.aiStep's ridden branch precedes the
		// isEffectiveAi gate: a steerable carrying a controlling
		// player travels by travelRidden whether it has AI or not,
		// and its own navigation/goal travel is bypassed outright.
		if e.Riding == 0 {
			if w := s.dimWorld(e.Dim); w != nil && s.mobRiddenStep(e, w) {
				s.mobSyncLivingFrame(e)
				continue
			}
		}
		// Pufferfish.aiStep's sting tail is NOT inside vanilla's
		// isEffectiveAi gate — a NoAI fish with a positive puff state
		// still pokes whatever overlaps it.
		if ambName == "pufferfish" && e.PuffState > 0 && e.Health > 0 {
			s.puffSting(e)
			s.puffPlayerTouch(e)
		}
		// LivingEntity.aiStep's tail — a water-sensitive mob in water
		// or rain calls hurtServer(drown, 1) every tick; the enderman's
		// own hurtServer rolls the contact teleport off it. The check
		// sits inside isEffectiveAi only — riders still take it.
		if !e.NoAI && mobWaterSensitive[ambName] && s.mobInWaterOrRain(e) {
			s.damageMob(e, 1, "drown", nil, &damageInfo{typeName: "drown"})
		}
		if !e.NoAI && e.Riding == 0 && !nonMobLiving[ambName] {
			// Pufferfish.tick's inflate/deflate walk runs on the
			// counters the puff goal set last tick (isEffectiveAi
			// gate), then the goal itself — canUse = a scary
			// LivingEntity within 2.0 — arms or zeroes the counter.
			if ambName == "pufferfish" && e.Health > 0 {
				s.puffStateStep(e)
			}
			// Drowned/stray conversion bookkeeping — inside the NoAI
			// gate like vanilla's aiStep/tick placement.
			if s.mobConvertTick(e, s.dims[e.Dim], ambName) {
				continue
			}
			// TraderLlama/WanderingTrader.maybeDespawn (aiStep level) —
			// the countdown discard; runs beside the goal chain.
			if ambName == "trader_llama" || ambName == "wandering_trader" {
				if s.traderDespawn(e) {
					continue
				}
			}
			// LlamaFollowCaravanGoal (priority 2) — outranks even the
			// family's PanicGoal(3).
			if ambName == "llama" || ambName == "trader_llama" {
				if w := s.dimWorld(e.Dim); w != nil && s.llamaCaravanStep(e, w) {
					s.mobSyncLivingFrame(e)
					continue
				}
				// The mob-hunt targetSelector (llama→untamed wolf at 4,
				// trader_llama→zombie/illager at 16) + RangedAttackGoal
				// — its MOVE flag loses to a live caravan, so this arm
				// only runs when the caravan link is off.
				if w := s.dimWorld(e.Dim); w != nil && s.llamaHunterStep(e, w) {
					s.mobSyncLivingFrame(e)
					continue
				}
			}
			// PanicGoal (priority 1) outranks every goal below —
			// including sheep eat — while the panic run is live.
			if s.mobPanicStep(e, ambName) {
				s.mobSyncLivingFrame(e)
				continue
			}
			// SitWhenOrderedToGoal (priority 2 on wolf/cat/parrot).
			if ambName == "wolf" || ambName == "cat" || ambName == "parrot" {
				s.tamableSitPoseStep(e)
			}
			// FollowOwnerGoal (priority 6 on wolf/cat/parrot,
			// MOVE|LOOK, 1.0×speed, 10 start / 2 stop) — a tamed pet
			// follows its owner and teleports past 12 blocks on the
			// 10-tick recalc cadence.
			if followOwnerTypes[ambName] && s.followOwnerStep(e, ambName) {
				s.mobSyncLivingFrame(e)
				continue
			}
			// BreedGoal (priority 2-3): animals in love walk to their
			// mate and breed on contact.
			if s.mobBreedStep(e, ambName) {
				s.mobSyncLivingFrame(e)
				continue
			}
			// CatRelaxOnOwnerGoal (priority 3) — a tamed cat lying on
			// its sleeping owner's bed outranks tempt/follow.
			if ambName == "cat" && s.catRelaxStep(e) {
				s.mobSyncLivingFrame(e)
				continue
			}
			// FrogAi's IDLE RunOne keeps a running Croak: 60 ticks at
			// pose CROAKING(8). ShootTongue's extra start conditions
			// refuse pose CROAKING, so the croak outranks a pending
			// tongue until it lapses.
			if ambName == "frog" && e.Pose == 8 {
				s.frogCroakStep(e)
				s.mobSyncLivingFrame(e)
				continue
			}
			// FrogAi's TONGUE activity (ShootTongue) — the frog yanks
			// a FROG_FOOD cube mob in and eats it.
			if ambName == "frog" && s.frogTongueStep(e) {
				s.mobSyncLivingFrame(e)
				continue
			}
			// TemptGoal (priority 3-4) outranks eat/stroll: a player
			// holding the mob's food pulls it to 2.5 blocks.
			if s.mobTemptStep(e, ambName) {
				s.mobSyncLivingFrame(e)
				continue
			}
			// BegGoal (priority 9, LOOK flag): the wolf raises
			// DATA_INTERESTED_ID and tracks the food-holder's
			// head-yaw without navigating. The spawn-tick canUse
			// is skipped: vanilla's sendChanges runs before the
			// living tick, so a freshly-paired wolf's beg lands
			// its own pack a tracker tick after the non-default
			// replay — mc-go's end-of-tick flush defers it the
			// same way.
			if ambName == "wolf" && !e.firstTick {
				s.wolfBegStep(e)
			}
			// FollowParentGoal (priority 4-5): babies trail the
			// nearest same-type adult at 3-16 blocks.
			if s.mobFollowParentStep(e, ambName) {
				s.mobSyncLivingFrame(e)
				continue
			}
			// Sheep's EatBlockGoal (priority 5) — while the eat
			// animation runs the sheep stands still.
			if ambName == "sheep" && s.sheepEatTick(e, s.dims[e.Dim]) {
				s.mobSyncLivingFrame(e)
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
			if hostileGoalTypes[ambName] {
				// evoker/vex/ravager/spider goal selectors (hostile.go).
				goalActed = s.hostileGoalStep(e, ambName)
			} else if s.mobFleeStep(e, ambName) {
				goalActed = true
			} else if mobRanged[ambName] {
				if s.mobHoldsBow(e) {
					goalActed = s.mobRangedStep(e)
				} else {
					goalActed = s.mobMeleeStep(e)
				}
				if ambName == "wither_skeleton" {
					if goalActed {
						// A live player mark (the @2
						// NearestAttackableTargetGoal) preempts the
						// @3 piglin hunt — its hold clears while a
						// combat goal runs.
						e.HuntMob = 0
					} else {
						goalActed = s.witherPiglinStep(e)
					}
				}
			} else if mobSwell[ambName] {
				goalActed = s.mobSwellStep(e, ambName)
			} else if ambName == "witch" {
				goalActed = s.mobWitchAttackStep(e)
			} else if mobCrossbow[ambName] && s.mobHoldsCrossbow(e) {
				goalActed = s.mobCrossbowStep(e, ambName)
			} else if mobBeam[ambName] {
				goalActed = s.mobGuardianStep(e, ambName)
			} else if ambName == "enderman" {
				// customServerAiStep's daylight flee runs ahead of
				// every goal — a bright-sky roll clears the target
				// and teleports the mob.
				s.enderDaylightFlee(e)
				// TakeBlockGoal/LeaveBlockGoal are ordinary goals —
				// they never gate movement, so the carry step runs
				// alongside the stare/melee dispatch.
				s.enderCarryStep(e)
				// EndermanLookForPlayerGoal + FreezeWhenLookedAt run
				// ahead of the melee chase — a live stare freezes or
				// teleports the mob instead of walking.
				goalActed = s.enderStareStep(e) || s.mobMeleeStep(e)
			} else if flyerMobs[ambName] {
				// Blaze/Ghast/Phantom goal sets (flyer.go).
				goalActed = s.flyerStep(e, ambName)
			} else if mobMelee[e.Type] {
				goalActed = s.mobMeleeStep(e)
			}
			if goalActed {
				s.mobSyncLivingFrame(e)
				continue
			}
			w := s.dimWorld(e.Dim)
			if w != nil {
				// Goal priority order once combat has declined:
				// LongDistancePatrolGoal (4) ahead of FleeSunGoal (5)
				// ahead of the ambient wander.
				if !s.mobPatrolStep(e, s.dims[e.Dim], w) &&
					!(e.FireTicks > 0 && mobSunFlees[ambName] && s.mobSunFleeStep(e, s.dims[e.Dim], w)) {
					if mobBeam[ambName] {
						s.guardianMoveStep(e, w)
					} else if ambName == "slime" || ambName == "magma_cube" {
						// AbstractCubeMob's goal set — the hop
						// rhythm replaces both chase and wander.
						s.cubeHopStep(e, w, ambName)
					} else {
						s.mobMoveStep(e, w)
					}
				}
			}
			// LookAtPlayerGoal + RandomLookAroundGoal run alongside
			// the move goals — the head re-aims on its own cadence.
			s.mobLookStep(e)
		}
		if e.Riding == 0 && nonMobLiving[ambName] && !armorStandSkipsTravel(e) {
			// LivingEntity.tick for a non-Mob: no goal selector, but
			// the physics chain still runs — baseTick's
			// updateFluidInteraction pushes the fluid current into
			// the delta and aiStep's zero-input travel integrates
			// gravity + fluid drag. An armor stand falls, sinks, and
			// drifts with the current; it just never wanders.
			if w := s.dimWorld(e.Dim); w != nil {
				bounds := entityBBDims(e)
				entityFluidTick(w, s.dims[e.Dim], e, bounds[0]/2, bounds[1])
				e.NeedsSync = true
				s.mobTravelStep(e, w, 0)
			}
		}
		s.mobSyncLivingFrame(e)
	}
	// Entity.tick's positionRider pass: every living vehicle welds
	// its non-player passengers to the rotated PASSENGER attachment
	// after the vehicle's own move — a jockey zombie rides welded to
	// the chicken's (0,0.7,-0.1) seat. Players are seated by
	// rideTickStep instead.
	for _, e := range mobs {
		if len(e.Passengers) == 0 {
			continue
		}
		for _, pid := range e.Passengers {
			rp, ok := s.entities.GetPtr(pid)
			if !ok || rp.Dying {
				continue
			}
			if rname, _ := EntityTypeName(rp.Type); rname == "player" {
				continue
			}
			nx, ny, nz := passengerSeatPos(e, rp)
			if rp.X != nx || rp.Y != ny || rp.Z != nz {
				rp.X, rp.Y, rp.Z = nx, ny, nz
				rp.WeldedPos = &[3]float64{nx, ny, nz}
				rp.PosDirty = true
				rp.NeedsSync = true
				s.mobSyncFlush(rp)
			}
		}
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
// a solid block is underfoot, then vanilla's RandomStrollGoal — idle
// rolls every strollInterval ticks, then walk to a picked target at the
// movement_speed attribute until arrival, ledge, or give-up. It only
// mutates entity state — the wire emission is mobSyncFlush's sendChanges
// selection. A NoAI mob never reaches it: LivingEntity.aiStep gates the
// whole travel branch on isEffectiveAi, which NoAI makes false — a
// NoAI entity floats where it stands, welded rider or not.
// mobStriderWarmStep is Strider's suffocation bookkeeping: the mob is
// warm when its own block position or its on-block sits in lava, or
// when a positive lava fluid depth encloses it; a strider riding a
// non-suffocating strider also stays warm. Every change flips the
// transient "suffocating" movement_speed modifier and re-arms the
// attribute resend.
func (s *Server) mobStriderWarmStep(e *Entity, w *world.World, inLava bool) {
	px := int(math.Floor(e.X))
	py := int(math.Floor(e.Y))
	pz := int(math.Floor(e.Z))
	warm := blockAt(w, world.Pos{X: px, Y: py, Z: pz}) == "lava" ||
		blockAt(w, world.Pos{X: px, Y: py - 1, Z: pz}) == "lava" ||
		inLava
	if !warm && e.Riding != 0 {
		s.entities.mu.RLock()
		parent, ok := s.entities.byID[e.Riding]
		s.entities.mu.RUnlock()
		if ok {
			pname, _ := EntityTypeName(parent.Type)
			warm = pname == "strider" && !parent.Suffocating
		}
	}
	if suf := !warm; suf != e.Suffocating {
		e.Suffocating = suf
		e.MetaDirty = true
		if suf {
			if e.AttrMods == nil {
				e.AttrMods = map[string]map[string]attrMod{}
			}
			if e.AttrMods["movement_speed"] == nil {
				e.AttrMods["movement_speed"] = map[string]attrMod{}
			}
			e.AttrMods["movement_speed"]["suffocating"] = attrMod{value: -0.34, op: 1}
		} else {
			delete(e.AttrMods["movement_speed"], "suffocating")
		}
		e.markAttrDirty("movement_speed")
	}
}

func (s *Server) mobMoveStep(e *Entity, w *world.World) {
	bounds := entityBBDims(e)
	// LivingEntity.aiStep's tail pushes the walk input via
	// Entity.push every tick — even all-zero — and push() latches
	// needsSync, so a living mob is motion-emit eligible on every
	// sendChanges pass (the drift check decides, not the gate).
	e.NeedsSync = true
	// Entity.tick's updateFluidInteraction runs for every entity —
	// isPushedByFluid is default-true, so the current push applies to
	// mobs too (items/orbs take their own path in itemTickStep).
	wDepth, inWater, lDepth, inLava := entityFluidTick(w, s.dims[e.Dim], e, bounds[0]/2, bounds[1])
	// Strider.tick's setSuffocating — gated on !isNoAi, before
	// Animal.tick. Warm = STRIDER_WARM_BLOCKS ({lava}) at the strider's
	// own block position or its on-block, or a positive lava fluid
	// height; a strider riding a non-suffocating strider also stays
	// warm. Every change also flips the transient "suffocating"
	// movement_speed modifier and re-arms the attribute resend.
	if name, _ := EntityTypeName(e.Type); name == "strider" && !e.NoAI {
		s.mobStriderWarmStep(e, w, inLava)
	}
	// FloatGoal.canUse: deep water (submersion past the eye-level
	// jump threshold) or any lava activates the priority-0 goal —
	// its tick jumps on an 80% roll, and LivingEntity.aiStep's jump
	// branch converts the flag into fluid climb or a floor hop.
	// Only the mobHasFloatGoal types register the goal; the rest
	// sink (a wither_skeleton sits at the lava bed).
	thresh := floatJumpThreshold(e, bounds)
	name, _ := EntityTypeName(e.Type)
	if mobHasFloatGoal[name] && ((inWater && wDepth > thresh) || inLava) {
		if floatGoalRoll(e.ID, e.WanderTicks2) {
			e.Jumping = true
		}
	}
	// aiStep's jump chain (measured on 26.2): the fluid probe h takes
	// LAVA's height first when both tags match, b1 is the deep-water
	// test, then a three-arm if — jumpInLiquid(WATER) when swimming
	// (off ground or deeper than the threshold), jumpInLiquid(LAVA)
	// when in lava and off ground or past the shallow line, and
	// jumpFromGround otherwise. A mob resting at the lava bed gets
	// only the +0.04 climbs while ungrounded — roughly canceling the
	// g/4 lava sink, which is why vanilla mobs sit at the bed instead
	// of bobbing the surface.
	if e.Jumping {
		h := wDepth
		if inLava {
			h = lDepth
		}
		b1 := inWater && h > 0
		switch {
		case b1 && (!e.Grounded || h > thresh):
			// jumpInLiquid(WATER): a fixed +0.04 climb.
			e.MotionY += 0.04
		case inLava && (!e.Grounded || lDepth > thresh):
			// jumpInLiquid(LAVA): the same +0.04 climb.
			e.MotionY += 0.04
		case (e.Grounded || (b1 && h <= thresh)) && e.NoJumpDelay == 0:
			// jumpFromGround: the floor hop — jump_strength (0.42
			// base) then the 10-tick delay.
			name, _ := EntityTypeName(e.Type)
			jump := 0.42
			if j, ok := entityAttrBases[name]["jump_strength"]; ok {
				jump = j
			}
			e.MotionY = jump
			e.NoJumpDelay = 10
		}
	} else {
		// not jumping: vanilla clears the cooldown outright.
		e.NoJumpDelay = 0
	}
	e.Jumping = false
	if e.NoJumpDelay > 0 {
		e.NoJumpDelay--
	}
	// shouldTravelInFluid: a mob whose box touches water or lava
	// travels by the fluid chain regardless of grounded state — the
	// slow fluid sink replaces the air fall, so mobGrounded never
	// runs. entityFluidTick already scanned both tags this tick.
	if !(inWater && wDepth > 0) && !(inLava && lDepth > 0) && !s.mobGrounded(e, w) {
		return
	}
	e.WanderTicks++
	// FishSwimGoal is RandomSwimmingGoal — a faster cadence
	// (interval 40) and a water-cell target draw. The ground
	// wander's ledge rule would park every fish, and the generic
	// ±10 target draws walk them out of the water onto land.
	if fishWanderTypes[name] {
		s.fishSwimStep(e, w, name, inWater, wDepth)
		return
	}
	// SitWhenOrderedToGoal's flag set (MOVE+JUMP) leaves a sitting
	// tame with no goal input: residual push/walk decay only.
	if e.SittingPose && (name == "wolf" || name == "cat" || name == "parrot") {
		s.mobTravelStep(e, w, 0)
		return
	}
	// RunAroundLikeCrazyGoal holds the MOVE flag while an untamed
	// equine carries a rider — the flee drive replaces the land
	// stroll for as long as the goal runs.
	if e.FleeRidden {
		s.equineFleeStep(e, w, name)
		return
	}
	// Camel's Brain-side pose machine — RandomSitting's repick draw
	// and the tickRidden forward-input stand; refuseToMove freezes
	// the drive while the camel sits or transitions. The water arm
	// already ran in mobTickStep's NoAI-free block above.
	if (name == "camel" || name == "camel_husk") && s.camelPoseStep(e, w, inWater) {
		s.mobTravelStep(e, w, 0)
		return
	}
	if !e.Strolling {
		// Goal's canUse: interval-tick roll (reducedTickDelay(120) in
		// vanilla — a ~6s mean idle between strolls).
		if e.WanderTicks%strollIntervalFor(e.ID) != 0 {
			// travel with no goal input: the residual walk/push delta
			// integrates into the position while it decays — overlapping
			// AI mobs drift apart; NoAI mobs never reach this line.
			s.mobTravelStep(e, w, 0)
			return
		}
		// FrogAi IDLE's RunOne draw: Croak weight 3 sits against
		// RandomStroll 1 + lookwalk 1 + the onGround no-op 2 — the
		// no-op repicks the same tick, so the croak pick lands 3/5.
		// Croak's start gates: WALK_TARGET absent (this repick slot),
		// pose STANDING, and Croak.start's !isInLiquid.
		if name == "frog" && e.Pose == 0 && !inWater && !inLava &&
			frogCroakRoll(e.ID, e.WanderTicks) {
			e.Pose = 8 // Pose.CROAKING
			e.MetaDirty = true
			e.FrogCroakTimer = 0
			s.mobTravelStep(e, w, 0)
			return
		}
		// getPosition: DefaultRandomPos.getPos(mob, 10, 7) — a target
		// within ±10 on x/z.
		e.Strolling = true
		e.StrollSteps = 0
		e.StrollTX = e.X + strollOffset(e.ID, e.WanderTicks)
		e.StrollTZ = e.Z + strollOffset(e.ID, e.WanderTicks+7)
	}
	e.StrollSteps++
	tx, tz := e.StrollTX-e.X, e.StrollTZ-e.Z
	dist := math.Hypot(tx, tz)
	// Navigation stops on arrival; a long stall means the path was
	// unreachable and vanilla gives up on it too.
	if dist < 0.4 || e.StrollSteps > 160 {
		e.Strolling = false
		e.WanderTicks = 0
		s.mobTravelStep(e, w, 0)
		return
	}
	speed, ok := entityAttrBases[name]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	step := speed
	if step > dist {
		step = dist
	}
	mobTurnToward(e, tx, tz, 90)
	// The head tracks the walk heading via the look lerp, not a jump.
	e.LookYaw = e.Yaw
	nx := e.X + tx/dist*step
	nz := e.Z + tz/dist*step
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
				// The path is cut — the goal gives up; FleeSunGoal
				// picks the shade target separately.
				e.Strolling = false
				e.WanderTicks = 0
				s.mobTravelStep(e, w, 0)
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
	// A fluid cell is swimmable, not an obstacle — the wander walks
	// into water the way vanilla's random pos targets cross it.
	spaceBlocked := false
	if nb := w.GetBlock(nextSpace); nb != world.Air && !moveNoCollision(nb) {
		spaceBlocked = true
	}
	if w.GetBlock(nextBelow) == world.Air || spaceBlocked {
		// Ledge or obstacle: the path is unreachable — the goal ends.
		// In water the swim drive keeps pressing the block — the wall
		// contact is what lets FloatGoal's hop fire next tick.
		if inWater && wDepth > 0 {
			s.mobTravelStep(e, w, speed)
		} else {
			s.mobTravelStep(e, w, 0)
		}
		e.Strolling = false
		e.WanderTicks = 0
		return
	}
	// MOVE_TO's walk drive: zza lands on the delta and travel carries
	// the momentum — the bundled-move wire in mobSyncFlush picks up
	// whatever the chain leaves in MotionX/Z.
	s.mobTravelStep(e, w, speed)
}

// mobTurnToward is MoveControl's setYRot(rotlerp(yRot, atan2-target,
// maxDeg)): the body yaw turns at most maxDeg° toward the wanted
// position per tick and travel's input lands along the CURRENT yaw —
// that's what curves vanilla walks while the mob is still turning.
// MOVE_TO passes 90; the look-driven ranged goals pass ~30.
func mobTurnToward(e *Entity, tx, tz, maxDeg float64) {
	target := float32(math.Atan2(-tx, tz) * 180 / math.Pi)
	d := target - e.Yaw
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	if d > float32(maxDeg) {
		d = float32(maxDeg)
	} else if d < float32(-maxDeg) {
		d = float32(-maxDeg)
	}
	e.Yaw += d
}

// mobTravelStep is LivingEntity.travel's grounded half for an
// AI-driven walk, measured off 26.2 bytecode. MOVE_TO's drive enters
// the delta as zza·f — zza is the goal's speed (speedModifier ×
// movement_speed), f is getFrictionInfluencedSpeed (speed itself on
// friction ≤ 0.6, speed·0.21600002/f³ on slipperier floors); the input
// lands along the body yaw, move() sweeps the delta through blocks and
// post-multiplies it by the containing block's speedFactor, then
// travelInAir's tail takes gravity off y and slides x/z by
// blockFriction·0.91. drive 0 is the idle case: no input, but residual
// momentum still integrates and decays.
func (s *Server) mobTravelStep(e *Entity, w *world.World, speed float64) {
	s.mobTravelStepPair(e, w, speed, speed, 0)
}

// mobTravelStepPair is the same travel pass with the full input pair:
// MoveControl's STRAFE tick sets zza/xxa while MOVE_TO drives only the
// forward half. When speed > 0 the drive sets the speed field and the
// input pair; the pair is length-capped at 1 the way vanilla's
// inputDirectionVector normalizes, then rotated by the body yaw.
func (s *Server) mobTravelStepPair(e *Entity, w *world.World, speed, zIn, xIn float64) {
	if w == nil {
		return
	}
	name, _ := EntityTypeName(e.Type)
	bounds := entityBBDims(e)
	gravity := 0.08
	// shouldTravelInFluid: the box overlapping water flips travel to
	// travelInWater — measured off 26.2 bytecode: moveRelative(0.02,
	// zza-drive) (a mob's water_movement_efficiency is 0), then
	// Entity.move, then delta·(0.8,0.8,0.8) and the
	// getFluidFallingAdjustedMovement sink — dy -= effectiveGravity/16
	// (0.005), terminal ≈ -0.025, so land mobs sink slowly and walk
	// the bed rather than floating.
	if d := s.dims[e.Dim]; d != nil {
		wDepth, inWater, _, _, _, _ := entityFluidScan(w, e.X, e.Y, e.Z, bounds[0]/2, bounds[1], fluidWater)
		if inWater && wDepth > 0 {
			e.FallDist = 0
			if speed > 0 {
				e.WalkSpeed = speed
				e.WalkZZA = zIn
				e.WalkXXA = xIn
			} else {
				// MoveControl's WAIT op zeroes the input pair and the
				// speed field every tick — no residue keeps pumping.
				e.WalkSpeed = 0
				e.WalkZZA = 0
				e.WalkXXA = 0
			}
			zx, xx := e.WalkZZA, e.WalkXXA
			if l := math.Hypot(zx, xx); l > 1 {
				zx /= l
				xx /= l
			}
			falling := e.MotionY <= 0
			y0 := e.Y
			yr := float64(e.Yaw) * math.Pi / 180
			e.MotionX += 0.02 * (xx*math.Cos(yr) - zx*math.Sin(yr))
			e.MotionZ += 0.02 * (xx*math.Sin(yr) + zx*math.Cos(yr))
			if e.NoGravity {
				gravity = 0
			}
			s.entityMove(e, w, bounds[0]/2, bounds[1], e.MotionX, e.MotionY, e.MotionZ, gravity, 1.0, true)
			e.MotionX *= 0.8
			e.MotionZ *= 0.8
			e.MotionY *= 0.8
			dy := e.MotionY - gravity/16
			// getFluidFallingAdjustedMovement's near-terminal snap.
			if falling && math.Abs(e.MotionY-0.005) < 0.003 && math.Abs(e.MotionY-gravity/16) < 0.003 {
				dy = -0.003
			}
			e.MotionY = dy
			// travelInWater's tail calls jumpOutOfFluid(y0) every tick —
			// not the threshold — checking headroom at the post-move
			// extrapolation for the wall hop that pops a mob out at the
			// pool edge.
			s.mobJumpOutOfFluid(w, e, y0, bounds)
			return
		}
		// travelInLava — same dispatch: moveRelative(0.02), move(),
		// then the shallow split on isInShallowFluid(LAVA) (depth ≤ the
		// fluid jump threshold): shallow damps {0.5,0.8,0.5} plus the
		// fall adjust, deep scales 0.5; effectiveGravity bites at d/4
		// — four times water's d/16 — and jumpOutOfFluid(y0) tails it.
		lDepth, inLava, _, _, _, _ := entityFluidScan(w, e.X, e.Y, e.Z, bounds[0]/2, bounds[1], fluidLava)
		if inLava && lDepth > 0 {
			if speed > 0 {
				e.WalkSpeed = speed
				e.WalkZZA = zIn
				e.WalkXXA = xIn
			} else {
				e.WalkSpeed = 0
				e.WalkZZA = 0
				e.WalkXXA = 0
			}
			zx, xx := e.WalkZZA, e.WalkXXA
			if l := math.Hypot(zx, xx); l > 1 {
				zx /= l
				xx /= l
			}
			falling := e.MotionY <= 0
			y0 := e.Y
			yr := float64(e.Yaw) * math.Pi / 180
			e.MotionX += 0.02 * (xx*math.Cos(yr) - zx*math.Sin(yr))
			e.MotionZ += 0.02 * (xx*math.Sin(yr) + zx*math.Cos(yr))
			if e.NoGravity {
				gravity = 0
			}
			s.entityMove(e, w, bounds[0]/2, bounds[1], e.MotionX, e.MotionY, e.MotionZ, gravity, 1.0, true)
			thresh := floatJumpThreshold(e, bounds)
			if lDepth <= thresh {
				e.MotionX *= 0.5
				e.MotionY *= 0.8
				e.MotionZ *= 0.5
				dy := e.MotionY - gravity/16
				if falling && math.Abs(e.MotionY-0.005) < 0.003 && math.Abs(e.MotionY-gravity/16) < 0.003 {
					dy = -0.003
				}
				e.MotionY = dy
			} else {
				e.MotionX *= 0.5
				e.MotionY *= 0.5
				e.MotionZ *= 0.5
			}
			if gravity != 0 {
				e.MotionY -= gravity / 4
			}
			s.mobJumpOutOfFluid(w, e, y0, bounds)
			return
		}
	}
	if g, ok := entityAttrBases[name]["gravity"]; ok {
		gravity = g
	}
	// travelInAir's friction split: grounded picks up the supporting
	// block's friction (capped by computeModifiedFriction, identity at
	// friction_modifier 1.0); airborne legs slide on 1.0 and drive at
	// getFlyingSpeed — a mob's 0.02, not the attribute.
	// applyInput decays the walk drive ×0.98 each tick only while a
	// goal is driving: MoveControl.tick with operation=WAIT (no move
	// goal ran) zeroes zza/xxa/speed outright — the residue decays
	// instantly and the slide is pure friction, not pumped motion.
	if speed > 0 {
		e.WalkSpeed = speed
		e.WalkZZA = zIn
		e.WalkXXA = xIn
	} else {
		e.WalkSpeed = 0
		e.WalkZZA = 0
		e.WalkXXA = 0
	}
	f3 := 1.0
	f := 0.02
	if e.Grounded {
		f3 = entityFrictionBelow(w, e.X, e.Y, e.Z)
		f = e.WalkSpeed
		if f3 > 0.6 {
			f = e.WalkSpeed * 0.21600002 / (f3 * f3 * f3)
		}
	}
	zx, xx := e.WalkZZA, e.WalkXXA
	if l := math.Hypot(zx, xx); l > 1 {
		zx /= l
		xx /= l
	}
	yr := float64(e.Yaw) * math.Pi / 180
	e.MotionX += f * (xx*math.Cos(yr) - zx*math.Sin(yr))
	e.MotionZ += f * (xx*math.Sin(yr) + zx*math.Cos(yr))
	s.entityMove(e, w, bounds[0]/2, bounds[1], e.MotionX, e.MotionY, e.MotionZ, gravity, 0.98, true)
	e.MotionY = (e.MotionY - gravity) * 0.98
	drag := f3 * 0.91
	e.MotionX *= drag
	e.MotionZ *= drag
}

// mobHasFloatGoal is the registerGoals set measured off the 26.2
// jar: only these types add FloatGoal (priority 0), so only they get
// the aiStep jump-in-liquid climbs. Zombies, skeletons, piglins and
// most other monsters do NOT register it — they sink to the bed of
// any fluid, which is why a vanilla wither_skeleton sits at the
// bottom of a lava pool instead of bobbing the surface.
var mobHasFloatGoal = map[string]bool{
	"bee": true, "chicken": true, "cow": true, "mooshroom": true,
	"horse": true, "donkey": true, "mule": true, "camel": true,
	"skeleton_horse": true, "zombie_horse": true, "llama": true,
	"trader_llama": true, "cat": true, "ocelot": true, "fox": true,
	"happy_ghast": true, "panda": true, "parrot": true, "pig": true,
	"polar_bear": true, "rabbit": true, "sheep": true, "wolf": true,
	"creeper": true, "enderman": true, "endermite": true,
	"ravager": true, "silverfish": true, "vex": true, "witch": true,
	"slime": true, "magma_cube": true, "evoker": true,
	"illusioner": true, "pillager": true, "vindicator": true,
	"spider": true, "cave_spider": true, "wandering_trader": true,
}

// floatGoalRoll is FloatGoal.tick's `nextFloat() < 0.8` — the 80%
// per-tick jump roll while the goal is active, keyed off the look
// counter like the other deterministic goal rolls.
func floatGoalRoll(eid int32, ticks int64) bool {
	h := uint64(uint32(eid))*40503 ^ uint64(ticks)*1099511628211
	return float64(h%1000)/1000 < 0.8
}

// floatJumpThreshold is LivingEntity.getFluidJumpThreshold — the
// submersion depth that switches jumpInLiquid to the wall-climb
// hop: 0.4 unless the eye sits below 0.4.
func floatJumpThreshold(e *Entity, bounds [2]float64) float64 {
	if bounds[1]*0.85 < 0.4 {
		return 0
	}
	return 0.4
}

// entityBoxFree is Entity.isFree(x, y, z) — whether a bounding box
// placed at the absolute position has no solid-cell overlap. The
// jumpOutOfFluid branch queries the escape space above the lip.
func (s *Server) entityBoxFree(w *world.World, x, y, z, half, height float64) bool {
	for bx := int(math.Floor(x - half)); float64(bx) <= x+half; bx++ {
		for by := int(math.Floor(y)); float64(by) <= y+height; by++ {
			for bz := int(math.Floor(z - half)); float64(bz) <= z+half; bz++ {
				b := w.GetBlock(world.Pos{X: bx, Y: by, Z: bz})
				if !world.NonSolid(b) && !moveNoCollision(b) && float64(by)+collisionTopOf(b) > y {
					return false
				}
			}
		}
	}
	return true
}

// mobJumpOutOfFluid is LivingEntity.jumpOutOfFluid(arg): while
// horizontally colliding, an entity whose box moved by the vector
// (delta.x, delta.y + 0.6 - getY() + arg, delta.z) hits nothing gets
// its vertical velocity snapped to 0.3 — the tail call in
// travelInWater/travelInLava passes the pre-move Y, the aiStep jump
// branch passes getFluidJumpThreshold. isFree translates the box by
// the vector, so the absolute probe is (x+dx, dy+0.6+arg, z+dz).
func (s *Server) mobJumpOutOfFluid(w *world.World, e *Entity, arg float64, bounds [2]float64) {
	if !e.HColl {
		return
	}
	if s.entityBoxFree(w, e.X+e.MotionX, e.MotionY+0.6+arg, e.Z+e.MotionZ, bounds[0]/2, bounds[1]) {
		e.MotionY = 0.3
	}
}

// strollIntervalFor is the per-entity stroll cadence: vanilla rolls
// nextInt(reducedTickDelay(120)) per idle tick — a ~120-tick mean
// idle — pinned here to a deterministic 100-140 tick cycle so test
// windows always contain strolls.
func strollIntervalFor(eid int32) int64 {
	return 100 + int64((uint32(eid)*2654435761>>16)%41)
}

// fishWanderTypes is the AbstractFish family — their goal is
// AbstractFish.FishSwimGoal(RandomSwimmingGoal(1.0, 40)) instead of
// the land RandomStrollGoal, so the stroll cadence, the target draw
// and the ground-support rules all differ.
var fishWanderTypes = map[string]bool{
	"cod":           true,
	"salmon":        true,
	"tropical_fish": true,
	"pufferfish":    true,
	"tadpole":       true,
}

// puffDimScale is Pufferfish.getScale — the puff state's dimension
// multiplier on the 0.7 base box.
var puffDimScale = [3]float64{0.5, 0.7, 1.0}

// puffStateStep is Pufferfish.tick's inflate/deflate walk +
// PufferfishPuffGoal + Pufferfish.aiStep's sting, in vanilla order:
// the state machine runs on the counters last tick's goal set, then
// the goal re-arms or zeroes them, then the sting hits whatever the
// fresh state touches. State transitions also play blow_up/blow_out
// at the fish and fire refreshDimensions — the resize is unmodeled.
func (s *Server) puffStateStep(e *Entity) {
	if e.inflateCounter > 0 {
		if e.PuffState == 0 {
			e.PuffState = 1
			e.MetaDirty = true
			if id, ok := SoundEventID("entity.puffer_fish.blow_up"); ok {
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 1.0, voicePitchE(e), nil)
			}
		} else if e.inflateCounter > 40 && e.PuffState == 1 {
			e.PuffState = 2
			e.MetaDirty = true
			if id, ok := SoundEventID("entity.puffer_fish.blow_up"); ok {
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 1.0, voicePitchE(e), nil)
			}
		}
		e.inflateCounter++
	} else if e.PuffState != 0 {
		if e.deflateTimer > 60 && e.PuffState == 2 {
			e.PuffState = 1
			e.MetaDirty = true
			if id, ok := SoundEventID("entity.puffer_fish.blow_out"); ok {
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 1.0, voicePitchE(e), nil)
			}
		} else if e.deflateTimer > 100 && e.PuffState == 1 {
			e.PuffState = 0
			e.MetaDirty = true
			if id, ok := SoundEventID("entity.puffer_fish.blow_out"); ok {
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 1.0, voicePitchE(e), nil)
			}
		}
		e.deflateTimer++
	}
	// PufferfishPuffGoal: canUse/canContinueToUse both test for a
	// scary LivingEntity inside the bounding box inflated 2.0; start
	// arms the counter and clears the deflate clock, stop zeroes it.
	if s.puffScaryNear(e) {
		if e.inflateCounter == 0 {
			e.inflateCounter = 1
			e.deflateTimer = 0
		}
	} else {
		e.inflateCounter = 0
	}
}

// puffScaryNear is the PufferfishPuffGoal.canUse target scan:
// TARGETING_CONDITIONS is forNonCombat + ignoreInvisibility +
// ignoreLineOfSight + the NOT_SCARY_FOR_PUFFERFISH selector, so any
// living, non-spectator entity outside the tag inside
// bbox.inflate(2.0) counts — creative players are still scary.
func (s *Server) puffScaryNear(e *Entity) bool {
	b := entityBBDims(e)
	if e.PuffState >= 0 && e.PuffState <= 2 {
		b[0] *= puffDimScale[e.PuffState]
		b[1] *= puffDimScale[e.PuffState]
	}
	halfW := b[0]/2 + 2.0
	top := e.Y + b[1] + 2.0
	bot := e.Y - 2.0
	inside := func(x, y, z float64, w, h float64) bool {
		return math.Abs(x-e.X) < halfW+w/2 &&
			math.Abs(z-e.Z) < halfW+w/2 &&
			y+h > bot && y < top
	}
	for _, o := range s.entities.All() {
		if o.ID == e.ID || !o.Living || o.Dim != e.Dim || o.Health <= 0 {
			continue
		}
		oname, _ := EntityTypeName(o.Type)
		if entityTypeInTag(oname, "not_scary_for_pufferfish") {
			continue
		}
		ob := entityBBDims(&o)
		if inside(o.X, o.Y, o.Z, ob[0], ob[1]) {
			return true
		}
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead ||
			sess.dimName() != e.Dim || sess.mode() == GameModeSpectator {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if inside(px, py, pz, 0.6, 1.8) {
			return false
		}
		return true
	})
	return false
}

// puffSting is Pufferfish.aiStep's contact arm: while puffed, every
// Mob inside bbox.inflate(0.3) takes a mobAttack hit of 1+state,
// POISON for 60*state ticks, and the sting sound — the player arm is
// puffPlayerTouch, called from the move-packet contact path.
func (s *Server) puffSting(e *Entity) {
	b := entityBBDims(e)
	scale := puffDimScale[0]
	if e.PuffState >= 0 && e.PuffState <= 2 {
		scale = puffDimScale[e.PuffState]
	}
	halfW := b[0]*scale/2 + 0.3
	top := e.Y + b[1]*scale + 0.3
	bot := e.Y - 0.3
	for _, o := range s.entities.All() {
		if o.ID == e.ID || !o.Living || o.Dim != e.Dim || o.Health <= 0 {
			continue
		}
		oname, _ := EntityTypeName(o.Type)
		if entityTypeInTag(oname, "not_scary_for_pufferfish") {
			continue
		}
		ob := entityBBDims(&o)
		if math.Abs(o.X-e.X) >= halfW+ob[0]/2 ||
			math.Abs(o.Z-e.Z) >= halfW+ob[0]/2 ||
			o.Y+ob[1] <= bot || o.Y >= top {
			continue
		}
		dmg := float32(1 + e.PuffState)
		dealt, _, _ := s.damageMob(&o, dmg, "mob_attack", nil, &damageInfo{
			typeName: "mob_attack",
			cause:    e.ID,
			direct:   e.ID,
		})
		if dealt > 0 {
			if pid, ok := MobEffectID("poison"); ok {
				s.mobGiveEffect(&o, int32(pid), 0, 60*e.PuffState, false)
			}
			if id, ok := SoundEventID("entity.puffer_fish.sting"); ok {
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, mobVoiceOf(e).src(), 1.0, 1.0, nil)
			}
		}
	}
}

// puffPlayerTouch is Pufferfish.playerTouch, polled per tick over the
// session set the way the cube-touch arm is: a puffed fish that a
// damageable player overlaps stings them for 1+state, fires
// ClientboundGameEventPacket.PUFFER_FISH_STING (event 9) at the
// touched player, and applies POISON for 60*state ticks. All three
// effects are gated on hurtServer landing — a creative spectator's
// brush does nothing.
func (s *Server) puffPlayerTouch(e *Entity) {
	b := entityBBDims(e)
	scale := puffDimScale[0]
	if e.PuffState >= 0 && e.PuffState <= 2 {
		scale = puffDimScale[e.PuffState]
	}
	halfW := b[0] * scale / 2
	top := e.Y + b[1]*scale
	dmg := float32(1 + e.PuffState)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead ||
			sess.dimName() != e.Dim {
			return true
		}
		if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if math.Abs(px-e.X) >= halfW+0.3 || math.Abs(pz-e.Z) >= halfW+0.3 ||
			py+1.8 <= e.Y || py >= top {
			return true
		}
		dealt := s.damageWith(sess, dmg, translatable("death.attack.mob",
			literal(sess.getName()), literal(mobDisplayName("pufferfish"))), damageSource{
			typeName: "mob_attack",
			cause:    e.ID,
			direct:   e.ID,
		})
		if dealt > 0 {
			s.sendGameEvent(sess, 9, 0)
			if pid, ok := MobEffectID("poison"); ok {
				s.applyMobEffect(sess, int32(pid), 0, 60*e.PuffState)
			}
		}
		return true
	})
}

// tamableSitPoseStep is SitWhenOrderedToGoal (priority 2 on
// wolf/cat/parrot): the idx18 0x01 bit is inSittingPose — applied by
// the goal, never by setOrderedToSit itself (a plain field). canUse
// refuses isInWater / !onGround starts; canContinueToUse is plain
// orderedToSit — once sitting, only the field flipping drops the pose.
func (s *Server) tamableSitPoseStep(e *Entity) {
	if e.Sitting && !e.SittingPose {
		if !e.Grounded {
			return
		}
		if w := s.dimWorld(e.Dim); w != nil &&
			cellFluidIsWater(w.GetBlock(world.Pos{
				X: int(math.Floor(e.X)),
				Y: int(math.Floor(e.Y)),
				Z: int(math.Floor(e.Z)),
			})) {
			return
		}
		e.SittingPose = true
		e.MetaDirty = true
	} else if !e.Sitting && e.SittingPose {
		e.SittingPose = false
		e.MetaDirty = true
	}
}

// fishSwimStep is FishSwimGoal: RandomSwimmingGoal.canUse rolls
// nextInt(reducedTickDelay(40)) then getRandomSwimmablePos retries
// DefaultRandomPos up to 10 times for a cell that isPathfindable by
// the WATER computation — in mc-go terms, a water-fluid cell. A fish
// out of water can still draw (a beached fish slides toward nearby
// water — vanilla's flop); a fish mid-stroll whose path enters a
// solid block gives up, but open air under the path does not park it
// since the box is suspended in fluid anyway.
func (s *Server) fishSwimStep(e *Entity, w *world.World, name string, inWater bool, wDepth float64) {
	if !e.Strolling {
		interval := 40 + int64((uint32(e.ID)*2654435761>>16)%21)
		if e.WanderTicks%interval != 0 {
			s.mobTravelStep(e, w, 0)
			return
		}
		for i := int64(0); i < 10; i++ {
			tx := e.X + strollOffset(e.ID, e.WanderTicks+i*3)
			tz := e.Z + strollOffset(e.ID, e.WanderTicks+i*3+7)
			ty := e.Y + strollOffset(e.ID, e.WanderTicks+i*3+13)*0.7
			p := world.Pos{
				X: int(math.Floor(tx)),
				Y: int(math.Floor(ty)),
				Z: int(math.Floor(tz)),
			}
			if cellFluidIsWater(w.GetBlock(p)) {
				e.Strolling = true
				e.StrollSteps = 0
				e.StrollTX, e.StrollTZ = tx, tz
				break
			}
		}
		if !e.Strolling {
			s.mobTravelStep(e, w, 0)
			return
		}
	}
	e.StrollSteps++
	tx, tz := e.StrollTX-e.X, e.StrollTZ-e.Z
	dist := math.Hypot(tx, tz)
	if dist < 0.4 || e.StrollSteps > 160 {
		e.Strolling = false
		e.WanderTicks = 0
		s.mobTravelStep(e, w, 0)
		return
	}
	speed, ok := entityAttrBases[name]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	step := speed
	if step > dist {
		step = dist
	}
	mobTurnToward(e, tx, tz, 90)
	e.LookYaw = e.Yaw
	nx := e.X + tx/dist*step
	nz := e.Z + tz/dist*step
	nextSpace := world.Pos{
		X: int(math.Floor(nx)),
		Y: int(math.Floor(e.Y)),
		Z: int(math.Floor(nz)),
	}
	if nb := w.GetBlock(nextSpace); nb != world.Air && !moveNoCollision(nb) {
		e.Strolling = false
		e.WanderTicks = 0
		if inWater && wDepth > 0 {
			s.mobTravelStep(e, w, speed)
		} else {
			s.mobTravelStep(e, w, 0)
		}
		return
	}
	s.mobTravelStep(e, w, speed)
}

// equineFleeStep is RunAroundLikeCrazyGoal's navigation: canUse picks
// DefaultRandomPos.getPos(mob, 5, 4) — a ±5 horizontal target — and
// start() runs moveTo(pos, speedModifier 1.2). canContinueToUse keeps
// it until arrival or the 160-step give-up, then canUse re-arms a new
// target the next tick — an untamed equine with a rider never stops
// fleeing.
func (s *Server) equineFleeStep(e *Entity, w *world.World, name string) {
	if e.FleeSteps == 0 {
		e.FleeTX = e.X + strollOffset(e.ID, e.WanderTicks)/2
		e.FleeTZ = e.Z + strollOffset(e.ID, e.WanderTicks+13)/2
	}
	e.FleeSteps++
	tx, tz := e.FleeTX-e.X, e.FleeTZ-e.Z
	dist := math.Hypot(tx, tz)
	if dist < 0.4 || e.FleeSteps > 160 {
		e.FleeSteps = 0
		s.mobTravelStep(e, w, 0)
		return
	}
	speed, ok := entityAttrBases[name]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	speed *= 1.2 // the goal's speedModifier
	step := speed
	if step > dist {
		step = dist
	}
	mobTurnToward(e, tx, tz, 90)
	e.LookYaw = e.Yaw
	nx := e.X + tx/dist*step
	nz := e.Z + tz/dist*step
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
	spaceBlocked := false
	if nb := w.GetBlock(nextSpace); nb != world.Air && !moveNoCollision(nb) {
		spaceBlocked = true
	}
	if w.GetBlock(nextBelow) == world.Air || spaceBlocked {
		// Ledge or obstacle — navigation gives up; canUse re-picks.
		e.FleeSteps = 0
		s.mobTravelStep(e, w, 0)
		return
	}
	s.mobTravelStep(e, w, speed)
}

// strollOffset is the DefaultRandomPos target component: a ±10 offset
// derived deterministically from the entity id and wander counter.
func strollOffset(eid int32, ticks int64) float64 {
	h := uint64(uint32(eid))*2654435761 ^ uint64(ticks)*6364136223846793005
	return float64(h%200)/10 - 10
}

// mobLookStep is RandomLookAroundGoal + LookAtPlayerGoal: each look
// period a fresh head target is picked — the nearest player within
// 6 blocks (vanilla's look distance) or a ±90° random offset — and
// LookControl ticks the head toward it a few degrees a tick, so a
// turn lands as several rotate_head emits like vanilla's lerp.
func (s *Server) mobLookStep(e *Entity) {
	e.WanderTicks2++
	if e.WanderTicks2%lookPeriodFor(e.ID) == 0 {
		if px, pz, ok := s.nearestPlayer(e.Dim, e.X, e.Z, 6); ok {
			e.LookYaw = float32(math.Atan2(-(px-e.X), pz-e.Z) * 180 / math.Pi)
		} else {
			h := uint64(uint32(e.ID))*2654435761 ^ uint64(e.WanderTicks2)*2862933555777941757
			e.LookYaw = e.Yaw + float32(int64(h%180)-90)
		}
	}
	diff := float64(e.LookYaw - e.HeadYaw)
	for diff > 180 {
		diff -= 360
	}
	for diff < -180 {
		diff += 360
	}
	if math.Abs(diff) < 0.5 {
		return
	}
	step := math.Max(-10, math.Min(10, diff))
	e.HeadYaw += float32(step)
}

// lookPeriodFor is the per-entity head-look cadence — vanilla's
// RandomLookAroundGoal re-rolls every 20-40 ticks.
func lookPeriodFor(eid int32) int64 {
	return 20 + int64((uint32(eid)*40503>>16)%20)
}

// nearestPlayer returns the closest play-state session's x/z within
// maxDist of (x,z), or false when none is in range.
func (s *Server) nearestPlayer(dim string, x, z, maxDist float64) (float64, float64, bool) {
	var bx, bz float64
	found := false
	best := maxDist * maxDist
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		px, _, pz, _, _, _ := other.Pos()
		dx, dz := px-x, pz-z
		if d := dx*dx + dz*dz; d < best {
			bx, bz, best, found = px, pz, d, true
		}
		return true
	})
	return bx, bz, found
}

// mobAlwaysFlies is the airborne set: these types never take
// gravity or wander the ground (the bee's Brain flies it — vanilla
// gives flying mobs no fall). mobGrounded answers grounded for them.
var mobAlwaysFlies = map[string]bool{
	"bee": true,
}

// mobSlowFalls is Chicken.aiStep's wing-flap glide: while airborne
// and descending, the stored delta is damped by 0.6 per tick, which
// settles the fall near -0.12 blocks/tick instead of the -1.0 cap.
var mobSlowFalls = map[string]bool{
	"chicken": true,
	// Blaze.aiStep's identical !onGround && y<0 → y·0.6 arm.
	"blaze": true,
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
	if kn, _ := EntityTypeName(e.Type); mobAlwaysFlies[kn] {
		e.Grounded = false
		return true
	}
	// The supporting cell is vanilla's getOnPos(0.500001) — half a block
	// under the feet — so a mob sunk 1/16 into honey still counts its
	// honey cell as the support instead of the air below it.
	below := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y - 0.500001)),
		Z: int(math.Floor(e.Z)),
	}
	if w.GetBlock(below) == world.Air {
		e.Grounded = false
		prevY := e.Y
		e.MotionY -= 0.08
		if e.MotionY < -1.0 {
			e.MotionY = -1.0
		}
		// Chicken.aiStep's wing-flap glide damps the stored descent
		// delta by 0.6 AFTER the air-travel tail's 0.98 drag — the next
		// descent step is the post-gravity delta scaled by 0.588, so
		// the glide settles near -0.114 blocks/tick.
		if e.MotionY < 0 {
			if kn, _ := EntityTypeName(e.Type); mobSlowFalls[kn] {
				e.MotionY *= 0.588
			}
		}
		// Entity.move's collide: sweep the foot face against the cells
		// it crosses — an unchecked += lets a fast fall tunnel through
		// the floor's collision top and ground the mob a full block
		// deep inside the floor (the half-below support check then sees
		// the NEXT solid cell down). clampBoxAxis stops the feet at the
		// highest collision top in the span, exactly like vanilla.
		bb := mobBoundsTable[e.Type]
		half, height := bb[0]/2, bb[1]
		cy := s.clampBoxAxis(w, [2]float64{e.Y, e.Y + height}, e.MotionY,
			1, e.X-half, e.X+half, e.Z-half, e.Z+half)
		e.Y += cy
		// Entity.updateFallDistance: downward travel accumulates.
		if dy := prevY - e.Y; dy > 0 {
			e.FallDist += dy
		}
		return false
	}
	// Landing edge. Vanilla's fallDistance counts the whole drop —
	// the residual down to the support collision top is part of the last
	// tick's travel. Water
	// contact zeroes it instead (vanilla resets per tick in fluid).
	if !e.Grounded && e.FallDist > 0 {
		if _, _, fluid := fluidOf(w.GetBlock(below)); !fluid {
			// Account only for descent remaining above the support shape.
			// Honey/soul-sand tops are fractional; floor(feet) would add
			// almost another block after collision already stopped the fall.
			if top, ok := collisionTopAt(w, below); ok {
				if remaining := e.Y - (float64(below.Y) + top); remaining > 0 {
					e.FallDist += remaining
				}
			}
			s.mobFallDamage(e, s.dims[e.Dim], below)
		}
		e.FallDist = 0
	}
	// Y and MotionY belong to the travel chain now: entityMove leaves
	// the feet on the block's collision top (honey −59.0625, soul_sand
	// −59.125) and the stored gravity pull negative — snapping to the
	// cell floor or zeroing the delta would fight the next tick's move.
	// applyEffectsFromBlocks' stepOn arm: the support block's stepOn
	// runs every grounded tick on the getOnPosLegacy cell (0.2 below
	// the feet). TurtleEggBlock.stepOn rolls nextInt(100) == 0 —
	// mobs never step carefully, so the roll always runs.
	onPos := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y - 0.2)), Z: int(math.Floor(e.Z))}
	if onName, _ := blockNameAt(s.dims[e.Dim], onPos); onName == "turtle_egg" {
		s.turtleEggStepOn(s.dims[e.Dim], onPos, e)
	}
	e.Grounded = true
	return true
}

// mobIsZombieFamily covers TurtleEggBlock.fallOn's
// `entity instanceof Zombie` exemption: the whole Zombie class
// tree — zombie, husk, drowned, zombie_villager, zombified_piglin.
var mobIsZombieFamily = map[string]bool{
	"zombie": true, "husk": true, "drowned": true,
	"zombie_villager": true, "zombified_piglin": true,
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
	landedName, _ := blockNameAt(dm, below)
	// checkFallDamage dispatches state.fallOn BEFORE causeFallDamage —
	// a fall-immune mob (iron golem, 5.29 volume) still runs
	// FarmlandBlock.fallOn's trample roll even though its damage tail
	// returns early.
	if landedName == "farmland" {
		bb := mobBoundsTable[e.Type]
		s.farmlandTrample(dm, below, e.X, e.Y, e.Z, false,
			bb[0]*bb[0]*bb[1], e.FallDist)
	}
	// TurtleEggBlock.fallOn: !(entity instanceof Zombie) ->
	// destroyEgg(chance=3) — the Zombie class family (zombie, husk,
	// drowned, zombie_villager, zombified_piglin) skips the landing
	// roll; like farmland it rides inside checkFallDamage before the
	// immunity early-return, so a fall-immune mob still rolls.
	if landedName == "turtle_egg" && !mobIsZombieFamily[name] {
		s.turtleEggDestroy(dm, below, e, nil, 3)
	}
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
	base := 3.0
	if value, ok := entityAttrBases[name]["safe_fall_distance"]; ok {
		base = value
	}
	if value, ok := e.AttrBase["safe_fall_distance"]; ok {
		base = value
	}
	mods := map[string]attrMod{}
	for id, modifier := range e.AttrMods["safe_fall_distance"] {
		mods[id] = modifier
	}
	if id, ok := MobEffectID("jump_boost"); ok {
		if effect, active := e.Effects[id]; active {
			mods["minecraft:effect.jump_boost"] = attrMod{value: float64(effect.amplifier + 1)}
		}
	}
	safe := attrRange("safe_fall_distance", attrValue(base, mods))
	mult := float64(float32(dmgMult)) * attrRange("fall_damage_multiplier", s.mobAttr(e, name, "fall_damage_multiplier", 1))
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
	// Entity.playSound → the positional clientbound sound packet.
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
	"bee":              true,
	"enderman":         true,
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
		"bee", "enderman",
	} {
		if id, ok := EntityTypeID(name); ok {
			mobMelee[id] = true
		}
	}
}

// mobMeleeDamage resolves attack_damage on the mob instance — the
// supplier base plus any finalizeSpawn/NBT override (wither_skeleton
// re-bases 2 → 4 in finalizeSpawn) — with the iron golem's
// doHurtTarget override: vanilla rolls
// attackDamage/2 + random.nextInt(attackDamage) per swing — 7.5..22.5
// at the 15 base, not a flat 15.
func (s *Server) mobMeleeDamage(e *Entity, name string) float32 {
	base := float32(s.mobAttr(e, name, "attack_damage", 0))
	if name == "iron_golem" {
		return base/2 + float32(rand.Intn(int(base)))
	}
	return base
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
	best := s.mobAttr(e, kn, "follow_range", 35.0)
	best *= best
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
			// The enderman's stare-latched target counts as anger —
			// LookForPlayerGoal handed it to setTarget.
			angry := (e.AngryAt == sess.uuid &&
				(e.AngryUntil == 0 || s.gameTime() < e.AngryUntil)) ||
				e.EnderStareTarget == sess.uuid
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
	dmg := s.mobMeleeDamage(e, kn)
	target := s.nearestMobTarget(e, kn)
	if target == nil {
		// Goal stop — the aggressive flag and the arm-raise counter
		// clear with the run.
		if e.Aggressive {
			e.Aggressive = false
			e.MetaDirty = true
		}
		e.MobArmTicks = 0
		return false
	}
	px, py, pz, _, _, _ := target.Pos()
	dx, dz := px-e.X, pz-e.Z
	dist := math.Hypot(dx, dz)
	mobTurnToward(e, dx, dz, 30)
	e.HeadYaw = e.Yaw
	// isWithinMeleeAttackRange: the mob's box inflated by
	// DEFAULT_ATTACK_REACH (sqrt(2.04)-0.6 ~= 0.828) intersects the
	// target's hitbox — edge-to-edge on XZ, full extents on Y.
	dims := entityBBDims(e)
	reach := dims[0]/2 + 0.8284271 + 0.3
	if kn == "bee" {
		// Bee Brain's attack move: fly straight at the target — a
		// full-3D approach at ~flying_speed pace (attribute 0.6).
		dy := py - e.Y
		d3 := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if d3 > reach {
			step := 0.5
			if step > d3-reach*0.8 {
				step = d3 - reach*0.8
			}
			if step > 0 {
				e.X += dx / d3 * step
				e.Y += dy / d3 * step
				e.Z += dz / d3 * step
			}
		}
	} else if dist > reach {
		// The navigation speed is the goal's speedModifier ctor arg
		// times movement_speed — MeleeAttackGoal carries 1.0 for the
		// zombie family (0.23) and 1.2 for the skeleton family
		// (0.25 × 1.2 = 0.3).
		chase := mobChaseSpeed
		if mod, ok := mobMeleeSpeedMod[kn]; ok {
			chase = s.mobAttr(e, kn, "movement_speed", mobChaseSpeed) * mod
		}
		s.mobTravelStep(e, s.dimWorld(e.Dim), chase)
	}
	if e.attackCooldown > 0 {
		e.attackCooldown--
	}
	if e.attackCooldown <= 0 && dist <= reach && py+1.8 > e.Y && py < e.Y+dims[1] {
		e.attackCooldown = 20
		s.broadcastEntityAnimation(nil, e.ID, 0) // swing main hand
		if kn == "iron_golem" {
			// IronGolem.doHurtTarget plays the attack whoosh on every
			// swing — landed or not.
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 897, srcNeutral, 1, 1, nil)
		}
		// Bee.doHurtTarget swings on the sting damage source
		// (damageSources().sting → death.attack.sting), not
		// mob_attack.
		dmgType, deathKey := "mob_attack", "death.attack.mob"
		if kn == "bee" {
			dmgType, deathKey = "sting", "death.attack.sting"
		}
		dealt := s.damageWith(target, dmg, translatable(deathKey,
			literal(target.getName()), literal(mobDisplayName(kn))), damageSource{
			typeName: dmgType,
			cause:    e.ID,
			direct:   e.ID,
		})
		if dealt > 0 {
			s.mobPostAttack(e, kn, target)
		}
	}
	// MeleeAttackGoal.start latches the DATA_MOB_FLAGS aggressive bit
	// for every melee runner except the Brain-driven bee, and
	// ZombieAttackGoal flips it on 5 ticks into the run only while the
	// cooldown sits under half the interval — it reads the post-attack
	// cooldown, so the flag is dark on the swing tick itself.
	aggr := kn != "bee"
	if zombieAttackTypes[kn] {
		e.MobArmTicks++
		aggr = e.MobArmTicks >= 5 && e.attackCooldown < 10
	}
	if aggr != e.Aggressive {
		e.Aggressive = aggr
		e.MetaDirty = true
	}
	return true
}

// mobMeleeSpeedMod is MeleeAttackGoal's speedModifier ctor arg for
// the measured types — the chase speed is modifier × movement_speed;
// types absent here chase at the flat zombie-family 0.23.
var mobMeleeSpeedMod = map[string]float64{
	"wither_skeleton": 1.2,
}

// zombieAttackTypes run ZombieAttackGoal — the goal that pulses the
// aggressive flag by raiseArmTicks and the attack cooldown.
var zombieAttackTypes = map[string]bool{
	"zombie": true, "husk": true, "drowned": true,
	"zombie_villager": true, "zombified_piglin": true,
}

// mobRanged is the AbstractSkeleton family: with a bow in mainhand
// they run RangedBowAttackGoal, bare-handed they run MeleeAttackGoal
// (reassessWeaponGoal swaps the two on equipment change).
var mobRanged = map[string]bool{
	"skeleton":        true,
	"stray":           true,
	"bogged":          true,
	"wither_skeleton": true,
}

// mobPanicSpeed is PanicGoal's speedModifier per mob type, read off
// the 26.2 jar's registerGoals constructors: farm animals 1.25,
// chicken 1.4, cow/mooshroom 2.0, equines and turtle 1.2, cat 1.5,
// wolf 1.5, parrot 1.25, rabbit and fox 2.2, panda 2.0.
var mobPanicSpeed = map[string]float64{
	"pig": 1.25, "sheep": 1.25, "chicken": 1.4,
	"cow": 2.0, "mooshroom": 2.0, "panda": 2.0,
	"horse": 1.2, "donkey": 1.2, "mule": 1.2, "llama": 1.2,
	"trader_llama": 2.0, "skeleton_horse": 1.2, "zombie_horse": 1.2,
	"turtle": 1.2,
	"cat":    1.5, "wolf": 1.5,
	"parrot": 1.25,
	"rabbit": 2.2, "fox": 2.2,
}

// panicCauseTypes is the PANIC_CAUSES damage-type tag (26.2 jar):
// environmental causes (fire/lava/cactus/freeze/hot floor), projectile
// and magic types, mob attacks, and player attacks. generic, fall,
// cramming and starvation do NOT panic.
var panicCauseTypes = map[string]bool{
	"cactus": true, "freeze": true, "hot_floor": true,
	"sulfur_cube_hot": true, "in_fire": true, "lava": true,
	"lightning_bolt": true, "on_fire": true,
	"arrow": true, "dragon_breath": true, "explosion": true,
	"fireball": true, "fireworks": true, "indirect_magic": true,
	"magic": true, "mob_attack": true, "mob_projectile": true,
	"player_explosion": true, "sonic_boom": true, "sting": true,
	"thrown": true, "trident": true, "unattributed_fireball": true,
	"wind_charge": true, "wither": true, "wither_skull": true,
	"player_attack": true,
}

// mobFlees is the measured AvoidEntityGoal table — feared class
// ("player" and "monster" are predicates, the rest entity types),
// trigger distance, and the walk/sprint speed modifiers. Verified on
// the 26.2 jar: creeper→ocelot+cat 6.0 @ 1.0/1.2, skeletons→wolf
// 6.0 @ 1.0/1.2, rabbit→player 8 / wolf 10 / monster 4 @ 2.2/2.2,
// fox→player 16 / wolf+polar_bear 8 @ 1.6/1.4 — fox walks FASTER
// than it sprints. Fox predicates: a discrete (sneaking) player is
// exempt from the player rule, and a tame wolf from the wolf rule.
type fleeRule struct {
	feared      string
	dist        float64
	walk        float64
	sprint      float64
	sneakExempt bool
	untamedOnly bool
}

var mobFlees = map[string][]fleeRule{
	"skeleton": {{"wolf", 6.0, 1.0, 1.2, false, false}},
	"stray":    {{"wolf", 6.0, 1.0, 1.2, false, false}},
	"bogged":   {{"wolf", 6.0, 1.0, 1.2, false, false}},
	"creeper":  {{"ocelot", 6.0, 1.0, 1.2, false, false}, {"cat", 6.0, 1.0, 1.2, false, false}},
	"rabbit":   {{"player", 8.0, 2.2, 2.2, false, false}, {"wolf", 10.0, 2.2, 2.2, false, false}, {"monster", 4.0, 2.2, 2.2, false, false}},
	"fox":      {{"player", 16.0, 1.6, 1.4, true, false}, {"wolf", 8.0, 1.6, 1.4, false, true}, {"polar_bear", 8.0, 1.6, 1.4, false, false}},
	"evoker":   {{"player", 8.0, 0.6, 1.0, false, false}},
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

// panicMalusCells are the getPathTypeStatic results whose
// pathfindingMalus is non-zero for a walking mob — water and lava
// cells, fire, powder snow and the damage blocks (cactus, sweet
// berries, magma-adjacent). Panic target candidates landing on one
// are rejected like vanilla's hasMalus check.
var panicMalusCells = map[string]bool{
	"water": true, "lava": true, "fire": true, "soul_fire": true,
	"powder_snow": true, "cactus": true, "sweet_berry_bush": true,
	"magma_block": true, "campfire": true, "soul_campfire": true,
}

// panicRollTarget is PanicGoal.canUse's target pick. A burning mob
// takes the closest water cell within ±5/±1 first
// (PanicGoal.lookForWater); everyone else runs
// DefaultRandomPos.getPos(mob, 5, 4): ten attempts of a uniform
// random cell offset (x/z ∈ [-5,5], y ∈ [-4,4]), each candidate kept
// when the cell is passable and malus-free (getPathTypeStatic) and
// the block below is solid-render (isStableDestination). All
// candidates tie on getWalkTargetValue for the panicking set, so the
// first accepted attempt wins.
func (s *Server) panicRollTarget(e *Entity) {
	w := s.dimWorld(e.Dim)
	// CamelPanic.start's first statement is standUpInstantly — every
	// panic activation (the hurt arm's roll and each in-window
	// re-fire alike) restamps the camel's tick to t-53 silently, so
	// a hurt sitting camel emits a second positive idx20 behind
	// actuallyHurt's own instant stand.
	if n, _ := EntityTypeName(e.Type); n == "camel" || n == "camel_husk" {
		s.camelStand(e, true)
	}
	bx, by, bz := int(math.Floor(e.X)), int(math.Floor(e.Y)), int(math.Floor(e.Z))
	cell := func(x, y, z int) (world.Block, string) {
		b := w.GetBlock(world.Pos{X: x, Y: y, Z: z})
		n, _ := world.StateOwner(b)
		return b, n
	}
	e.PanicActive = false
	// PanicGoal.canUse's water branch: isOnFire and the mob's own cell
	// is collision-free — scan for the closest water fluid cell.
	if e.FireTicks > 0 {
		best := -1
		var wx, wy, wz int
		bd := float64(1e18)
		for dx := -5; dx <= 5 && best == -1; dx++ {
			for dz := -5; dz <= 5; dz++ {
				for dy := -1; dy <= 1; dy++ {
					cx, cy, cz := bx+dx, by+dy, bz+dz
					b, _ := cell(cx, cy, cz)
					if k, _, ok := fluidOf(b); !ok || k != fluidWater {
						continue
					}
					d := float64(dx*dx + dz*dz + dy*dy)
					if d < bd {
						bd, wx, wy, wz = d, cx, cy, cz
						best = 0
					}
				}
			}
		}
		if best == 0 {
			e.PanicTX, e.PanicTZ = float64(wx)+0.5, float64(wz)+0.5
			_ = wy
			e.PanicActive = true
			e.PanicSteps, e.PanicStill = 0, 0
			e.PanicBest = math.Hypot(e.PanicTX-e.X, e.PanicTZ-e.Z)
			return
		}
	}
	now := s.gameTime()
	for i := 0; i < 10; i++ {
		h := uint64(uint32(e.ID))*2654435761 ^ uint64(now)*1099511628211 ^
			uint64(i)*40503 ^ uint64(e.PanicSteps)*97531
		ox := int(h%11) - 5
		h >>= 8
		oy := int(h%9) - 4
		h >>= 6
		oz := int(h%11) - 5
		cx, cy, cz := bx+ox, by+oy, bz+oz
		if cy < -64 || cy >= 320 {
			continue // isOutsideLimits
		}
		b, n := cell(cx, cy, cz)
		if !world.NonSolid(b) || panicMalusCells[n] {
			continue // BLOCKED or malus path type
		}
		if below, bn := cell(cx, cy-1, cz); world.NonSolid(below) || panicMalusCells[bn] {
			continue // !isStableDestination — needs solid-render below
		}
		e.PanicTX, e.PanicTZ = float64(cx)+0.5, float64(cz)+0.5
		e.PanicActive = true
		e.PanicSteps, e.PanicStill = 0, 0
		e.PanicBest = math.Hypot(e.PanicTX-e.X, e.PanicTZ-e.Z)
		return
	}
	// All ten attempts rejected: the goal can't start this tick —
	// vanilla's canUse just returns false and re-rolls next tick.
}

// mobPanicStep is PanicGoal (goal priority 1): while armed it walks
// the mob to its DefaultRandomPos target at speedModifier ×
// movement_speed, mirroring the stroll's motion write so the tracked
// delta ships on the wire. The run ends on arrival (~0.5), ten ticks
// without closing progress (the exhausted-partial-path stand-in), or
// the 60-tick cap; while the 40-tick damage-source window is still
// live the goal immediately re-fires with a fresh random target,
// otherwise it stops for good.
func (s *Server) mobPanicStep(e *Entity, name string) bool {
	mod, ok := mobPanicSpeed[name]
	if !ok {
		e.PanicActive = false
		return false
	}
	if !e.PanicActive {
		// Between runs inside the window: re-roll now.
		if e.PanicUntil > s.gameTime() {
			s.panicRollTarget(e)
		}
		if !e.PanicActive {
			return false
		}
	}
	// TamableAnimalPanicGoal.tick's first half: while the panic run
	// is live, a tamed pet still teleports to its owner past 12
	// blocks (no recalc cadence — every tick).
	if followOwnerTypes[name] && e.ownerUUID != ([16]byte{}) &&
		!e.Sitting && e.Riding == 0 && e.LeashHolder == 0 {
		if p := s.sessionByUUID(e.ownerUUID); p != nil && p.spawned.Load() &&
			p.dimName() == e.Dim && p.mode() != GameModeSpectator {
			px, py, pz, _, _, _ := p.Pos()
			dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
			if dx*dx+dy*dy+dz*dz >= 144.0 {
				s.followOwnerTeleport(e, p, px, py, pz)
			}
		}
	}
	tx, tz := e.PanicTX-e.X, e.PanicTZ-e.Z
	dist := math.Hypot(tx, tz)
	e.PanicSteps++
	if dist < e.PanicBest-0.05 {
		e.PanicBest = dist
		e.PanicStill = 0
	} else {
		e.PanicStill++
	}
	if dist < 0.5 || e.PanicStill >= 10 || e.PanicSteps > 60 {
		if e.PanicUntil > s.gameTime() {
			s.panicRollTarget(e)
		} else {
			e.PanicActive = false
			e.PanicUntil = 0
		}
		if !e.PanicActive {
			s.mobTravelStep(e, s.dimWorld(e.Dim), 0)
		}
		return true
	}
	speed, ok := entityAttrBases[name]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	mobTurnToward(e, tx, tz, 90)
	e.LookYaw = e.Yaw
	// PanicGoal's drive: speedModifier·movement_speed on travel's input.
	s.mobTravelStep(e, s.dimWorld(e.Dim), speed*mod)
	return true
}

// mobTemptFood is TemptGoal's `items` predicate per animal type (the
// FOOD item tags in the 26.2 jar) plus its speedModifier, read off the
// registerGoals constructors: pig 1.2, sheep 1.1, cow/horse-family
// 1.25, chicken 1.0. Rabbits and cats pass canScare=true — their
// scurry-on-move variant is not modeled here.
var mobTemptFood = map[string]struct {
	speed float64
	items map[int32]bool
}{
	"pig":            {1.2, temptSet("carrot", "potato", "beetroot")},
	"sheep":          {1.1, temptSet("wheat")},
	"cow":            {1.25, temptSet("wheat")},
	"mooshroom":      {1.25, temptSet("wheat")},
	"chicken":        {1.0, temptSet("wheat_seeds", "melon_seeds", "pumpkin_seeds", "beetroot_seeds", "torchflower_seeds", "pitcher_pod")},
	"horse":          {1.25, temptSet("wheat", "sugar", "hay_block", "apple", "carrot", "golden_carrot", "golden_apple", "enchanted_golden_apple")},
	"donkey":         {1.25, temptSet("wheat", "sugar", "hay_block", "apple", "carrot", "golden_carrot", "golden_apple", "enchanted_golden_apple")},
	"mule":           {1.25, temptSet("wheat", "sugar", "hay_block", "apple", "carrot", "golden_carrot", "golden_apple", "enchanted_golden_apple")},
	"llama":          {1.25, temptSet("wheat", "hay_block")},
	"trader_llama":   {1.25, temptSet("wheat", "hay_block")},
	"skeleton_horse": {1.25, temptSet("wheat", "sugar", "hay_block", "apple", "carrot", "golden_carrot", "golden_apple", "enchanted_golden_apple")},
	"zombie_horse":   {1.25, temptSet("wheat", "sugar", "hay_block", "apple", "carrot", "golden_carrot", "golden_apple", "enchanted_golden_apple")},
}

// temptSet resolves the item-tag names to registry ids once.
func temptSet(names ...string) map[int32]bool {
	m := make(map[int32]bool, len(names))
	for _, n := range names {
		if id, ok := world.ItemID(n); ok {
			m[id] = true
		}
	}
	return m
}

// wolfBegItems is BegGoal's playerHoldingInteresting predicate:
// item.is(BONE) || wolf.isFood(stack) — the wolf_food tag plus
// bone on top.
var wolfBegItems = func() map[int32]bool {
	m := map[int32]bool{}
	for n := range wolfFoodNames() {
		if id, ok := world.ItemID(n); ok {
			m[id] = true
		}
	}
	if id, ok := world.ItemID("bone"); ok {
		m[id] = true
	}
	return m
}()

// wolfBegStep is BegGoal (goal priority 9, LOOK flag, constructed
// with lookDistance 8.0): while the nearest noncombat player
// within 8 blocks holds bone or wolf_food in either hand the wolf
// raises DATA_INTERESTED_ID and looks at them for
// adjustedTickDelay(40+rand(40)) ticks. canContinueToUse drops the
// interest when the player dies, leaves range, or swaps off the
// item — stop() clears the accessor.
func (s *Server) wolfBegStep(e *Entity) {
	if e.Interested {
		p := s.sessionByEntityID(e.BegTarget)
		if p == nil || e.BegLook <= 0 || !wolfHoldsBeg(p) || wolfBegDist2(e, p) > 64 {
			e.Interested = false
			e.BegTarget = 0
			e.MetaDirty = true
		} else {
			e.BegLook--
			px, _, pz, _, _, _ := p.Pos()
			e.LookYaw = float32(math.Atan2(-(px-e.X), pz-e.Z) * 180 / math.Pi)
			return
		}
	}
	var target *Session
	best := 64.0
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || !p.spawned.Load() || p.dead ||
			p.mode() == GameModeSpectator || p.dimName() != e.Dim {
			return true
		}
		if !wolfHoldsBeg(p) {
			return true
		}
		if d := wolfBegDist2(e, p); d < best {
			best = d
			target = p
		}
		return true
	})
	if target == nil {
		return
	}
	e.Interested = true
	e.BegLook = 40 + rand.Int31n(40)
	e.BegTarget = target.entityID.Load()
	e.MetaDirty = true
	px, _, pz, _, _, _ := target.Pos()
	e.LookYaw = float32(math.Atan2(-(px-e.X), pz-e.Z) * 180 / math.Pi)
}

// wolfHoldsBeg is BegGoal's playerHoldingInteresting: either hand
// carries bone or wolf_food.
func wolfHoldsBeg(p *Session) bool {
	return wolfBegItems[p.heldStack().ID] || wolfBegItems[p.stackAt(slotOffhand).ID]
}

func wolfBegDist2(e *Entity, p *Session) float64 {
	px, py, pz, _, _, _ := p.Pos()
	return (px-e.X)*(px-e.X) + (py-e.Y)*(py-e.Y) + (pz-e.Z)*(pz-e.Z)
}

// mobTemptStep is TemptGoal (goal priority 3-4, canScare=false):
// while a nearby player holds one of the mob's tempting items in
// either hand the mob navigates to the holder at speedModifier ×
// movement_speed and stands at 2.5 blocks (the default
// stopDistance), looking at them. Losing the item or the player
// leaving TEMPT_RANGE (the 10.0 attribute default) ends the goal and
// starts vanilla's reducedTickDelay(100) calm-down.
func (s *Server) mobTemptStep(e *Entity, name string) bool {
	spec, ok := mobTemptFood[name]
	if !ok {
		return false
	}
	if e.TemptCalm > 0 {
		e.TemptCalm--
		e.TemptActive = false
		return false
	}
	var target *Session
	best := 10.0 * 10.0 // TEMPT_RANGE default
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || !p.spawned.Load() || p.dead ||
			p.mode() == GameModeSpectator || p.dimName() != e.Dim {
			return true
		}
		if !spec.items[p.heldStack().ID] && !spec.items[p.stackAt(slotOffhand).ID] {
			return true
		}
		px, py, pz, _, _, _ := p.Pos()
		d := (px-e.X)*(px-e.X) + (py-e.Y)*(py-e.Y) + (pz-e.Z)*(pz-e.Z)
		if d < best {
			best = d
			target = p
		}
		return true
	})
	if target == nil {
		if e.TemptActive {
			e.TemptCalm = 100
		}
		e.TemptActive = false
		return false
	}
	e.TemptActive = true
	e.TemptTarget = target.entityID.Load()
	px, _, pz, _, _, _ := target.Pos()
	tx, tz := px-e.X, pz-e.Z
	dist := math.Hypot(tx, tz)
	mobTurnToward(e, tx, tz, 90)
	e.LookYaw = e.Yaw
	if dist <= 2.5 {
		// Inside stopDistance: navigation.stop() drops the drive, but
		// travel still integrates and decays the residual delta.
		s.mobTravelStep(e, s.dimWorld(e.Dim), 0)
		return true
	}
	speed, ok := entityAttrBases[name]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	// TemptGoal's speedModifier multiplies the attribute — the drive is
	// zza = spec.speed·movement_speed on travel's input, not a teleport.
	s.mobTravelStep(e, s.dimWorld(e.Dim), speed*spec.speed)
	return true
}

// mobFollowParentSpeed is FollowParentGoal's speedModifier per animal
// type, read off the 26.2 registerGoals constructors (pig/sheep/
// chicken 1.1, cow 1.25, equines 1.0). Rabbits have no
// FollowParentGoal.
var mobFollowParentSpeed = map[string]float64{
	"pig": 1.1, "sheep": 1.1, "chicken": 1.1,
	"cow": 1.25, "mooshroom": 1.25,
	"horse": 1.0, "donkey": 1.0, "mule": 1.0, "llama": 1.0,
	"trader_llama": 1.0, "skeleton_horse": 1.0, "zombie_horse": 1.0,
	"panda": 1.25, "turtle": 1.2, "cat": 1.0, "wolf": 1.1, "fox": 1.0,
}

// mobFollowParentStep is FollowParentGoal: a baby animal follows the
// nearest adult of the SAME type while the gap sits in the
// follow window — start when farther than 3 (9.0 dist²), keep while
// inside 16 (256 dist²), the adults scanned out of the mob's bounding
// box inflated (8,4,8). Vanilla repaths every ~10 ticks; the
// straight-line walk here emits the same approach.
func (s *Server) mobFollowParentStep(e *Entity, name string) bool {
	if !e.Baby {
		return false
	}
	mod, ok := mobFollowParentSpeed[name]
	if !ok {
		return false
	}
	var px, pz float64
	found := false
	best := math.MaxFloat64
	for _, o := range s.entities.All() {
		if o.ID == e.ID || !o.Living || o.Type != e.Type || o.Dim != e.Dim {
			continue
		}
		if o.Baby || o.Age < 0 {
			continue
		}
		if math.Abs(o.Y-e.Y) > 4.5 || math.Abs(o.X-e.X) > 8.5 || math.Abs(o.Z-e.Z) > 8.5 {
			continue
		}
		d := (o.X-e.X)*(o.X-e.X) + (o.Y-e.Y)*(o.Y-e.Y) + (o.Z-e.Z)*(o.Z-e.Z)
		if d < best {
			best = d
			px, pz = o.X, o.Z
			found = true
		}
	}
	if !found || best < 9.0 || best > 256.0 {
		return false
	}
	tx, tz := px-e.X, pz-e.Z
	speed, ok := entityAttrBases[name]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	mobTurnToward(e, tx, tz, 90)
	e.LookYaw = e.Yaw
	// FollowParentGoal's nav drive: speedModifier·movement_speed.
	s.mobTravelStep(e, s.dimWorld(e.Dim), speed*mod)
	return true
}

// mobBreedStep is BreedGoal (priority 2-3): an animal in love (fed
// within the last 600 ticks) walks to the nearest in-love mate of the
// same species inside its bbox inflated 8 on every axis —
// canMate filters babies, panicking partners and animals on the
// cooldown — looking at it the whole way. After ~60 ticks of approach
// the pair breeds once the gap drops under 3 (9.0 dist²); a miss
// resets the counter like the goal dropping and restarting.
func (s *Server) mobBreedStep(e *Entity, name string) bool {
	if e.loveTicks <= 0 || e.Baby || e.Age != 0 {
		e.BreedTime = 0
		return false
	}
	mates, equine := equineMates[name]
	if !equine {
		if _, breedable := animalFood[name]; !breedable {
			return false
		}
	} else if !equineCanParent(e) {
		e.BreedTime = 0
		return false
	}
	var mateID int32
	var mx, mz float64
	best := math.MaxFloat64
	for _, o := range s.entities.All() {
		if o.ID == e.ID || !o.Living || o.Dim != e.Dim {
			continue
		}
		if equine {
			// canMate: the type pair must mate (horse×donkey is
			// legal — the baby is a mule) and the partner must pass
			// canParent; in-love rides the same loveTicks gate.
			oname, ok := EntityTypeName(o.Type)
			if !ok || !mates[oname] || o.loveTicks <= 0 || !equineCanParent(&o) {
				continue
			}
		} else {
			if o.Type != e.Type {
				continue
			}
			if o.loveTicks <= 0 || o.PanicActive || o.Baby || o.Age != 0 {
				continue
			}
		}
		if math.Abs(o.X-e.X) > 8.5 || math.Abs(o.Y-e.Y) > 8.5 || math.Abs(o.Z-e.Z) > 8.5 {
			continue
		}
		d := (o.X-e.X)*(o.X-e.X) + (o.Y-e.Y)*(o.Y-e.Y) + (o.Z-e.Z)*(o.Z-e.Z)
		if d < best {
			best = d
			mateID = o.ID
			mx, mz = o.X, o.Z
		}
	}
	if mateID == 0 {
		e.BreedTime = 0
		return false
	}
	e.BreedTime++
	tx, tz := mx-e.X, mz-e.Z
	dist := math.Hypot(tx, tz)
	mobTurnToward(e, tx, tz, 90)
	e.LookYaw = e.Yaw
	if e.BreedTime >= 60 {
		e.BreedTime = 0
		if dist <= 3.0 {
			if mp, ok := s.entities.GetPtr(mateID); ok && mp.loveTicks > 0 {
				s.breedPair(e, mp, name)
			}
		}
		return true
	}
	speed, ok := entityAttrBases[name]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	// BreedGoal's nav drive: speedModifier 1.0 · movement_speed.
	s.mobTravelStep(e, s.dimWorld(e.Dim), speed)
	return true
}

// mobFleeStep is AvoidEntityGoal. canUse scans the feared classes for
// the nearest threat inside the rule's trigger distance, then rolls a
// random escape cell in the away half-space (getPosAway 16/7 — a
// straight-away vector is wrong); canUse fails for the tick when no
// candidate survives or the picked cell would end CLOSER to the
// threat. A latched run drives to its fixed cell until arrival or
// no-progress (canContinueToUse = !navigation.isDone), sprinting while
// the feared entity's live position stays inside 7 blocks
// (distanceToSqr < 49) and walking beyond.
func (s *Server) mobFleeStep(e *Entity, kn string) bool {
	rules, ok := mobFlees[kn]
	if !ok {
		return false
	}
	w := s.dimWorld(e.Dim)
	if w == nil {
		return false
	}
	speed := mobChaseSpeed
	if mv, ok := entityAttrBases[kn]["movement_speed"]; ok {
		speed = mv
	}
	if e.AvoidActive {
		e.AvoidSteps++
		dist := math.Hypot(e.AvoidTX-e.X, e.AvoidTZ-e.Z)
		if dist < e.AvoidBest-0.05 {
			e.AvoidBest = dist
			e.AvoidStill = 0
		} else {
			e.AvoidStill++
		}
		if dist < 0.5 || e.AvoidStill >= 10 || e.AvoidSteps > 160 {
			// navigation.isDone: the run ends; canUse may re-roll a
			// fresh escape target on the very next tick.
			e.AvoidActive = false
			s.mobTravelStep(e, w, 0)
			return true
		}
		mod := e.AvoidWalk
		var txp, typ, tzp float64
		threat := false
		if e.AvoidFearedID != 0 {
			if fp, ok := s.entities.GetPtr(e.AvoidFearedID); ok {
				txp, typ, tzp, threat = fp.X, fp.Y, fp.Z, true
			}
		} else if e.AvoidFearedP != nil {
			txp, typ, tzp, _, _, _ = e.AvoidFearedP.Pos()
			threat = true
		}
		if threat {
			dx, dy, dz := txp-e.X, typ-e.Y, tzp-e.Z
			if dx*dx+dy*dy+dz*dz < 49 {
				mod = e.AvoidSprint
			}
		}
		mobTurnToward(e, e.AvoidTX-e.X, e.AvoidTZ-e.Z, 90)
		e.HeadYaw = e.Yaw
		s.mobTravelStep(e, w, speed*mod)
		return true
	}
	var fx, fy, fz float64
	var fearedID int32
	var fearedP *Session
	var fleeWalk, fleeSprint float64
	best := -1.0
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
				// The fox's AVOID_PLAYERS predicate exempts a discrete
				// player — sneaking does not trigger the flee.
				if r.sneakExempt && p.sneaking {
					return true
				}
				if p.dimName() != e.Dim {
					return true
				}
				px, py, pz, _, _, _ := p.Pos()
				dd := (px-e.X)*(px-e.X) + (py-e.Y)*(py-e.Y) + (pz-e.Z)*(pz-e.Z)
				if dd < d2 && (best < 0 || dd < best) {
					best = dd
					fx, fy, fz = px, py, pz
					fearedID = 0
					fearedP = p
					fleeWalk, fleeSprint = r.walk, r.sprint
				}
				return true
			})
		default:
			fearedTID, fok := EntityTypeID(r.feared)
			for _, o := range s.entities.Live() {
				if o.Dim != e.Dim || o.ID == e.ID || o.Dying || o.Dormant {
					continue
				}
				if r.feared == "monster" {
					on, _ := EntityTypeName(o.Type)
					if !mobMonsterType[on] {
						continue
					}
				} else if !fok || o.Type != fearedTID {
					continue
				}
				// The fox's wolf rule only fears untamed wolves.
				if r.untamedOnly && o.tamed {
					continue
				}
				dd := (o.X-e.X)*(o.X-e.X) + (o.Y-e.Y)*(o.Y-e.Y) + (o.Z-e.Z)*(o.Z-e.Z)
				if dd < d2 && (best < 0 || dd < best) {
					best = dd
					fx, fy, fz = o.X, o.Y, o.Z
					fearedID = o.ID
					fearedP = nil
					fleeWalk, fleeSprint = r.walk, r.sprint
				}
			}
		}
	}
	if best < 0 {
		return false
	}
	// A failed roll (no surviving candidate, or the best lands closer
	// to the threat) fails canUse — the goal retries next tick.
	if !s.avoidRollTarget(e, w, fx, fy, fz) {
		return false
	}
	e.AvoidActive = true
	e.AvoidFearedID = fearedID
	e.AvoidFearedP = fearedP
	e.AvoidWalk, e.AvoidSprint = fleeWalk, fleeSprint
	// tick() upgrades moveTo's walk speed to sprint while the feared
	// entity stays inside 7 blocks — same check as the drive loop.
	mod := fleeWalk
	{
		dx, dy, dz := fx-e.X, fy-e.Y, fz-e.Z
		if dx*dx+dy*dy+dz*dz < 49 {
			mod = fleeSprint
		}
	}
	mobTurnToward(e, e.AvoidTX-e.X, e.AvoidTZ-e.Z, 90)
	e.HeadYaw = e.Yaw
	s.mobTravelStep(e, w, speed*mod)
	return true
}

// avoidRollTarget is DefaultRandomPos.getPosAway(mob, 16, 7,
// fearedPos): up to 10 attempts of
// generateRandomDirectionWithinRadians(0, 16, 7, 0, dirAway, π/2) — a
// random direction within ±90° of the direct away-vector, distance
// sqrt-biased toward far up to 16·√2 then square-clamped to ±16 —
// filtered by the same limits/restrict/stable/malus checks as
// PanicGoal's roll. WalkTargetValue ties for every candidate cell on
// flat ground, so the first accepted attempt wins. The surviving cell
// is then rejected wholesale when it lands closer to the threat than
// the mob currently is (AvoidEntityGoal.canUse's distanceToSqr
// comparison); a false return leaves the goal unarmed.
func (s *Server) avoidRollTarget(e *Entity, w *world.World, fx, fy, fz float64) bool {
	now := s.gameTime()
	dirX, dirZ := e.X-fx, e.Z-fz
	yRCenter := math.Atan2(dirZ, dirX) - math.Pi/2
	tx, ty, tz := 0, 0, 0
	accepted := false
	for i := 0; i < 10; i++ {
		h := uint64(uint32(e.ID))*2654435761 ^ uint64(now)*1099511628211 ^
			uint64(i)*40503 ^ uint64(e.AvoidSteps)*97531
		u1 := float64(h%100000) / 100000
		h >>= 8
		u2 := float64(h%100000) / 100000
		h >>= 8
		u3 := float64(h%100000) / 100000
		yRadians := yRCenter + (2*u1-1)*math.Pi/2
		dist := math.Sqrt(u2) * 16 * math.Sqrt2
		xt := -dist * math.Sin(yRadians)
		zt := dist * math.Cos(yRadians)
		if math.Abs(xt) > 16 || math.Abs(zt) > 16 {
			continue
		}
		yt := int(u3*15) - 7
		// containing(xt+mob.x, yt+mob.y, zt+mob.z) — the offset adds to
		// the float position before the floor.
		cx := int(math.Floor(xt + e.X))
		cy := int(math.Floor(float64(yt) + e.Y))
		cz := int(math.Floor(zt + e.Z))
		if cy < -64 || cy >= 320 {
			continue
		}
		b := w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz})
		name, _ := world.StateOwner(b)
		if !world.NonSolid(b) || panicMalusCells[name] {
			continue
		}
		below := w.GetBlock(world.Pos{X: cx, Y: cy - 1, Z: cz})
		bname, _ := world.StateOwner(below)
		if world.NonSolid(below) || panicMalusCells[bname] {
			continue
		}
		tx, ty, tz = cx, cy, cz
		accepted = true
		break
	}
	if !accepted {
		return false
	}
	// canUse's closer-than-now check: distanceToSqr is full 3D — the
	// y leg matters when the threat stands above/below the mob.
	px, py, pz := float64(tx)+0.5, float64(ty), float64(tz)+0.5
	ddNew := (px-fx)*(px-fx) + (py-fy)*(py-fy) + (pz-fz)*(pz-fz)
	ddNow := (e.X-fx)*(e.X-fx) + (e.Y-fy)*(e.Y-fy) + (e.Z-fz)*(e.Z-fz)
	if ddNew < ddNow {
		return false
	}
	e.AvoidTX = px
	e.AvoidTZ = pz
	e.AvoidSteps = 0
	e.AvoidStill = 0
	e.AvoidBest = math.Hypot(px-e.X, pz-e.Z)
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
		if e.Dim == dimOverworld && !e.ImmuneToZombification && !e.NoAI {
			e.TimeInOverworld++
			if e.TimeInOverworld > 300 {
				if !e.Silent {
					s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, conv.snd, 5, 1, 1, nil)
				}
				if ne := s.mobConvert(e, conv.dst, 0); ne != nil {
					if nid, ok := MobEffectID("nausea"); ok {
						mobAddEffect(ne, nid, 0, s.clock.count+200, s.clock.count)
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
			inSnow = strings.TrimPrefix(bn, "minecraft:") == "powder_snow"
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
	s.mobAttrModAddTransient(e, "movement_speed", "minecraft:drinking", -0.25, 0)
}

// witchApplyPotion applies the finished potion's contents the way
// PotionContents.forEachEffect does: the timed effects get their
// potion duration, instant_health applies immediately and heals
// four hearts' halves (4 damage points at amplifier 0).
func (s *Server) witchApplyPotion(e *Entity, potion string) {
	switch potion {
	case "water_breathing":
		if id, ok := MobEffectID("water_breathing"); ok {
			mobAddEffect(e, id, 0, s.clock.count+3600, s.clock.count)
		}
	case "fire_resistance":
		if id, ok := MobEffectID("fire_resistance"); ok {
			mobAddEffect(e, id, 0, s.clock.count+3600, s.clock.count)
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
		e.ensureMetaInit()
		e.metaSent.health = e.Health
	case "swiftness":
		if id, ok := MobEffectID("speed"); ok {
			mobAddEffect(e, id, 0, s.clock.count+3600, s.clock.count)
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
			// adjustedTickDelay: the heal goal isn't an every-tick goal,
			// so vanilla's start() cooldown is reducedTickDelay(200) =
			// positiveCeilDiv(200, 2) = 100, not 200.
			e.witchHealCd = 100
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
	mobTurnToward(e, dx, dz, 30)
	e.HeadYaw = e.Yaw
	const radius = 10.0
	if dist > radius {
		step := mobChaseSpeed
		if mv, ok := entityAttrBases["witch"]["movement_speed"]; ok {
			step = mv
		}
		// RangedAttackGoal's approach is nav MOVE_TO — travel input.
		s.mobTravelStep(e, s.dimWorld(e.Dim), step)
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
	mobTurnToward(e, dx, dz, 30)
	e.HeadYaw = e.Yaw
	const radius = 10.0
	if dist > radius {
		step := mobChaseSpeed
		if mv, ok := entityAttrBases["witch"]["movement_speed"]; ok {
			step = mv
		}
		s.mobTravelStep(e, s.dimWorld(e.Dim), step)
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
			bb := entityBBDims(rt)
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
// (0.45 under two blocks). Projectile.shoot jitters the normalized
// direction by triangle(0, 0.0172275*uncertainty) per axis — the
// witch's 8.0 scatter sometimes misses the raider entirely, which is
// why a heal splash actually reaches a raider vanilla would have
// peppered with off-target regen.
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
	// RandomSource.triangle(0, 0.0172275*8) per axis, added AFTER the
	// normalize like Projectile.shoot, then scaled by the velocity.
	const jitter = 0.0172275 * 8
	tri := func() float64 { return jitter * (rand.Float64() - rand.Float64()) }
	ux, uy, uz := dx/mag+tri(), aimY/mag+tri(), dz/mag+tri()
	vx, vy, vz := ux*vel, uy*vel, uz*vel
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
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 1779, mobVoiceOf(e).src(), 1,
			float32(0.8+rand.Float64()*0.4), nil)
	}
}

// sessMobHasEff is sessHasEffect by potion-name.
func sessMobHasEff(t *Session, name string) bool {
	id, ok := MobEffectID(name)
	return ok && sessHasEffect(t, id)
}

// mobEyeInWater is isEyeInFluid(FluidTags.WATER): the block at the
// entity's eye height is water. Eye height is the type's standing
// dimensions times 0.85 (Entity.getEyeHeight).
func (s *Server) mobEyeInWater(e *Entity, d *dimension) bool {
	if d == nil {
		return false
	}
	bounds := entityBBDims(e)
	eye := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y + bounds[1]*0.85)),
		Z: int(math.Floor(e.Z)),
	}
	k, _, ok := fluidOf(d.w.GetBlock(eye))
	return ok && k == fluidWater
}

// mobInWall mirrors Entity.isInWall: a 1e-6-tall plane centred on
// the eye position, width*0.8 across, over the cells it overlaps.
// A cell counts when it is non-air, isSuffocating (the opaque
// full-cube table), and its collision shape meets the plane — every
// member of the suffocating set collides as a full cube, so the
// shape clause is implied by the table.
func (s *Server) mobInWall(e *Entity, w *world.World) bool {
	if w == nil {
		return false
	}
	bounds := entityBBDims(e)
	half := bounds[0] * 0.4
	ey := int(math.Floor(e.Y + bounds[1]*0.85))
	for bx := int(math.Floor(e.X - half)); bx <= int(math.Floor(e.X+half)); bx++ {
		for bz := int(math.Floor(e.Z - half)); bz <= int(math.Floor(e.Z+half)); bz++ {
			b := w.GetBlock(world.Pos{X: bx, Y: ey, Z: bz})
			if b == world.Air {
				continue
			}
			if name, ok := world.StateOwner(b); ok && suffocatingBlocks[strings.TrimPrefix(name, "minecraft:")] {
				return true
			}
		}
	}
	return false
}

// mobHasAirEffect is vanilla's MobEffectUtil.hasWaterBreathing /
// shouldEffectsRefillAirsupply — both read the same three-effect set.
func mobHasAirEffect(s *Server, e *Entity) bool {
	return s.mobHasEff(e, "water_breathing") ||
		s.mobHasEff(e, "conduit_power") ||
		s.mobHasEff(e, "breath_of_the_nautilus")
}

// mobEyeInBubbleColumn is the baseTick drain exemption: an eye cell
// holding a bubble column refills air instead of draining it.
func (s *Server) mobEyeInBubbleColumn(e *Entity, d *dimension) bool {
	if d == nil {
		return false
	}
	bounds := entityBBDims(e)
	eye := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y + bounds[1]*0.85)),
		Z: int(math.Floor(e.Z)),
	}
	n, _ := world.StateOwner(d.w.GetBlock(eye))
	return n == "minecraft:bubble_column"
}

// mobHasEff is mobHasEffect by name.
func (s *Server) mobHasEff(e *Entity, name string) bool {
	id, ok := MobEffectID(name)
	return ok && mobHasEffect(s, e, id)
}

// mobAttrModAdd/Remove mirror AttributeInstance's modifier set on the
// entity's pack state; the wire resend rides the next attr sync.
// Adds land in the permanent set — spawn bonuses and /attribute
// writes; transient effects use mobAttrModAddTransient.
func (s *Server) mobAttrModAdd(e *Entity, attr, id string, value float64, op int) {
	e.markAttrDirty(attr)
	if e.AttrMods == nil {
		e.AttrMods = map[string]map[string]attrMod{}
	}
	if e.AttrMods[attr] == nil {
		e.AttrMods[attr] = map[string]attrMod{}
	}
	e.AttrMods[attr][id] = attrMod{value: value, op: op, permanent: true}
}

// mobAttrModAddTransient is AttributeInstance.addTransientModifier:
// the modifier affects the value and syncs to clients but is absent
// from the save pack.
func (s *Server) mobAttrModAddTransient(e *Entity, attr, id string, value float64, op int) {
	e.markAttrDirty(attr)
	if e.AttrMods == nil {
		e.AttrMods = map[string]map[string]attrMod{}
	}
	if e.AttrMods[attr] == nil {
		e.AttrMods[attr] = map[string]attrMod{}
	}
	e.AttrMods[attr][id] = attrMod{value: value, op: op}
}

func (s *Server) mobAttrModRemove(e *Entity, attr, id string) {
	if e.AttrMods == nil {
		return
	}
	mods := e.AttrMods[attr]
	if _, ok := mods[id]; !ok {
		return
	}
	delete(mods, id)
	e.markAttrDirty(attr)
}

// frostPass is LivingEntity.baseTick's removeFrost+tryAddFrost pair:
// the powder_snow MOVEMENT_SPEED modifier drops unconditionally, then
// re-adds as -0.05*getPercentFrozen ADD_VALUE while the entity is
// frozen and its on-block is non-air.
func (s *Server) frostPass(e *Entity, w *world.World) {
	s.mobAttrModRemove(e, "movement_speed", "minecraft:powder_snow")
	if e.TicksFrozen <= 0 || w == nil {
		return
	}
	on := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y - 0.2)), Z: int(math.Floor(e.Z))}
	bn, ok := world.StateOwner(w.GetBlock(on))
	if !ok || bn == "minecraft:air" || bn == "minecraft:cave_air" || bn == "minecraft:void_air" {
		return
	}
	pct := math.Min(float64(e.TicksFrozen), 140) / 140
	s.mobAttrModAddTransient(e, "movement_speed", "minecraft:powder_snow", -0.05*pct, 0)
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
	// ConversionType.convert (vanilla bytecode): the new entity
	// spawns fresh — full type health, fresh UUID, attributes and
	// absorption reset — then convert() copies over the measured
	// carry list. The finalizeSpawn attribute rolls (spawn_
	// reinforcements, random_spawn_bonus) run BEFORE the carries so
	// the copied flags win — vanilla keeps a leftHanded piglin
	// left-handed through zombification while re-rolling the zombie
	// family's spawn attributes. Health is NOT carried (piglin at
	// 9hp converts to a full-20hp zombified piglin).
	ne := &Entity{
		Type: nt,
		Dim:  e.Dim,
		X:    e.X, Y: e.Y, Z: e.Z,
		Yaw: e.Yaw, Pitch: e.Pitch, HeadYaw: e.HeadYaw,
		MotionX: e.MotionX, MotionY: e.MotionY, MotionZ: e.MotionZ,
		Living:    e.Living,
		Health:    mobHealth[targetName],
		MaxHealth: mobHealth[targetName],
	}
	s.mobFinalizeAttributes(ne)
	ne.Baby = e.Baby
	ne.CustomName = e.CustomName
	ne.CustomNameComp = e.CustomNameComp
	ne.CustomNameVisible = e.CustomNameVisible
	ne.Silent = e.Silent
	ne.NoAI = e.NoAI
	ne.NoGravity = e.NoGravity
	ne.LeftHanded = e.LeftHanded
	ne.PersistenceRequired = e.PersistenceRequired
	ne.CanPickUpLoot = e.CanPickUpLoot
	ne.FireTicks = e.FireTicks
	ne.CanBreakDoors = e.CanBreakDoors
	ne.Invulnerable = e.Invulnerable
	// The rest of ConversionType.convertCommon + the keepInventory
	// convert(): live-state fields that are not part of the fresh
	// spawn — grounded/fallFlying/hurtTime carry the motion state,
	// portalCooldown and absorptionAmount the counters, Age the
	// AgeableMob pair.
	ne.Grounded = e.Grounded
	ne.FallFlying = e.FallFlying
	ne.HurtTime = e.HurtTime
	ne.PortalCooldown = e.PortalCooldown
	// A parked entity converts parked — its chunk stays unticketed.
	ne.Dormant = e.Dormant
	// setAbsorptionAmount clamps to the max_absorption attribute —
	// base 0 on every mob — so a merged absorption never survives
	// the convert even though convertCommon calls the setter.
	ne.AbsorptionAmount = e.AbsorptionAmount
	if max := float32(attrValue(mobAttrBase(ne, "max_absorption"), ne.AttrMods["max_absorption"])); ne.AbsorptionAmount > max {
		ne.AbsorptionAmount = max
	}
	ne.Age = e.Age
	ne.LeashHolder = e.LeashHolder
	// convert() re-rides every passenger onto the new entity — the
	// ids move over; each passenger's Riding repoints below once
	// Spawn assigns the new id.
	if len(e.Passengers) > 0 {
		ne.Passengers = append([]int32(nil), e.Passengers...)
	}
	if len(e.Effects) > 0 {
		ne.Effects = make(map[int32]mobEffectInst, len(e.Effects))
		for k, v := range e.Effects {
			ne.Effects[k] = v
		}
	}
	if e.Equips != nil {
		ne.Equips = map[int32]ItemStack{}
		for k, v := range e.Equips {
			ne.Equips[k] = v
		}
	}
	if t, ok := e.ExtraNBT["Tags"]; ok {
		ne.ExtraNBT = map[string]any{"Tags": t}
	}
	s.entities.Spawn(ne)
	for _, pid := range ne.Passengers {
		if p, ok := s.entities.GetPtr(pid); ok {
			p.Riding = ne.ID
		}
	}
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
	if math.Hypot(dx, dz) == 0 {
		return
	}
	mobTurnToward(e, dx, dz, 90)
	e.HeadYaw = e.Yaw
	// PathNavigation's walk drive — the goal's speed lands on the delta
	// as travel input; the mob closes on the target by momentum, never
	// by a snap.
	s.mobTravelStep(e, s.dimWorld(e.Dim), speed)
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
	mobTurnToward(e, dx, dz, 30)
	e.HeadYaw = e.Yaw
	reach := entityBBDims(e)[0] + 0.6 + 0.3
	if dist > reach {
		s.mobTravelStep(e, s.dimWorld(e.Dim), mobChaseSpeed)
	}
	return true
}

// explodeCreeper is Creeper.explodeCreeper: explosionRadius (NBT,
// default 3) doubled when powered, centered on the feet, MOB
// interaction — then discard.
func (s *Server) explodeCreeper(e *Entity) {
	power := float32(e.BlastPower)
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
	// LivingEntity.spawnLingeringCloud's guard: no active effects,
	// no cloud — a clean creeper leaves none.
	if len(e.Effects) == 0 {
		return
	}
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
		now := s.gameTime()
		dose := func(mobOrSess *Entity) bool {
			bb := entityBBDims(mobOrSess)
			if mobOrSess.Y >= ep.Y+0.5 || mobOrSess.Y+bb[1] <= ep.Y {
				return true
			}
			dx, dz := mobOrSess.X-ep.X, mobOrSess.Z-ep.Z
			if dx*dx+dz*dz > float64(ep.CloudRadius)*float64(ep.CloudRadius) {
				return true
			}
			ep.CloudVictims[mobOrSess.ID] = ep.CloudAge + 20
			// The dose hurts through damageSources.indirectMagic(cloud,
			// owner): indirect_magic with the cloud as direct entity and
			// the spawning owner as cause (0 for ownerless clouds).
			src := &damageInfo{typeName: "indirect_magic", cause: ep.CloudOwnerID, direct: ep.ID}
			for _, dose := range potionCloudDoses(ep, now) {
				id, ticks := dose.id, dose.ticks
				inst := mobEffectInst{amplifier: dose.amplifier, hide: dose.hide}
				if isInstantaneousEffect(id) {
					s.mobInstantEffectScaled(mobOrSess, id, inst.amplifier, 0.5, src)
				} else {
					s.mobGiveEffectFlags(mobOrSess, id, inst.amplifier, ticks, dose.flags, src)
				}
			}
			return cloudAfterDose(ep)
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
			px, py, pz, _, _, _ := sess.Pos()
			if py >= ep.Y+0.5 || py+1.8 <= ep.Y {
				return true
			}
			dx, dz := px-ep.X, pz-ep.Z
			if dx*dx+dz*dz > float64(ep.CloudRadius)*float64(ep.CloudRadius) {
				return true
			}
			ep.CloudVictims[eid] = ep.CloudAge + 20
			psrc := damageSource{typeName: "indirect_magic", cause: ep.CloudOwnerID, direct: ep.ID}
			for _, dose := range potionCloudDoses(ep, now) {
				id, ticks := dose.id, dose.ticks
				inst := mobEffectInst{amplifier: dose.amplifier, hide: dose.hide}
				if isInstantaneousEffect(id) {
					s.applyInstantEffectScaled(sess, id, inst.amplifier, 0.5, psrc)
				} else {
					if dose.custom {
						applied := s.playerGiveEffectFlags(sess, id, inst.amplifier, ticks, inst.hide, &dose.flags, psrc)
						s.potionPlayerFlags(sess, id, inst.amplifier, ticks, dose.flags, applied)
					} else {
						s.playerGiveEffect(sess, id, inst.amplifier, ticks, inst.hide, psrc)
					}
				}
			}
			dead = !cloudAfterDose(ep)
			return !dead
		})
		if dead || ep.CloudRadius < 0.5 {
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

// mobAttr resolves a mob attribute's effective value: the AttrBase
// override, else the generated supplier default, through the vanilla
// modifier passes (attrValue).
func (s *Server) mobAttr(e *Entity, kn, attr string, def float64) float64 {
	base, ok := e.AttrBase[attr]
	if !ok {
		base, ok = entityAttrBases[kn][attr]
		if !ok {
			base = def
		}
	}
	return attrValue(base, e.AttrMods[attr])
}

// mobFinalizeAttributes is the attribute-and-flag half of vanilla's
// finalizeSpawn, measured on 26.2. Mob.finalizeSpawn:
//
//   - follow_range gains random_spawn_bonus = triangle(0, 0.11485)
//     multiplied_base, skipped when the attribute already carries a
//     modifier under that id;
//   - nextFloat < 0.05 sets LeftHanded.
//
// Zombie.finalizeSpawn then rolls setCanPickUpLoot(nextFloat <
// 0.55·special) (skipped for CONVERSION spawns, which arrive via
// mobConvert and never reach this path) and the 5% baby odds, and its
// handleAttributes(special, reason) tail:
//
//   - spawn_reinforcements base = nextDouble·0.1;
//   - knockback_resistance gains random_spawn_bonus = nextDouble·0.05
//     add_value;
//   - d3 = nextDouble·1.5·special > 1 adds zombie_random_spawn_bonus d3
//     multiplied_total to follow_range;
//   - the nextFloat < special·0.05 leader roll adds leader_zombie_bonus
//     (0.5+nextDouble·0.25 add_value on reinforcements, 1+nextDouble·3
//     multiplied_total on max_health) and heals to the new max.
func (s *Server) mobFinalizeAttributes(e *Entity) {
	name, _ := EntityTypeName(e.Type)
	if _, has := e.AttrMods["follow_range"]["minecraft:random_spawn_bonus"]; !has {
		s.mobAttrModAdd(e, "follow_range", "minecraft:random_spawn_bonus",
			0.11485*(rand.Float64()-rand.Float64()), 1)
	}
	e.LeftHanded = rand.Float64() < 0.05
	// AbstractHorse.finalizeSpawn's randomizeAttributes runs on every
	// spawn kind — the equine rolls are the same as the summon path's.
	if rolls, ok := horseAttrRolls[name]; ok {
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		for _, attr := range rolls {
			e.markAttrTouched(attr)
			horseAttrRoll(e, name, attr)
		}
	}
	switch name {
	case "zombie", "husk", "zombie_villager", "zombified_piglin", "drowned":
		special := s.specialMultiplier()
		e.CanPickUpLoot = rand.Float64() < 0.55*special
		if rand.Float64() < 0.05 {
			e.Baby = true
		}
		// Zombie.finalizeSpawn's jockey pair (baby && canSpawnJockey):
		// a 5% roll that hijacks a nearby free chicken, else a second
		// 5% that spawns a fresh JOCKEY-reason chicken under the
		// rider. Only plain zombie is jockey-eligible on vanilla —
		// husk/zombie_villager/drowned skip the block.
		if e.Baby && name == "zombie" {
			s.spawnChickenJockey(e)
		}
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		// randomizeReinforcementsChance: setBaseValue materializes the
		// instance, so the save pack carries it.
		e.AttrBase["spawn_reinforcements"] = rand.Float64() * 0.1
		e.markAttrTouched("spawn_reinforcements")
		s.mobAttrModAdd(e, "knockback_resistance", "minecraft:random_spawn_bonus",
			rand.Float64()*0.05, 0)
		if d3 := rand.Float64() * 1.5 * special; d3 > 1 {
			s.mobAttrModAdd(e, "follow_range", "minecraft:zombie_random_spawn_bonus", d3, 2)
		}
		if rand.Float64() < special*0.05 {
			s.mobAttrModAdd(e, "spawn_reinforcements", "minecraft:leader_zombie_bonus",
				rand.Float64()*0.25+0.5, 0)
			s.mobAttrModAdd(e, "max_health", "minecraft:leader_zombie_bonus",
				rand.Float64()*3+1, 2)
			e.Health = float32(attrValue(mobAttrBase(e, "max_health"), e.AttrMods["max_health"]))
		}
	}
}

// mobDefaultEquipReason is mobDefaultEquip with vanilla's spawn-reason
// split threaded through.
func (s *Server) mobDefaultEquipReason(e *Entity, reason spawnKind) {
	name, _ := EntityTypeName(e.Type)
	special := s.specialMultiplier()
	s.mobFinalizeAttributes(e)

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
	case "vex":
		// Vex.populateDefaultEquipmentSlots: iron sword, drop chance 0.
		mobEquipSlot(e, equipMainHand, "iron_sword")
		if e.DropChances == nil {
			e.DropChances = map[string]float64{}
		}
		e.DropChances["mainhand"] = 0
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
		e.Equips[equipMainHand] = enchantApply(st, selectEnchantment(newJavaRand(rand.Int63()), st, cost()))
	}
	for _, slot := range []int32{equipHelmet, equipChest, equipLegs, equipBoots} {
		if st := e.Equips[slot]; !st.Empty() && rand.Float64() < 0.5*special {
			e.Equips[slot] = enchantApply(st, selectEnchantment(newJavaRand(rand.Int63()), st, cost()))
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
	mobTurnToward(e, dx, dz, 30)
	e.HeadYaw = e.Yaw
	const radius2 = 15.0 * 15.0
	speed := mobChaseSpeed
	if mv, ok := entityAttrBases[kn]["movement_speed"]; ok {
		speed = mv
	}
	if d2 <= radius2 && e.rangedSee >= 20 {
		e.rangedStrafe++
	} else if dist > 0 {
		s.mobTravelStep(e, s.dimWorld(e.Dim), speed)
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
		// MoveControl.strafe(fwd, lat): the STRAFE tick scales the
		// ±0.5 inputs to length speedModifier·movement_speed (the m<1
		// floor keeps ±0.5/±0.5 at 0.125·√2), yaw-rotates them for
		// the isWalkable probe — an unwalkable probe reverts to a
		// plain forward zza=1 — then zza/xxa land on travel's input
		// pair and the friction chain carries them.
		fwd, lat := 0.5, 0.5
		if e.rangedBack {
			fwd = -0.5
		}
		if !e.rangedCW {
			lat = -0.5
		}
		m := math.Hypot(fwd, lat)
		if m < 1 {
			m = 1
		}
		zIn := fwd * speed / m
		xIn := lat * speed / m
		yr := float64(e.Yaw) * math.Pi / 180
		wx := zIn*math.Cos(yr) - xIn*math.Sin(yr)
		wz := xIn*math.Cos(yr) + zIn*math.Sin(yr)
		if !s.strafeWalkable(s.dimWorld(e.Dim), e, wx, wz) {
			zIn, xIn = 1, 0
		}
		s.mobTravelStepPair(e, s.dimWorld(e.Dim), speed, zIn, xIn)
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

// strafeWalkable approximates MoveControl.isWalkable: the node
// evaluator's getPathType at the cell one rotated strafe-step ahead
// must be WALKABLE — on flat ground that means open feet over a
// standable floor. Water, lava, void edges and walls all fail it.
func (s *Server) strafeWalkable(w *world.World, e *Entity, wx, wz float64) bool {
	if w == nil {
		return false
	}
	px := int(math.Floor(e.X + wx))
	py := int(math.Floor(e.Y))
	pz := int(math.Floor(e.Z + wz))
	feet := w.GetBlock(world.Pos{X: px, Y: py, Z: pz})
	below := w.GetBlock(world.Pos{X: px, Y: py - 1, Z: pz})
	// Fluids aren't in the NonSolid decoration set — a water or lava
	// cell is its own PathType (WATER/LAVA), never WALKABLE.
	if k, _, ok := fluidOf(feet); ok && k != fluidNone {
		return false
	}
	if k, _, ok := fluidOf(below); ok && k != fluidNone {
		return false
	}
	return world.NonSolid(feet) && !world.NonSolid(below)
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
	// Projectile.shoot: the normalized aim is perturbed per axis by
	// triangle(0, 0.0172275·inaccuracy) — inaccuracy is
	// 14 − difficulty·4 (14/10/6/2 peaceful..hard), so a normal-mode
	// skeleton still occasionally misses at range.
	inacc := 0.0172275 * (14 - float64(s.difficulty)*4)
	tri := func() float64 { return inacc * (rand.Float64() + rand.Float64() - 1) }
	const spd = 1.6
	vx := (dx/mag + tri()) * spd
	vyN := (vy/mag + tri()) * spd
	vz := (dz/mag + tri()) * spd
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
	case "wither_skeleton":
		// WitherSkeleton.getArrow: igniteForSeconds(100) — the arrow
		// flies burning and doPostHurtEffects ignites the victim for
		// 5 seconds on a landed hit.
		ae.FireTicks = 2000
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
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 1491, 5, 1, 1, nil)
}

// llamaSpitAt is Llama.performRangedAttack → spit(target): the
// LlamaSpit spawns a half-width forward of the owner's eye, shoots
// with 1.5 power / 10 divergence at target.getY(1/3) plus the
// 0.2·√horizDist arc lift, and plays entity.llama.spit positioned
// (pitch 1 ± 0.2 triangle). The one-shot contract — didSpit — is
// the caller's countdown arm, not this function's.
func (s *Server) llamaSpitAt(e *Entity, t *Session) {
	px, py, pz, _, _, _ := t.Pos()
	s.llamaSpitLaunch(e, px, py, pz, 1.8)
}

// llamaSpitAtEntity is the same shot at a mob target (the family's
// NearestAttackableTargetGoal picks) — target.getY(1/3) comes off
// the target's own bounding height.
func (s *Server) llamaSpitAtEntity(e *Entity, m *Entity) {
	dims := entityBBDims(m)
	s.llamaSpitLaunch(e, m.X, m.Y, m.Z, dims[1])
}

func (s *Server) llamaSpitLaunch(e *Entity, px, py, pz, targetH float64) {
	spID, ok := EntityTypeID("llama_spit")
	if !ok {
		return
	}
	dx := px - e.X
	dz := pz - e.Z
	// LlamaSpit ctor's setPos: owner pos stepped (bbW+1)/2 along
	// bodyRot — llama bbWidth 0.9 → 0.95 — at eyeY-0.1. By fire time
	// lookControl has converged yBodyRot onto the target (~30°/tick
	// over the 40-tick arm), so the step lands along the target
	// direction; e.Yaw itself may lag or be stolen by other goals.
	dims := entityBBDims(e)
	half := (dims[0] + 1.0) * 0.5
	horiz := math.Hypot(dx, dz)
	if horiz < 1e-6 {
		return
	}
	sx := e.X + half*(dx/horiz)
	sy := e.Y + dims[1]*0.95 - 0.1
	sz := e.Z + half*(dz/horiz)
	dy := (py + targetH/3) - sy // target.getY(1/3) of its box
	dy += horiz * 0.2
	mag := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if mag < 1e-6 {
		return
	}
	// Projectile.shoot: normalize, perturb each axis by
	// triangle(0, 0.0172275·divergence), scale by power.
	inacc := 0.0172275 * 10
	tri := func() float64 { return inacc * (rand.Float64() + rand.Float64() - 1) }
	const spd = 1.5
	vx := (dx/mag + tri()) * spd
	vy := (dy/mag + tri()) * spd
	vz := (dz/mag + tri()) * spd
	sp := s.entities.Spawn(&Entity{
		Type:    spID,
		Dim:     e.Dim,
		X:       sx,
		Y:       sy,
		Z:       sz,
		Yaw:     float32(math.Atan2(-vx, vz) * 180 / math.Pi),
		Pitch:   float32(math.Asin(-vy/math.Sqrt(vx*vx+vy*vy+vz*vz)) * 180 / math.Pi),
		MotionX: vx, MotionY: vy, MotionZ: vz,
		VX: int16(vx * 8000), VY: int16(vy * 8000), VZ: int16(vz * 8000),
		Data: e.ID,
	})
	sp.arrowShooter = e.ID
	s.broadcastEntitySpawn(nil, *sp)
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, sndLlamaSpit, srcNeutral,
		1.0, 1.0+float32((rand.Float64()+rand.Float64()-1)*0.2), nil)
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
	case "wither_skeleton":
		// WitherSkeleton.doHurtTarget: WITHER 200 ticks on every
		// landed LivingEntity hit — no difficulty gate.
		if id, ok := MobEffectID("wither"); ok {
			s.playerGiveEffect(target, id, 0, 200, false)
		}
	case "bee":
		// Bee.doHurtTarget: +1 stinger on the victim, POISON by
		// difficulty (10s normal, 18s hard, none easy/peaceful), then
		// setHasStung + stopBeingAngry + the BEE_STING sound — the bee
		// keeps flying but stops attacking and dies on the post-sting
		// clock.
		var dur int32
		switch s.difficulty {
		case DifficultyNormal:
			dur = 200
		case DifficultyHard:
			dur = 360
		}
		if dur > 0 {
			id, _ := MobEffectID("poison")
			s.playerGiveEffect(target, id, 0, dur, false)
		}
		e.Stung = true
		e.AngryAt = [16]byte{}
		e.AngryUntil = 0
		e.MetaDirty = true
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 160, srcNeutral, 1, 1, nil) // entity.bee.sting
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
// and plays entity.wolf.hurt. Vanilla anchors the sound on the entity
// (sound_entity), not positionally.
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
	// playSound is entity-attached — sound_entity anchored on the mob.
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, v.src(), v.vol, voicePitchE(e), nil)
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
// AgeableWaterCreature, nautiluses). Babies of these types count Age
// up toward 0 and grow up on arrival. Adult-only Animals (frog,
// camel_husk, parrot, zombie_nautilus) emit InLove but no age keys;
// Slime+allies are not AgeableMob at all — they carry Size.
var ageableTypes = map[string]bool{
	"armadillo": true, "axolotl": true, "bee": true, "camel": true,
	"chicken": true, "cow": true, "mooshroom": true,
	"dolphin": true, "donkey": true, "horse": true, "llama": true,
	"mule": true, "skeleton_horse": true, "trader_llama": true,
	"zombie_horse": true, "cat": true, "ocelot": true, "fox": true,
	"goat": true, "happy_ghast": true, "nautilus": true,
	"panda": true, "pig": true,
	"polar_bear": true, "rabbit": true, "sheep": true, "sniffer": true,
	"glow_squid": true, "squid": true, "turtle": true, "wolf": true,
	"strider": true, "hoglin": true, "villager": true,
	"wandering_trader": true,
}

// animalTypes is ageableTypes minus the non-Animal AgeableMobs —
// WaterAnimal squid family + dolphin and the AbstractVillager
// villagers — since Animal alone declares InLove/LoveCause.
var animalTypes = map[string]bool{
	"armadillo": true, "axolotl": true, "bee": true, "camel": true,
	"camel_husk": true, "chicken": true, "cow": true, "mooshroom": true,
	"donkey": true, "horse": true, "llama": true,
	"mule": true, "skeleton_horse": true, "trader_llama": true,
	"zombie_horse": true, "cat": true, "ocelot": true, "fox": true,
	"frog": true, "goat": true, "happy_ghast": true, "nautilus": true,
	"zombie_nautilus": true, "panda": true, "parrot": true, "pig": true,
	"polar_bear": true, "rabbit": true, "sheep": true, "sniffer": true,
	"turtle": true, "wolf": true, "strider": true, "hoglin": true,
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
	// ZombifiedPiglin extends Zombie — vanilla emits its IsBaby
	// byte like every other zombie-family type.
	"zombified_piglin": true,
}

// zombieTypes is vanilla's Zombie hierarchy — the types that
// declare CanBreakDoors/InWaterTime/DrownedConversionTime in their
// addAdditionalSaveData.
var zombieTypes = map[string]bool{
	"zombie":           true,
	"zombie_villager":  true,
	"drowned":          true,
	"husk":             true,
	"zombified_piglin": true,
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
	// isSunBurnTick's isInPowderSnow veto — the inside-step flag is
	// one tick stale here, which matches vanilla reading it inside
	// the same tick the snow set it.
	if e.IsInPowderSnow {
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

// mobWaterSensitive is LivingEntity.isSensitiveToWater's overrides —
// the four types aiStep hurts drown,1.0 every tick in water or rain.
var mobWaterSensitive = map[string]bool{
	"enderman":   true,
	"blaze":      true,
	"strider":    true,
	"snow_golem": true,
}

// mobInWaterOrRain is Entity.isInWaterOrRain — fluid contact at the
// feet, or rain at the feet or the top of the bounding box.
func (s *Server) mobInWaterOrRain(e *Entity) bool {
	if s.mobInWater(e) {
		return true
	}
	d := s.dims[e.Dim]
	if d == nil {
		return false
	}
	feet := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	if s.fireRainingAt(d, feet) {
		return true
	}
	feet.Y = int(math.Floor(e.Y + entityBBDims(e)[1]))
	return s.fireRainingAt(d, feet)
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
	fr := s.mobAttr(e, "piglin", "follow_range", 16.0)
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
		if cur, live := sessEffect(sess, id, sess.effectNow()); live &&
			cur.amplifier >= 2 && cur.expiry-sess.effectNow() > 1199 {
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

// guardianFlopStep runs Guardian.aiStep's land flop — measured off the
// 26.2 bytecode: in water the air resets to getMaxAirSupply (300); out
// of water while grounded the mob hops every tick — a random ±0.4
// horizontal and +0.5 up added to the delta, a random yaw, grounded
// cleared so the hop arcs instead of re-firing midair. The added
// delta integrates through the regular move chain (mobMoveStep's
// grounded/air paths). Under NoAI the flop still mutates once — the
// delta add fires — then latches airborne forever, because
// LivingEntity.aiStep only calls travel under isEffectiveAi: vanilla
// NoAI mobs genuinely hover, and so does mc-go's.
func (s *Server) guardianFlopStep(e *Entity, w *world.World) {
	if w == nil {
		return
	}
	bounds := entityBBDims(e)
	_, inWater, _, _, _, _ := entityFluidScan(w, e.X, e.Y, e.Z, bounds[0]/2, bounds[1], fluidWater)
	if inWater {
		e.AirSupply = 300
		return
	}
	if !e.Grounded {
		return
	}
	e.MotionX += (rand.Float64()*2 - 1) * 0.4
	e.MotionY += 0.5
	e.MotionZ += (rand.Float64()*2 - 1) * 0.4
	e.Yaw = rand.Float32() * 360
	e.Grounded = false
	e.NeedsSync = true
}

// guardianSetMoving latches Guardian.setMoving — DATA_ID_MOVING's
// transition emits metadata index 16 as a boolean.
func (s *Server) guardianSetMoving(e *Entity, moving bool) {
	if e.Moving == moving {
		return
	}
	e.Moving = moving
	e.MetaDirty = true
}

// guardianTravelWater is Guardian.travelInWater, measured off the 26.2
// jar: moveRelative(0.1, (0,0,zza)) — the input normalizes to a fixed
// 0.1 horizontal drive along the yaw while the speed field is on —
// then move(), then delta·0.9 on all axes. Unlike the generic
// travelInWater there is NO gravity sink: the only downward drift is
// the -0.005 applied to delta.y while not moving and target-free.
func (s *Server) guardianTravelWater(e *Entity, w *world.World) {
	bounds := entityBBDims(e)
	yr := float64(e.Yaw) * math.Pi / 180
	if e.WalkSpeed > 0 {
		e.MotionX += -0.1 * math.Sin(yr)
		e.MotionZ += 0.1 * math.Cos(yr)
	}
	s.entityMove(e, w, bounds[0]/2, bounds[1], e.MotionX, e.MotionY, e.MotionZ, 0, 1.0, true)
	e.MotionX *= 0.9
	e.MotionY *= 0.9
	e.MotionZ *= 0.9
	if !e.Moving && e.beamTarget == 0 {
		e.MotionY -= 0.005
	}
}

// guardianSwimIdle is GuardianMoveControl.tick's no-path tail:
// setSpeed(0) + setMoving(false), then travelInWater still runs.
func (s *Server) guardianSwimIdle(e *Entity, w *world.World) {
	e.WalkSpeed = 0
	s.guardianSetMoving(e, false)
	s.guardianTravelWater(e, w)
}

// guardianSwimDrive is the MOVE_TO arm of GuardianMoveControl.tick:
// the body yaw rotlerps 90°/tick toward the horizontal heading, the
// speed field lerps 0.125 toward speedModifier·movement_speed (1.0
// for the stroll), and the delta picks up the signature wobble — a
// sin((tickCount+id)·0.5)·0.05 horizontal sinusoid rotated by the yaw
// plus the bob sin((tickCount+id)·0.75)·0.05·(sin+cos)·0.25 and the
// normalized-y drive ·speed·0.1 — then setMoving(true) and travel.
func (s *Server) guardianSwimDrive(e *Entity, w *world.World) {
	dx := e.StrollTX - e.X
	dy := e.StrollTY - e.Y
	dz := e.StrollTZ - e.Z
	d := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if d < 0.4 || e.StrollSteps > 160 {
		e.Strolling = false
		e.WanderTicks = 0
		s.guardianSwimIdle(e, w)
		return
	}
	mobTurnToward(e, dx, dz, 90)
	e.LookYaw = e.Yaw
	name, _ := EntityTypeName(e.Type)
	mspd := mobWalkSpeed
	if v, ok := entityAttrBases[name]["movement_speed"]; ok {
		mspd = v
	}
	e.WalkSpeed += (mspd - e.WalkSpeed) * 0.125
	tc := float64(e.SyncCount + int64(e.ID))
	wob := math.Sin(tc*0.5) * 0.05
	yr := float64(e.Yaw) * math.Pi / 180
	cy, sy := math.Cos(yr), math.Sin(yr)
	bob := math.Sin(tc*0.75) * 0.05
	e.MotionX += wob * cy
	e.MotionY += bob*(sy+cy)*0.25 + e.WalkSpeed*(dy/d)*0.1
	e.MotionZ += wob * sy
	s.guardianSetMoving(e, true)
	s.guardianTravelWater(e, w)
}

// guardianMoveStep is the non-beam half of a guardian tick. Out of
// water there is no navigation — MoveControl sees an empty path and
// just zeroes the speed/moving flags while the flop (already run in
// aiStep order) and gravity do the work. In water RandomStrollGoal
// (speed 1.0, fixed 80-tick interval) rolls a 3D aquatic target — the
// vanilla pick is confined to water cells — and the MOVE_TO drive
// runs until arrival or the stall cap.
func (s *Server) guardianMoveStep(e *Entity, w *world.World) {
	bounds := entityBBDims(e)
	e.NeedsSync = true
	wDepth, inWater, _, _ := entityFluidTick(w, s.dims[e.Dim], e, bounds[0]/2, bounds[1])
	if !(inWater && wDepth > 0) {
		s.guardianSetMoving(e, false)
		// The same grounded-or-falling chain mobMoveStep runs, minus
		// the land wander: mobGrounded owns the air fall (arc of the
		// flop hop), and while grounded a zero-input travel integrates
		// the flop's XZ delta with friction.
		if s.mobGrounded(e, w) {
			s.mobTravelStep(e, w, 0)
		}
		return
	}
	e.FallDist = 0
	e.WanderTicks++
	if !e.Strolling {
		if e.WanderTicks%80 != 0 {
			s.guardianSwimIdle(e, w)
			return
		}
		// generateRandomPosForAquatic: a ±10 x/z, ±7 y pick that must
		// land in water — a dry cell aborts the stroll like a failed
		// vanilla pathfinding probe.
		tx := e.X + strollOffset(e.ID, e.WanderTicks)
		tz := e.Z + strollOffset(e.ID, e.WanderTicks+7)
		ty := e.Y + strollOffset(e.ID, e.WanderTicks+13)*0.7
		k, _, ok := fluidOf(w.GetBlock(world.Pos{
			X: int(math.Floor(tx)), Y: int(math.Floor(ty)), Z: int(math.Floor(tz)),
		}))
		if !ok || k != fluidWater {
			s.guardianSwimIdle(e, w)
			return
		}
		e.Strolling = true
		e.StrollSteps = 0
		e.StrollTX, e.StrollTY, e.StrollTZ = tx, ty, tz
	}
	e.StrollSteps++
	s.guardianSwimDrive(e, w)
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
		best := s.mobAttr(e, kn, "follow_range", 16.0)
		best *= best
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
			s.damageWith(target, s.mobMeleeDamage(e, kn), translatable("death.attack.mob",
				literal(target.getName()), literal(mobDisplayName(kn))),
				damageSource{typeName: "mob_attack", cause: e.ID, direct: e.ID})
		} else {
			s.damageMob(mobTarget, beam, "indirect_magic", nil,
				&damageInfo{typeName: "indirect_magic", cause: e.ID, direct: e.ID})
			s.damageMob(mobTarget, s.mobMeleeDamage(e, kn), "mob_attack", nil,
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

// thunderStrikeEntities is LightningBolt.tick's strikeEntities pass:
// every living entity inside the (x±3, y-3..y+9, z±3) box gets its
// per-type thunderHit — pig/villager convertTo on non-peaceful
// difficulty (ConversionParams.single: keepCustomName for the pig,
// not for the villager), creeper takes the hit then latches
// DATA_IS_POWERED, mooshroom flips its variant and plays
// MOOSHROOM_CONVERT — and everything without an override takes the
// generic 5-damage lightning hit (fire is unmodeled).
func (s *Server) thunderStrikeEntities(d *dimension, bolt *Entity, x, y, z float64, cause *Session) []string {
	var victims []string
	for _, oe := range s.entities.All() {
		if oe.ID == bolt.ID || oe.Dim != d.name || !oe.Living || oe.Dying {
			continue
		}
		if math.Abs(oe.X-x) > 3 || oe.Y < y-3 || oe.Y > y+9 || math.Abs(oe.Z-z) > 3 {
			continue
		}
		t, ok := s.entities.GetPtr(oe.ID)
		if !ok {
			continue
		}
		on, _ := EntityTypeName(t.Type)
		switch on {
		case "mooshroom":
			t.MooshroomVariant ^= 1
			s.broadcastEntityMetadata(oe.ID, encodeLivingMetadata(t, nil))
			s.playSoundSrc(oe.Dim, oe.X, oe.Y, oe.Z, 985, 6, 2, 1, nil)
		case "pig":
			if s.difficulty != DifficultyPeaceful {
				// Pig's finalizeConversion lambda: default equipment
				// (the golden sword) + persistence.
				s.convertEntityType(t, "zombified_piglin", func(ne *Entity) {
					if sid, ok := world.ItemID("golden_sword"); ok {
						ne.Equips[equipMainHand] = ItemStack{ID: sid, Count: 1}
					}
					ne.PersistenceRequired = true
				})
			} else {
				s.igniteMob(t, 160)
				s.damageMob(t, 5, "lightning_bolt", cause, &damageInfo{typeName: "lightning_bolt", cause: bolt.ID, direct: bolt.ID})
			}
		case "villager":
			if s.difficulty != DifficultyPeaceful {
				// Villager's lambda: Witch.finalizeSpawn + persistence
				// (the poi release is unmodeled).
				s.convertEntityType(t, "witch", func(ne *Entity) {
					ne.PersistenceRequired = true
				})
			} else {
				s.igniteMob(t, 160)
				s.damageMob(t, 5, "lightning_bolt", cause, &damageInfo{typeName: "lightning_bolt", cause: bolt.ID, direct: bolt.ID})
			}
		case "creeper":
			s.igniteMob(t, 160)
			s.damageMob(t, 5, "lightning_bolt", cause, &damageInfo{typeName: "lightning_bolt", cause: bolt.ID, direct: bolt.ID})
			if !t.Dying {
				t.Powered = true
				s.broadcastEntityMetadata(oe.ID, encodeLivingMetadata(t, nil))
			}
		default:
			s.igniteMob(t, 160)
			s.damageMob(t, 5, "lightning_bolt", cause, &damageInfo{typeName: "lightning_bolt", cause: bolt.ID, direct: bolt.ID})
		}
		victims = append(victims, on)
	}
	return victims
}

// convertEntityType is Mob.convertTo's SINGLE flavor: the replacement
// mob spawns at the old one's position and rotation (fresh health,
// dropped equipment), then the old entity is discarded — the wire
// order is add-then-remove. ConversionType.COMPONENTS_TO_COPY is
// {CUSTOM_NAME, CUSTOM_DATA}; convertCommon additionally carries the
// age pair, the baby flag and the active effects over.
func (s *Server) convertEntityType(t *Entity, toName string, finalize func(ne *Entity)) {
	ne := &Entity{
		Type: mustEntityType(toName),
		Dim:  t.Dim,
		X:    t.X, Y: t.Y, Z: t.Z,
		Yaw:               t.Yaw,
		Pitch:             t.Pitch,
		Living:            true,
		CustomName:        t.CustomName,
		CustomNameVisible: t.CustomNameVisible,
		ExtraNBT:          t.ExtraNBT,
		Age:               t.Age,
		Baby:              t.Baby,
		Effects:           t.Effects,
		Equips:            map[int32]ItemStack{},
	}
	if finalize != nil {
		finalize(ne)
	}
	ne = s.entities.Spawn(ne)
	s.broadcastEntitySpawnIn(t.Dim, nil, *ne)
	s.entities.Remove(t.ID)
	s.broadcastEntityRemoveIn(t.Dim, nil, []int32{t.ID})
}
