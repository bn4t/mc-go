package loot

import (
	"strings"

	"mc-go/internal/data"
)

// checkConditions reports whether every condition in the list passes.
// An empty list passes, which is how an unconditional pool or entry is
// expressed.
func checkConditions(list []map[string]any, ctx *Context) bool {
	for _, c := range list {
		if !checkCondition(c, ctx) {
			return false
		}
	}
	return true
}

// checkCondition evaluates one condition.
//
// The default arm is the never-panic policy: a condition this
// interpreter has not heard of counts as satisfied, and the meeting is
// recorded in the skipped diagnostics. The alternative -- failing an
// unknown condition -- would silently delete drops the moment the data
// pack grew a construct, which is harder to notice than an extra drop.
func checkCondition(c map[string]any, ctx *Context) bool {
	kind := name(asStr(c["condition"]))
	switch kind {
	case "survives_explosion":
		// An explosion that reached the block destroys each drop with
		// probability 1/radius, so the drop survives with probability
		// 1/radius. The radius is carried in the loot context; without one
		// (an ordinary break) nothing decays.
		if ctx == nil || ctx.ExplosionRadius <= 0 {
			return true
		}
		return ctx.rng().Float64()*float64(ctx.ExplosionRadius) < 1

	case "random_chance":
		// "chance" may be paired with "looting_multiplier", which adds
		// looting_multiplier per point of luck.
		p := asFloat(c["chance"]) + asFloat(c["looting_multiplier"])*ctx.luck()
		return ctx.rng().Float64() < p

	case "random_chance_with_enchanted_bonus":
		// The chance is a level-based value: without the enchantment the
		// flat unenchanted_chance applies, with it the enchanted chance
		// is evaluated at the enchantment's level (which is what
		// minecraft:linear's "base"+per-level shape is for).
		level := ctx.Enchantments[name(asStr(c["enchantment"]))]
		if level > 0 {
			p := parseNumber(c["enchanted_chance"]).value(ctx.rng(), level)
			return ctx.rng().Float64() < p
		}
		return ctx.rng().Float64() < asFloat(c["unenchanted_chance"])

	case "table_bonus":
		// chances is indexed by enchantment level, clamped to the end of
		// the array: level 0 (no enchantment) uses chances[0], which is
		// why the arrays in the data pack start at the plain chance.
		level := ctx.Enchantments[name(asStr(c["enchantment"]))]
		chances := asList(c["chances"])
		if len(chances) == 0 {
			noteSkipped("malformed-condition:table_bonus")
			return true
		}
		if level >= len(chances) {
			level = len(chances) - 1
		}
		if level < 0 {
			level = 0
		}
		return ctx.rng().Float64() < asFloat(chances[level])

	case "match_tool":
		return matchTool(asMap(c["predicate"]), ctx)

	case "inverted":
		return !checkCondition(asMap(c["term"]), ctx)

	case "any_of":
		for _, t := range asList(c["terms"]) {
			if checkCondition(asMap(t), ctx) {
				return true
			}
		}
		return false

	case "all_of":
		for _, t := range asList(c["terms"]) {
			if !checkCondition(asMap(t), ctx) {
				return false
			}
		}
		return true

	case "killed_by_player":
		return ctx != nil && ctx.KilledByPlayer

	case "block_state_property":
		// The block id is only enforced when the caller said which block
		// was broken; the properties are always compared, because a
		// condition that names a property can only pass for a block that
		// has it with that value.
		if b := name(asStr(c["block"])); b != "" && ctx != nil && ctx.Block != "" && b != name(ctx.Block) {
			return false
		}
		for k, want := range asMap(c["properties"]) {
			got, ok := ctx.BlockProps[k]
			if !ok || got != asStr(want) {
				return false
			}
		}
		return true

	case "entity_properties":
		return entityProperties(asMap(c["predicate"]), asStr(c["entity"]), ctx)

	case "damage_source_properties":
		// TODO: the damage source is not part of our loot context yet, so
		// there is nothing to compare the predicate against: this
		// condition passes whatever the source was. The tables that use
		// it (lightning-killed turtles, frog-killed slimes and magma
		// cubes, fireball-killed ghasts) only gate bonus drops, so the
		// visible effect is an extra conditional drop, not a lost one.
		noteSkipped("unmodelled-condition:minecraft:damage_source_properties")
		return true

	case "location_check":
		// TODO: the loot context carries no position, so the predicate
		// (biome of the broken block, or the state of a neighbour at
		// offsetY) cannot be evaluated. Passing means the two tall-plant
		// tables double up their seed drops and the biome-gated chest
		// entries always qualify; a position-aware context is the fix.
		noteSkipped("unmodelled-condition:minecraft:location_check")
		return true
	}

	noteSkipped("unknown-condition:" + kind)
	return true
}

