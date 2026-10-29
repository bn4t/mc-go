package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"strings"
	"sync"
)

// Test definitions are read from the same captured registry sent to clients.
// This catalog is immutable; runtime/template state remains server-owned.
var capturedTestInstances = sync.OnceValue(func() map[string]map[string]any {
	catalog := map[string]map[string]any{}
	config, err := javaConfigFor(776)
	if err != nil {
		panic(err)
	}
	for _, reg := range config.registries {
		if reg.key != "minecraft:test_instance" {
			continue
		}
		r := bytes.NewReader(reg.body)
		if _, err = protocol.ReadString(r); err != nil {
			panic(err)
		}
		count, err := protocol.ReadVarInt(r)
		if err != nil {
			panic(err)
		}
		for i := int32(0); i < count; i++ {
			name, err := protocol.ReadString(r)
			if err != nil {
				panic(err)
			}
			present, err := protocol.ReadBool(r)
			if err != nil {
				panic(err)
			}
			if !present {
				continue
			}
			value, err := nbt.ReadUnnamed(r)
			if err != nil {
				panic(err)
			}
			if m, ok := value.(map[string]any); ok {
				catalog[name] = m
			}
		}
	}
	return catalog
})

func testInstanceDefinition(name string) map[string]any { return capturedTestInstances()[name] }

func testInstanceDescription(def map[string]any) component {
	if def == nil {
		c := translatable("test_instance.description.no_test")
		c.color = "red"
		return c
	}
	row := func(key string, value component) component {
		value.color = "blue"
		c := translatable("test_instance.description."+key, value)
		c.extra = []component{literal("\n")}
		return c
	}
	kind, _ := def["type"].(string)
	out := row("type", translatable("test_instance.type."+strings.TrimPrefix(kind, "minecraft:")))
	if function, ok := def["function"].(string); ok {
		out.extra = append(out.extra, row("function", literal(function)))
	}
	name, _ := def["structure"].(string)
	batch, _ := def["environment"].(string)
	info := row("structure", literal(name))
	info.extra = append(info.extra, row("batch", literal(batch)))
	out.extra = append(out.extra, info)
	return out
}
