package worldgen

import (
	"math"
	"sort"
	"strings"
	"sync"

	"mc-go/internal/data"
)

// Reading the generated feature data trees (features_gen.go): objects,
// lists, numbers, strings and booleans as the data pack encodes them.

func fobj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func fhas(v any, key string) bool {
	_, ok := fobj(v)[key]
	return ok
}

func ffield(v any, key string) any { return fobj(v)[key] }

func fnum(v any, def float64) float64 {
	if n, ok := v.(float64); ok {
		return n
	}
	return def
}

func fint(v any, def int32) int32 { return int32(fnum(v, float64(def))) }

func ffloat(v any, def float32) float32 { return float32(fnum(v, float64(def))) }

func fstr(v any) string {
	s, _ := v.(string)
	return s
}

func fbool(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

func flist(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case nil:
		return nil
	}
	return []any{v}
}

func ftype(v any) string { return fstr(ffield(v, "type")) }

// blockStateOf reads a block state object: its block and properties,
// unlisted properties at the block's defaults.
func blockStateOf(v any) StateID {
	name := fstr(ffield(v, "Name"))
	s := defaultState(name)
	for k, val := range fobj(ffield(v, "Properties")) {
		s = s.with(k, fstr(val))
	}
	return s
}

// Block tags, expanded by the generated data.

var (
	tagSetsMu sync.Mutex
	tagSets   = map[string]map[string]bool{}
)

func blockTagSet(tag string) map[string]bool {
	tag = strings.TrimPrefix(strings.TrimPrefix(tag, "#"), "minecraft:")
	tagSetsMu.Lock()
	defer tagSetsMu.Unlock()
	set, ok := tagSets[tag]
	if !ok {
		set = map[string]bool{}
		for _, b := range data.BlockTags[tag] {
			set[b] = true
		}
		tagSets[tag] = set
	}
	return set
}

func (s StateID) inTag(tag string) bool { return blockTagSet(tag)[s.block()] }

// Integer providers.

type intProvider interface {
	sample(r Random) int32
	minValue() int32
	maxValue() int32
}

type constInt int32

func (c constInt) sample(Random) int32 { return int32(c) }
func (c constInt) minValue() int32     { return int32(c) }
func (c constInt) maxValue() int32     { return int32(c) }

type uniformInt struct{ lo, hi int32 }

func (u uniformInt) sample(r Random) int32 { return randomBetweenInclusive(r, u.lo, u.hi) }
func (u uniformInt) minValue() int32       { return u.lo }
func (u uniformInt) maxValue() int32       { return u.hi }

type biasedInt struct{ lo, hi int32 }

func (b biasedInt) sample(r Random) int32 {
	return b.lo + r.NextIntn(r.NextIntn(b.hi-b.lo+1)+1)
}
func (b biasedInt) minValue() int32 { return b.lo }
func (b biasedInt) maxValue() int32 { return b.hi }

type clampedInt struct {
	source intProvider
	lo, hi int32
}

func (c clampedInt) sample(r Random) int32 { return min(max(c.source.sample(r), c.lo), c.hi) }
func (c clampedInt) minValue() int32       { return max(c.lo, c.source.minValue()) }
func (c clampedInt) maxValue() int32       { return min(c.hi, c.source.maxValue()) }

type clampedNormalInt struct {
	mean, deviation float32
	lo, hi          int32
}

func (c clampedNormalInt) sample(r Random) int32 {
	v := c.mean + float32(float32(r.NextGaussian())*c.deviation)
	return int32(min(max(v, float32(c.lo)), float32(c.hi)))
}
func (c clampedNormalInt) minValue() int32 { return c.lo }
func (c clampedNormalInt) maxValue() int32 { return c.hi }

type weightedInt struct {
	entries []weightedEntry[intProvider]
	total   int32
}

func (w weightedInt) sample(r Random) int32 { return pickWeighted(r, w.entries, w.total).sample(r) }
func (w weightedInt) minValue() int32 {
	v := int32(math.MaxInt32)
	for _, e := range w.entries {
		v = min(v, e.value.minValue())
	}
	return v
}
func (w weightedInt) maxValue() int32 {
	v := int32(math.MinInt32)
	for _, e := range w.entries {
		v = max(v, e.value.maxValue())
	}
	return v
}

type trapezoidInt struct{ lo, hi, plateau int32 }

