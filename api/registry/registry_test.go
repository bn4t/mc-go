package registry_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"mc-go/api/registry"
)

func key(t *testing.T, name string, props map[string]string) registry.StateKey {
	t.Helper()
	k, err := registry.NewStateKey(name, props)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
func catalog(t *testing.T, states ...registry.StateKey) *registry.BlockCatalog {
	t.Helper()
	defs := make([]registry.BlockDefinition, len(states))
	seen := map[string]bool{}
	for i, k := range states {
		defs[i] = registry.BlockDefinition{State: k, Default: !seen[k.Name()]}
		seen[k.Name()] = true
	}
	c, err := registry.NewBlockCatalog(defs)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCanonicalKeyPropertyOrderImmutabilityAndJSON(t *testing.T) {
	props := map[string]string{"axis": "x", "custom": "a,b =%+"}
	first := key(t, "example:log", props)
	second := key(t, "example:log", map[string]string{"custom": "a,b =%+", "axis": "x"})
	if first != second {
		t.Fatal("property order changed identity")
	}
	props["axis"] = "y"
	copy := first.Properties()
	copy["axis"] = "z"
	if first.Properties()["axis"] != "x" {
		t.Fatal("state aliases mutable input/output")
	}
	parsed, err := registry.ParseStateKey(first.String())
	if err != nil || parsed != first {
		t.Fatalf("text roundtrip: %v %v", parsed, err)
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var decoded registry.StateKey
	if err = json.Unmarshal(raw, &decoded); err != nil || decoded != first {
		t.Fatalf("JSON roundtrip: %s %v", raw, err)
	}
	for _, bad := range []string{"stone", "Minecraft:stone", "minecraft:", "minecraft:stone[axis=x,axis=y]", "minecraft:stone[]", "minecraft:stone[axis=]", "minecraft:stone[axis=%]"} {
		if _, err := registry.ParseStateKey(bad); err == nil {
			t.Fatalf("accepted malformed key %q", bad)
		}
	}
}

func TestCatalogDefaultsAndMappingAreIndependentOfInsertionOrder(t *testing.T) {
	air := key(t, "minecraft:air", nil)
	x := key(t, "example:log", map[string]string{"axis": "x"})
	y := key(t, "example:log", map[string]string{"axis": "y"})
	first := catalog(t, air, x, y)
	second := catalog(t, y, air, x)
	a, _ := first.Lookup(x)
	b, _ := second.Lookup(x)
	if a == b {
		t.Fatal("fixture failed to permute handles")
	}
	entries := []registry.BlockWireEntry{{State: air, WireID: 12}, {State: x, WireID: 3}, {State: y, WireID: 7}}
	for _, c := range []*registry.BlockCatalog{first, second} {
		mapping, err := registry.NewBlockMapping(900, c, entries, registry.MissingBlockPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		if err = mapping.RequireComplete(); err != nil {
			t.Fatal(err)
		}
		id, _ := c.Lookup(x)
		wire, err := mapping.Encode(id)
		if err != nil || wire != 3 {
			t.Fatalf("runtime=%d encoded=%d err=%v", id, wire, err)
		}
		back, ok := mapping.FromWire(wire)
		if !ok || back != id {
			t.Fatal("numeric decode did not resolve canonical state")
		}
	}
	if id, ok := first.Resolve("example:log", nil); !ok || id != a {
		t.Fatal("default property resolution failed")
	}
	if _, ok := first.Resolve("example:log", map[string]string{"extra": "false"}); ok {
		t.Fatal("unknown property accepted")
	}
	if _, ok := first.Resolve("example:log", map[string]string{"axis": "z"}); ok {
		t.Fatal("unknown state fabricated")
	}
}

func TestStrictMissingAndExplicitVisualFallback(t *testing.T) {
	air := key(t, "minecraft:air", nil)
	newBlock := key(t, "example:new_block", nil)
	c := catalog(t, air, newBlock)
	id, _ := c.Lookup(newBlock)
	strict, err := registry.NewBlockMapping(775, c, []registry.BlockWireEntry{{State: air, WireID: 5}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = strict.Encode(id); !errors.Is(err, registry.ErrMissingBlockState) {
		t.Fatal("missing state was silently projected")
	}
	if err = strict.RequireComplete(); err == nil {
		t.Fatal("incomplete map claimed completeness")
	}
	visual, err := strict.WithPolicy(registry.MissingBlockPolicy{Mode: registry.VisualFallbackBlocks, Fallback: air})
	if err != nil {
		t.Fatal(err)
	}
	if wire, err := visual.Encode(id); err != nil || wire != 5 {
		t.Fatalf("fallback=%d %v", wire, err)
	}
	if _, exact := visual.Lookup(id); exact {
		t.Fatal("fallback claimed exact mapping")
	}
	if _, err := strict.Encode(id); !errors.Is(err, registry.ErrMissingBlockState) {
		t.Fatal("fallback mutated original strict view")
	}
	if state, _ := c.State(id); state != newBlock {
		t.Fatal("visual fallback rewrote authoritative identity")
	}
	missing := visual.Missing()
	missing[0] = 999
	if !reflect.DeepEqual(visual.Missing(), []registry.BlockStateID{id}) {
		t.Fatal("missing metadata aliases caller")
	}
	if _, err = visual.Encode(999); !errors.Is(err, registry.ErrUnknownBlockState) {
		t.Fatal("unknown handle used fallback")
	}
	if _, ok := visual.FromWire(6); ok {
		t.Fatal("unknown wire state accepted")
	}
	if _, err = strict.WithPolicy(registry.MissingBlockPolicy{Mode: registry.VisualFallbackBlocks, Fallback: newBlock}); err == nil {
		t.Fatal("unmapped fallback accepted")
	}
}

func TestInvalidCatalogAndMappingReject(t *testing.T) {
	air := key(t, "minecraft:air", nil)
	stone := key(t, "minecraft:stone", nil)
	if _, err := registry.NewBlockCatalog([]registry.BlockDefinition{{State: air}}); err == nil {
		t.Fatal("missing default accepted")
	}
	if _, err := registry.NewBlockCatalog([]registry.BlockDefinition{{State: air, Default: true}, {State: air}}); err == nil {
		t.Fatal("duplicate canonical state accepted")
	}
	if _, err := registry.NewBlockCatalog([]registry.BlockDefinition{
		{State: key(t, "example:inconsistent", map[string]string{"a": "1"}), Default: true},
		{State: key(t, "example:inconsistent", map[string]string{"b": "2"})},
	}); err == nil {
		t.Fatal("inconsistent per-block property schema accepted")
	}
	c := catalog(t, air, stone)
	for name, entries := range map[string][]registry.BlockWireEntry{
		"unknown":             {{State: key(t, "example:absent", nil), WireID: 0}},
		"negative":            {{State: air, WireID: -1}},
		"sparse enormous":     {{State: air, WireID: 1 << 30}},
		"duplicate wire":      {{State: air, WireID: 0}, {State: stone, WireID: 0}},
		"duplicate canonical": {{State: air, WireID: 0}, {State: air, WireID: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := registry.NewBlockMapping(776, c, entries, registry.MissingBlockPolicy{}); err == nil {
				t.Fatal("bad mapping accepted")
			}
		})
	}
}

func TestMappingHotPathConcurrentAndAllocationFree(t *testing.T) {
	air := key(t, "minecraft:air", nil)
	stone := key(t, "minecraft:stone", nil)
	c := catalog(t, air, stone)
	m, err := registry.NewBlockMapping(776, c, []registry.BlockWireEntry{{State: air, WireID: 9}, {State: stone, WireID: 2}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if allocs := testing.AllocsPerRun(1000, func() { _, _ = m.Encode(1); _, _ = m.Lookup(0); _, _ = m.FromWire(2) }); allocs != 0 {
		t.Fatalf("hot mapping allocates %v", allocs)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 1000 {
				if wire, err := m.Encode(1); wire != 2 || err != nil {
					t.Error("immutable mapping changed")
				}
			}
		})
	}
	wg.Wait()
}
