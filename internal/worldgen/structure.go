package worldgen

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/nbt"
)

// Structure starts: what each chunk's structure-starts stage begins.
// For every structure set placing a structure in the chunk, one of the
// set's structures is picked by weight and asked for a generation point;
// a point in one of its biomes starts it, and its pieces are laid out.
// A start is kept as the tag the game saves it under.

// boundingBox is an inclusive box of blocks.
type boundingBox struct{ minX, minY, minZ, maxX, maxY, maxZ int32 }

func boxFromCorners(a, b Point) boundingBox {
	return boundingBox{min(a.X, b.X), min(a.Y, b.Y), min(a.Z, b.Z), max(a.X, b.X), max(a.Y, b.Y), max(a.Z, b.Z)}
}

func boxAt(p Point) boundingBox { return boundingBox{p.X, p.Y, p.Z, p.X, p.Y, p.Z} }

func (b boundingBox) moved(dx, dy, dz int32) boundingBox {
	return boundingBox{b.minX + dx, b.minY + dy, b.minZ + dz, b.maxX + dx, b.maxY + dy, b.maxZ + dz}
}

func (b boundingBox) intersects(o boundingBox) bool {
	return b.maxX >= o.minX && b.minX <= o.maxX && b.maxZ >= o.minZ && b.minZ <= o.maxZ && b.maxY >= o.minY && b.minY <= o.maxY
}

func (b boundingBox) contains(p Point) bool {
	return p.X >= b.minX && p.X <= b.maxX && p.Z >= b.minZ && p.Z <= b.maxZ && p.Y >= b.minY && p.Y <= b.maxY
}

func (b *boundingBox) encapsulate(o boundingBox) {
	b.minX, b.minY, b.minZ = min(b.minX, o.minX), min(b.minY, o.minY), min(b.minZ, o.minZ)
	b.maxX, b.maxY, b.maxZ = max(b.maxX, o.maxX), max(b.maxY, o.maxY), max(b.maxZ, o.maxZ)
}

func (b boundingBox) spanX() int32 { return b.maxX - b.minX + 1 }
func (b boundingBox) spanY() int32 { return b.maxY - b.minY + 1 }
func (b boundingBox) spanZ() int32 { return b.maxZ - b.minZ + 1 }

func (b boundingBox) center() Point {
	return Point{b.minX + (b.maxX-b.minX+1)/2, b.minY + (b.maxY-b.minY+1)/2, b.minZ + (b.maxZ-b.minZ+1)/2}
}

func (b boundingBox) tag() []int32 { return []int32{b.minX, b.minY, b.minZ, b.maxX, b.maxY, b.maxZ} }

// data2D is a horizontal direction's save value.
func data2D(dir int) int32 {
	switch dir {
	case dirSouth:
		return 0
	case dirWest:
		return 1
	case dirNorth:
		return 2
	case dirEast:
		return 3
	}
	return -1
}

// structurePiece is one piece of a structure start.
type structurePiece interface {
	piece() *pieceBase
	// saveTag adds the piece's own fields to its save tag.
	saveTag(tag map[string]any)
}

// pieceBase is what every piece has: its type, box, facing and depth in
// the structure's piece tree.
type pieceBase struct {
	kind     string
	box      boundingBox
	facing   int // a horizontal direction, or -1
	genDepth int32
	// rot and mirror follow from the facing: how the piece's own
	// coordinates turn into the world's.
	rot    rotation
	mirror mirror
}

func newPieceBase(kind string, depth int32, box boundingBox) pieceBase {
	return pieceBase{kind: kind, box: box, facing: -1, genDepth: depth}
}

func (p *pieceBase) piece() *pieceBase { return p }

func (p *pieceBase) setOrientation(dir int) {
	p.facing = dir
	p.rot, p.mirror = rotNone, mirrorNone
	switch dir {
	case dirSouth:
		p.mirror = mirrorLeftRight
	case dirWest:
		p.mirror, p.rot = mirrorLeftRight, rotCW90
	case dirEast:
		p.rot = rotCW90
	}
}

// pieceTag is a piece's save tag.
func pieceTag(p structurePiece) map[string]any {
	b := p.piece()
	o := int32(-1)
	if b.facing >= 0 {
		o = data2D(b.facing)
	}
	tag := map[string]any{"id": b.kind, "BB": b.box.tag(), "O": o, "GD": b.genDepth}
	p.saveTag(tag)
	return tag
}

// makeBoundingBox is the box of a piece width × height × depth from
// (x, y, z), its depth along the facing.
func makeBoundingBox(x, y, z int32, dir int, width, height, depth int32) boundingBox {
	if dir == dirNorth || dir == dirSouth {
		return boundingBox{x, y, z, x + width - 1, y + height - 1, z + depth - 1}
	}
	return boundingBox{x, y, z, x + depth - 1, y + height - 1, z + width - 1}
}

