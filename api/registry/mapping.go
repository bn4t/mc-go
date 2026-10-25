package registry

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownBlockState = errors.New("registry: unknown runtime block state")
	ErrMissingBlockState = errors.New("registry: block state has no exact protocol mapping")
)

// BlockWireEntry maps a canonical identity to a measured protocol numeric ID.
// Unknown canonical states, duplicate identities and duplicate wire IDs reject.
type BlockWireEntry struct {
	State  StateKey
	WireID int32
}

type MissingBlockMode uint8

const (
	RejectMissingBlocks MissingBlockMode = iota
	VisualFallbackBlocks
)

// MissingBlockPolicy is strict by default. VisualFallbackBlocks is an explicit
// presentation choice: it changes the encoded view, never authoritative state.
// Its fallback must have an exact entry. Inspect Missing for affected states.
type MissingBlockPolicy struct {
	Mode     MissingBlockMode
	Fallback StateKey
}

// BlockMapping is an immutable adapter-owned numeric translation. String keys
// are used only during construction; Encode/Lookup/FromWire use direct arrays.
// Partial tables are allowed, but strict Encode rejects each missing state.
type BlockMapping struct {
	protocol     int
	catalog      *BlockCatalog
	toWire       []int32
	fromWire     []BlockStateID
	present      []bool
	missing      []BlockStateID
	policy       MissingBlockPolicy
	fallbackWire int32
}

// NewBlockMapping bounds its reverse array to 1,048,576 wire entries. This
// prevents malformed source data from allocating an unbounded sparse table.
func NewBlockMapping(protocol int, catalog *BlockCatalog, entries []BlockWireEntry, policy MissingBlockPolicy) (*BlockMapping, error) {
	if protocol < 1 || catalog == nil {
		return nil, fmt.Errorf("registry: positive protocol and catalog required")
	}
	if policy.Mode != RejectMissingBlocks && policy.Mode != VisualFallbackBlocks {
		return nil, fmt.Errorf("registry: unknown missing-block policy")
	}
	m := &BlockMapping{protocol: protocol, catalog: catalog, toWire: make([]int32, catalog.Len()), policy: policy, fallbackWire: -1}
	for i := range m.toWire {
		m.toWire[i] = -1
	}
	maxWire := int32(-1)
	seenWire := map[int32]bool{}
	for _, entry := range entries {
		runtime, ok := catalog.Lookup(entry.State)
		if !ok {
			return nil, fmt.Errorf("registry: mapping references absent state %s", entry.State)
		}
		if entry.WireID < 0 || entry.WireID >= 1<<20 {
			return nil, fmt.Errorf("registry: wire block state ID %d out of bounds", entry.WireID)
		}
		if m.toWire[runtime] >= 0 || seenWire[entry.WireID] {
			return nil, fmt.Errorf("registry: duplicate block mapping")
		}
		seenWire[entry.WireID] = true
		m.toWire[runtime] = entry.WireID
		if entry.WireID > maxWire {
			maxWire = entry.WireID
		}
	}
	m.fromWire = make([]BlockStateID, int(maxWire)+1)
	m.present = make([]bool, int(maxWire)+1)
	for runtime, wire := range m.toWire {
		if wire < 0 {
			m.missing = append(m.missing, BlockStateID(runtime))
			continue
		}
		m.fromWire[wire] = BlockStateID(runtime)
		m.present[wire] = true
	}
	if policy.Mode == VisualFallbackBlocks {
		fallback, ok := catalog.Lookup(policy.Fallback)
		if !ok || m.toWire[fallback] < 0 {
			return nil, fmt.Errorf("registry: visual fallback must have an exact mapping")
		}
		m.fallbackWire = m.toWire[fallback]
	}
	return m, nil
}
func (m *BlockMapping) Protocol() int              { return m.protocol }
func (m *BlockMapping) Catalog() *BlockCatalog     { return m.catalog }
func (m *BlockMapping) Policy() MissingBlockPolicy { return m.policy }
func (m *BlockMapping) Missing() []BlockStateID    { return append([]BlockStateID(nil), m.missing...) }
func (m *BlockMapping) RequireComplete() error {
	if len(m.missing) > 0 {
		return fmt.Errorf("registry: protocol %d lacks %d of %d canonical block states", m.protocol, len(m.missing), m.catalog.Len())
	}
	return nil
}
func (m *BlockMapping) Lookup(state BlockStateID) (int32, bool) {
	if uint64(state) >= uint64(len(m.toWire)) {
		return -1, false
	}
	id := m.toWire[state]
	return id, id >= 0
}
func (m *BlockMapping) FromWire(id int32) (BlockStateID, bool) {
	if id < 0 || int64(id) >= int64(len(m.fromWire)) || !m.present[id] {
		return 0, false
	}
	return m.fromWire[id], true
}
func (m *BlockMapping) Encode(state BlockStateID) (int32, error) {
	if uint64(state) >= uint64(len(m.toWire)) {
		return -1, ErrUnknownBlockState
	}
	if wire := m.toWire[state]; wire >= 0 {
		return wire, nil
	}
	if m.policy.Mode == VisualFallbackBlocks {
		return m.fallbackWire, nil
	}
	return -1, ErrMissingBlockState
}

// WithPolicy returns another immutable view of the exact same measured table.
// It cannot add mappings. All immutable arrays remain shared between views.
func (m *BlockMapping) WithPolicy(policy MissingBlockPolicy) (*BlockMapping, error) {
	if policy.Mode != RejectMissingBlocks && policy.Mode != VisualFallbackBlocks {
		return nil, fmt.Errorf("registry: unknown missing-block policy")
	}
	copy := *m
	copy.policy = policy
	copy.fallbackWire = -1
	if policy.Mode == VisualFallbackBlocks {
		state, ok := m.catalog.Lookup(policy.Fallback)
		if !ok {
			return nil, fmt.Errorf("registry: visual fallback state absent from catalog")
		}
		wire, ok := m.Lookup(state)
		if !ok {
			return nil, fmt.Errorf("registry: visual fallback must have an exact mapping")
		}
		copy.fallbackWire = wire
	}
	return &copy, nil
}
