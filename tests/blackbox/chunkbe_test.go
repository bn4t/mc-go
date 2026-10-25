package blackbox

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// A chunk's block entities travel twice: once in the chunk file, and once
// in the chunk packet a joining client is sent. The two are not the same
// data, and the packet is the one a player sees: a sign whose entry is
// missing from the packet is a sign with blank text however right the
// file is.
//
// TestChunkPacketBlockEntityParity builds one of every block entity
// mc-go models on the real server, hands an identical copy of the world
// to each server, joins both with a scripted client, and compares the
// block entity array of the chunk packet each one sends, entry for entry.
//
// What the real server sends was measured rather than assumed, and it is
// not the saved form: the entry holds a packed column position and a type
// varint instead of an id and x/y/z - and for everything except a sign
// the data is empty. A chest holding three diamonds, a barrel, a furnace
// mid-cook, a hopper, a dropper, a dispenser, a shulker box, a brewing
// stand and a crafter all arrive as a single TAG_End byte; only a sign
// (and a hanging sign) arrives as a nameless compound of front_text,
// back_text and is_waxed.
func TestChunkPacketBlockEntityParity(t *testing.T) {
	jar := blackboxJar(t)
	built := buildChunkBEWorld(t, jar)

	// ---- the real server, joining the world ----
	vDir := t.TempDir()
	vWorld := filepath.Join(vDir, "world")
	copyTree(t, built, vWorld)
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort, map[string]string{"level-name": "world"}, nil, vDir)
	vc, vpr, _ := joinPlayBlackbox(t, fmt.Sprintf("127.0.0.1:%d", vPort), "Viewer")
	want := collectChunkBlockEntities(t, vc, vpr, 0, 0)
	vc.Close()
	say("stop")
	wait()
	checkChunkBEArray(t, want, "the real server")
	t.Logf("the real server's chunk (0,0) carries %d block entities: %s", len(want), renderChunkBEs(want))

	// ---- mc-go, from an identical copy ----
	mDir := t.TempDir()
	mWorld := filepath.Join(mDir, "world")
	copyTree(t, built, mWorld)
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr := startMcGoCfg(t, mPort, func(s *server.Server) {
		s.WorldDir = mWorld
		s.WorldPath = ""
		s.LevelName = "world"
	})
	mc, mpr, _ := joinPlayBlackbox(t, addr, "Viewer")
	got := collectChunkBlockEntities(t, mc, mpr, 0, 0)
	mc.Close()

	compareChunkBEs(t, got, want, "mc-go's chunk packet")
}

