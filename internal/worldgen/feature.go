package worldgen

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// BlockPos is a block position (x, y, z).
type BlockPos = [3]int32

func offset(p BlockPos, dx, dy, dz int32) BlockPos { return BlockPos{p[0] + dx, p[1] + dy, p[2] + dz} }

// Direction offsets in vanilla's Direction order (down, up, north,
// south, west, east).
var dirOffsets = [6]BlockPos{{0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-1, 0, 0}, {1, 0, 0}}

var dirNames = map[string]int{"down": dirDown, "up": dirUp, "north": dirNorth, "south": dirSouth, "west": dirWest, "east": dirEast}

// Horizontal directions in Direction.Plane.HORIZONTAL order (north,
// east, south, west).
var horizontalDirs = [4]int{dirNorth, dirEast, dirSouth, dirWest}

func oppositeDir(d int) int { return d ^ 1 }

// placeCtx is PlacementContext plus the decorating region.
type placeCtx struct {
	g   *Generator
	rg  *Region
	top string // the top-level placed feature (biome filter)
}

// featCtx is FeaturePlaceContext.
type featCtx struct {
	g   *Generator
	rg  *Region
	top string
}

type modifier func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos))

type placedFeature struct {
	feature *configuredFeature
	mods    []modifier
}

// place is PlacedFeature.placeWithContext: modifiers chained as nested
// flatMaps, each position fully expanded (and its feature placed)
// before the next upstream position is drawn.
func (pf *placedFeature) place(c *placeCtx, r Random, origin BlockPos) bool {
	placed := false
	var run func(i int, p BlockPos)
	run = func(i int, p BlockPos) {
		if i == len(pf.mods) {
			if pf.feature.place(&featCtx{g: c.g, rg: c.rg, top: c.top}, r, p) {
				placed = true
			}
			return
		}
		pf.mods[i](c, r, p, func(q BlockPos) { run(i+1, q) })
	}
	run(0, origin)
	return placed
}

type configuredFeature struct {
	typ   string
	place func(c *featCtx, r Random, p BlockPos) bool
}

// featureBuilders maps feature types to their compilers.
var featureBuilders = map[string]func(g *Generator, cfg obj) func(c *featCtx, r Random, p BlockPos) bool{}

func (g *Generator) placed(name string) *placedFeature {
	g.featMu.Lock()
	if pf, ok := g.placedCache[name]; ok {
		g.featMu.Unlock()
		return pf
	}
	g.featMu.Unlock()
	raw, ok := g.RS.data.Get("placed_feature/" + name)
	if !ok {
		panic("worldgen: unknown placed feature " + name)
	}
	pf := g.compilePlaced(raw)
	g.featMu.Lock()
	if g.placedCache == nil {
		g.placedCache = map[string]*placedFeature{}
	}
	g.placedCache[name] = pf
	g.featMu.Unlock()
	return pf
}

// placedRef resolves a placed feature given by name or inline.
func (g *Generator) placedRef(raw json.RawMessage) *placedFeature {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return g.placed(trimNS(name))
	}
	return g.compilePlaced(raw)
}

func (g *Generator) compilePlaced(raw json.RawMessage) *placedFeature {
	var o obj
	jsonUnmarshal(raw, &o)
	pf := &placedFeature{feature: g.configuredRef(o["feature"])}
	var mods []json.RawMessage
	if m, ok := o["placement"]; ok {
		jsonUnmarshal(m, &mods)
	}
	for _, m := range mods {
		pf.mods = append(pf.mods, g.compileModifier(m))
	}
	return pf
}

func (g *Generator) configured(name string) *configuredFeature {
	g.featMu.Lock()
	if cf, ok := g.configuredCache[name]; ok {
		g.featMu.Unlock()
		return cf
	}
	g.featMu.Unlock()
	raw, ok := g.RS.data.Get("configured_feature/" + name)
	if !ok {
		panic("worldgen: unknown configured feature " + name)
	}
	cf := g.compileConfigured(raw)
	g.featMu.Lock()
	if g.configuredCache == nil {
		g.configuredCache = map[string]*configuredFeature{}
	}
	g.configuredCache[name] = cf
	g.featMu.Unlock()
	return cf
}

