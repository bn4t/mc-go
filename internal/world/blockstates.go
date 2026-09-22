package world

import (
	"math/bits"
	"slices"
	"sort"
	"sync"
)

// Full block-state access: name + properties <-> global state id.
//
// The generated tables (blockstates_gen.go) carry the raw data; this
// file turns it into the lookups the server needs to place, describe,
// and modify blocks by their vanilla state — the substrate for fluids,
// redstone, crops, and every other stateful block.
//
// A state id is dense over [0, maxBlockState) and every block's states
// occupy one contiguous range, so the owning block is a binary search
// rather than a 32k-entry reverse table.

// stateKey identifies a state by owning block index plus packed
// property values.
type stateKey uint64

func stateKeyFor(blockIdx int, packed uint32) stateKey {
	return stateKey(uint64(blockIdx)<<32 | uint64(packed))
}

// ownerIndex resolves a global state id to its block index, or -1.
func ownerIndex(id int) int {
	i := sort.Search(len(blockBaseOrder), func(i int) bool {
		bi := blockBaseOrder[i]
		return int(blockBase[bi])+int(blockCounts[bi]) > id
	})
	if i >= len(blockBaseOrder) {
		return -1
	}
	bi := int(blockBaseOrder[i])
	if id < int(blockBase[bi]) {
		return -1
	}
	return bi
}

// propWidth is the bit width one property occupies in a packed value.
func propWidth(vals []string) int { return bits.Len(uint(len(vals) - 1)) }

// unpackProps decodes a packed value into value indices, in schema
// order. The returned slice is indexed like blockSchemas[bi].Names.
func unpackProps(bi int, packed uint32) []int {
	sc := blockSchemas[bi]
	out := make([]int, len(sc.Names))
	off := 0
	for j, vals := range sc.Values {
		w := propWidth(vals)
		if w > 0 {
			out[j] = int(packed>>off) & (1<<w - 1)
		}
		off += w
	}
	return out
}

// packProps encodes value indices back into a packed value.
func packProps(bi int, idx []int) uint32 {
	sc := blockSchemas[bi]
	var v uint32
	off := 0
	for j, vals := range sc.Values {
		w := propWidth(vals)
		if w > 0 {
			v |= uint32(idx[j]) << off
		}
		off += w
	}
	return v
}

// valueIndex finds a property value's position, or -1.
func valueIndex(vals []string, want string) int {
	for i, v := range vals {
		if v == want {
			return i
		}
	}
	return -1
}

// The name+properties -> state id map is built once on first use rather
// than in init(): file init order within a package is unspecified by
// the language, and the generated file's own init has to decode the
// packed blob before any of it can be read.
var (
	stateLookupOnce sync.Once
	stateLookup     map[stateKey]Block
)

func stateLookupTable() map[stateKey]Block {
	stateLookupOnce.Do(func() {
		stateLookup = make(map[stateKey]Block, len(statePacked))
		for id := range statePacked {
			bi := ownerIndex(id)
			if bi < 0 {
				continue
			}
			stateLookup[stateKeyFor(bi, statePacked[id])] = Block(id)
		}
	})
	return stateLookup
}

// BlockStateOf resolves a block name (without the minecraft: prefix)
// plus property values to a global state id. Properties absent from
// props take their value from the block's default state, so
// BlockStateOf(name, nil) is the block's default state. Unknown block
// names, unknown properties, and out-of-range values return false.
func BlockStateOf(name string, props map[string]string) (Block, bool) {
	bi, ok := blockIndexByName[name]
	if !ok {
		return Air, false
	}
	b := int(bi)
	sc := blockSchemas[b]
	idx := unpackProps(b, blockDefaultPacked[b])
	for prop, want := range props {
		j := -1
		for k, n := range sc.Names {
			if n == prop {
				j = k
				break
			}
		}
		if j < 0 {
			return Air, false
		}
		vi := valueIndex(sc.Values[j], want)
		if vi < 0 {
			return Air, false
		}
		idx[j] = vi
	}
	s, ok := stateLookupTable()[stateKeyFor(b, packProps(b, idx))]
	return s, ok
}

// BlockProperties returns every property of a state by name, which is
// what a chunk palette entry carries: the real server writes all of a
// block's properties, defaults included - a grass block is saved as
// {snowy: "false"} - and omits the map only for the blocks that have no
// properties at all. A map that dropped the defaulted properties would
// still read back as the same state, but it is not the same file.
func BlockProperties(b Block) map[string]string {
	bi := ownerIndex(int(b))
	if bi < 0 || int(b) >= len(statePacked) {
		return nil
	}
	sc := blockSchemas[bi]
	if len(sc.Names) == 0 {
		return nil
	}
	idx := unpackProps(bi, statePacked[b])
	out := make(map[string]string, len(sc.Names))
	for j, name := range sc.Names {
		out[name] = sc.Values[j][idx[j]]
	}
	return out
}

