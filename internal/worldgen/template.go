package worldgen

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"

	"mc-go/internal/nbt"
)

// Structure templates: the block layouts the game ships for its
// template-built structures, generated into templates_gen.bin by
// tools/gentemplates and decoded on first use.

//go:embed templates_gen.bin
var templatesBin []byte

// structureTemplate is a template: its size, its block layouts (a
// template may carry several palettes of the same blocks) and its
// entities.
type structureTemplate struct {
	name     string
	size     Point
	palettes [][]templateBlock
	entities []templateEntity
}

// templateBlock is one block of a template palette.
type templateBlock struct {
	pos   Point
	state StateID
	// nbt is the block entity's tag, nil for none.
	nbt map[string]any
}

type templateEntity struct {
	pos      [3]float64
	blockPos Point
	nbt      map[string]any
}

var templateBodies = sync.OnceValue(func() map[string][]byte {
	zr, err := gzip.NewReader(bytes.NewReader(templatesBin))
	if err != nil {
		panic(fmt.Sprintf("worldgen: templates: %v", err))
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		panic(fmt.Sprintf("worldgen: templates: %v", err))
	}
	const magic = "mcgo templates 1\n"
	if !bytes.HasPrefix(raw, []byte(magic)) {
		panic("worldgen: templates: bad header")
	}
	r := &byteReader{b: raw[len(magic):]}
	out := map[string][]byte{}
	for n := r.uvarint(); n > 0; n-- {
		name := r.str()
		out[name] = r.bytes()
	}
	return out
})

var templates sync.Map // name -> *structureTemplate

// loadTemplate returns a shipped template, or nil if there is none of that
// name.
func loadTemplate(name string) *structureTemplate {
	if t, ok := templates.Load(name); ok {
		return t.(*structureTemplate)
	}
	body, ok := templateBodies()[name]
	if !ok {
		return nil
	}
	t := decodeTemplate(name, body)
	actual, _ := templates.LoadOrStore(name, t)
	return actual.(*structureTemplate)
}

func decodeTemplate(name string, body []byte) *structureTemplate {
	r := &byteReader{b: body}
	t := &structureTemplate{name: name}
	t.size = Point{int32(r.varint()), int32(r.varint()), int32(r.varint())}
	var palettes [][]StateID
	for n := r.uvarint(); n > 0; n-- {
		var states []StateID
		for m := r.uvarint(); m > 0; m-- {
			s := defaultState(r.str())
			for k := r.uvarint(); k > 0; k-- {
				prop, value := r.str(), r.str()
				s = s.with(prop, value)
			}
			states = append(states, s)
		}
		palettes = append(palettes, states)
	}
	type rawBlock struct {
		pos   Point
		state int
		nbt   map[string]any
	}
	var blocks []rawBlock
	for n := r.uvarint(); n > 0; n-- {
		b := rawBlock{pos: Point{int32(r.varint()), int32(r.varint()), int32(r.varint())}, state: int(r.uvarint())}
		b.nbt = r.tag()
		blocks = append(blocks, b)
	}
	for n := r.uvarint(); n > 0; n-- {
		var e templateEntity
		for i := range e.pos {
			e.pos[i] = math.Float64frombits(binary.LittleEndian.Uint64(r.next(8)))
		}
		e.blockPos = Point{int32(r.varint()), int32(r.varint()), int32(r.varint())}
		e.nbt = r.tag()
		t.entities = append(t.entities, e)
	}
	// Each palette lists its full collision blocks first, then the other
	// blocks, then those with block entities, each group by y, x and z.
	for _, states := range palettes {
		var full, other, withTag []templateBlock
		for _, b := range blocks {
			s := stAir
			if b.state < len(states) {
				s = states[b.state]
			}
			tb := templateBlock{pos: b.pos, state: s, nbt: b.nbt}
			switch {
			case b.nbt != nil:
				withTag = append(withTag, tb)
			case !s.hasDynamicShape() && s.isCollisionFullBlock():
				full = append(full, tb)
			default:
				other = append(other, tb)
			}
		}
		for _, group := range [][]templateBlock{full, other, withTag} {
			sort.SliceStable(group, func(i, j int) bool {
				a, b := group[i].pos, group[j].pos
				if a.Y != b.Y {
					return a.Y < b.Y
				}
				if a.X != b.X {
					return a.X < b.X
				}
				return a.Z < b.Z
			})
		}
		t.palettes = append(t.palettes, append(append(full, other...), withTag...))
	}
	return t
}

