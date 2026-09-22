package server

import (
	"math/rand"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// rollLootTable draws one chest's contents from a vanilla loot table.
// Item entries are weighted picks; set_count's uniform provider sets
// the stack size. Functions without an item effect (set_damage,
// enchant_*) are skipped — the generated item set is what the
// client-visible slots get. Nested loot_table entries recurse into
// the named table (gameplay/fishing's fish/junk/treasure split),
// group/sequence children roll their member entries, and
// alternatives entries roll the first child that rolls anything.
func rollLootTable(table string, seed int64) []ItemStack {
	return rollLootDepth(table, rand.New(rand.NewSource(seed)), 0)
}

func rollLootDepth(table string, rng *rand.Rand, depth int) []ItemStack {
	if depth > 8 {
		return nil
	}
	lt, ok := data.LootTables()[table]
	if !ok {
		return nil
	}
	pools, _ := lt["pools"].([]any)
	var out []ItemStack
	for _, pv := range pools {
		pool, _ := pv.(map[string]any)
		rolls := lootNumber(pool["rolls"], rng)
		entries, _ := pool["entries"].([]any)
		if rolls <= 0 || len(entries) == 0 {
			continue
		}
		for i := 0; i < int(rolls); i++ {
			out = append(out, rollPoolPick(entries, rng, depth)...)
		}
	}
	return out
}

// lootEntry picks one weighted entry and evaluates it.
func rollPoolPick(entries []any, rng *rand.Rand, depth int) []ItemStack {
	total := 0.0
	for _, ev := range entries {
		em, _ := ev.(map[string]any)
		w, _ := em["weight"].(float64)
		if w <= 0 {
			w = 1
		}
		total += w
	}
	if total <= 0 {
		return nil
	}
	pick := rng.Float64() * total
	for _, ev := range entries {
		em, _ := ev.(map[string]any)
		w, _ := em["weight"].(float64)
		if w <= 0 {
			w = 1
		}
		pick -= w
		if pick > 0 {
			continue
		}
		return lootEntryValue(em, rng, depth)
	}
	return nil
}

// lootEntryValue evaluates a chosen entry by type.
func lootEntryValue(em map[string]any, rng *rand.Rand, depth int) []ItemStack {
	switch ty, _ := em["type"].(string); ty {
	case "minecraft:loot_table", "loot_table":
		name, _ := em["value"].(string)
		return rollLootDepth(strings.TrimPrefix(name, "minecraft:"), rng, depth+1)
	case "minecraft:alternatives", "alternatives", "minecraft:sequence", "sequence", "minecraft:group", "group":
		children, _ := em["children"].([]any)
		if ty == "minecraft:alternatives" || ty == "alternatives" {
			// First child that produces anything; conditions are
			// ignored, so the first reachable child wins.
			for _, cv := range children {
				cm, _ := cv.(map[string]any)
				if got := lootEntryValue(cm, rng, depth); len(got) > 0 {
					return got
				}
			}
			return nil
		}
		var out []ItemStack
		for _, cv := range children {
			cm, _ := cv.(map[string]any)
			out = append(out, lootEntryValue(cm, rng, depth)...)
		}
		return out
	case "minecraft:item", "item":
		name, _ := em["name"].(string)
		id, ok := world.ItemID(strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			return nil
		}
		count := int32(1)
		fns, _ := em["functions"].([]any)
		for _, fv := range fns {
			fm, _ := fv.(map[string]any)
			if f, _ := fm["function"].(string); f == "minecraft:set_count" {
				count = int32(lootNumber(fm["count"], rng))
			}
		}
		if count < 1 {
			count = 1
		}
		return []ItemStack{{ID: id, Count: count}}
	}
	return nil
}

// lootNumber resolves a loot number provider: a bare float or the
// uniform/binomial shapes vanilla uses in tables.
func lootNumber(v any, rng *rand.Rand) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case float32:
		return float64(t)
	case map[string]any:
		switch ty, _ := t["type"].(string); ty {
		case "minecraft:uniform", "uniform":
			min, _ := t["min"].(float64)
			max, _ := t["max"].(float64)
			return min + rng.Float64()*(max-min)
		case "minecraft:binomial", "binomial":
			n, _ := t["n"].(float64)
			p, _ := t["p"].(float64)
			acc := 0.0
			for i := 0; i < int(n); i++ {
				if rng.Float64() < p {
					acc++
				}
			}
			return acc
		case "minecraft:constant", "constant":
			c, _ := t["value"].(float64)
			return c
		}
		if c, ok := t["value"]; ok {
			return lootNumber(c, rng)
		}
	}
	return 0
}
