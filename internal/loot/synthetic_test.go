package loot

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// The synthetic tables below are the constructs the vanilla data pack
// happens not to use (bonus_rolls, quality, sequence, group, an unknown
// condition) plus the ones that are easier to pin exactly than by
// searching the pack for a table that uses them.

func TestWeightedEntries(t *testing.T) {
	register(t, "test/weighted", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "weight": 3},
			{"type": "minecraft:item", "name": "minecraft:stick", "weight": 1}
		]}]
	}`)
	// The weight total is 4, so a draw below 0.25 lands on the apple and
	// one at 0.75 or above skips past its three weight points.
	if got := items(Roll("test/weighted", &Context{RNG: fixedRNG(0.1)})); !reflect.DeepEqual(got, []string{"apple"}) {
		t.Fatalf("low draw = %v, want the weight-3 entry", got)
	}
	if got := items(Roll("test/weighted", &Context{RNG: fixedRNG(0.8)})); !reflect.DeepEqual(got, []string{"stick"}) {
		t.Fatalf("high draw = %v, want the weight-1 entry", got)
	}
}

func TestQualityAndLuckShiftTheWeights(t *testing.T) {
	register(t, "test/quality", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "weight": 1},
			{"type": "minecraft:item", "name": "minecraft:stick", "weight": 1, "quality": 2}
		]}]
	}`)
	// Without luck the two entries are even: a 0.3 draw lands on the
	// first. With one point of luck the stick gains quality*luck = 2
	// weight, so the same draw now falls past the apple.
	if got := items(Roll("test/quality", &Context{RNG: fixedRNG(0.3)})); !reflect.DeepEqual(got, []string{"apple"}) {
		t.Fatalf("no luck = %v, want the apple", got)
	}
	if got := items(Roll("test/quality", &Context{RNG: fixedRNG(0.3), Luck: 1})); !reflect.DeepEqual(got, []string{"stick"}) {
		t.Fatalf("luck 1 = %v, want the quality entry", got)
	}
}

func TestNegativeQualityCanSwitchAnEntryOff(t *testing.T) {
	register(t, "test/negative-quality", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "weight": 1, "quality": -4},
			{"type": "minecraft:item", "name": "minecraft:stick", "weight": 1}
		]}]
	}`)
	for i := 0; i < 20; i++ {
		got := items(Roll("test/negative-quality", &Context{RNG: fixedRNG(float64(i) / 20), Luck: 1}))
		if !reflect.DeepEqual(got, []string{"stick"}) {
			t.Fatalf("draw %d = %v, want the surviving entry: the apple's weight floors at zero", i, got)
		}
	}
}

func TestAlternativesTakeTheFirstMatch(t *testing.T) {
	register(t, "test/alternatives", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:alternatives", "children": [
				{"type": "minecraft:item", "name": "minecraft:diamond",
				 "conditions": [{"condition": "minecraft:random_chance", "chance": 0.5}]},
				{"type": "minecraft:item", "name": "minecraft:coal"}
			]}
		]}]
	}`)
	if got := items(Roll("test/alternatives", &Context{RNG: fixedRNG(0.1)})); !reflect.DeepEqual(got, []string{"diamond"}) {
		t.Fatalf("passing first child = %v, want the diamond", got)
	}
	if got := items(Roll("test/alternatives", &Context{RNG: fixedRNG(0.9)})); !reflect.DeepEqual(got, []string{"coal"}) {
		t.Fatalf("failing first child = %v, want the fallback", got)
	}
}