// matchTool evaluates a minecraft:match_tool predicate against the tool
// the caller reported. A predicate that names items tests the item
// name; a #tag tests tag membership. An unmodelled part of the
// predicate counts as unmatched, because ignoring it would hand out
// items the real server only gives for a specifically enchanted tool.
func matchTool(p map[string]any, ctx *Context) bool {
	if p == nil {
		return true
	}
	if v, has := p["items"]; has {
		if !toolMatches(v, ctx) {
			return false
		}
	}
	for key, v := range asMap(p["predicates"]) {
		switch name(key) {
		case "enchantments":
			if !enchantmentsMatch(v, ctx.Enchantments, ctx) {
				return false
			}
		default:
			noteSkipped("unmodelled-predicate:match_tool/" + name(key))
			return false
		}
	}
	return true
}

// toolMatches compares the tool item against an "items" field: a single
// item name, a "#tag", or a list of either.
func toolMatches(v any, ctx *Context) bool {
	if list, ok := v.([]any); ok {
		for _, item := range list {
			if toolMatches(item, ctx) {
				return true
			}
		}
		return false
	}
	want := asStr(v)
	if want == "" || ctx == nil {
		return false
	}
	if strings.HasPrefix(want, "#") {
		return toolHasTag(want, ctx)
	}
	return name(want) == name(ctx.Tool)
}

// toolHasTag answers "#minecraft:axes" for the context's tool. The
// caller's ToolTags win, because it may know about a tag the generated
// data pack table does not carry; otherwise the item tag registry is
// asked.
func toolHasTag(tag string, ctx *Context) bool {
	bare := name(strings.TrimPrefix(tag, "#"))
	for _, t := range ctx.ToolTags {
		if name(strings.TrimPrefix(t, "#")) == bare {
			return true
		}
	}
	return data.HasItemTag(bare, name(ctx.Tool))
}

// enchantmentsMatch evaluates a list of enchantment predicates, which
// the format ANDs together: [{enchantments: name-or-#tag,
// levels: {min, max}}]. A predicate without levels means "has it at
// all", i.e. level >= 1.
func enchantmentsMatch(v any, ench map[string]int, ctx *Context) bool {
	for _, e := range asList(v) {
		em := asMap(e)
		want := asStr(em["enchantments"])
		minLevel, maxLevel := 1, 255
		if lm := asMap(em["levels"]); lm != nil {
			if x, has := lm["min"]; has {
				minLevel = toInt(asFloat(x))
			}
			if x, has := lm["max"]; has {
				maxLevel = toInt(asFloat(x))
			}
		}
		if strings.HasPrefix(want, "#") {
			// An enchantment tag: resolve it to its members, since the
			// context maps enchantment names, not tag names.
			level := 0
			for _, member := range enchantTagMembers(want, ctx) {
				if l := ench[member]; l > level {
					level = l
				}
			}
			if level < minLevel || level > maxLevel || level == 0 {
				return false
			}
			continue
		}
		level, ok := ench[name(want)]
		if !ok || level < minLevel || level > maxLevel {
			return false
		}
	}
	return true
}

