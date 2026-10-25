package loot

import (
	"strconv"

	"mc-go/internal/data"
)

// The decoded JSON of one loot table becomes this tree: a table holds
// pools, a pool holds entries, an entry may hold children and item
// functions, and every "count"/"rolls" field becomes a number provider.
// Conditions and functions stay as their raw decoded maps because they
// are small, heterogeneous and only interpreted at roll time; keeping
// them raw is what lets an unrecognised one be counted and skipped
// instead of failing a decode.
//
// Parsing happens once per table and the result is never mutated, so
// the tree can be shared by every goroutine that rolls it.

// table is defined in loot.go; these are the pieces below it.

type pool struct {
	rolls      *number
	bonusRolls *number
	conditions []map[string]any
	entries    []*entry
}

type entry struct {
	kind       string // item, tag, alternatives, group, sequence, empty, loot_table, dynamic
	name       string // item name, tag name or dynamic name
	expand     bool   // tag entries: expand into one child per tag member
	weight     int32
	quality    int32
	conditions []map[string]any
	functions  []map[string]any
	children   []*entry
	ref        string // loot_table entries: the referenced table
	inline     *table // loot_table entries: an inline table definition
}

// parseTable builds the tree for one decoded table.
func parseTable(raw map[string]any) *table {
	t := &table{kind: name(asStr(raw["type"]))}
	for _, p := range asList(raw["pools"]) {
		pm, ok := p.(map[string]any)
		if !ok {
			noteSkipped("malformed-pool")
			continue
		}
		t.pools = append(t.pools, parsePool(pm))
	}
	return t
}

func parsePool(m map[string]any) pool {
	p := pool{conditions: asConditions(m["conditions"])}
	p.rolls = parseNumber(m["rolls"])
	if p.rolls == nil {
		// A pool without rolls rolls once, which is what the data pack
		// means by an absent field.
		p.rolls = &number{kind: numConstant, cval: 1}
	}
	p.bonusRolls = parseNumber(m["bonus_rolls"])
	for _, e := range asList(m["entries"]) {
		em, ok := e.(map[string]any)
		if !ok {
			noteSkipped("malformed-entry")
			continue
		}
		p.entries = append(p.entries, parseEntry(em)...)
	}
	return p
}

// parseEntry decodes one entry. It returns a list because a tag entry
// with "expand": true is not one entry at all: vanilla expands it into
// one child per tag member so the pool's weighted draw can pick a single
// member (a creeper's music disc comes out of a one-roll pool holding an
// expanded tag). The caller splices what it gets into the list it is
// building.
func parseEntry(m map[string]any) []*entry {
	e := &entry{
		kind:       name(asStr(m["type"])),
		name:       name(asStr(m["name"])),
		expand:     asBool(m["expand"]),
		weight:     int32(asFloat(m["weight"])),
		quality:    int32(asFloat(m["quality"])),
		conditions: asConditions(m["conditions"]),
	}
	if _, has := m["weight"]; !has {
		// weight defaults to 1: every entry is equally likely until the
		// table says otherwise.
		e.weight = 1
	}
	for _, f := range asList(m["functions"]) {
		fm, ok := f.(map[string]any)
		if !ok {
			noteSkipped("malformed-function")
			continue
		}
		e.functions = append(e.functions, fm)
	}
	switch e.kind {
	case "item", "empty", "dynamic":
		// Leaf entries; nothing further to decode.
	case "tag":
		if e.expand {
			return expandTag(e)
		}
	case "alternatives", "group", "sequence":
		for _, c := range asList(m["children"]) {
			cm, ok := c.(map[string]any)
			if !ok {
				noteSkipped("malformed-entry")
				continue
			}
			e.children = append(e.children, parseEntry(cm)...)
		}
	case "loot_table":
		switch v := m["value"].(type) {
		case string:
			e.ref = tableName(v)
		case map[string]any:
			// The value may be an inline table definition instead of a
			// reference; the data pack's own tables never do this, but
			// the format allows it.
			e.inline = parseTable(v)
		default:
			noteSkipped("unknown-entry:loot_table-value")
		}
	default:
		noteSkipped("unknown-entry:" + e.kind)
		e.kind = "empty"
	}
	return []*entry{e}
}

// expandTag turns a tag entry with "expand": true into one item child
// per tag member. Vanilla expands the entry the same way so the pool's
// weighted draw can pick a member, and the wiki notes that the
// expansion drops the entry's item functions, which this reproduces
// (each expanded child carries the conditions, weight and quality, and
// no functions).
func expandTag(e *entry) []*entry {
	items := data.ItemTags[e.name]
	if len(items) == 0 {
		noteSkipped("tag-missing:" + e.name)
		return nil
	}
	out := make([]*entry, 0, len(items))
	for _, it := range items {
		out = append(out, &entry{
			kind:       "item",
			name:       it,
			weight:     e.weight,
			quality:    e.quality,
			conditions: e.conditions,
		})
	}
	return out
}