func (g *Generator) configuredRef(raw json.RawMessage) *configuredFeature {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return g.configured(trimNS(name))
	}
	return g.compileConfigured(raw)
}

// UnsupportedFeatures collects feature types without an implementation
// (they place nothing); tests assert it stays empty.
var UnsupportedFeatures = map[string]bool{}

func (g *Generator) compileConfigured(raw json.RawMessage) *configuredFeature {
	var o obj
	jsonUnmarshal(raw, &o)
	t := trimNS(o.str("type"))
	var cfg obj
	if c, ok := o["config"]; ok {
		jsonUnmarshal(c, &cfg)
	}
	b, ok := featureBuilders[t]
	if !ok {
		g.featMu.Lock()
		UnsupportedFeatures[t] = true
		g.featMu.Unlock()
		return &configuredFeature{typ: t, place: func(*featCtx, Random, BlockPos) bool { return false }}
	}
	inner := b(g, cfg)
	return &configuredFeature{typ: t, place: func(c *featCtx, r Random, p BlockPos) bool {
		// Feature.place: the origin must be writable.
		if !c.rg.canWrite(p) {
			return false
		}
		return inner(c, r, p)
	}}
}

func (rg *Region) canWrite(p BlockPos) bool {
	cx, cz := p[0]>>4, p[2]>>4
	return cx >= rg.center.CX-1 && cx <= rg.center.CX+1 && cz >= rg.center.CZ-1 && cz <= rg.center.CZ+1
}

func heightmapType(s string) int {
	switch s {
	case "WORLD_SURFACE_WG":
		return HMWorldSurfaceWG
	case "WORLD_SURFACE":
		return HMWorldSurface
	case "OCEAN_FLOOR_WG":
		return HMOceanFloorWG
	case "OCEAN_FLOOR":
		return HMOceanFloor
	case "MOTION_BLOCKING":
		return HMMotionBlocking
	case "MOTION_BLOCKING_NO_LEAVES":
		return HMMotionBlockingNoLeaves
	}
	panic("worldgen: unknown heightmap " + s)
}

