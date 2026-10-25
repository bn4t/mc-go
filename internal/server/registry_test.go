package server

import (
	"bytes"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// countingReader tracks bytes consumed so the test can interleave
// fixed-field reads with NBT decodes on one stream.
type countingReader struct {
	r *bytes.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	c.n += k
	return k, err
}

// parseRegistry decodes one Registry Data body: registry key plus entry
// keys, verifying each entry carries a parseable NBT compound.
func parseRegistry(t *testing.T, data []byte) (string, []string) {
	t.Helper()
	cr := &countingReader{r: bytes.NewReader(data)}
	key, err := protocol.ReadString(cr)
	if err != nil {
		t.Fatalf("registry key: %v", err)
	}
	n, err := protocol.ReadVarInt(cr)
	if err != nil || n < 0 || n > 1024 {
		t.Fatalf("entry count = %d, err %v", n, err)
	}
	keys := make([]string, 0, n)
	for i := int32(0); i < n; i++ {
		ek, err := protocol.ReadString(cr)
		if err != nil {
			t.Fatalf("entry key: %v", err)
		}
		present, err := protocol.ReadBool(cr)
		if err != nil || !present {
			t.Fatalf("entry %q present=%v err=%v", ek, present, err)
		}
		v, err := nbt.ReadUnnamed(cr)
		if err != nil {
			t.Fatalf("entry %q nbt: %v", ek, err)
		}
		if _, ok := v.(map[string]any); !ok {
			t.Fatalf("entry %q nbt type %T, want compound", ek, v)
		}
		keys = append(keys, ek)
	}
	if cr.r.Len() != 0 {
		t.Fatalf("%d trailing bytes in registry %q", cr.r.Len(), key)
	}
	return key, keys
}

func TestCapturedRegistries(t *testing.T) {
	loadConfigData()
	if len(configRegs) < 20 {
		t.Fatalf("captured registries = %d, want the full vanilla set", len(configRegs))
	}
	seen := map[string]bool{}
	for _, reg := range configRegs {
		if seen[reg.key] {
			t.Fatalf("duplicate registry %s", reg.key)
		}
		seen[reg.key] = true
		key, ids, err := parseRegistryBody(reg.body)
		if err != nil || key != reg.key {
			t.Fatalf("parse %s: key=%s err=%v", reg.key, key, err)
		}
		if len(ids) == 0 {
			t.Fatalf("registry %s has no entries", reg.key)
		}
	}
	for _, need := range []string{
		"minecraft:worldgen/biome",
		"minecraft:dimension_type",
		"minecraft:damage_type",
		"minecraft:chat_type",
	} {
		if !seen[need] {
			t.Fatalf("captured registries lack %s: %v", need, seen)
		}
	}
}

func TestBiomeIDs(t *testing.T) {
	loadConfigData()
	if got := biomeID("minecraft:plains"); got < 0 {
		t.Fatalf("plains index = %d", got)
	}
	if got := biomeID("minecraft:no_such_biome"); got != 0 {
		t.Fatalf("unknown biome index = %d, want fallback 0", got)
	}
}

func TestDimensionTypes(t *testing.T) {
	loadConfigData()
	var dim entryIDs
	for _, reg := range configRegs {
		if reg.key == "minecraft:dimension_type" {
			_, ids, err := parseRegistryBody(reg.body)
			if err != nil {
				t.Fatal(err)
			}
			dim = ids
		}
	}
	if dim == nil {
		t.Fatal("no dimension_type capture")
	}
	for name, want := range map[string]int32{
		"minecraft:overworld":       0,
		"minecraft:overworld_caves": 1,
		"minecraft:the_end":         2,
		"minecraft:the_nether":      3,
	} {
		if got, ok := dim[name]; !ok || got != want {
			t.Fatalf("dimension %s index = %d (present %v), want %d", name, got, ok, want)
		}
	}
}
