package worldgen

import (
	"strings"

	"mc-go/internal/data"
)

// Block state providers, survival rules, the selector features, springs
// and single blocks.

// stateProvider picks the state a feature places at a position; ok is
// false when a rule-based provider has no state for it.
type stateProvider interface {
	get(r *decoRegion, rand Random, p Point) StateID
	optional(r *decoRegion, rand Random, p Point) (StateID, bool)
}

type simpleProvider StateID

func (s simpleProvider) get(*decoRegion, Random, Point) StateID { return StateID(s) }
func (s simpleProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	return s.get(r, rand, p), true
}

type weightedProvider struct {
	entries []weightedEntry[StateID]
	total   int32
}

func (w weightedProvider) get(_ *decoRegion, rand Random, _ Point) StateID {
	return pickWeighted(rand, w.entries, w.total)
}
func (w weightedProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	return w.get(r, rand, p), true
}

// noiseStates is the noise providers' shared part: a normal noise seeded
// from a legacy source and sampled at the block scaled.
type noiseStates struct {
	noise *Normal
	scale float32
}

func newNoiseStates(v any) noiseStates {
	p := ffield(v, "noise")
	params := NoiseParams{FirstOctave: int(fint(ffield(p, "firstOctave"), 0))}
	for _, a := range flist(ffield(p, "amplitudes")) {
		params.Amplitudes = append(params.Amplitudes, fnum(a, 0))
	}
	return noiseStates{NewNormal(NewLegacy(int64(fnum(ffield(v, "seed"), 0))), params, false), ffloat(ffield(v, "scale"), 1)}
}

func (n noiseStates) value(p Point, scale float64) float64 {
	return n.noise.Value(float64(p.X)*scale, float64(p.Y)*scale, float64(p.Z)*scale)
}

// pickByNoise maps a noise value in [-1, 1] onto a list.
func pickByNoise(states []StateID, v float64) StateID {
	t := min(max((1+v)/2, 0), 0.9999)
	return states[int(t*float64(len(states)))]
}

type noiseProvider struct {
	noiseStates
	states []StateID
}

func (n noiseProvider) get(_ *decoRegion, _ Random, p Point) StateID {
	return pickByNoise(n.states, n.value(p, float64(n.scale)))
}
func (n noiseProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	return n.get(r, rand, p), true
}

type noiseThresholdProvider struct {
	noiseStates
	threshold, highChance float32
	def                   StateID
	low, high             []StateID
}

func (n noiseThresholdProvider) get(_ *decoRegion, rand Random, p Point) StateID {
	if n.value(p, float64(n.scale)) < float64(n.threshold) {
		return n.low[rand.NextIntn(int32(len(n.low)))]
	}
	if rand.NextFloat() < n.highChance {
		return n.high[rand.NextIntn(int32(len(n.high)))]
	}
	return n.def
}
func (n noiseThresholdProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	return n.get(r, rand, p), true
}

type dualNoiseProvider struct {
	noiseProvider
	varietyLo, varietyHi int32
	slow                 *Normal
	slowScale            float32
}

func (d dualNoiseProvider) slowValue(p Point) float64 {
	s := float64(d.slowScale)
	return d.slow.Value(float64(p.X)*s, float64(p.Y)*s, float64(p.Z)*s)
}

func (d dualNoiseProvider) get(_ *decoRegion, _ Random, p Point) StateID {
	t := (d.slowValue(p) - -1) / (1 - -1)
	lo, hi := float64(d.varietyLo), float64(d.varietyHi+1)
	var n int32
	switch {
	case t < 0:
		n = int32(lo)
	case t > 1:
		n = int32(hi)
	default:
		n = int32(lo + float64(t*(hi-lo)))
	}
	picked := make([]StateID, 0, n)
	for i := int32(0); i < n; i++ {
		picked = append(picked, pickByNoise(d.states, d.slowValue(Point{p.X + i*54545, p.Y, p.Z + i*34234})))
	}
	return pickByNoise(picked, d.value(p, float64(d.scale)))
}
func (d dualNoiseProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	return d.get(r, rand, p), true
}