func (g *Generator) compileModifier(raw json.RawMessage) modifier {
	var o obj
	jsonUnmarshal(raw, &o)
	gc := genCtx{g.RS.settings.minY, g.RS.settings.height}
	switch t := trimNS(o.str("type")); t {
	case "count":
		n := parseIntProvider(o["count"])
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			k := n(r)
			for i := int32(0); i < k; i++ {
				emit(p)
			}
		}
	case "in_square":
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			x := r.NextIntN(16) + p[0]
			z := r.NextIntN(16) + p[2]
			emit(BlockPos{x, p[1], z})
		}
	case "biome":
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			b := c.rg.Biome(p[0], p[1], p[2])
			if c.g.RS.BiomeInfo(b).hasFeature(c.top) {
				emit(p)
			}
		}
	case "rarity_filter":
		chance := o.i32("chance")
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			if r.NextFloat() < 1/float32(chance) {
				emit(p)
			}
		}
	case "heightmap":
		hm := heightmapType(o.str("heightmap"))
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			h := c.rg.Height(hm, p[0], p[2])
			if h > c.rg.MinY() {
				emit(BlockPos{p[0], h, p[2]})
			}
		}
	case "height_range":
		hp := parseHeightProvider(o["height"])
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			emit(BlockPos{p[0], hp(r, gc), p[2]})
		}
	case "random_offset":
		xz := parseIntProvider(o["xz_spread"])
		y := parseIntProvider(o["y_spread"])
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			x := p[0] + xz(r)
			yy := p[1] + y(r)
			z := p[2] + xz(r)
			emit(BlockPos{x, yy, z})
		}
	case "block_predicate_filter":
		pred := g.compilePredicate(o["predicate"])
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			if pred(c.rg, p) {
				emit(p)
			}
		}
	case "surface_water_depth_filter":
		maxDepth := o.i32("max_water_depth")
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			floor := c.rg.Height(HMOceanFloor, p[0], p[2])
			surf := c.rg.Height(HMWorldSurface, p[0], p[2])
			if surf-floor <= maxDepth {
				emit(p)
			}
		}
	case "surface_relative_threshold_filter":
		hm := heightmapType(o.str("heightmap"))
		lo, hi := int64(math.MinInt32), int64(math.MaxInt32)
		if v, ok := o["min_inclusive"]; ok {
			lo = int64(obj{"v": v}.i32("v"))
		}
		if v, ok := o["max_inclusive"]; ok {
			hi = int64(obj{"v": v}.i32("v"))
		}
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			h := int64(c.rg.Height(hm, p[0], p[2]))
			if h+lo <= int64(p[1]) && int64(p[1]) <= h+hi {
				emit(p)
			}
		}
	case "noise_based_count":
		ratio := o.i32("noise_to_count_ratio")
		factor := o.f64("noise_factor")
		var off float64
		if _, ok := o["noise_offset"]; ok {
			off = o.f64("noise_offset")
		}
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			biomeNoises()
			d := biomeInfoNoise.value(float64(p[0])/factor, float64(p[2])/factor, false)
			k := d2i(math.Ceil(float64((d + off) * float64(ratio))))
			for i := int32(0); i < k; i++ {
				emit(p)
			}
		}
	case "noise_threshold_count":
		level := o.f64("noise_level")
		below, above := o.i32("below_noise"), o.i32("above_noise")
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			biomeNoises()
			d := biomeInfoNoise.value(float64(p[0])/200, float64(p[2])/200, false)
			k := above
			if d < level {
				k = below
			}
			for i := int32(0); i < k; i++ {
				emit(p)
			}
		}
	case "count_on_every_layer":
		n := parseIntProvider(o["count"])
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			var out []BlockPos
			layer := int32(0)
			for {
				found := false
				for i := int32(0); i < n(r); i++ {
					x := r.NextIntN(16) + p[0]
					z := r.NextIntN(16) + p[2]
					h := c.rg.Height(HMMotionBlocking, x, z)
					if y, ok := onGroundY(c.rg, x, h, z, layer); ok {
						out = append(out, BlockPos{x, y, z})
						found = true
					}
				}
				layer++
				if !found {
					break
				}
			}
			for _, q := range out {
				emit(q)
			}
		}
	case "environment_scan":
		dir := dirNames[o.str("direction_of_search")]
		target := g.compilePredicate(o["target_condition"])
		allowed := func(*Region, BlockPos) bool { return true }
		if a, ok := o["allowed_search_condition"]; ok {
			allowed = g.compilePredicate(a)
		}
		steps := o.i32("max_steps")
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			q := p
			if !allowed(c.rg, q) {
				return
			}
			for i := int32(0); i < steps; i++ {
				if target(c.rg, q) {
					emit(q)
					return
				}
				d := dirOffsets[dir]
				q = offset(q, d[0], d[1], d[2])
				if q[1] < c.rg.MinY() || q[1] > c.rg.MaxY() {
					return
				}
				if !allowed(c.rg, q) {
					break
				}
			}
			if target(c.rg, q) {
				emit(q)
			}
		}
	case "fixed_placement":
		var pts [][3]int32
		jsonUnmarshal(o["positions"], &pts)
		return func(c *placeCtx, r Random, p BlockPos, emit func(BlockPos)) {
			cx, cz := p[0]>>4, p[2]>>4
			for _, q := range pts {
				if q[0]>>4 == cx && q[2]>>4 == cz {
					emit(q)
				}
			}
		}
	default:
		panic("worldgen: unknown placement modifier " + t)
	}
}

func isEmptyForLayer(b world.Block) bool {
	if isAir(b) {
		return true
	}
	n, _ := world.StateOwner(b)
	return n == "water" || n == "lava"
}

// onGroundY is CountOnEveryLayerPlacement.findOnGroundYPosition.
func onGroundY(rg *Region, x, y, z, layer int32) (int32, bool) {
	count := int32(0)
	prev := rg.Block(x, y, z)
	for yy := y; yy >= rg.MinY()+1; yy-- {
		cur := rg.Block(x, yy-1, z)
		if !isEmptyForLayer(cur) && isEmptyForLayer(prev) && !isBlock(cur, "bedrock") {
			if count == layer {
				return yy - 1 + 1, true
			}
			count++
		}
		prev = cur
	}
	return 0, false
}

func isBlock(b world.Block, name string) bool {
	n, _ := world.StateOwner(b)
	return n == name
}

// blockPredicate is a BlockPredicate.
type blockPredicate func(rg *Region, p BlockPos) bool

