package worldgen

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// Structure placement: which chunks each structure set starts its
// structures in. A random-spread set divides the world into square
// regions of spacing chunks and picks one chunk per region, kept at
// least separation chunks clear of the region's far sides; a frequency
// below 1 then keeps only a share of those, and an exclusion zone drops
// those near another set's. Strongholds instead lie on concentric rings
// around the origin, each pulled toward a preferred biome nearby.

// structureSet is a registered structure set: its structures, weighted,
// and where it places them.
type structureSet struct {
	name      string
	entries   []structureEntry
	placement structurePlacement
}

type structureEntry struct {
	structure string
	weight    int32
}

// The frequency reduction methods.
const (
	reduceDefault = iota
	reduceLegacy1 // the pillager outpost's
	reduceLegacy2
	reduceLegacy3
)

type structurePlacement struct {
	rings        bool
	salt         int32
	frequency    float32
	reduction    int
	locateOffset [3]int32
	// exclusionSet is the set whose structure chunks, within
	// exclusionChunks of a chunk, forbid this set there.
	exclusionSet    string
	exclusionChunks int32

	// Random spread.
	spacing, separation int32
	triangular          bool

	// Concentric rings.
	distance, spread, count int32
	preferred               map[string]bool
}

var structureSets = sync.OnceValue(func() map[string]*structureSet {
	out := map[string]*structureSet{}
	for name, raw := range structureSetData {
		m := raw.(map[string]any)
		s := &structureSet{name: name}
		for _, e := range m["structures"].([]any) {
			em := e.(map[string]any)
			s.entries = append(s.entries, structureEntry{em["structure"].(string), int32(em["weight"].(float64))})
		}
		pm := m["placement"].(map[string]any)
		p := &s.placement
		p.salt = int32(pm["salt"].(float64))
		p.frequency = 1
		if f, ok := pm["frequency"].(float64); ok {
			p.frequency = float32(f)
		}
		switch pm["frequency_reduction_method"] {
		case nil, "default":
			p.reduction = reduceDefault
		case "legacy_type_1":
			p.reduction = reduceLegacy1
		case "legacy_type_2":
			p.reduction = reduceLegacy2
		case "legacy_type_3":
			p.reduction = reduceLegacy3
		default:
			panic(fmt.Sprintf("worldgen: structure set %s: unknown frequency reduction %v", name, pm["frequency_reduction_method"]))
		}
		if off, ok := pm["locate_offset"].([]any); ok {
			for i := range p.locateOffset {
				p.locateOffset[i] = int32(off[i].(float64))
			}
		}
		if ez, ok := pm["exclusion_zone"].(map[string]any); ok {
			p.exclusionSet = ez["other_set"].(string)
			p.exclusionChunks = int32(ez["chunk_count"].(float64))
		}
		switch pm["type"] {
		case "minecraft:random_spread":
			p.spacing = int32(pm["spacing"].(float64))
			p.separation = int32(pm["separation"].(float64))
			p.triangular = pm["spread_type"] == "triangular"
		case "minecraft:concentric_rings":
			p.rings = true
			p.distance = int32(pm["distance"].(float64))
			p.spread = int32(pm["spread"].(float64))
			p.count = int32(pm["count"].(float64))
			p.preferred = map[string]bool{}
			for _, b := range biomeHolderSet(pm["preferred_biomes"]) {
				p.preferred[b] = true
			}
		default:
			panic(fmt.Sprintf("worldgen: structure set %s: unknown placement %v", name, pm["type"]))
		}
		out[name] = s
	}
	return out
})

// biomeHolderSet resolves a biome holder set: a tag, a single id or a
// list of ids.
func biomeHolderSet(v any) []string {
	switch t := v.(type) {
	case string:
		if len(t) > 0 && t[0] == '#' {
			list, ok := biomeTagData[t[1:]]
			if !ok {
				panic("worldgen: unknown biome tag " + t)
			}
			return list
		}
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, e.(string))
		}
		return out
	}
	panic(fmt.Sprintf("worldgen: bad biome holder set %v", v))
}

