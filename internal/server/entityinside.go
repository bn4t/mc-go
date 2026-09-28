package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// Entity.checkInsideBlocks runs once per entity per tick: every cell
// the entity's box overlaps gets its block's inside effect. Tripwire
// is the modeled family — a wire's collision shape is a full-cell
// column 2.5px tall when attached (strung between hooks) and 8px when
// loose.
//
// Spectator-mode players are isIgnoringBlockTriggers and never press.

// entityDims is the (width, height) box for the entity types mc-go
// spawns where it differs from the 0.6 x 1.8 default.
var entityDims = map[string][2]float64{
	"item":             {0.25, 0.25},
	"experience_orb":   {0.5, 0.5},
	"arrow":            {0.5, 0.5},
	"trident":          {0.5, 0.5},
	"armor_stand":      {0.5, 1.975},
	"bat":              {0.5, 0.9},
	"bee":              {0.7, 0.6},
	"chicken":          {0.4, 0.7},
	"cod":              {0.5, 0.3},
	"salmon":           {0.7, 0.4},
	"pufferfish":       {0.7, 0.7},
	"tropical_fish":    {0.5, 0.4},
	"tadpole":          {0.4, 0.3},
	"silverfish":       {0.4, 0.3},
	"endermite":        {0.4, 0.3},
	"cave_spider":      {0.7, 0.5},
	"spider":           {1.4, 0.9},
	"pig":              {0.9, 0.9},
	"sheep":            {0.9, 1.3},
	"cow":              {0.9, 1.4},
	"mooshroom":        {0.9, 1.4},
	"rabbit":           {0.4, 0.5},
	"fox":              {0.6, 0.7},
	"wolf":             {0.6, 0.85},
	"cat":              {0.6, 0.7},
	"ocelot":           {0.6, 0.7},
	"parrot":           {0.5, 0.9},
	"horse":            {1.3965, 1.6},
	"donkey":           {1.3965, 1.5},
	"mule":             {1.3965, 1.52},
	"llama":            {0.9, 1.87},
	"camel":            {1.7, 2.375},
	"creeper":          {0.6, 1.7},
	"skeleton":         {0.6, 1.99},
	"stray":            {0.6, 1.99},
	"wither_skeleton":  {0.7, 2.4},
	"zombie":           {0.6, 1.95},
	"husk":             {0.6, 1.95},
	"drowned":          {0.6, 1.95},
	"zombie_villager":  {0.6, 1.95},
	"zombified_piglin": {0.6, 1.95},
	"skeleton_horse":   {1.3965, 1.6},
	"zombie_horse":     {1.3965, 1.6},
	"villager":         {0.6, 1.95},
	"wandering_trader": {0.6, 1.95},
	"pillager":         {0.6, 1.95},
	"vindicator":       {0.6, 1.95},
	"evoker":           {0.6, 1.95},
	"witch":            {0.6, 1.95},
	"ravager":          {1.95, 2.2},
	"blaze":            {0.6, 1.8},
	"magma_cube":       {0.52, 0.52},
	"slime":            {0.52, 0.52},
	"turtle":           {1.2, 0.4},
	"axolotl":          {0.75, 0.42},
	"frog":             {0.5, 0.5},
	"allay":            {0.35, 0.6},
	"enderman":         {0.6, 2.9},
	"iron_golem":       {1.4, 2.7},
	"snow_golem":       {0.7, 1.9},
	"ghast":            {4.0, 4.0},
	"wither":           {0.9, 3.5},
	"ender_dragon":     {16.0, 8.0},
	"warden":           {0.9, 2.9},
	"sniffer":          {1.9, 1.75},
	"shulker":          {1.0, 1.0},
	"phantom":          {0.9, 0.5},
	"boat":             {1.375, 0.5625},
	"oak_boat":         {1.375, 0.5625},
	"minecart":         {0.98, 0.7},
}

// entityBox returns an entity's (width, height); 0.6 x 1.8 (player
// size) when the type isn't in the table.
func entityBox(name string) (w, h float64) {
	if d, ok := entityDims[name]; ok {
		return d[0], d[1]
	}
	return 0.6, 1.8
}