func parseOffset(o obj) BlockPos {
	var off BlockPos
	if v, ok := o["offset"]; ok {
		var a [3]int32
		jsonUnmarshal(v, &a)
		off = a
	}
	return off
}

// blockNameSet parses a HolderSet<Block>: "#tag", "id" or ["id", ...].
func blockNameSet(raw json.RawMessage) map[string]bool {
	set := map[string]bool{}
	var one string
	var many []string
	if json.Unmarshal(raw, &one) == nil {
		many = []string{one}
	} else {
		jsonUnmarshal(raw, &many)
	}
	for _, n := range many {
		if strings.HasPrefix(n, "#") {
			for _, b := range data.BlockTags[trimNS(n[1:])] {
				set[trimNS(b)] = true
			}
			continue
		}
		set[trimNS(n)] = true
	}
	return set
}

func blockTagSet(tag string) map[string]bool {
	set := map[string]bool{}
	for _, b := range data.BlockTags[trimNS(strings.TrimPrefix(tag, "#"))] {
		set[trimNS(b)] = true
	}
	return set
}

func inTag(b world.Block, set map[string]bool) bool {
	n, _ := world.StateOwner(b)
	return set[n]
}

func (g *Generator) compilePredicate(raw json.RawMessage) blockPredicate {
	var o obj
	jsonUnmarshal(raw, &o)
	off := parseOffset(o)
	at := func(p BlockPos) BlockPos { return offset(p, off[0], off[1], off[2]) }
	switch t := trimNS(o.str("type")); t {
	case "true":
		return func(*Region, BlockPos) bool { return true }
	case "not":
		inner := g.compilePredicate(o["predicate"])
		return func(rg *Region, p BlockPos) bool { return !inner(rg, p) }
	case "all_of", "any_of":
		var list []json.RawMessage
		jsonUnmarshal(o["predicates"], &list)
		var ps []blockPredicate
		for _, l := range list {
			ps = append(ps, g.compilePredicate(l))
		}
		all := t == "all_of"
		return func(rg *Region, p BlockPos) bool {
			for _, pr := range ps {
				if pr(rg, p) != all {
					return !all
				}
			}
			return all
		}
	case "matching_blocks":
		set := blockNameSet(o["blocks"])
		return func(rg *Region, p BlockPos) bool { q := at(p); return inTag(rg.Block(q[0], q[1], q[2]), set) }
	case "matching_block_tag":
		set := blockTagSet(o.str("tag"))
		return func(rg *Region, p BlockPos) bool { q := at(p); return inTag(rg.Block(q[0], q[1], q[2]), set) }
	case "matching_fluids":
		fl := fluidSet(o["fluids"])
		return func(rg *Region, p BlockPos) bool { q := at(p); return fl(rg.Block(q[0], q[1], q[2])) }
	case "has_sturdy_face":
		dir := dirNames[o.str("direction")]
		return func(rg *Region, p BlockPos) bool { q := at(p); return sturdyFull(rg.Block(q[0], q[1], q[2]), dir) }
	case "inside_world_bounds":
		return func(rg *Region, p BlockPos) bool { q := at(p); return q[1] >= rg.MinY() && q[1] <= rg.MaxY() }
	case "solid":
		return func(rg *Region, p BlockPos) bool { q := at(p); return isSolidState(rg.Block(q[0], q[1], q[2])) }
	case "replaceable":
		return func(rg *Region, p BlockPos) bool { q := at(p); return isReplaceable(rg.Block(q[0], q[1], q[2])) }
	case "would_survive":
		var st obj
		jsonUnmarshal(o["state"], &st)
		state := stateFromJSON(st)
		return func(rg *Region, p BlockPos) bool { return canSurvive(rg, state, at(p)) }
	case "unobstructed":
		return func(*Region, BlockPos) bool { return true }
	case "matching_biomes":
		set := map[string]bool{}
		for b := range blockNameSet(o["biomes"]) {
			set[b] = true
		}
		return func(rg *Region, p BlockPos) bool { return set[rg.Biome(p[0], p[1], p[2])] }
	default:
		panic("worldgen: unknown block predicate " + t)
	}
}

