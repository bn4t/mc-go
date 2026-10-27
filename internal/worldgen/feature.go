package worldgen

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
)

// Placed and configured features, decoded on first use from the
// generated data. A configured feature whose type is not implemented
// has no place function; running it reports errUnsupportedFeature.

type featureCtx struct {
	r      *decoRegion
	rand   Random
	origin Point
	top    *placedFeature
}

type configuredFeature struct {
	name  string
	kind  string
	place func(fc *featureCtx) bool
}

type placement interface {
	positions(fc *featureCtx, p Point, emit func(Point))
}

type placedFeature struct {
	name    string
	feature *configuredFeature
	mods    []placement
}

// unsupportedFeature is raised when a feature or a part of one is not
// implemented.
type unsupportedFeature struct{ what string }

func (u unsupportedFeature) Error() string { return "worldgen: unsupported " + u.what }

func unsupported(format string, args ...any) {
	panic(unsupportedFeature{fmt.Sprintf(format, args...)})
}

var (
	featureMu  sync.Mutex
	configured = map[string]*configuredFeature{}
	placed     = map[string]*placedFeature{}
)

// featureKinds decode a configured feature's settings into its place
// function.
var featureKinds = map[string]func(cfg any) func(fc *featureCtx) bool{}

func lookupConfigured(name string) *configuredFeature {
	featureMu.Lock()
	cf, ok := configured[name]
	featureMu.Unlock()
	if ok {
		return cf
	}
	raw, ok := configuredFeatureData[name]
	if !ok {
		panic("worldgen: unknown configured feature " + name)
	}
	cf = decodeConfigured(raw)
	cf.name = name
	featureMu.Lock()
	if prev, ok := configured[name]; ok {
		cf = prev
	} else {
		configured[name] = cf
	}
	featureMu.Unlock()
	return cf
}

func lookupPlaced(name string) *placedFeature {
	featureMu.Lock()
	pf, ok := placed[name]
	featureMu.Unlock()
	if ok {
		return pf
	}
	raw, ok := placedFeatureData[name]
	if !ok {
		panic("worldgen: unknown placed feature " + name)
	}
	pf = decodePlaced(raw)
	pf.name = name
	featureMu.Lock()
	if prev, ok := placed[name]; ok {
		pf = prev
	} else {
		placed[name] = pf
	}
	featureMu.Unlock()
	return pf
}

func decodeConfigured(v any) *configuredFeature {
	if name, ok := v.(string); ok {
		return lookupConfigured(name)
	}
	cf := &configuredFeature{kind: ftype(v)}
	if dec, ok := featureKinds[cf.kind]; ok {
		cf.place = deferUnsupported(func() func(*featureCtx) bool { return dec(ffield(v, "config")) })
	}
	return cf
}

// deferUnsupported decodes, turning a decode-time unsupported part into
// a place function that reports it when run.
func deferUnsupported(decode func() func(*featureCtx) bool) (place func(*featureCtx) bool) {
	defer func() {
		if e := recover(); e != nil {
			u, ok := e.(unsupportedFeature)
			if !ok {
				panic(e)
			}
			place = func(*featureCtx) bool { panic(u) }
		}
	}()
	return decode()
}

// decodePlaced reads a placed feature: a registry name or an inline
// feature with its placement.
func decodePlaced(v any) *placedFeature {
	if name, ok := v.(string); ok {
		return lookupPlaced(name)
	}
	pf := &placedFeature{feature: decodeConfigured(ffield(v, "feature"))}
	func() {
		defer func() {
			if e := recover(); e != nil {
				u, ok := e.(unsupportedFeature)
				if !ok {
					panic(e)
				}
				pf.mods = []placement{failing{u}}
			}
		}()
		for _, m := range flist(ffield(v, "placement")) {
			pf.mods = append(pf.mods, decodePlacement(m))
		}
	}()
	return pf
}

// failing is a placement that could not be decoded.
type failing struct{ err unsupportedFeature }

func (f failing) positions(*featureCtx, Point, func(Point)) { panic(f.err) }

// placeWithBiomeCheck runs a top-level placed feature: positions flow
// through the placement modifiers one at a time, each reaching the
// feature before the next is produced.
func (pf *placedFeature) placeWithBiomeCheck(r *decoRegion, random Random, origin Point) bool {
	return pf.placeIn(&featureCtx{r: r, rand: random, top: pf}, origin)
}

