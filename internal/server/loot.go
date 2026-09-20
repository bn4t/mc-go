package server

import (
	"math/rand"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// rollLootTable draws one chest's contents from a vanilla loot table.
// Item entries are weighted picks; set_count's uniform provider sets
// the stack size. Functions without an item effect (set_damage,
// enchant_*) and non-item entries (tags, nested tables) are skipped —
// the generated item set is what the client-visible slots get.
func rollLootTable(table string, seed int64) []ItemStack {
	lt, ok := data.LootTables()[table]
	if !ok {
		return nil
	}
	rng := rand.New(rand.NewSource(seed))
	pools, _ := lt["pools"].([]any)
	var out []ItemStack
	for _, pv := range pools {
		pool, _ := pv.(map[string]any)
		rolls := lootNumber(pool["rolls"], rng)
		entries, _ := pool["entries"].([]any)
		if rolls <= 0 || len(entries) == 0 {
			continue
		}
		total := 0.0
		for _, ev := range entries {
			em, _ := ev.(map[string]any)
			if s, _ := em["type"].(string); s != "minecraft:item" {
				continue
			}
			w, _ := em["weight"].(float64)
			if w <= 0 {
				w = 1
			}
			total += w
		}
		if total <= 0 {
			continue
		}
		for i := 0; i < int(rolls); i++ {
			pick := rng.Float64() * total
			for _, ev := range entries {
				em, _ := ev.(map[string]any)
				if s, _ := em["type"].(string); s != "minecraft:item" {
					continue
				}
				w, _ := em["weight"].(float64)
				if w <= 0 {
					w = 1
				}
				pick -= w
				if pick > 0 {
					continue
				}
				name, _ := em["name"].(string)
				id, ok := world.ItemID(name)
				if !ok {
					break
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
				out = append(out, ItemStack{ID: id, Count: count})
				break
			}
		}
	}
	return out
}

// lootNumber resolves a loot number provider: a bare float or the
// uniform/binomial shapes vanilla uses in tables.
func lootNumber(v any, rng *rand.Rand) float64 {
	switch t := v.(type) {
	case float64:
		return t
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