// chunkBEWorld is the world both servers are handed: one of every block
// entity mc-go models, each holding state that only a block entity can
// hold, plus a hanging sign and a second sign whose text, colour and
// glow differ from the default.
var chunkBEWorld = []string{
	"setblock 1 -60 1 minecraft:chest",
	"item replace block 1 -60 1 container.0 with minecraft:diamond 3",
	"item replace block 1 -60 1 container.26 with minecraft:apple 1",
	"setblock 2 -60 2 minecraft:furnace[facing=north]",
	"item replace block 2 -60 2 container.0 with minecraft:raw_iron 2",
	"item replace block 2 -60 2 container.1 with minecraft:coal 1",
	"setblock 3 -60 3 minecraft:hopper[facing=down]",
	"item replace block 3 -60 3 container.3 with minecraft:gold_ingot 12",
	"setblock 4 -60 4 minecraft:barrel",
	"item replace block 4 -60 4 container.1 with minecraft:apple 7",
	"setblock 5 -60 5 minecraft:brewing_stand",
	"item replace block 5 -60 5 container.3 with minecraft:blaze_powder 2",
	"setblock 6 -60 6 minecraft:blast_furnace[facing=north]",
	"item replace block 6 -60 6 container.1 with minecraft:coal 4",
	"setblock 7 -60 7 minecraft:smoker[facing=north]",
	"item replace block 7 -60 7 container.0 with minecraft:beef 2",
	"setblock 8 -60 8 minecraft:dispenser[facing=north]",
	"item replace block 8 -60 8 container.0 with minecraft:arrow 5",
	"setblock 9 -60 9 minecraft:dropper[facing=north]",
	"item replace block 9 -60 9 container.0 with minecraft:stone 1",
	"setblock 10 -60 10 minecraft:shulker_box",
	"item replace block 10 -60 10 container.0 with minecraft:gold_ingot 4",
	"setblock 11 -60 11 minecraft:trapped_chest",
	"item replace block 11 -60 11 container.0 with minecraft:emerald 2",
	"setblock 12 -60 12 minecraft:crafter[orientation=down_east]",
	"item replace block 12 -60 12 container.0 with minecraft:stone 2",
	// A sign at a position whose x and z differ, so the two nibbles of
	// the packed position byte cannot be confused with each other.
	"setblock 14 -60 3 minecraft:oak_sign",
	`data merge block 14 -60 3 {front_text:{messages:['"line one"','{"text":"line two"}','','']},back_text:{messages:['"behind"','','','']}}`,
	"setblock 15 -61 4 minecraft:oak_sign",
	`data merge block 15 -61 4 {front_text:{color:"red",has_glowing_text:1b,messages:['"glow"','','','']},is_waxed:1b}`,
	"setblock 0 -60 5 minecraft:oak_hanging_sign",
	`data merge block 0 -60 5 {front_text:{messages:['"hang"','','','']}}`,
}

// chunkBEContainers is how many container block entities the fixture
// world holds: every block entity mc-go models except the signs.
const chunkBEContainers = 12

// chunkBEWorldSigns is the sign data the packet has to carry: the two
// signs' front lines and, for the second, its colour, glow and wax.
var chunkBEWorldSigns = map[[3]int][]string{
	{14, -60, 3}: {`"line one"`, `{"text":"line two"}`, ``, ``},
	{15, -61, 4}: {`"glow"`, ``, ``, ``},
	{0, -60, 5}:  {`"hang"`, ``, ``, ``},
}

// buildChunkBEWorld builds the fixture world on the real server and
// returns its directory, checking that every fixture block became a block
// entity: a command the real server refused would leave this test
// comparing packets over a world with one arrangement missing.
func buildChunkBEWorld(t *testing.T, jar string) string {
	t.Helper()
	dir := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, port, map[string]string{"level-name": "world"}, nil, dir)
	say("forceload add 0 0")
	say("gamerule minecraft:advance_time false")
	// The tick loop is frozen so nothing moves between building the
	// fixtures and saving them.
	say("tick freeze")
	for _, c := range chunkBEWorld {
		say(c)
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited with code %d while building the fixtures, log tail:\n%s",
			code, vanillaLogTailText(dir))
	}
	worldDir := filepath.Join(dir, "world")
	be := chunkBlockEntitiesByPos(t, worldDir)
	for _, pos := range [][3]int{
		{1, -60, 1}, {2, -60, 2}, {3, -60, 3}, {4, -60, 4}, {5, -60, 5},
		{6, -60, 6}, {7, -60, 7}, {8, -60, 8}, {9, -60, 9}, {10, -60, 10},
		{11, -60, 11}, {12, -60, 12}, {14, -60, 3}, {15, -61, 4}, {0, -60, 5},
	} {
		if _, ok := be[pos]; !ok {
			t.Fatalf("the fixture world has no block entity at %v: %s", pos, renderBlockEntities(be))
		}
	}
	return worldDir
}

// chunkBEEntry is one entry of a chunk packet's block entity array.
type chunkBEEntry struct {
	typ int32
	nbt map[string]any
}

