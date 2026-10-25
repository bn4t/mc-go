package loot

import (
	"encoding/json"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"mc-go/internal/data"
)

// The tests come in two halves. The first rolls the real tables from
// the bundled data pack and pins the drops a player would actually see
// (cobblestone from stone, porkchop from a pig, and so on), plus one
// test that rolls every table in the pack looking for a panic. The
// second half registers synthetic tables, which is the only way to
// exercise the constructs the vanilla pack happens not to use:
// bonus_rolls, sequence, group, weighted draws with quality, and the
// unknown-construct policy.
//
// Every roll here is deterministic: either a fake RNG that returns a
// fixed sequence of floats, or math/rand with a pinned seed.

// ---------------------------------------------------------------------
// deterministic randomness

// fakeRNG returns a fixed sequence of floats, cycling, and derives
// Intn from the same sequence so one list drives both draw paths. A
// value below 1/n is what makes Intn(n) return 0, which is how the
// tests force a particular branch.
type fakeRNG struct {
	vals []float64
	i    int
}

func fixedRNG(vals ...float64) *fakeRNG {
	if len(vals) == 0 {
		vals = []float64{0}
	}
	return &fakeRNG{vals: vals}
}

func (f *fakeRNG) Float64() float64 {
	v := f.vals[f.i%len(f.vals)]
	f.i++
	return v
}

func (f *fakeRNG) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	i := int(f.Float64() * float64(n))
	if i >= n {
		i = n - 1
	}
	return i
}

// seededRNG is the distribution-test source: math/rand with a pinned
// seed, so a failure is always reproducible.
func seededRNG(seed int64) RNG { return rand.New(rand.NewSource(seed)) }

// register parses a synthetic table from JSON. Writing the tests'
// tables as JSON keeps them legible next to the data pack's own.
func register(t *testing.T, name, def string) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(def), &raw); err != nil {
		t.Fatalf("bad test table %s: %v", name, err)
	}
	RegisterTable(name, raw)
}

// itemTag is a sorted copy of an item tag's members, so a test can
// compare against it without touching the registry's own slice.
func itemTag(t *testing.T, tag string) []string {
	t.Helper()
	members := data.ItemTags[tag]
	if len(members) == 0 {
		t.Fatalf("item tag %s is empty", tag)
	}
	out := append([]string(nil), members...)
	sort.Strings(out)
	return out
}