func (pf *placedFeature) placeIn(fc *featureCtx, origin Point) bool {
	placedAny := false
	var walk func(i int, p Point)
	walk = func(i int, p Point) {
		if i == len(pf.mods) {
			if pf.feature.run(fc.r, fc.rand, p) {
				placedAny = true
			}
			return
		}
		pf.mods[i].positions(fc, p, func(q Point) { walk(i+1, q) })
	}
	walk(0, origin)
	return placedAny
}

// run places the configured feature at p: nothing outside the region's
// write zone.
func (cf *configuredFeature) run(r *decoRegion, random Random, p Point) bool {
	if !r.canWrite(p) {
		return false
	}
	if cf.place == nil {
		unsupported("feature type %s", cf.kind)
	}
	return cf.place(&featureCtx{r: r, rand: random, origin: p})
}

// Placement modifiers.

func decodePlacement(v any) placement {
	switch ftype(v) {
	case "minecraft:count":
		return countPlacement{decodeIntProvider(ffield(v, "count"))}
	case "minecraft:in_square":
		return inSquare{}
	case "minecraft:height_range":
		return heightRange{decodeHeightProvider(ffield(v, "height"))}
	case "minecraft:biome":
		return biomeFilter{}
	case "minecraft:heightmap":
		return heightmapPlacement{heightmapNames[fstr(ffield(v, "heightmap"))]}
	case "minecraft:random_offset":
		return randomOffset{decodeIntProvider(ffield(v, "xz_spread")), decodeIntProvider(ffield(v, "y_spread"))}
	case "minecraft:rarity_filter":
		return rarityFilter{fint(ffield(v, "chance"), 1)}
	case "minecraft:block_predicate_filter":
		return predicateFilter{decodeBlockPredicate(ffield(v, "predicate"))}
	case "minecraft:surface_water_depth_filter":
		return waterDepthFilter{fint(ffield(v, "max_water_depth"), 0)}
	case "minecraft:surface_relative_threshold_filter":
		return relativeThreshold{heightmapNames[fstr(ffield(v, "heightmap"))],
			int64(fint(ffield(v, "min_inclusive"), math.MinInt32)), int64(fint(ffield(v, "max_inclusive"), math.MaxInt32))}
	case "minecraft:environment_scan":
		target := decodeBlockPredicate(ffield(v, "target_condition"))
		allowed := blockPredicate(predTrue)
		if fhas(v, "allowed_search_condition") {
			allowed = decodeBlockPredicate(ffield(v, "allowed_search_condition"))
		}
		dir := dirUp
		if fstr(ffield(v, "direction_of_search")) == "down" {
			dir = dirDown
		}
		return envScan{dir, target, allowed, fint(ffield(v, "max_steps"), 0)}
	case "minecraft:count_on_every_layer":
		return everyLayer{decodeIntProvider(ffield(v, "count"))}
	case "minecraft:noise_threshold_count":
		return noiseThresholdCount{fnum(ffield(v, "noise_level"), 0), fint(ffield(v, "below_noise"), 0), fint(ffield(v, "above_noise"), 0)}
	case "minecraft:noise_based_count":
		return noiseBasedCount{fint(ffield(v, "noise_to_count_ratio"), 0), fnum(ffield(v, "noise_factor"), 0), fnum(ffield(v, "noise_offset"), 0)}
	case "minecraft:fixed_placement":
		var f fixedPlacement
		for _, p := range flist(ffield(v, "positions")) {
			l := flist(p)
			f = append(f, Point{fint(l[0], 0), fint(l[1], 0), fint(l[2], 0)})
		}
		return f
	}
	unsupported("placement %v", ftype(v))
	return nil
}

type countPlacement struct{ count intProvider }

func (c countPlacement) positions(fc *featureCtx, p Point, emit func(Point)) {
	n := c.count.sample(fc.rand)
	for i := int32(0); i < n; i++ {
		emit(p)
	}
}

type inSquare struct{}

func (inSquare) positions(fc *featureCtx, p Point, emit func(Point)) {
	x := fc.rand.NextIntn(16) + p.X
	z := fc.rand.NextIntn(16) + p.Z
	emit(Point{x, p.Y, z})
}

type heightRange struct{ height heightSampler }

func (h heightRange) positions(fc *featureCtx, p Point, emit func(Point)) {
	emit(Point{p.X, h.height.sample(fc.rand, fc.r.genMinY, fc.r.genDepth), p.Z})
}

