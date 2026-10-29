package worldgen

import (
	"sync"

)

// Structure starts, pieces and references: the STRUCTURE_STARTS and
// STRUCTURE_REFERENCES stages (ChunkGenerator.createStructures /
// createReferences), Structure.generate with its GenerationContext and
// GenerationStub, StructureStart and the StructurePiece contract the
// structure types implement.
//
// Starts are cached per chunk for the generator's lifetime (they are a
// pure function of seed and data, so a save may also install them with
// SetStructureStarts); references are a bounded cache recomputed from
// the starts.
//
// Adding a structure type: register a builder in structureTypes (keyed
// by the structure JSON "type", namespace trimmed). The builder compiles
// the type's config once and returns findGenerationPoint: given the
// GenContext it returns a stub (the position the biome check samples)
// whose Generate callback adds pieces; nothing is drawn from the
// context random before the biome check passes unless the vanilla
// structure does so in findGenerationPoint itself. Pieces implement
// StructurePiece; PostProcess writes blocks clipped to the chunk box.
// Non-jigsaw pieces count as rigid (ground level delta 0) for the
// beardifier when their structure has terrain adaptation. Saving: a
// piece type adds its fields by implementing pieceSaver and registers a
// loader in pieceLoaders (struct_nbt.go).

// BoundingBox is an inclusive block box (BoundingBox).
type BoundingBox struct{ MinX, MinY, MinZ, MaxX, MaxY, MaxZ int32 }

func (b BoundingBox) internal() box { return box{b.MinX, b.MinY, b.MinZ, b.MaxX, b.MaxY, b.MaxZ} }

func bbFrom(b box) BoundingBox { return BoundingBox{b.x0, b.y0, b.z0, b.x1, b.y1, b.z1} }

// Intersects is BoundingBox.intersects(BoundingBox).
func (b BoundingBox) Intersects(o BoundingBox) bool {
	return b.MaxX >= o.MinX && b.MinX <= o.MaxX && b.MaxZ >= o.MinZ && b.MinZ <= o.MaxZ && b.MaxY >= o.MinY && b.MinY <= o.MaxY
}

// IntersectsXZ is BoundingBox.intersects(minX, minZ, maxX, maxZ).
func (b BoundingBox) IntersectsXZ(minX, minZ, maxX, maxZ int32) bool {
	return b.MaxX >= minX && b.MinX <= maxX && b.MaxZ >= minZ && b.MinZ <= maxZ
}

// IsInside is BoundingBox.isInside(pos).
func (b BoundingBox) IsInside(p BlockPos) bool {
	return p[0] >= b.MinX && p[0] <= b.MaxX && p[2] >= b.MinZ && p[2] <= b.MaxZ && p[1] >= b.MinY && p[1] <= b.MaxY
}

// Moved is BoundingBox.moved.
func (b BoundingBox) Moved(dx, dy, dz int32) BoundingBox {
	return BoundingBox{b.MinX + dx, b.MinY + dy, b.MinZ + dz, b.MaxX + dx, b.MaxY + dy, b.MaxZ + dz}
}

// Inflated is BoundingBox.inflatedBy(n).
func (b BoundingBox) Inflated(n int32) BoundingBox {
	return BoundingBox{b.MinX - n, b.MinY - n, b.MinZ - n, b.MaxX + n, b.MaxY + n, b.MaxZ + n}
}

// Encapsulate is BoundingBox.encapsulate(other).
func (b BoundingBox) Encapsulate(o BoundingBox) BoundingBox {
	return BoundingBox{min(b.MinX, o.MinX), min(b.MinY, o.MinY), min(b.MinZ, o.MinZ), max(b.MaxX, o.MaxX), max(b.MaxY, o.MaxY), max(b.MaxZ, o.MaxZ)}
}

// Center is BoundingBox.getCenter.
func (b BoundingBox) Center() BlockPos {
	return BlockPos{b.MinX + (b.MaxX-b.MinX+1)/2, b.MinY + (b.MaxY-b.MinY+1)/2, b.MinZ + (b.MaxZ-b.MinZ+1)/2}
}

