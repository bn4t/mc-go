package blackbox

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// Region record compression, checked in both directions like the other
// save-format suites (either server reading its own files proves
// nothing about the format):
//
//	A. the real server saves a small world; the region records are
//	   reframed by hand into the versions the real writer can produce
//	   but never mixes in one file - gzip, LZ4, and the 0x80 external
//	   stubs whose payloads live in c.<x>.<z>.mcc files beside the
//	   region, with vanilla zlib records left untouched as controls -
//	   and both servers must load the crafted world over the wire: the
//	   five marker blocks and both saved mobs.
//	B. the real server saves with region-file-compression=lz4; every
//	   record it writes is a version-4 LZ4 stream, and mc-go boots on
//	   that directory and serves the block and the mob over the wire.
//
// The probes are functional, not timing pins: a marker cell that comes
// back air or a mob that never spawns is what regeneration looks like,
// on either server.

// bbXXHash32 is the public xxHash32, duplicated here so this test can
// craft LZ4 records the real reader accepts. The three sanity vectors
// in bbCraftRegion are measured outputs of the xxhash library vanilla
// ships (three of the vectors pinned in internal/world/xxhash32_test.go),
// so this copy cannot silently drift from the one the reader uses: a
// wrong hash here makes the crafted records unreadable on both servers
// and the test fails loudly.
const (
	bbXXP1 uint32 = 2654435761
	bbXXP2 uint32 = 2246822519
	bbXXP3 uint32 = 3266489917
	bbXXP4 uint32 = 668265263
	bbXXP5 uint32 = 374761393
)

func bbXXRound(acc, in uint32) uint32 {
	acc += in * bbXXP2
	acc = bits.RotateLeft32(acc, 13)
	return acc * bbXXP1
}

func bbXXHash32(data []byte, seed uint32) uint32 {
	n := len(data)
	var h32 uint32
	i := 0
	if n >= 16 {
		acc1, acc2, acc3, acc4 := seed+bbXXP1+bbXXP2, seed+bbXXP2, seed, seed-bbXXP1
		for ; i+16 <= n; i += 16 {
			acc1 = bbXXRound(acc1, binary.LittleEndian.Uint32(data[i:]))
			acc2 = bbXXRound(acc2, binary.LittleEndian.Uint32(data[i+4:]))
			acc3 = bbXXRound(acc3, binary.LittleEndian.Uint32(data[i+8:]))
			acc4 = bbXXRound(acc4, binary.LittleEndian.Uint32(data[i+12:]))
		}
		h32 = bits.RotateLeft32(acc1, 1) + bits.RotateLeft32(acc2, 7) +
			bits.RotateLeft32(acc3, 12) + bits.RotateLeft32(acc4, 18)
	} else {
		h32 = seed + bbXXP5
	}
	h32 += uint32(n)
	for ; i+4 <= n; i += 4 {
		h32 += binary.LittleEndian.Uint32(data[i:]) * bbXXP3
		h32 = bits.RotateLeft32(h32, 17) * bbXXP4
	}
	for ; i < n; i++ {
		h32 += uint32(data[i]) * bbXXP5
		h32 = bits.RotateLeft32(h32, 11) * bbXXP1
	}
	h32 ^= h32 >> 15
	h32 *= bbXXP2
	h32 ^= h32 >> 13
	h32 *= bbXXP3
	h32 ^= h32 >> 16
	return h32
}

