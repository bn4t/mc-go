package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Spawn-placement and spawn-rule checks — SpawnPlacements, the
// SpawnPlacementTypes, NaturalSpawner.isValidEmptySpawnBlock and the
// per-type static check*SpawnRules predicates, measured on 26.2
// (bytecode inspection; see naturalspawn_data.go for the tables).

func blockName(b world.Block) string {
	n, _ := world.StateOwner(b)
	return strings.TrimPrefix(n, "minecraft:")
}

func above(p world.Pos) world.Pos { return world.Pos{X: p.X, Y: p.Y + 1, Z: p.Z} }
func below(p world.Pos) world.Pos { return world.Pos{X: p.X, Y: p.Y - 1, Z: p.Z} }

// insideBorder is WorldBorder.isWithinBounds(pos).
func (s *Server) insideBorder(d *dimension, p world.Pos) bool {
	b := s.borderFor(d.name)
	h := b.size / 2
	x, z := float64(p.X), float64(p.Z)
	return x >= b.centerX-h && x+1 <= b.centerX+h && z >= b.centerZ-h && z+1 <= b.centerZ+h
}

// spawnPositionOk is SpawnPlacements.isSpawnPositionOk through the
// type's SpawnPlacementType:
//   - NO_RESTRICTIONS: always;
//   - IN_WATER: inside the border, water fluid here and a
//     non-conductor above;
//   - IN_LAVA: inside the border, lava fluid here;
//   - ON_GROUND: inside the border, the block below isValidSpawn for
//     the type and this cell and the one above isValidEmptySpawnBlock.
func (s *Server) spawnPositionOk(d *dimension, name string, p world.Pos) bool {
	pl := spawnPlacements[name]
	switch pl.place {
	case placeInWater:
		if !s.insideBorder(d, p) {
			return false
		}
		return worldgen.IsWater(d.w.GetBlock(p)) && !worldgen.RedstoneConductor(d.w.GetBlock(above(p)))
	case placeInLava:
		if !s.insideBorder(d, p) {
			return false
		}
		return worldgen.IsLava(d.w.GetBlock(p))
	case placeOnGround:
		if !s.insideBorder(d, p) {
			return false
		}
		if !blockValidSpawn(d.w.GetBlock(below(p)), name) {
			return false
		}
		return validEmptySpawnBlock(d.w.GetBlock(p), name) && validEmptySpawnBlock(d.w.GetBlock(above(p)), name)
	}
	return true
}

// blockValidSpawn is BlockState.isValidSpawn: the default predicate
// (isFaceSturdy UP and light emission < 14) unless the block's
// properties override it — Blocks.never for glass, tinted and stained
// glass, every trapdoor, copper grates, barrier, bedrock,
// chorus_flower, moving_piston and scaffolding; Blocks.ocelotOrParrot
// for leaves; polar bear only for ice and frosted_ice; Blocks.always
// for carved_pumpkin, jack_o_lantern, mud, redstone_lamp and
// soul_sand; fire-immune types only for magma_block.
func blockValidSpawn(b world.Block, typ string) bool {
	n := blockName(b)
	switch n {
	case "glass", "tinted_glass", "barrier", "bedrock", "chorus_flower", "moving_piston", "scaffolding":
		return false
	case "ice", "frosted_ice":
		return typ == "polar_bear"
	case "carved_pumpkin", "jack_o_lantern", "mud", "redstone_lamp", "soul_sand":
		return true
	case "magma_block":
		return mobFireImmune[typ]
	}
	switch {
	case strings.HasSuffix(n, "_trapdoor"), strings.HasSuffix(n, "_stained_glass"), strings.HasSuffix(n, "copper_grate"):
		return false
	case strings.HasSuffix(n, "_leaves"):
		return typ == "ocelot" || typ == "parrot"
	}
	return worldgen.SturdyUp(b) && worldgen.LightEmission(b) < 14
}