func TestAnyOfAndInverted(t *testing.T) {
	register(t, "test/any-of", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:bone", "conditions": [
				{"condition": "minecraft:any_of", "terms": [
					{"condition": "minecraft:inverted",
					 "term": {"condition": "minecraft:killed_by_player"}},
					{"condition": "minecraft:random_chance", "chance": 0.9}
				]}
			]}
		]}]
	}`)
	// Not killed by a player: the inverted term passes on its own, with
	// no draw spent.
	if got := items(Roll("test/any-of", &Context{RNG: fixedRNG(0.95)})); !reflect.DeepEqual(got, []string{"bone"}) {
		t.Fatalf("not killed by a player = %v, want the inverted term to carry it", got)
	}
	// Killed by a player: the inverted term fails and the chance term
	// decides.
	if got := items(Roll("test/any-of", &Context{RNG: fixedRNG(0.5), KilledByPlayer: true})); !reflect.DeepEqual(got, []string{"bone"}) {
		t.Fatalf("killed by a player, lucky draw = %v, want the bone", got)
	}
	if got := Roll("test/any-of", &Context{RNG: fixedRNG(0.95), KilledByPlayer: true}); got != nil {
		t.Fatalf("killed by a player, unlucky draw = %v, want nothing", got)
	}
}

func TestAllOf(t *testing.T) {
	register(t, "test/all-of", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:bone", "conditions": [
				{"condition": "minecraft:all_of", "terms": [
					{"condition": "minecraft:killed_by_player"},
					{"condition": "minecraft:random_chance", "chance": 0.5}
				]}
			]}
		]}]
	}`)
	if got := items(Roll("test/all-of", &Context{RNG: fixedRNG(0.1), KilledByPlayer: true})); !reflect.DeepEqual(got, []string{"bone"}) {
		t.Fatalf("all terms hold = %v, want the bone", got)
	}
	if got := Roll("test/all-of", &Context{RNG: fixedRNG(0.1)}); got != nil {
		t.Fatalf("one term fails = %v, want nothing", got)
	}
}

func TestBonusRollsScaleWithLuck(t *testing.T) {
	register(t, "test/bonus-rolls", `{
		"pools": [{"rolls": 1, "bonus_rolls": 2, "entries": [
			{"type": "minecraft:item", "name": "minecraft:arrow"}
		]}]
	}`)
	for _, tc := range []struct {
		luck float32
		want int
	}{{0, 1}, {0.5, 2}, {1, 3}, {2, 5}} {
		got := Roll("test/bonus-rolls", &Context{RNG: fixedRNG(0.5), Luck: tc.luck})
		if len(got) != tc.want {
			t.Fatalf("luck %v rolled %d times, want %d", tc.luck, len(got), tc.want)
		}
	}
}

func TestUniformRollsProvider(t *testing.T) {
	register(t, "test/uniform-rolls", `{
		"pools": [{"rolls": {"type": "minecraft:uniform", "min": 1, "max": 3}, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple"}
		]}]
	}`)
	// A uniform integer draw is inclusive of max: a 0.9 draw takes the
	// third of three rolls.
	if got := Roll("test/uniform-rolls", &Context{RNG: fixedRNG(0.9)}); len(got) != 3 {
		t.Fatalf("uniform 1..3 with a high draw rolled %d times, want 3", len(got))
	}
	if got := Roll("test/uniform-rolls", &Context{RNG: fixedRNG(0.0)}); len(got) != 1 {
		t.Fatalf("uniform 1..3 with a low draw rolled %d times, want 1", len(got))
	}
}

func TestSetCountAndAdd(t *testing.T) {
	register(t, "test/set-count", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "functions": [
				{"function": "minecraft:set_count", "count": 5},
				{"function": "minecraft:set_count", "count": 2, "add": true}
			]}
		]}]
	}`)
	got := Roll("test/set-count", &Context{RNG: fixedRNG(0)})
	if len(got) != 1 || got[0].Count != 7 {
		t.Fatalf("set_count 5 then add 2 = %v, want 7", got)
	}
}

func TestZeroCountIsDropped(t *testing.T) {
	register(t, "test/zero-count", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "functions": [
				{"function": "minecraft:set_count", "count": 0}
			]}
		]}]
	}`)
	if got := Roll("test/zero-count", &Context{RNG: fixedRNG(0)}); got != nil {
		t.Fatalf("a zero count = %v, want nothing", got)
	}
}

func TestLimitCountClamps(t *testing.T) {
	register(t, "test/limit-max", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "functions": [
				{"function": "minecraft:set_count", "count": 5},
				{"function": "minecraft:limit_count", "limit": {"max": 2}}
			]}
		]}]
	}`)
	register(t, "test/limit-min", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "functions": [
				{"function": "minecraft:limit_count", "limit": {"min": 3}}
			]}
		]}]
	}`)
	if got := Roll("test/limit-max", &Context{RNG: fixedRNG(0)}); got[0].Count != 2 {
		t.Fatalf("limit max 2 on a count of 5 = %v, want 2", got)
	}
	if got := Roll("test/limit-min", &Context{RNG: fixedRNG(0)}); got[0].Count != 3 {
		t.Fatalf("limit min 3 on a count of 1 = %v, want 3", got)
	}
}

