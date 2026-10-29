package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Seeded container loot, as LootTable.fill unpacks it the first time a
// generated chest is opened (RandomizableContainer.unpackLootTable).
// Measured from the 26.2 bytecode (javap):
//
//   - the context's random is RandomSource.create(seed): a
//     LegacyRandomSource on the saved LootTableSeed (seed 0 means the
//     level's own random instead, which is not reproducible);
//   - every pool rolls rolls.getInt + floor(bonus_rolls.getFloat * luck)
//     times (bonus_rolls is evaluated even at luck 0); each roll gathers
//     the entries whose weight is positive and, when more than one is
//     left, draws nextInt(total weight) and walks the list subtracting
//     weights (a single candidate is taken without a draw);
//   - an item entry starts as one item and runs its own functions, then
//     the pool's, then the table's; a stack above its max size is cut
//     into max-size stacks as it is emitted;
//   - number providers: a constant's getInt is Math.round of its value,
//     uniform's getInt is Mth.nextInt over its bounds' getInt and its
//     getFloat Mth.nextFloat, binomial counts n nextFloat() < p trials;
//   - set_count sets (or adds) count.getInt; enchant_randomly picks
//     uniformly from its options (a book takes any of them; anything
//     else only what it supports) and a level Mth.nextInt(1, max); a
//     book becomes an enchanted book with stored enchantments;
//   - then getAvailableSlots lists the container's empty slots in
//     order and Util.shuffle-s them, shuffleAndSplitItems drops empty
//     stacks, sets stacks of more than one aside and, while empty slots
//     outnumber the stacks, splits a random set-aside stack in two
//     (nextInt(1, count/2) off it, each half staying aside on a
//     nextBoolean when it still holds more than one), appends what is
//     left aside and shuffles the lot; each stack then takes the last
//     remaining slot of the shuffled list.
//
// A table that uses anything else (conditions, other functions, other
// entry types) reports false and the caller falls back to the
// approximate roll.

type lootFillCtx struct {
	r *worldgen.Legacy
}

