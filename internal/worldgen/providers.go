package worldgen

import (
	"encoding/json"
	"strings"
)

// genCtx is WorldGenerationContext: the generation floor and depth
// vertical anchors resolve against.
type genCtx struct{ minY, genDepth int32 }

func (g genCtx) anchor(raw json.RawMessage) int32 { return resolveAnchor(raw, g.minY, g.genDepth) }

// floatProvider is a FloatProvider.
type floatProvider func(r Random) float32

// intProvider is an IntProvider.
type intProvider func(r Random) int32

// heightProvider is a HeightProvider.
type heightProvider func(r Random, g genCtx) int32

func isNumber(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && (s[0] == '-' || (s[0] >= '0' && s[0] <= '9'))
}

func parseFloatProvider(raw json.RawMessage) floatProvider {
	if isNumber(raw) {
		v := parseF32(raw)
		return func(Random) float32 { return v }
	}
	var o obj
	jsonUnmarshal(raw, &o)
	switch trimNS(o.str("type")) {
	case "constant":
		v := parseF32(o["value"])
		return func(Random) float32 { return v }
	case "uniform":
		lo, hi := parseF32(o["min_inclusive"]), parseF32(o["max_exclusive"])
		return func(r Random) float32 { return r.NextFloat()*(hi-lo) + lo }
	case "trapezoid":
		lo, hi, plateau := parseF32(o["min"]), parseF32(o["max"]), parseF32(o["plateau"])
		return func(r Random) float32 {
			f := hi - lo
			g := (f - plateau) / 2
			h := f - g
			return lo + float32(r.NextFloat()*h) + float32(r.NextFloat()*g)
		}
	case "clamped_normal":
		mean, dev, lo, hi := parseF32(o["mean"]), parseF32(o["deviation"]), parseF32(o["min"]), parseF32(o["max"])
		return func(r Random) float32 {
			return clampF(mean+float32(float32(r.NextGaussian())*dev), lo, hi)
		}
	}
	panic("worldgen: unknown float provider " + o.str("type"))
}