// collectChunkBlockEntities reads packets until chunk (cx,cz) arrives and
// returns its block entity array, keyed by position.
func collectChunkBlockEntities(t *testing.T, c net.Conn, pr *protocol.Reader, cx, cz int32) map[[3]int]chunkBEEntry {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	for i := 0; i < 600; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("waiting for chunk (%d,%d): %v", cx, cz, err)
		}
		if p.ID != server.IDPlayChunkData {
			continue
		}
		gx, gz, entries, err := parseChunkBlockEntities(p.Data)
		if err != nil {
			t.Fatalf("parse chunk packet: %v", err)
		}
		if gx == cx && gz == cz {
			return entries
		}
	}
	t.Fatalf("chunk (%d,%d) never arrived", cx, cz)
	return nil
}

// parseChunkBlockEntities walks a chunk packet's header to its block
// entity array and returns every entry in it, keyed by position.
//
// The header is: x and z as ints, a varint count of heightmaps each made
// of a varint type, a varint long count and that many longs, a
// length-prefixed section blob, and then the block entity array. Each
// entry is a packed column position byte, the y as a big-endian short, a
// varint type, and the entry's NBT - a nameless value, or a bare TAG_End
// when the real server sends no data for it.
func parseChunkBlockEntities(data []byte) (int32, int32, map[[3]int]chunkBEEntry, error) {
	r := protocol.NewPacketReader(data)
	cx, err := r.Int()
	if err != nil {
		return 0, 0, nil, err
	}
	cz, err := r.Int()
	if err != nil {
		return 0, 0, nil, err
	}
	n, err := r.VarInt()
	if err != nil {
		return 0, 0, nil, err
	}
	for i := int32(0); i < n; i++ {
		if _, err := r.VarInt(); err != nil { // heightmap type
			return 0, 0, nil, err
		}
		ln, err := r.VarInt()
		if err != nil {
			return 0, 0, nil, err
		}
		for j := int32(0); j < ln; j++ {
			if _, err := r.Long(); err != nil {
				return 0, 0, nil, err
			}
		}
	}
	sz, err := r.VarInt()
	if err != nil {
		return 0, 0, nil, err
	}
	for j := int32(0); j < sz; j++ {
		if _, err := r.Byte(); err != nil {
			return 0, 0, nil, err
		}
	}
	beCount, err := r.VarInt()
	if err != nil {
		return 0, 0, nil, err
	}
	rest := data[len(data)-r.Remaining():]
	out := map[[3]int]chunkBEEntry{}
	for i := int32(0); i < beCount; i++ {
		br := bytes.NewReader(rest)
		var entry chunkBEEntry
		xz, err := readChunkBEByte(br)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("entry %d position: %w", i, err)
		}
		var yb [2]byte
		if _, err := io.ReadFull(br, yb[:]); err != nil {
			return 0, 0, nil, fmt.Errorf("entry %d y: %w", i, err)
		}
		if entry.typ, err = readChunkBEVarInt(br); err != nil {
			return 0, 0, nil, fmt.Errorf("entry %d type: %w", i, err)
		}
		peek, err := readChunkBEByte(br)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("entry %d nbt: %w", i, err)
		}
		entry.nbt = map[string]any{}
		if peek != 0 {
			// Not a bare TAG_End: read the value that follows.
			if err := br.UnreadByte(); err != nil {
				return 0, 0, nil, err
			}
			v, err := nbt.ReadUnnamed(br)
			if err != nil {
				return 0, 0, nil, fmt.Errorf("entry %d nbt: %w", i, err)
			}
			m, ok := v.(map[string]any)
			if !ok {
				entry.nbt["__raw"] = v
			} else {
				entry.nbt = m
			}
		} else {
			entry.nbt["__empty"] = true
		}
		pos := [3]int{
			int(xz >> 4),
			int(int16(uint16(yb[0])<<8 | uint16(yb[1]))),
			int(xz & 15),
		}
		out[pos] = entry
		rest = rest[len(rest)-br.Len():]
	}
	return cx, cz, out, nil
}

func readChunkBEByte(r io.Reader) (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r, b[:])
	return b[0], err
}