// YSpan is BoundingBox.getYSpan.
func (b BoundingBox) YSpan() int32 { return b.MaxY - b.MinY + 1 }

// PieceContext is what StructurePiece.postProcess receives: the
// decorating region, the per-structure feature-seeded random, the
// writable box of the chunk being decorated and the start's reference
// position (its first piece's bottom center).
type PieceContext struct {
	G        *Generator
	Region   *Region
	Random   Random
	ChunkBox BoundingBox
	Chunk    [2]int32
	Pivot    BlockPos
}

// StructurePiece is one piece of a start.
type StructurePiece interface {
	// Type is the StructurePieceType id ("jigsaw", "mscorridor", ...).
	Type() string
	BoundingBox() BoundingBox
	// GenDepth and Orientation are StructurePiece.genDepth and the
	// saved orientation ("O": 2D data value, -1 for none).
	GenDepth() int32
	Orientation() int32
	Move(dx, dy, dz int32)
	PostProcess(c *PieceContext)
}

// StructureStart is a structure's start in one chunk.
type StructureStart struct {
	Structure  string // structure id, namespace trimmed
	ChunkX     int32
	ChunkZ     int32
	References int32
	Pieces     []StructurePiece

	def  *structureDef
	bb   BoundingBox
	bbOK bool
}

// BoundingBox is StructureStart.getBoundingBox: the pieces' union,
// inflated by 12 for structures with terrain adaptation.
func (s *StructureStart) BoundingBox() BoundingBox {
	if !s.bbOK {
		b := s.Pieces[0].BoundingBox()
		for _, p := range s.Pieces[1:] {
			b = b.Encapsulate(p.BoundingBox())
		}
		if s.def.adaptation != adaptNone {
			b = b.Inflated(12)
		}
		s.bb, s.bbOK = b, true
	}
	return s.bb
}

// Step is the structure's decoration step ordinal.
func (s *StructureStart) Step() int { return s.def.step }

// Terrain adaptations (TerrainAdjustment, ordinal order).
const (
	adaptNone = iota
	adaptBury
	adaptBeardThin
	adaptBeardBox
	adaptEncapsulate
)

var adaptNames = map[string]int{"none": adaptNone, "bury": adaptBury, "beard_thin": adaptBeardThin, "beard_box": adaptBeardBox, "encapsulate": adaptEncapsulate}

// Decoration steps (GenerationStep.Decoration ordinals).
var decorationSteps = map[string]int{
	"raw_generation": 0, "lakes": 1, "local_modifications": 2, "underground_structures": 3,
	"surface_structures": 4, "strongholds": 5, "underground_ores": 6, "underground_decoration": 7,
	"fluid_springs": 8, "vegetal_decoration": 9, "top_layer_modification": 10,
}

// structureDef is a Structure: the shared settings plus the compiled
// type-specific generation point finder.
type structureDef struct {
	name       string
	typ        string
	biomes     map[string]bool
	step       int
	adaptation int
	raw        obj
	find       func(c *GenContext) (*GenerationStub, bool)
	// afterPlace is Structure.afterPlace, run after the pieces of every
	// placeInChunk (nil: nothing).
	afterPlace func(c *PieceContext, s *StructureStart)
}

// GenContext is Structure.GenerationContext.
type GenContext struct {
	G      *Generator
	Random Random // WorldgenRandom(LegacyRandomSource) at the large-feature seed
	ChunkX int32
	ChunkZ int32
	Seed   int64
	Def    *structureDef
}

// GenerationStub is Structure.GenerationStub: the biome-check position
// and the deferred piece generator.
type GenerationStub struct {
	Pos      BlockPos
	Generate func(add func(StructurePiece))
}

// structureTypes compiles a structure's type config.
var structureTypes = map[string]func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool){}

// UnsupportedStructures collects structure types without a builder.
var UnsupportedStructures = map[string]bool{}

