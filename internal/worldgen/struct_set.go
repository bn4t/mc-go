package worldgen

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
)

// Structure sets and their placements (StructureSet,
// StructurePlacement, RandomSpreadStructurePlacement,
// ConcentricRingsStructurePlacement) and the per-generator placement
// state (ChunkGeneratorStructureState): which sets can generate in this
// biome source, the lazily computed concentric ring positions, and the
// per-chunk "is this a structure chunk" test with frequency reduction
// and exclusion zones.

// Placement kinds.
const (
	placeRandomSpread = iota
	placeConcentricRings
)

// Frequency reduction methods (StructurePlacement.FrequencyReductionMethod).
const (
	freqDefault = iota
	freqLegacy1 // legacyPillagerOutpostReducer
	freqLegacy2 // legacyArbitrarySaltProbabilityReducer
	freqLegacy3 // legacyProbabilityReducerWithDouble
)

type structPlacement struct {
	kind         int
	locateOffset BlockPos
	freqMethod   int
	frequency    float32
	salt         int32
	// exclusion zone: no placement within chunkCount chunks of a chunk
	// the other set places in.
	exclusionSet   string
	exclusionCount int32
	// random_spread
	spacing, separation int32
	triangular          bool
	// concentric_rings
	distance, count, spread int32
	preferred               map[string]bool

	ringOnce sync.Once
	rings    map[[2]int32]bool
	ringList [][2]int32
}

// StructureSetEntry is StructureSet.StructureSelectionEntry.
type StructureSetEntry struct {
	Structure string
	Weight    int32
}

type structureSet struct {
	name      string
	placement *structPlacement
	entries   []StructureSetEntry
}

// biomeHolderSet resolves a HolderSet<Biome> ("#tag", "id" or a list)
// to biome names; tag entries may be nested tags or {"id", "required"}.
func biomeHolderSet(data Data, raw json.RawMessage) map[string]bool {
	out := map[string]bool{}
	var add func(raw json.RawMessage)
	addName := func(n string) {
		if strings.HasPrefix(n, "#") {
			t, ok := data.Get("tags/worldgen/biome/" + trimNS(n[1:]))
			if !ok {
				return
			}
			var tag struct {
				Values []json.RawMessage `json:"values"`
			}
			jsonUnmarshal(t, &tag)
			for _, v := range tag.Values {
				add(v)
			}
			return
		}
		out[trimNS(n)] = true
	}
	add = func(raw json.RawMessage) {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			addName(s)
			return
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil {
			for _, v := range list {
				add(v)
			}
			return
		}
		var o obj
		jsonUnmarshal(raw, &o)
		addName(o.str("id"))
	}
	add(raw)
	return out
}

func parsePlacement(data Data, raw json.RawMessage) *structPlacement {
	var o obj
	jsonUnmarshal(raw, &o)
	p := &structPlacement{frequency: 1, salt: o.optI32("salt", 0)}
	if v, ok := o["locate_offset"]; ok {
		var l []int32
		jsonUnmarshal(v, &l)
		copy(p.locateOffset[:], l)
	}
	if v, ok := o["frequency"]; ok {
		p.frequency = parseF32(v)
	}
	switch o.optStr("frequency_reduction_method") {
	case "legacy_type_1":
		p.freqMethod = freqLegacy1
	case "legacy_type_2":
		p.freqMethod = freqLegacy2
	case "legacy_type_3":
		p.freqMethod = freqLegacy3
	}
	if v, ok := o["exclusion_zone"]; ok {
		var ez obj
		jsonUnmarshal(v, &ez)
		p.exclusionSet = trimNS(ez.str("other_set"))
		p.exclusionCount = ez.i32("chunk_count")
	}
	switch trimNS(o.str("type")) {
	case "random_spread":
		p.kind = placeRandomSpread
		p.spacing = o.i32("spacing")
		p.separation = o.i32("separation")
		p.triangular = o.optStr("spread_type") == "triangular"
	case "concentric_rings":
		p.kind = placeConcentricRings
		p.distance = o.i32("distance")
		p.count = o.i32("count")
		p.spread = o.i32("spread")
		p.preferred = biomeHolderSet(data, o["preferred_biomes"])
	default:
		panic("worldgen: unknown structure placement " + o.str("type"))
	}
	return p
}

// optStr and optI32 read optional fields ("" / def when absent).
func (o obj) optStr(k string) string {
	if _, ok := o[k]; !ok {
		return ""
	}
	return o.str(k)
}

func (o obj) optI32(k string, def int32) int32 {
	if _, ok := o[k]; !ok {
		return def
	}
	return o.i32(k)
}

// structState is ChunkGeneratorStructureState plus the generator's
// structure start and piece caches.
type structState struct {
	once       sync.Once
	sets       []*structureSet // possible sets, registry (id) order
	setByName  map[string]*structureSet
	structures map[string]*structureDef
	// structOrder is the structure registry order (by id).
	structOrder []string

	mu     sync.Mutex
	starts map[[2]int32]*chunkStarts
	refs   map[[2]int32][]StructureRef
}