func TestGroupRunsEveryChildAndSequenceStops(t *testing.T) {
	const children = `"children": [
		{"type": "minecraft:item", "name": "minecraft:apple"},
		{"type": "minecraft:item", "name": "minecraft:stick",
		 "conditions": [{"condition": "minecraft:random_chance", "chance": 0.5}]},
		{"type": "minecraft:item", "name": "minecraft:bone"}
	]`
	register(t, "test/group", `{"pools": [{"rolls": 1, "entries": [
		{"type": "minecraft:group", `+children+`}]}]}`)
	register(t, "test/sequence", `{"pools": [{"rolls": 1, "entries": [
		{"type": "minecraft:sequence", `+children+`}]}]}`)

	// Every child runs when they all pass.
	want := []string{"apple", "bone", "stick"}
	if got := items(Roll("test/group", &Context{RNG: fixedRNG(0.1)})); !reflect.DeepEqual(got, want) {
		t.Fatalf("group, all passing = %v, want %v", got, want)
	}
	if got := items(Roll("test/sequence", &Context{RNG: fixedRNG(0.1)})); !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence, all passing = %v, want %v", got, want)
	}
	// The failing middle child is skipped by a group but ends a
	// sequence.
	if got := items(Roll("test/group", &Context{RNG: fixedRNG(0.9)})); !reflect.DeepEqual(got, []string{"apple", "bone"}) {
		t.Fatalf("group, middle failing = %v, want the two unconditional children", got)
	}
	if got := items(Roll("test/sequence", &Context{RNG: fixedRNG(0.9)})); !reflect.DeepEqual(got, []string{"apple"}) {
		t.Fatalf("sequence, middle failing = %v, want the first child only", got)
	}
}

func TestTagEntryPicksOneMember(t *testing.T) {
	register(t, "test/tag", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:tag", "name": "minecraft:anvil"}
		]}]
	}`)
	members := itemTag(t, "anvil")
	for i := 0; i < 30; i++ {
		got := Roll("test/tag", &Context{RNG: fixedRNG(float64(i) / 30)})
		if len(got) != 1 {
			t.Fatalf("a tag entry rolled %d stacks, want one member", len(got))
		}
		if !contains(members, got[0].Item) {
			t.Fatalf("a tag entry rolled %q, which is not in the anvil tag (%v)", got[0].Item, members)
		}
	}
}

func TestTagEntryExpandMakesOneChildPerMember(t *testing.T) {
	register(t, "test/tag-expand", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:group", "children": [
				{"type": "minecraft:item", "name": "minecraft:apple"},
				{"type": "minecraft:tag", "name": "minecraft:anvil", "expand": true}
			]}
		]}]
	}`)
	got := items(Roll("test/tag-expand", &Context{RNG: fixedRNG(0)}))
	want := append([]string{"apple"}, itemTag(t, "anvil")...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expanded tag inside a group = %v, want %v", got, want)
	}
}

func TestMatchToolItemsAndTags(t *testing.T) {
	register(t, "test/match-tool-list", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:match_tool",
				 "predicate": {"items": ["minecraft:shears", "minecraft:flint_and_steel"]}}
			]}
		]}]
	}`)
	register(t, "test/match-tool-tag", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:match_tool", "predicate": {"items": "#minecraft:axes"}}
			]}
		]}]
	}`)
	if got := items(Roll("test/match-tool-list", &Context{RNG: fixedRNG(0), Tool: "shears"})); len(got) != 1 {
		t.Fatalf("shears = %v, want the apple", got)
	}
	if got := Roll("test/match-tool-list", &Context{RNG: fixedRNG(0), Tool: "stone_axe"}); got != nil {
		t.Fatalf("an axe = %v, want nothing", got)
	}
	// A tag predicate goes through the item tag registry.
	if got := items(Roll("test/match-tool-tag", &Context{RNG: fixedRNG(0), Tool: "iron_axe"})); len(got) != 1 {
		t.Fatalf("iron axe against #axes = %v, want the apple", got)
	}
	if got := Roll("test/match-tool-tag", &Context{RNG: fixedRNG(0), Tool: "shears"}); got != nil {
		t.Fatalf("shears against #axes = %v, want nothing", got)
	}
	// A caller that knows the tool's tags does not need the registry.
	ctx := &Context{RNG: fixedRNG(0), ToolTags: []string{"minecraft:axes"}}
	if got := items(Roll("test/match-tool-tag", ctx)); len(got) != 1 {
		t.Fatalf("ToolTags membership = %v, want the apple", got)
	}
}