var structureDefMu sync.Mutex

func (g *Generator) structure(name string) *structureDef {
	st := &g.structs
	structureDefMu.Lock()
	defer structureDefMu.Unlock()
	if d, ok := st.structures[name]; ok {
		return d
	}
	raw, ok := g.RS.data.Get("structure/" + name)
	if !ok {
		panic("worldgen: unknown structure " + name)
	}
	var o obj
	jsonUnmarshal(raw, &o)
	d := &structureDef{name: name, typ: trimNS(o.str("type")), raw: o}
	d.biomes = biomeHolderSet(g.RS.data, o["biomes"])
	d.step = decorationSteps[o.str("step")]
	d.adaptation = adaptNames[o.optStr("terrain_adaptation")]
	if b, ok := structureTypes[d.typ]; ok {
		d.find = b(g, d)
	} else {
		UnsupportedStructures[d.typ] = true
	}
	st.structures[name] = d
	return d
}

// chunkStarts is one chunk's STRUCTURE_STARTS result.
type chunkStarts struct {
	done   chan struct{}
	starts []*StructureStart
}

// StructureStarts returns the starts generated in chunk (cx, cz)
// (ChunkGenerator.createStructures), in creation order; nil when
// structures are off.
func (g *Generator) StructureStarts(cx, cz int32) []*StructureStart {
	if !g.Structures {
		return nil
	}
	st := g.structureState()
	k := [2]int32{cx, cz}
	st.mu.Lock()
	cs, ok := st.starts[k]
	if ok {
		st.mu.Unlock()
		<-cs.done
		return cs.starts
	}
	cs = &chunkStarts{done: make(chan struct{})}
	st.starts[k] = cs
	st.mu.Unlock()
	cs.starts = g.createStructures(cx, cz)
	close(cs.done)
	return cs.starts
}

func (g *Generator) createStructures(cx, cz int32) []*StructureStart {
	st := g.structureState()
	var out []*StructureStart
	have := map[string]bool{}
	for _, set := range st.sets {
		skip := false
		for _, e := range set.entries {
			skip = skip || have[e.Structure]
		}
		if skip || !g.isStructureChunk(set.placement, cx, cz) {
			continue
		}
		if len(set.entries) == 1 {
			if s := g.tryGenerateStructure(set.entries[0].Structure, cx, cz); s != nil {
				out = append(out, s)
				have[s.Structure] = true
			}
			continue
		}
		// Weighted selection without replacement until one generates.
		list := append([]StructureSetEntry(nil), set.entries...)
		r := NewLegacy(0)
		setLargeFeatureSeed(r, g.RS.Seed, cx, cz)
		total := int32(0)
		for _, e := range list {
			total += e.Weight
		}
		for len(list) > 0 {
			n := r.NextIntN(total)
			i := 0
			for ; i < len(list); i++ {
				n -= list[i].Weight
				if n < 0 {
					break
				}
			}
			e := list[i]
			if g.structure(e.Structure).find == nil {
				// A type without a builder would have been tried here;
				// falling through to the next entry would place a
				// structure vanilla does not, so the set stops.
				break
			}
			if s := g.tryGenerateStructure(e.Structure, cx, cz); s != nil {
				out = append(out, s)
				have[s.Structure] = true
				break
			}
			list = append(list[:i], list[i+1:]...)
			total -= e.Weight
		}
	}
	return out
}