// validEmptySpawnBlock is NaturalSpawner.isValidEmptySpawnBlock: no
// full collision cube, no signal source, no fluid, not in
// prevent_mob_spawning_inside (rails), and not dangerous to the type
// (EntityType.isBlockDangerous: fire/lava/magma/lit campfire/lava
// cauldron for non-fire-immune types, wither rose, sweet berry bush,
// cactus, powder snow — minus the type's immuneTo block).
func validEmptySpawnBlock(b world.Block, typ string) bool {
	if worldgen.FullCollision(b) {
		return false
	}
	n := blockName(b)
	if isSignalSourceBlock(n) {
		return false
	}
	if worldgen.HasFluid(b) {
		return false
	}
	if blockInTag("prevent_mob_spawning_inside", n) {
		return false
	}
	return !blockDangerous(b, n, typ)
}

func blockDangerous(b world.Block, n, typ string) bool {
	if imm, ok := spawnImmuneTo[typ]; ok && imm == n {
		return false
	}
	if !mobFireImmune[typ] {
		if blockInTag("fire", n) || n == "lava" || n == "magma_block" || n == "lava_cauldron" {
			return true
		}
		if n == "campfire" || n == "soul_campfire" {
			if lit, _ := world.StateProperty(b, "lit"); lit == "true" {
				return true
			}
		}
	}
	switch n {
	case "wither_rose", "sweet_berry_bush", "cactus", "powder_snow":
		return true
	}
	return false
}

// boxCollides is !noCollision(aabb) against block collision shapes.
func (s *Server) boxCollides(d *dimension, x0, y0, z0, x1, y1, z1 float64) bool {
	const eps = 1e-7
	for cx := int(math.Floor(x0 + eps)); cx <= int(math.Floor(x1-eps)); cx++ {
		for cy := int(math.Floor(y0 + eps)); cy <= int(math.Floor(y1-eps)); cy++ {
			for cz := int(math.Floor(z0 + eps)); cz <= int(math.Floor(z1-eps)); cz++ {
				b := d.w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz})
				if b == world.Air {
					continue
				}
				var boxes []cellBox
				if worldgen.FullCollision(b) {
					boxes = []cellBox{{0, 0, 0, 1, 1, 1}}
				} else if bx, has := collisionBoxesOf(b); has {
					boxes = bx
				} else if !worldgen.BlocksMotion(b) {
					continue
				} else {
					boxes = []cellBox{{0, 0, 0, 1, 1, 1}}
				}
				fx, fy, fz := float64(cx), float64(cy), float64(cz)
				for _, bb := range boxes {
					if x1 > fx+bb[0]+eps && x0 < fx+bb[3]-eps &&
						y1 > fy+bb[1]+eps && y0 < fy+bb[4]-eps &&
						z1 > fz+bb[2]+eps && z0 < fz+bb[5]-eps {
						return true
					}
				}
			}
		}
	}
	return false
}

// containsAnyLiquid is LevelReader.containsAnyLiquid over the box's
// cells floor(min)..ceil(max)-1.
func (s *Server) containsAnyLiquid(d *dimension, x0, y0, z0, x1, y1, z1 float64) bool {
	for x := int(math.Floor(x0)); x < int(math.Ceil(x1)); x++ {
		for y := int(math.Floor(y0)); y < int(math.Ceil(y1)); y++ {
			for z := int(math.Floor(z0)); z < int(math.Ceil(z1)); z++ {
				if worldgen.HasFluid(d.w.GetBlock(world.Pos{X: x, Y: y, Z: z})) {
					return true
				}
			}
		}
	}
	return false
}

// boxObstructed is EntityGetter.isUnobstructed's entity half: a live
// entity with blocksBuilding (every LivingEntity and player, boats,
// minecarts, armor stands, primed tnt, falling blocks, end crystals,
// striders) overlapping the box.
func (s *Server) boxObstructed(d *dimension, x0, y0, z0, x1, y1, z1 float64) bool {
	overlap := func(ex, ey, ez, half, h float64) bool {
		return ex+half > x0 && ex-half < x1 && ey+h > y0 && ey < y1 && ez+half > z0 && ez-half < z1
	}
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Dim != d.name || e.presenceOnly || e.Dormant {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		if !e.Living && !blocksBuildingType(name) {
			continue
		}
		dims := entityBBDims(e)
		if overlap(e.X, e.Y, e.Z, dims[0]/2, dims[1]) {
			s.entities.mu.RUnlock()
			return true
		}
	}
	s.entities.mu.RUnlock()
	hit := false
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || !p.spawned.Load() || p.dimName() != d.name {
			return true
		}
		x, y, z, _, _, _ := p.Pos()
		if overlap(x, y, z, 0.3, 1.8) {
			hit = true
			return false
		}
		return true
	})
	return hit
}