func TestMatchToolEmptyTool(t *testing.T) {
	register(t, "test/match-tool-empty", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:match_tool", "predicate": {"items": "minecraft:shears"}}
			]}
		]}]
	}`)
	if got := Roll("test/match-tool-empty", &Context{RNG: fixedRNG(0)}); got != nil {
		t.Fatalf("a bare hand = %v, want nothing", got)
	}
}

func TestBlockStateProperty(t *testing.T) {
	register(t, "test/block-state", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "functions": [
				{"function": "minecraft:set_count", "count": 2, "conditions": [
					{"condition": "minecraft:block_state_property", "block": "minecraft:oak_slab",
					 "properties": {"type": "double"}}
				]}
			]}
		]}]
	}`)
	double := &Context{RNG: fixedRNG(0), Block: "oak_slab", BlockProps: map[string]string{"type": "double"}}
	bottom := &Context{RNG: fixedRNG(0), Block: "oak_slab", BlockProps: map[string]string{"type": "bottom"}}
	wrong := &Context{RNG: fixedRNG(0), Block: "stone", BlockProps: map[string]string{"type": "double"}}
	unknown := &Context{RNG: fixedRNG(0)}
	for _, tc := range []struct {
		name string
		ctx  *Context
		want int32
	}{
		{"double slab", double, 2},
		{"bottom slab", bottom, 1},
		{"different block", wrong, 1},
		{"no block state at all", unknown, 1},
	} {
		got := Roll("test/block-state", tc.ctx)
		if len(got) != 1 || got[0].Count != tc.want {
			t.Fatalf("%s = %v, want count %d", tc.name, got, tc.want)
		}
	}
}

func TestTableBonusClampsTheLevel(t *testing.T) {
	register(t, "test/table-bonus", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:table_bonus", "enchantment": "minecraft:fortune",
				 "chances": [0.1, 0.5, 0.9]}
			]}
		]}]
	}`)
	for _, tc := range []struct {
		level int
		draw  float64
		want  bool
	}{
		{0, 0.05, true},  // chances[0]
		{0, 0.5, false},  // above chances[0]
		{2, 0.5, true},   // chances[2]
		{9, 0.5, true},   // clamped to the last entry
		{9, 0.95, false}, // above the last entry
	} {
		ctx := &Context{RNG: fixedRNG(tc.draw), Enchantments: map[string]int{"fortune": tc.level}}
		if tc.level == 0 {
			ctx.Enchantments = nil
		}
		got := Roll("test/table-bonus", ctx)
		if (len(got) == 1) != tc.want {
			t.Fatalf("fortune %d with draw %v = %v, want drop %v", tc.level, tc.draw, got, tc.want)
		}
	}
}

func TestRandomChanceLootingMultiplier(t *testing.T) {
	register(t, "test/random-chance", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:random_chance", "chance": 0.1, "looting_multiplier": 0.2}
			]}
		]}]
	}`)
	if got := Roll("test/random-chance", &Context{RNG: fixedRNG(0.2)}); got != nil {
		t.Fatalf("no luck with a 0.2 draw = %v, want nothing: the chance is 0.1", got)
	}
	if got := Roll("test/random-chance", &Context{RNG: fixedRNG(0.2), Luck: 1}); len(got) != 1 {
		t.Fatalf("luck 1 with a 0.2 draw = %v, want the drop: the chance is 0.3", got)
	}
}

func TestRandomChanceWithEnchantedBonusUsesLinearChance(t *testing.T) {
	register(t, "test/enchanted-chance", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:random_chance_with_enchanted_bonus",
				 "enchantment": "minecraft:looting",
				 "unenchanted_chance": 0.25,
				 "enchanted_chance": {"type": "minecraft:linear", "base": 0.25, "per_level_above_first": 0.1}}
			]}
		]}]
	}`)
	// Without the enchantment the flat chance applies: a 0.3 draw fails.
	if got := Roll("test/enchanted-chance", &Context{RNG: fixedRNG(0.3)}); got != nil {
		t.Fatalf("no looting = %v, want nothing", got)
	}
	// Looting I adds nothing above base, so 0.3 still fails...
	if got := Roll("test/enchanted-chance", &Context{RNG: fixedRNG(0.3), Enchantments: map[string]int{"looting": 1}}); got != nil {
		t.Fatalf("looting I = %v, want nothing: base is the first level", got)
	}
	// ...while looting III reaches 0.45 and passes.
	ctx := &Context{RNG: fixedRNG(0.3), Enchantments: map[string]int{"looting": 3}}
	if got := Roll("test/enchanted-chance", ctx); len(got) != 1 {
		t.Fatalf("looting III = %v, want the drop at chance 0.45", got)
	}
}

func TestApplyBonusOreDrops(t *testing.T) {
	register(t, "test/ore-drops", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:coal", "functions": [
				{"function": "minecraft:set_count", "count": 1},
				{"function": "minecraft:apply_bonus", "enchantment": "minecraft:fortune",
				 "formula": "minecraft:ore_drops"}
			]}
		]}]
	}`)
	// Level 1 draws from 0,1,2 and floors the multiplier at one, so the
	// count is 1 or 2 and never zero.
	for _, tc := range []struct {
		draw float64
		want int32
	}{{0, 1}, {0.5, 1}, {0.9, 2}} {
		ctx := &Context{RNG: fixedRNG(tc.draw), Enchantments: map[string]int{"fortune": 1}}
		got := Roll("test/ore-drops", ctx)
		if len(got) != 1 || got[0].Count != tc.want {
			t.Fatalf("fortune I with draw %v = %v, want %d", tc.draw, got, tc.want)
		}
	}
	// Without the enchantment the formula does not run at all.
	if got := Roll("test/ore-drops", &Context{RNG: fixedRNG(0.9)}); got[0].Count != 1 {
		t.Fatalf("no fortune = %v, want the plain count", got)
	}
}

