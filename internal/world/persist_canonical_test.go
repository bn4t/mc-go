package world

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"mc-go/api/registry"
)

func persistenceCatalog(t *testing.T, order []string) *registry.BlockCatalog {
	t.Helper()
	definitions := make([]registry.BlockDefinition, len(order))
	for i, text := range order {
		key, err := registry.ParseStateKey(text)
		if err != nil {
			t.Fatal(err)
		}
		definitions[i] = registry.BlockDefinition{State: key, Default: text != "minecraft:oak_log[axis=y]"}
	}
	c, err := registry.NewBlockCatalog(definitions)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func stateID(t *testing.T, c *registry.BlockCatalog, text string) Block {
	t.Helper()
	key, err := registry.ParseStateKey(text)
	if err != nil {
		t.Fatal(err)
	}
	id, ok := c.Lookup(key)
	if !ok {
		t.Fatal("fixture state absent")
	}
	return id
}

func TestCanonicalV2RestartWithReorderedCatalogAndExplicitAir(t *testing.T) {
	order := []string{"minecraft:air", "minecraft:stone", "minecraft:oak_log[axis=x]", "minecraft:oak_log[axis=y]"}
	a := persistenceCatalog(t, order)
	b := persistenceCatalog(t, []string{order[3], order[2], order[0], order[1]})
	aStone := stateID(t, a, "minecraft:stone")
	bStone := stateID(t, b, "minecraft:stone")
	first := NewGen(nil, 8, func(Pos) Block { return aStone })
	positions := []Pos{{-17, -61, 16}, {0, 0, 0}, {16, 70, -17}}
	keys := []string{"minecraft:air", "minecraft:oak_log[axis=x]", "minecraft:oak_log[axis=y]"}
	for i, pos := range positions {
		first.SetBlock(pos, stateID(t, a, keys[i]))
	}
	path := filepath.Join(t.TempDir(), "world.json")
	if err := first.saveWithCatalog(path, a); err != nil {
		t.Fatal(err)
	}
	first.Stop()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"b":`)) || !bytes.Contains(raw, []byte(`"state":"minecraft:air"`)) || !bytes.Contains(raw, []byte(`oak_log[axis=x]`)) {
		t.Fatalf("v2persisted runtime IDs or lost fullprops/air: %s", raw)
	}
	second := NewGen(nil, 3, func(Pos) Block { return bStone })
	defer second.Stop()
	if err = second.loadWithCatalog(path, b); err != nil {
		t.Fatal(err)
	}
	for i, pos := range positions {
		got := second.GetBlock(pos)
		want := stateID(t, b, keys[i])
		if got != want {
			t.Fatalf("%s restored runtime=%d want=%d", keys[i], got, want)
		}
	}
	if got := second.GetBlock(Pos{99, 5, 99}); got != bStone {
		t.Fatal("restart changed implicit generator state")
	}
	if stateID(t, a, keys[0]) == stateID(t, b, keys[0]) {
		t.Fatal("fixture failed to move air handle")
	}
}

func TestLegacyV1UsesHistorical776IdentityInReorderedCatalog(t *testing.T) {
	catalog := persistenceCatalog(t, []string{"minecraft:oak_log[axis=y]", "minecraft:oak_log[axis=x]", "minecraft:air", "minecraft:stone"})
	path := filepath.Join(t.TempDir(), "legacy.json")
	legacyLog, ok := BlockStateOf("oak_log", map[string]string{"axis": "x"})
	if !ok {
		t.Fatal("missing legacy oak log")
	}
	old := struct {
		Version int `json:"version"`
		Blocks  []struct {
			X, Y, Z int
			B       int64
		} `json:"blocks"`
	}{Version: 1}
	for i, wire := range []int64{0, 1, int64(legacyLog)} {
		old.Blocks = append(old.Blocks, struct {
			X, Y, Z int
			B       int64
		}{X: i, B: wire})
	}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	w := NewGen(nil, 2, func(Pos) Block { return stateID(t, catalog, "minecraft:stone") })
	defer w.Stop()
	if err = w.loadWithCatalog(path, catalog); err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"minecraft:air", "minecraft:stone", "minecraft:oak_log[axis=x]"} {
		if got := w.GetBlock(Pos{X: i}); got != stateID(t, catalog, key) {
			t.Fatalf("legacy numeric ID reinterpreted: %s ->%d", key, got)
		}
	}
	if err = w.saveWithCatalog(path, catalog); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"version":2`)) {
		t.Fatal("legacy file not upgraded on save")
	}
}

func TestUnknownSavedStateRejectsWithoutPartialRestore(t *testing.T) {
	w := New(nil, 2)
	defer w.Stop()
	w.SetBlock(Pos{1, 2, 3}, Stone)
	before := w.Snapshot()
	for _, raw := range []string{
		`{"version":2,"blocks":[{"x":4,"state":"minecraft:dirt"},{"x":5,"state":"example:missing"}]}`,
		`{"version":2,"blocks":[{"state":"minecraft:oak_log[axis=missing]"}]}`,
		`{"version":1,"blocks":[{"x":4,"b":1},{"x":5,"b":32366}]}`,
		`{"version":1,"blocks":[{"b":-1}]}`,
		`{"version":3,"blocks":[]}`,
	} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := w.Load(path); err == nil {
			t.Fatalf("invalid save accepted: %s", raw)
		}
		if !reflect.DeepEqual(before, w.Snapshot()) {
			t.Fatal("failedload partially replaced world")
		}
	}
}