// bbLZ4Stream frames payload the way the real writer's fallback does:
// raw 64 KiB blocks (the method LZ4BlockOutputStream picks when LZ4
// cannot shrink the data) and the closing empty block, each checksummed
// with the 28-bit-masked xxHash32 under the stream's DEFAULT_SEED.
func bbLZ4Stream(payload []byte) []byte {
	var b bytes.Buffer
	for start := 0; start < len(payload); start += 65536 {
		end := start + 65536
		if end > len(payload) {
			end = len(payload)
		}
		block := payload[start:end]
		b.WriteString("LZ4Block")
		b.WriteByte(0x10 | 6) // raw method, 64 KiB bound
		var u [4]byte
		binary.LittleEndian.PutUint32(u[:], uint32(len(block)))
		b.Write(u[:])
		binary.LittleEndian.PutUint32(u[:], uint32(len(block)))
		b.Write(u[:])
		binary.LittleEndian.PutUint32(u[:], bbXXHash32(block, 0x9747b28c)&0x0FFFFFFF)
		b.Write(u[:])
		b.Write(block)
		if end == len(payload) {
			break
		}
	}
	// The closing empty block the real reader requires before it
	// reports end of stream.
	b.WriteString("LZ4Block")
	b.WriteByte(0x10 | 6)
	b.Write([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	return b.Bytes()
}

// bbGzipRecord and bbLZ4Record build in-file record bytes (version
// byte + payload) from an uncompressed NBT stream.
func bbGzipRecord(plain []byte) []byte {
	var c bytes.Buffer
	gw := gzip.NewWriter(&c)
	_, _ = gw.Write(plain)
	_ = gw.Close()
	return append([]byte{1}, c.Bytes()...)
}

func bbZlibPayload(plain []byte) []byte {
	var c bytes.Buffer
	zw := zlib.NewWriter(&c)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	return c.Bytes()
}

func bbLZ4Record(plain []byte) []byte {
	return append([]byte{4}, bbLZ4Stream(plain)...)
}

// bbExternalStub is the five-byte record the region holds for a chunk
// whose compressed payload lives in a c.<x>.<z>.mcc beside the file.
func bbExternalStub(version byte) []byte {
	return []byte{0x80 | version}
}

// bbRegionRecords extracts every allocated slot's stored record bytes
// (version byte + compressed payload) and the timestamp table.
func bbRegionRecords(t *testing.T, raw []byte) (map[int][]byte, []byte) {
	t.Helper()
	if len(raw) < 8192 {
		t.Fatalf("region header truncated (%d bytes)", len(raw))
	}
	stamps := append([]byte(nil), raw[4096:8192]...)
	records := map[int][]byte{}
	for slot := 0; slot < 1024; slot++ {
		loc := binary.BigEndian.Uint32(raw[slot*4 : slot*4+4])
		if loc == 0 {
			continue
		}
		start := int(loc>>8) * 4096
		if start+4 > len(raw) {
			t.Fatalf("slot %d location past end of file", slot)
		}
		length := int(binary.BigEndian.Uint32(raw[start : start+4]))
		if start+4+length > len(raw) || length < 1 {
			t.Fatalf("slot %d record length %d out of range", slot, length)
		}
		records[slot] = append([]byte(nil), raw[start+4:start+4+length]...)
	}
	return records, stamps
}

// bbRegionEncode lays records out with fresh sector allocation and the
// original timestamps, the same shape the merge writer produces.
func bbRegionEncode(records map[int][]byte, stamps []byte) []byte {
	data := make([]byte, 8192)
	copy(data[4096:], stamps)
	for slot := 0; slot < 1024; slot++ {
		record, ok := records[slot]
		if !ok {
			continue
		}
		sectors := (len(record) + 4 + 4095) / 4096
		off := len(data) / 4096
		binary.BigEndian.PutUint32(data[slot*4:], uint32(off<<8|sectors))
		start := len(data)
		data = append(data, make([]byte, sectors*4096)...)
		binary.BigEndian.PutUint32(data[start:], uint32(len(record)))
		copy(data[start+4:], record)
	}
	return data
}

// bbInflate unpacks a zlib record payload (the deflate frames vanilla
// writes by default) back into the raw NBT stream.
func bbInflate(t *testing.T, payload []byte) []byte {
	t.Helper()
	zr, err := zlib.NewReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("control record is not zlib: %v", err)
	}
	plain, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		t.Fatalf("control record inflate: %v", err)
	}
	return plain
}