func clampI(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func randBetweenInclusive(r Random, lo, hi int32) int32 { return r.NextIntN(hi-lo+1) + lo }

func mthNextInt(r Random, lo, hi int32) int32 {
	if lo >= hi {
		return lo
	}
	return r.NextIntN(hi-lo+1) + lo
}

func parseIntProvider(raw json.RawMessage) intProvider {
	if isNumber(raw) {
		v := int32(obj{"v": raw}.f64("v"))
		return func(Random) int32 { return v }
	}
	var o obj
	jsonUnmarshal(raw, &o)
	switch trimNS(o.str("type")) {
	case "constant":
		v := o.i32("value")
		return func(Random) int32 { return v }
	case "uniform":
		lo, hi := o.i32("min_inclusive"), o.i32("max_inclusive")
		return func(r Random) int32 { return randBetweenInclusive(r, lo, hi) }
	case "biased_to_bottom":
		lo, hi := o.i32("min_inclusive"), o.i32("max_inclusive")
		return func(r Random) int32 { return lo + r.NextIntN(r.NextIntN(hi-lo+1)+1) }
	case "clamped":
		src := parseIntProvider(o["source"])
		lo, hi := o.i32("min_inclusive"), o.i32("max_inclusive")
		return func(r Random) int32 { return clampI(src(r), lo, hi) }
	case "clamped_normal":
		mean, dev := parseF32(o["mean"]), parseF32(o["deviation"])
		lo, hi := float32(o.i32("min_inclusive")), float32(o.i32("max_inclusive"))
		return func(r Random) int32 {
			return int32(clampF(mean+float32(float32(r.NextGaussian())*dev), lo, hi))
		}
	case "trapezoid":
		lo, hi, plateau := o.i32("min"), o.i32("max"), o.i32("plateau") // TrapezoidInt codec: min, max, plateau
		return func(r Random) int32 {
			if plateau == 0 && hi == -lo {
				return r.NextIntN(hi+1) - r.NextIntN(hi+1)
			}
			span := hi - lo
			if plateau == span {
				return randBetweenInclusive(r, lo, hi)
			}
			a := (span - plateau) / 2
			b := span - a
			return lo + randBetweenInclusive(r, 0, b) + randBetweenInclusive(r, 0, a)
		}
	case "weighted_list":
		w := parseWeighted(o["distribution"], func(raw json.RawMessage) intProvider { return parseIntProvider(raw) })
		return func(r Random) int32 { return w.pick(r)(r) }
	}
	panic("worldgen: unknown int provider " + o.str("type"))
}

func parseHeightProvider(raw json.RawMessage) heightProvider {
	var o obj
	jsonUnmarshal(raw, &o)
	if _, ok := o["type"]; !ok {
		// A bare vertical anchor is a constant height.
		return func(_ Random, g genCtx) int32 { return g.anchor(raw) }
	}
	switch trimNS(o.str("type")) {
	case "constant":
		v := o["value"]
		return func(_ Random, g genCtx) int32 { return g.anchor(v) }
	case "uniform":
		lo, hi := o["min_inclusive"], o["max_inclusive"]
		return func(r Random, g genCtx) int32 {
			i, j := g.anchor(lo), g.anchor(hi)
			if i > j {
				return i
			}
			return randBetweenInclusive(r, i, j)
		}
	case "biased_to_bottom":
		lo, hi := o["min_inclusive"], o["max_inclusive"]
		inner := int32(1)
		if v, ok := o["inner"]; ok {
			inner = int32(obj{"v": v}.f64("v"))
		}
		return func(r Random, g genCtx) int32 {
			i, j := g.anchor(lo), g.anchor(hi)
			if j-i-inner+1 <= 0 {
				return i
			}
			k := r.NextIntN(j - i - inner + 1)
			return r.NextIntN(k+inner) + i
		}
	case "very_biased_to_bottom":
		lo, hi := o["min_inclusive"], o["max_inclusive"]
		inner := int32(1)
		if v, ok := o["inner"]; ok {
			inner = int32(obj{"v": v}.f64("v"))
		}
		return func(r Random, g genCtx) int32 {
			i, j := g.anchor(lo), g.anchor(hi)
			if j-i-inner+1 <= 0 {
				return i
			}
			k := mthNextInt(r, i+inner, j)
			l := mthNextInt(r, i, k-1)
			return mthNextInt(r, i, l-1+inner)
		}
	case "trapezoid":
		lo, hi := o["min_inclusive"], o["max_inclusive"]
		plateau := int32(0)
		if v, ok := o["plateau"]; ok {
			plateau = int32(obj{"v": v}.f64("v"))
		}
		return func(r Random, g genCtx) int32 {
			i, j := g.anchor(lo), g.anchor(hi)
			if i > j {
				return i
			}
			k := j - i
			if plateau >= k {
				return randBetweenInclusive(r, i, j)
			}
			l := (k - plateau) / 2
			m := k - l
			return i + randBetweenInclusive(r, 0, m) + randBetweenInclusive(r, 0, l)
		}
	case "weighted_list":
		w := parseWeighted(o["distribution"], func(raw json.RawMessage) heightProvider { return parseHeightProvider(raw) })
		return func(r Random, g genCtx) int32 { return w.pick(r)(r, g) }
	}
	panic("worldgen: unknown height provider " + o.str("type"))
}

// weighted is a WeightedList.
type weighted[T any] struct {
	items   []T
	weights []int32
	total   int32
}

func parseWeighted[T any](raw json.RawMessage, data func(json.RawMessage) T) *weighted[T] {
	var entries []obj
	jsonUnmarshal(raw, &entries)
	w := &weighted[T]{}
	for _, e := range entries {
		w.items = append(w.items, data(e["data"]))
		wt := e.i32("weight")
		w.weights = append(w.weights, wt)
		w.total += wt
	}
	return w
}

func (w *weighted[T]) pick(r Random) T {
	i := r.NextIntN(w.total)
	for k, wt := range w.weights {
		i -= wt
		if i < 0 {
			return w.items[k]
		}
	}
	return w.items[len(w.items)-1]
}