// fluidSet parses a HolderSet<Fluid>; still and flowing fluids are
// distinct entries (a flowing level only matches "flowing_water").
func fluidSet(raw json.RawMessage) func(world.Block) bool {
	var one string
	var many []string
	if json.Unmarshal(raw, &one) == nil {
		many = []string{one}
	} else {
		jsonUnmarshal(raw, &many)
	}
	want := map[string]bool{}
	for _, n := range many {
		if strings.HasPrefix(n, "#") {
			switch trimNS(n[1:]) {
			case "water":
				want["water"], want["flowing_water"] = true, true
			case "lava":
				want["lava"], want["flowing_lava"] = true, true
			default:
				panic("worldgen: unknown fluid tag " + n)
			}
			continue
		}
		want[trimNS(n)] = true
	}
	return func(b world.Block) bool {
		if !hasFluid(b) {
			return want["empty"]
		}
		src := isFluidSource(b)
		switch {
		case isWaterFluid(b):
			return (src && want["water"]) || (!src && want["flowing_water"])
		case isLavaFluid(b):
			return (src && want["lava"]) || (!src && want["flowing_lava"])
		}
		return false
	}
}

// stateProvider is a BlockStateProvider; ok=false is getOptionalState's
// null (rule-based provider with no rule matching and no fallback).
type stateProvider func(rg *Region, r Random, p BlockPos) (world.Block, bool)

func (g *Generator) compileStateProvider(raw json.RawMessage) stateProvider {
	var o obj
	jsonUnmarshal(raw, &o)
	switch t := trimNS(o.str("type")); t {
	case "simple_state_provider":
		var st obj
		jsonUnmarshal(o["state"], &st)
		s := stateFromJSON(st)
		return func(*Region, Random, BlockPos) (world.Block, bool) { return s, true }
	case "weighted_state_provider":
		w := parseWeighted(o["entries"], func(raw json.RawMessage) world.Block {
			var st obj
			jsonUnmarshal(raw, &st)
			return stateFromJSON(st)
		})
		return func(rg *Region, r Random, p BlockPos) (world.Block, bool) { return w.pick(r), true }
	case "rotated_block_provider":
		var st obj
		jsonUnmarshal(o["state"], &st)
		base := stateFromJSON(st)
		return func(rg *Region, r Random, p BlockPos) (world.Block, bool) {
			axis := [3]string{"x", "y", "z"}[r.NextIntN(3)]
			if b, ok := world.WithProperty(base, "axis", axis); ok {
				return b, true
			}
			return base, true
		}
	case "randomized_int_state_provider":
		src := g.compileStateProvider(o["source"])
		prop := o.str("property")
		vals := parseIntProvider(o["values"])
		return func(rg *Region, r Random, p BlockPos) (world.Block, bool) {
			b, _ := src(rg, r, p)
			if !world.HasProperty(b, prop) {
				return b, true
			}
			if nb, ok := world.WithProperty(b, prop, fmt.Sprint(vals(r))); ok {
				return nb, true
			}
			return b, true
		}
	case "rule_based_state_provider":
		var rules []obj
		jsonUnmarshal(o["rules"], &rules)
		type rule struct {
			pred blockPredicate
			then stateProvider
		}
		var rs []rule
		for _, ru := range rules {
			rs = append(rs, rule{g.compilePredicate(ru["if_true"]), g.compileStateProvider(ru["then"])})
		}
		var fallback stateProvider
		if f, ok := o["fallback"]; ok && strings.TrimSpace(string(f)) != "null" {
			fallback = g.compileStateProvider(f)
		}
		return func(rg *Region, r Random, p BlockPos) (world.Block, bool) {
			for _, ru := range rs {
				if ru.pred(rg, p) {
					return ru.then(rg, r, p)
				}
			}
			if fallback == nil {
				return 0, false
			}
			return fallback(rg, r, p)
		}
	case "noise_provider", "noise_threshold_provider", "dual_noise_provider":
		return g.compileNoiseProvider(t, o)
	default:
		panic("worldgen: unknown state provider " + t)
	}
}

// stateProviderOrCurrent is BlockStateProvider.getState: a rule-based
// provider with no match yields the block already there.
func (sp stateProvider) getState(rg *Region, r Random, p BlockPos) world.Block {
	if b, ok := sp(rg, r, p); ok {
		return b
	}
	return rg.Block(p[0], p[1], p[2])
}