// asConditions lifts a "conditions" field into a list of decoded
// condition maps.
func asConditions(v any) []map[string]any {
	list := asList(v)
	if len(list) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, c := range list {
		if cm, ok := c.(map[string]any); ok {
			out = append(out, cm)
		} else {
			noteSkipped("malformed-condition")
		}
	}
	return out
}

// ---------------------------------------------------------------------
// number providers

// Number provider kinds. The constant kind also covers a bare number in
// the JSON ("rolls": 1.0) and a constant object.
const (
	numConstant = "constant"
	numUniform  = "uniform"
	numBinomial = "binomial"
	numLinear   = "linear"
	numUnknown  = "unknown"
)

// number is a loot table number provider: a constant, an inclusive
// uniform range, a binomial count, or a linear level-based value.
//
// The uniform bounds are providers themselves because the format allows
// nesting them, even though no vanilla table does; parsing them that
// way keeps the recursion harmless.
type number struct {
	kind string
	cval float64 // constant

	lo, hi *number // uniform

	n int     // binomial: trials
	p float64 // binomial: per-trial probability

	base     float64 // linear: value at level 1
	perLevel float64 // linear: added for each level above the first
}

// value evaluates the provider as a float. level is the enchantment
// level in scope for level-based providers (minecraft:linear); it is
// zero everywhere except inside a random_chance_with_enchanted_bonus
// condition, which is the only place vanilla uses linear.
//
// A uniform float is drawn from [min, max), so it is strictly below
// max; an integer draw is inclusive of both ends (see intValue). That
// split is what the number provider documentation describes and what
// the real tables' ranges require: a pig's set_count of uniform 1..3
// has to be able to roll a 3.
func (n *number) value(rng RNG, level int) float64 {
	if n == nil {
		return 0
	}
	switch n.kind {
	case numConstant:
		return n.cval
	case numUniform:
		lo, hi := n.lo.value(rng, level), n.hi.value(rng, level)
		if hi <= lo {
			return lo
		}
		return lo + rng.Float64()*(hi-lo)
	case numBinomial:
		return float64(n.sample(rng))
	case numLinear:
		// "base" is the value at the first level; each level above it
		// adds "per_level_above_first". Level 0 (no enchantment) never
		// reaches here in vanilla, because the condition picks the
		// unenchanted chance first, so base is the sane answer there.
		if level <= 1 {
			return n.base
		}
		return n.base + n.perLevel*float64(level-1)
	}
	return 0
}

// intValue evaluates the provider as an integer, which is the form
// "rolls" and a stack count need.
func (n *number) intValue(rng RNG, level int) int {
	if n == nil {
		return 0
	}
	switch n.kind {
	case numConstant:
		return toInt(n.cval)
	case numUniform:
		lo, hi := toInt(n.lo.value(rng, level)), toInt(n.hi.value(rng, level))
		if hi <= lo {
			return lo
		}
		return lo + rng.Intn(hi-lo+1)
	case numBinomial:
		return n.sample(rng)
	}
	return toInt(n.value(rng, level))
}

// maxTrials caps a binomial trial count. The vanilla tables never ask
// for more than three, and the cap is what keeps a malformed table from
// spinning the tick away.
const maxTrials = 4096

// sample draws the binomial count: n independent trials that each
// succeed with probability p. Both the float and the integer reading of
// a binomial provider are this same count, as the number provider
// documentation states.
func (n *number) sample(rng RNG) int {
	trials := n.n
	if trials > maxTrials {
		trials = maxTrials
	}
	if trials <= 0 {
		return 0
	}
	c := 0
	for i := 0; i < trials; i++ {
		if rng.Float64() < n.p {
			c++
		}
	}
	return c
}

// parseNumber decodes a number provider, returning nil when the field
// is absent. A bare number is a constant, and an object with only
// min/max is the uniform shorthand the older data packs use.
func parseNumber(v any) *number {
	switch x := v.(type) {
	case nil:
		return nil
	case float64:
		return &number{kind: numConstant, cval: x}
	case string:
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return &number{kind: numConstant, cval: f}
		}
		noteSkipped("unknown-number:string")
		return &number{kind: numUnknown}
	case map[string]any:
		kind := name(asStr(x["type"]))
		if kind == "" {
			kind = numUniform
		}
		switch kind {
		case numConstant:
			return &number{kind: numConstant, cval: asFloat(x["value"])}
		case numUniform:
			lo, hi := parseNumber(x["min"]), parseNumber(x["max"])
			if lo == nil {
				lo = &number{kind: numConstant}
			}
			if hi == nil {
				hi = &number{kind: numConstant}
			}
			return &number{kind: numUniform, lo: lo, hi: hi}
		case numBinomial:
			return &number{kind: numBinomial, n: toInt(asFloat(x["n"])), p: asFloat(x["p"])}
		case numLinear:
			return &number{kind: numLinear, base: asFloat(x["base"]), perLevel: asFloat(x["per_level_above_first"])}
		default:
			// Score providers and anything invented later read as zero.
			// Counts, not scores, are what this interpreter is for.
			noteSkipped("unknown-number:" + kind)
			return &number{kind: numUnknown}
		}
	}
	noteSkipped("unknown-number:malformed")
	return &number{kind: numUnknown}
}