// BlockPropNames returns the property names a block name declares,
// nil when the name is not a block.
func BlockPropNames(name string) []string {
	bi, ok := blockIndexByName[name]
	if !ok {
		return nil
	}
	return blockSchemas[bi].Names
}

// StateOwner returns the block name owning a state id.
func StateOwner(b Block) (string, bool) {
	bi := ownerIndex(int(b))
	if bi < 0 {
		return "", false
	}
	return blockNamesByIndex()[bi], true
}

// blockNamesInIndexOrder is the inverse of blockIndexByName; it is
// derived once because the generated table is keyed the other way.
var (
	blockNamesOnce         sync.Once
	blockNamesInIndexOrder []string
)

func blockNamesByIndex() []string {
	blockNamesOnce.Do(func() {
		blockNamesInIndexOrder = make([]string, len(blockSchemas))
		for name, bi := range blockIndexByName {
			blockNamesInIndexOrder[bi] = name
		}
	})
	return blockNamesInIndexOrder
}

// DescribeState returns a state's block name and every property value.
func DescribeState(b Block) (string, map[string]string, bool) {
	bi := ownerIndex(int(b))
	if bi < 0 {
		return "", nil, false
	}
	sc := blockSchemas[bi]
	idx := unpackProps(bi, statePacked[b])
	out := make(map[string]string, len(sc.Names))
	for j, n := range sc.Names {
		out[n] = sc.Values[j][idx[j]]
	}
	return blockNamesByIndex()[bi], out, true
}

// StateProperty reads one property of a state.
func StateProperty(b Block, prop string) (string, bool) {
	bi := ownerIndex(int(b))
	if bi < 0 {
		return "", false
	}
	sc := blockSchemas[bi]
	idx := unpackProps(bi, statePacked[b])
	for j, n := range sc.Names {
		if n == prop {
			return sc.Values[j][idx[j]], true
		}
	}
	return "", false
}

// HasProperty reports whether the state's block declares a property.
func HasProperty(b Block, prop string) bool {
	bi := ownerIndex(int(b))
	if bi < 0 {
		return false
	}
	return slices.Contains(blockSchemas[bi].Names, prop)
}

// WithProperty returns the same block with one property changed. An
// unknown property, a block that lacks it, or a value outside the
// property's allowed set returns false.
func WithProperty(b Block, prop, value string) (Block, bool) {
	bi := ownerIndex(int(b))
	if bi < 0 {
		return Air, false
	}
	sc := blockSchemas[bi]
	idx := unpackProps(bi, statePacked[b])
	for j, n := range sc.Names {
		if n != prop {
			continue
		}
		vi := valueIndex(sc.Values[j], value)
		if vi < 0 {
			return Air, false
		}
		idx[j] = vi
		s, ok := stateLookupTable()[stateKeyFor(bi, packProps(bi, idx))]
		return s, ok
	}
	return Air, false
}

// PropertyNames lists a block's property names, or nil for an unknown
// block or one with no properties.
func PropertyNames(name string) []string {
	bi, ok := blockIndexByName[name]
	if !ok {
		return nil
	}
	names := blockSchemas[bi].Names
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	copy(out, names)
	return out
}

// PropertyValues lists one property's allowed values for a block.
func PropertyValues(name, prop string) []string {
	bi, ok := blockIndexByName[name]
	if !ok {
		return nil
	}
	sc := blockSchemas[bi]
	for j, n := range sc.Names {
		if n == prop {
			out := make([]string, len(sc.Values[j]))
			copy(out, sc.Values[j])
			return out
		}
	}
	return nil
}

// StatesOfBlock lists every state id of a block, in id order.
func StatesOfBlock(name string) []Block {
	bi, ok := blockIndexByName[name]
	if !ok {
		return nil
	}
	base, n := blockBase[bi], blockCounts[bi]
	out := make([]Block, n)
	for i := range out {
		out[i] = Block(base + int32(i))
	}
	return out
}

// BlockHasName reports whether a block name exists in the registry.
func BlockHasName(name string) bool {
	_, ok := blockIndexByName[name]
	return ok
}

// StateCount is the number of distinct block states in the registry.
func StateCount() int { return len(statePacked) }