func TestApplyBonusUniformBonusCount(t *testing.T) {
	register(t, "test/uniform-bonus", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:coal", "functions": [
				{"function": "minecraft:set_count", "count": 1},
				{"function": "minecraft:apply_bonus", "enchantment": "minecraft:fortune",
				 "formula": "minecraft:uniform_bonus_count", "parameters": {"bonusMultiplier": 2}}
			]}
		]}]
	}`)
	// bonusMultiplier 2 with fortune III adds a uniform 0..6.
	for _, tc := range []struct {
		draw float64
		want int32
	}{{0, 1}, {0.5, 4}, {0.9, 7}} {
		ctx := &Context{RNG: fixedRNG(tc.draw), Enchantments: map[string]int{"fortune": 3}}
		got := Roll("test/uniform-bonus", ctx)
		if len(got) != 1 || got[0].Count != tc.want {
			t.Fatalf("uniform bonus with draw %v = %v, want %d", tc.draw, got, tc.want)
		}
	}
}

func TestApplyBonusBinomialWithBonusCount(t *testing.T) {
	register(t, "test/binomial-bonus", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:coal", "functions": [
				{"function": "minecraft:set_count", "count": 1},
				{"function": "minecraft:apply_bonus", "enchantment": "minecraft:fortune",
				 "formula": "minecraft:binomial_with_bonus_count",
				 "parameters": {"extra": 3, "probability": 0.5}}
			]}
		]}]
	}`)
	// extra 3 plus fortune I is four trials at p = 0.5, so a constant
	// 0.1 draw succeeds every trial and a 0.9 draw succeeds none.
	low := &Context{RNG: fixedRNG(0.1), Enchantments: map[string]int{"fortune": 1}}
	if got := Roll("test/binomial-bonus", low); got[0].Count != 5 {
		t.Fatalf("four certain trials = %v, want 5", got)
	}
	high := &Context{RNG: fixedRNG(0.9), Enchantments: map[string]int{"fortune": 1}}
	if got := Roll("test/binomial-bonus", high); got[0].Count != 1 {
		t.Fatalf("four impossible trials = %v, want 1", got)
	}
}

func TestEnchantedCountIncrease(t *testing.T) {
	const table = `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:porkchop", "functions": [
				{"function": "minecraft:set_count", "count": 1},
				{"function": "minecraft:enchanted_count_increase", "enchantment": "minecraft:looting",
				 "count": {"type": "minecraft:uniform", "min": 0, "max": 1}%s}
			]}
		]}]
	}`
	register(t, "test/looting", fmt.Sprintf(table, ""))
	register(t, "test/looting-limit", fmt.Sprintf(table, `, "limit": 1`))

	// A uniform float in [0,1) times the level, rounded the data pack
	// way: 0.5 * 3 = 1.5 rounds to 2.
	ctx := &Context{RNG: fixedRNG(0.5), Enchantments: map[string]int{"looting": 3}}
	if got := Roll("test/looting", ctx); got[0].Count != 3 {
		t.Fatalf("looting III with a 0.5 draw = %v, want 3", got)
	}
	// No enchantment, no extra items.
	if got := Roll("test/looting", &Context{RNG: fixedRNG(0.9)}); got[0].Count != 1 {
		t.Fatalf("no looting = %v, want 1", got)
	}
	// A low draw rounds down to nothing.
	ctx = &Context{RNG: fixedRNG(0.1), Enchantments: map[string]int{"looting": 3}}
	if got := Roll("test/looting", ctx); got[0].Count != 1 {
		t.Fatalf("looting III with a 0.1 draw = %v, want 1: 0.3 rounds to 0", got)
	}
	// The limit caps the extra items, not the total.
	ctx = &Context{RNG: fixedRNG(0.9), Enchantments: map[string]int{"looting": 3}}
	if got := Roll("test/looting-limit", ctx); got[0].Count != 2 {
		t.Fatalf("looting III with a limit of 1 = %v, want 2", got)
	}
}

