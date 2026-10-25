package data

import "testing"

// The generated tables are only trustworthy if their shape matches the
// data pack they came from, so these tests pin the invariants the
// runtime depends on: stable ordering, dense ids, and a handful of
// recipes and tags whose contents are easy to reason about by hand.

func TestRecipeTableShape(t *testing.T) {
	total := 0
	for _, n := range RecipeTypeCounts {
		total += n
	}
	if total != len(Recipes) {
		t.Fatalf("RecipeTypeCounts sums to %d, table holds %d", total, len(Recipes))
	}
	// The table is ordered by recipe path, which is what makes network
	// ids reproducible run to run.
	for i := 1; i < len(Recipes); i++ {
		if Recipes[i-1].ID >= Recipes[i].ID {
			t.Fatalf("recipe %d (%s) sorts before %d (%s)", i-1, Recipes[i-1].ID, i, Recipes[i].ID)
		}
	}
	for i, r := range Recipes {
		if r.Result.Count <= 0 {
			t.Fatalf("recipe %s has result count %d", r.ID, r.Result.Count)
		}
		if got := RecipeIndex(r.ID); got != int32(i) {
			t.Fatalf("RecipeIndex(%s) = %d, want %d", r.ID, got, i)
		}
	}
}

func TestKnownRecipes(t *testing.T) {
	cases := []struct {
		id     string
		typ    string
		result string
		count  int32
	}{
		{"stick", CraftingShaped, "stick", 4},
		{"oak_planks", CraftingShapeless, "oak_planks", 4},
		{"crafting_table", CraftingShaped, "crafting_table", 1},
		{"furnace", CraftingShaped, "furnace", 1},
		{"chest", CraftingShaped, "chest", 1},
		{"iron_ingot_from_smelting_iron_ore", Smelting, "iron_ingot", 1},
		{"torch", CraftingShaped, "torch", 4},
	}
	for _, c := range cases {
		r, ok := RecipeByID(c.id)
		if !ok {
			t.Fatalf("recipe %s missing", c.id)
		}
		if r.Type != c.typ || r.Result.Item != c.result || r.Result.Count != c.count {
			t.Fatalf("recipe %s = %s %s x%d, want %s %s x%d",
				c.id, r.Type, r.Result.Item, r.Result.Count, c.typ, c.result, c.count)
		}
	}
}

// TestRecipeIngredientsAreResolved guards the tag flattening: a planks
// recipe must accept every plank, and must remember it came from a tag
// so the recipe book can echo it back.
func TestRecipeIngredientsAreResolved(t *testing.T) {
	r, ok := RecipeByID("oak_planks")
	if !ok {
		t.Fatal("oak_planks missing")
	}
	if len(r.Ingredients) != 1 {
		t.Fatalf("oak_planks has %d ingredients", len(r.Ingredients))
	}
	in := r.Ingredients[0]
	if in.Tag != "oak_logs" {
		t.Fatalf("oak_planks ingredient tag = %q, want oak_logs", in.Tag)
	}
	for _, item := range []string{"oak_log", "oak_wood", "stripped_oak_log", "stripped_oak_wood"} {
		if !in.Accepts(item) {
			t.Fatalf("oak_planks does not accept %s (items %v)", item, in.Items)
		}
	}
	if in.Accepts("spruce_log") {
		t.Fatal("oak_planks accepts spruce_log")
	}

	table, ok := RecipeByID("crafting_table")
	if !ok {
		t.Fatal("crafting_table missing")
	}
	if !table.Key['#'].Accepts("crimson_planks") || !table.Key['#'].Accepts("oak_planks") {
		t.Fatalf("crafting_table key does not cover planks: %v", table.Key['#'].Items)
	}
}

func TestItemTags(t *testing.T) {
	planks := ItemTags["planks"]
	if len(planks) < 10 {
		t.Fatalf("planks tag has %d entries: %v", len(planks), planks)
	}
	for _, want := range []string{"oak_planks", "bamboo_planks", "pale_oak_planks"} {
		if !HasItemTag("planks", want) {
			t.Fatalf("planks tag is missing %s", want)
		}
	}
	if HasItemTag("planks", "oak_log") {
		t.Fatal("planks tag contains a log")
	}
	// Nested tag references must be expanded: logs includes wood.
	if !HasItemTag("#minecraft:logs", "oak_wood") {
		t.Fatalf("logs tag is missing oak_wood: %v", ItemTags["logs"])
	}
}

func TestEmbeddedDataPacks(t *testing.T) {
	if got := len(LootTables()); got < 1300 {
		t.Fatalf("loot tables = %d, want the full data pack", got)
	}
	if _, ok := LootTables()["blocks/stone"]; !ok {
		t.Fatal("blocks/stone loot table missing")
	}
	if got := len(Advancements()); got < 1600 {
		t.Fatalf("advancements = %d", got)
	}
	if _, ok := Advancements()["adventure/kill_a_mob"]; !ok {
		t.Fatal("adventure/kill_a_mob advancement missing")
	}
	if got := len(VillagerTrades()); got < 300 {
		t.Fatalf("villager trades = %d", got)
	}
}
