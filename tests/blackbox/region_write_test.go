package blackbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// The write side of `region-file-compression`: mc-go's own records are
// read back by the real server. The read-side suite already proves
// vanilla's records load in mc-go; this is the direction that was
// missing, and the one a real deployment depends on - a world mc-go
// saves has to open in vanilla.
//
// Each leg writes the five marker chunks plus two mobs through mc-go,
// asserts the record version byte the selected codec must produce,
// and then hands the world directory to the real server, which has to
// restore every marker and both mobs over the console. The external
// leg pushes one chunk past the 256-sector threshold with command
// blocks whose NBT is far larger than the in-file limit, then makes
// the real server read the stub + side file and write the chunk back.

// bbWriteCompressedWorld starts mc-go with the given record codec,
// writes the marker blocks and two persistent mobs, and saves.
func bbWriteCompressedWorld(t *testing.T, codec world.RegionCodec) string {
	t.Helper()
	dir := t.TempDir()
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	var srv *server.Server
	addr := startMcGoCfg(t, mPort, func(s *server.Server) {
		srv = s
		s.WorldDir = filepath.Join(dir, "world")
		s.WorldPath = ""
		s.LevelName = "world"
		s.WorldSeed = 1
		s.RegionCompression = codec
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Writer")
	sink := newChatSink(pr, pw)
	time.Sleep(2 * time.Second)
	sink.take()
	for _, b := range bbMarkerBlocks {
		sendCmd(t, pw, fmt.Sprintf("setblock %d %d %d %s", b.pos[0], b.pos[1], b.pos[2], b.name))
	}
	sendCmd(t, pw, "summon minecraft:zombie 10 -60 10 {NoAI:1b,PersistenceRequired:1b}")
	sendCmd(t, pw, "summon minecraft:pig 40 -60 10 {NoAI:1b,PersistenceRequired:1b}")
	waitDrained(t, pw, sink)
	_ = c.Close()
	if err := srv.SaveWorld(); err != nil {
		t.Fatalf("mc-go save: %v", err)
	}
	return dir
}

// bbAssertRecordVersions checks every allocated record in one region
// file carries the codec's version byte, and returns the records.
func bbAssertRecordVersions(t *testing.T, path string, want byte) map[int][]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		t.Fatalf("mc-go wrote no %s: %v", path, err)
	}
	records, _ := bbRegionRecords(t, raw)
	if len(records) == 0 {
		t.Fatalf("%s holds no records", path)
	}
	for slot, record := range records {
		if record[0] != want {
			t.Errorf("%s slot %d is record version %d, want %d", filepath.Base(path), slot, record[0], want)
		}
	}
	return records
}

// bbLZ4StreamMethods walks one LZ4 block stream and counts data blocks
// by method nibble (raw 0x10, compressed 0x20), checking the frame
// layout as it goes. It returns after the required closing block.
func bbLZ4StreamMethods(t *testing.T, payload []byte) map[byte]int {
	t.Helper()
	methods := map[byte]int{}
	for {
		if len(payload) < 21 {
			t.Fatalf("lz4 stream truncated in a block header (%d bytes left)", len(payload))
		}
		if string(payload[:8]) != "LZ4Block" {
			t.Fatalf("lz4 block magic is %q, want LZ4Block", payload[:8])
		}
		method := payload[8] >> 4
		compressed := int(binary.LittleEndian.Uint32(payload[9:13]))
		original := int(binary.LittleEndian.Uint32(payload[13:17]))
		if compressed == 0 && original == 0 {
			return methods
		}
		methods[method]++
		if len(payload) < 21+compressed {
			t.Fatalf("lz4 block claims %d compressed bytes with %d left", compressed, len(payload)-21)
		}
		payload = payload[21+compressed:]
	}
}