func TestExplosionDecay(t *testing.T) {
	register(t, "test/decay", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "functions": [
				{"function": "minecraft:set_count", "count": 10},
				{"function": "minecraft:explosion_decay"}
			]}
		]}]
	}`)
	// No explosion: nothing decays.
	got := Roll("test/decay", &Context{RNG: fixedRNG(0.99)})
	if len(got) != 1 || got[0].Count != 10 {
		t.Fatalf("no explosion = %v, want 10", got)
	}
	// A radius of 1 cannot destroy anything: the survival chance is
	// 1/1, and Float64 is always below 1.
	got = Roll("test/decay", &Context{RNG: fixedRNG(0.99), ExplosionRadius: 1})
	if len(got) != 1 || got[0].Count != 10 {
		t.Fatalf("radius 1 = %v, want 10", got)
	}
	// At radius 4 an item survives when its draw is below 1/4, so
	// alternating draws keep half of the pile.
	got = Roll("test/decay", &Context{RNG: fixedRNG(0.1, 0.9), ExplosionRadius: 4})
	if len(got) != 1 || got[0].Count != 5 {
		t.Fatalf("radius 4 with alternating draws = %v, want 5", got)
	}
	// A big blast wipes the whole stack out, and a zero-count stack is
	// not a drop.
	got = Roll("test/decay", &Context{RNG: fixedRNG(0.5), ExplosionRadius: 100})
	if got != nil {
		t.Fatalf("radius 100 = %v, want nothing left", got)
	}
}

func TestSurvivesExplosion(t *testing.T) {
	register(t, "test/survives", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:cobblestone", "conditions": [
				{"condition": "minecraft:survives_explosion"}
			]}
		]}]
	}`)
	if got := Roll("test/survives", &Context{RNG: fixedRNG(0.99), ExplosionRadius: 1}); len(got) != 1 {
		t.Fatalf("radius 1 = %v, want the drop", got)
	}
	if got := Roll("test/survives", &Context{RNG: fixedRNG(0.1), ExplosionRadius: 4}); len(got) != 1 {
		t.Fatalf("radius 4 with a low draw = %v, want the drop", got)
	}
	if got := Roll("test/survives", &Context{RNG: fixedRNG(0.9), ExplosionRadius: 4}); got != nil {
		t.Fatalf("radius 4 with a high draw = %v, want nothing", got)
	}
}

func TestLootTableEntriesSpliceTheirStacks(t *testing.T) {
	register(t, "test/sub", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple"}
		]}]
	}`)
	// One pool per entry, so both are always rolled: a single pool with
	// one roll would pick only one of them.
	register(t, "test/splice", `{
		"pools": [
			{"rolls": 1, "entries": [
				{"type": "minecraft:loot_table", "value": "minecraft:test/sub"}]},
			{"rolls": 1, "entries": [
				{"type": "minecraft:loot_table", "value": {"pools": [{"rolls": 1, "entries": [
					{"type": "minecraft:item", "name": "minecraft:bone"}
				]}]}}]}
		]
	}`)
	got := items(Roll("test/splice", &Context{RNG: fixedRNG(0)}))
	if want := []string{"apple", "bone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("spliced tables = %v, want %v", got, want)
	}
}

func TestEmptyAndDynamicEntries(t *testing.T) {
	register(t, "test/empty", `{
		"pools": [
			{"rolls": 1, "entries": [{"type": "minecraft:empty"}]},
			{"rolls": 1, "entries": [{"type": "minecraft:dynamic", "name": "minecraft:contents"}]}
		]
	}`)
	ResetSkipped()
	if got := Roll("test/empty", &Context{RNG: fixedRNG(0)}); got != nil {
		t.Fatalf("empty and dynamic entries = %v, want nothing", got)
	}
	if Skipped()["unmodelled-entry:minecraft:dynamic"] != 1 {
		t.Fatalf("the dynamic entry was not recorded: %v", Skipped())
	}
}

func TestUnknownConstructsAreCountedAndSurvive(t *testing.T) {
	register(t, "test/unknown", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple",
			 "conditions": [{"condition": "minecraft:not_a_condition"}],
			 "functions": [{"function": "minecraft:not_a_function"}]}
		]}]
	}`)
	ResetSkipped()
	got := items(Roll("test/unknown", &Context{RNG: fixedRNG(0)}))
	if !reflect.DeepEqual(got, []string{"apple"}) {
		t.Fatalf("unknown condition rolled %v, want the drop: unknown conditions pass", got)
	}
	d := Skipped()
	if d["unknown-condition:not_a_condition"] != 1 {
		t.Fatalf("the unknown condition was not counted: %v", d)
	}
	if d["unknown-function:not_a_function"] != 1 {
		t.Fatalf("the unknown function was not counted: %v", d)
	}
}