type ruleRule struct {
	test blockPredicate
	then stateProvider
}

type ruleProvider struct {
	fallback stateProvider
	rules    []ruleRule
}

func (rp ruleProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	for _, rule := range rp.rules {
		if rule.test(r, p) {
			return rule.then.get(r, rand, p), true
		}
	}
	if rp.fallback == nil {
		return 0, false
	}
	return rp.fallback.get(r, rand, p), true
}

func (rp ruleProvider) get(r *decoRegion, rand Random, p Point) StateID {
	if s, ok := rp.optional(r, rand, p); ok {
		return s
	}
	return r.get(p)
}

type randomizedIntProvider struct {
	source stateProvider
	prop   string
	values intProvider
}

func (ri randomizedIntProvider) get(r *decoRegion, rand Random, p Point) StateID {
	s := ri.source.get(r, rand, p)
	if !s.hasProp(ri.prop) {
		return s
	}
	return s.with(ri.prop, itoa(ri.values.sample(rand)))
}
func (ri randomizedIntProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	return ri.get(r, rand, p), true
}

type rotatedProvider StateID

func (rb rotatedProvider) get(_ *decoRegion, rand Random, _ Point) StateID {
	axis := [3]string{"x", "y", "z"}[rand.NextIntn(3)]
	return StateID(rb).with("axis", axis)
}
func (rb rotatedProvider) optional(r *decoRegion, rand Random, p Point) (StateID, bool) {
	return rb.get(r, rand, p), true
}

func itoa(v int32) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func decodeStateProvider(v any) stateProvider {
	switch ftype(v) {
	case "minecraft:simple_state_provider":
		return simpleProvider(blockStateOf(ffield(v, "state")))
	case "minecraft:weighted_state_provider":
		var w weightedProvider
		for _, e := range flist(ffield(v, "entries")) {
			wt := fint(ffield(e, "weight"), 1)
			w.entries = append(w.entries, weightedEntry[StateID]{blockStateOf(ffield(e, "data")), wt})
			w.total += wt
		}
		return w
	case "minecraft:noise_provider":
		var n noiseProvider
		n.noiseStates = newNoiseStates(v)
		for _, s := range flist(ffield(v, "states")) {
			n.states = append(n.states, blockStateOf(s))
		}
		return n
	case "minecraft:noise_threshold_provider":
		n := noiseThresholdProvider{noiseStates: newNoiseStates(v), threshold: ffloat(ffield(v, "threshold"), 0),
			highChance: ffloat(ffield(v, "high_chance"), 0), def: blockStateOf(ffield(v, "default_state"))}
		for _, s := range flist(ffield(v, "low_states")) {
			n.low = append(n.low, blockStateOf(s))
		}
		for _, s := range flist(ffield(v, "high_states")) {
			n.high = append(n.high, blockStateOf(s))
		}
		return n
	case "minecraft:dual_noise_provider":
		d := dualNoiseProvider{slowScale: ffloat(ffield(v, "slow_scale"), 1)}
		d.noiseStates = newNoiseStates(v)
		for _, s := range flist(ffield(v, "states")) {
			d.states = append(d.states, blockStateOf(s))
		}
		variety := flist(ffield(v, "variety"))
		d.varietyLo, d.varietyHi = fint(variety[0], 0), fint(variety[len(variety)-1], 0)
		sp := ffield(v, "slow_noise")
		params := NoiseParams{FirstOctave: int(fint(ffield(sp, "firstOctave"), 0))}
		for _, a := range flist(ffield(sp, "amplitudes")) {
			params.Amplitudes = append(params.Amplitudes, fnum(a, 0))
		}
		d.slow = NewNormal(NewLegacy(int64(fnum(ffield(v, "seed"), 0))), params, false)
		return d
	case "minecraft:rule_based_state_provider":
		var rp ruleProvider
		if fhas(v, "fallback") {
			rp.fallback = decodeStateProvider(ffield(v, "fallback"))
		}
		for _, rule := range flist(ffield(v, "rules")) {
			rp.rules = append(rp.rules, ruleRule{decodeBlockPredicate(ffield(rule, "if_true")), decodeStateProvider(ffield(rule, "then"))})
		}
		return rp
	case "minecraft:randomized_int_state_provider":
		return randomizedIntProvider{decodeStateProvider(ffield(v, "source")), fstr(ffield(v, "property")), decodeIntProvider(ffield(v, "values"))}
	case "minecraft:rotated_block_provider":
		return rotatedProvider(blockStateOf(ffield(v, "state")))
	}
	unsupported("state provider %v", ftype(v))
	return nil
}