// bbMobsSeen joins addr and returns the spawn packets that arrive in
// the window as entity type -> positions, answering keep-alives, chunk
// batch acks and the join teleport so the stream keeps flowing.
func bbMobsSeen(t *testing.T, addr, name string, window time.Duration) map[int32][][3]float64 {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	spawns := map[int32][][3]float64{}
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			var kb protocol.Buffer
			kb.Raw(p.Data)
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			_, e1 := r.VarInt()
			_, e2 := r.Read(16)
			typ, e3 := r.VarInt()
			x, e4 := r.Double()
			y, e5 := r.Double()
			z, e6 := r.Double()
			if e1 == nil && e2 == nil && e3 == nil && e4 == nil && e5 == nil && e6 == nil {
				spawns[typ] = append(spawns[typ], [3]float64{x, y, z})
			}
		}
	}
	return spawns
}

func bbNear(list [][3]float64, want [3]float64, tol float64) bool {
	for _, p := range list {
		if math.Abs(p[0]-want[0]) <= tol &&
			math.Abs(p[1]-want[1]) <= tol &&
			math.Abs(p[2]-want[2]) <= tol {
			return true
		}
	}
	return false
}

// The marker blocks: one per chunk of region r.0.0 the crafted file
// reframes, so every record version carries a cell only that record
// can restore.
var bbMarkerBlocks = []struct {
	pos    [3]int
	name   string
	marker string
}{
	{[3]int{4, -60, 4}, "minecraft:gold_block", "RCOK_GOLD"},      // chunk (0,0) -> gzip
	{[3]int{20, -60, 4}, "minecraft:diamond_block", "RCOK_DIAMOND"}, // chunk (1,0) -> lz4
	{[3]int{36, -60, 4}, "minecraft:emerald_block", "RCOK_EMERALD"}, // chunk (2,0) -> zlib control
	{[3]int{4, -60, 20}, "minecraft:lapis_block", "RCOK_LAPIS"},    // chunk (0,1) -> external zlib
	{[3]int{20, -60, 20}, "minecraft:redstone_block", "RCOK_REDSTONE"}, // chunk (1,1) -> external lz4
}

// bbPlaceMarkers runs the setblock commands on vanilla's console and
// confirms every placement through the log, reissuing the ones that
// landed before their chunk finished loading.
func bbPlaceMarkers(t *testing.T, dir string, say func(string)) {
	t.Helper()
	for _, b := range bbMarkerBlocks {
		say(fmt.Sprintf("setblock %d %d %d %s", b.pos[0], b.pos[1], b.pos[2], b.name))
	}
	for _, b := range bbMarkerBlocks {
		placed := false
		for try := 0; try < 40 && !placed; try++ {
			say(fmt.Sprintf("execute if block %d %d %d %s run say %s",
				b.pos[0], b.pos[1], b.pos[2], b.name, b.marker))
			if vanillaLogHas(dir, b.marker) {
				placed = true
				break
			}
			if try%8 == 7 {
				say(fmt.Sprintf("setblock %d %d %d %s", b.pos[0], b.pos[1], b.pos[2], b.name))
			}
		}
		if !placed {
			t.Fatalf("vanilla never placed %s at %v", b.name, b.pos)
		}
	}
}