// lootTableDef hands back the data pack's own definition of a table, so
// a test that overrides one can put the real one back.
func lootTableDef(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, ok := data.LootTables()[name]
	if !ok {
		t.Fatalf("the data pack has no loot table %s", name)
	}
	return raw
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// items lists the item names of a roll, sorted, which is what most
// assertions want to compare against.
func items(stacks []Stack) []string {
	out := make([]string, 0, len(stacks))
	for _, s := range stacks {
		out = append(out, s.Item)
	}
	sort.Strings(out)
	return out
}

// total is the summed count of a roll.
func total(stacks []Stack) int32 {
	var n int32
	for _, s := range stacks {
		n += s.Count
	}
	return n
}

// ---------------------------------------------------------------------
// real tables

// TestStoneWithoutToolDropsCobblestone pins the alternatives rule: the
// stone entry is gated on silk touch, so a bare break falls through to
// cobblestone, which is gated on surviving the explosion.
func TestStoneWithoutToolDropsCobblestone(t *testing.T) {
	got := Roll("blocks/stone", &Context{RNG: fixedRNG(0.5)})
	if len(got) != 1 || got[0].Item != "cobblestone" || got[0].Count != 1 {
		t.Fatalf("blocks/stone without a tool = %v, want one cobblestone", got)
	}
}

func TestStoneWithSilkTouchDropsStone(t *testing.T) {
	ctx := &Context{
		RNG:          fixedRNG(0.5),
		Tool:         "diamond_pickaxe",
		Enchantments: map[string]int{"silk_touch": 1},
	}
	got := Roll("blocks/stone", ctx)
	if len(got) != 1 || got[0].Item != "stone" || got[0].Count != 1 {
		t.Fatalf("blocks/stone with silk touch = %v, want one stone", got)
	}
}

// TestStoneSilkTouchLevelZeroIsNotSilkTouch checks the levels predicate
// rather than the enchantment's mere presence.
func TestStoneSilkTouchLevelZeroIsNotSilkTouch(t *testing.T) {
	ctx := &Context{
		RNG:          fixedRNG(0.5),
		Tool:         "diamond_pickaxe",
		Enchantments: map[string]int{"silk_touch": 0},
	}
	got := Roll("blocks/stone", ctx)
	if len(got) != 1 || got[0].Item != "cobblestone" {
		t.Fatalf("blocks/stone with silk touch level 0 = %v, want cobblestone", got)
	}
}

// TestGrassBlockDropsDirt is the same alternatives shape with a
// different default drop.
func TestGrassBlockDropsDirt(t *testing.T) {
	for i := 0; i < 50; i++ {
		got := Roll("blocks/grass_block", &Context{RNG: seededRNG(int64(i))})
		if len(got) != 1 || got[0].Item != "dirt" || got[0].Count != 1 {
			t.Fatalf("blocks/grass_block = %v, want one dirt", got)
		}
	}
}

func TestGrassBlockWithSilkTouchDropsGrassBlock(t *testing.T) {
	ctx := &Context{
		RNG:          fixedRNG(0.5),
		Tool:         "diamond_shovel",
		Enchantments: map[string]int{"silk_touch": 1},
	}
	got := Roll("blocks/grass_block", ctx)
	if len(got) != 1 || got[0].Item != "grass_block" {
		t.Fatalf("blocks/grass_block with silk touch = %v, want grass_block", got)
	}
}

func TestCoalOreWithSilkTouchDropsTheOre(t *testing.T) {
	ctx := &Context{
		RNG:          fixedRNG(0.5),
		Tool:         "iron_pickaxe",
		Enchantments: map[string]int{"silk_touch": 1},
	}
	got := Roll("blocks/coal_ore", ctx)
	if len(got) != 1 || got[0].Item != "coal_ore" || got[0].Count != 1 {
		t.Fatalf("blocks/coal_ore with silk touch = %v, want one coal_ore", got)
	}
}

// TestCoalOreFortuneThreeShiftsTheDistribution is the ore_drops check.
// Without fortune every break yields exactly one coal; with fortune III
// the multiplier is max(1, 0..4), so the count reaches 4 sometimes and
// averages 2.2 -- the figure the published ore tables give for fortune
// III coal, and the reason the multiplier's low end is doubled rather
// than dropped.
func TestCoalOreFortuneThreeShiftsTheDistribution(t *testing.T) {
	none := &Context{RNG: seededRNG(7), Tool: "iron_pickaxe"}
	fortune := &Context{
		RNG:          seededRNG(7),
		Tool:         "iron_pickaxe",
		Enchantments: map[string]int{"fortune": 3},
	}

	const trials = 20000
	var plainSum, fortuneSum int
	fortuneMax := 0
	counts := map[int]int{}
	for i := 0; i < trials; i++ {
		got := Roll("blocks/coal_ore", none)
		if len(got) != 1 || got[0].Item != "coal" || got[0].Count != 1 {
			t.Fatalf("blocks/coal_ore without fortune = %v, want one coal", got)
		}
		plainSum += int(got[0].Count)

		got = Roll("blocks/coal_ore", fortune)
		if len(got) != 1 || got[0].Item != "coal" {
			t.Fatalf("blocks/coal_ore with fortune = %v, want one coal stack", got)
		}
		if got[0].Count < 1 {
			t.Fatalf("blocks/coal_ore with fortune rolled %d coal, want at least 1", got[0].Count)
		}
		fortuneSum += int(got[0].Count)
		counts[int(got[0].Count)]++
		if int(got[0].Count) > fortuneMax {
			fortuneMax = int(got[0].Count)
		}
	}

	if plainSum != trials {
		t.Fatalf("coal without fortune totalled %d over %d breaks, want %d", plainSum, trials, trials)
	}
	if fortuneMax != 4 {
		t.Fatalf("coal with fortune III peaked at %d, want 4 (the max multiplier is level+1)", fortuneMax)
	}
	mean := float64(fortuneSum) / trials
	if mean < 2.15 || mean > 2.25 {
		t.Fatalf("coal with fortune III averaged %.3f, want about 2.2 (1,1,2,3,4 over five draws)", mean)
	}
	for _, n := range []int{1, 2, 3, 4} {
		if counts[n] == 0 {
			t.Fatalf("coal with fortune III never rolled %d over %d breaks (histogram %v)", n, trials, counts)
		}
	}
}

// TestPigDropsOneToThreePorkchops checks the number provider used as a
// stack count: uniform 1..3 is inclusive at both ends, so all three
// values must appear and nothing outside them.
func TestPigDropsOneToThreePorkchops(t *testing.T) {
	seen := map[int32]int{}
	for i := 0; i < 300; i++ {
		got := Roll("entities/pig", &Context{RNG: seededRNG(int64(i))})
		if len(got) != 1 || got[0].Item != "porkchop" {
			t.Fatalf("entities/pig = %v, want one porkchop stack", got)
		}
		if got[0].Count < 1 || got[0].Count > 3 {
			t.Fatalf("entities/pig rolled %d porkchops, want 1..3", got[0].Count)
		}
		seen[got[0].Count]++
	}
	for _, n := range []int32{1, 2, 3} {
		if seen[n] == 0 {
			t.Fatalf("entities/pig never rolled %d porkchops over 300 kills (histogram %v)", n, seen)
		}
	}
}

// TestPigOnFireSmeltsThePorkchop covers the furnace_smelt path and the
// entity_properties on-fire flag it hangs off: a pig that dies burning
// drops cooked porkchop.
func TestPigOnFireSmeltsThePorkchop(t *testing.T) {
	ctx := &Context{RNG: fixedRNG(0), EntityOnFire: true, EntityType: "pig"}
	got := Roll("entities/pig", ctx)
	if len(got) != 1 || got[0].Item != "cooked_porkchop" || got[0].Count != 1 {
		t.Fatalf("burning pig = %v, want one cooked_porkchop", got)
	}
}

// TestPigSmeltNeedsProof guards the other direction: without the on-fire
// flag and without a smelting weapon the drop must stay raw, which is
// what the unmodelled attacker-equipment predicate has to fail rather
// than pass.
func TestPigSmeltNeedsProof(t *testing.T) {
	for _, ctx := range []*Context{
		{RNG: fixedRNG(0), EntityType: "pig"},
		{RNG: fixedRNG(0), EntityType: "pig", Tool: "diamond_sword"},
		{RNG: fixedRNG(0), EntityType: "pig", Tool: "diamond_sword", Enchantments: map[string]int{"sharpness": 5}},
	} {
		got := Roll("entities/pig", ctx)
		if len(got) != 1 || got[0].Item != "porkchop" {
			t.Fatalf("pig killed with %+v = %v, want raw porkchop", ctx, got)
		}
	}
}

// TestPigSmeltWithFireAspect covers the other half of that any_of: a
// fire aspect weapon smelts the drop even though the pig died of the
// sword. The enchantment tag is the reason the interpreter carries a
// small built-in tag table.
func TestPigSmeltWithFireAspect(t *testing.T) {
	ctx := &Context{
		RNG:          fixedRNG(0),
		EntityType:   "pig",
		Tool:         "diamond_sword",
		Enchantments: map[string]int{"fire_aspect": 2},
	}
	got := Roll("entities/pig", ctx)
	if len(got) != 1 || got[0].Item != "cooked_porkchop" {
		t.Fatalf("pig killed with fire aspect = %v, want cooked_porkchop", got)
	}
}

// TestZombieLootingMatchesThePublishedDrops pins the subtlest formula
// in the package against numbers published for the real game: a zombie
// drops 0-2 rotten flesh (a third of the time nothing at all, average
// one), and with looting III that becomes 0-5 with a 17/18 drop chance
// and an average of 2.5. Reproducing those three numbers at once is
// what confirms that enchanted_count_increase multiplies the rolled
// per-level count by the level and rounds it with floor(x + 0.5),
// rather than summing a whole extra item per level.
func TestZombieLootingMatchesThePublishedDrops(t *testing.T) {
	const trials = 20000
	plain := &Context{RNG: seededRNG(1), KilledByPlayer: true}
	looting := &Context{
		RNG:            seededRNG(1),
		KilledByPlayer: true,
		Enchantments:   map[string]int{"looting": 3},
	}
	var plainSum, lootingSum, plainDrops, lootingDrops int
	var plainMax, lootingMax int32
	for i := 0; i < trials; i++ {
		got := Roll("entities/zombie", plain)
		c := fleshCount(got)
		plainSum += int(c)
		if c > 0 {
			plainDrops++
		}
		if c > plainMax {
			plainMax = c
		}

		got = Roll("entities/zombie", looting)
		c = fleshCount(got)
		lootingSum += int(c)
		if c > 0 {
			lootingDrops++
		}
		if c > lootingMax {
			lootingMax = c
		}
	}
	if plainMax != 2 {
		t.Fatalf("plain zombie dropped up to %d rotten flesh, want 2", plainMax)
	}
	if lootingMax != 5 {
		t.Fatalf("looting III zombie dropped up to %d rotten flesh, want 5", lootingMax)
	}
	if mean := float64(plainSum) / trials; mean < 0.95 || mean > 1.05 {
		t.Fatalf("plain zombie averaged %.3f rotten flesh, want 1.00", mean)
	}
	if mean := float64(lootingSum) / trials; mean < 2.45 || mean > 2.55 {
		t.Fatalf("looting III zombie averaged %.3f rotten flesh, want 2.50", mean)
	}
	if p := float64(plainDrops) / trials; p < 0.64 || p > 0.69 {
		t.Fatalf("plain zombie dropped flesh %.3f of the time, want 2/3", p)
	}
	if p := float64(lootingDrops) / trials; p < 0.93 || p > 0.96 {
		t.Fatalf("looting III zombie dropped flesh %.3f of the time, want 17/18", p)
	}
}

// fleshCount is the rotten flesh count of a zombie roll.
func fleshCount(stacks []Stack) int32 {
	for _, s := range stacks {
		if s.Item == "rotten_flesh" {
			return s.Count
		}
	}
	return 0
}

// TestGravelFlintChance is a table_bonus test on a real table: gravel
// drops flint about a tenth of the time, and the chances array runs up
// to 1.0 at fortune III, where flint is guaranteed.
func TestGravelFlintChance(t *testing.T) {
	const trials = 20000
	plain := &Context{RNG: seededRNG(2)}
	fortune := &Context{RNG: seededRNG(2), Tool: "iron_shovel", Enchantments: map[string]int{"fortune": 3}}
	var plainFlint, fortuneFlint int
	for i := 0; i < trials; i++ {
		if got := Roll("blocks/gravel", plain); len(got) == 1 && got[0].Item == "flint" {
			plainFlint++
		}
		if got := Roll("blocks/gravel", fortune); len(got) == 1 && got[0].Item == "flint" {
			fortuneFlint++
		}
	}
	if p := float64(plainFlint) / trials; p < 0.08 || p > 0.12 {
		t.Fatalf("gravel dropped flint %.3f of the time, want about 0.1", p)
	}
	if fortuneFlint != trials {
		t.Fatalf("gravel with fortune III dropped flint %d times out of %d, want all of them", fortuneFlint, trials)
	}
}

// TestSimpleDungeonRollsManyStacks is the chest smoke test: the table
// has three pools, a uniform rolls provider and a dozen weighted
// entries, and every trial must produce a plausible pile.
func TestSimpleDungeonRollsManyStacks(t *testing.T) {
	rng := seededRNG(11)
	for i := 0; i < 200; i++ {
		got := Roll("chests/simple_dungeon", &Context{RNG: rng, Luck: -2})
		if len(got) == 0 {
			t.Fatalf("chests/simple_dungeon trial %d rolled nothing", i)
		}
		if len(got) > 12 {
			t.Fatalf("chests/simple_dungeon trial %d rolled %d stacks, more than any pool could produce", i, len(got))
		}
		for _, s := range got {
			if s.Item == "" {
				t.Fatalf("chests/simple_dungeon trial %d produced an empty item name: %v", i, got)
			}
			if s.Count < 1 {
				t.Fatalf("chests/simple_dungeon trial %d produced a non-positive count: %v", i, got)
			}
		}
	}
}

// TestEveryVanillaTableRolls walks the whole data pack. It is the drift
// alarm: every condition, function, entry type and number provider the
// pack uses has to survive being rolled, twice, under contexts chosen
// to light up both the plain and the enchanted paths, and nothing may
// come back with an empty name or a zero count.
func TestEveryVanillaTableRolls(t *testing.T) {
	names := Tables()
	if len(names) < 1300 {
		t.Fatalf("the data pack exposed only %d loot tables", len(names))
	}
	plain := &Context{RNG: seededRNG(3)}
	rich := &Context{
		RNG:             seededRNG(5),
		Luck:            2,
		Tool:            "netherite_pickaxe",
		ToolTags:        []string{"minecraft:cluster_max_harvestables"},
		Enchantments:    map[string]int{"silk_touch": 1, "fortune": 3, "looting": 3, "fire_aspect": 2},
		Block:           "stone",
		BlockProps:      map[string]string{"type": "double", "half": "upper", "age": "3"},
		ExplosionRadius: 3,
		KilledByPlayer:  true,
		EntityOnFire:    true,
		EntityType:      "pig",
		AttackerType:    "player",
	}
	for _, name := range names {
		for _, ctx := range []*Context{plain, rich} {
			got := Roll(name, ctx)
			for _, s := range got {
				if s.Item == "" || s.Count < 1 {
					t.Fatalf("%s rolled %+v", name, s)
				}
			}
		}
	}
}

// TestSkippedDiagnosticsAreReported checks that the drift counter is
// wired up: rolling the whole pack must leave a record of the
// constructs this interpreter does not model.
func TestSkippedDiagnosticsAreReported(t *testing.T) {
	ResetSkipped()
	Roll("blocks/stone", &Context{RNG: fixedRNG(0)})
	if got := Skipped(); len(got) != 0 {
		t.Fatalf("a plain stone break skipped %v, want nothing", got)
	}
	// A chest table is full of enchant_randomly and set_potion, which
	// are component-side effects we do not model yet. One draw rarely
	// picks the enchanted-book entry, so roll a handful of chests.
	rng := seededRNG(4)
	for i := 0; i < 50; i++ {
		Roll("chests/simple_dungeon", &Context{RNG: rng})
	}
	got := Skipped()
	if got["unmodelled-function:minecraft:enchant_randomly"] == 0 {
		t.Fatalf("diagnostics after a dungeon chest = %v, want the component functions counted", got)
	}
}

// TestConcurrentRoll is the safety net for the shared caches and the
// diagnostics counter: worlds tick from several goroutines and the
// package promises Roll is safe from all of them. Run with -race for
// the real check.
func TestConcurrentRoll(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := seededRNG(seed)
			for i := 0; i < 100; i++ {
				if got := Roll("entities/pig", &Context{RNG: rng}); len(got) == 0 {
					t.Errorf("concurrent pig roll came back empty")
					return
				}
				if got := Roll("chests/simple_dungeon", &Context{RNG: rng}); len(got) == 0 {
					t.Errorf("concurrent dungeon roll came back empty")
					return
				}
			}
		}(int64(g))
	}
	wg.Wait()
}

