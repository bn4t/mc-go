package server

// Measured natural-spawning facts for 26.2. Source class: bytecode
// inspection (javap -c -p of server-26.2.jar) for the enum and builder
// constants, report/datapack (the datagen biome and tag JSON bundled in
// internal/worldgen and internal/data) for spawner lists and tags.

// mobCategory is MobCategory, in its enum order — the order
// NaturalSpawner.SPAWNING_CATEGORIES walks (every value but MISC).
type mobCategory uint8

const (
	catMonster mobCategory = iota
	catCreature
	catAmbient
	catAxolotls
	catUndergroundWaterCreature
	catWaterCreature
	catWaterAmbient
	catMisc
	catCount
)

// mobCategoryInfo is MobCategory.<clinit>: (serialized name, max
// instances per chunk, isFriendly, isPersistent, despawnDistance).
// noDespawnDistance is the constant 32 for every value.
var mobCategoryInfo = [catCount]struct {
	name       string
	max        int
	friendly   bool
	persistent bool
	despawn    int
}{
	catMonster:                  {"monster", 70, false, false, 128},
	catCreature:                 {"creature", 10, true, true, 128},
	catAmbient:                  {"ambient", 15, true, false, 128},
	catAxolotls:                 {"axolotls", 5, true, false, 128},
	catUndergroundWaterCreature: {"underground_water_creature", 5, true, false, 128},
	catWaterCreature:            {"water_creature", 5, true, false, 128},
	catWaterAmbient:             {"water_ambient", 20, true, false, 64},
	catMisc:                     {"misc", -1, true, true, 128},
}

const mobNoDespawnDistance = 32

// mobCategoryByName resolves a biome "spawners" key.
func mobCategoryByName(name string) (mobCategory, bool) {
	for c := mobCategory(0); c < catCount; c++ {
		if mobCategoryInfo[c].name == name {
			return c, true
		}
	}
	return 0, false
}

// entityCategories is every EntityType whose builder (EntityTypes
// .<clinit>, EntityType.Builder.of(factory, MobCategory.X)) names a
// category other than MISC. Everything absent is MISC — villagers,
// golems, players, items and the rest never count against a cap.
// Note tadpole is CREATURE (despawn distance 128), not water ambient.
var entityCategories = map[string]mobCategory{
	"allay": catCreature, "armadillo": catCreature, "axolotl": catAxolotls,
	"bat": catAmbient, "bee": catCreature, "blaze": catMonster,
	"bogged": catMonster, "breeze": catMonster, "camel": catCreature,
	"camel_husk": catMonster, "cat": catCreature, "cave_spider": catMonster,
	"chicken": catCreature, "cod": catWaterAmbient, "cow": catCreature,
	"creaking": catMonster, "creeper": catMonster, "dolphin": catWaterCreature,
	"donkey": catCreature, "drowned": catMonster, "elder_guardian": catMonster,
	"enderman": catMonster, "endermite": catMonster, "ender_dragon": catMonster,
	"evoker": catMonster, "fox": catCreature, "frog": catCreature,
	"ghast": catMonster, "happy_ghast": catCreature, "giant": catMonster,
	"glow_squid": catUndergroundWaterCreature, "goat": catCreature,
	"guardian": catMonster, "hoglin": catMonster, "horse": catCreature,
	"husk": catMonster, "illusioner": catMonster, "llama": catCreature,
	"magma_cube": catMonster, "mooshroom": catCreature, "mule": catCreature,
	"nautilus": catWaterCreature, "ocelot": catCreature, "panda": catCreature,
	"parched": catMonster, "parrot": catCreature, "phantom": catMonster,
	"pig": catCreature, "piglin": catMonster, "piglin_brute": catMonster,
	"pillager": catMonster, "polar_bear": catCreature, "pufferfish": catWaterAmbient,
	"rabbit": catCreature, "ravager": catMonster, "salmon": catWaterAmbient,
	"sheep": catCreature, "shulker": catMonster, "silverfish": catMonster,
	"skeleton": catMonster, "skeleton_horse": catCreature, "slime": catMonster,
	"sniffer": catCreature, "spider": catMonster, "squid": catWaterCreature,
	"stray": catMonster, "strider": catCreature, "sulfur_cube": catMonster,
	"tadpole": catCreature, "trader_llama": catCreature, "tropical_fish": catWaterAmbient,
	"turtle": catCreature, "vex": catMonster, "vindicator": catMonster,
	"wandering_trader": catCreature, "warden": catMonster, "witch": catMonster,
	"wither": catMonster, "wither_skeleton": catMonster, "wolf": catCreature,
	"zoglin": catMonster, "zombie": catMonster, "zombie_horse": catMonster,
	"zombie_nautilus": catMonster, "zombie_villager": catMonster,
	"zombified_piglin": catMonster,
}