// bbCraftRegion rewrites one region file so its records use the given
// framings, and writes the c.<x>.<z>.mcc side files external stubs
// need. reframes maps slot -> how to restore that slot's payload.
func bbCraftRegion(t *testing.T, path string, reframes map[int]func(plain []byte) []byte, external map[int]byte) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	records, stamps := bbRegionRecords(t, raw)
	for slot, reframe := range reframes {
		record, ok := records[slot]
		if !ok {
			t.Fatalf("slot %d has no saved record to reframe", slot)
		}
		if record[0] != 2 {
			t.Fatalf("slot %d record is version %d, want the default deflate 2", slot, record[0])
		}
		plain := bbInflate(t, record[1:])
		records[slot] = reframe(plain)
	}
	for slot, version := range external {
		record, ok := records[slot]
		if !ok {
			t.Fatalf("slot %d has no saved record to externalize", slot)
		}
		plain := bbInflate(t, record[1:])
		cx, cz := slot%32, slot/32
		side := fmt.Sprintf("c.%d.%d.mcc", cx, cz)
		var payload []byte
		switch version {
		case 2:
			payload = bbZlibPayload(plain)
		case 4:
			payload = bbLZ4Stream(plain)
		default:
			t.Fatalf("unsupported external side version %d", version)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), side), payload, 0o644); err != nil {
			t.Fatal(err)
		}
		records[slot] = bbExternalStub(version)
	}
	if err := os.WriteFile(path, bbRegionEncode(records, stamps), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRegionRecordVersionsParity is leg A: a hand-reframed region is
// loadable by the real server and by mc-go, over the wire.
func TestRegionRecordVersionsParity(t *testing.T) {
	jar := blackboxJar(t)

	// This test's xxHash copy is pinned to the measured library output
	// before it frames anything.
	for _, v := range []struct {
		in   string
		seed uint32
		want uint32
	}{
		{"", 0x9747b28c, 0x8d3b42d8},
		{"lz4block stream", 0x9747b28c, 0x9e98dc44},
		{"", 0, 0x02cc5d05},
	} {
		if got := bbXXHash32([]byte(v.in), v.seed); got != v.want {
			t.Fatalf("test xxhash copy drifted: %q seed %#x = %#x, want %#x", v.in, v.seed, got, v.want)
		}
	}

	zombieID, _ := server.EntityTypeID("zombie")
	pigID, _ := server.EntityTypeID("pig")

	// ---- vanilla writes the base world ----
	vDir := t.TempDir()
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort,
		map[string]string{"level-name": "world", "spawn-protection": "0"}, nil, vDir)
	// All five marker chunks plus the two mob chunks sit inside
	// x 0..47, z 0..31 of region r.0.0.
	say("forceload add 0 0 47 31")
	say("summon minecraft:zombie 10 -60 10 {NoAI:1b,PersistenceRequired:1b}")
	say("summon minecraft:pig 40 -60 10 {NoAI:1b}")
	bbPlaceMarkers(t, vDir, say)
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d, log tail:\n%s", code, vanillaLogTailText(vDir))
	}

	overworld := world.DimensionDir(filepath.Join(vDir, "world"), "minecraft:overworld")
	chunkRegion := filepath.Join(overworld, "region", "r.0.0.mca")
	entityRegion := filepath.Join(overworld, "entities", "r.0.0.mca")
	for _, p := range []string{chunkRegion, entityRegion} {
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			t.Fatalf("vanilla wrote no %s: %v", p, err)
		}
	}

	// ---- craft the reframed copies ----
	// Chunk records: (0,0) gzip, (1,0) lz4, (2,0) untouched deflate
	// control, (0,1) external deflate, (1,1) external lz4. The zombie
	// (chunk 0,0) becomes a gzip entity record and the pig (chunk 2,0)
	// an external deflate one.
	aDir := t.TempDir() // the real server's copy
	bDir := t.TempDir() // mc-go's copy
	cpWorld(t, filepath.Join(vDir, "world"), filepath.Join(aDir, "world"))
	cpWorld(t, filepath.Join(vDir, "world"), filepath.Join(bDir, "world"))
	bbCraftRegion(t, filepath.Join(aDir, "world", "dimensions", "minecraft", "overworld", "region", "r.0.0.mca"),
		map[int]func([]byte) []byte{0: bbGzipRecord, 1: bbLZ4Record},
		map[int]byte{32: 2, 33: 4})
	bbCraftRegion(t, filepath.Join(aDir, "world", "dimensions", "minecraft", "overworld", "entities", "r.0.0.mca"),
		map[int]func([]byte) []byte{0: bbGzipRecord}, map[int]byte{2: 2})
	bbCraftRegion(t, filepath.Join(bDir, "world", "dimensions", "minecraft", "overworld", "region", "r.0.0.mca"),
		map[int]func([]byte) []byte{0: bbGzipRecord, 1: bbLZ4Record},
		map[int]byte{32: 2, 33: 4})
	bbCraftRegion(t, filepath.Join(bDir, "world", "dimensions", "minecraft", "overworld", "entities", "r.0.0.mca"),
		map[int]func([]byte) []byte{0: bbGzipRecord}, map[int]byte{2: 2})

	// ---- the real server loads the crafted world ----
	aPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say2, wait2 := vanillaConsole(t, jar, aPort,
		map[string]string{"level-name": "world", "spawn-protection": "0"}, nil, aDir)
	say2("forceload add 0 0 47 31")
	for _, b := range bbMarkerBlocks {
		back := false
		for try := 0; try < 40 && !back; try++ {
			say2(fmt.Sprintf("execute if block %d %d %d %s run say %s_BACK",
				b.pos[0], b.pos[1], b.pos[2], b.name, b.marker))
			if vanillaLogHas(aDir, b.marker+"_BACK") {
				back = true
				break
			}
		}
		if !back {
			t.Errorf("vanilla lost %s at %v: the reframed record for its chunk did not load", b.name, b.pos)
		}
	}
	for _, probe := range []struct {
		selector string
		marker   string
	}{
		{"@e[type=minecraft:zombie]", "RCBACK_ZOMBIE"},
		{"@e[type=minecraft:pig]", "RCBACK_PIG"},
	} {
		back := false
		for try := 0; try < 40 && !back; try++ {
			say2("execute if entity " + probe.selector + " run say " + probe.marker)
			if vanillaLogHas(aDir, probe.marker) {
				back = true
				break
			}
		}
		if !back {
			t.Errorf("vanilla lost the %s from the reframed entity region", probe.selector)
		}
	}
	say2("stop")
	if code := wait2(); code != 0 {
		t.Fatalf("vanilla exited %d on the crafted world", code)
	}

	// ---- mc-go loads the same crafted world ----
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr := startMcGoCfg(t, mPort, func(s *server.Server) {
		s.WorldDir = filepath.Join(bDir, "world")
		s.WorldPath = ""
		s.LevelName = "world"
	})
	mobs := bbMobsSeen(t, addr, "Mobber", 4*time.Second)
	if !bbNear(mobs[zombieID], [3]float64{10, -60, 10}, 1.5) {
		t.Errorf("mc-go lost the zombie from the gzip entity record: spawns %v", mobs[zombieID])
	}
	if !bbNear(mobs[pigID], [3]float64{40, -60, 10}, 1.5) {
		t.Errorf("mc-go lost the pig from the external entity record: spawns %v", mobs[pigID])
	}

	c, pr, pw := joinPlayBlackbox(t, addr, "Reader")
	defer c.Close()
	sink := newChatSink(pr, pw)
	time.Sleep(2 * time.Second)
	sink.take()

	positions := make([][3]int, 0, len(bbMarkerBlocks))
	want := map[[3]int]string{}
	for _, b := range bbMarkerBlocks {
		positions = append(positions, b.pos)
		want[b.pos] = b.name
	}
	candidates := []string{"minecraft:gold_block", "minecraft:diamond_block", "minecraft:emerald_block",
		"minecraft:lapis_block", "minecraft:redstone_block", "minecraft:grass_block", "minecraft:air"}
	got := queryNames(t, pw, sink, candidates, positions)
	for _, b := range bbMarkerBlocks {
		if got[b.pos] != want[b.pos] {
			t.Errorf("mc-go lost %s at %v: the crafted record for its chunk did not load (got %q)",
				b.name, b.pos, got[b.pos])
		}
	}
}

