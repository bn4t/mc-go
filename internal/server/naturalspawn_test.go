package server

import (
	"testing"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Unit pins for the measured NaturalSpawner constants and the spawn
// placement predicates (see naturalspawn*.go for the javap sources).

func TestMobCategoryConstants(t *testing.T) {
	want := map[mobCategory][5]any{
		catMonster:                  {"monster", 70, false, false, 128},
		catCreature:                 {"creature", 10, true, true, 128},
		catAmbient:                  {"ambient", 15, true, false, 128},
		catAxolotls:                 {"axolotls", 5, true, false, 128},
		catUndergroundWaterCreature: {"underground_water_creature", 5, true, false, 128},
		catWaterCreature:            {"water_creature", 5, true, false, 128},
		catWaterAmbient:             {"water_ambient", 20, true, false, 64},
	}
	for c, w := range want {
		i := mobCategoryInfo[c]
		got := [5]any{i.name, i.max, i.friendly, i.persistent, i.despawn}
		if got != w {
			t.Errorf("category %d = %v, want %v", c, got, w)
		}
	}
	// The global cap: max * spawnableChunkCount / 17². A lone player
	// tracks 289 chunks, so the monster cap is the bare 70.
	if got := mobCategoryInfo[catMonster].max * 289 / 289; got != 70 {
		t.Errorf("lone-player monster cap = %d", got)
	}
	if got := mobCategoryInfo[catCreature].max * 578 / 289; got != 20 {
		t.Errorf("two-isolated-players creature cap = %d", got)
	}
	if entityCategoryOf("tadpole") != catCreature || entityCategoryOf("villager") != catMisc ||
		entityCategoryOf("cod") != catWaterAmbient || entityCategoryOf("glow_squid") != catUndergroundWaterCreature {
		t.Error("entity category table drifted")
	}
}

func TestSpawnSkyDarken(t *testing.T) {
	s := New("unused")
	d := s.dims[dimOverworld]
	cases := []struct {
		day  int64
		want int
	}{{6000, 0}, {18000, 11}, {0, 0}, {13000, 6}, {12000, 0}, {13670, 11}, {23000, 6}}
	for _, c := range cases {
		s.clock.mu.Lock()
		s.clock.day = c.day
		s.clock.mu.Unlock()
		if got := s.spawnSkyDarken(d); got != c.want {
			t.Errorf("skyDarken at day %d = %d, want %d", c.day, got, c.want)
		}
	}
	s.clock.mu.Lock()
	s.clock.day = 6000
	s.clock.mu.Unlock()
	s.weatherRain.Store(1000)
	if got := s.spawnSkyDarken(d); got != 3 {
		t.Errorf("full-rain noon darken = %d, want 3", got)
	}
	s.weatherThundLv.Store(1000)
	if got := s.spawnSkyDarken(d); got != 5 {
		t.Errorf("full-thunder noon darken = %d, want 5", got)
	}
	if got := s.spawnSkyDarken(s.dims[dimNether]); got != 11 {
		t.Errorf("nether darken = %d, want 11", got)
	}
}

func TestBiomeSpawnSettings(t *testing.T) {
	s := New("unused")
	p := s.biomeSpawns("minecraft:plains")
	if p == nil {
		t.Fatal("plains has no spawn settings")
	}
	if p.creatureProb != 0.1 {
		t.Errorf("plains creature probability = %v, want the 0.1 default", p.creatureProb)
	}
	found := false
	for _, e := range p.lists[catMonster] {
		if e.typ == "zombie" && e.weight == 90 && e.min == 4 && e.max == 4 {
			found = true
		}
	}
	if !found {
		t.Errorf("plains monsters lack zombie 90x4: %v", p.lists[catMonster])
	}
	if !s.biomeSpawns("the_void").empty(catMonster) || !s.biomeSpawns("the_void").empty(catCreature) {
		t.Error("the_void must spawn nothing")
	}
	ssv := s.biomeSpawns("soul_sand_valley")
	if c, ok := ssv.costFor("ghast"); !ok || c != [2]float64{0.7, 0.15} {
		t.Errorf("soul_sand_valley ghast cost = %v %v", c, ok)
	}
	if !s.biomeHasTag("more_frequent_drowned_spawns", "river") || s.biomeHasTag("more_frequent_drowned_spawns", "plains") {
		t.Error("nested biome tag #is_river not resolved")
	}
	if !s.biomeHasTag("allows_surface_slime_spawns", "swamp") {
		t.Error("swamp allows surface slimes")
	}
}

func TestWeightedPick(t *testing.T) {
	b := &biomeSpawnSettings{}
	b.lists[catMonster] = []spawnerEntry{{"a", 1, 1, 1}, {"b", 3, 1, 1}}
	b.totals[catMonster] = 4
	for roll, want := range []string{"a", "b", "b", "b"} {
		e, ok := b.pick(catMonster, fixedRoll(roll))
		if !ok || e.typ != want {
			t.Errorf("roll %d picked %v, want %s", roll, e, want)
		}
	}
}

type fixedRoll int32

func (f fixedRoll) NextIntN(int32) int32 { return int32(f) }
func (f fixedRoll) NextFloat() float32   { return 0 }
func (f fixedRoll) NextBool() bool       { return false }

func TestSpawnPlacementBlocks(t *testing.T) {
	st := world.BlockStateMust
	slab := func(typ string) world.Block {
		b, _ := world.WithProperty(st("stone_slab"), "type", typ)
		return b
	}
	cases := []struct {
		name string
		b    world.Block
		typ  string
		want bool
	}{
		{"grass", st("grass_block"), "zombie", true},
		{"stone", st("stone"), "zombie", true},
		{"glass", st("glass"), "zombie", false},
		{"bedrock", st("bedrock"), "zombie", false},
		{"leaves zombie", st("oak_leaves"), "zombie", false},
		{"leaves parrot", st("oak_leaves"), "parrot", true},
		{"ice zombie", st("ice"), "zombie", false},
		{"ice bear", st("ice"), "polar_bear", true},
		{"magma zombie", st("magma_block"), "zombie", false},
		{"magma blaze", st("magma_block"), "blaze", true},
		{"glowstone", st("glowstone"), "zombie", false},
		{"bottom slab", slab("bottom"), "zombie", false},
		{"top slab", slab("top"), "zombie", true},
		{"soul sand", st("soul_sand"), "zombie", true},
		{"oak trapdoor", st("oak_trapdoor"), "zombie", false},
	}
	for _, c := range cases {
		if got := blockValidSpawn(c.b, c.typ); got != c.want {
			t.Errorf("isValidSpawn(%s, %s) = %v, want %v", c.name, c.typ, got, c.want)
		}
	}
	empty := []struct {
		name string
		b    world.Block
		typ  string
		want bool
	}{
		{"air", world.Air, "zombie", true},
		{"short grass", st("short_grass"), "zombie", true},
		{"rail", st("rail"), "zombie", false},
		{"water", st("water"), "zombie", false},
		{"stone", st("stone"), "zombie", false},
		{"button", st("oak_button"), "zombie", false},
		{"fire zombie", st("fire"), "zombie", false},
		{"fire blaze", st("fire"), "blaze", true},
		{"berry fox", st("sweet_berry_bush"), "fox", true},
		{"berry rabbit", st("sweet_berry_bush"), "rabbit", false},
		{"powder stray", st("powder_snow"), "stray", true},
		{"light", st("light"), "zombie", true},
	}
	for _, c := range empty {
		if got := validEmptySpawnBlock(c.b, c.typ); got != c.want {
			t.Errorf("isValidEmptySpawnBlock(%s, %s) = %v, want %v", c.name, c.typ, got, c.want)
		}
	}
}

func TestSlimeChunkDensity(t *testing.T) {
	n := 0
	for x := -50; x < 50; x++ {
		for z := -50; z < 50; z++ {
			if isSlimeChunk(12345, x, z) {
				n++
			}
		}
	}
	// One chunk in ten.
	if n < 900 || n > 1100 {
		t.Errorf("slime chunks in 10000 = %d, want ~1000", n)
	}
	if surfaceSlimeChance(0) != 0.5 || surfaceSlimeChance(96000) != 0 || surfaceSlimeChance(191999) != 0.375 {
		t.Error("moon phase slime chance drifted")
	}
}

// spawnArena builds a sealed stone box (floor y=-61, roof y=-57,
// interior x/z in [x0+1, x0+14]) over one chunk and marks the chunk's
// cells plains.
func spawnArena(s *Server, d *dimension, cx, cz int, light world.Block) {
	x0, z0 := cx*16, cz*16
	stone := world.BlockStateMust("stone")
	for x := x0; x < x0+16; x++ {
		for z := z0; z < z0+16; z++ {
			for y := -61; y <= -57; y++ {
				edge := x == x0 || x == x0+15 || z == z0 || z == z0+15 || y == -61 || y == -57
				b := world.Air
				if edge {
					b = stone
				} else if light != world.Air {
					b = light
				}
				s.setSilentBlock(d, world.Pos{X: x, Y: y, Z: z}, b)
			}
		}
	}
	d.biomesMu.Lock()
	if d.biomes == nil {
		d.biomes = map[[3]int]string{}
	}
	for qx := cx*4 - 4; qx < cx*4+8; qx++ {
		for qz := cz*4 - 4; qz < cz*4+8; qz++ {
			for qy := -64 >> 2; qy < 0; qy++ {
				d.biomes[[3]int{qx, qy, qz}] = "minecraft:plains"
			}
		}
	}
	d.biomesMu.Unlock()
}

func countMonsters(s *Server, cx, cz int) int {
	n := 0
	for _, e := range s.entities.All() {
		name, _ := EntityTypeName(e.Type)
		if entityCategoryOf(name) == catMonster && int(e.X)>>4 == cx && int(e.Z)>>4 == cz {
			n++
		}
	}
	return n
}

// TestNaturalSpawnDarkArena drives the per-tick spawner over a sealed
// plains arena: monsters spawn in the dark, never at block light 1,
// never with spawn_monsters off, and the count stops at the 70 cap.
func TestNaturalSpawnDarkArena(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeCreative
	s.WorldSeed = 1
	s.SimulationDistance = 8
	d := s.dims[dimOverworld]
	sess := placeSession(t, s, "Spawner", 0.5, -60, 0.5)
	sess.spawned.Store(true)
	s.clock.mu.Lock()
	s.clock.day = 6000
	s.clock.mu.Unlock()
	spawnArena(s, d, 3, 0, world.Air)
	lit, _ := world.WithProperty(world.BlockStateMust("light"), "level", "1")
	spawnArena(s, d, -4, 0, lit)
	for _, p := range []world.Pos{{X: 3*16 + 5, Y: -60, Z: 5}, {X: -4*16 + 5, Y: -60, Z: 5}} {
		sky, blk := s.spawnLight(d, p)
		t.Logf("light at %v: sky %d block %d", p, sky, blk)
	}

	setRuleForTest(s, "minecraft:spawn_monsters", "false")
	for i := 0; i < 300; i++ {
		s.naturalSpawnDim(d, s.gameRuleBool("minecraft:spawn_monsters"), false)
	}
	if n := countMonsters(s, 3, 0); n != 0 {
		t.Fatalf("spawn_monsters=false spawned %d monsters", n)
	}
	setRuleForTest(s, "minecraft:spawn_monsters", "true")
	for i := 0; i < 3000; i++ {
		s.naturalSpawnDim(d, true, false)
	}
	dark, litN := countMonsters(s, 3, 0), countMonsters(s, -4, 0)
	t.Logf("dark arena %d monsters, lit arena %d", dark, litN)
	if litN != 0 {
		t.Errorf("block light 1 arena spawned %d monsters", litN)
	}
	// Unticked mobs pile up and obstruct the 14x14 floor before the
	// cap is reached; the count can never pass cap + one chunk call.
	if dark < 20 || dark > 73 {
		t.Errorf("dark arena holds %d monsters, want 20..73", dark)
	}
	types := map[string]int{}
	for _, e := range s.entities.All() {
		name, _ := EntityTypeName(e.Type)
		types[name]++
	}
	t.Logf("spawned types: %v", types)
	for name := range types {
		switch name {
		case "spider", "zombie", "zombie_villager", "skeleton", "creeper", "slime", "enderman", "witch", "zombie_horse", "bat", "player":
		default:
			t.Errorf("unexpected spawn %s for plains", name)
		}
	}
	_ = worldgen.IsAir
}

func setRuleForTest(s *Server, k, v string) {
	s.rulesMu.Lock()
	s.gameRules[k] = v
	s.rulesMu.Unlock()
}

// TestNaturalSpawnMonsterCap pins the global/local cap: 70 counted
// (non-persistent) monsters anywhere in the lone player's 289 chunks
// stop every monster spawn; 69 leave room for one chunk call (up to
// getMaxSpawnClusterSize more). Persistent mobs never count.
func TestNaturalSpawnMonsterCap(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeCreative
	s.WorldSeed = 1
	s.SimulationDistance = 8
	d := s.dims[dimOverworld]
	sess := placeSession(t, s, "Capper", 0.5, -60, 0.5)
	sess.spawned.Store(true)
	s.clock.mu.Lock()
	s.clock.day = 6000
	s.clock.mu.Unlock()
	spawnArena(s, d, 3, 0, world.Air)
	var filler []*Entity
	for i := 0; i < 70; i++ {
		e, ok := s.summonMob(d, "zombie", float64(i%10)+0.5, -60, float64(-20-i/10)+0.5, true)
		if !ok {
			t.Fatal("summon failed")
		}
		filler = append(filler, e)
	}
	// A persistent extra changes nothing.
	if e, ok := s.summonMob(d, "zombie", 0.5, -60, -40.5, true); ok {
		e.PersistenceRequired = true
	}
	for i := 0; i < 600; i++ {
		s.naturalSpawnDim(d, true, false)
	}
	if n := countMonsters(s, 3, 0); n != 0 {
		t.Fatalf("at the 70 cap the arena gained %d monsters", n)
	}
	s.entities.Remove(filler[0].ID)
	for i := 0; i < 2000 && countMonsters(s, 3, 0) == 0; i++ {
		s.naturalSpawnDim(d, true, false)
	}
	n := countMonsters(s, 3, 0)
	if n == 0 {
		t.Fatal("69 counted monsters still blocked spawning")
	}
	for i := 0; i < 600; i++ {
		s.naturalSpawnDim(d, true, false)
	}
	if m := countMonsters(s, 3, 0); m > 4 {
		t.Errorf("one free cap slot let %d monsters in (first call %d), want <= 4", m, n)
	}
}
