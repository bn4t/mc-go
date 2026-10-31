package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeEntityRegionKnownRemovalPreservesOpaqueRecords(t *testing.T) {
	for _, mode := range []string{"last_known", "mixed_remove", "mixed_keep", "mixed_move"} {
		t.Run(mode, func(t *testing.T) {
			seed := nativeSaveTestServer(t)
			if err := seed.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			knownUUID := []int32{1, 2, 3, 4}
			opaqueUUID := []int32{11, 22, 33, 44}
			neighborUUID := []int32{111, 222, 333, 444}
			vector := func(x float64) nbt.List {
				return nbt.List{Elem: nbt.TagDouble, Items: []any{x, float64(0), float64(1)}}
			}
			known := map[string]any{"id": "minecraft:item", "UUID": knownUUID, "Pos": vector(1), "Item": map[string]any{"id": "minecraft:stone", "count": int32(1)}, "Health": int16(5), "Age": int16(0), "PickupDelay": int16(32767)}
			opaque := map[string]any{"id": "minecraft:arrow", "UUID": opaqueUUID, "Pos": vector(3), "NoGravity": int8(1), "life": int16(93), "inGround": int8(1), "opaque_sentinel": map[string]any{"payload": []byte{3, 1, 4}, "text": "001"}}
			neighbor := map[string]any{"id": "minecraft:arrow", "UUID": neighborUUID, "Pos": vector(513), "opaque_sentinel": "UNTOUCHED_REGION"}
			first := []map[string]any{known}
			if mode != "last_known" {
				first = append(first, opaque)
			}
			dimDir := world.DimensionDir(seed.WorldDir, dimOverworld)
			if err := world.SaveEntityRegions(dimDir, map[[2]int32][]map[string]any{{0, 0}: first, {32, 0}: {neighbor}}); err != nil {
				t.Fatal(err)
			}
			neighborPath := filepath.Join(dimDir, "entities", "r.1.0.mca")
			beforeNeighbor, err := os.ReadFile(neighborPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeRows, err := world.LoadEntityRegions(dimDir)
			if err != nil {
				t.Fatal(err)
			}
			var beforeOpaque map[string]any
			for _, row := range beforeRows {
				if reflect.DeepEqual(row["UUID"], opaqueUUID) {
					beforeOpaque = row
				}
			}
			loaded := nativeSaveTestServer(t)
			loaded.WorldDir = seed.WorldDir
			if err := loaded.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			var knownEntity *Entity
			opaqueLoaded := 0
			for _, copy := range loaded.entities.All() {
				e, ok := loaded.entities.GetPtr(copy.ID)
				if !ok {
					continue
				}
				saved := entitySaveMap(e, loaded.clock.count, loaded.entities).m
				if reflect.DeepEqual(saved["UUID"], knownUUID) {
					knownEntity = e
				}
				if reflect.DeepEqual(saved["UUID"], opaqueUUID) || reflect.DeepEqual(saved["UUID"], neighborUUID) {
					opaqueLoaded++
				}
			}
			if knownEntity == nil {
				t.Fatal("known item did not load")
			}
			captured := loaded.captureEntityRegionsOwned(loaded.dims[dimOverworld])
			t.Logf("OWNERSHIP managedOpaque=%d capturedGroups=%d", opaqueLoaded, len(captured))
			if mode == "last_known" || mode == "mixed_remove" {
				loaded.entities.Remove(knownEntity.ID)
			}
			if mode == "mixed_move" {
				knownEntity.X = 1025
			}
			if err := loaded.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			afterRows, err := world.LoadEntityRegions(dimDir)
			if err != nil {
				t.Fatal(err)
			}
			knownCount := 0
			var afterOpaque map[string]any
			for _, row := range afterRows {
				if reflect.DeepEqual(row["UUID"], knownUUID) {
					knownCount++
				}
				if reflect.DeepEqual(row["UUID"], opaqueUUID) {
					afterOpaque = row
				}
			}
			wantKnown := 1
			if mode == "last_known" || mode == "mixed_remove" {
				wantKnown = 0
			}
			if knownCount != wantKnown {
				t.Errorf("known rows after full save/reload=%d want%d", knownCount, wantKnown)
			}
			if mode != "last_known" && !reflect.DeepEqual(beforeOpaque, afterOpaque) {
				t.Errorf("opaque same-chunk state lost: before%#v after%#v", beforeOpaque, afterOpaque)
			}
			afterNeighbor, err := os.ReadFile(neighborPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(beforeNeighbor, afterNeighbor) {
				t.Error("untouched neighboring entity region bytes changed")
			}
			restarted := nativeSaveTestServer(t)
			restarted.WorldDir = seed.WorldDir
			if err := restarted.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			actual := 0
			for _, e := range restarted.entities.All() {
				saved := entitySaveMap(&e, restarted.clock.count, restarted.entities).m
				if reflect.DeepEqual(saved["UUID"], knownUUID) {
					actual++
				}
			}
			if actual != wantKnown {
				t.Errorf("known entities after restart=%d want%d", actual, wantKnown)
			}
		})
	}
}

// An unsupported descendant makes the entire composite opaque; supported
// neighbors remain mutable, including when their deletion selects this slot.
func TestNativeEntityOpaquePassengerComposite(t *testing.T) {
	for _, rootName := range []string{"zombie", "item", "command_block_minecart", "area_effect_cloud", "experience_orb", "marker"} {
		t.Run(rootName, func(t *testing.T) {
			seed := nativeSaveTestServer(t)
			if err := seed.SaveWorld(); err != nil {
				t.Fatal(err)
			}
			pos := nbt.List{Elem: nbt.TagDouble, Items: []any{float64(1), float64(0), float64(1)}}
			opaque := map[string]any{"id": "minecraft:" + rootName, "UUID": []int32{1, 2, 3, 4}, "Pos": pos, "Item": map[string]any{"id": "minecraft:stone", "count": int32(1)}, "Passengers": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "minecraft:pig", "Pos": pos, "Passengers": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "minecraft:arrow", "sentinel": []byte{2, 7, 1}}}}}}}}
			if rootName == "area_effect_cloud" || rootName == "experience_orb" || rootName == "marker" {
				delete(opaque, "Passengers")
			}
			neighbor := map[string]any{"id": "minecraft:item", "UUID": []int32{11, 22, 33, 44}, "Pos": pos, "Item": map[string]any{"id": "minecraft:stone", "count": int32(1)}}
			dir := world.DimensionDir(seed.WorldDir, dimOverworld)
			if err := world.SaveEntityRegions(dir, map[[2]int32][]map[string]any{{0, 0}: {opaque, neighbor}}); err != nil {
				t.Fatal(err)
			}
			before, err := world.LoadEntityRegions(dir)
			if err != nil {
				t.Fatal(err)
			}
			var original map[string]any
			for _, row := range before {
				if reflect.DeepEqual(row["UUID"], []int32{1, 2, 3, 4}) {
					original = row
				}
			}
			for pass := 0; pass < 2; pass++ {
				loaded := nativeSaveTestServer(t)
				loaded.WorldDir = seed.WorldDir
				if err := loaded.LoadWorld(); err != nil {
					t.Fatal(err)
				}
				for _, e := range loaded.entities.All() {
					if name, _ := EntityTypeName(e.Type); name == rootName && rootName != "item" {
						t.Fatal("opaque root simulated")
					}
					if reflect.DeepEqual(entitySaveMap(&e, loaded.clock.count, loaded.entities).m["UUID"], []int32{1, 2, 3, 4}) {
						t.Fatal("opaque composite hydrated")
					}
					loaded.entities.Remove(e.ID)
				}
				if err := loaded.SaveWorld(); err != nil {
					t.Fatal(err)
				}
				rows, err := world.LoadEntityRegions(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != 1 || !reflect.DeepEqual(rows[0], original) {
					t.Fatalf("opaque composite changed after pass%d: %#v", pass, rows)
				}
			}
		})
	}
}

