package world

import (
	"fmt"
	"sync"

	"mc-go/api/registry"
)

var canonicalBlocks struct {
	sync.Once
	catalog *registry.BlockCatalog
	java776 *registry.BlockMapping
}

// BlockCatalog exposes immutable canonical identities. Builtin runtime handles
// retain their historical order so generated behavior tables remain valid;
// adapters must still translate handles through a measured protocol mapping.
func BlockCatalog() *registry.BlockCatalog {
	canonicalBlocks.Do(func() {
		definitions := make([]registry.BlockDefinition, maxBlockState)
		entries := make([]registry.BlockWireEntry, maxBlockState)
		for i := 0; i < maxBlockState; i++ {
			name, properties, ok := DescribeState(Block(i))
			if !ok {
				panic(fmt.Sprintf("world: invalid builtin state %d", i))
			}
			key, err := registry.NewStateKey("minecraft:"+name, properties)
			if err != nil {
				panic(err)
			}
			definitions[i] = registry.BlockDefinition{State: key, Default: blockStates[name] == Block(i)}
			entries[i] = registry.BlockWireEntry{State: key, WireID: int32(i)}
		}
		var err error
		canonicalBlocks.catalog, err = registry.NewBlockCatalog(definitions)
		if err != nil {
			panic(err)
		}
		canonicalBlocks.java776, err = registry.NewBlockMapping(776, canonicalBlocks.catalog, entries, registry.MissingBlockPolicy{})
		if err != nil {
			panic(err)
		}
	})
	return canonicalBlocks.catalog
}

var java775Blocks struct {
	sync.Once
	mapping *registry.BlockMapping
}

// BlockMapping returns an immutable, strict adapter projection. Protocol 775
// has factual gaps for states introduced in 776; Encode rejects those states.
// A frontend must explicitly choose any visual fallback through WithPolicy.
// A mapping does not establish support for that protocol's other packet/data
// domains, and does not make it an admitted game protocol.
func BlockMapping(protocol int) (*registry.BlockMapping, bool) {
	catalog := BlockCatalog()
	switch protocol {
	case 776:
		return canonicalBlocks.java776, true
	case 775:
		java775Blocks.Do(func() {
			if len(protocol775BlockStates) != catalog.Len() {
				panic("world: protocol 775 block mapping length mismatch")
			}
			entries := make([]registry.BlockWireEntry, 0, len(protocol775BlockStates))
			for runtime, wire := range protocol775BlockStates {
				if wire < 0 {
					continue
				}
				key, _ := catalog.State(registry.BlockStateID(runtime))
				entries = append(entries, registry.BlockWireEntry{State: key, WireID: wire})
			}
			var err error
			java775Blocks.mapping, err = registry.NewBlockMapping(775, catalog, entries, registry.MissingBlockPolicy{})
			if err != nil {
				panic(err)
			}
		})
		return java775Blocks.mapping, true
	default:
		return nil, false
	}
}