func blocksBuildingType(name string) bool {
	switch name {
	case "tnt", "falling_block", "armor_stand", "end_crystal", "minecart", "chest_minecart",
		"furnace_minecart", "hopper_minecart", "tnt_minecart", "spawner_minecart",
		"command_block_minecart":
		return true
	}
	return strings.HasSuffix(name, "_boat") || strings.HasSuffix(name, "_raft") || name == "boat"
}

// spawnObstructionOk is Mob.checkSpawnObstruction on the fresh mob's
// box: no liquid and unobstructed by other entities; the water
// families drop the liquid test. At chunk generation (entities false)
// the WorldGenRegion lists no entities, so nothing obstructs. Ocelot
// additionally needs y >= sea level on grass or leaves.
func (s *Server) spawnObstructionOk(d *dimension, name string, x, y, z float64, entities bool) bool {
	id, _ := EntityTypeID(name)
	b := mobBounds(id)
	half, h := b[0]/2, b[1]
	x0, y0, z0, x1, y1, z1 := x-half, y, z-half, x+half, y+h, z+half
	if waterObstructionTypes[name] {
		return !entities || !s.boxObstructed(d, x0, y0, z0, x1, y1, z1)
	}
	if s.containsAnyLiquid(d, x0, y0, z0, x1, y1, z1) || (entities && s.boxObstructed(d, x0, y0, z0, x1, y1, z1)) {
		return false
	}
	if name == "ocelot" {
		p := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
		if p.Y < int(d.seaLevel) {
			return false
		}
		bn := blockName(d.w.GetBlock(below(p)))
		return bn == "grass_block" || strings.HasSuffix(bn, "_leaves")
	}
	return true
}

// walkTargetValue is getWalkTargetValue at the mob's block position.
func (s *Server) walkTargetValue(d *dimension, name string, p world.Pos) float32 {
	switch spawnWalkTarget[name] {
	case walkMonster:
		return 0.5 - s.lightMagic(d, p)
	case walkAnimal:
		if blockName(d.w.GetBlock(below(p))) == "grass_block" {
			return 10
		}
		return s.lightMagic(d, p) - 0.5
	case walkMushroom:
		if blockName(d.w.GetBlock(below(p))) == "mycelium" {
			return 10
		}
		return s.lightMagic(d, p) - 0.5
	case walkHoglin:
		if blockName(d.w.GetBlock(below(p))) == "crimson_nylium" {
			return 10
		}
		return 0
	case walkStrider:
		if worldgen.IsLava(d.w.GetBlock(p)) {
			return 10
		}
		return 0
	case walkTurtle:
		if worldgen.IsWater(d.w.GetBlock(p)) {
			return 10
		}
		if blockInTag("sand", blockName(d.w.GetBlock(below(p)))) {
			return 10
		}
		return s.lightMagic(d, p) - 0.5
	}
	return 0
}

// checkSpawnRules is SpawnPlacements.checkSpawnRules: a type not
// allowed in peaceful fails there, then the registered predicate.
func (s *Server) checkSpawnRules(d *dimension, name string, p world.Pos, r spawnRandom, chunkGen bool) bool {
	if notInPeaceful[name] && s.difficulty == DifficultyPeaceful {
		return false
	}
	pl, ok := spawnPlacements[name]
	if !ok {
		return true
	}
	return s.spawnRuleHolds(d, pl.rule, name, p, r)
}

// darkEnoughToSpawn is Monster.isDarkEnoughToSpawn: sky light above
// nextInt(32) fails; block light above the dimension's limit fails
// (unless the limit is 15); then the max local raw brightness —
// skyDarken 10 while thundering — must not exceed a sample of the
// dimension's monster_spawn_light_level.
func (s *Server) darkEnoughToSpawn(d *dimension, p world.Pos, r spawnRandom) bool {
	sky, blk := s.spawnLight(d, p)
	if sky > int(r.NextIntN(32)) {
		return false
	}
	f := spawnFactsOf(d)
	if f.blockLightLimit < 15 && blk > f.blockLightLimit {
		return false
	}
	var raw int
	if d.name == dimOverworld && s.isThundering() {
		raw = max(sky-10, blk)
	} else {
		raw = max(sky-s.spawnSkyDarken(d), blk)
	}
	sample := f.lightMin
	if f.lightM > f.lightMin {
		sample += int(r.NextIntN(int32(f.lightM - f.lightMin + 1)))
	}
	return raw <= sample
}

