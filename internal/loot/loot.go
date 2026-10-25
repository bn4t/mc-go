// Package loot interprets the vanilla loot tables: the data-driven
// rules that decide what a broken block or a dead mob drops.
//
// The tables themselves are JSON from the bundled data pack, which
// internal/data publishes as data.LootTables. This package is the
// interpreter: it walks the pools, picks weighted entries, tests
// conditions and applies item functions.
//
// The semantics come from the data pack plus the public documentation
// (minecraft.wiki "Loot table", "Item modifier", "Number provider"),
// not from Mojang code. The less obvious points are called out at their
// sites: the luck term on entry weights, how a rolled fractional count
// becomes an item count, the three apply_bonus formulas, and the
// per-item survival roll behind explosion_decay. Where the wiki and
// the shape of the real tables were the only evidence, the comment says
// so.
//
// Policy for constructs this interpreter does not know. A loot roll
// runs inside a server tick, so it must never panic and never wedge
// anything; therefore an unknown condition counts as satisfied, an
// unknown function as a no-op, and an unknown entry or number provider
// as an empty result. Every such meeting is counted in a package-level
// map (Skipped) so drift between the real tables and this interpreter
// is visible rather than silent. Constructs that are known but not
// modelled yet -- component-only item functions, most entity predicate
// sub-tests, damage source and location predicates -- are counted there
// too under an "unmodelled-" prefix, and each has a comment naming the
// gap at its site.
package loot

import (
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"

	"mc-go/internal/data"
)

// Stack is one rolled item: an item name without the minecraft: prefix
// and a count of at least one.
type Stack struct {
	Item  string
	Count int32
}

// RNG abstracts randomness so a caller can make rolls deterministic.
// Implementations must be safe for concurrent use when the same
// Context is shared, because worlds tick from several goroutines.
type RNG interface {
	Float64() float64
	Intn(n int) int
}

// Context describes the circumstances of one roll: the luck of the
// player who caused it, the tool that did the work, the block state
// that was broken, and so on. A nil Context (or one with a nil RNG) is
// usable: the roll then uses the package's shared random source and
// treats every optional fact as unknown.
type Context struct {
	RNG  RNG
	Luck float32

	// Tool is the item name (no prefix) of the tool or weapon used, or
	// "" for a bare hand. ToolTags lists the item tags it belongs to, in
	// wire form ("minecraft:axes"); it is only consulted for "#tag" tool
	// predicates when the tag lookup in data.ItemTags cannot answer.
	Tool     string
	ToolTags []string

	// Enchantments maps an enchantment name (no prefix) to its level on
	// the tool. Looting-style conditions and the enchantment-count
	// functions read it.
	Enchantments map[string]int

	// Block is the block id that was broken (no prefix) and BlockProps
	// its state properties, consulted by block_state_property. The
	// properties are checked on their own even when Block is "".
	Block      string
	BlockProps map[string]string

	// ExplosionRadius is > 0 when the block was destroyed by an
	// explosion, which is what makes survives_explosion and
	// explosion_decay do anything.
	ExplosionRadius float32

	KilledByPlayer bool
	// EntityOnFire reports that the looted entity died on fire, the
	// second half of the furnace_smelt path for food drops.
	EntityOnFire bool

	// EntityType is the type id (no prefix) of the entity being looted
	// and AttackerType the type id of whatever killed it; both are read
	// by entity_properties. Empty means "not known", in which case an
	// entity_type test fails rather than silently passing.
	EntityType   string
	AttackerType string

	// AttackerEnchantments are the mainhand enchantments of the killer,
	// for the entity_properties equipment tests (the smelts_loot and
	// fire-aspect style predicates). It falls back to Enchantments,
	// because for a mob death the tool in Enchantments is the killer's
	// weapon anyway.
	AttackerEnchantments map[string]int

	// EnchantTags resolves enchantment tags named in an enchantment
	// predicate ("#minecraft:smelts_loot" -> the enchantments in it).
	// Callers that know the registry can fill it in; otherwise the small
	// built-in table of tags the vanilla tables actually use applies.
	EnchantTags map[string][]string
}

// rng returns the context's random source, or the shared one when the
// caller did not supply any.
func (c *Context) rng() RNG {
	if c != nil && c.RNG != nil {
		return c.RNG
	}
	return sharedRNG{}
}

// luck is the luck value that entry quality and bonus rolls scale by.
func (c *Context) luck() float64 {
	if c == nil {
		return 0
	}
	return float64(c.Luck)
}

// sharedRNG is the fallback source. math/rand/v2's package-level
// functions are safe for concurrent use, which is what Roll promises.
type sharedRNG struct{}

func (sharedRNG) Float64() float64 { return rand.Float64() }
func (sharedRNG) Intn(n int) int   { return rand.IntN(n) }

// table is a parsed loot table. Parsed tables are immutable, which is
// what makes them safe to share between goroutines.
type table struct {
	kind  string // "block", "entity", "chest", ... (diagnostics only)
	pools []pool
}

var (
	tablesMu  sync.Mutex
	parsed    = map[string]*table{}
	overrides = map[string]map[string]any{}
)

