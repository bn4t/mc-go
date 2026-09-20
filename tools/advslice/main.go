// advslice rebuilds internal/data/advwire.dat from a wire capture of
// `advancement grant @s everything` (the length-prefixed
// update_advancements payload). It walks the packet sequentially —
// defs, removed ids, progress — slicing each def entry's raw bytes
// into the generated table. Usage: go run ./tools/advslice <in> <out>
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"sort"

	"mc-go/internal/data"
	"mc-go/internal/nbt"
)

type rd struct {
	b *bytes.Reader
}

func (r *rd) varint() int32 {
	var v int32
	for i := 0; i < 5; i++ {
		b, err := r.b.ReadByte()
		if err != nil {
			panic(err)
		}
		v |= int32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return v
		}
	}
	panic("varint too long")
}

func (r *rd) str() string {
	n := int(r.varint())
	buf := make([]byte, n)
	if _, err := r.b.Read(buf); err != nil {
		panic(err)
	}
	return string(buf)
}

func (r *rd) byte() byte {
	b, err := r.b.ReadByte()
	if err != nil {
		panic(err)
	}
	return b
}

func putVar(b *bytes.Buffer, v int32) {
	for {
		if v&^0x7f == 0 {
			b.WriteByte(byte(v))
			return
		}
		b.WriteByte(byte(v&0x7f | 0x80))
		v = int32(uint32(v) >> 7)
	}
}

func putStr(b *bytes.Buffer, s string) {
	putVar(b, int32(len(s)))
	b.WriteString(s)
}

func (r *rd) int32() int32 {
	var v int32
	if err := binary.Read(r.b, binary.BigEndian, &v); err != nil {
		panic(err)
	}
	return v
}

func (r *rd) float32() float32 {
	var v float32
	if err := binary.Read(r.b, binary.BigEndian, &v); err != nil {
		panic(err)
	}
	return v
}

// skipComponentValue skips one added patch entry's value, dispatched
// on the data-component-type registry id. Only the component kinds the
// bundled datapack actually puts on advancement icons are understood;
// anything else panics so a new kind is a loud gap, not a silent one.
func (r *rd) skipComponentValue(typeID int32) {
	switch typeID {
	case 9: // minecraft:item_name — a text component (inline NBT tag)
		if _, err := nbt.ReadUnnamed(r.b); err != nil {
			panic(err)
		}
	case 12: // minecraft:rarity — enum varint
		r.varint()
	case 18: // minecraft:tooltip_display — bool + HolderSet varints
		r.byte()
		for n := r.varint(); n > 0; n-- {
			r.varint()
		}
	case 72: // minecraft:banner_patterns — (holder,color) pairs
		for n := r.varint(); n > 0; n-- {
			r.varint() // banner pattern holder
			r.varint() // dye color
		}
	case 74: // minecraft:pot_decorations — item holder list
		for n := r.varint(); n > 0; n-- {
			r.varint()
		}
	default:
		panic(fmt.Sprintf("unknown patched component type id %d", typeID))
	}
}