// brightEnoughToSpawn is Animal.isBrightEnoughToSpawn:
// getRawBrightness(pos, 0) > 8.
func (s *Server) brightEnoughToSpawn(d *dimension, p world.Pos) bool {
	return s.rawBrightness(d, p, 0) > 8
}

func (s *Server) canSeeSky(d *dimension, p world.Pos) bool {
	sky, _ := s.spawnLight(d, p)
	return sky >= 15
}

// mobSpawnRule is Mob.checkMobSpawnRules: the block below must be a
// valid spawn for the type.
func (s *Server) mobSpawnRule(d *dimension, name string, p world.Pos) bool {
	return blockValidSpawn(d.w.GetBlock(below(p)), name)
}

// surfaceWater is the shared surface-water window of
// WaterAnimal/AgeableWaterCreature: seaLevel-13 <= y <= seaLevel,
// water fluid below and a water block above.
func (s *Server) surfaceWater(d *dimension, p world.Pos) bool {
	sea := int(d.seaLevel)
	return p.Y >= sea-13 && p.Y <= sea &&
		worldgen.IsWater(d.w.GetBlock(below(p))) && blockName(d.w.GetBlock(above(p))) == "water"
}

func (s *Server) spawnRuleHolds(d *dimension, rule spawnRule, name string, p world.Pos, r spawnRandom) bool {
	peaceful := s.difficulty == DifficultyPeaceful
	bn := func(q world.Pos) string { return blockName(d.w.GetBlock(q)) }
	if tag, ok := spawnRuleTags[rule]; ok {
		return blockInTag(tag, bn(below(p))) && s.brightEnoughToSpawn(d, p)
	}
	switch rule {
	case ruleMob, ruleAnyLightMonster, ruleEndermite, ruleSilverfish, ruleSkeletonHorse, rulePatrolling:
		// Endermite/Silverfish/SkeletonHorse/PatrollingMonster are not
		// in any biome spawner list; their natural path is unused.
		return s.mobSpawnRule(d, name, p)
	case ruleMonster:
		return s.darkEnoughToSpawn(d, p, r) && s.mobSpawnRule(d, name, p)
	case ruleSurfaceMonsters:
		return s.darkEnoughToSpawn(d, p, r) && s.mobSpawnRule(d, name, p) && s.canSeeSky(d, p)
	case ruleSurfaceWaterAnimal, ruleSurfaceAgeableWater:
		return s.surfaceWater(d, p)
	case ruleAxolotl:
		return blockInTag("axolotls_spawnable_on", bn(below(p)))
	case ruleDrowned:
		if !worldgen.IsWater(d.w.GetBlock(below(p))) {
			return false
		}
		biome := s.spawnBiomeAt(d, p)
		can := !peaceful && s.darkEnoughToSpawn(d, p, r) && worldgen.IsWater(d.w.GetBlock(p))
		if s.biomeHasTag("more_frequent_drowned_spawns", biome) {
			return r.NextIntN(15) == 0 && can
		}
		return r.NextIntN(40) == 0 && p.Y < int(d.seaLevel)-5 && can
	case ruleGuardian:
		return false
	case ruleTropicalFish:
		if !worldgen.IsWater(d.w.GetBlock(below(p))) || bn(above(p)) != "water" {
			return false
		}
		return s.biomeHasTag("allows_tropical_fish_spawns_at_any_height", s.spawnBiomeAt(d, p)) || s.surfaceWater(d, p)
	case ruleBat:
		surface, _, _ := s.spawnColumnHeights(d, p.X, p.Z)
		if p.Y >= surface {
			return false
		}
		if r.NextBool() {
			return false
		}
		if s.maxLocalRawBrightness(d, p) > int(r.NextIntN(4)) {
			return false
		}
		if !blockInTag("bats_spawnable_on", bn(below(p))) {
			return false
		}
		return s.mobSpawnRule(d, name, p)
	case ruleGhast:
		return !peaceful && r.NextIntN(20) == 0 && s.mobSpawnRule(d, name, p)
	case ruleGlowSquid:
		return p.Y <= int(d.seaLevel)-33 && s.rawBrightness(d, p, 0) == 0 && bn(p) == "water"
	case ruleMagmaCube:
		return !peaceful
	case ruleSulfurCube:
		return true
	case ruleNautilus:
		sea := int(d.seaLevel)
		return p.Y >= sea-25 && p.Y <= sea-5 &&
			worldgen.IsWater(d.w.GetBlock(below(p))) && bn(above(p)) == "water"
	case ruleOcelot:
		return r.NextIntN(3) != 0
	case ruleHoglin, rulePiglin:
		return bn(below(p)) != "nether_wart_block"
	case rulePolarBear:
		if s.biomeHasTag("polar_bears_spawn_on_alternate_blocks", s.spawnBiomeAt(d, p)) {
			return s.brightEnoughToSpawn(d, p) && blockInTag("polar_bears_spawnable_on_alternate", bn(below(p)))
		}
		return blockInTag("animals_spawnable_on", bn(below(p))) && s.brightEnoughToSpawn(d, p)
	case ruleSlime:
		return s.slimeSpawnRule(d, name, p, r)
	case ruleStray:
		q := p
		for {
			q = above(q)
			if bn(q) != "powder_snow" {
				break
			}
		}
		return s.darkEnoughToSpawn(d, p, r) && s.mobSpawnRule(d, name, p) && s.canSeeSky(d, below(q))
	case ruleStrider:
		q := p
		for {
			q = above(q)
			if !worldgen.IsLava(d.w.GetBlock(q)) {
				break
			}
		}
		return worldgen.IsAir(d.w.GetBlock(q))
	case ruleTurtle:
		return p.Y < int(d.seaLevel)+4 && blockInTag("sand", bn(below(p))) && s.brightEnoughToSpawn(d, p)
	case ruleZombifiedPiglin:
		return !peaceful && bn(below(p)) != "nether_wart_block"
	}
	return true
}

