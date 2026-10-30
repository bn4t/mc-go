package world

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/nbt"
)

type subsetCaptureProvider struct{ calls [][2]int }

func (p *subsetCaptureProvider) ProvideChunk(cx, cz int) *BaseChunk {
	p.calls = append(p.calls, [2]int{cx, cz})
	return &BaseChunk{MinY: 0, Height: 16, Sections: [][]Block{make([]Block, 4096)}}
}

func TestDimensionSubsetCaptureTouchesOnlySelectedColumns(t *testing.T) {
	provider := &subsetCaptureProvider{}
	w := NewProvided(nil, 2, provider)
	t.Cleanup(w.Stop)
	w.BaseChunk(5, 5)
	called := 0
	longs := []int64{7, 11, 13}
	entity := map[string]any{"id": "minecraft:chest", "x": int32(1), "y": int32(1), "z": int32(1), "opaque": map[string]any{"longs": longs}}
	records := []SavedTick{{Type: "minecraft:stone_button", Pos: Pos{1, 1, 1}, Delay: 17, Priority: -3}}
	selection := map[[2]int32]SavedChunkTicks{{0, 0}: {Blocks: records}}
	s := w.CaptureDimensionChunksWithTicks(0, 16, func(cx, cz int) []map[string]any {
		called++
		if cx != 0 || cz != 0 {
			t.Fatalf("unselected callback %d,%d", cx, cz)
		}
		return []map[string]any{entity}
	}, nil, selection)
	if s.err != nil || len(s.chunks) != 1 || len(s.regions) != 1 || called != 1 || len(provider.calls) != 2 || provider.calls[1] != [2]int{0, 0} {
		t.Fatalf("selection capture: chunks%d calls%v err%v", len(s.chunks), provider.calls, s.err)
	}
	longs[0] = 99
	records[0].Delay = 99
	entity["id"] = "mutated"
	w.Stop()
	chunk := s.chunks[chunkKey{}]
	if chunk.ticks.Blocks[0].Delay != 17 || chunk.entities[0]["id"] != "minecraft:chest" || chunk.entities[0]["opaque"].(map[string]any)["longs"].([]int64)[0] != 7 {
		t.Fatal("subset retained callback/tick aliases")
	}
	if err := s.WriteMerged(t.TempDir(), "minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
}

func TestDimensionSubsetEmptySelectionDoesNotMaterializeOrWrite(t *testing.T) {
	provider := &subsetCaptureProvider{}
	w := NewProvided(nil, 1, provider)
	t.Cleanup(w.Stop)
	s := w.CaptureDimensionChunksWithTicks(0, 16, func(int, int) []map[string]any { t.Fatal("empty callback"); return nil }, nil, nil)
	dir := t.TempDir()
	if err := s.WriteMerged(dir, "minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 || len(provider.calls) != 0 {
		t.Fatal("empty selection touched storage/provider")
	}
}

func subsetTestRecord(t *testing.T, raw []byte, slot int) []byte {
	t.Helper()
	location := binary.BigEndian.Uint32(raw[slot*4:])
	start := int(location>>8) * 4096
	length := int(binary.BigEndian.Uint32(raw[start:]))
	return append([]byte(nil), raw[start+4:start+4+length]...)
}

func subsetTestAppendRecord(raw []byte, slot int, record []byte) []byte {
	start := (len(raw) + 4095) / 4096 * 4096
	sectors := (len(record) + 4 + 4095) / 4096
	raw = append(raw, make([]byte, start+sectors*4096-len(raw))...)
	binary.BigEndian.PutUint32(raw[slot*4:], uint32(start/4096<<8|sectors))
	binary.BigEndian.PutUint32(raw[start:], uint32(len(record)))
	copy(raw[start+4:], record)
	return raw
}

func subsetTestFrame(t *testing.T, kind byte, compound map[string]any) []byte {
	t.Helper()
	if kind == 0x82 {
		return []byte{kind}
	}
	var plain bytes.Buffer
	if err := nbt.WriteNamed(&plain, "", compound); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	switch kind {
	case compressNone:
		return append([]byte{kind}, plain.Bytes()...)
	case compressGzip:
		zw := gzip.NewWriter(&compressed)
		if _, err := zw.Write(plain.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	case compressZlib:
		zw := zlib.NewWriter(&compressed)
		if _, err := zw.Write(plain.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return append([]byte{kind}, compressed.Bytes()...)
}

func subsetTestSelectedCompound(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	record := subsetTestRecord(t, raw, 0)
	if record[0] != compressZlib {
		t.Fatal("selected compression")
	}
	zr, err := zlib.NewReader(bytes.NewReader(record[1:]))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	_, root, err := nbt.ReadNamed(zr)
	if err != nil {
		t.Fatal(err)
	}
	return root.(map[string]any)
}

func TestMergedRegionPreservesUnselectedRecordsAndTimestamps(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
	}{{"zlib", compressZlib}, {"gzip", compressGzip}, {"none", compressNone}, {"external", 0x82}} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewGen(nil, 2, func(Pos) Block { return Air })
			t.Cleanup(w.Stop)
			a, b := Pos{1, 1, 1}, Pos{17, 1, 1}
			w.SetBlock(a, Stone)
			w.SetBlock(b, Grass)
			dir := t.TempDir()
			dim := "minecraft:overworld"
			initial := map[[2]int32]SavedChunkTicks{{0, 0}: {Blocks: []SavedTick{{Type: "minecraft:stone_button", Pos: a, Delay: 17}}}, {1, 0}: {Blocks: []SavedTick{{Type: "minecraft:dirt", Pos: b, Delay: 23}}}}
			if err := w.CaptureDimensionSaveWithTicks(0, 16, nil, nil, initial).Write(dir, dim); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(DimensionDir(dir, dim), "region", "r.0.0.mca")
			decoded, err := ReadRegionFile(path)
			if err != nil {
				t.Fatal(err)
			}
			foreign := decoded[[2]int32{1, 0}]
			foreign["OpaqueUnknown"] = map[string]any{"text": "keep", "longs": []int64{7, 11, 13}}
			frame := subsetTestFrame(t, tc.kind, foreign)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw = subsetTestAppendRecord(raw, 1, frame)
			binary.BigEndian.PutUint32(raw[4096:], 0x01020304)
			binary.BigEndian.PutUint32(raw[4100:], 0x12345678)
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			external := filepath.Join(filepath.Dir(path), "c.1.0.mcc")
			if tc.kind == 0x82 {
				if err := os.WriteFile(external, []byte("opaque external payload"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			w.SetBlock(a, Dirt)
			w.SetBlock(b, Stone)
			selected := w.CaptureDimensionChunksWithTicks(0, 16, nil, nil, map[[2]int32]SavedChunkTicks{{0, 0}: {}})
			if len(selected.chunks) != 1 {
				t.Fatal("subset included unrelated overrides")
			}
			w.SetBlock(a, Grass)
			w.Stop()
			firstSize := 0
			for i := 0; i < 5; i++ {
				if err := selected.WriteMerged(dir, dim); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(subsetTestRecord(t, after, 1), frame) || binary.BigEndian.Uint32(after[4100:]) != 0x12345678 {
					t.Fatal("untouched record/timestamp changed")
				}
				if binary.BigEndian.Uint32(after[4096:]) == 0x01020304 {
					t.Fatal("selected timestamp not updated")
				}
				if firstSize == 0 {
					firstSize = len(after)
				} else if firstSize != len(after) {
					t.Fatal("repeated writes accumulated unreachable sectors")
				}
				chunk := subsetTestSelectedCompound(t, after)
				if ticks := chunk["block_ticks"].(nbt.List); len(ticks.Items) != 0 {
					t.Fatal("explicit empty tick selection retained stale records")
				}
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("existing region mode changed")
			}
			if tc.kind == 0x82 {
				data, err := os.ReadFile(external)
				if err != nil || string(data) != "opaque external payload" {
					t.Fatal("external payload touched")
				}
			}
			if tc.kind == compressZlib || tc.kind == compressNone {
				verify := NewGen(nil, 1, func(Pos) Block { return Air })
				defer verify.Stop()
				if err := verify.LoadDimension(dir, dim, 0, 16); err != nil {
					t.Fatal(err)
				}
				if verify.GetBlock(a) != Dirt || verify.GetBlock(b) != Grass {
					t.Fatal("selected snapshot or unselected saved blocks changed")
				}
				if rows := verify.SavedChunkTicks()[[2]int32{1, 0}].Blocks; len(rows) != 1 || rows[0].Delay != 23 {
					t.Fatal("unselected ticks changed")
				}
				all, err := ReadRegionFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if all[[2]int32{1, 0}]["OpaqueUnknown"].(map[string]any)["text"] != "keep" {
					t.Fatal("unknown unselected NBT lost")
				}
			}
		})
	}
}

func TestMergedRegionRejectsInvalidFramingWithoutReplacing(t *testing.T) {
	w := NewGen(nil, 1, func(Pos) Block { return Air })
	defer w.Stop()
	w.SetBlock(Pos{}, Stone)
	s := w.CaptureDimensionChunksWithTicks(0, 16, nil, nil, map[[2]int32]SavedChunkTicks{{0, 0}: {}})
	dir := t.TempDir()
	path := filepath.Join(DimensionDir(dir, "minecraft:overworld"), "region", "r.0.0.mca")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	before := []byte("incomplete prior region")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteMerged(dir, "minecraft:overworld"); err == nil {
		t.Fatal("invalid framing accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("invalid source was replaced")
	}
}

func TestMergedRegionOversizeSelectionReturnsErrorWithoutReplacing(t *testing.T) {
	w := NewGen(nil, 1, func(Pos) Block { return Air })
	defer w.Stop()
	w.SetBlock(Pos{}, Stone)
	dir := t.TempDir()
	dim := "minecraft:overworld"
	if err := w.CaptureDimensionSave(0, 16, nil, nil).Write(dir, dim); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(DimensionDir(dir, dim), "region", "r.0.0.mca")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 1100000)
	_, _ = rand.New(rand.NewSource(1)).Read(payload)
	s := w.CaptureDimensionChunksWithTicks(0, 16, func(int, int) []map[string]any { return []map[string]any{{"opaque": payload}} }, nil, map[[2]int32]SavedChunkTicks{{0, 0}: {}})
	if err := s.WriteMerged(dir, dim); err == nil {
		t.Fatal("oversize selected chunk silently omitted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed oversize update replaced prior region")
	}
}