// entityInsideStep is Entity.checkInsideBlocks over every loaded
// entity plus each session's player entity.
func (s *Server) entityInsideStep() {
	spectator := map[int32]bool{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.gameMode == 3 {
			if eid := sess.entityID.Load(); eid != 0 {
				spectator[eid] = true
			}
		}
		return true
	})
	s.entities.mu.RLock()
	es := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		es = append(es, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range es {
		if spectator[e.ID] {
			continue
		}
		d := s.dims[e.Dim]
		if d == nil {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		// Entity.baseTick clears isInPowderSnow before the inside
		// pass re-sets it for this tick's powder-snow overlap.
		e.IsInPowderSnow = false
		w, h := entityBox(name)
		minX := int(math.Floor(e.X - float64(w)/2))
		maxX := int(math.Floor(e.X + float64(w)/2))
		minY := int(math.Floor(e.Y))
		maxY := int(math.Floor(e.Y + float64(h)))
		minZ := int(math.Floor(e.Z - float64(w)/2))
		maxZ := int(math.Floor(e.Z + float64(w)/2))
		// Vanilla's inside pass dedupes by effect applier: cells sharing
		// a block state apply once per tick, so a two-cell box in powder
		// snow still accrues +1 TicksFrozen, not +2.
		applied := map[string]bool{}
		for x := minX; x <= maxX; x++ {
			for y := minY; y <= maxY; y++ {
				for z := minZ; z <= maxZ; z++ {
					pos := world.Pos{X: x, Y: y, Z: z}
					b := d.w.GetBlock(pos)
					bn, ok := world.StateOwner(b)
					if !ok {
						continue
					}
					on := strings.TrimPrefix(bn, "minecraft:")
					if applied[on] {
						continue
					}
					switch on {
					case "tripwire":
						// The inside shape only covers the column's
						// low band — 0.15625 attached, 0.5 loose.
						band := 0.5
						if at, _ := world.StateProperty(b, "attached"); at == "true" {
							band = 0.15625
						}
						if e.Y >= float64(y)+band || e.Y+float64(h) <= float64(y) {
							continue
						}
						s.tripwireInside(d, pos, b)
					case "powder_snow":
						s.powderSnowInside(e, d, pos, name)
					case "fire":
						if name != "player" {
							s.fireBlockInside(e, name, 1)
						}
					case "soul_fire":
						if name != "player" {
							s.fireBlockInside(e, name, 2)
						}
					case "frogspawn":
						// FrogspawnBlock.entityInside: a falling
						// block landing on the egg breaks it
						// (destroyBlock → 2001 + drops).
						if name == "falling_block" {
							s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
							s.setNaturalBlock(d, pos, world.Air)
						}
					case "lava", "lava_cauldron":
						// Player records: the session-side fluid pass
						// in playerFluidTickStep owns ignite, damage and
						// extinguish for players — a record-level ignite
						// would broadcast its own index-0 beside it.
						if name != "player" {
							s.lavaInside(e, name)
						}
					case "water", "bubble_column", "water_cauldron", "powder_snow_cauldron":
						if name != "player" {
							s.extinguishInside(e)
						}
					}
					applied[on] = true
				}
			}
		}
	}
}

// entityInLavaWorld is Entity.isInLava for mobs: any bounding-box
// cell holding a lava fluid state. The session-scoped variant in
// ride.go delegates here via the dimension's world.
func entityInLavaWorld(w *world.World, e *Entity) bool {
	if w == nil {
		return false
	}
	bb := entityBBDims(e)
	half := bb[0] / 2
	for bx := int(math.Floor(e.X - half)); bx <= int(math.Floor(e.X+half)); bx++ {
		for by := int(math.Floor(e.Y)); by <= int(math.Floor(e.Y+bb[1])); by++ {
			for bz := int(math.Floor(e.Z - half)); bz <= int(math.Floor(e.Z+half)); bz++ {
				if name, ok := world.StateOwner(w.GetBlock(world.Pos{X: bx, Y: by, Z: bz})); ok && strings.TrimPrefix(name, "minecraft:") == "lava" {
					return true
				}
			}
		}
	}
	return false
}

// freezeImmuneTypes is vanilla's freeze_immune_entity_types tag —
// types whose canFreeze returns false; they never accrue
// TicksFrozen and always thaw.
var freezeImmuneTypes = map[string]bool{
	"stray": true, "polar_bear": true, "snow_golem": true, "wither": true,
}

// freezeHurtsExtraTypes is vanilla's freeze_hurts_extra_types tag —
// the nether natives take five times the freeze damage.
var freezeHurtsExtraTypes = map[string]bool{
	"strider": true, "blaze": true, "magma_cube": true,
}

// extinguishInside is InsideBlockEffectType.EXTINGUISH: clearFire
// clamps remainingFireTicks to ≤0 — a burning entity's countdown
// stops and its shared-flags on-fire bit drops.
func (s *Server) extinguishInside(e *Entity) {
	if e.FireTicks > 0 {
		e.FireTicks = 0
		s.broadcastMobSharedFlags(e.ID, false, e.GlowUntil > s.gameTime())
	}
}

// powderSnowInside is PowderSnowBlock.entityInside: runBefore
// EXTINGUISH melts the snow block when a burning entity that may
// interact steps in (mob_griefing for mobs), FREEZE sets
// isInPowderSnow and accrues TicksFrozen by one up to the 140-tick
// requirement for entities that can freeze, and EXTINGUISH puts the
// burn out.
func (s *Server) powderSnowInside(e *Entity, d *dimension, pos world.Pos, name string) {
	if e.FireTicks > 0 {
		// The melt λ gates on mobGriefing for non-player entities.
		if s.gameRuleBool("minecraft:mob_griefing") || name == "player" {
			s.setNaturalBlock(d, pos, world.Air)
		}
	}
	e.IsInPowderSnow = true
	if !freezeImmuneTypes[name] && e.TicksFrozen < 140 {
		e.TicksFrozen++
		if e.Living {
			e.MetaDirty = true
		}
	}
	s.extinguishInside(e)
}

// fireBlockInside is BaseFireBlock.entityInside: CLEAR_FREEZE zeroes
// TicksFrozen, FIRE_IGNITE runs fireIgnite (a remaining tick below
// zero counts up one, a non-negative countdown ignites for eight
// seconds — the +random(1,3) ServerPlayer branch is player-only and
// players never reach this path as mobs), and the runAfter consumer
// deals the in_fire damage — one heart on fire, two on soul fire.
func (s *Server) fireBlockInside(e *Entity, name string, dmg float32) {
	e.TicksFrozen = 0
	if e.FireTicks < 0 {
		e.FireTicks++
	}
	if e.FireTicks >= 0 && !mobFireImmune[name] {
		s.igniteMob(e, 160)
	}
	if e.Living {
		s.damageMob(e, dmg, "in_fire", nil, &damageInfo{typeName: "in_fire"})
	}
}

// lavaInside is LavaFluid/LavaCauldronBlock.entityInside:
// CLEAR_FREEZE, LAVA_IGNITE (fire-immune entities skip the
// fifteen-second ignite), and the runAfter lavaHurt — four damage
// plus the GENERIC_BURN sound.
func (s *Server) lavaInside(e *Entity, name string) {
	e.TicksFrozen = 0
	if !mobFireImmune[name] {
		s.igniteMob(e, 300)
	}
	if name == "item" {
		// ItemEntity.lavaHurt: hurt(lava, 4) — health 5 → burns a
		// tick then dies on the second. GENERIC_BURN rides each
		// hurt tick while health > 0; damage_resistant:#is_fire
		// items (netherite family, ancient_debris) are fireImmune.
		if itemFireImmune(e) {
			return
		}
		if e.ItemHP == 0 {
			e.ItemHP = 5
		}
		e.ItemHP -= 4
		if !e.Silent {
			// ItemEntity.getSoundSource overrides to AMBIENT.
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z,
				soundEventIDs["entity.generic.burn"], srcAmbient,
				0.4, 2+rand.Float32()*0.4, nil)
		}
		if e.ItemHP <= 0 {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
		}
		return
	}
	if e.Living {
		dealt, _, _ := s.damageMob(e, 4, "lava", nil, &damageInfo{typeName: "lava"})
		// Entity.lavaHurt: GENERIC_BURN rides each landed lava hit —
		// shouldPlayLavaHurtSound is true for every living entity
		// (ItemEntity's health>0||tick%10 is the only override).
		if dealt > 0 && !e.Silent {
			v := mobVoiceOf(e)
			// Entity.playSound → positional; for a player
			// entity Player.playSound self-excludes.
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z,
				soundEventIDs["entity.generic.burn"], v.src(),
				0.4, 2+rand.Float32()*0.4, s.sessionByEntityID(e.ID))
		}
	}
}

