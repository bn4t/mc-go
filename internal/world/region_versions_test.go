package world

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mc-go/internal/nbt"
)

// regionTestFrame wraps a compound under a record version byte.
func regionTestFrame(t *testing.T, ver byte, compound map[string]any) []byte {
	t.Helper()
	var plain bytes.Buffer
	if err := nbt.WriteNamed(&plain, "", compound); err != nil {
		t.Fatal(err)
	}
	switch ver & 0x7F {
	case compressZlib:
		var c bytes.Buffer
		zw := zlib.NewWriter(&c)
		zw.Write(plain.Bytes())
		zw.Close()
		return c.Bytes()
	case compressGzip:
		var c bytes.Buffer
		gw := gzip.NewWriter(&c)
		gw.Write(plain.Bytes())
		gw.Close()
		return c.Bytes()
	case compressNone:
		return plain.Bytes()
	case compressLZ4:
		return lz4TestStream(lz4TestBlock(0x10, 16, plain.Bytes(), plain.Bytes(),
			xxHash32(plain.Bytes(), 0x9747b28c)&0x0FFFFFFF))
	default:
		t.Fatalf("unsupported frame version %d", ver)
		return nil
	}
}

// regionTestFile lays out records - slot to (version byte, payload) -
// one sector each, and returns the .mca path. A payload of nil with an
// external version writes only the stub and no side file.
func regionTestFile(t *testing.T, dir string, records map[int][]byte, versions map[int]byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "region"), 0o755); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 8192)
	var body bytes.Buffer
	next := 2
	for slot := 0; slot < 1024; slot++ {
		ver, hasVersion := versions[slot]
		payload, ok := records[slot]
		if !ok && !hasVersion {
			continue
		}
		if ver&0x80 != 0 && payload != nil {
			if err := os.WriteFile(filepath.Join(dir, "region",
				fmt.Sprintf("c.%d.%d.mcc", slot%32, slot/32)), payload, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		rec := make([]byte, 4096)
		binary.BigEndian.PutUint32(rec, uint32(1+len(payload)))
		rec[4] = ver
		copy(rec[5:], payload)
		binary.BigEndian.PutUint32(header[slot*4:], uint32(next<<8|1))
		binary.BigEndian.PutUint32(header[4096+slot*4:], 1)
		body.Write(rec)
		next++
	}
	path := filepath.Join(dir, "region", "r.0.0.mca")
	if err := os.WriteFile(path, append(header, body.Bytes()...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func regionTestCompound(slot int) map[string]any {
	return map[string]any{"Status": "minecraft:full", "Slot": int32(slot)}
}

func TestReadRegionFileAllRecordVersions(t *testing.T) {
	dir := t.TempDir()
	frames := map[int]byte{
		0: compressZlib,
		1: compressGzip,
		2: compressNone,
		3: compressLZ4,
		4: 0x80 | compressZlib, // external zlib
		5: 0x80 | compressLZ4, // external lz4
	}
	records := map[int][]byte{}
	for slot, ver := range frames {
		records[slot] = regionTestFrame(t, ver, regionTestCompound(slot))
	}
	path := regionTestFile(t, dir, records, frames)
	chunks, err := ReadRegionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 6 {
		t.Fatalf("decoded %d chunks, want 6", len(chunks))
	}
	for slot := 0; slot < 6; slot++ {
		k := [2]int32{int32(slot), 0}
		c, ok := chunks[k]
		if !ok {
			t.Errorf("chunk %v missing", k)
			continue
		}
		if c["Status"] != "minecraft:full" || c["Slot"] != int32(slot) {
			t.Errorf("chunk %v: slot %v", k, c["Slot"])
		}
	}
}

func TestReadRegionFileSkipsUnreadableRecords(t *testing.T) {
	// Every unreadable record is a chunk that is not there, never a
	// load error for the file: an unknown version, the custom codec id
	// the real server never registers, an external stub whose side
	// file is missing, an allocated-but-missing stream, a declared
	// size running backwards, a payload past its sector allocation,
	// and a payload that is not NBT. The zlib control survives.
	dir := t.TempDir()
	junk := func() []byte {
		var c bytes.Buffer
		zw := zlib.NewWriter(&c)
		zw.Write([]byte("not nbt at all"))
		zw.Close()
		return c.Bytes()
	}()
	versions := map[int]byte{
		0: compressZlib,             // control
		1: 7,                        // unknown version
		2: 127,                      // custom, never registered
		3: 0x80 | compressZlib,      // external without its .mcc
		4: compressZlib,             // length zero: allocated, missing
		5: compressZlib,             // declared size negative
		6: compressZlib,             // payload past its sector count
		7: compressZlib,             // payload is not NBT
	}
	records := map[int][]byte{
		0: regionTestFrame(t, compressZlib, regionTestCompound(0)),
		1: regionTestFrame(t, compressZlib, regionTestCompound(1)),
		2: regionTestFrame(t, compressZlib, regionTestCompound(2)),
		4: junk,
		5: junk,
		6: junk,
		7: junk,
	}
	path := regionTestFile(t, dir, records, versions)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Slot 4: length zero. Slot 5: length runs backwards. Slot 6:
	// declared payload of two sectors in a one-sector allocation.
	for slot, length := range map[int]uint32{4: 0, 5: 0xFFFFFF80, 6: 8193} {
		loc := binary.BigEndian.Uint32(raw[slot*4:])
		start := int(loc>>8) * 4096
		binary.BigEndian.PutUint32(raw[start:], length)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	chunks, err := ReadRegionFile(path)
	if err != nil {
		t.Fatalf("unreadable records must not fail the file: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("want only the control chunk, got %d: %v", len(chunks), chunks)
	}
	if c := chunks[[2]int32{0, 0}]; c == nil || c["Slot"] != int32(0) {
		t.Fatalf("control chunk damaged: %v", c)
	}
}