type biomeFilter struct{}

func (biomeFilter) positions(fc *featureCtx, p Point, emit func(Point)) {
	if fc.top == nil {
		panic("worldgen: biome check of a feature with no top feature")
	}
	if biomeHasFeature(fc.r.biome(p), fc.top.name) {
		emit(p)
	}
}

type heightmapPlacement struct{ kind int }

func (h heightmapPlacement) positions(fc *featureCtx, p Point, emit func(Point)) {
	y := fc.r.heightAt(h.kind, p.X, p.Z)
	if y > fc.r.minY {
		emit(Point{p.X, y, p.Z})
	}
}

type randomOffset struct{ xz, y intProvider }

func (o randomOffset) positions(fc *featureCtx, p Point, emit func(Point)) {
	x := p.X + o.xz.sample(fc.rand)
	y := p.Y + o.y.sample(fc.rand)
	z := p.Z + o.xz.sample(fc.rand)
	emit(Point{x, y, z})
}

type rarityFilter struct{ chance int32 }

func (f rarityFilter) positions(fc *featureCtx, p Point, emit func(Point)) {
	if fc.rand.NextFloat() < 1/float32(f.chance) {
		emit(p)
	}
}

type predicateFilter struct{ pred blockPredicate }

func (f predicateFilter) positions(fc *featureCtx, p Point, emit func(Point)) {
	if f.pred(fc.r, p) {
		emit(p)
	}
}

type waterDepthFilter struct{ maxDepth int32 }

func (f waterDepthFilter) positions(fc *featureCtx, p Point, emit func(Point)) {
	floor := fc.r.heightAt(hmOceanFloor, p.X, p.Z)
	surface := fc.r.heightAt(hmWorldSurface, p.X, p.Z)
	if surface-floor <= f.maxDepth {
		emit(p)
	}
}

type relativeThreshold struct {
	kind   int
	lo, hi int64
}

func (f relativeThreshold) positions(fc *featureCtx, p Point, emit func(Point)) {
	h := int64(fc.r.heightAt(f.kind, p.X, p.Z))
	if h+f.lo <= int64(p.Y) && int64(p.Y) <= h+f.hi {
		emit(p)
	}
}

type envScan struct {
	dir             int
	target, allowed blockPredicate
	maxSteps        int32
}

func (e envScan) positions(fc *featureCtx, p Point, emit func(Point)) {
	if !e.allowed(fc.r, p) {
		return
	}
	d := dirOffsets[e.dir]
	for i := int32(0); i < e.maxSteps; i++ {
		if e.target(fc.r, p) {
			emit(p)
			return
		}
		p = Point{p.X + d[0], p.Y + d[1], p.Z + d[2]}
		if fc.r.outside(p.Y) {
			return
		}
		if !e.allowed(fc.r, p) {
			break
		}
	}
	if e.target(fc.r, p) {
		emit(p)
	}
}

type everyLayer struct{ count intProvider }

func (e everyLayer) positions(fc *featureCtx, p Point, emit func(Point)) {
	var out []Point
	for layer := int32(0); ; layer++ {
		found := false
		for i := int32(0); i < e.count.sample(fc.rand); i++ {
			x := fc.rand.NextIntn(16) + p.X
			z := fc.rand.NextIntn(16) + p.Z
			h := fc.r.heightAt(hmMotionBlocking, x, z)
			if y, ok := groundLayerY(fc.r, x, h, z, layer); ok {
				out = append(out, Point{x, y, z})
				found = true
			}
		}
		if !found {
			break
		}
	}
	for _, q := range out {
		emit(q)
	}
}

// groundLayerY finds the target-th surface (an empty block over a
// non-empty, non-bedrock one) scanning down from y.
func groundLayerY(r *decoRegion, x, y, z, target int32) (int32, bool) {
	empty := func(s StateID) bool { return s.isAir() || s.is("water") || s.is("lava") }
	layer := int32(0)
	above := r.get(Point{x, y, z})
	for yy := y; yy >= r.minY+1; yy-- {
		below := r.get(Point{x, yy - 1, z})
		if !empty(below) && empty(above) && !below.is("bedrock") {
			if layer == target {
				return yy, true
			}
			layer++
		}
		above = below
	}
	return 0, false
}

type noiseThresholdCount struct {
	level        float64
	below, above int32
}

