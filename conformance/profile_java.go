package conformance

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// fields decodes bounded protocol-775/776 packet bodies. Errors are sticky so every
// required field is consumed before complete verifies the exact body boundary.
type fields struct {
	r   *bytes.Reader
	err error
}

func bodyFields(b []byte) *fields { return &fields{r: bytes.NewReader(b)} }
func (f *fields) fail(s string) {
	if f.err == nil {
		f.err = fmt.Errorf("%s", s)
	}
}
func (f *fields) fixed(n int) []byte {
	if f.err != nil {
		return nil
	}
	if n < 0 || n > f.r.Len() {
		f.fail("truncated fixed-size field")
		return nil
	}
	b := make([]byte, n)
	_, f.err = io.ReadFull(f.r, b)
	return b
}
func (f *fields) byte() byte {
	b := f.fixed(1)
	if len(b) == 0 {
		return 0
	}
	return b[0]
}
func (f *fields) boolean() bool {
	b := f.byte()
	if b > 1 {
		f.fail("boolean must be 0 or 1")
	}
	return b == 1
}
func (f *fields) integer() int32 {
	if f.err != nil {
		return 0
	}
	n, e := readInt(f.r)
	f.err = e
	return n
}
func (f *fields) nonnegative() int32 {
	n := f.integer()
	if n < 0 {
		f.fail("negative VarInt field")
	}
	return n
}
func (f *fields) count() int32 {
	n := f.nonnegative()
	if int64(n) > int64(f.r.Len()) {
		f.fail("count exceeds remaining packet bytes")
	}
	return n
}
func (f *fields) string() string {
	if f.err != nil {
		return ""
	}
	s, e := takeString(f.r)
	f.err = e
	return s
}
func (f *fields) identifier() string {
	s := f.string()
	if f.err == nil && !identifier(s) {
		f.fail("invalid resource identifier " + s)
	}
	return s
}
func (f *fields) complete() error {
	if f.err != nil {
		return f.err
	}
	if f.r.Len() != 0 {
		return fmt.Errorf("%d trailing packet bytes", f.r.Len())
	}
	return nil
}
func identifier(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	for part, p := range parts {
		for _, c := range p {
			if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.' || part == 1 && c == '/' {
				continue
			}
			return false
		}
	}
	return true
}