// TestRegionLZ4WrittenByVanilla is leg B: the real server's own LZ4
// records, written with region-file-compression=lz4, load in mc-go.
func TestRegionLZ4WrittenByVanilla(t *testing.T) {
	jar := blackboxJar(t)
	zombieID, _ := server.EntityTypeID("zombie")

	vDir := t.TempDir()
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort,
		map[string]string{"level-name": "world", "region-file-compression": "lz4"}, nil, vDir)
	say("forceload add 0 0 15 15")
	say("summon minecraft:zombie 10 -60 10 {NoAI:1b,PersistenceRequired:1b}")
	say("setblock 4 -60 4 minecraft:ancient_debris")
	placed := false
	for try := 0; try < 40 && !placed; try++ {
		say("execute if block 4 -60 4 minecraft:ancient_debris run say LZ4OK_DEBRIS")
		if vanillaLogHas(vDir, "LZ4OK_DEBRIS") {
			placed = true
			break
		}
		if try%8 == 7 {
			say("setblock 4 -60 4 minecraft:ancient_debris")
		}
	}
	if !placed {
		t.Fatalf("vanilla never placed the debris marker")
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d, log tail:\n%s", code, vanillaLogTailText(vDir))
	}

	overworld := world.DimensionDir(filepath.Join(vDir, "world"), "minecraft:overworld")
	chunkRegion := filepath.Join(overworld, "region", "r.0.0.mca")
	entityRegion := filepath.Join(overworld, "entities", "r.0.0.mca")
	for _, path := range []string{chunkRegion, entityRegion} {
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			t.Fatalf("vanilla wrote no %s: %v", path, err)
		}
		records, _ := bbRegionRecords(t, raw)
		if len(records) == 0 {
			t.Fatalf("%s holds no records", path)
		}
		for slot, record := range records {
			if record[0] != 4 {
				t.Errorf("%s slot %d is record version %d, want the LZ4 version 4", filepath.Base(path), slot, record[0])
			}
		}
	}
	// And the file decodes through the reader under test before the
	// boot leg repeats it through the whole server.
	chunks, err := world.ReadRegionFile(chunkRegion)
	if err != nil {
		t.Fatalf("read vanilla's lz4 chunk region: %v", err)
	}
	if _, ok := chunks[[2]int32{0, 0}]; !ok {
		t.Fatalf("chunk (0,0) missing from vanilla's lz4 region decode")
	}

	mDir := t.TempDir()
	cpWorld(t, filepath.Join(vDir, "world"), filepath.Join(mDir, "world"))
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr := startMcGoCfg(t, mPort, func(s *server.Server) {
		s.WorldDir = filepath.Join(mDir, "world")
		s.WorldPath = ""
		s.LevelName = "world"
	})
	mobs := bbMobsSeen(t, addr, "Mobber", 4*time.Second)
	if !bbNear(mobs[zombieID], [3]float64{10, -60, 10}, 1.5) {
		t.Errorf("mc-go lost the zombie from vanilla's lz4 entity region: spawns %v", mobs[zombieID])
	}

	c, pr, pw := joinPlayBlackbox(t, addr, "Reader")
	defer c.Close()
	sink := newChatSink(pr, pw)
	time.Sleep(2 * time.Second)
	sink.take()
	got := queryNames(t, pw, sink,
		[]string{"minecraft:ancient_debris", "minecraft:grass_block", "minecraft:air"},
		[][3]int{{4, -60, 4}})
	if got[[3]int{4, -60, 4}] != "minecraft:ancient_debris" {
		t.Errorf("mc-go lost the debris marker from vanilla's lz4 region (got %q)",
			got[[3]int{4, -60, 4}])
	}
}