// structurePlacer places a dimension's structure sets for one seed.
type structurePlacer struct {
	g    *Generator
	seed int64
	// ringSeed seeds the stronghold rings: the level seed, except on
	// flat worlds.
	ringSeed int64
	// sets are the sets the biome source can hold a structure of, by
	// name.
	sets []*structureSet

	ringsOnce sync.Once
	rings     map[string][][2]int32
	ringSet   map[string]map[[2]int32]bool
}

func newStructurePlacer(g *Generator, seed, ringSeed int64) *structurePlacer {
	p := &structurePlacer{g: g, seed: seed, ringSeed: ringSeed}
	possible := map[string]bool{}
	for _, b := range possibleBiomes(g.source) {
		possible[b] = true
	}
	all := structureSets()
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := all[n]
		holds := false
		for _, e := range s.entries {
			for _, b := range structureBiomes(e.structure) {
				if possible[b] {
					holds = true
				}
			}
		}
		if holds {
			p.sets = append(p.sets, s)
		}
	}
	return p
}

// structureBiomes are the biomes a structure may start in.
func structureBiomes(name string) []string {
	m, ok := structureData[name].(map[string]any)
	if !ok {
		panic("worldgen: unknown structure " + name)
	}
	return biomeHolderSet(m["biomes"])
}

// potentialChunk is the chunk a random-spread placement picks in the
// region holding chunk (cx, cz).
func (pl *structurePlacement) potentialChunk(seed int64, cx, cz int32) (int32, int32) {
	rx, rz := floorDiv32(cx, pl.spacing), floorDiv32(cz, pl.spacing)
	r := NewLegacy(0)
	setLargeFeatureWithSalt(r, seed, rx, rz, pl.salt)
	n := pl.spacing - pl.separation
	spread := func() int32 {
		if pl.triangular {
			return (r.NextIntn(n) + r.NextIntn(n)) / 2
		}
		return r.NextIntn(n)
	}
	ox := spread()
	oz := spread()
	return rx*pl.spacing + ox, rz*pl.spacing + oz
}

// setLargeFeatureWithSalt seeds r from a region or chunk position and a
// salt.
func setLargeFeatureWithSalt(r *Legacy, seed int64, x, z, salt int32) {
	r.SetSeed(int64(x)*341873128712 + int64(z)*132897987541 + seed + int64(salt))
}

// keep is the frequency reduction: whether a placement chunk survives
// a frequency below 1.
func (pl *structurePlacement) keep(seed int64, cx, cz int32) bool {
	r := NewLegacy(0)
	switch pl.reduction {
	case reduceDefault:
		// The salt goes where the chunk x does, the chunk x where its z
		// does and its z where the salt does.
		setLargeFeatureWithSalt(r, seed, pl.salt, cx, cz)
		return r.NextFloat() < pl.frequency
	case reduceLegacy1:
		r.SetSeed(int64((cx>>4)^(cz>>4)<<4) ^ seed)
		r.NextInt()
		return r.NextIntn(int32(1/pl.frequency)) == 0
	case reduceLegacy2:
		setLargeFeatureWithSalt(r, seed, cx, cz, 10387320)
		return r.NextFloat() < pl.frequency
	default:
		r.setLargeFeatureSeed(seed, cx, cz)
		return r.NextDouble() < float64(pl.frequency)
	}
}

// isStructureChunk reports whether set s starts a structure in chunk
// (cx, cz).
func (p *structurePlacer) isStructureChunk(s *structureSet, cx, cz int32) bool {
	pl := &s.placement
	if pl.rings {
		p.ensureRings()
		if !p.ringSet[s.name][[2]int32{cx, cz}] {
			return false
		}
	} else if x, z := pl.potentialChunk(p.seed, cx, cz); x != cx || z != cz {
		return false
	}
	if pl.frequency < 1 && !pl.keep(p.seed, cx, cz) {
		return false
	}
	if pl.exclusionSet != "" {
		other, ok := structureSets()[pl.exclusionSet]
		if !ok {
			panic("worldgen: unknown structure set " + pl.exclusionSet)
		}
		n := pl.exclusionChunks
		for x := cx - n; x <= cx+n; x++ {
			for z := cz - n; z <= cz+n; z++ {
				if p.isStructureChunk(other, x, z) {
					return false
				}
			}
		}
	}
	return true
}