// Fluids and fluid tags.

// fluidName is the fluid a state holds, as the fluid registry names it.
func (s StateID) fluidName() string {
	if !s.hasFluid() {
		return "empty"
	}
	src := s.isSourceFluid()
	switch {
	case s.isWaterFluid() && src:
		return "water"
	case s.isWaterFluid():
		return "flowing_water"
	case src:
		return "lava"
	}
	return "flowing_lava"
}

func (s StateID) fluidInTag(tag string) bool {
	name := s.fluidName()
	for _, f := range data.FluidTags[strings.TrimPrefix(tag, "minecraft:")] {
		if f == name {
			return true
		}
	}
	return false
}

func (r *decoRegion) isWaterAt(p Point) bool { return r.get(p).fluidInTag("water") }

// rawBrightness is the light the features stage sees: nothing is lit
// yet, so sky light reads full.
func (r *decoRegion) rawBrightness(Point, int32) int32 { return 15 }

// Survival rules: BlockState.canSurvive for the blocks features place,
// by the classes declaring each block's canSurvive and mayPlaceOn.

func canSurvive(r *decoRegion, s StateID, p Point) bool {
	l := s.layout()
	if l == nil {
		return true
	}
	below := p.offset(dirDown)
	switch l.survive {
	case "BlockBehaviour", "-":
		return true
	case "VegetationBlock":
		return mayPlaceOn(r, l, below)
	case "DoublePlantBlock", "SmallDripleafBlock":
		if s.prop("half") == "upper" {
			b := r.get(below)
			return b.block() == l.name && b.prop("half") == "lower"
		}
		return mayPlaceOn(r, l, below)
	case "MushroomBlock":
		b := r.get(below)
		if b.inTag("overrides_mushroom_light_requirement") {
			return true
		}
		return r.rawBrightness(p, 0) < 13 && mayPlaceOn(r, l, below)
	case "LeafLitterBlock":
		return r.get(below).sturdy(dirUp)
	case "CarpetBlock":
		return !r.get(below).isAir()
	case "MossyCarpetBlock":
		b := r.get(below)
		if s.prop("bottom") == "true" {
			return !b.isAir()
		}
		return b.block() == l.name && b.prop("bottom") == "true"
	case "SporeBlossomBlock":
		return r.get(p.offset(dirUp)).sturdyCenter(dirDown) && !r.isWaterAt(p)
	case "SoulFireBlock":
		return r.get(below).inTag("soul_fire_base_blocks")
	case "FireBlock":
		if r.get(below).sturdy(dirUp) {
			return true
		}
		for d := 0; d < 6; d++ {
			if r.get(p.offset(d)).flammable() {
				return true
			}
		}
		return false
	case "SugarCaneBlock":
		b := r.get(below)
		if b.block() == l.name {
			return true
		}
		if b.inTag("supports_sugar_cane") {
			for _, d := range []int{dirNorth, dirEast, dirSouth, dirWest} {
				n := r.get(below.offset(d))
				if n.fluidInTag("supports_sugar_cane_adjacently") || n.inTag("supports_sugar_cane_adjacently") {
					return true
				}
			}
		}
		return false
	case "CactusBlock":
		for _, d := range []int{dirNorth, dirEast, dirSouth, dirWest} {
			n := r.get(p.offset(d))
			if n.isSolid() || n.fluidInTag("lava") {
				return false
			}
		}
		b := r.get(below)
		return (b.block() == l.name || b.inTag("supports_cactus")) && !r.get(p.offset(dirUp)).isLiquidBlock()
	case "AmethystClusterBlock":
		facing := directionNames[s.prop("facing")]
		return r.get(p.offset(facing ^ 1)).sturdy(facing)
	case "BigDripleafBlock":
		b := r.get(below)
		return b.block() == l.name || b.is("big_dripleaf_stem") || b.inTag("supports_big_dripleaf")
	case "BigDripleafStemBlock":
		b, a := r.get(below), r.get(p.offset(dirUp))
		return (b.block() == l.name || b.inTag("supports_big_dripleaf")) && (a.block() == l.name || a.is("big_dripleaf"))
	case "HangingRootsBlock":
		return r.get(p.offset(dirUp)).sturdy(dirDown)
	case "SnowLayerBlock":
		b := r.get(below)
		if b.inTag("cannot_support_snow_layer") {
			return false
		}
		if b.inTag("support_override_snow_layer") {
			return true
		}
		return b.collisionTopFull() || (b.is("snow") && b.prop("layers") == "8")
	case "MangrovePropaguleBlock":
		if s.prop("hanging") == "true" {
			return r.get(p.offset(dirUp)).inTag("supports_hanging_mangrove_propagule")
		}
		return mayPlaceOn(r, l, below)
	}
	unsupported("survival of %s (%s)", l.name, l.survive)
	return false
}