// builtinEnchantTags holds the enchantment tags the vanilla loot tables
// name. They live in the bundled data pack
// (data/minecraft/tags/enchantment/smelts_loot.json) but internal/data
// does not generate an enchantment-tag registry, so the single tag the
// tables actually use is inlined here. Context.EnchantTags overrides
// it for a caller that knows better.
// TODO: generate the enchantment tag registry and drop this table.
var builtinEnchantTags = map[string][]string{
	"smelts_loot": {"fire_aspect"},
}

func enchantTagMembers(tag string, ctx *Context) []string {
	bare := name(strings.TrimPrefix(tag, "#"))
	if ctx != nil {
		if members, ok := ctx.EnchantTags[bare]; ok {
			return members
		}
	}
	if members, ok := builtinEnchantTags[bare]; ok {
		return members
	}
	noteSkipped("enchantment-tag-missing:" + bare)
	return nil
}

// entityProperties evaluates a minecraft:entity_properties predicate
// against whichever entity the condition names: "this" is the entity
// being looted, the attacker roles are the killer. The caller describes
// them through the Context fields; a fact the caller did not supply is
// "unknown" and fails the matching test rather than passing it, because
// the unmodelled shapes in the real tables (a smelting weapon on the
// killer, a sheep's colour) gate conditional extras -- letting them
// pass would turn every bare-handed pig kill into cooked porkchop.
func entityProperties(p map[string]any, role string, ctx *Context) bool {
	if ctx == nil {
		ctx = &Context{}
	}
	if len(p) == 0 {
		// An empty predicate matches every entity of that role.
		return true
	}
	switch name(role) {
	case "", "this":
		return entityPredicateParts(p, ctx.EntityType, ctx.EntityOnFire, ctx.Enchantments, ctx, "this")
	case "attacker", "direct_attacker", "killer", "direct_killer":
		ench := ctx.AttackerEnchantments
		if ench == nil {
			// For a mob death the tool in Enchantments is the killer's
			// weapon, so it is the same fact under another name.
			ench = ctx.Enchantments
		}
		// Nothing tells us whether the killer was burning, so the
		// on-fire flag stays unknown for this role.
		return entityPredicateParts(p, ctx.AttackerType, false, ench, ctx, "attacker")
	default:
		noteSkipped("unmodelled-predicate:entity_properties/" + name(role))
		return false
	}
}

// entityPredicateParts tests the sub-predicates of an entity predicate
// that this interpreter models. Anything else is recorded and fails.
func entityPredicateParts(p map[string]any, typ string, onFire bool, ench map[string]int, ctx *Context, role string) bool {
	for key, v := range p {
		switch name(key) {
		case "entity_type":
			want := asStr(v)
			if strings.HasPrefix(want, "#") {
				// Entity type tags are not in internal/data yet, so the
				// membership cannot be decided. The tables that use one
				// (skeleton-killed-creepers) lose their bonus disc.
				noteSkipped("unmodelled-predicate:entity_properties/entity_type-tag")
				return false
			}
			if typ == "" || name(want) != name(typ) {
				return false
			}

		case "flags":
			for flag, want := range asMap(v) {
				if name(flag) != "is_on_fire" || role != "this" {
					// is_baby, is_sneaking, ... are facts the context
					// does not carry.
					noteSkipped("unmodelled-predicate:entity_properties/flags." + name(flag))
					return false
				}
				if asBool(want) != onFire {
					return false
				}
			}

		case "equipment":
			for slot, sv := range asMap(v) {
				if name(slot) != "mainhand" {
					noteSkipped("unmodelled-predicate:entity_properties/equipment." + name(slot))
					return false
				}
				for pkey, pv := range asMap(asMap(sv)["predicates"]) {
					if name(pkey) != "enchantments" {
						noteSkipped("unmodelled-predicate:entity_properties/equipment/" + name(pkey))
						return false
					}
					if !enchantmentsMatch(pv, ench, ctx) {
						return false
					}
				}
			}

		default:
			// minecraft:components (a sheep's colour), type_specific/...
			// (sheared, cube size, raider captain), vehicle, tags: all
			// facts the loot context does not carry yet.
			noteSkipped("unmodelled-predicate:entity_properties/" + name(key))
			return false
		}
	}
	return true
}