// bbVanillaRestoresWorld boots the real server on a world directory and
// proves every marker block and both mobs came back. forceload is the
// console switch that loads the five marker chunks; a command issued
// before its chunk is loaded fails "That position is not loaded", so
// each probe is repeated until the log shows the answer.
func bbVanillaRestoresWorld(t *testing.T, jar, worldDir, property string) {
	t.Helper()
	vDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vDir, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	cpWorld(t, worldDir, filepath.Join(vDir, "world"))
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort,
		map[string]string{"level-name": "world", "spawn-protection": "0", "region-file-compression": property}, nil, vDir)
	stopped := false
	defer func() {
		if !stopped {
			say("stop")
			wait()
		}
	}()
	say("forceload add 0 0 47 31")
	for _, b := range bbMarkerBlocks {
		back := false
		for try := 0; try < 40 && !back; try++ {
			say(fmt.Sprintf("execute if block %d %d %d %s run say %s_FROM_GO",
				b.pos[0], b.pos[1], b.pos[2], b.name, b.marker))
			if vanillaLogHas(vDir, b.marker+"_FROM_GO") {
				back = true
			}
		}
		if !back {
			t.Errorf("vanilla lost %s at %v: it did not read mc-go's record for that chunk", b.name, b.pos)
		}
	}
	for _, probe := range []struct {
		selector string
		marker   string
	}{
		{"@e[type=minecraft:zombie]", "RWBACK_ZOMBIE"},
		{"@e[type=minecraft:pig]", "RWBACK_PIG"},
	} {
		back := false
		for try := 0; try < 40 && !back; try++ {
			say("execute if entity " + probe.selector + " run say " + probe.marker)
			if vanillaLogHas(vDir, probe.marker) {
				back = true
			}
		}
		if !back {
			t.Errorf("vanilla lost the %s from mc-go's entity region", probe.selector)
		}
	}
	say("save-all flush")
	say("stop")
	stopped = true
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d on mc-go's world, log tail:\n%s", code, vanillaLogTailText(vDir))
	}
}

// TestRegionNoneWrittenByMcGo is the uncompressed write leg: version 3
// records, readable by the real server.
func TestRegionNoneWrittenByMcGo(t *testing.T) {
	jar := blackboxJar(t)
	dir := bbWriteCompressedWorld(t, world.RegionNone)
	overworld := world.DimensionDir(filepath.Join(dir, "world"), "minecraft:overworld")
	chunkRecords := bbAssertRecordVersions(t, filepath.Join(overworld, "region", "r.0.0.mca"), 3)
	bbAssertRecordVersions(t, filepath.Join(overworld, "entities", "r.0.0.mca"), 3)
	for slot, record := range chunkRecords {
		// A raw record's payload is the chunk NBT itself, so it has
		// to start with the root compound tag.
		if len(record) < 3 || record[1] != 0x0a {
			t.Errorf("chunk slot %d is not a raw NBT compound (version %#x, first tag %#x)", slot, record[0], record[1])
		}
	}
	// The external side files belong to oversized records only; this
	// world has none.
	if files, _ := filepath.Glob(filepath.Join(overworld, "region", "c.*.*.mcc")); len(files) != 0 {
		t.Errorf("none-codec world wrote external side files: %v", files)
	}
	bbVanillaRestoresWorld(t, jar, filepath.Join(dir, "world"), "none")
}

// TestRegionLZ4WrittenByMcGo is the LZ4 write leg: version 4 records
// with at least one compressed block, read by the library the real
// server ships.
func TestRegionLZ4WrittenByMcGo(t *testing.T) {
	jar := blackboxJar(t)
	dir := bbWriteCompressedWorld(t, world.RegionLZ4)
	overworld := world.DimensionDir(filepath.Join(dir, "world"), "minecraft:overworld")
	chunkRecords := bbAssertRecordVersions(t, filepath.Join(overworld, "region", "r.0.0.mca"), 4)
	entityRecords := bbAssertRecordVersions(t, filepath.Join(overworld, "entities", "r.0.0.mca"), 4)

	compressedBlocks := 0
	for _, records := range []map[int][]byte{chunkRecords, entityRecords} {
		for _, record := range records {
			methods := bbLZ4StreamMethods(t, record[1:])
			compressedBlocks += methods[2]
		}
	}
	t.Logf("LZ4 records carry %d compressed blocks", compressedBlocks)
	if compressedBlocks == 0 {
		t.Errorf("no LZ4 block was compressed; the library's compressed path was not exercised")
	}
	// The reader under test decodes the same records the real server
	// library will read.
	chunks, err := world.ReadRegionFile(filepath.Join(overworld, "region", "r.0.0.mca"))
	if err != nil {
		t.Fatalf("read mc-go's lz4 chunk region: %v", err)
	}
	if _, ok := chunks[[2]int32{0, 0}]; !ok {
		t.Fatalf("chunk (0,0) missing from mc-go's lz4 region decode")
	}
	bbVanillaRestoresWorld(t, jar, filepath.Join(dir, "world"), "lz4")
}

