package world

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/nbt"
)

func TestRegionCodecByNameMatchesMeasuredOptions(t *testing.T) {
	// Measured: RegionFileVersion.configure resolves the exact option
	// names deflate, none and lz4; anything else keeps the default.
	for name, want := range map[string]RegionCodec{
		"deflate": RegionDeflate,
		"none":    RegionNone,
		"lz4":     RegionLZ4,
	} {
		got, ok := RegionCodecByName(name)
		if !ok || got != want {
			t.Fatalf("name %q -> %d,%v", name, got, ok)
		}
	}
	for _, bad := range []string{"", "DEFLATE", "zlib", "gzip", "custom", "lz4 "} {
		if got, ok := RegionCodecByName(bad); ok || got != RegionDeflate {
			t.Fatalf("invalid name %q -> %d,%v, want deflate default", bad, got, ok)
		}
	}
	if RegionDeflate.versionByte() != compressZlib ||
		RegionNone.versionByte() != compressNone ||
		RegionLZ4.versionByte() != compressLZ4 {
		t.Fatal("codec version bytes do not match the measured ids")
	}
}

func TestCompressRecordPayloadRoundTrips(t *testing.T) {
	// Every codec's payload decodes back through the record reader's
	// own decoders: identity for none, the stream reader for lz4,
	// zlib inflate for deflate.
	raw := javaPattern(150000, 11)

	none, err := RegionNone.compressRecordPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(none, raw) {
		t.Fatal("none codec altered the payload")
	}

	lz4, err := RegionLZ4.compressRecordPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeLZ4BlockStream(lz4)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("lz4 codec round trip mismatch")
	}

	deflate, err := RegionDeflate.compressRecordPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(deflate))
	if err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("deflate codec round trip mismatch")
	}
}

func TestEncodeRegionExternalThresholdBoundary(t *testing.T) {
	// Measured: a record of 256 or more sectors moves out of the
	// region file - the in-file record is the 5-byte stub (length 1,
	// version byte with the external flag), the payload goes to the
	// side file - while 255 sectors stays in-file. The stub occupies
	// exactly one sector.
	at255 := make([]byte, 255*4096-5)
	data, externals := encodeRegion(map[chunkKey][]byte{{0, 0}: at255}, compressZlib)
	if len(externals) != 0 {
		t.Fatal("255-sector record stored externally")
	}
	if loc := binary.BigEndian.Uint32(data[0:]); loc>>8 < 2 || loc&0xFF != 255 {
		t.Fatalf("255-sector location %#x", loc)
	}

	at256 := make([]byte, 255*4096-4)
	data, externals = encodeRegion(map[chunkKey][]byte{{0, 0}: at256}, compressZlib)
	if len(externals) != 1 {
		t.Fatalf("256-sector record kept in-file with %d externals", len(externals))
	}
	if !bytes.Equal(externals[chunkKey{0, 0}], at256) {
		t.Fatal("external payload not returned for the side file")
	}
	loc := binary.BigEndian.Uint32(data[0:])
	if loc&0xFF != 1 {
		t.Fatalf("stub location %#x, want one sector", loc)
	}
	start := int(loc>>8) * 4096
	stub := data[start : start+5]
	if binary.BigEndian.Uint32(stub) != 1 {
		t.Fatalf("stub length %d, want 1", binary.BigEndian.Uint32(stub))
	}
	if stub[4] != compressZlib|0x80 {
		t.Fatalf("stub version byte %#x, want deflate with external flag", stub[4])
	}
}