func TestNativeEntityOwnershipFailedReceiptRetry(t *testing.T) {
	s := nativeSaveTestServer(t)
	state := s.newNativeEntityRegionStateOwned(dimOverworld)
	state.observed = true
	old := [2]int32{0, 0}
	partial := [2]int32{32, 0}
	final := [2]int32{64, 0}
	state.owned[old] = true
	selection := s.selectNativeEntityRecordsOwned(dimOverworld, map[[2]int32][]map[string]any{partial: {{"id": "minecraft:item"}}})
	receipts := map[string]nativeEntityOwnership{dimOverworld: selection.ownership}
	s.beginNativeEntityWritesOwned(receipts)
	failed := &nativeWorldSaveJob{err: os.ErrPermission, entityOwnership: receipts}
	s.recordNativeSaveOwned(failed)
	if !state.owned[old] || state.owned[partial] {
		t.Fatal("failed receipt advanced ownership")
	}
	retry := s.selectNativeEntityRecordsOwned(dimOverworld, map[[2]int32][]map[string]any{final: {{"id": "minecraft:item"}}})
	for _, key := range [][2]int32{old, partial, final} {
		if _, ok := retry.rows[key]; !ok {
			t.Fatalf("retry omitted %v", key)
		}
	}
	success := &nativeWorldSaveJob{entityOwnership: map[string]nativeEntityOwnership{dimOverworld: retry.ownership}}
	s.recordNativeSaveOwned(success)
	if len(state.owned) != 1 || !state.owned[final] || len(state.unsettled) != 0 {
		t.Fatalf("successful receipt: %#v", state)
	}
	stale := s.newNativeEntityRegionStateOwned(dimOverworld)
	s.completeNativeEntityWritesOwned(success.entityOwnership)
	if len(stale.owned) != 0 {
		t.Fatal("stale receipt changed new incarnation")
	}
}