func entityCategoryOf(name string) mobCategory {
	if c, ok := entityCategories[name]; ok {
		return c
	}
	return catMisc
}

// spawnFarFromPlayer is EntityType.Builder.canSpawnFarFromPlayer:
// only pillager and shulker set it.
var spawnFarFromPlayer = map[string]bool{"pillager": true, "shulker": true}

// notInPeaceful is EntityType.Builder.notInPeaceful (allowedInPeaceful
// false): SpawnPlacements.checkSpawnRules refuses these on peaceful
// and Mob.checkDespawn discards them there.
var notInPeaceful = map[string]bool{
	"blaze": true, "bogged": true, "breeze": true, "cave_spider": true,
	"creaking": true, "creeper": true, "drowned": true, "elder_guardian": true,
	"enderman": true, "endermite": true, "evoker": true, "ghast": true,
	"giant": true, "guardian": true, "hoglin": true, "husk": true,
	"illusioner": true, "magma_cube": true, "parched": true, "phantom": true,
	"piglin_brute": true, "pillager": true, "ravager": true, "silverfish": true,
	"skeleton": true, "slime": true, "spider": true, "stray": true,
	"vex": true, "vindicator": true, "warden": true, "witch": true,
	"wither": true, "wither_skeleton": true, "zoglin": true, "zombie": true,
	"zombie_villager": true, "zombified_piglin": true,
}

// spawnDimsScale is EntityType.Builder.spawnDimensionsScale: the
// spawn AABB (getSpawnAABB) scales the type's base size by it.
var spawnDimsScale = map[string]float64{"slime": 4, "magma_cube": 4, "sulfur_cube": 2}

// spawnImmuneTo is EntityType.Builder.immuneTo — the dangerous-block
// exemptions isBlockDangerous checks first.
var spawnImmuneTo = map[string]string{
	"stray": "powder_snow", "snow_golem": "powder_snow",
	"wither_skeleton": "wither_rose", "fox": "sweet_berry_bush",
}

// Placement kinds are SpawnPlacementTypes; the zero value is the
// NO_RESTRICTIONS default getPlacementType answers for an
// unregistered type.
type placementKind uint8

const (
	placeNoRestrictions placementKind = iota
	placeOnGround
	placeInWater
	placeInLava
)

// heightmapKind is the Heightmap.Types a SpawnPlacements entry uses
// (getTopNonCollidingPos at chunk generation).
type heightmapKind uint8

const (
	hmMotionBlockingNoLeaves heightmapKind = iota
	hmMotionBlocking
)

// spawnRule names a SpawnPlacements$SpawnPredicate: each is one of
// the static check*SpawnRules methods (bootstrap lambdas #1-#39 of
// SpawnPlacements.<clinit>).
type spawnRule uint8

const (
	ruleNone                spawnRule = iota // unregistered: no predicate, always passes
	ruleMob                                  // Mob.checkMobSpawnRules
	ruleMonster                              // Monster.checkMonsterSpawnRules
	ruleAnyLightMonster                      // Monster.checkAnyLightMonsterSpawnRules
	ruleSurfaceMonsters                      // Monster.checkSurfaceMonstersSpawnRules
	ruleAnimal                               // Animal.checkAnimalSpawnRules
	ruleSurfaceWaterAnimal                   // WaterAnimal.checkSurfaceWaterAnimalSpawnRules
	ruleSurfaceAgeableWater                  // AgeableWaterCreature.checkSurfaceAgeableWaterCreatureSpawnRules
	ruleAxolotl
	ruleDrowned
	ruleGuardian
	ruleTropicalFish
	ruleArmadillo
	ruleBat
	ruleCamel
	ruleEndermite
	ruleFrog
	ruleGhast
	ruleGlowSquid
	ruleGoat
	ruleMagmaCube
	ruleSulfurCube
	ruleMushroom
	ruleNautilus
	ruleOcelot
	ruleParrot
	ruleHoglin
	rulePiglin
	rulePatrolling
	rulePolarBear
	ruleRabbit
	ruleSilverfish
	ruleSkeletonHorse
	ruleSlime
	ruleStray
	ruleStrider
	ruleTurtle
	ruleWolf
	ruleZombifiedPiglin
	ruleFox
)

