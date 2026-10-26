package world

import (
	"encoding/json"
	"fmt"
	"mc-go/api/registry"
	"os"
	"sort"
)

// Version 1 stored historical Java776 numeric block IDs. Version 2 stores
// complete canonical identities, independent of runtime handle assignment.
type persistEntry struct {
	X     int               `json:"x"`
	Y     int               `json:"y"`
	Z     int               `json:"z"`
	State registry.StateKey `json:"state"`
}
type persistFile struct {
	Version int            `json:"version"`
	Blocks  []persistEntry `json:"blocks"`
}

const persistVersion = 2

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
			w.invalidateBlock(p)
		}
	}
	for p, b := range m {
		w.SetBlock(p, b)
	}
}

// SaveSnapshot owns detached block overrides and their immutable catalog. It
// retains no world or shard workers, so Write can run after gameplay resumes or
// the source world stops. Capture still copies every override; this is not a
// bounded-byte or incremental world snapshot.
type SaveSnapshot struct {
	blocks  map[Pos]Block
	catalog *registry.BlockCatalog
}

// CaptureSave copies overrides for a later compact JSON save. Callers must
// exclude gameplay mutations while capturing when they need a coherent cut.
func (w *World) CaptureSave() *SaveSnapshot {
	return w.captureSaveWithCatalog(BlockCatalog())
}

func (w *World) captureSaveWithCatalog(catalog *registry.BlockCatalog) *SaveSnapshot {
	return &SaveSnapshot{blocks: w.Snapshot(), catalog: catalog}
}

// Save writes canonical identities as versioned JSON. Generator output is
// implicit; numeric runtime handles are never persisted by the current format.
func (w *World) Save(path string) error { return w.CaptureSave().Write(path) }

func (w *World) saveWithCatalog(path string, catalog *registry.BlockCatalog) error {
	return w.captureSaveWithCatalog(catalog).Write(path)
}

// Write sorts, serializes and writes only captured values. Completion means the
// file write returned; it does not imply fsync or an atomic multi-file commit.
func (snap *SaveSnapshot) Write(path string) error {
	f := persistFile{Version: persistVersion, Blocks: make([]persistEntry, 0, len(snap.blocks))}
	for p, id := range snap.blocks {
		state, ok := snap.catalog.State(id)
		if !ok {
			return fmt.Errorf("world: cannot persist unknown runtime state %d at %+v", id, p)
		}
		f.Blocks = append(f.Blocks, persistEntry{X: p.X, Y: p.Y, Z: p.Z, State: state})
	}
	sort.Slice(f.Blocks, func(i, j int) bool {
		a, b := f.Blocks[i], f.Blocks[j]
		if a.X != b.X {
			return a.X < b.X
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.Z < b.Z
	})
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Load validates the complete file before replacing overrides. Version 1 IDs
// are decoded through their historical776 projection, then resolved into the
// current catalog. Unknown names/properties/legacy IDs reject without mutation.
func (w *World) Load(path string) error { return w.loadWithCatalog(path, BlockCatalog()) }

func (w *World) loadWithCatalog(path string, catalog *registry.BlockCatalog) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var header struct {
		Version int             `json:"version"`
		Blocks  json.RawMessage `json:"blocks"`
	}
	if err = json.Unmarshal(data, &header); err != nil {
		return err
	}
	states := make(map[Pos]Block)
	switch header.Version {
	case persistVersion:
		var entries []persistEntry
		if err = json.Unmarshal(header.Blocks, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			id, ok := catalog.Lookup(entry.State)
			if !ok {
				return fmt.Errorf("world: unknown saved block state %q", entry.State.String())
			}
			states[Pos{entry.X, entry.Y, entry.Z}] = id
		}
	case 1:
		var entries []struct {
			X, Y, Z int
			B       int64
		}
		if err = json.Unmarshal(header.Blocks, &entries); err != nil {
			return err
		}
		historical, _ := BlockMapping(776)
		for _, entry := range entries {
			if entry.B < 0 || entry.B > int64(^uint32(0)>>1) {
				return fmt.Errorf("world: invalid historical776 block ID %d", entry.B)
			}
			historicalID, ok := historical.FromWire(int32(entry.B))
			if !ok {
				return fmt.Errorf("world: unknown historical776 block ID %d", entry.B)
			}
			key, _ := historical.Catalog().State(historicalID)
			id, ok := catalog.Lookup(key)
			if !ok {
				return fmt.Errorf("world: historical block %s absent from current catalog", key)
			}
			states[Pos{entry.X, entry.Y, entry.Z}] = id
		}
	default:
		return fmt.Errorf("world: save version %d unsupported (want 1 or %d)", header.Version, persistVersion)
	}
	w.Restore(states)
	return nil
}