func TestWriteExternalChunksReclaimsStaleSideFiles(t *testing.T) {
	// Measured: every in-file write deletes the chunk's .mcc first,
	// so a region save leaves side files only for records that are
	// still external - and never touches another region's files.
	dir := t.TempDir()
	keep := chunkKey{1, 0}
	stale := chunkKey{0, 0}
	if err := writeExternalChunks(dir, 0, 0, map[chunkKey][]byte{
		stale: []byte("old"), keep: []byte("kept"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeExternalChunks(dir, 0, 0, map[chunkKey][]byte{keep: []byte("kept2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(externalChunkPath(dir, stale.X, stale.Z)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale side file survived: %v", err)
	}
	body, err := os.ReadFile(externalChunkPath(dir, keep.X, keep.Z))
	if err != nil || !bytes.Equal(body, []byte("kept2")) {
		t.Fatalf("kept side file not rewritten: %q %v", body, err)
	}
}

func bigEntityField(total int) map[string]any {
	// Two byte arrays rather than one: the NBT reader caps a single
	// string/array at 1 MiB, and an oversized record needs more than
	// that once compressed random data barely shrinks.
	half := total / 2
	return map[string]any{
		"id":    "minecraft:pig",
		"Blob1": javaPattern(half, 2),
		"Blob2": javaPattern(half, 3),
	}
}

func TestMergeEntityRegionsExternalRecordRoundTrip(t *testing.T) {
	// An oversized entity record merges out to a stub plus side file,
	// loads back through the reader's external-record path, and a
	// later small merge reclaims the side file.
	dir := t.TempDir()
	if err := MergeEntityRegions(dir, RegionDeflate, map[[2]int32][]map[string]any{
		{0, 0}: {bigEntityField(1100000)},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "entities", "r.0.0.mca")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := storedRegionRecords(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := records[0]; !ok || len(rec) != 1 || rec[0] != compressZlib|0x80 {
		t.Fatalf("slot 0 record %#x (present %v), want external stub", rec, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, "entities", "c.0.0.mcc")); err != nil {
		t.Fatalf("side file missing after oversized merge: %v", err)
	}
	chunks, err := LoadEntityRegionChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if blob, ok := chunks[[2]int32{0, 0}][0]["Blob1"].([]byte); !ok || len(blob) != 550000 {
		t.Fatalf("external record did not load back: %T", chunks[[2]int32{0, 0}][0]["Blob1"])
	}

	// Shrinking the record back in-file must remove the side file.
	if err := MergeEntityRegions(dir, RegionDeflate, map[[2]int32][]map[string]any{
		{0, 0}: {map[string]any{"id": "minecraft:pig", "sentinel": "small"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entities", "c.0.0.mcc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("side file survived an in-file merge: %v", err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records, _, err = storedRegionRecords(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec := records[0]; len(rec) < 2 || rec[0] != compressZlib {
		t.Fatalf("shrunk record %#x, want in-file deflate", rec)
	}
	chunks, err = LoadEntityRegionChunks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := chunks[[2]int32{0, 0}][0]["sentinel"].(string); !ok || s != "small" {
		t.Fatal("shrunk record did not load back")
	}
}

func TestMergeEntityRegionsDeletionReclaimsSideFile(t *testing.T) {
	// Measured: clear(ChunkPos) also deletes the external payload, so
	// a deleted entity record cannot leave its side file behind.
	dir := t.TempDir()
	if err := MergeEntityRegions(dir, RegionLZ4, map[[2]int32][]map[string]any{
		{0, 0}: {bigEntityField(1100000)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entities", "c.0.0.mcc")); err != nil {
		t.Fatal(err)
	}
	if err := MergeEntityRegions(dir, RegionLZ4, map[[2]int32][]map[string]any{{0, 0}: nil}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entities", "c.0.0.mcc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted record left its side file: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "entities", "r.0.0.mca"))
	if err != nil {
		t.Fatal(err)
	}
	if loc := binary.BigEndian.Uint32(raw[0:]); loc != 0 {
		t.Fatalf("deleted record still located at %#x", loc)
	}
}

func TestWriteMergedExternalRecordRoundTrip(t *testing.T) {
	// The chunk-merge writer takes the same external path: an
	// oversized block entity drives the record past the threshold,
	// and shrinking it reclaims the side file.
	provider := &subsetCaptureProvider{}
	w := NewProvided(nil, 1, provider)
	t.Cleanup(w.Stop)
	dir := t.TempDir()
	big := []map[string]any{{"id": "minecraft:chest", "x": int32(1), "y": int32(1), "z": int32(1),
		"Blob1": javaPattern(550000, 5), "Blob2": javaPattern(550000, 6)}}
	s := w.CaptureDimensionChunksWithTicks(0, 16, func(cx, cz int) []map[string]any { return big }, nil,
		map[[2]int32]SavedChunkTicks{{0, 0}: {}})
	s.SetRegionCompression(RegionDeflate)
	if err := s.WriteMerged(dir, "minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
	regionPath := filepath.Join(dir, "dimensions", "minecraft", "overworld", "region", "r.0.0.mca")
	raw, err := os.ReadFile(regionPath)
	if err != nil {
		t.Fatal(err)
	}
	records, _, err := storedRegionRecords(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec, ok := records[0]; !ok || len(rec) != 1 || rec[0] != compressZlib|0x80 {
		t.Fatalf("slot 0 record %#x (present %v), want external stub", rec, ok)
	}
	side, err := os.ReadFile(filepath.Join(dir, "dimensions", "minecraft", "overworld", "region", "c.0.0.mcc"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(side))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	_, chunk, err := nbt.ReadNamed(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if blob := chunk.(map[string]any)["block_entities"].(nbt.List).Items[0].(map[string]any)["Blob1"].([]byte); len(blob) != 550000 {
		t.Fatalf("side payload carries %d-byte blob", len(blob))
	}

	// A small merge rewrites the record in-file and reclaims the file.
	small := []map[string]any{{"id": "minecraft:chest", "x": int32(1), "y": int32(1), "z": int32(1)}}
	s2 := w.CaptureDimensionChunksWithTicks(0, 16, func(cx, cz int) []map[string]any { return small }, nil,
		map[[2]int32]SavedChunkTicks{{0, 0}: {}})
	s2.SetRegionCompression(RegionDeflate)
	if err := s2.WriteMerged(dir, "minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dimensions", "minecraft", "overworld", "region", "c.0.0.mcc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("side file survived an in-file merge: %v", err)
	}
	raw, err = os.ReadFile(regionPath)
	if err != nil {
		t.Fatal(err)
	}
	records, _, err = storedRegionRecords(raw)
	if err != nil {
		t.Fatal(err)
	}
	if rec := records[0]; len(rec) < 2 || rec[0] != compressZlib {
		t.Fatalf("shrunk record %#x, want in-file deflate", rec)
	}
}