func (g *Generator) structureState() *structState {
	st := &g.structs
	st.once.Do(func() {
		data := g.RS.data
		st.setByName = map[string]*structureSet{}
		st.structures = map[string]*structureDef{}
		for _, k := range dataKeys(data, "structure/") {
			name := strings.TrimPrefix(k, "structure/")
			st.structOrder = append(st.structOrder, name)
		}
		possible := map[string]bool{}
		for _, b := range g.Biomes.possibleBiomes() {
			possible[b] = true
		}
		for _, k := range dataKeys(data, "structure_set/") {
			raw, _ := data.Get(k)
			var o obj
			jsonUnmarshal(raw, &o)
			set := &structureSet{name: strings.TrimPrefix(k, "structure_set/"), placement: parsePlacement(data, o["placement"])}
			var entries []struct {
				Structure string `json:"structure"`
				Weight    int32  `json:"weight"`
			}
			jsonUnmarshal(o["structures"], &entries)
			for _, e := range entries {
				set.entries = append(set.entries, StructureSetEntry{trimNS(e.Structure), e.Weight})
			}
			st.setByName[set.name] = set
			// ChunkGeneratorStructureState.hasBiomesForStructureSet.
			has := false
			for _, e := range set.entries {
				for b := range g.structure(e.Structure).biomes {
					has = has || possible[b]
				}
			}
			if has {
				st.sets = append(st.sets, set)
			}
		}
		st.starts = map[[2]int32]*chunkStarts{}
		st.refs = map[[2]int32][]StructureRef{}
	})
	return st
}