func parseStateList(raw json.RawMessage) []world.Block {
	var list []obj
	jsonUnmarshal(raw, &list)
	out := make([]world.Block, len(list))
	for i, s := range list {
		out[i] = stateFromJSON(s)
	}
	return out
}

func (g *Generator) compileNoiseProvider(t string, o obj) stateProvider {
	seed := int64(o.f64("seed"))
	var np NoiseParams
	jsonUnmarshal(o["noise"], &np)
	noise := NewNormalNoise(NewLegacy(seed), np)
	scale := parseF32(o["scale"])
	noiseAt := func(p BlockPos, s float64) float64 {
		return noise.Value(float64(float64(p[0])*s), float64(float64(p[1])*s), float64(float64(p[2])*s))
	}
	pick := func(states []world.Block, v float64) world.Block {
		d := clampD((1+v)/2, 0, 0.9999)
		return states[d2i(float64(d*float64(len(states))))]
	}
	switch t {
	case "noise_provider":
		states := parseStateList(o["states"])
		return func(rg *Region, r Random, p BlockPos) (world.Block, bool) {
			return pick(states, noiseAt(p, float64(scale))), true
		}
	case "noise_threshold_provider":
		threshold := parseF32(o["threshold"])
		highChance := parseF32(o["high_chance"])
		var def obj
		jsonUnmarshal(o["default_state"], &def)
		defState := stateFromJSON(def)
		low := parseStateList(o["low_states"])
		high := parseStateList(o["high_states"])
		return func(rg *Region, r Random, p BlockPos) (world.Block, bool) {
			v := noiseAt(p, float64(scale))
			if v < float64(threshold) {
				return low[r.NextIntN(int32(len(low)))], true
			}
			if r.NextFloat() < highChance {
				return high[r.NextIntN(int32(len(high)))], true
			}
			return defState, true
		}
	default: // dual_noise_provider
		states := parseStateList(o["states"])
		var variety [2]int32
		jsonUnmarshal(o["variety"], &variety)
		var snp NoiseParams
		jsonUnmarshal(o["slow_noise"], &snp)
		slow := NewNormalNoise(NewLegacy(seed), snp)
		slowScale := parseF32(o["slow_scale"])
		slowAt := func(p BlockPos) float64 {
			return slow.Value(float64(float32(p[0])*slowScale), float64(float32(p[1])*slowScale), float64(float32(p[2])*slowScale))
		}
		return func(rg *Region, r Random, p BlockPos) (world.Block, bool) {
			k := d2i(clampedMap(slowAt(p), -1, 1, float64(variety[0]), float64(variety[1]+1)))
			list := make([]world.Block, 0, k)
			for i := int32(0); i < k; i++ {
				q := offset(p, i*54545, 0, i*34234)
				list = append(list, pick(states, slowAt(q)))
			}
			return pick(list, noiseAt(p, float64(scale))), true
		}
	}
}

// ruleTest is a template-system RuleTest (ore targets).
type ruleTest func(b world.Block, r Random) bool

func compileRuleTest(raw json.RawMessage) ruleTest {
	var o obj
	jsonUnmarshal(raw, &o)
	switch t := trimNS(o.str("predicate_type")); t {
	case "always_true":
		return func(world.Block, Random) bool { return true }
	case "tag_match":
		set := blockTagSet(o.str("tag"))
		return func(b world.Block, _ Random) bool { return inTag(b, set) }
	case "block_match":
		name := trimNS(o.str("block"))
		return func(b world.Block, _ Random) bool { return isBlock(b, name) }
	case "blockstate_match":
		var st obj
		jsonUnmarshal(o["block_state"], &st)
		s := stateFromJSON(st)
		return func(b world.Block, _ Random) bool { return b == s }
	case "random_block_match":
		name := trimNS(o.str("block"))
		prob := parseF32(o["probability"])
		return func(b world.Block, r Random) bool { return isBlock(b, name) && r.NextFloat() < prob }
	case "random_blockstate_match":
		var st obj
		jsonUnmarshal(o["block_state"], &st)
		s := stateFromJSON(st)
		prob := parseF32(o["probability"])
		return func(b world.Block, r Random) bool { return b == s && r.NextFloat() < prob }
	default:
		panic("worldgen: unknown rule test " + t)
	}
}