// lootFillExact fills a container of size slots (occupied reports the
// slots already holding something) from table with seed, returning the
// slot assignments; false when the table needs something this does not
// model.
func lootFillExact(table string, seed int64, size int, occupied func(int) bool) (map[int]ItemStack, bool) {
	if seed == 0 {
		return nil, false
	}
	lt, ok := data.LootTables()[strings.TrimPrefix(table, "minecraft:")]
	if !ok {
		return nil, false
	}
	ctx := &lootFillCtx{r: worldgen.NewLegacy(seed)}
	tableFns, _ := lt["functions"].([]any)
	var items []ItemStack
	emit := func(st ItemStack) {
		if st.Empty() {
			return
		}
		// LootTable.createStackSplitter.
		max := maxStackOfID(st.ID)
		if max <= 0 {
			max = 64
		}
		for n := st.Count; n > 0; {
			c := st
			c.Count = min(n, max)
			n -= c.Count
			items = append(items, c)
		}
	}
	pools, _ := lt["pools"].([]any)
	for _, pv := range pools {
		pool, _ := pv.(map[string]any)
		if pool == nil || pool["conditions"] != nil {
			return nil, false
		}
		poolFns, _ := pool["functions"].([]any)
		rolls, ok := ctx.numInt(pool["rolls"])
		if !ok {
			return nil, false
		}
		if b, present := pool["bonus_rolls"]; present {
			bonus, ok := ctx.numFloat(b)
			if !ok {
				return nil, false
			}
			rolls += int32(math.Floor(float64(bonus * 0)))
		}
		entries, _ := pool["entries"].([]any)
		for i := int32(0); i < rolls; i++ {
			type cand struct {
				e      map[string]any
				weight int32
			}
			var list []cand
			total := int32(0)
			for _, ev := range entries {
				e, _ := ev.(map[string]any)
				if e == nil || e["conditions"] != nil {
					return nil, false
				}
				switch e["type"] {
				case "minecraft:item", "minecraft:empty":
				default:
					return nil, false
				}
				w := int32(1)
				if v, ok := e["weight"].(float64); ok {
					w = int32(v)
				}
				// getWeight(luck): max(floor(weight + quality*luck), 0)
				// at luck 0.
				if w > 0 {
					list = append(list, cand{e, w})
					total += w
				}
			}
			if total == 0 || len(list) == 0 {
				continue
			}
			pick := list[0].e
			if len(list) > 1 {
				j := ctx.r.NextIntN(total)
				for _, c := range list {
					j -= c.weight
					if j < 0 {
						pick = c.e
						break
					}
				}
			}
			if pick["type"] == "minecraft:empty" {
				continue
			}
			name, _ := pick["name"].(string)
			id, ok := world.ItemID(strings.TrimPrefix(name, "minecraft:"))
			if !ok {
				return nil, false
			}
			st := ItemStack{ID: id, Count: 1}
			for _, fns := range [][]any{asList(pick["functions"]), poolFns, tableFns} {
				if st, ok = ctx.applyFns(fns, st); !ok {
					return nil, false
				}
			}
			emit(st)
		}
	}

	// getAvailableSlots: the empty slots, shuffled.
	var slots []int
	for i := 0; i < size; i++ {
		if !occupied(i) {
			slots = append(slots, i)
		}
	}
	shuffleInts(slots, ctx.r)
	items = ctx.shuffleAndSplit(items, len(slots))
	out := map[int]ItemStack{}
	for _, st := range items {
		if len(slots) == 0 {
			break // "Tried to over-fill a container"
		}
		slot := slots[len(slots)-1]
		slots = slots[:len(slots)-1]
		out[slot] = st
	}
	return out, true
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// shuffleInts / shuffleStacks are Util.shuffle.
func shuffleInts(l []int, r *worldgen.Legacy) {
	for i := len(l); i > 1; i-- {
		j := r.NextIntN(int32(i))
		l[i-1], l[j] = l[j], l[i-1]
	}
}

func shuffleStacks(l []ItemStack, r *worldgen.Legacy) {
	for i := len(l); i > 1; i-- {
		j := r.NextIntN(int32(i))
		l[i-1], l[j] = l[j], l[i-1]
	}
}

// mthNextInt is Mth.nextInt(random, min, max).
func mthNextInt(r *worldgen.Legacy, lo, hi int32) int32 {
	if lo >= hi {
		return lo
	}
	return r.NextIntN(hi-lo+1) + lo
}

// shuffleAndSplit is LootTable.shuffleAndSplitItems.
func (c *lootFillCtx) shuffleAndSplit(items []ItemStack, slots int) []ItemStack {
	var keep, split []ItemStack
	for _, st := range items {
		switch {
		case st.Empty():
		case st.Count > 1:
			split = append(split, st)
		default:
			keep = append(keep, st)
		}
	}
	for slots-len(keep)-len(split) > 0 && len(split) > 0 {
		i := mthNextInt(c.r, 0, int32(len(split)-1))
		st := split[i]
		split = append(split[:i], split[i+1:]...)
		n := mthNextInt(c.r, 1, st.Count/2)
		part := st
		part.Count = n
		st.Count -= n
		if st.Count > 1 && c.r.NextBool() {
			split = append(split, st)
		} else {
			keep = append(keep, st)
		}
		if part.Count > 1 && c.r.NextBool() {
			split = append(split, part)
		} else {
			keep = append(keep, part)
		}
	}
	keep = append(keep, split...)
	shuffleStacks(keep, c.r)
	return keep
}

// numInt / numFloat evaluate a number provider's getInt / getFloat.
func (c *lootFillCtx) numInt(v any) (int32, bool) {
	switch n := v.(type) {
	case float64:
		return int32(math.Floor(float64(float32(n)) + 0.5)), true
	case map[string]any:
		switch n["type"] {
		case "minecraft:uniform":
			lo, ok1 := c.numInt(n["min"])
			hi, ok2 := c.numInt(n["max"])
			if !ok1 || !ok2 {
				return 0, false
			}
			return mthNextInt(c.r, lo, hi), true
		case "minecraft:constant":
			return c.numInt(n["value"])
		case "minecraft:binomial":
			trials, ok1 := c.numInt(n["n"])
			p, ok2 := c.numFloat(n["p"])
			if !ok1 || !ok2 {
				return 0, false
			}
			k := int32(0)
			for i := int32(0); i < trials; i++ {
				if c.r.NextFloat() < p {
					k++
				}
			}
			return k, true
		}
	}
	return 0, false
}

func (c *lootFillCtx) numFloat(v any) (float32, bool) {
	switch n := v.(type) {
	case float64:
		return float32(n), true
	case map[string]any:
		switch n["type"] {
		case "minecraft:uniform":
			lo, ok1 := c.numFloat(n["min"])
			hi, ok2 := c.numFloat(n["max"])
			if !ok1 || !ok2 {
				return 0, false
			}
			if lo >= hi {
				return lo, true
			}
			return c.r.NextFloat()*(hi-lo) + lo, true
		case "minecraft:constant":
			return c.numFloat(n["value"])
		case "minecraft:binomial":
			k, ok := c.numInt(n)
			return float32(k), ok
		}
	}
	return 0, false
}

// applyFns runs a function list over a stack.
func (c *lootFillCtx) applyFns(fns []any, st ItemStack) (ItemStack, bool) {
	for _, fv := range fns {
		f, _ := fv.(map[string]any)
		if f == nil || f["conditions"] != nil {
			return st, false
		}
		switch f["function"] {
		case "minecraft:set_count":
			base := int32(0)
			if add, _ := f["add"].(bool); add {
				base = st.Count
			}
			n, ok := c.numInt(f["count"])
			if !ok {
				return st, false
			}
			st.Count = base + n
			if st.Count <= 0 {
				st = ItemStack{}
			}
		case "minecraft:enchant_randomly":
			var ok bool
			if st, ok = c.enchantRandomly(f, st); !ok {
				return st, false
			}
		default:
			return st, false
		}
	}
	return st, true
}

// enchantRandomly is EnchantRandomlyFunction.run.
func (c *lootFillCtx) enchantRandomly(f map[string]any, st ItemStack) (ItemStack, bool) {
	if st.Empty() {
		return st, true
	}
	if v, _ := f["include_additional_cost_component"].(bool); v {
		return st, false
	}
	var options []string
	switch o := f["options"].(type) {
	case string:
		switch o {
		case "#minecraft:on_random_loot":
			options = data.OnRandomLootOrder
		default:
			if strings.HasPrefix(o, "#") {
				return st, false
			}
			options = []string{strings.TrimPrefix(o, "minecraft:")}
		}
	case []any:
		for _, e := range o {
			s, _ := e.(string)
			if strings.HasPrefix(s, "#") {
				return st, false
			}
			options = append(options, strings.TrimPrefix(s, "minecraft:"))
		}
	default:
		// No options: the whole enchantment registry, in registry order.
		return st, false
	}
	name, _ := itemNameOf(st.ID)
	isBook := name == "book"
	onlyCompatible := true
	if v, ok := f["only_compatible"].(bool); ok {
		onlyCompatible = v
	}
	var list []string
	for _, e := range options {
		meta, ok := data.Enchantments[e]
		if !ok {
			return st, false
		}
		if !isBook && onlyCompatible && !containsString(meta.Supported, name) {
			continue
		}
		list = append(list, e)
	}
	if len(list) == 0 {
		return st, true
	}
	e := list[c.r.NextIntN(int32(len(list)))]
	lvl := mthNextInt(c.r, 1, int32(data.Enchantments[e].MaxLevel))
	return enchantApply(st, []enchantInstance{{name: e, level: int(lvl)}}), true
}

func containsString(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// unpackLootInto fills slots from an armed loot table: vanilla's exact
// seeded fill when lootFillExact models the table, else the approximate
// roll scattered into random empty slots (a seed of 0 - the level's own
// random in vanilla - rolls a fresh one).
func unpackLootInto(slots []ItemStack, table string, seed int64) {
	if fill, ok := lootFillExact(table, seed, len(slots), func(i int) bool { return !slots[i].Empty() }); ok {
		for i, st := range fill {
			slots[i] = st
		}
		return
	}
	if seed == 0 {
		seed = rand.Int63()
	}
	items := rollLootTable(strings.TrimPrefix(table, "minecraft:"), seed)
	var free []int
	for i := range slots {
		if slots[i].Empty() {
			free = append(free, i)
		}
	}
	for _, it := range items {
		if len(free) == 0 {
			break
		}
		i := rand.Intn(len(free))
		slots[free[i]] = it
		free = append(free[:i], free[i+1:]...)
	}
}