// tryGenerateStructure is ChunkGenerator.tryGenerateStructure +
// Structure.generate: the generation point, its biome check at the
// stub position's quart (uncached biome source, no y clamp), then the
// pieces; a start with no pieces is invalid.
func (g *Generator) tryGenerateStructure(name string, cx, cz int32) *StructureStart {
	d := g.structure(name)
	if d.find == nil {
		return nil
	}
	r := NewLegacy(0)
	setLargeFeatureSeed(r, g.RS.Seed, cx, cz)
	c := &GenContext{G: g, Random: r, ChunkX: cx, ChunkZ: cz, Seed: g.RS.Seed, Def: d}
	stub, ok := d.find(c)
	if !ok {
		return nil
	}
	b := g.Biomes.Biome(g.RS, nil, quartFromBlock(stub.Pos[0]), quartFromBlock(stub.Pos[1]), quartFromBlock(stub.Pos[2]))
	if !d.biomes[b] {
		return nil
	}
	s := &StructureStart{Structure: name, ChunkX: cx, ChunkZ: cz, def: d}
	stub.Generate(func(p StructurePiece) { s.Pieces = append(s.Pieces, p) })
	if len(s.Pieces) == 0 {
		return nil
	}
	return s
}

// StructureRef is one STRUCTURE_REFERENCES entry: a structure and the
// start chunks whose box reaches this chunk, in the iteration order of
// the chunk's LongOpenHashSet.
type StructureRef struct {
	Structure string
	Starts    [][2]int32
}

// StructureReferences is ChunkGenerator.createReferences for chunk
// (cx, cz): starts within 8 chunks whose bounding box intersects the
// chunk's columns, grouped by structure (registry order).
func (g *Generator) StructureReferences(cx, cz int32) []StructureRef {
	if !g.Structures {
		return nil
	}
	st := g.structureState()
	k := [2]int32{cx, cz}
	st.mu.Lock()
	if r, ok := st.refs[k]; ok {
		st.mu.Unlock()
		return r
	}
	st.mu.Unlock()
	r := g.createReferences(cx, cz)
	st.mu.Lock()
	if len(st.refs) >= 1<<16 {
		st.refs = map[[2]int32][]StructureRef{}
	}
	st.refs[k] = r
	st.mu.Unlock()
	return r
}

// StructuresIntersecting returns the starts whose (adapted) box reaches
// chunk (cx, cz): StructureManager.startsForStructure over all the
// chunk's references.
func (g *Generator) StructuresIntersecting(cx, cz int32) []*StructureStart {
	return g.startsReferencing(cx, cz, nil)
}

func (g *Generator) createReferences(cx, cz int32) []StructureRef {
	byStruct := map[string]*longOpenHashSet{}
	minX, minZ := cx*16, cz*16
	for x := cx - 8; x <= cx+8; x++ {
		for z := cz - 8; z <= cz+8; z++ {
			for _, s := range g.StructureStarts(x, z) {
				if s.BoundingBox().IntersectsXZ(minX, minZ, minX+15, minZ+15) {
					set := byStruct[s.Structure]
					if set == nil {
						set = newLongOpenHashSet()
						byStruct[s.Structure] = set
					}
					set.add(packChunk(x, z))
				}
			}
		}
	}
	var out []StructureRef
	for _, name := range g.structureState().structOrder {
		set := byStruct[name]
		if set == nil {
			continue
		}
		ref := StructureRef{Structure: name}
		for _, l := range set.list() {
			ref.Starts = append(ref.Starts, [2]int32{int32(l), int32(l >> 32)})
		}
		out = append(out, ref)
	}
	return out
}