// dataKeys lists the entries under prefix: the bundled ones plus any an
// overlay adds, sorted (the registry order of a datapack registry).
func dataKeys(data Data, prefix string) []string {
	seen := map[string]bool{}
	for _, k := range VanillaKeys(prefix) {
		seen[k] = true
	}
	for d := data; ; {
		ov, ok := d.(overlayData)
		if !ok {
			break
		}
		for k := range ov.top {
			if strings.HasPrefix(k, prefix) {
				seen[k] = true
			}
		}
		d = ov.base
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// isStructureChunk is StructurePlacement.isStructureChunk.
func (g *Generator) isStructureChunk(p *structPlacement, cx, cz int32) bool {
	if !g.isPlacementChunk(p, cx, cz) {
		return false
	}
	if p.frequency < 1 && !frequencyReduce(p.freqMethod, g.RS.Seed, p.salt, cx, cz, p.frequency) {
		return false
	}
	if p.exclusionSet != "" {
		other := g.structureState().setByName[p.exclusionSet]
		if other != nil && g.hasStructureChunkInRange(other.placement, cx, cz, p.exclusionCount) {
			return false
		}
	}
	return true
}

// hasStructureChunkInRange is
// ChunkGeneratorStructureState.hasStructureChunkInRange.
func (g *Generator) hasStructureChunkInRange(p *structPlacement, cx, cz, r int32) bool {
	for x := cx - r; x <= cx+r; x++ {
		for z := cz - r; z <= cz+r; z++ {
			if g.isStructureChunk(p, x, z) {
				return true
			}
		}
	}
	return false
}

func (g *Generator) isPlacementChunk(p *structPlacement, cx, cz int32) bool {
	if p.kind == placeConcentricRings {
		return g.ringPositions(p)[[2]int32{cx, cz}]
	}
	c := p.potentialChunk(g.RS.Seed, cx, cz)
	return c == [2]int32{cx, cz}
}

// potentialChunk is RandomSpreadStructurePlacement
// .getPotentialStructureChunk: the region's one candidate chunk.
func (p *structPlacement) potentialChunk(seed int64, cx, cz int32) [2]int32 {
	rx := floorDiv(cx, p.spacing)
	rz := floorDiv(cz, p.spacing)
	r := NewLegacy(0)
	setLargeFeatureWithSalt(r, seed, rx, rz, p.salt)
	n := p.spacing - p.separation
	eval := func() int32 {
		if p.triangular {
			return (r.NextIntN(n) + r.NextIntN(n)) / 2
		}
		return r.NextIntN(n)
	}
	ox := eval()
	oz := eval()
	return [2]int32{rx*p.spacing + ox, rz*p.spacing + oz}
}

// setLargeFeatureWithSalt is WorldgenRandom.setLargeFeatureWithSalt.
func setLargeFeatureWithSalt(r Random, seed int64, x, z, salt int32) {
	r.SetSeed(int64(x)*341873128712 + int64(z)*132897987541 + seed + int64(salt))
}

// frequencyReduce is FrequencyReductionMethod.shouldGenerate.
func frequencyReduce(method int, seed int64, salt, cx, cz int32, freq float32) bool {
	r := NewLegacy(0)
	switch method {
	case freqLegacy1:
		// legacyPillagerOutpostReducer: one draw discarded, then a
		// 1-in-(1/frequency) chance seeded by the 16-chunk region.
		i, j := cx>>4, cz>>4
		r.SetSeed(int64(i^j<<4) ^ seed)
		r.NextInt()
		return r.NextIntN(int32(1/freq)) == 0
	case freqLegacy2:
		setLargeFeatureWithSalt(r, seed, cx, cz, 10387320)
		return r.NextFloat() < freq
	case freqLegacy3:
		setLargeFeatureSeed(r, seed, cx, cz)
		return r.NextDouble() < float64(freq)
	}
	setLargeFeatureWithSalt(r, seed, cx, cz, salt)
	return r.NextFloat() < freq
}

// ringPositions is ChunkGeneratorStructureState.generateRingPositions:
// count positions on rings of distance-scaled radius, spread per ring
// growing ring by ring, each moved to a preferred biome found by
// BiomeSource.findBiomeHorizontal within 112 blocks.
func (g *Generator) ringPositions(p *structPlacement) map[[2]int32]bool {
	p.ringOnce.Do(func() {
		p.rings = map[[2]int32]bool{}
		if p.count == 0 {
			return
		}
		r := NewLegacy(0)
		r.SetSeed(g.RS.Seed)
		ang := r.NextDouble() * math.Pi * 2
		ring, inRing := int32(0), int32(0)
		spread := p.spread
		type job struct {
			x, z int32
			r    Random
		}
		var jobs []job
		for i := int32(0); i < p.count; i++ {
			dist := float64(4*p.distance+p.distance*ring*6) + (r.NextDouble()-0.5)*(float64(p.distance)*2.5)
			x := int32(javaRound(javaCos(ang) * dist))
			z := int32(javaRound(javaSin(ang) * dist))
			jobs = append(jobs, job{x, z, r.Fork()})
			ang += 6.283185307179586 / float64(spread)
			inRing++
			if inRing == spread {
				ring++
				inRing = 0
				spread += 2 * spread / (ring + 1)
				spread = min(spread, p.count-i)
				ang += r.NextDouble() * math.Pi * 2
			}
		}
		out := make([][2]int32, len(jobs))
		var wg sync.WaitGroup
		for i, j := range jobs {
			wg.Add(1)
			go func(i int, j job) {
				defer wg.Done()
				out[i] = [2]int32{j.x, j.z}
				if bx, bz, ok := g.findBiomeHorizontal(j.x*16+8, 0, j.z*16+8, 112, p.preferred, j.r); ok {
					out[i] = [2]int32{bx >> 4, bz >> 4}
				}
			}(i, j)
		}
		wg.Wait()
		p.ringList = out
		for _, c := range out {
			p.rings[c] = true
		}
	})
	return p.rings
}

// RingPositions lists a concentric-rings set's chunk positions in
// generation order (nil for other placements).
func (g *Generator) RingPositions(set string) [][2]int32 {
	s := g.structureState().setByName[set]
	if s == nil || s.placement.kind != placeConcentricRings {
		return nil
	}
	g.ringPositions(s.placement)
	return s.placement.ringList
}

// findBiomeHorizontal is BiomeSource.findBiomeHorizontal with step 1
// and findClosest false: every quart of the square is sampled (rows by
// z, then x) and one of the matches is kept by reservoir sampling.
func (g *Generator) findBiomeHorizontal(x, y, z, radius int32, want map[string]bool, r Random) (int32, int32, bool) {
	qx, qz := quartFromBlock(x), quartFromBlock(z)
	qr := quartFromBlock(radius)
	qy := quartFromBlock(y)
	found := false
	var bx, bz int32
	n := int32(0)
	// Each lookup starts from the previous result (Climate.RTree's
	// per-thread lastResult), which breaks exact fitness ties; a search
	// starts as on a fresh thread.
	var last *rnode
	for dz := -qr; dz <= qr; dz++ {
		for dx := -qr; dx <= qr; dx++ {
			px, pz := qx+dx, qz+dz
			var b string
			b, last = g.Biomes.biomeFrom(g.RS, px, qy, pz, last)
			if !want[b] {
				continue
			}
			if !found || r.NextIntN(n+1) == 0 {
				bx, bz = quartToBlock(px), quartToBlock(pz)
				found = true
			}
			n++
		}
	}
	return bx, bz, found
}

// biomeFrom is the uncached BiomeSource.getNoiseBiome lookup seeded by
// the previous search result (see ChunkBiomes).
func (bs *BiomeSource) biomeFrom(rs *RandomState, qx, qy, qz int32, last *rnode) (string, *rnode) {
	if bs.end || bs.tree == nil {
		return bs.Biome(rs, nil, qx, qy, qz), nil
	}
	t := rs.sampleClimate(nil, qx, qy, qz)
	last = bs.tree.searchFrom(&t, last)
	return last.leaf.biome, last
}
