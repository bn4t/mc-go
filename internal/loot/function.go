package loot

import (
	"mc-go/internal/data"
)

// applyFunctions runs an entry's item functions over the stacks it
// produced, in order. A function may change the count, drop the stack
// (a count of zero) or change the item (furnace_smelt).
func applyFunctions(stacks []Stack, fns []map[string]any, ctx *Context) []Stack {
	if len(fns) == 0 {
		return stacks
	}
	for i := range stacks {
		for _, f := range fns {
			stacks[i] = applyFunction(stacks[i], f, ctx)
		}
	}
	return stacks
}

// applyFunction applies one item function to one stack.
//
// Functions carry their own optional conditions, which gate that
// function alone (a double slab dropping two items, a smelted drop on a
// burning victim).
//
// The component-side functions -- set_damage, set_potion,
// set_enchantments, enchant_randomly, enchant_with_levels,
// copy_components, copy_state, set_name, set_components, and the effect
// and instrument setters -- are counted as unmodelled and do nothing,
// because our stacks carry no item components yet. Each is a real
// parity gap the moment items gain components: an enchanted book from a
// dungeon chest has to arrive enchanted. The count-affecting functions
// are complete.
func applyFunction(s Stack, f map[string]any, ctx *Context) Stack {
	if ctx == nil {
		ctx = &Context{}
	}
	if !checkConditions(asConditions(f["conditions"]), ctx) {
		return s
	}
	rng := ctx.rng()
	switch name(asStr(f["function"])) {
	case "set_count":
		n := parseNumber(f["count"]).intValue(rng, 0)
		if asBool(f["add"]) {
			s.Count += int32(n)
		} else {
			s.Count = int32(n)
		}

	case "explosion_decay":
		// Each item of the stack independently survives an explosion
		// with probability 1/radius, so a big blast destroys most of a
		// pile. No radius in the context means no decay.
		r := float64(ctx.ExplosionRadius)
		if r <= 0 {
			return s
		}
		kept := int32(0)
		for i := int32(0); i < s.Count; i++ {
			if rng.Float64()*r < 1 {
				kept++
			}
		}
		s.Count = kept

	case "limit_count":
		// Either an exact integer limit or an object with optional
		// min/max providers. Missing bounds leave that side alone.
		switch lim := f["limit"].(type) {
		case float64:
			s.Count = int32(lim)
		case map[string]any:
			if mn := parseNumber(lim["min"]); mn != nil {
				if min := int32(mn.intValue(rng, 0)); s.Count < min {
					s.Count = min
				}
			}
			if mx := parseNumber(lim["max"]); mx != nil {
				if max := int32(mx.intValue(rng, 0)); s.Count > max {
					s.Count = max
				}
			}
		default:
			noteSkipped("malformed-function:limit_count")
		}

	case "furnace_smelt":
		// The drop comes out of the furnace as if cooked, keeping its
		// count. Vanilla uses the smelting recipe; we search the whole
		// cooking family with smelting first so a food that is also
		// smokeable still lands on its furnace result.
		if result, ok := cookingResult(s.Item); ok {
			s.Item = result
		}

	case "enchanted_count_increase":
		// The count provider is "extra items per level": its float value
		// is multiplied by the enchantment level and then rounded the
		// data pack way (floor(x + 0.5)). That rounding is what gives
		// looting its documented half an item per level on average and
		// its one extra item per level at the top end, which the wiki's
		// mob drop tables (a zombie's 0-2 rotten flesh becoming 0-5 with
		// looting III) confirm.
		level := ctx.Enchantments[name(asStr(f["enchantment"]))]
		if level <= 0 {
			return s
		}
		extra := toInt(parseNumber(f["count"]).value(rng, 0) * float64(level))
		if limit := toInt(asFloat(f["limit"])); limit > 0 && extra > limit {
			extra = limit
		}
		if extra > 0 {
			s.Count += int32(extra)
		}

	case "apply_bonus":
		level := ctx.Enchantments[name(asStr(f["enchantment"]))]
		if level <= 0 {
			return s
		}
		params := asMap(f["parameters"])
		switch name(asStr(f["formula"])) {
		case "ore_drops":
			// Count *= max(1, nextInt(level+2)): the multiplier is drawn
			// from 0..level+1 and floored at one. Fortune III therefore
			// gives 1,1,2,3,4 with 2.2 items per block on average, which
			// is the published average for fortune III coal and the
			// reason the low end is doubled rather than dropped.
			m := rng.Intn(level + 2)
			if m < 1 {
				m = 1
			}
			s.Count *= int32(m)

		case "uniform_bonus_count":
			// Count += nextInt(bonusMultiplier*level + 1), i.e. a
			// uniform whole number of extra items from 0 to
			// bonusMultiplier*level inclusive.
			mult := asFloat(params["bonusMultiplier"])
			if mult < 0 {
				mult = 0
			}
			extra := int(mult * float64(level))
			if extra > 0 {
				s.Count += int32(rng.Intn(extra + 1))
			}

		case "binomial_with_bonus_count":
			// Count += Binomial(level + extra, probability): the
			// parameters' "extra" rounds the trial count up.
			extra := toInt(asFloat(params["extra"]))
			s.Count += int32(parseNumber(map[string]any{
				"type": "minecraft:binomial",
				"n":    float64(extra + level),
				"p":    asFloat(params["probability"]),
			}).sample(rng))

		default:
			noteSkipped("unknown-formula:" + name(asStr(f["formula"])))
		}

	case "set_damage":
		noteSkipped("unmodelled-function:minecraft:set_damage")
	case "set_potion":
		noteSkipped("unmodelled-function:minecraft:set_potion")
	case "set_enchantments":
		noteSkipped("unmodelled-function:minecraft:set_enchantments")
	case "enchant_randomly":
		noteSkipped("unmodelled-function:minecraft:enchant_randomly")
	case "enchant_with_levels":
		noteSkipped("unmodelled-function:minecraft:enchant_with_levels")
	case "copy_components":
		noteSkipped("unmodelled-function:minecraft:copy_components")
	case "copy_state":
		noteSkipped("unmodelled-function:minecraft:copy_state")
	case "set_name":
		noteSkipped("unmodelled-function:minecraft:set_name")
	case "set_components":
		noteSkipped("unmodelled-function:minecraft:set_components")
	case "set_stew_effect":
		noteSkipped("unmodelled-function:minecraft:set_stew_effect")
	case "set_ominous_bottle_amplifier":
		noteSkipped("unmodelled-function:minecraft:set_ominous_bottle_amplifier")
	case "set_instrument":
		noteSkipped("unmodelled-function:minecraft:set_instrument")
	case "exploration_map":
		noteSkipped("unmodelled-function:minecraft:exploration_map")

	case "":
		noteSkipped("malformed-function:no-name")

	default:
		noteSkipped("unknown-function:" + name(asStr(f["function"])))
	}
	return s
}

// cookingResult maps an item to its cooked form, using the cooking
// recipes from the generated recipe registry. Smelting is searched
// first so the answer is the furnace's, then the other three cooking
// types for foods that only smoke or roast.
func cookingResult(item string) (string, bool) {
	for _, want := range []string{data.Smelting, data.Blasting, data.Smoking, data.CampfireCooking} {
		for _, r := range data.Recipes {
			if r.Type != want || r.Result.Item == "" {
				continue
			}
			for _, in := range r.Ingredients {
				if in.Accepts(item) {
					return r.Result.Item, true
				}
			}
		}
	}
	return "", false
}
