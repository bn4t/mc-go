package worldgen

// Surface rules decide which block replaces the default block near the
// surface: a tree of sequences and conditional rules over the column
// context (biome, depth below the surface, water above, noise values).

type srKind uint8

const (
	srBlockRule srKind = iota
	srSequenceRule
	srConditionRule
	srBandlandsRule
)

// SurfaceRule is a seed-independent surface rule.
type SurfaceRule struct {
	kind  srKind
	state string // block rule: canonical block state
	rules []*SurfaceRule
	cond  *SurfaceCondition
}

type scKind uint8

const (
	scBiome scKind = iota
	scNoiseThreshold
	scVerticalGradient
	scYAbove
	scWater
	scTemperature
	scSteep
	scNot
	scHole
	scAbovePreliminarySurface
	scStoneDepth
)

// anchorKind says what a vertical anchor counts from.
type anchorKind uint8

const (
	anchorAbsolute anchorKind = iota
	anchorAboveBottom
	anchorBelowTop
)

// VerticalAnchor is a Y given relative to the world's bounds.
type VerticalAnchor struct {
	kind anchorKind
	v    int32
}

// resolve turns the anchor into a Y in a world spanning [minY, minY+height).
func (a VerticalAnchor) resolve(minY, height int32) int32 {
	switch a.kind {
	case anchorAboveBottom:
		return minY + a.v
	case anchorBelowTop:
		return minY + height - 1 - a.v
	}
	return a.v
}

// SurfaceCondition is a seed-independent surface rule condition.
type SurfaceCondition struct {
	kind     scKind
	biomes   map[string]bool
	noise    string
	min, max float64
	is3D     bool
	name     string // vertical gradient random name
	a, b     VerticalAnchor
	offset   int32
	mult     int32
	add      bool // y/water: add stone depth; stone depth: add surface depth
	ceiling  bool
	secRange int32
	inner    *SurfaceCondition
}

// Constructors the generated tables are written with.

func srBlock(state string) *SurfaceRule { return &SurfaceRule{kind: srBlockRule, state: state} }
func srSeq(rules ...*SurfaceRule) *SurfaceRule {
	return &SurfaceRule{kind: srSequenceRule, rules: rules}
}
func srIf(c *SurfaceCondition, r *SurfaceRule) *SurfaceRule {
	return &SurfaceRule{kind: srConditionRule, cond: c, rules: []*SurfaceRule{r}}
}
func srBandlands() *SurfaceRule { return &SurfaceRule{kind: srBandlandsRule} }

func scBiomeIs(ids ...string) *SurfaceCondition {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return &SurfaceCondition{kind: scBiome, biomes: m}
}
func scNoise(id string, lo, hi float64, is3D bool) *SurfaceCondition {
	return &SurfaceCondition{kind: scNoiseThreshold, noise: id, min: lo, max: hi, is3D: is3D}
}
func scGradient(name string, trueAtAndBelow, falseAtAndAbove VerticalAnchor) *SurfaceCondition {
	return &SurfaceCondition{kind: scVerticalGradient, name: name, a: trueAtAndBelow, b: falseAtAndAbove}
}
func scY(anchor VerticalAnchor, mult int32, addStoneDepth bool) *SurfaceCondition {
	return &SurfaceCondition{kind: scYAbove, a: anchor, mult: mult, add: addStoneDepth}
}
func scWaterAbove(offset, mult int32, addStoneDepth bool) *SurfaceCondition {
	return &SurfaceCondition{kind: scWater, offset: offset, mult: mult, add: addStoneDepth}
}
func scDepth(offset int32, addSurfaceDepth bool, secondaryRange int32, ceiling bool) *SurfaceCondition {
	return &SurfaceCondition{kind: scStoneDepth, offset: offset, add: addSurfaceDepth, secRange: secondaryRange, ceiling: ceiling}
}
func scInvert(c *SurfaceCondition) *SurfaceCondition {
	return &SurfaceCondition{kind: scNot, inner: c}
}
func scLeaf(k scKind) *SurfaceCondition           { return &SurfaceCondition{kind: k} }
func anchor(k anchorKind, v int32) VerticalAnchor { return VerticalAnchor{kind: k, v: v} }