func (n noiseThresholdCount) positions(fc *featureCtx, p Point, emit func(Point)) {
	climateNoises()
	v := biomeInfoNoise.Value(float64(p.X)/200, float64(p.Z)/200)
	c := n.above
	if v < n.level {
		c = n.below
	}
	for i := int32(0); i < c; i++ {
		emit(p)
	}
}

type noiseBasedCount struct {
	ratio          int32
	factor, offset float64
}

func (n noiseBasedCount) positions(fc *featureCtx, p Point, emit func(Point)) {
	climateNoises()
	v := biomeInfoNoise.Value(float64(p.X)/n.factor, float64(p.Z)/n.factor)
	c := int32(math.Ceil((v + n.offset) * float64(n.ratio)))
	for i := int32(0); i < c; i++ {
		emit(p)
	}
}

type fixedPlacement []Point

func (f fixedPlacement) positions(fc *featureCtx, p Point, emit func(Point)) {
	cx, cz := p.X>>4, p.Z>>4
	for _, q := range f {
		if q.X>>4 == cx && q.Z>>4 == cz {
			emit(q)
		}
	}
}

// biomeHasFeature reports whether a biome lists a placed feature in any
// step.
func biomeHasFeature(biome, feature string) bool {
	biomeFeatureSetsOnce.Do(func() {
		biomeFeatureSets = map[string]map[string]bool{}
		for b, steps := range biomeFeatures {
			set := map[string]bool{}
			for _, step := range steps {
				for _, f := range step {
					set[f] = true
				}
			}
			biomeFeatureSets[b] = set
		}
	})
	return biomeFeatureSets[biome][feature]
}

var (
	biomeFeatureSetsOnce sync.Once
	biomeFeatureSets     map[string]map[string]bool
)

// Block predicates.

type blockPredicate func(r *decoRegion, p Point) bool

func predTrue(*decoRegion, Point) bool { return true }

func decodeOffset(v any) Point {
	l := flist(ffield(v, "offset"))
	if len(l) != 3 {
		return Point{}
	}
	return Point{fint(l[0], 0), fint(l[1], 0), fint(l[2], 0)}
}

func (p Point) add(o Point) Point { return Point{p.X + o.X, p.Y + o.Y, p.Z + o.Z} }

func (p Point) offset(dir int) Point {
	d := dirOffsets[dir]
	return Point{p.X + d[0], p.Y + d[1], p.Z + d[2]}
}

var directionNames = map[string]int{"down": dirDown, "up": dirUp, "north": dirNorth, "south": dirSouth, "west": dirWest, "east": dirEast}

func decodeBlockPredicate(v any) blockPredicate {
	off := decodeOffset(v)
	switch ftype(v) {
	case "minecraft:true":
		return predTrue
	case "minecraft:matching_blocks":
		set := map[string]bool{}
		for _, b := range flist(ffield(v, "blocks")) {
			name := fstr(b)
			if strings.HasPrefix(name, "#") {
				for k := range blockTagSet(name) {
					set[k] = true
				}
				continue
			}
			set[strings.TrimPrefix(name, "minecraft:")] = true
		}
		return func(r *decoRegion, p Point) bool { return set[r.get(p.add(off)).block()] }
	case "minecraft:matching_block_tag":
		tag := fstr(ffield(v, "tag"))
		return func(r *decoRegion, p Point) bool { return r.get(p.add(off)).inTag(tag) }
	case "minecraft:matching_fluids":
		var water, flowingWater, lava, flowingLava, empty bool
		for _, f := range flist(ffield(v, "fluids")) {
			switch fstr(f) {
			case "minecraft:water":
				water = true
			case "minecraft:flowing_water":
				flowingWater = true
			case "minecraft:lava":
				lava = true
			case "minecraft:flowing_lava":
				flowingLava = true
			case "minecraft:empty":
				empty = true
			default:
				unsupported("fluid %v", f)
			}
		}
		return func(r *decoRegion, p Point) bool {
			s := r.get(p.add(off))
			if !s.hasFluid() {
				return empty
			}
			src := s.isSourceFluid()
			if s.isWaterFluid() {
				return (src && water) || (!src && flowingWater)
			}
			return (src && lava) || (!src && flowingLava)
		}
	case "minecraft:solid":
		return func(r *decoRegion, p Point) bool { return r.get(p.add(off)).isSolid() }
	case "minecraft:replaceable":
		return func(r *decoRegion, p Point) bool { return r.get(p.add(off)).canBeReplaced() }
	case "minecraft:would_survive":
		state := blockStateOf(ffield(v, "state"))
		return func(r *decoRegion, p Point) bool { return canSurvive(r, state, p.add(off)) }
	case "minecraft:has_sturdy_face":
		dir := directionNames[fstr(ffield(v, "direction"))]
		return func(r *decoRegion, p Point) bool { return r.get(p.add(off)).sturdy(dir) }
	case "minecraft:inside_world_bounds":
		return func(r *decoRegion, p Point) bool { return !r.outside(p.add(off).Y) }
	case "minecraft:all_of", "minecraft:any_of":
		var preds []blockPredicate
		for _, e := range flist(ffield(v, "predicates")) {
			preds = append(preds, decodeBlockPredicate(e))
		}
		all := ftype(v) == "minecraft:all_of"
		return func(r *decoRegion, p Point) bool {
			for _, pr := range preds {
				if pr(r, p) != all {
					return !all
				}
			}
			return all
		}
	case "minecraft:not":
		inner := decodeBlockPredicate(ffield(v, "predicate"))
		return func(r *decoRegion, p Point) bool { return !inner(r, p) }
	}
	unsupported("block predicate %v", ftype(v))
	return nil
}

