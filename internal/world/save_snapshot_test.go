package world

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveSnapshotDetachedCanonicalRestartAfterWorldStop(t *testing.T) {
	w := NewGen(nil, 3, func(Pos) Block { return Stone })
	positions := []Pos{{-17, -61, 16}, {0, 0, 0}, {16, 70, -17}}
	keys := []string{"minecraft:air", "minecraft:oak_log[axis=x]", "minecraft:oak_log[axis=y]"}
	for i, pos := range positions {
		state, ok := BlockStateOf("oak_log", map[string]string{"axis": "x"})
		if i == 0 {
			state, ok = Air, true
		} else if i == 2 {
			state, ok = BlockStateOf("oak_log", map[string]string{"axis": "y"})
		}
		if !ok {
			t.Fatal("missing canonical fixture state")
		}
		w.SetBlock(pos, state)
	}
	snapshot := w.CaptureSave()
	w.Restore(map[Pos]Block{{X: 100}: Dirt})
	w.Stop()
	path := filepath.Join(t.TempDir(), "captured.json")
	if err := snapshot.Write(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Write(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if !bytes.Equal(first, second) || bytes.Contains(first, []byte(`"b":`)) {
		t.Fatal("detached canonical serialization was unstable or persisted numeric handles")
	}
	catalog := persistenceCatalog(t, []string{keys[2], "minecraft:stone", keys[0], keys[1]})
	restarted := NewGen(nil, 2, func(Pos) Block { return stateID(t, catalog, "minecraft:stone") })
	defer restarted.Stop()
	if err := restarted.loadWithCatalog(path, catalog); err != nil {
		t.Fatal(err)
	}
	for i, pos := range positions {
		if got := restarted.GetBlock(pos); got != stateID(t, catalog, keys[i]) {
			t.Fatalf("captured canonical state %s lost at %+v: %d", keys[i], pos, got)
		}
	}
	if got := restarted.GetBlock(Pos{X: 100}); got != stateID(t, catalog, "minecraft:stone") {
		t.Fatal("post-capture Restore leaked into the saved snapshot")
	}
}

func TestSaveSnapshotUnknownStateDoesNotTruncateExistingFile(t *testing.T) {
	w := NewGen(nil, 1, func(Pos) Block { return Air })
	w.SetBlock(Pos{X: -9}, Block(^uint32(0)))
	snapshot := w.CaptureSave()
	w.Stop()
	path := filepath.Join(t.TempDir(), "existing.json")
	prior := []byte("previous complete file")
	if err := os.WriteFile(path, prior, 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Write(path); err == nil {
		t.Fatal("unknown runtime state was persisted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, prior) {
		t.Fatalf("canonical validation damaged the previous file: %q, %v", after, err)
	}
}