func TestNativeEntityFailedLoadBlocksSave(t *testing.T) {
	s := nativeSaveTestServer(t)
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(world.DimensionDir(s.WorldDir, dimOverworld), "entities")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "r.0.0.mca")
	corrupt := []byte("partial entity region")
	if err := os.WriteFile(path, corrupt, 0644); err != nil {
		t.Fatal(err)
	}
	loaded := nativeSaveTestServer(t)
	loaded.WorldDir = s.WorldDir
	if err := loaded.LoadWorld(); err == nil {
		t.Fatal("corrupt entity load accepted")
	}
	if err := loaded.SaveWorld(); err == nil {
		t.Fatal("save after corrupt entity load accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, corrupt) {
		t.Fatal("failed load data overwritten")
	}
}

func TestNativeEntityPartialWriteRetryRemovesIntermediateRegion(t *testing.T) {
	s := nativeSaveTestServer(t)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: dimOverworld, X: 1, Y: 0, Z: 1, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 1}})
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	e.X = 513
	selection := s.captureNativeEntitySelectionOwned(s.dims[dimOverworld])
	if selection.err != nil {
		t.Fatal(selection.err)
	}
	receipts := map[string]nativeEntityOwnership{dimOverworld: selection.ownership}
	s.beginNativeEntityWritesOwned(receipts)
	// The destination reaches disk, while a later region fails before the old
	// position tombstone is written. This is the nontransactional writer boundary.
	dir := world.DimensionDir(s.WorldDir, dimOverworld)
	if err := world.MergeEntityRegions(dir, map[[2]int32][]map[string]any{{32, 0}: selection.rows[[2]int32{32, 0}]}); err != nil {
		t.Fatal(err)
	}
	s.recordNativeSaveOwned(&nativeWorldSaveJob{err: os.ErrPermission, entityOwnership: receipts})
	e.X = 1025
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	chunks, err := world.LoadEntityRegionChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || len(chunks[[2]int32{64, 0}]) != 1 {
		t.Fatalf("retry left old/intermediate rows: %#v", chunks)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, item := range restarted.entities.All() {
		if name, _ := EntityTypeName(item.Type); name == "item" {
			count++
			if item.X != 1025 {
				t.Fatalf("restart X=%v", item.X)
			}
		}
	}
	if count != 1 {
		t.Fatalf("restart count=%d", count)
	}
}

func TestNativeEntityOpaqueSnapshotDetached(t *testing.T) {
	s := nativeSaveTestServer(t)
	state := s.newNativeEntityRegionStateOwned(dimOverworld)
	state.observed = true
	key := [2]int32{0, 0}
	state.owned[key] = true
	state.opaque[key] = []map[string]any{{"id": "minecraft:arrow", "sentinel": []byte{1, 2, 3}}}
	first := s.selectNativeEntityRecordsOwned(dimOverworld, map[[2]int32][]map[string]any{})
	first.rows[key][0]["sentinel"].([]byte)[0] = 9
	second := s.selectNativeEntityRecordsOwned(dimOverworld, map[[2]int32][]map[string]any{})
	if second.rows[key][0]["sentinel"].([]byte)[0] != 1 {
		t.Fatal("snapshot aliases private opaque state")
	}
}
