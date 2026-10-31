package world

import (
	"bytes"
	"mc-go/internal/nbt"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeEntityRegionsDeletionAndRawNeighbor(t *testing.T) {
	dir := t.TempDir()
	row := func(marker string) map[string]any { return map[string]any{"id": "minecraft:arrow", "marker": marker} }
	if err := SaveEntityRegions(dir, map[[2]int32][]map[string]any{{0, 0}: {row("delete")}, {1, 0}: {row("opaque")}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "entities", "r.0.0.mca")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old, stamp, err := storedRegionRecords(before)
	if err != nil {
		t.Fatal(err)
	}
	if err := MergeEntityRegions(dir, map[[2]int32][]map[string]any{{0, 0}: nil, {32, 0}: {row("moved")}}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records, timestamps, err := storedRegionRecords(after)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := records[0]; ok {
		t.Fatal("deleted record survived")
	}
	if !bytes.Equal(old[1], records[1]) || !bytes.Equal(stamp[4:8], timestamps[4:8]) {
		t.Fatal("unselected record payload/timestamp changed")
	}
	chunks, err := LoadEntityRegionChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 || chunks[[2]int32{32, 0}][0]["marker"] != "moved" {
		t.Fatalf("rows %#v", chunks)
	}
}

func TestLoadEntityRegionRejectsPartialAuthority(t *testing.T) {
	for _, mode := range []string{"missing-list", "wrong-list", "invalid-location", "truncated-length"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "entities", "r.0.0.mca")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			root := map[string]any{"Position": []int32{0, 0}, "Entities": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "minecraft:pig"}}}}
			if mode == "missing-list" {
				delete(root, "Entities")
			}
			if mode == "wrong-list" {
				root["Entities"] = nbt.List{Elem: nbt.TagString, Items: []any{"bad"}}
			}
			var buf bytes.Buffer
			if err := nbt.WriteNamed(&buf, "", root); err != nil {
				t.Fatal(err)
			}
			raw, err := encodeStoredRegion(map[int][]byte{0: append([]byte{compressNone}, buf.Bytes()...)}, make([]byte, 4096))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "invalid-location" {
				raw[2] = 1
				raw[3] = 0
			}
			if mode == "truncated-length" {
				raw = raw[:len(raw)-1]
			}
			if err := os.WriteFile(path, raw, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadEntityRegionChunks(dir); err == nil {
				t.Fatal("accepted partial authority")
			}
		})
	}
}

func TestLoadEntityRegionPhysicalSourceAuthority(t *testing.T) {
	for _, name := range []string{"r.foo.0.mca", "r.0.bad.mca", "r.67108864.0.mca", "r.00.0.mca"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "entities", name)
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, make([]byte, 8192), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadEntityRegionChunks(dir); err == nil {
				t.Fatal("malformed filename accepted")
			}
		})
	}
	dir := t.TempDir()
	// Deliberately discordant saved Position/Pos: the source is still the
	// physical slot (negative region, local chunk31), not an entity claim.
	payload, err := encodeEntityChunk([2]int32{123, 456}, []map[string]any{{"id": "minecraft:pig"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := encodeStoredRegion(map[int][]byte{31: append([]byte{compressZlib}, payload...)}, make([]byte, 4096))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "entities", "r.-1.0.mca")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	chunks, err := LoadEntityRegionChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || len(chunks[[2]int32{-1, 0}]) != 1 {
		t.Fatalf("physical ownership %#v", chunks)
	}
}