// slimeSpawnRule is Slime.checkSlimeSpawnRules: never on peaceful; in
// an allows_surface_slime_spawns biome strictly between y 50 and 70
// the moon-phase chance (the moon timeline's surface_slime_spawn
// _chance) and a max-local-brightness <= nextInt(8) roll lead to the
// mob rule; otherwise a slime chunk (seedSlimeChunk(x, z, seed,
// 987234911).nextInt(10) == 0), a 1-in-10 roll and y < 40.
func (s *Server) slimeSpawnRule(d *dimension, name string, p world.Pos, r spawnRandom) bool {
	if s.difficulty == DifficultyPeaceful {
		return false
	}
	if s.biomeHasTag("allows_surface_slime_spawns", s.spawnBiomeAt(d, p)) && p.Y > 50 && p.Y < 70 &&
		r.NextFloat() < surfaceSlimeChance(s.dayTime()) &&
		s.maxLocalRawBrightness(d, p) <= int(r.NextIntN(8)) {
		return s.mobSpawnRule(d, name, p)
	}
	slimeChunk := isSlimeChunk(s.WorldSeed, p.X>>4, p.Z>>4)
	if r.NextIntN(10) == 0 && slimeChunk && p.Y < 40 {
		return s.mobSpawnRule(d, name, p)
	}
	return false
}

// isSlimeChunk is WorldgenRandom.seedSlimeChunk(x, z, seed,
// 987234911).nextInt(10) == 0 — the int-overflowing products widen
// before the sum, the salt XORs last.
func isSlimeChunk(seed int64, x, z int) bool {
	cx, cz := int32(x), int32(z)
	s := seed + int64(cx*cx*4987142) + int64(cx*5947611) + int64(cz*cz)*4392871 + int64(cz*389711) ^ 987234911
	return worldgen.NewLegacy(s).NextIntN(10) == 0
}

// surfaceSlimeChance samples the moon timeline (period 192000,
// constant keyframes every 24000): 0.5, 0.375, 0.25, 0.125, 0, 0.125,
// 0.25, 0.375.
func surfaceSlimeChance(dayTime int64) float32 {
	phases := [8]float32{0.5, 0.375, 0.25, 0.125, 0, 0.125, 0.25, 0.375}
	t := ((dayTime % 192000) + 192000) % 192000
	return phases[t/24000]
}