// mayPlaceOn is the vegetation support test of the block below.
func mayPlaceOn(r *decoRegion, l *blockLayout, below Point) bool {
	b := r.get(below)
	switch l.placeOn {
	case "VegetationBlock", "DoublePlantBlock":
		return b.inTag("supports_vegetation")
	case "AzaleaBlock":
		return b.inTag("supports_azalea")
	case "NetherRootsBlock", "NetherFungusBlock":
		return b.inTag(l.tag)
	case "NetherSproutsBlock":
		return b.inTag("supports_nether_sprouts")
	case "DryVegetationBlock":
		return b.inTag("supports_dry_vegetation")
	case "LilyPadBlock":
		return (b.fluidInTag("supports_lily_pad") || b.inTag("supports_lily_pad")) && !r.get(below.offset(dirUp)).hasFluid()
	case "SeagrassBlock":
		return b.sturdy(dirUp) && !b.inTag("cannot_support_seagrass")
	case "MushroomBlock":
		return b.isSolidRender()
	case "SmallDripleafBlock":
		if b.inTag("supports_small_dripleaf") {
			return true
		}
		a := r.get(below.offset(dirUp))
		return a.isWaterFluid() && a.isSourceFluid() && b.inTag("supports_vegetation")
	case "CactusFlowerBlock":
		return b.inTag("support_override_cactus_flower") || b.sturdyCenter(dirUp)
	case "MangrovePropaguleBlock":
		return b.inTag("supports_mangrove_propagule")
	}
	unsupported("support of %s (%s)", l.name, l.placeOn)
	return false
}

// Features.

func init() {
	featureKinds["minecraft:random_selector"] = func(cfg any) func(*featureCtx) bool {
		type choice struct {
			chance  float32
			feature *placedFeature
		}
		var choices []choice
		for _, c := range flist(ffield(cfg, "features")) {
			choices = append(choices, choice{ffloat(ffield(c, "chance"), 0), decodePlaced(ffield(c, "feature"))})
		}
		def := decodePlaced(ffield(cfg, "default"))
		return func(fc *featureCtx) bool {
			for _, c := range choices {
				if fc.rand.NextFloat() < c.chance {
					return c.feature.placeNested(fc)
				}
			}
			return def.placeNested(fc)
		}
	}
	featureKinds["minecraft:simple_random_selector"] = func(cfg any) func(*featureCtx) bool {
		var features []*placedFeature
		for _, f := range flist(ffield(cfg, "features")) {
			features = append(features, decodePlaced(f))
		}
		return func(fc *featureCtx) bool {
			return features[fc.rand.NextIntn(int32(len(features)))].placeNested(fc)
		}
	}
	featureKinds["minecraft:random_boolean_selector"] = func(cfg any) func(*featureCtx) bool {
		yes, no := decodePlaced(ffield(cfg, "feature_true")), decodePlaced(ffield(cfg, "feature_false"))
		return func(fc *featureCtx) bool {
			if fc.rand.NextBool() {
				return yes.placeNested(fc)
			}
			return no.placeNested(fc)
		}
	}
	featureKinds["minecraft:sequence"] = func(cfg any) func(*featureCtx) bool {
		var features []*placedFeature
		for _, f := range flist(ffield(cfg, "features")) {
			features = append(features, decodePlaced(f))
		}
		return func(fc *featureCtx) bool {
			for _, f := range features {
				if !f.placeNested(fc) {
					return false
				}
			}
			return true
		}
	}
	featureKinds["minecraft:spring_feature"] = decodeSpring
	featureKinds["minecraft:simple_block"] = decodeSimpleBlock
}