// canSurvive is BlockState.canSurvive for the states features place.
func canSurvive(r *decoRegion, s StateID, p Point) bool {
	unsupported("survival of %s", StateName(s))
	return false
}

// Feature order.

// featureSteps is a dimension's placed features per decoration step in
// the generator's global order: a depth-first topological order of every
// possible biome's per-step lists, so each biome's order is kept.
func featureSteps(possibleBiomes []string) [][]*placedFeature {
	type node struct {
		step, index int
	}
	ids := map[string]int{}
	var names []string
	graph := map[node]map[node]bool{}
	maxSteps := 0
	for _, b := range possibleBiomes {
		steps := biomeFeatures[b]
		maxSteps = max(maxSteps, len(steps))
		var flat []node
		for step, list := range steps {
			for _, name := range list {
				id, ok := ids[name]
				if !ok {
					id = len(names)
					ids[name] = id
					names = append(names, name)
				}
				flat = append(flat, node{step, id})
			}
		}
		for i, n := range flat {
			if graph[n] == nil {
				graph[n] = map[node]bool{}
			}
			if i+1 < len(flat) {
				graph[n][flat[i+1]] = true
			}
		}
	}
	less := func(a, b node) bool {
		if a.step != b.step {
			return a.step < b.step
		}
		return a.index < b.index
	}
	sortedNodes := func(m map[node]bool) []node {
		out := make([]node, 0, len(m))
		for n := range m {
			out = append(out, n)
		}
		sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
		return out
	}
	keys := map[node]bool{}
	for n := range graph {
		keys[n] = true
	}
	visited, onStack := map[node]bool{}, map[node]bool{}
	var order []node
	var dfs func(n node) bool
	dfs = func(n node) bool {
		if visited[n] {
			return false
		}
		if onStack[n] {
			return true
		}
		onStack[n] = true
		for _, next := range sortedNodes(graph[n]) {
			if dfs(next) {
				return true
			}
		}
		delete(onStack, n)
		visited[n] = true
		order = append(order, n)
		return false
	}
	for _, n := range sortedNodes(keys) {
		if !visited[n] && dfs(n) {
			panic("worldgen: feature order cycle")
		}
	}
	steps := make([][]*placedFeature, maxSteps)
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		steps[n.step] = append(steps[n.step], lookupPlaced(names[n.index]))
	}
	return steps
}

// featureIndex is a placed feature's index in its step.
func featureIndex(steps [][]*placedFeature, step int, name string) int {
	for i, f := range steps[step] {
		if f.name == name {
			return i
		}
	}
	return -1
}

// possibleBiomes are a biome source's biomes in the order the generator
// lists them: a parameter list's biomes by first appearance, the End's
// in its fixed order.
func possibleBiomes(src BiomeSource) []string {
	switch s := src.(type) {
	case *MultiNoiseBiomes:
		var out []string
		seen := map[uint8]bool{}
		for _, p := range s.points {
			if !seen[p.biome] {
				seen[p.biome] = true
				out = append(out, climateBiomeNames[p.biome])
			}
		}
		return out
	case EndBiomeSource:
		return append([]string(nil), endBiomes...)
	}
	panic("worldgen: unknown biome source")
}