func readChunkBEVarInt(r io.Reader) (int32, error) {
	var v uint32
	for i := 0; i < 5; i++ {
		b, err := readChunkBEByte(r)
		if err != nil {
			return 0, err
		}
		v |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return int32(v), nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}

// checkChunkBEArray requires the real server's own array to be the one
// this test describes, so that the comparison against mc-go is a
// comparison against a packet that carried something.
func checkChunkBEArray(t *testing.T, got map[[3]int]chunkBEEntry, who string) {
	t.Helper()
	// The twelve containers plus the signs: every fixture block that owns
	// a block entity, and nothing else in the chunk does.
	if len(got) != chunkBEContainers+len(chunkBEWorldSigns) {
		t.Errorf("%s sent %d block entities, want %d: %s",
			who, len(got), chunkBEContainers+len(chunkBEWorldSigns), renderChunkBEs(got))
	}
	for pos, lines := range chunkBEWorldSigns {
		entry, ok := got[pos]
		if !ok {
			t.Errorf("%s sent no block entity for the sign at %v: %s", who, pos, renderChunkBEs(got))
			continue
		}
		front, ok := entry.nbt["front_text"].(map[string]any)
		if !ok {
			t.Errorf("%s sent the sign at %v without front_text: %s", who, pos, renderNBT(entry.nbt, true))
			continue
		}
		msgs, _ := front["messages"].(nbt.List)
		if len(msgs.Items) != len(lines) {
			t.Errorf("%s sent the sign at %v with %d lines, want %d: %s",
				who, pos, len(msgs.Items), len(lines), renderNBT(entry.nbt, true))
			continue
		}
		for i, want := range lines {
			if s, _ := msgs.Items[i].(string); s != want {
				t.Errorf("%s sent line %d of the sign at %v as %q, want %q", who, i, pos, s, want)
			}
		}
		// The sign's block entity type is minecraft:sign, and a hanging
		// sign's is its own: 7 and 8 in the block entity type registry.
		wantType := int32(7)
		if pos == [3]int{0, -60, 5} {
			wantType = 8
		}
		if entry.typ != wantType {
			t.Errorf("%s sent the sign at %v as type %d, want %d", who, pos, entry.typ, wantType)
		}
	}
}

// compareChunkBEs compares two block entity arrays entry for entry, by
// position, type and data.
func compareChunkBEs(t *testing.T, got, want map[[3]int]chunkBEEntry, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s carries %d block entities, want %d:\n got %s\nwant %s",
			what, len(got), len(want), renderChunkBEs(got), renderChunkBEs(want))
	}
	for pos, wantEntry := range want {
		gotEntry, ok := got[pos]
		if !ok {
			t.Errorf("%s has no block entity at %v (want %s)", what, pos, renderNBT(wantEntry.nbt, true))
			continue
		}
		if gotEntry.typ != wantEntry.typ {
			t.Errorf("%s at %v is type %d, want %d", what, pos, gotEntry.typ, wantEntry.typ)
		}
		if a, b := renderNBT(gotEntry.nbt, true), renderNBT(wantEntry.nbt, true); a != b {
			t.Errorf("%s at %v:\n got %s\nwant %s", what, pos, a, b)
		}
	}
}

// renderChunkBEs renders a whole block entity array for a log line.
func renderChunkBEs(m map[[3]int]chunkBEEntry) string {
	pos := make([][3]int, 0, len(m))
	for p := range m {
		pos = append(pos, p)
	}
	sort.Slice(pos, func(i, j int) bool {
		for k := 0; k < 3; k++ {
			if pos[i][k] != pos[j][k] {
				return pos[i][k] < pos[j][k]
			}
		}
		return false
	})
	var b strings.Builder
	for _, p := range pos {
		fmt.Fprintf(&b, "\n  %v type %d %s", p, m[p].typ, renderNBT(m[p].nbt, true))
	}
	return b.String()
}