// templatePiece is a piece placing one template.
type templatePiece struct {
	pieceBase
	templateName string
	template     *structureTemplate
	settings     placeSettings
	pos          Point
}

func newTemplatePiece(kind string, depth int32, name string, s placeSettings, pos Point) templatePiece {
	t := loadTemplate(name)
	if t == nil {
		panic("worldgen: no structure template " + name)
	}
	p := templatePiece{pieceBase: newPieceBase(kind, depth, t.boundingBox(s, pos)), templateName: name, template: t, settings: s, pos: pos}
	p.setOrientation(dirNorth)
	return p
}

func (p *templatePiece) saveTemplate(tag map[string]any) {
	tag["TPX"], tag["TPY"], tag["TPZ"] = p.pos.X, p.pos.Y, p.pos.Z
	tag["Template"] = p.templateName
}

// structureStart is a structure begun in a chunk.
type structureStart struct {
	structure  string
	cx, cz     int32
	references int32
	pieces     []structurePiece
}

// tag is the start as a chunk saves it.
func (s *structureStart) tag() map[string]any {
	children := nbt.List{Elem: nbt.TagCompound}
	for _, p := range s.pieces {
		children.Items = append(children.Items, pieceTag(p))
	}
	return map[string]any{"id": s.structure, "ChunkX": s.cx, "ChunkZ": s.cz, "references": s.references, "Children": children}
}

// structureDef is a registered structure.
type structureDef struct {
	name   string
	kind   string
	biomes map[string]bool
	cfg    map[string]any
}

var structureDefs = sync.OnceValue(func() map[string]*structureDef {
	out := map[string]*structureDef{}
	for name, raw := range structureData {
		m := raw.(map[string]any)
		d := &structureDef{name: name, kind: m["type"].(string), biomes: map[string]bool{}, cfg: m}
		for _, b := range biomeHolderSet(m["biomes"]) {
			d.biomes[b] = true
		}
		out[name] = d
	}
	return out
})

// structureContext is what a structure sees while it finds its
// generation point and lays out its pieces.
type structureContext struct {
	g      *Generator
	def    *structureDef
	seed   int64
	cx, cz int32
	// random is seeded from the chunk, shared by the point search and
	// the pieces.
	random *Legacy
}

func (c *structureContext) minX() int32 { return c.cx * 16 }
func (c *structureContext) minZ() int32 { return c.cz * 16 }

// firstOccupiedHeight is the highest block a heightmap counts at
// (x, z) in the noise terrain.
func (c *structureContext) firstOccupiedHeight(x, z int32, heightmap int) int32 {
	return c.g.firstOccupiedHeight(x, z, heightmap)
}

// cornerHeights are the world-surface heights at the four corners of
// the box from (x, z) spanning w and d.
func (c *structureContext) cornerHeights(x, w, z, d int32) [4]int32 {
	h := func(x, z int32) int32 { return c.firstOccupiedHeight(x, z, worldSurfaceWG) }
	return [4]int32{h(x, z), h(x, z+d), h(x+w, z), h(x+w, z+d)}
}

// lowestY is the lowest corner height of the box from (x, z) spanning w
// and d.
func (c *structureContext) lowestY(x, z, w, d int32) int32 {
	h := c.cornerHeights(x, w, z, d)
	return min(min(h[0], h[1]), min(h[2], h[3]))
}

// meanFirstOccupiedHeight is the mean corner height of the box from
// (x, z) spanning w and d.
func (c *structureContext) meanFirstOccupiedHeight(x, w, z, d int32) int32 {
	h := c.cornerHeights(x, w, z, d)
	return (h[0] + h[1] + h[2] + h[3]) / 4
}

// biomeAt is the biome source's biome at a block.
func (c *structureContext) biomeAt(p Point) string {
	g := c.g
	w := <-g.workers
	defer func() { g.workers <- w }()
	return biomeNameTable[g.source.biomeIndex(g.rs, w.searcher, p.X>>2, p.Y>>2, p.Z>>2)]
}

// genStub is a found generation point and the pieces it would lay out.
type genStub struct {
	pos   Point
	build func() []structurePiece
}

// onTopOfChunkCenter puts the structure on the noise terrain at the
// chunk's middle.
func (c *structureContext) onTopOfChunkCenter(heightmap int, build func() []structurePiece) (genStub, bool) {
	x, z := c.minX()+8, c.minZ()+8
	return genStub{Point{x, c.firstOccupiedHeight(x, z, heightmap), z}, build}, true
}