type spawnPlacementData struct {
	place placementKind
	hm    heightmapKind
	rule  spawnRule
}

// spawnPlacements is SpawnPlacements.<clinit>'s register table, in
// its order: (type, placement, heightmap, predicate).
var spawnPlacements = map[string]spawnPlacementData{
	"axolotl":          {placeInWater, hmMotionBlockingNoLeaves, ruleAxolotl},
	"cod":              {placeInWater, hmMotionBlockingNoLeaves, ruleSurfaceWaterAnimal},
	"dolphin":          {placeInWater, hmMotionBlockingNoLeaves, ruleSurfaceAgeableWater},
	"drowned":          {placeInWater, hmMotionBlockingNoLeaves, ruleDrowned},
	"guardian":         {placeInWater, hmMotionBlockingNoLeaves, ruleGuardian},
	"pufferfish":       {placeInWater, hmMotionBlockingNoLeaves, ruleSurfaceWaterAnimal},
	"salmon":           {placeInWater, hmMotionBlockingNoLeaves, ruleSurfaceWaterAnimal},
	"squid":            {placeInWater, hmMotionBlockingNoLeaves, ruleSurfaceAgeableWater},
	"tropical_fish":    {placeInWater, hmMotionBlockingNoLeaves, ruleTropicalFish},
	"armadillo":        {placeOnGround, hmMotionBlockingNoLeaves, ruleArmadillo},
	"bat":              {placeOnGround, hmMotionBlockingNoLeaves, ruleBat},
	"blaze":            {placeOnGround, hmMotionBlockingNoLeaves, ruleAnyLightMonster},
	"bogged":           {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"breeze":           {placeOnGround, hmMotionBlockingNoLeaves, ruleAnyLightMonster},
	"camel":            {placeOnGround, hmMotionBlockingNoLeaves, ruleCamel},
	"camel_husk":       {placeOnGround, hmMotionBlockingNoLeaves, ruleSurfaceMonsters},
	"cave_spider":      {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"chicken":          {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"cow":              {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"creeper":          {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"donkey":           {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"enderman":         {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"endermite":        {placeOnGround, hmMotionBlockingNoLeaves, ruleEndermite},
	"ender_dragon":     {placeOnGround, hmMotionBlockingNoLeaves, ruleMob},
	"frog":             {placeOnGround, hmMotionBlockingNoLeaves, ruleFrog},
	"ghast":            {placeOnGround, hmMotionBlockingNoLeaves, ruleGhast},
	"happy_ghast":      {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"giant":            {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"glow_squid":       {placeInWater, hmMotionBlockingNoLeaves, ruleGlowSquid},
	"goat":             {placeOnGround, hmMotionBlockingNoLeaves, ruleGoat},
	"horse":            {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"husk":             {placeOnGround, hmMotionBlockingNoLeaves, ruleSurfaceMonsters},
	"iron_golem":       {placeOnGround, hmMotionBlockingNoLeaves, ruleMob},
	"llama":            {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"magma_cube":       {placeOnGround, hmMotionBlockingNoLeaves, ruleMagmaCube},
	"sulfur_cube":      {placeOnGround, hmMotionBlockingNoLeaves, ruleSulfurCube},
	"mooshroom":        {placeOnGround, hmMotionBlockingNoLeaves, ruleMushroom},
	"mule":             {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"nautilus":         {placeInWater, hmMotionBlockingNoLeaves, ruleNautilus},
	"ocelot":           {placeOnGround, hmMotionBlocking, ruleOcelot},
	"parrot":           {placeOnGround, hmMotionBlocking, ruleParrot},
	"pig":              {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"hoglin":           {placeOnGround, hmMotionBlockingNoLeaves, ruleHoglin},
	"piglin":           {placeOnGround, hmMotionBlockingNoLeaves, rulePiglin},
	"pillager":         {placeOnGround, hmMotionBlockingNoLeaves, rulePatrolling},
	"polar_bear":       {placeOnGround, hmMotionBlockingNoLeaves, rulePolarBear},
	"rabbit":           {placeOnGround, hmMotionBlockingNoLeaves, ruleRabbit},
	"sheep":            {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"silverfish":       {placeOnGround, hmMotionBlockingNoLeaves, ruleSilverfish},
	"skeleton":         {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"skeleton_horse":   {placeOnGround, hmMotionBlockingNoLeaves, ruleSkeletonHorse},
	"slime":            {placeOnGround, hmMotionBlockingNoLeaves, ruleSlime},
	"snow_golem":       {placeOnGround, hmMotionBlockingNoLeaves, ruleMob},
	"spider":           {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"stray":            {placeOnGround, hmMotionBlockingNoLeaves, ruleStray},
	"parched":          {placeOnGround, hmMotionBlockingNoLeaves, ruleSurfaceMonsters},
	"strider":          {placeInLava, hmMotionBlockingNoLeaves, ruleStrider},
	"turtle":           {placeOnGround, hmMotionBlockingNoLeaves, ruleTurtle},
	"villager":         {placeOnGround, hmMotionBlockingNoLeaves, ruleMob},
	"witch":            {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"wither":           {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"wither_skeleton":  {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"wolf":             {placeOnGround, hmMotionBlockingNoLeaves, ruleWolf},
	"zoglin":           {placeOnGround, hmMotionBlockingNoLeaves, ruleAnyLightMonster},
	"creaking":         {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"zombie":           {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"zombie_horse":     {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"zombified_piglin": {placeOnGround, hmMotionBlockingNoLeaves, ruleZombifiedPiglin},
	"zombie_villager":  {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"cat":              {placeOnGround, hmMotionBlockingNoLeaves, ruleAnimal},
	"elder_guardian":   {placeInWater, hmMotionBlockingNoLeaves, ruleGuardian},
	"evoker":           {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleMonster},
	"fox":              {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleFox},
	"illusioner":       {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleMonster},
	"panda":            {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleAnimal},
	"phantom":          {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleMob},
	"ravager":          {placeOnGround, hmMotionBlockingNoLeaves, ruleMonster},
	"shulker":          {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleMob},
	"trader_llama":     {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleAnimal},
	"vex":              {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleMonster},
	"vindicator":       {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleMonster},
	"wandering_trader": {placeOnGround, hmMotionBlockingNoLeaves, ruleMob},
	"warden":           {placeNoRestrictions, hmMotionBlockingNoLeaves, ruleMonster},
}

// spawnRuleTags are the predicates that are "block below in tag X and
// isBrightEnoughToSpawn" (Armadillo, Camel, Frog, Goat, MushroomCow,
// Parrot, Rabbit, Wolf, Fox, and Animal's animals_spawnable_on).
var spawnRuleTags = map[spawnRule]string{
	ruleAnimal:    "animals_spawnable_on",
	ruleArmadillo: "armadillo_spawnable_on",
	ruleCamel:     "camels_spawnable_on",
	ruleFrog:      "frogs_spawnable_on",
	ruleGoat:      "goats_spawnable_on",
	ruleMushroom:  "mooshrooms_spawnable_on",
	ruleParrot:    "parrots_spawnable_on",
	ruleRabbit:    "rabbits_spawnable_on",
	ruleWolf:      "wolves_spawnable_on",
	ruleFox:       "foxes_spawnable_on",
}

// walkTargetKind is the getWalkTargetValue override the
// PathfinderMob.checkSpawnRules (walk target >= 0) reads; the zero
// value is a Mob that is no PathfinderMob (ghast, slime, phantom,
// bat — Mob.checkSpawnRules is always true).
type walkTargetKind uint8

const (
	walkNone     walkTargetKind = iota // not a PathfinderMob: always passes
	walkZero                           // PathfinderMob default 0.0 (or an override returning 0)
	walkMonster                        // Monster: 0.5 - lightMagic
	walkAnimal                         // Animal: grass below 10, else lightMagic - 0.5
	walkMushroom                       // MushroomCow: mycelium below 10, else lightMagic - 0.5
	walkHoglin                         // Hoglin: crimson nylium 10, else 0 (repellent ignored)
	walkStrider                        // Strider: lava 10, else 0
	walkTurtle                         // Turtle: water 10, sand below 10, else lightMagic - 0.5
)

// spawnWalkTarget maps the spawnable types to their getWalkTargetValue
// family. Monster subclasses inherit walkMonster; EnderMan, Axolotl
// and AbstractNautilus return 0; Animal subclasses walkAnimal.
var spawnWalkTarget = map[string]walkTargetKind{
	// Monster family.
	"zombie": walkMonster, "husk": walkMonster, "drowned": walkMonster,
	"zombie_villager": walkMonster, "zombified_piglin": walkMonster,
	"skeleton": walkMonster, "stray": walkMonster, "bogged": walkMonster,
	"parched": walkMonster, "wither_skeleton": walkMonster,
	"creeper": walkMonster, "spider": walkMonster, "cave_spider": walkMonster,
	"witch": walkMonster, "blaze": walkMonster, "silverfish": walkMonster,
	"endermite": walkMonster, "giant": walkMonster, "guardian": walkMonster,
	"elder_guardian": walkMonster, "pillager": walkMonster, "vindicator": walkMonster,
	"evoker": walkMonster, "illusioner": walkMonster, "ravager": walkMonster,
	"vex": walkMonster, "zoglin": walkMonster, "piglin": walkMonster,
	"piglin_brute": walkMonster, "breeze": walkMonster, "creaking": walkMonster,
	"warden": walkMonster, "strider": walkStrider,
	"enderman": walkZero,
	// Animal family.
	"pig": walkAnimal, "cow": walkAnimal, "sheep": walkAnimal, "chicken": walkAnimal,
	"rabbit": walkAnimal, "horse": walkAnimal, "donkey": walkAnimal, "mule": walkAnimal,
	"llama": walkAnimal, "trader_llama": walkAnimal, "skeleton_horse": walkAnimal,
	"zombie_horse": walkAnimal, "camel": walkAnimal, "camel_husk": walkAnimal,
	"wolf": walkAnimal, "cat": walkAnimal, "ocelot": walkAnimal, "parrot": walkAnimal,
	"fox": walkAnimal, "panda": walkAnimal, "polar_bear": walkAnimal, "goat": walkAnimal,
	"frog": walkAnimal, "armadillo": walkAnimal, "bee": walkAnimal, "sniffer": walkAnimal,
	"happy_ghast": walkAnimal, "mooshroom": walkMushroom, "hoglin": walkHoglin,
	"turtle": walkTurtle, "axolotl": walkZero, "nautilus": walkZero,
	"zombie_nautilus": walkZero,
	// Other PathfinderMobs (water creatures, villagers, golems).
	"squid": walkZero, "glow_squid": walkZero, "dolphin": walkZero, "cod": walkZero,
	"salmon": walkZero, "pufferfish": walkZero, "tropical_fish": walkZero,
	"tadpole": walkZero, "villager": walkZero, "wandering_trader": walkZero,
	"iron_golem": walkZero, "snow_golem": walkZero, "allay": walkZero,
}

// waterObstructionTypes override checkSpawnObstruction with a bare
// level.isUnobstructed(this) — no liquid test (WaterAnimal,
// AgeableWaterCreature, Drowned, Axolotl, AbstractNautilus, Strider).
// Ocelot adds sea-level/grass-or-leaves requirements.
var waterObstructionTypes = map[string]bool{
	"cod": true, "salmon": true, "pufferfish": true, "tropical_fish": true,
	"squid": true, "glow_squid": true, "dolphin": true, "drowned": true,
	"axolotl": true, "nautilus": true, "zombie_nautilus": true, "strider": true,
	"tadpole": true,
}

// spawnClusterSize is getMaxSpawnClusterSize: 4 by default, Wolf 8,
// AbstractHorse 6, Ghast/HappyGhast 1, AbstractFish 8, schooling fish
// their getMaxSchoolSize (salmon 5, the rest the AbstractFish 8).
var spawnClusterSize = map[string]int{
	"wolf": 8, "horse": 6, "donkey": 6, "mule": 6, "llama": 6, "trader_llama": 6,
	"skeleton_horse": 6, "zombie_horse": 6, "camel": 6, "camel_husk": 6,
	"ghast": 1, "happy_ghast": 1, "cod": 8, "salmon": 5, "pufferfish": 8,
	"tropical_fish": 8,
}

func maxSpawnClusterSize(name string) int {
	if n, ok := spawnClusterSize[name]; ok {
		return n
	}
	return 4
}

// isSignalSourceBlock is Block.isSignalSource for the overriding
// families (pressure plates, buttons, diodes, lever, observer, ...).
func isSignalSourceBlock(name string) bool {
	switch name {
	case "daylight_detector", "detector_rail", "repeater", "comparator",
		"jukebox", "lectern", "lever", "observer", "redstone_block",
		"redstone_wire", "redstone_torch", "redstone_wall_torch",
		"sculk_sensor", "calibrated_sculk_sensor", "target",
		"trapped_chest", "tripwire_hook":
		return true
	}
	return hasSuffixAny(name, "_pressure_plate", "_button", "lightning_rod")
}

func hasSuffixAny(s string, suf ...string) bool {
	for _, x := range suf {
		if len(s) >= len(x) && s[len(s)-len(x):] == x {
			return true
		}
	}
	return false
}