func TestUnknownEntryTypeRollsNothing(t *testing.T) {
	register(t, "test/unknown-entry", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:not_an_entry", "name": "minecraft:apple"}
		]}]
	}`)
	ResetSkipped()
	if got := Roll("test/unknown-entry", &Context{RNG: fixedRNG(0)}); got != nil {
		t.Fatalf("an unknown entry type rolled %v, want nothing", got)
	}
	if Skipped()["unknown-entry:not_an_entry"] != 1 {
		t.Fatalf("the unknown entry was not counted: %v", Skipped())
	}
}

func TestPoolConditionsGateTheWholePool(t *testing.T) {
	register(t, "test/pool-condition", `{
		"pools": [
			{"rolls": 1, "conditions": [{"condition": "minecraft:killed_by_player"}], "entries": [
				{"type": "minecraft:item", "name": "minecraft:apple"}]},
			{"rolls": 1, "entries": [{"type": "minecraft:item", "name": "minecraft:bone"}]}
		]
	}`)
	got := items(Roll("test/pool-condition", &Context{RNG: fixedRNG(0)}))
	if want := []string{"bone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a gated pool = %v, want %v", got, want)
	}
	got = items(Roll("test/pool-condition", &Context{RNG: fixedRNG(0), KilledByPlayer: true}))
	if want := []string{"apple", "bone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("both pools = %v, want %v", got, want)
	}
}

func TestEntityPropertiesNeedsTheCallersFacts(t *testing.T) {
	register(t, "test/entity-type", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:entity_properties", "entity": "this",
				 "predicate": {"minecraft:entity_type": "minecraft:pig"}}
			]}
		]}]
	}`)
	ResetSkipped()
	if got := Roll("test/entity-type", &Context{RNG: fixedRNG(0)}); got != nil {
		t.Fatalf("an unknown entity type = %v, want nothing: unknown must not pass", got)
	}
	if got := Roll("test/entity-type", &Context{RNG: fixedRNG(0), EntityType: "pig"}); len(got) != 1 {
		t.Fatalf("a pig = %v, want the apple", got)
	}
	register(t, "test/entity-component", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:white_wool", "conditions": [
				{"condition": "minecraft:entity_properties", "entity": "this",
				 "predicate": {"minecraft:components": {"minecraft:sheep/color": "white"}}}
			]}
		]}]
	}`)
	// The component predicates a sheep's wool pool needs are not
	// modelled, so the drop is recorded as a gap instead of guessed at.
	if got := Roll("test/entity-component", &Context{RNG: fixedRNG(0), EntityType: "sheep"}); got != nil {
		t.Fatalf("a sheep colour predicate = %v, want nothing", got)
	}
	if Skipped()["unmodelled-predicate:entity_properties/components"] == 0 {
		t.Fatalf("the sheep colour predicate was not recorded: %v", Skipped())
	}
}

func TestEntityPropertiesFlagsAndAttacker(t *testing.T) {
	register(t, "test/flags", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:entity_properties", "entity": "this",
				 "predicate": {"minecraft:flags": {"is_on_fire": true}}}
			]}
		]}]
	}`)
	register(t, "test/flags-not", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:entity_properties", "entity": "this",
				 "predicate": {"minecraft:flags": {"is_on_fire": false}}}
			]}
		]}]
	}`)
	if got := Roll("test/flags", &Context{RNG: fixedRNG(0)}); got != nil {
		t.Fatalf("a cold entity against is_on_fire = %v, want nothing", got)
	}
	if got := Roll("test/flags", &Context{RNG: fixedRNG(0), EntityOnFire: true}); len(got) != 1 {
		t.Fatalf("a burning entity = %v, want the apple", got)
	}
	if got := Roll("test/flags-not", &Context{RNG: fixedRNG(0)}); len(got) != 1 {
		t.Fatalf("is_on_fire false on a cold entity = %v, want the apple", got)
	}

	// The attacker role reads the killer's type, not the victim's.
	register(t, "test/attacker", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:entity_properties", "entity": "attacker",
				 "predicate": {"minecraft:entity_type": "minecraft:skeleton"}}
			]}
		]}]
	}`)
	ctx := &Context{RNG: fixedRNG(0), EntityType: "creeper", AttackerType: "skeleton"}
	if got := Roll("test/attacker", ctx); len(got) != 1 {
		t.Fatalf("a skeleton attacker = %v, want the apple", got)
	}
	ctx = &Context{RNG: fixedRNG(0), EntityType: "skeleton", AttackerType: "player"}
	if got := Roll("test/attacker", ctx); got != nil {
		t.Fatalf("a player attacker = %v, want nothing", got)
	}
}