func (t trapezoidInt) sample(r Random) int32 {
	if t.plateau == 0 && t.hi == -t.lo {
		return r.NextIntn(t.hi+1) - r.NextIntn(t.hi+1)
	}
	span := t.hi - t.lo
	if t.plateau == span {
		return randomBetweenInclusive(r, t.lo, t.hi)
	}
	ramp := (span - t.plateau) / 2
	v := t.lo + randomBetweenInclusive(r, 0, span-ramp)
	return v + randomBetweenInclusive(r, 0, ramp)
}
func (t trapezoidInt) minValue() int32 { return t.lo }
func (t trapezoidInt) maxValue() int32 { return t.hi }

type weightedEntry[T any] struct {
	value  T
	weight int32
}

// pickWeighted draws an entry with chance proportional to its weight.
func pickWeighted[T any](r Random, entries []weightedEntry[T], total int32) T {
	i := r.NextIntn(total)
	for _, e := range entries {
		i -= e.weight
		if i < 0 {
			return e.value
		}
	}
	panic("worldgen: weighted pick out of range")
}

func randomBetweenInclusive(r Random, lo, hi int32) int32 { return r.NextIntn(hi-lo+1) + lo }

func decodeIntProvider(v any) intProvider {
	if n, ok := v.(float64); ok {
		return constInt(int32(n))
	}
	switch ftype(v) {
	case "minecraft:constant":
		return constInt(fint(ffield(v, "value"), 0))
	case "minecraft:uniform":
		return uniformInt{fint(ffield(v, "min_inclusive"), 0), fint(ffield(v, "max_inclusive"), 0)}
	case "minecraft:biased_to_bottom":
		return biasedInt{fint(ffield(v, "min_inclusive"), 0), fint(ffield(v, "max_inclusive"), 0)}
	case "minecraft:clamped":
		return clampedInt{decodeIntProvider(ffield(v, "source")), fint(ffield(v, "min_inclusive"), 0), fint(ffield(v, "max_inclusive"), 0)}
	case "minecraft:clamped_normal":
		return clampedNormalInt{ffloat(ffield(v, "mean"), 0), ffloat(ffield(v, "deviation"), 0),
			fint(ffield(v, "min_inclusive"), 0), fint(ffield(v, "max_inclusive"), 0)}
	case "minecraft:weighted_list":
		var w weightedInt
		for _, e := range flist(ffield(v, "distribution")) {
			wt := fint(ffield(e, "weight"), 1)
			w.entries = append(w.entries, weightedEntry[intProvider]{decodeIntProvider(ffield(e, "data")), wt})
			w.total += wt
		}
		return w
	case "minecraft:trapezoid":
		return trapezoidInt{fint(ffield(v, "min"), 0), fint(ffield(v, "max"), 0), fint(ffield(v, "plateau"), 0)}
	}
	unsupported("int provider %v", ftype(v))
	return nil
}

// Float providers.

type clampedNormalFloat struct{ mean, deviation, lo, hi float32 }

func decodeFloatProvider(v any) FloatProvider {
	if n, ok := v.(float64); ok {
		return floatConst(float32(n))
	}
	switch ftype(v) {
	case "minecraft:constant":
		return floatConst(ffloat(ffield(v, "value"), 0))
	case "minecraft:uniform":
		return floatUniform(ffloat(ffield(v, "min_inclusive"), 0), ffloat(ffield(v, "max_exclusive"), 0))
	case "minecraft:trapezoid":
		return floatTrapezoid(ffloat(ffield(v, "min"), 0), ffloat(ffield(v, "max"), 0), ffloat(ffield(v, "plateau"), 0))
	}
	unsupported("float provider %v", ftype(v))
	return FloatProvider{}
}

// Vertical anchors and height providers.

func decodeAnchor(v any) VerticalAnchor {
	switch {
	case fhas(v, "absolute"):
		return anchor(anchorAbsolute, fint(ffield(v, "absolute"), 0))
	case fhas(v, "above_bottom"):
		return anchor(anchorAboveBottom, fint(ffield(v, "above_bottom"), 0))
	case fhas(v, "below_top"):
		return anchor(anchorBelowTop, fint(ffield(v, "below_top"), 0))
	}
	unsupported("anchor %v", v)
	return VerticalAnchor{}
}

// heightSampler draws a Y in a world of the given generation bounds.
type heightSampler interface {
	sample(r Random, minY, height int32) int32
}

type constHeight VerticalAnchor

func (c constHeight) sample(_ Random, minY, height int32) int32 {
	return VerticalAnchor(c).resolve(minY, height)
}