// byteReader decodes templates_gen.bin.
type byteReader struct {
	b []byte
}

func (r *byteReader) next(n int) []byte {
	if n > len(r.b) {
		panic("worldgen: templates: truncated")
	}
	p := r.b[:n]
	r.b = r.b[n:]
	return p
}

func (r *byteReader) uvarint() uint64 {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		panic("worldgen: templates: bad uvarint")
	}
	r.b = r.b[n:]
	return v
}

func (r *byteReader) varint() int64 {
	v, n := binary.Varint(r.b)
	if n <= 0 {
		panic("worldgen: templates: bad varint")
	}
	r.b = r.b[n:]
	return v
}

func (r *byteReader) bytes() []byte { return r.next(int(r.uvarint())) }
func (r *byteReader) str() string   { return string(r.bytes()) }

func (r *byteReader) tag() map[string]any {
	p := r.bytes()
	if len(p) == 0 {
		return nil
	}
	v, err := nbt.ReadUnnamed(bufio.NewReader(bytes.NewReader(p)))
	if err != nil {
		panic(fmt.Sprintf("worldgen: templates: %v", err))
	}
	return v.(map[string]any)
}

// Rotations, in the game's order.
type rotation int8

const (
	rotNone rotation = iota
	rotCW90
	rotCW180
	rotCCW90
)

var rotationNames = [4]string{"NONE", "CLOCKWISE_90", "CLOCKWISE_180", "COUNTERCLOCKWISE_90"}

// randomRotation draws one of the four rotations.
func randomRotation(r Random) rotation { return rotation(r.NextIntn(4)) }

func (a rotation) then(b rotation) rotation { return (a + b) & 3 }

// Mirrors, in the game's order.
type mirror int8

const (
	mirrorNone mirror = iota
	mirrorLeftRight
	mirrorFrontBack
)

var mirrorNames = [3]string{"NONE", "LEFT_RIGHT", "FRONT_BACK"}

// placeSettings are how a template is placed: turned about a pivot and
// mirrored.
type placeSettings struct {
	rot    rotation
	mirror mirror
	pivot  Point
}

// transformPos maps a template-local position through a mirror and then
// a rotation about pivot.
func transformPos(p Point, m mirror, rot rotation, pivot Point) Point {
	x, y, z := p.X, p.Y, p.Z
	mirrored := true
	switch m {
	case mirrorLeftRight:
		z = -z
	case mirrorFrontBack:
		x = -x
	default:
		mirrored = false
	}
	px, pz := pivot.X, pivot.Z
	switch rot {
	case rotCCW90:
		return Point{px - pz + z, y, px + pz - x}
	case rotCW90:
		return Point{px + pz - z, y, pz - px + x}
	case rotCW180:
		return Point{px + px - x, y, pz + pz - z}
	}
	if mirrored {
		return Point{x, y, z}
	}
	return p
}

// sizeFor is the template's size turned by a rotation.
func (t *structureTemplate) sizeFor(rot rotation) Point {
	if rot == rotCW90 || rot == rotCCW90 {
		return Point{t.size.Z, t.size.Y, t.size.X}
	}
	return t.size
}

// boundingBox is the box the template covers placed at pos.
func (t *structureTemplate) boundingBox(s placeSettings, pos Point) boundingBox {
	return templateBox(pos, s.rot, s.pivot, s.mirror, t.size)
}

func templateBox(pos Point, rot rotation, pivot Point, m mirror, size Point) boundingBox {
	a := transformPos(Point{}, m, rot, pivot)
	b := transformPos(Point{size.X - 1, size.Y - 1, size.Z - 1}, m, rot, pivot)
	return boxFromCorners(a, b).moved(pos.X, pos.Y, pos.Z)
}

// zeroPositionWithTransform is where a template's origin corner lands
// when it is placed so that its box starts at pos.
func zeroPositionWithTransform(pos Point, m mirror, rot rotation, sizeX, sizeZ int32) Point {
	sizeX--
	sizeZ--
	mx, mz := int32(0), int32(0)
	if m == mirrorFrontBack {
		mx = sizeX
	}
	if m == mirrorLeftRight {
		mz = sizeZ
	}
	switch rot {
	case rotCCW90:
		return pos.add(Point{mz, 0, sizeX - mx})
	case rotCW90:
		return pos.add(Point{sizeZ - mz, 0, mx})
	case rotCW180:
		return pos.add(Point{sizeX - mx, 0, sizeZ - mz})
	}
	return pos.add(Point{mx, 0, mz})
}
