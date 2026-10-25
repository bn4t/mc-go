package world

import (
	"encoding/json"
	"fmt"
	"os"
)

// persistEntry is one override row in the save file.
type persistEntry struct {
	X int   `json:"x"`
	Y int   `json:"y"`
	Z int   `json:"z"`
	B Block `json:"b"`
}

type persistFile struct {
	Version int            `json:"version"`
	Blocks  []persistEntry `json:"blocks"`
}

// persistVersion is bumped whenever the save shape changes.
const persistVersion = 1

// Snapshot merges every shard's overrides into one map. The flat
// generator needs no storage: only explicit SetBlock rows are returned.
func (w *World) Snapshot() map[Pos]Block {
	out := make(map[Pos]Block)
	for _, s := range w.shards {
		for p, b := range s.snapshot() {
			out[p] = b
		}
	}
	return out
}

// Restore replaces overrides with m. It clears existing rows first so a
// restore is exact, not a merge.
func (w *World) Restore(m map[Pos]Block) {
	for _, s := range w.shards {
		for p := range s.snapshot() {
			s.delete(p)
		}
	}
	for p, b := range m {
		w.SetBlock(p, b)
	}
}

// Save writes overrides to path as versioned JSON. Generator output is
// implicit and never stored.
func (w *World) Save(path string) error {
	snap := w.Snapshot()
	f := persistFile{Version: persistVersion}
	for p, b := range snap {
		f.Blocks = append(f.Blocks, persistEntry{X: p.X, Y: p.Y, Z: p.Z, B: b})
	}
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Load replaces overrides with the contents of path.
func (w *World) Load(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f persistFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if f.Version != persistVersion {
		return fmt.Errorf("world: save version %d, want %d", f.Version, persistVersion)
	}
	m := make(map[Pos]Block, len(f.Blocks))
	for _, e := range f.Blocks {
		m[Pos{X: e.X, Y: e.Y, Z: e.Z}] = e.B
	}
	w.Restore(m)
	return nil
}