// TestRegionExternalRecordWrittenByMcGo pushes one chunk past the
// writer's 256-sector threshold and proves the real server reads the
// stub + c.0.0.mcc side file, including the oversized block-entity NBT.
// The world uses the none codec so the payload stays large: 70 command
// blocks with 9000 CJK characters each is about 1.9 MB of raw chunk
// NBT, far beyond the 1 MiB in-file limit.
func TestRegionExternalRecordWrittenByMcGo(t *testing.T) {
	jar := blackboxJar(t)

	const commandBlocks = 70
	giant := "say " + strings.Repeat("界", 9000)

	dir := t.TempDir()
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	var srv *server.Server
	addr := startMcGoCfg(t, mPort, func(s *server.Server) {
		srv = s
		s.WorldDir = filepath.Join(dir, "world")
		s.WorldPath = ""
		s.LevelName = "world"
		s.WorldSeed = 1
		s.RegionCompression = world.RegionNone
	})
	c, pr, pw := joinPlayBlackbox(t, addr, "Writer")
	sink := newChatSink(pr, pw)
	time.Sleep(2 * time.Second)
	sink.take()
	// A marker outside the block-entity grid (command blocks occupy
	// x 1..10, z 1..7) proves the chunk's block states came from
	// mc-go's file too.
	sendCmd(t, pw, "setblock 14 -60 14 minecraft:ancient_debris")
	for i := 0; i < commandBlocks; i++ {
		x := 1 + i%10
		z := 1 + i/10
		sendCmd(t, pw, fmt.Sprintf(`setblock %d -60 %d minecraft:command_block{Command:%q}`, x, z, giant))
	}
	waitDrained(t, pw, sink)
	_ = c.Close()
	if err := srv.SaveWorld(); err != nil {
		t.Fatalf("mc-go save: %v", err)
	}

	overworld := world.DimensionDir(filepath.Join(dir, "world"), "minecraft:overworld")
	regionDir := filepath.Join(overworld, "region")
	regionPath := filepath.Join(regionDir, "r.0.0.mca")
	raw, err := os.ReadFile(regionPath)
	if err != nil || len(raw) < 8192 {
		t.Fatalf("mc-go wrote no chunk region: %v", err)
	}
	records, _ := bbRegionRecords(t, raw)
	stub, ok := records[0] // chunk (0,0)
	if !ok {
		t.Fatalf("mc-go wrote no record for chunk (0,0)")
	}
	if len(stub) != 1 || stub[0] != 0x80|3 {
		t.Fatalf("chunk (0,0) record is % x, want the 5-byte external none stub with version %#x", stub, 0x80|3)
	}
	loc := binary.BigEndian.Uint32(raw[0:4])
	if sectors := loc & 0xFF; sectors != 1 {
		t.Errorf("external stub allocates %d sectors, want 1", sectors)
	}
	sidePath := filepath.Join(regionDir, "c.0.0.mcc")
	side, err := os.ReadFile(sidePath)
	if err != nil {
		t.Fatalf("mc-go wrote no external side file: %v", err)
	}
	if len(side) < 1<<20 {
		t.Fatalf("external payload is %d bytes, want more than the 1 MiB threshold", len(side))
	}
	if side[0] != 0x0a {
		t.Fatalf("external payload starts with tag %#x, want the chunk compound", side[0])
	}
	// The side file is the chunk NBT; the giant command survives into
	// it with the exact string the commands carried.
	_, root, err := nbt.ReadNamed(strings.NewReader(string(side)))
	if err != nil {
		t.Fatalf("decode mc-go's external chunk NBT: %v", err)
	}
	got := bbCommandBlockCommand(t, root, 1, -60, 1)
	if got != giant {
		t.Fatalf("mc-go's external chunk holds a %d-byte command, want the %d-byte one written", len(got), len(giant))
	}

	// The real server reads the stub and side file, restores the
	// chunk, and writes it back. Its own save is the receipt: the
	// marker change is carried in the same record as the command
	// block, so a regenerated chunk would lose both.
	vDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vDir, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	cpWorld(t, filepath.Join(dir, "world"), filepath.Join(vDir, "world"))
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort,
		map[string]string{"level-name": "world", "spawn-protection": "0", "region-file-compression": "none"}, nil, vDir)
	stopped := false
	defer func() {
		if !stopped {
			say("stop")
			wait()
		}
	}()
	say("forceload add 0 0")
	read := false
	for try := 0; try < 40 && !read; try++ {
		say("execute if block 14 -60 14 minecraft:ancient_debris run say RWBACK_EXTERNAL_MARKER")
		if vanillaLogHas(vDir, "RWBACK_EXTERNAL_MARKER") {
			read = true
		}
	}
	if !read {
		t.Fatalf("vanilla never read mc-go's external record for chunk (0,0), log tail:\n%s", vanillaLogTailText(vDir))
	}
	// Force a save of the chunk the real server just read.
	say("setblock 14 -60 14 minecraft:stone")
	say("save-all flush")
	say("stop")
	stopped = true
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d on mc-go's external world, log tail:\n%s", code, vanillaLogTailText(vDir))
	}

	vOverworld := world.DimensionDir(filepath.Join(vDir, "world"), "minecraft:overworld")
	vRegion := filepath.Join(vOverworld, "region", "r.0.0.mca")
	vRaw, err := os.ReadFile(vRegion)
	if err != nil || len(vRaw) < 8192 {
		t.Fatalf("vanilla wrote no chunk region after loading: %v", err)
	}
	vRecords, _ := bbRegionRecords(t, vRaw)
	vStub, ok := vRecords[0]
	if !ok {
		t.Fatalf("vanilla wrote no record for chunk (0,0)")
	}
	if len(vStub) != 1 || vStub[0] != 0x80|3 {
		t.Fatalf("vanilla's resave of chunk (0,0) is % x, want the external none stub", vStub)
	}
	vSide, err := os.ReadFile(filepath.Join(vOverworld, "region", "c.0.0.mcc"))
	if err != nil {
		t.Fatalf("vanilla wrote no external side file: %v", err)
	}
	t.Logf("external chunk payload: mc-go wrote %d bytes, vanilla's resave is %d bytes", len(side), len(vSide))
	_, vRoot, err := nbt.ReadNamed(strings.NewReader(string(vSide)))
	if err != nil {
		t.Fatalf("decode vanilla's external chunk NBT: %v", err)
	}
	if got := bbCommandBlockCommand(t, vRoot, 1, -60, 1); got != giant {
		t.Errorf("vanilla's resave holds a %d-byte command, want the %d-byte one mc-go wrote", len(got), len(giant))
	}
	// The marker change is the receipt that vanilla rewrote the chunk:
	// mc-go's file holds ancient_debris there, and only a save of the
	// chunk vanilla loaded and edited can put stone on disk.
	reload := world.New(nil, 4)
	reload.Start()
	defer reload.Stop()
	if err := reload.LoadDimension(filepath.Join(vDir, "world"), "minecraft:overworld", -64, 384); err != nil {
		t.Fatalf("read vanilla's saved external world: %v", err)
	}
	gotBlock := reload.GetBlock(world.Pos{X: 14, Y: -60, Z: 14})
	name, ok := world.StateOwner(gotBlock)
	if !ok || name != "stone" {
		t.Errorf("vanilla's resave has %q at the marker, want the stone it placed", name)
	}
}

// bbChunkBlockEntities returns a saved chunk's block-entity compounds.
func bbChunkBlockEntities(t *testing.T, root any) []map[string]any {
	t.Helper()
	m, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("chunk NBT root is %T, want a compound", root)
	}
	list, ok := m["block_entities"].(nbt.List)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		if e, ok := item.(map[string]any); ok {
			out = append(out, e)
		}
	}
	return out
}

// bbCommandBlockCommand returns the Command string of the command block
// at one position in a saved chunk, or "" when there is none.
func bbCommandBlockCommand(t *testing.T, root any, x, y, z int) string {
	t.Helper()
	for _, e := range bbChunkBlockEntities(t, root) {
		id, _ := e["id"].(string)
		if id != "minecraft:command_block" {
			continue
		}
		bx, _ := e["x"].(int32)
		by, _ := e["y"].(int32)
		bz, _ := e["z"].(int32)
		if int(bx) == x && int(by) == y && int(bz) == z {
			cmd, _ := e["Command"].(string)
			return cmd
		}
	}
	return ""
}