func TestEmptyEntityPredicateMatches(t *testing.T) {
	register(t, "test/empty-predicate", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "conditions": [
				{"condition": "minecraft:entity_properties", "entity": "this", "predicate": {}}
			]}
		]}]
	}`)
	if got := Roll("test/empty-predicate", &Context{RNG: fixedRNG(0)}); len(got) != 1 {
		t.Fatalf("an empty predicate = %v, want the drop", got)
	}
}

func TestAbsurdRollsAndTrialsAreCapped(t *testing.T) {
	register(t, "test/absurd-rolls", `{
		"pools": [{"rolls": 1000000000, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple"}
		]}]
	}`)
	register(t, "test/absurd-trials", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:apple", "functions": [
				{"function": "minecraft:set_count", "count": 1},
				{"function": "minecraft:apply_bonus", "enchantment": "minecraft:fortune",
				 "formula": "minecraft:binomial_with_bonus_count",
				 "parameters": {"extra": 1000000000, "probability": 0.5}}
			]}
		]}]
	}`)
	ResetSkipped()
	// Both tables must finish: a wrong table may not spin the tick away.
	got := Roll("test/absurd-rolls", &Context{RNG: fixedRNG(0.5)})
	if len(got) != maxRolls {
		t.Fatalf("an absurd roll count produced %d stacks, want the cap of %d", len(got), maxRolls)
	}
	if Skipped()["rolls-capped"] == 0 {
		t.Fatalf("the capped roll count was not recorded: %v", Skipped())
	}
	ctx := &Context{RNG: fixedRNG(0.1), Enchantments: map[string]int{"fortune": 1}}
	if got := Roll("test/absurd-trials", ctx); len(got) != 1 || got[0].Count != maxTrials+1 {
		t.Fatalf("an absurd trial count = %v, want it clamped to %d extra items", got, maxTrials)
	}
}

func TestNilRNGAndNilContext(t *testing.T) {
	// A caller that only wants a rough roll may leave the randomness and
	// the context out entirely.
	if got := Roll("blocks/stone", nil); len(got) != 1 || got[0].Item != "cobblestone" {
		t.Fatalf("a nil context = %v, want the cobblestone", got)
	}
	if got := Roll("blocks/stone", &Context{}); len(got) != 1 {
		t.Fatalf("a nil RNG = %v, want the shared source to answer", got)
	}
}

func TestRegisterTableOverridesTheDataPack(t *testing.T) {
	register(t, "blocks/stone", `{
		"pools": [{"rolls": 1, "entries": [
			{"type": "minecraft:item", "name": "minecraft:diamond"}
		]}]
	}`)
	// Restoring the pack's own table keeps later tests honest; the
	// override is what Roll sees in between.
	if got := items(Roll("blocks/stone", &Context{RNG: fixedRNG(0)})); !reflect.DeepEqual(got, []string{"diamond"}) {
		t.Fatalf("overridden stone = %v, want the diamond", got)
	}
	RegisterTable("blocks/stone", lootTableDef(t, "blocks/stone"))
	if got := items(Roll("blocks/stone", &Context{RNG: fixedRNG(0)})); !reflect.DeepEqual(got, []string{"cobblestone"}) {
		t.Fatalf("restored stone = %v, want cobblestone", got)
	}
}
