package loot

import (
	"math"

	"mc-go/internal/data"
)

// maxRolls caps the draws one pool may ask for, for the same reason
// maxTrials exists in the number providers.
const maxRolls = 4096

// roll evaluates one pool: its conditions gate the whole pool, then
// rolls and bonus_rolls decide how many independent draws happen, and
// each draw picks one entry by weight.
func (p *pool) roll(ctx *Context, depth int) []Stack {
	if !checkConditions(p.conditions, ctx) {
		return nil
	}
	rng := ctx.rng()
	rolls := p.rolls.intValue(rng, 0)

	// Bonus rolls are per level of luck, rounded down after the
	// multiply; the vanilla tables barely use the field, but the chest
	// tables that do mean it.
	bonus := 0
	if p.bonusRolls != nil {
		bonus = int(math.Floor(p.bonusRolls.value(rng, 0) * ctx.luck()))
		if bonus < 0 {
			bonus = 0
		}
	}

	// Entry weight is weight + quality*luck, floored and clamped at
	// zero, so luck can make a "rare" entry more likely and a negative
	// quality can switch one off entirely.
	weights := make([]int, len(p.entries))
	total := 0
	for i, e := range p.entries {
		w := int(e.weight) + int(math.Floor(float64(e.quality)*ctx.luck()))
		if w < 0 {
			w = 0
		}
		weights[i] = w
		total += w
	}
	if total <= 0 {
		return nil
	}

	// A pool rolls at most ten times in the vanilla data pack (the
	// stronghold library), so a table asking for millions of draws is a
	// mistake or an attack; cap it rather than spin the tick away.
	if rolls+bonus > maxRolls {
		noteSkipped("rolls-capped")
		rolls, bonus = maxRolls, 0
	}

	var out []Stack
	for r := 0; r < rolls+bonus; r++ {
		pick := rng.Intn(total)
		for i, w := range weights {
			if pick < w {
				out = append(out, p.entries[i].roll(ctx, depth+1)...)
				break
			}
			pick -= w
		}
	}
	return out
}

// roll evaluates one entry: its own conditions first, then its body.
func (e *entry) roll(ctx *Context, depth int) []Stack {
	if !checkConditions(e.conditions, ctx) {
		return nil
	}
	return e.rollBody(ctx, depth)
}

// rollBody is roll without the entry's own condition check.
// minecraft:alternatives needs the split, because it inspects each
// child's conditions itself to take the first match and must then run
// that child's body without testing them again.
func (e *entry) rollBody(ctx *Context, depth int) []Stack {
	if depth > maxDepth {
		noteSkipped("recursion-limit")
		return nil
	}
	switch e.kind {
	case "empty":
		return nil

	case "dynamic":
		// minecraft:dynamic drops block-entity contents (a shulker box'
		// items, a decorated pot's sherds). Those are not items this
		// interpreter can name from a loot table: the caller owns the
		// block entity and has to hand its contents over itself.
		noteSkipped("unmodelled-entry:minecraft:dynamic")
		return nil

	case "item":
		if e.name == "" {
			noteSkipped("malformed-entry:item-without-name")
			return nil
		}
		return applyFunctions([]Stack{{Item: e.name, Count: 1}}, e.functions, ctx)

	case "tag":
		items := data.ItemTags[e.name]
		if len(items) == 0 {
			noteSkipped("tag-missing:" + e.name)
			return nil
		}
		// A tag without "expand" is one entry that drops a random member.
		st := Stack{Item: items[ctx.rng().Intn(len(items))], Count: 1}
		return applyFunctions([]Stack{st}, e.functions, ctx)

	case "alternatives":
		for _, c := range e.children {
			if checkConditions(c.conditions, ctx) {
				return applyFunctions(c.rollBody(ctx, depth+1), e.functions, ctx)
			}
		}
		return nil

	case "group":
		var out []Stack
		for _, c := range e.children {
			out = append(out, c.roll(ctx, depth+1)...)
		}
		return applyFunctions(out, e.functions, ctx)

	case "sequence":
		// Children run in order until one fails its conditions, and the
		// sequence stops there -- that is the difference from group.
		var out []Stack
		for _, c := range e.children {
			if !checkConditions(c.conditions, ctx) {
				break
			}
			out = append(out, c.rollBody(ctx, depth+1)...)
		}
		return applyFunctions(out, e.functions, ctx)

	case "loot_table":
		if e.inline != nil {
			return e.inline.roll(ctx, depth+1)
		}
		sub := lookup(e.ref)
		if sub == nil {
			noteSkipped("missing-table")
			return nil
		}
		return sub.roll(ctx, depth+1)
	}
	return nil
}