// skipNBT validates the full unnamed network-NBT value without importing or
// allocating implementation registry structures. The format's independent
// scalar/list/compound rules are pinned to the public NBT specification.
func skipNBT(f *fields, tag byte, depth int, nodes *int) {
	if f.err != nil {
		return
	}
	*nodes++
	if depth > 512 || *nodes > maxFrame {
		f.fail("NBT nesting/node limit exceeded")
		return
	}
	switch tag {
	case 1:
		f.fixed(1)
	case 2:
		f.fixed(2)
	case 3, 5:
		f.fixed(4)
	case 4, 6:
		f.fixed(8)
	case 7, 11, 12:
		b := f.fixed(4)
		if f.err != nil {
			return
		}
		n := int64(int32(binary.BigEndian.Uint32(b)))
		width := int64(1)
		if tag == 11 {
			width = 4
		}
		if tag == 12 {
			width = 8
		}
		if n < 0 || n*width > int64(f.r.Len()) {
			f.fail("invalid NBT array length")
			return
		}
		f.fixed(int(n * width))
	case 8:
		skipNBTString(f)
	case 9:
		elem := f.byte()
		b := f.fixed(4)
		if f.err != nil {
			return
		}
		n := int32(binary.BigEndian.Uint32(b))
		if n < 0 || int64(n) > int64(f.r.Len()) || elem > 12 || elem == 0 && n != 0 {
			f.fail("invalid NBT list")
			return
		}
		for i := int32(0); i < n && f.err == nil; i++ {
			skipNBT(f, elem, depth+1, nodes)
		}
	case 10:
		for f.err == nil {
			kind := f.byte()
			if f.err != nil || kind == 0 {
				return
			}
			skipNBTString(f)
			skipNBT(f, kind, depth+1, nodes)
		}
	default:
		f.fail(fmt.Sprintf("invalid NBT tag %d", tag))
	}
}
func skipNBTString(f *fields) {
	b := f.fixed(2)
	if f.err == nil {
		f.fixed(int(binary.BigEndian.Uint16(b)))
	}
}
func registryJava(data []byte) (string, int32, error) {
	f := bodyFields(data)
	name := f.identifier()
	count := f.count()
	seen := map[string]bool{}
	nodes := 0
	for i := int32(0); i < count && f.err == nil; i++ {
		entry := f.identifier()
		if seen[entry] {
			f.fail("duplicate registry entry " + entry)
		}
		seen[entry] = true
		// We negotiated zero known packs: every entry must carry its full data.
		if !f.boolean() {
			f.fail("registry data omitted despite empty known-pack selection")
			break
		}
		tag := f.byte()
		if tag == 0 {
			f.fail("registry NBT must contain a value")
		}
		skipNBT(f, tag, 0, &nodes)
	}
	return name, count, f.complete()
}
func featuresJava(data []byte) error {
	f := bodyFields(data)
	n := f.count()
	if n == 0 {
		f.fail("empty feature flag set")
	}
	seen := map[string]bool{}
	for i := int32(0); i < n && f.err == nil; i++ {
		name := f.identifier()
		if seen[name] {
			f.fail("duplicate feature flag")
		}
		seen[name] = true
	}
	return f.complete()
}
func tagsJava(data []byte) error {
	f := bodyFields(data)
	n := f.count()
	seen := map[string]bool{}
	for i := int32(0); i < n && f.err == nil; i++ {
		registry := f.identifier()
		if seen[registry] {
			f.fail("duplicate tag registry")
		}
		seen[registry] = true
		nt := f.count()
		tags := map[string]bool{}
		for j := int32(0); j < nt && f.err == nil; j++ {
			tag := f.identifier()
			if tags[tag] {
				f.fail("duplicate tag name")
			}
			tags[tag] = true
			nv := f.count()
			for k := int32(0); k < nv && f.err == nil; k++ {
				f.nonnegative()
			}
		}
	}
	return f.complete()
}
func joinJava(data []byte, registries map[string]int32, protocol int32) error {
	profile, ok := profileFor(protocol)
	if !ok {
		return fmt.Errorf("unsupported join protocol %d", protocol)
	}
	f := bodyFields(data)
	f.fixed(4)
	f.boolean()
	n := f.count()
	if n == 0 {
		f.fail("no advertised dimensions")
	}
	dims := map[string]bool{}
	for i := int32(0); i < n && f.err == nil; i++ {
		name := f.identifier()
		if dims[name] {
			f.fail("duplicate dimension name")
		}
		dims[name] = true
	}
	f.nonnegative() // max players
	f.nonnegative() // view distance
	f.nonnegative() // simulation distance
	f.boolean()
	f.boolean()
	f.boolean() // reduced debug, respawn screen, limited crafting
	dimType := f.nonnegative()
	if limit, ok := registries["minecraft:dimension_type"]; !ok || dimType >= limit {
		f.fail("dimension type references an absent registry entry")
	}
	dimension := f.identifier()
	if !dims[dimension] {
		f.fail("current dimension was not advertised")
	}
	f.fixed(8) // hashed seed
	mode, previous := f.byte(), f.byte()
	if mode > 3 || previous > 3 && previous != 255 {
		f.fail("invalid game mode")
	}
	f.boolean()
	f.boolean() // debug and flat worlds
	if f.boolean() {
		f.identifier()
		f.fixed(8)
	} // optional death global position
	f.nonnegative() // portal cooldown
	f.integer()     // sea level may be negative
	if profile.joinOnlineMode {
		f.boolean()
	} // added in protocol 776
	f.boolean() // enforced secure chat
	return f.complete()
}