// generateStructure tries to start structure name in chunk (cx, cz).
func (g *Generator) generateStructure(name string, cx, cz int32) *structureStart {
	d, ok := structureDefs()[name]
	if !ok {
		panic("worldgen: unknown structure " + name)
	}
	r := NewLegacy(0)
	r.setLargeFeatureSeed(g.rs.Seed, cx, cz)
	ctx := &structureContext{g: g, def: d, seed: g.rs.Seed, cx: cx, cz: cz, random: r}
	gen, ok := structureGenerators[d.kind]
	if !ok {
		return nil
	}
	stub, ok := gen(ctx)
	if !ok || !d.biomes[ctx.biomeAt(stub.pos)] {
		return nil
	}
	pieces := stub.build()
	if len(pieces) == 0 {
		return nil
	}
	return &structureStart{structure: name, cx: cx, cz: cz, pieces: pieces}
}

// structureGenerators find each structure type's generation point.
var structureGenerators = map[string]func(*structureContext) (genStub, bool){}

// createStructures is chunk (cx, cz)'s structure-starts stage.
func (g *Generator) createStructures(cx, cz int32) []*structureStart {
	p := g.structures()
	var out []*structureStart
	for _, s := range p.sets {
		if !p.isStructureChunk(s, cx, cz) {
			continue
		}
		if len(s.entries) == 1 {
			if st := g.generateStructure(s.entries[0].structure, cx, cz); st != nil {
				out = append(out, st)
			}
			continue
		}
		entries := append([]structureEntry(nil), s.entries...)
		total := int32(0)
		for _, e := range entries {
			total += e.weight
		}
		r := NewLegacy(0)
		r.setLargeFeatureSeed(p.seed, cx, cz)
		for len(entries) > 0 {
			pick := r.NextIntn(total)
			i := 0
			for ; i < len(entries)-1; i++ {
				if pick -= entries[i].weight; pick < 0 {
					break
				}
			}
			e := entries[i]
			if st := g.generateStructure(e.structure, cx, cz); st != nil {
				out = append(out, st)
				break
			}
			entries = append(entries[:i], entries[i+1:]...)
			total -= e.weight
		}
	}
	return out
}

// snbt renders a tag value as the game's SNBT, compound keys sorted.
func snbt(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := "{"
		for i, k := range keys {
			if i > 0 {
				out += ","
			}
			out += k + ":" + snbt(t[k])
		}
		return out + "}"
	case nbt.List:
		out := "["
		for i, e := range t.Items {
			if i > 0 {
				out += ","
			}
			out += snbt(e)
		}
		return out + "]"
	case int8:
		return fmt.Sprintf("%db", t)
	case int16:
		return fmt.Sprintf("%ds", t)
	case int32:
		return fmt.Sprint(t)
	case int64:
		return fmt.Sprintf("%dL", t)
	case float32:
		return javaFloatString(float64(t), 32) + "f"
	case float64:
		return javaFloatString(t, 64) + "d"
	case string:
		return quoteSNBT(t)
	case []int32:
		out := "[I;"
		for i, e := range t {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprint(e)
		}
		return out + "]"
	case []int64:
		out := "[L;"
		for i, e := range t {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf("%dL", e)
		}
		return out + "]"
	case []byte:
		out := "[B;"
		for i, e := range t {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf("%db", int8(e))
		}
		return out + "]"
	}
	panic(fmt.Sprintf("worldgen: no SNBT for %T", v))
}

// quoteSNBT quotes a string as SNBT does: in double quotes unless it
// holds one and no single quote, escaping the quote and backslashes.
func quoteSNBT(s string) string {
	var quote rune
	out := []rune{' '}
	for _, c := range s {
		switch c {
		case '\\':
			out = append(out, '\\')
		case '"', '\'':
			if quote == 0 {
				if c == '"' {
					quote = '\''
				} else {
					quote = '"'
				}
			}
			if quote == c {
				out = append(out, '\\')
			}
		}
		out = append(out, c)
	}
	if quote == 0 {
		quote = '"'
	}
	out[0] = quote
	return string(append(out, quote))
}

// javaFloatString prints a float in Java's notation from its shortest
// round-tripping digits: plain from 10⁻³ up to 10⁷, otherwise as d.dddEn.
// (The server's SNBT printer also covers the denormals, where Java
// prints two digits; no structure tag carries one.)
func javaFloatString(f float64, bits int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	a := math.Abs(f)
	if a >= 1e-3 && a < 1e7 {
		s := strconv.FormatFloat(f, 'f', -1, bits)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	s := strconv.FormatFloat(f, 'e', -1, bits)
	mant, exp, _ := strings.Cut(s, "e")
	if !strings.Contains(mant, ".") {
		mant += ".0"
	}
	e, _ := strconv.Atoi(exp)
	return mant + "E" + strconv.Itoa(e)
}