// itemFireImmune is ItemEntity.fireImmune: the stack's
// damage_resistant:#is_fire component — measured on 26.2 as the
// netherite family (tools, armor, ingot, scrap, block, spears,
// horse armor, nautilus armor) plus ancient_debris.
func itemFireImmune(e *Entity) bool {
	n, _ := itemNameOf(e.Item.ID)
	return itemFireImmuneNames[n]
}

var itemFireImmuneNames = map[string]bool{
	"netherite_pickaxe": true, "netherite_ingot": true,
	"netherite_sword": true, "netherite_chestplate": true,
	"netherite_scrap": true, "netherite_axe": true,
	"netherite_shovel": true, "netherite_leggings": true,
	"netherite_boots": true, "netherite_helmet": true,
	"netherite_horse_armor": true, "netherite_block": true,
	"netherite_hoe": true, "netherite_nautilus_armor": true,
	"netherite_spear": true, "ancient_debris": true,
}

// tripwireInside is TripWireBlock.entityInside: an unpowered wire
// with no pending recheck presses.
func (s *Server) tripwireInside(d *dimension, pos world.Pos, b world.Block) {
	if p, _ := world.StateProperty(b, "powered"); p == "true" {
		return
	}
	if s.ticks != nil && s.ticks.isQueued(schedKey{dim: d.name, pos: pos, kind: tickBlock}) {
		return
	}
	s.tripwireSetPressed(d, pos, true)
}