// parsePayload walks one update_advancements payload, folding each
// def entry's raw wire bytes into entries keyed by id.
func parsePayload(payload []byte, entries map[string][]byte) {
	r := &rd{b: bytes.NewReader(payload)}
	r.byte() // reset flag
	ndefs := int(r.varint())
	for i := 0; i < ndefs; i++ {
		start := len(payload) - r.b.Len()
		id := r.str()
		defer func() {
			if e := recover(); e != nil {
				panic(fmt.Sprintf("def %d (%q): %v", i, id, e))
			}
		}()
		if r.byte() == 1 {
			r.str() // parent
		}
		if r.byte() == 1 { // display
			if _, err := nbt.ReadUnnamed(r.b); err != nil { // title
				panic(fmt.Sprintf("%s title: %v", id, err))
			}
			if _, err := nbt.ReadUnnamed(r.b); err != nil { // description
				panic(fmt.Sprintf("%s desc: %v", id, err))
			}
			r.varint() // icon item holder
			r.varint() // icon count
			// DataComponentPatch: both counts lead, then the added
			// (typeId, value) pairs, then the removed typeIds.
			nadd, nrem := r.varint(), r.varint()
			for ; nadd > 0; nadd-- {
				r.skipComponentValue(r.varint())
			}
			for ; nrem > 0; nrem-- {
				r.varint()
			}
			r.varint() // frame enum
			flags := r.int32()
			if flags&1 != 0 {
				r.str() // background texture
			}
			r.float32() // x
			r.float32() // y
		}
		for n := r.varint(); n > 0; n-- { // requirement groups
			for m := r.varint(); m > 0; m-- {
				r.str()
			}
		}
		r.byte() // telemetry flag
		end := len(payload) - r.b.Len()
		entries[id] = payload[start:end]
	}
	if nrem := r.varint(); nrem != 0 {
		panic(fmt.Sprintf("unexpected %d removed ids", nrem))
	}
	nprog := int(r.varint())
	for i := 0; i < nprog; i++ {
		r.str()
		for n := r.varint(); n > 0; n-- {
			r.str()
			if r.byte() == 1 {
				var ts int64
				binary.Read(r.b, binary.BigEndian, &ts)
			}
		}
	}
	r.byte() // showAdvancements
	if r.b.Len() != 0 {
		panic(fmt.Sprintf("%d trailing bytes", r.b.Len()))
	}
}

func main() {
	// Input files: length-prefixed packet captures (each file may hold
	// several concatenated packets). Last arg is the output table.
	entries := map[string][]byte{}
	out := os.Args[len(os.Args)-1]
	for _, name := range os.Args[1 : len(os.Args)-1] {
		raw, err := os.ReadFile(name)
		if err != nil {
			panic(err)
		}
		for len(raw) > 0 {
			n := int(binary.BigEndian.Uint32(raw[:4]))
			if n > len(raw)-4 {
				panic(fmt.Sprintf("%s: length prefix %d > %d", name, n, len(raw)-4))
			}
			parsePayload(raw[4:4+n], entries)
			raw = raw[4+n:]
		}
	}
	// Fill gaps for display-less advancements (e.g. recipes/root) that
	// vanilla only sends on criteria progress our captures never hit.
	// Without a display the def is fully derivable: id, parent, reqs,
	// telemetry=false — matching the shape of captured recipe defs.
	for path, raw := range data.Advancements() {
		id := "minecraft:" + path
		if _, ok := entries[id]; ok {
			continue
		}
		if _, has := raw["display"]; has {
			continue
		}
		var e bytes.Buffer
		putStr(&e, id)
		parent, _ := raw["parent"].(string)
		if parent == "" {
			e.WriteByte(0)
		} else {
			e.WriteByte(1)
			putStr(&e, parent)
		}
		e.WriteByte(0) // hasDisplay
		var groups [][]string
		for _, g := range raw["requirements"].([]any) {
			var crits []string
			for _, c := range g.([]any) {
				crits = append(crits, c.(string))
			}
			groups = append(groups, crits)
		}
		putVar(&e, int32(len(groups)))
		for _, g := range groups {
			putVar(&e, int32(len(g)))
			for _, c := range g {
				putStr(&e, c)
			}
		}
		e.WriteByte(0) // telemetry
		entries[id] = e.Bytes()
	}
	order := make([]string, 0, len(entries))
	for id := range entries {
		order = append(order, id)
	}
	sort.Strings(order)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(len(order)))
	for _, id := range order {
		e := entries[id]
		binary.Write(&buf, binary.BigEndian, uint16(len(id)))
		buf.WriteString(id)
		binary.Write(&buf, binary.BigEndian, uint32(len(e)))
		buf.Write(e)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("%d defs sliced\n", len(order))
}