// locatePos is the block a located structure of set s in chunk (cx, cz)
// is reported at.
func (s *structureSet) locatePos(cx, cz int32) Point {
	o := s.placement.locateOffset
	return Point{cx*16 + o[0], o[1], cz*16 + o[2]}
}

// ensureRings places every concentric-rings set's rings once.
func (p *structurePlacer) ensureRings() {
	p.ringsOnce.Do(func() {
		p.rings = map[string][][2]int32{}
		p.ringSet = map[string]map[[2]int32]bool{}
		for _, s := range p.sets {
			if !s.placement.rings {
				continue
			}
			positions := p.ringPositions(&s.placement)
			p.rings[s.name] = positions
			set := map[[2]int32]bool{}
			for _, c := range positions {
				set[c] = true
			}
			p.ringSet[s.name] = set
		}
	})
}

// ringPositions lays out a concentric-rings placement: count positions
// on rings 6·distance chunks apart, spread around each ring evenly from
// a random start angle, the first ring holding spread of them and each
// later one more; each moves to a random preferred-biome quart within
// 112 blocks, if there is one.
func (p *structurePlacer) ringPositions(pl *structurePlacement) [][2]int32 {
	if pl.count == 0 {
		return nil
	}
	r := NewLegacy(p.ringSeed)
	angle := r.NextDouble() * math.Pi * 2
	spread := pl.spread
	ring, inRing := int32(0), int32(0)
	type pending struct {
		x, z int32
		fork *Legacy
	}
	var todo []pending
	for i := int32(0); i < pl.count; i++ {
		dist := float64(4*pl.distance+pl.distance*ring*6) + (r.NextDouble()-0.5)*(float64(pl.distance)*2.5)
		x := int32(javaRound(jcos(angle) * dist))
		z := int32(javaRound(jsin(angle) * dist))
		todo = append(todo, pending{x, z, r.Fork().(*Legacy)})
		angle += 6.283185307179586 / float64(spread)
		inRing++
		if inRing == spread {
			ring++
			inRing = 0
			spread += 2 * spread / (ring + 1)
			spread = min(spread, pl.count-i)
			angle += r.NextDouble() * math.Pi * 2
		}
	}
	out := make([][2]int32, len(todo))
	var wg sync.WaitGroup
	for i, t := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = [2]int32{t.x, t.z}
			if bx, bz, ok := p.findBiomeHorizontal(t.x*16+8, 0, t.z*16+8, 112, pl.preferred, t.fork); ok {
				out[i] = [2]int32{bx >> 4, bz >> 4}
			}
		}()
	}
	wg.Wait()
	return out
}

// findBiomeHorizontal picks, uniformly at random, one of the quarts
// within radius blocks of (x, z) — a square of them at the quart of y —
// whose source biome is wanted, and returns its block position.
func (p *structurePlacer) findBiomeHorizontal(x, y, z, radius int32, wanted map[string]bool, r *Legacy) (int32, int32, bool) {
	qx, qz, qr, qy := x>>2, z>>2, radius>>2, y>>2
	w := <-p.g.workers
	defer func() { p.g.workers <- w }()
	found := false
	var fx, fz int32
	count := int32(0)
	for dz := -qr; dz <= qr; dz++ {
		for dx := -qr; dx <= qr; dx++ {
			bx, bz := qx+dx, qz+dz
			if !wanted[biomeNameTable[p.g.source.biomeIndex(p.g.rs, w.searcher, bx, qy, bz)]] {
				continue
			}
			if !found || r.NextIntn(count+1) == 0 {
				found, fx, fz = true, bx<<2, bz<<2
			}
			count++
		}
	}
	return fx, fz, found
}