type trapezoidHeight struct {
	lo, hi  VerticalAnchor
	plateau int32
}

func (t trapezoidHeight) sample(r Random, minY, height int32) int32 {
	lo, hi := t.lo.resolve(minY, height), t.hi.resolve(minY, height)
	if lo > hi {
		return lo
	}
	span := hi - lo
	if t.plateau >= span {
		return randomBetweenInclusive(r, lo, hi)
	}
	ramp := (span - t.plateau) / 2
	v := lo + randomBetweenInclusive(r, 0, span-ramp)
	return v + randomBetweenInclusive(r, 0, ramp)
}

type biasedHeight struct {
	lo, hi VerticalAnchor
	inner  int32
	very   bool
}

func (b biasedHeight) sample(r Random, minY, height int32) int32 {
	lo, hi := b.lo.resolve(minY, height), b.hi.resolve(minY, height)
	if hi-lo-b.inner+1 <= 0 {
		return lo
	}
	if !b.very {
		return lo + r.NextIntn(r.NextIntn(hi-lo-b.inner+1)+b.inner)
	}
	a := mthNextInt(r, lo+b.inner, hi)
	c := mthNextInt(r, lo, a-1)
	return mthNextInt(r, lo, c-1+b.inner)
}

// mthNextInt is a uniform int in [lo, hi], lo itself when the range is
// empty.
func mthNextInt(r Random, lo, hi int32) int32 {
	if lo >= hi {
		return lo
	}
	return r.NextIntn(hi-lo+1) + lo
}

type weightedHeight struct {
	entries []weightedEntry[heightSampler]
	total   int32
}

func (w weightedHeight) sample(r Random, minY, height int32) int32 {
	return pickWeighted(r, w.entries, w.total).sample(r, minY, height)
}

func decodeHeightProvider(v any) heightSampler {
	if fhas(v, "absolute") || fhas(v, "above_bottom") || fhas(v, "below_top") {
		return constHeight(decodeAnchor(v))
	}
	switch ftype(v) {
	case "minecraft:constant":
		return constHeight(decodeAnchor(ffield(v, "value")))
	case "minecraft:uniform":
		return heightUniform(decodeAnchor(ffield(v, "min_inclusive")), decodeAnchor(ffield(v, "max_inclusive")))
	case "minecraft:trapezoid":
		return trapezoidHeight{decodeAnchor(ffield(v, "min_inclusive")), decodeAnchor(ffield(v, "max_inclusive")), fint(ffield(v, "plateau"), 0)}
	case "minecraft:biased_to_bottom", "minecraft:very_biased_to_bottom":
		return biasedHeight{decodeAnchor(ffield(v, "min_inclusive")), decodeAnchor(ffield(v, "max_inclusive")),
			fint(ffield(v, "inner"), 1), ftype(v) == "minecraft:very_biased_to_bottom"}
	case "minecraft:weighted_list":
		var w weightedHeight
		for _, e := range flist(ffield(v, "distribution")) {
			wt := fint(ffield(e, "weight"), 1)
			w.entries = append(w.entries, weightedEntry[heightSampler]{decodeHeightProvider(ffield(e, "data")), wt})
			w.total += wt
		}
		return w
	}
	unsupported("height provider %v", ftype(v))
	return nil
}

// Rule tests (ore targets).

type ruleTest func(s StateID, r Random) bool

func decodeRuleTest(v any) ruleTest {
	switch fstr(ffield(v, "predicate_type")) {
	case "minecraft:always_true":
		return func(StateID, Random) bool { return true }
	case "minecraft:tag_match":
		tag := fstr(ffield(v, "tag"))
		return func(s StateID, _ Random) bool { return s.inTag(tag) }
	case "minecraft:block_match":
		block := strings.TrimPrefix(fstr(ffield(v, "block")), "minecraft:")
		return func(s StateID, _ Random) bool { return s.is(block) }
	case "minecraft:blockstate_match":
		want := blockStateOf(ffield(v, "block_state"))
		return func(s StateID, _ Random) bool { return s == want }
	case "minecraft:random_block_match":
		block := strings.TrimPrefix(fstr(ffield(v, "block")), "minecraft:")
		p := ffloat(ffield(v, "probability"), 0)
		return func(s StateID, r Random) bool { return s.is(block) && r.NextFloat() < p }
	}
	unsupported("rule test %v", fstr(ffield(v, "predicate_type")))
	return nil
}

// sortedKeys lists a map's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