// RegisterTable installs a loot table definition, replacing any table
// of that name from the data pack. It exists so tests (and, later, data
// packs) can roll a table that is not in the vanilla data pack; the
// definition is the same JSON shape as the data pack's
// data/minecraft/loot_table files, decoded into map[string]any.
func RegisterTable(name string, def map[string]any) {
	name = tableName(name)
	tablesMu.Lock()
	defer tablesMu.Unlock()
	overrides[name] = def
	delete(parsed, name)
}

// lookup returns the parsed table of that name, parsing it on first
// use. The returned table is shared and must not be mutated.
func lookup(name string) *table {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	if t, ok := parsed[name]; ok {
		return t
	}
	raw, ok := overrides[name]
	if !ok {
		raw, ok = data.LootTables()[name]
	}
	if !ok {
		return nil
	}
	t := parseTable(raw)
	parsed[name] = t
	return t
}

// Tables returns the names of the loaded tables, sorted, for
// diagnostics. It decodes the whole data pack blob, so it is not on any
// hot path.
func Tables() []string {
	all := data.LootTables()
	out := make([]string, 0, len(all)+len(overrides))
	for name := range all {
		out = append(out, name)
	}
	tablesMu.Lock()
	for name := range overrides {
		out = append(out, name)
	}
	tablesMu.Unlock()
	sort.Strings(out)
	return out
}

// Roll evaluates the named table once. One call is one block break or
// one mob death: the returned stacks are what drops, already filtered
// down to positive counts, and one stack per rolled entry -- identical
// items are not merged, which is the list vanilla hands its caller. A
// table that does not exist rolls nothing.
//
// Roll is safe for concurrent use. A caller that shares one RNG across
// goroutines must make that RNG safe too.
func Roll(table string, ctx *Context) []Stack {
	if ctx == nil {
		// Every other function in the package may then assume a context.
		ctx = &Context{}
	}
	t := lookup(tableName(table))
	if t == nil {
		noteSkipped("missing-table")
		return nil
	}
	out := t.roll(ctx, 0)
	keep := out[:0]
	for _, s := range out {
		if s.Item != "" && s.Count > 0 {
			keep = append(keep, s)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	return keep
}

// roll evaluates every pool in order. depth bounds loot_table
// recursion, which the data pack format forbids but a broken table
// could still express.
func (t *table) roll(ctx *Context, depth int) []Stack {
	if t == nil {
		return nil
	}
	if depth > maxDepth {
		noteSkipped("recursion-limit")
		return nil
	}
	var out []Stack
	for i := range t.pools {
		out = append(out, t.pools[i].roll(ctx, depth)...)
	}
	return out
}

// maxDepth is the loot_table nesting limit. The vanilla tables nest two
// deep at most; anything past this is a cycle and is cut off rather
// than allowed to recurse forever.
const maxDepth = 16

// ---------------------------------------------------------------------
// skipped-construct diagnostics

var (
	skippedMu sync.Mutex
	skipped   = map[string]int64{}
)

// noteSkipped records that the interpreter met a construct it did not
// fully understand or does not model yet. The key says which and where:
// "missing-table", "recursion-limit", "unknown-condition:<name>",
// "unknown-function:<name>", "unknown-entry:<name>",
// "unknown-number:<name>", "unknown-formula:<name>",
// "unmodelled-function:<name>", "unmodelled-predicate:<name>",
// "tag-missing:<tag>".
func noteSkipped(key string) {
	skippedMu.Lock()
	skipped[key]++
	skippedMu.Unlock()
}

// Skipped returns a snapshot of the diagnostics: every construct the
// interpreter has met that it does not model, and how often. A nonzero
// entry is a parity gap the caller may want to look at; an entry that
// shows up in a roll where the real server would have done something
// else is a bug report.
func Skipped() map[string]int64 {
	skippedMu.Lock()
	defer skippedMu.Unlock()
	out := make(map[string]int64, len(skipped))
	for k, v := range skipped {
		out[k] = v
	}
	return out
}

// ResetSkipped clears the diagnostics, for tests that want a clean
// slate.
func ResetSkipped() {
	skippedMu.Lock()
	skipped = map[string]int64{}
	skippedMu.Unlock()
}

// ---------------------------------------------------------------------
// small helpers over the decoded JSON

// name strips the minecraft: namespace, leaving other namespaces alone
// so a foreign item id is never silently rewritten.
func name(s string) string { return strings.TrimPrefix(s, "minecraft:") }

// tableName normalises a table reference: with or without the
// minecraft: namespace, with or without a trailing .json, with or
// without a leading slash.
func tableName(s string) string {
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, ".json")
	return name(s)
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asList(v any) []any         { l, _ := v.([]any); return l }
func asStr(v any) string         { s, _ := v.(string); return s }
func asFloat(v any) float64      { f, _ := v.(float64); return f }
func asBool(v any) bool          { b, _ := v.(bool); return b }

// toInt converts a rolled float to an item count the way the data pack
// documents it: integer results default to the float rounded with
// floor(x + 0.5). That rounding rule is what makes looting's
// "half an item per level on average" come out right, so it is not
// interchangeable with a plain truncation.
func toInt(f float64) int { return int(math.Floor(f + 0.5)) }
