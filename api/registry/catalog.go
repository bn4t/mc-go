package registry

import (
	"fmt"
	"strings"
)

// BlockDefinition declares one complete state. Exactly one state for every
// block name must be its default. Input order assigns runtime handles; adding
// another catalog or protocol does not change an existing catalog's handles.
type BlockDefinition struct {
	State   StateKey
	Default bool
}

// BlockCatalog is immutable after construction and safe for concurrent reads.
// It describes identities, not block behavior: supplying a custom definition
// does not implement its simulation, collision, drops, or client support.
type BlockCatalog struct {
	states   []StateKey
	ids      map[StateKey]BlockStateID
	defaults map[string]BlockStateID
}

func NewBlockCatalog(definitions []BlockDefinition) (*BlockCatalog, error) {
	if len(definitions) == 0 || uint64(len(definitions)) > uint64(^BlockStateID(0))+1 {
		return nil, fmt.Errorf("registry: invalid block state count %d", len(definitions))
	}
	c := &BlockCatalog{states: make([]StateKey, len(definitions)), ids: make(map[StateKey]BlockStateID, len(definitions)), defaults: map[string]BlockStateID{}}
	schemas := make(map[string]string)
	for i, d := range definitions {
		if !d.State.Valid() {
			return nil, fmt.Errorf("registry: invalid state at index %d", i)
		}
		// A block's complete state identities must share one property schema;
		// otherwise default-based Resolve could never reach some definitions.
		var shape strings.Builder
		for rest := d.State.properties; rest != ""; {
			entry, next, _ := strings.Cut(rest, ",")
			rest = next
			name, _, _ := strings.Cut(entry, "=")
			shape.WriteString(name)
			shape.WriteByte(',')
		}
		if existing, ok := schemas[d.State.Name()]; ok && existing != shape.String() {
			return nil, fmt.Errorf("registry: inconsistent property schema for %s", d.State.Name())
		}
		schemas[d.State.Name()] = shape.String()
		if _, exists := c.ids[d.State]; exists {
			return nil, fmt.Errorf("registry: duplicate state %s", d.State)
		}
		id := BlockStateID(i)
		c.states[i] = d.State
		c.ids[d.State] = id
		if d.Default {
			if _, exists := c.defaults[d.State.Name()]; exists {
				return nil, fmt.Errorf("registry: duplicate default for %s", d.State.Name())
			}
			c.defaults[d.State.Name()] = id
		}
	}
	for _, state := range c.states {
		if _, exists := c.defaults[state.Name()]; !exists {
			return nil, fmt.Errorf("registry: missing default for %s", state.Name())
		}
	}
	return c, nil
}
func (c *BlockCatalog) Len() int { return len(c.states) }
func (c *BlockCatalog) State(id BlockStateID) (StateKey, bool) {
	if uint64(id) >= uint64(len(c.states)) {
		return StateKey{}, false
	}
	return c.states[id], true
}
func (c *BlockCatalog) Lookup(state StateKey) (BlockStateID, bool) {
	id, ok := c.ids[state]
	return id, ok
}
func (c *BlockCatalog) Default(name string) (BlockStateID, bool) {
	id, ok := c.defaults[name]
	return id, ok
}

// Resolve fills omitted properties from the block's declared default. Unknown
// properties, invalid values and unknown names reject; no state is fabricated.
func (c *BlockCatalog) Resolve(name string, properties map[string]string) (BlockStateID, bool) {
	base, ok := c.Default(name)
	if !ok {
		return 0, false
	}
	defaults := c.states[base].Properties()
	for key, value := range properties {
		if _, exists := defaults[key]; !exists {
			return 0, false
		}
		defaults[key] = value
	}
	state, err := NewStateKey(name, defaults)
	if err != nil {
		return 0, false
	}
	return c.Lookup(state)
}
