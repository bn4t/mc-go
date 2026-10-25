package world

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mc-go/api/registry"
)

func TestBuiltinCanonicalCatalogAnd776Identity(t *testing.T) {
	catalog := BlockCatalog()
	if catalog.Len() != 32366 {
		t.Fatalf("catalog has %d states", catalog.Len())
	}
	mapping, ok := BlockMapping(776)
	if !ok {
		t.Fatal("missing776projection")
	}
	if err := mapping.RequireComplete(); err != nil {
		t.Fatal(err)
	}
	for runtime := 0; runtime < catalog.Len(); runtime++ {
		id := registry.BlockStateID(runtime)
		key, ok := catalog.State(id)
		if !ok {
			t.Fatalf("missing runtime handle %d", id)
		}
		name, props, ok := DescribeState(id)
		if !ok {
			t.Fatal("builtin behavior descriptor absent")
		}
		want, err := registry.NewStateKey("minecraft:"+name, props)
		if err != nil || key != want {
			t.Fatalf("state %d key=%s want=%s err=%v", id, key, want, err)
		}
		if back, ok := catalog.Lookup(key); !ok || back != id {
			t.Fatalf("canonical roundtrip %d -> %d", id, back)
		}
		wire, err := mapping.Encode(id)
		if err != nil || wire != int32(runtime) {
			t.Fatalf("776encode state%d -> %d (%v)", runtime, wire, err)
		}
		if back, ok := mapping.FromWire(wire); !ok || back != id {
			t.Fatalf("776decode %d -> %d", wire, back)
		}
	}
	if _, ok := mapping.FromWire(32366); ok {
		t.Fatal("unknown numeric state accepted")
	}
	if _, ok := catalog.State(^registry.BlockStateID(0)); ok {
		t.Fatal("unknown runtime state accepted")
	}
	if _, ok := BlockMapping(774); ok {
		t.Fatal("unmeasured projection advertised")
	}
}

func TestProtocol775ProjectionFactsAndMissingPolicy(t *testing.T) {
	mapping, _ := BlockMapping(775)
	if mapping.RequireComplete() == nil || len(mapping.Missing()) != 2493 {
		t.Fatal("775projection hid new776states")
	}
	renumbered, exact := 0, 0
	for runtime := 0; runtime < BlockCatalog().Len(); runtime++ {
		wire, ok := mapping.Lookup(registry.BlockStateID(runtime))
		if !ok {
			continue
		}
		exact++
		if wire != int32(runtime) {
			renumbered++
		}
		if back, ok := mapping.FromWire(wire); !ok || back != registry.BlockStateID(runtime) {
			t.Fatal("775numeric reverse lookup changed canonical identity")
		}
	}
	if protocol775SourceStateCount != 29873 {
		t.Fatal("source state count changed")
	}
	if exact != protocol775SourceStateCount || renumbered != 5186 {
		t.Fatalf("775facts exact=%d renumbered=%d", exact, renumbered)
	}
	missing := mapping.Missing()[0]
	if _, err := mapping.Encode(missing); err != registry.ErrMissingBlockState {
		t.Fatal("strict775projection fabricated newstate")
	}
	air, _ := BlockCatalog().State(Air)
	visual, err := mapping.WithPolicy(registry.MissingBlockPolicy{Mode: registry.VisualFallbackBlocks, Fallback: air})
	if err != nil {
		t.Fatal(err)
	}
	if wire, err := visual.Encode(missing); err != nil || wire != 0 {
		t.Fatalf("explicit air presentation=%d %v", wire, err)
	}
	if key, _ := mapping.Catalog().State(missing); key.Name() == "minecraft:air" {
		t.Fatal("fallback overwrote canonical identity")
	}
}

// Optional authoritative report checks supplement always-on exhaustive table
// invariants. Missing ignored datagen inputs are explicit skips, not passes.
func TestCanonicalMappingsMatchOfficialReports(t *testing.T) {
	for _, fixture := range []struct {
		version  string
		protocol int
	}{{"26.1", 775}, {"26.2", 776}} {
		t.Run(fixture.version, func(t *testing.T) {
			path := filepath.Join("..", "..", "vanilla", "measure-"+fixture.version, "generated", "reports", "blocks.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("official report unavailable: %v", err)
			}
			var report map[string]struct {
				States []struct {
					ID         int32             `json:"id"`
					Properties map[string]string `json:"properties"`
				} `json:"states"`
			}
			if err = json.Unmarshal(raw, &report); err != nil {
				t.Fatal(err)
			}
			mapping, _ := BlockMapping(fixture.protocol)
			checked := 0
			for name, block := range report {
				for _, state := range block.States {
					key, err := registry.NewStateKey(name, state.Properties)
					if err != nil {
						t.Fatal(err)
					}
					id, ok := BlockCatalog().Lookup(key)
					if !ok {
						t.Fatalf("official state missing: %s", key)
					}
					wire, err := mapping.Encode(id)
					if err != nil || wire != state.ID {
						t.Fatalf("%s wire=%d want=%d err=%v", key, wire, state.ID, err)
					}
					checked++
				}
			}
			want := 32366
			if fixture.protocol == 775 {
				want = 29873
			}
			if checked != want {
				t.Fatalf("checked %d states, want %d", checked, want)
			}
		})
	}
}