// placeNested runs a placed feature inside another feature: placement
// with no top feature.
func (pf *placedFeature) placeNested(fc *featureCtx) bool {
	return pf.placeIn(&featureCtx{r: fc.r, rand: fc.rand}, fc.origin)
}

func blockSet(v any) map[string]bool {
	set := map[string]bool{}
	for _, b := range flist(v) {
		name := fstr(b)
		if strings.HasPrefix(name, "#") {
			for k := range blockTagSet(name) {
				set[k] = true
			}
			continue
		}
		set[strings.TrimPrefix(name, "minecraft:")] = true
	}
	return set
}

func decodeSpring(cfg any) func(*featureCtx) bool {
	// The fluid's legacy block: a source fluid's holds level 0.
	block := defaultState(fstr(ffield(ffield(cfg, "state"), "Name"))).with("level", "0")
	valid := blockSet(ffield(cfg, "valid_blocks"))
	rocks, holes := fint(ffield(cfg, "rock_count"), 4), fint(ffield(cfg, "hole_count"), 1)
	below := fbool(ffield(cfg, "requires_block_below"), true)
	return func(fc *featureCtx) bool {
		r, o := fc.r, fc.origin
		if !valid[r.get(o.offset(dirUp)).block()] {
			return false
		}
		if below && !valid[r.get(o.offset(dirDown)).block()] {
			return false
		}
		if s := r.get(o); !s.isAir() && !valid[s.block()] {
			return false
		}
		rock, hole := int32(0), int32(0)
		for _, d := range []int{dirWest, dirEast, dirNorth, dirSouth, dirDown} {
			if valid[r.get(o.offset(d)).block()] {
				rock++
			}
		}
		for _, d := range []int{dirWest, dirEast, dirNorth, dirSouth, dirDown} {
			if r.get(o.offset(d)).isAir() {
				hole++
			}
		}
		if rock == rocks && hole == holes {
			r.set(o, block)
			return true
		}
		return false
	}
}

func decodeSimpleBlock(cfg any) func(*featureCtx) bool {
	provider := decodeStateProvider(ffield(cfg, "to_place"))
	return func(fc *featureCtx) bool {
		r, o := fc.r, fc.origin
		s, ok := provider.optional(r, fc.rand, o)
		if !ok || !canSurvive(r, s, o) {
			return false
		}
		switch s.layout().class {
		case "DoublePlantBlock", "TallFlowerBlock", "SmallDripleafBlock", "PitcherCropBlock", "TallSeagrassBlock":
			if !r.get(o.offset(dirUp)).isAir() {
				return false
			}
			placeDoublePlant(r, s, o)
		case "MossyCarpetBlock":
			unsupported("mossy carpet placement")
		default:
			r.set(o, s)
		}
		return true
	}
}

// placeDoublePlant sets both halves, each waterlogged as the water there.
func placeDoublePlant(r *decoRegion, s StateID, p Point) {
	up := p.offset(dirUp)
	r.set(p, copyWaterlogged(r, p, s.with("half", "lower")))
	r.set(up, copyWaterlogged(r, up, s.with("half", "upper")))
}

func copyWaterlogged(r *decoRegion, p Point, s StateID) StateID {
	if !s.hasProp("waterlogged") {
		return s
	}
	if r.isWaterAt(p) {
		return s.with("waterlogged", "true")
	}
	return s.with("waterlogged", "false")
}