// startsReferencing is StructureManager.startsForStructure over every
// structure referenced from chunk (cx, cz) that keep accepts.
func (g *Generator) startsReferencing(cx, cz int32, keep func(*structureDef) bool) []*StructureStart {
	var out []*StructureStart
	for _, ref := range g.StructureReferences(cx, cz) {
		d := g.structure(ref.Structure)
		if keep != nil && !keep(d) {
			continue
		}
		for _, c := range ref.Starts {
			for _, s := range g.StructureStarts(c[0], c[1]) {
				if s.Structure == ref.Structure {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// placeInChunk is StructureStart.placeInChunk.
func (s *StructureStart) placeInChunk(g *Generator, rg *Region, r Random, chunkBox BoundingBox, chunk [2]int32) {
	if len(s.Pieces) == 0 {
		return
	}
	first := s.Pieces[0].BoundingBox()
	c := first.Center()
	pivot := BlockPos{c[0], first.MinY, c[2]}
	pc := &PieceContext{G: g, Region: rg, Random: r, ChunkBox: chunkBox, Chunk: chunk, Pivot: pivot}
	for _, p := range s.Pieces {
		if p.BoundingBox().Intersects(chunkBox) {
			p.PostProcess(pc)
		}
	}
	if s.def.afterPlace != nil {
		s.def.afterPlace(pc, s)
	}
}

// decorateStructures places, for decoration step `step`, every
// structure of that step in registry order, each with its feature seed
// (index among the step's structures), clipped to the center chunk's
// writable area (ChunkGenerator.applyBiomeDecoration).
func (g *Generator) decorateStructures(rg *Region, r Random, deco int64, step int) {
	if !g.Structures {
		return
	}
	st := g.structureState()
	center := rg.center
	chunk := [2]int32{center.CX, center.CZ}
	minY := g.RS.lvMinY
	chunkBox := BoundingBox{center.CX * 16, minY + 1, center.CZ * 16, center.CX*16 + 15, minY + g.RS.lvHeight - 1, center.CZ*16 + 15}
	refs := g.StructureReferences(center.CX, center.CZ)
	idx := int32(0)
	for _, name := range st.structOrder {
		d := g.structure(name)
		if d.step != step {
			continue
		}
		i := idx
		idx++
		for _, ref := range refs {
			if ref.Structure != name {
				continue
			}
			setFeatureSeed(r, deco, i, int32(step))
			for _, c := range ref.Starts {
				for _, s := range g.StructureStarts(c[0], c[1]) {
					if s.Structure == name {
						s.placeInChunk(g, rg, r, chunkBox, chunk)
					}
				}
			}
		}
	}
}

// packChunk is ChunkPos.pack.
func packChunk(x, z int32) int64 { return int64(uint32(x)) | int64(uint32(z))<<32 }

// longOpenHashSet models fastutil's LongOpenHashSet insertion and
// iteration order (linear probing over HashCommon.mix, backwards
// iteration, rehash at 3/4 load).
type longOpenHashSet struct {
	key       []int64
	hasZero   bool
	size, max int
}

func newLongOpenHashSet() *longOpenHashSet {
	return &longOpenHashSet{key: make([]int64, 32), max: 24}
}

func fastutilMix(x int64) int64 {
	h := int64(uint64(x) * 0x9E3779B97F4A7C15)
	h ^= int64(uint64(h) >> 32)
	return h ^ int64(uint64(h)>>16)
}

func (s *longOpenHashSet) add(k int64) {
	if k == 0 {
		if !s.hasZero {
			s.hasZero = true
			s.size++
		}
		return
	}
	mask := len(s.key) - 1
	pos := int(fastutilMix(k)) & mask
	for s.key[pos] != 0 {
		if s.key[pos] == k {
			return
		}
		pos = (pos + 1) & mask
	}
	s.key[pos] = k
	s.size++
	if s.size >= s.max {
		s.rehash(len(s.key) * 2)
	}
}

func (s *longOpenHashSet) rehash(n int) {
	nk := make([]int64, n)
	mask := n - 1
	i := len(s.key)
	cnt := s.size
	if s.hasZero {
		cnt--
	}
	for ; cnt > 0; cnt-- {
		i--
		for s.key[i] == 0 {
			i--
		}
		pos := int(fastutilMix(s.key[i])) & mask
		for nk[pos] != 0 {
			pos = (pos + 1) & mask
		}
		nk[pos] = s.key[i]
	}
	s.key = nk
	s.max = min(n*3/4+boolInt(n*3%4 != 0), n-1)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *longOpenHashSet) list() []int64 {
	var out []int64
	if s.hasZero {
		out = append(out, 0)
	}
	for i := len(s.key) - 1; i >= 0; i-- {
		if s.key[i] != 0 {
			out = append(out, s.key[i])
		}
	}
	return out
}