// TestRollUnknownTableIsEmpty checks the missing-table path: no panic,
// no stacks, and a diagnostic.
func TestRollUnknownTableIsEmpty(t *testing.T) {
	ResetSkipped()
	if got := Roll("blocks/not_a_table", &Context{RNG: fixedRNG(0)}); got != nil {
		t.Fatalf("unknown table rolled %v, want nothing", got)
	}
	if got := Roll("minecraft:blocks/stone.json", nil); len(got) != 1 {
		t.Fatalf("a namespaced name with .json rolled %v, want the stone table", got)
	}
	if Skipped()["missing-table"] == 0 {
		t.Fatal("rolling a missing table was not recorded in the diagnostics")
	}
}

// TestTablesListsTheDataPack checks the diagnostics accessor can see
// the real tables.
func TestTablesListsTheDataPack(t *testing.T) {
	names := Tables()
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	for _, want := range []string{"blocks/stone", "entities/pig", "chests/simple_dungeon"} {
		if !found[want] {
			t.Fatalf("Tables() does not list %s", want)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Fatal("Tables() is not sorted")
	}
}

// TestFurnaceSmeltUsesTheCookingRecipes checks the item swap directly,
// including that a block with a smelting recipe is recognised.
func TestFurnaceSmeltUsesTheCookingRecipes(t *testing.T) {
	if got, ok := cookingResult("porkchop"); !ok || got != "cooked_porkchop" {
		t.Fatalf("cookingResult(porkchop) = %q, %v", got, ok)
	}
	if got, ok := cookingResult("cobblestone"); !ok || got != "stone" {
		t.Fatalf("cookingResult(cobblestone) = %q, %v", got, ok)
	}
	if _, ok := cookingResult("diamond_pickaxe"); ok {
		t.Fatal("cookingResult found a recipe for a pickaxe")
	}
	if len(data.Recipes) == 0 {
		t.Fatal("the recipe registry is empty")
	}
}