// tripwireSetPressed is TripWireBlock.checkPressed: flip powered when
// it disagrees, walk the run's hooks, and arm the 10-tick recheck
// while pressed.
func (s *Server) tripwireSetPressed(d *dimension, pos world.Pos, pressed bool) {
	b := d.w.GetBlock(pos)
	name, ok := world.StateOwner(b)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "tripwire" {
		return
	}
	was, _ := world.StateProperty(b, "powered")
	if (was == "true") != pressed {
		nb, ok := world.WithProperty(b, "powered", boolStr(pressed))
		if ok {
			s.setNaturalBlock(d, pos, nb)
			s.tripwireUpdateSource(d, pos, nb)
		}
	}
	if pressed {
		s.scheduleBlockTick(d, pos, 10)
	}
}

// tripwireUpdateSource is TripWireBlock.updateSource: from the pressed
// wire, walk south and west through wires; a hook facing back at the
// run recalculates.
func (s *Server) tripwireUpdateSource(d *dimension, pos world.Pos, _ world.Block) {
	for _, dir := range [2]string{"south", "west"} {
		for i := 1; i < 42; i++ {
			hp := pos
			for k := 0; k < i; k++ {
				hp = shiftPos(hp, dir)
			}
			hb := d.w.GetBlock(hp)
			hn, _ := world.StateOwner(hb)
			hn = strings.TrimPrefix(hn, "minecraft:")
			if hn == "tripwire_hook" {
				f, _ := world.StateProperty(hb, "facing")
				if f == dirOpposite(dir) {
					s.hookRecalc(d, hp, f)
				}
				break
			}
			if hn != "tripwire" {
				break
			}
		}
	}
}

// tripwireEntities returns whether any non-spectator entity still
// overlaps the wire's inside band — the tick's entity scan.
func (s *Server) tripwireEntities(d *dimension, pos world.Pos, b world.Block) bool {
	band := 0.5
	if at, _ := world.StateProperty(b, "attached"); at == "true" {
		band = 0.15625
	}
	spectator := map[int32]bool{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.gameMode == 3 {
			if eid := sess.entityID.Load(); eid != 0 {
				spectator[eid] = true
			}
		}
		return true
	})
	for _, e := range s.entities.All() {
		if spectator[e.ID] || e.Dim != d.name {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		w, h := entityBox(name)
		if e.X+float64(w)/2 <= float64(pos.X) || e.X-float64(w)/2 >= float64(pos.X+1) {
			continue
		}
		if e.Z+float64(w)/2 <= float64(pos.Z) || e.Z-float64(w)/2 >= float64(pos.Z+1) {
			continue
		}
		if e.Y >= float64(pos.Y)+band || e.Y+float64(h) <= float64(pos.Y) {
			continue
		}
		return true
	}
	return false
}

// tripwireTick is TripWireBlock.tick: a powered wire re-scans; empty
// means release, pressed means reschedule.
func (s *Server) tripwireTick(d *dimension, pos world.Pos, b world.Block) {
	if p, _ := world.StateProperty(b, "powered"); p != "true" {
		return
	}
	s.tripwireSetPressed(d, pos, s.tripwireEntities(d, pos, b))
}
