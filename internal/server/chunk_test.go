package server

import (
	"sort"
	"testing"

	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// testOverworld wraps a world in the overworld dimension shape for
// chunk-encoder tests.
func testOverworld(w *world.World) *dimension {
	return &dimension{
		name: dimOverworld, typeID: 0, w: w,
		seaLevel: -63, flat: true, coordScale: 1,
		minY: MinY, height: Height, skylight: true, biome: "minecraft:the_void",
	}
}

// readLong reads one big-endian long.
func readLong(t *testing.T, r *protocol.PacketReader) uint64 {
	t.Helper()
	raw, err := r.Read(8)
	if err != nil {
		t.Fatalf("long: %v", err)
	}
	var v uint64
	for _, b := range raw {
		v = v<<8 | uint64(b)
	}
	return v
}

// decodeContainer decodes one paletted container and returns the
// entries values in section order (x fastest, then z, then y).
func decodeContainer(t *testing.T, r *protocol.PacketReader, entries int, indirectMin int) []int32 {
	t.Helper()
	bpe, err := r.Byte()
	if err != nil {
		t.Fatalf("bpe: %v", err)
	}
	if bpe == 0 {
		v, err := r.VarInt()
		if err != nil {
			t.Fatalf("single value: %v", err)
		}
		out := make([]int32, entries)
		for i := range out {
			out[i] = v
		}
		return out
	}
	direct := false
	var pal []int32
	if entries == 4096 {
		direct = bpe >= 15
	} else {
		direct = bpe >= 7
	}
	if !direct {
		n, err := r.VarInt()
		if err != nil {
			t.Fatalf("palette len: %v", err)
		}
		pal = make([]int32, n)
		for i := range pal {
			pal[i], err = r.VarInt()
			if err != nil {
				t.Fatalf("palette: %v", err)
			}
		}
	}
	perLong := 64 / int(bpe)
	nLongs := (entries + perLong - 1) / perLong
	mask := uint64(1)<<bpe - 1
	out := make([]int32, 0, entries)
	for i := 0; i < nLongs; i++ {
		v := readLong(t, r)
		for k := 0; k < perLong && len(out) < entries; k++ {
			idx := (v >> uint(k*int(bpe))) & mask
			if direct {
				out = append(out, int32(idx))
			} else {
				out = append(out, pal[idx])
			}
		}
	}
	return out
}

func TestLightStructure(t *testing.T) {
	body := encodeLight(5, -3)
	r := protocol.NewPacketReader(body)
	if x, err := r.VarInt(); err != nil || x != 5 {
		t.Fatalf("light x = %d, err %v", x, err)
	}
	if z, err := r.VarInt(); err != nil || z != -3 {
		t.Fatalf("light z = %d, err %v", z, err)
	}
	mask := func() uint64 {
		t.Helper()
		n, err := r.VarInt()
		if err != nil || n != 1 {
			t.Fatalf("mask longs = %d, err %v (want 1)", n, err)
		}
		return readLong(t, r)
	}
	// Flat-world light, matching a real 26.2 server: explicit sky
	// arrays for the ground section and the one above it, empty sky
	// below the world, empty block light for the ground sections.
	if m := mask(); m != 0x6 {
		t.Fatalf("sky mask = %x, want 6", m)
	}
	if n, err := r.VarInt(); err != nil || n != 0 {
		t.Fatalf("block mask longs = %d (want 0)", n)
	}
	if m := mask(); m != 0x1 {
		t.Fatalf("empty sky mask = %x, want 1", m)
	}
	if m := mask(); m != 0x7 {
		t.Fatalf("empty block mask = %x, want 7", m)
	}
	total, err := r.VarInt()
	if err != nil || total != 2 {
		t.Fatalf("sky arrays = %d, err %v (want 2)", total, err)
	}
	for i := 0; i < 2; i++ {
		n, err := r.VarInt()
		if err != nil || n != 2048 {
			t.Fatalf("sky array %d len = %d, err %v", i, n, err)
		}
		raw, err := r.Read(2048)
		if err != nil {
			t.Fatalf("sky array %d: %v", i, err)
		}
		for j, v := range raw {
			want := byte(0xFF)
			if i == 0 && j < 512 {
				want = 0x00 // four solid layers keep sky light 0
			}
			if v != want {
				t.Fatalf("sky array %d byte %d = %#x, want %#x", i, j, v, want)
			}
		}
	}
	if n, err := r.VarInt(); err != nil || n != 0 {
		t.Fatalf("block arrays = %d (want 0)", n)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing bytes: %d", r.Remaining())
	}
}

func TestChunkRoundTrip(t *testing.T) {
	w := world.New(event.New(), 4)
	w.Start()
	defer w.Stop()
	w.SetBlock(world.Pos{X: 3, Y: 70, Z: 3}, world.Stone) // override above ground

	body := encodeChunk(testOverworld(w), 0, 0, nil)
	r := protocol.NewPacketReader(body)
	x, err := r.Int()
	if err != nil || x != 0 {
		t.Fatalf("chunk x = %d, err %v", x, err)
	}
	if z, err := r.Int(); err != nil || z != 0 {
		t.Fatalf("chunk z = %d, err %v", z, err)
	}
	// Heightmaps: 3 prefixed entries of 37 padded longs each.
	hmCount, err := r.VarInt()
	if err != nil || hmCount != 3 {
		t.Fatalf("heightmaps = %d, err %v", hmCount, err)
	}
	for i := int32(0); i < hmCount; i++ {
		typ, err := r.VarInt()
		if err != nil || (typ != 1 && typ != 4 && typ != 5) {
			t.Fatalf("heightmap %d type = %d, err %v", i, typ, err)
		}
		nLongs, err := r.VarInt()
		if err != nil || nLongs != 37 {
			t.Fatalf("heightmap %d longs = %d, err %v", i, nLongs, err)
		}
		longs := make([]uint64, nLongs)
		for j := range longs {
			longs[j] = readLong(t, r)
		}
		// Entry (z*16+x) is value 4 (grass top at y=-61) except the
		// overridden column (3,3) which is 135 (y=70).
		get := func(x, z int) int32 {
			idx := z*16 + x
			li, shift := idx/7, uint(idx%7)*9
			return int32((longs[li] >> shift) & 0x1FF)
		}
		if got := get(0, 0); got != 4 {
			t.Fatalf("heightmap %d (0,0) = %d, want 4", typ, got)
		}
		if got := get(3, 3); got != 135 {
			t.Fatalf("heightmap %d (3,3) = %d, want 135", typ, got)
		}
	}
	// Data blob: length-prefixed, 24 sections.
	dataLen, err := r.VarInt()
	if err != nil {
		t.Fatalf("data len: %v", err)
	}
	data, err := r.Read(int(dataLen))
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	dr := protocol.NewPacketReader(data)
	for sy := 0; sy < Sections; sy++ {
		bc, err := dr.Short()
		if err != nil {
			t.Fatalf("section %d block count: %v", sy, err)
		}
		if _, err := dr.Short(); err != nil {
			t.Fatalf("section %d fluid count: %v", sy, err)
		}
		wantCount := int16(0)
		if sy == 0 {
			wantCount = 1024 // bedrock + 2 dirt + grass layers
		}
		if sy == 8 {
			wantCount = 1 // the override at y=70
		}
		if bc != wantCount {
			t.Fatalf("section %d block count = %d, want %d", sy, bc, wantCount)
		}
		vals := decodeContainer(t, dr, 4096, 4)
		bio := decodeContainer(t, dr, 64, 1)
		if got := bio[0]; got != biomeID("minecraft:the_void") {
			t.Fatalf("section %d biome = %d", sy, got)
		}
		at := func(x, y, z int) int32 { return vals[(y*16+z)*16+x] }
		switch sy {
		case 0:
			if at(0, 0, 0) != blockState(world.BlockStateMust("bedrock")) {
				t.Fatalf("bedrock layer wrong: %d", at(0, 0, 0))
			}
			if at(8, 3, 8) != StateGrass {
				t.Fatalf("grass at y=-61 = %d", at(8, 3, 8))
			}
			if at(8, 4, 8) != StateAir {
				t.Fatalf("above ground not air")
			}
		case 8:
			if at(3, 70-(MinY+sy*16), 3) != StateStone {
				t.Fatalf("override missing: %d", at(3, 6, 3))
			}
		}
	}
	if dr.Remaining() != 0 {
		t.Fatalf("data trailing: %d", dr.Remaining())
	}
	if n, err := r.VarInt(); err != nil || n != 0 {
		t.Fatalf("block entities = %d, err %v (want 0)", n, err)
	}
	// Inline light: sky mask with ground+above arrays.
	if n, err := r.VarInt(); err != nil || n != 1 {
		t.Fatalf("inline light sky mask longs = %d", n)
	}
	// Ground layers {0,1} + the y=70 override's newly eligible
	// neighbourhood {7,8,9} — vanilla's sectioned model materializes
	// one section around every non-empty block section.
	if m := readLong(t, r); m != 0x706 {
		t.Fatalf("inline light sky mask = %x, want 706", m)
	}
	if r.Remaining() == 0 {
		t.Fatal("inline light missing")
	}
}

func TestForgetEncoding(t *testing.T) {
	body := encodeForget(-3, 17)
	r := protocol.NewPacketReader(body)
	v, err := r.Long()
	if err != nil {
		t.Fatalf("forget long: %v", err)
	}
	if x := int32(uint64(v) & 0xFFFFFFFF); x != -3 {
		t.Fatalf("forget x = %d", x)
	}
	if z := int32(uint64(v) >> 32); z != 17 {
		t.Fatalf("forget z = %d", z)
	}
	if r.Remaining() != 0 {
		t.Fatalf("trailing = %d", r.Remaining())
	}
}

func TestForgetChunkOnFarMove(t *testing.T) {
	s := New("unused")
	c, pr, pw := joinPlay(t, s, "Walker")
	defer c.Close()
	drainJoinBurst(t, pr)
	finishJoin(t, s, pr, pw)
	next := movementPackets(pr, pw)

	// Jump from chunk (0,0) to chunk (10,0): the columns of the window
	// around the new column that the old window did not hold stream as
	// chunk packets (light rides inside them), and every column the old
	// window held is past the prune border and is forgotten.
	sendPosRot(t, pw, 168.5, -60, 8.5, 0, 0, true)
	fresh := freshColumns(2, 0, 0, 10, 0)
	if len(fresh) == 0 {
		t.Fatal("the jump streams nothing")
	}
	readMovementChunkCenter(t, next, 10, 0)
	readMovementChunkBatchStart(t, next)
	for i := 0; i < len(fresh); i++ {
		ch, err := next()
		if err != nil || ch.ID != IDPlayChunkData {
			t.Fatalf("stream chunk %d = %#x, err %v", i, ch.ID, err)
		}
	}
	if p, err := next(); err != nil || p.ID != IDPlayChunkBatchDone {
		t.Fatalf("batch done = %#x, err %v", p.ID, err)
	}
	want := forgotColumns(2, 0, 0, 10, 0)
	if len(want) == 0 {
		t.Fatal("the jump forgets nothing")
	}
	for i, w := range want {
		p, err := next()
		if err != nil || p.ID != IDPlayForgetChunk {
			t.Fatalf("forget %d = %#x, err %v", i, p.ID, err)
		}
		v, err := protocol.NewPacketReader(p.Data).Long()
		if err != nil {
			t.Fatalf("forget %d long: %v", i, err)
		}
		x, z := int32(uint64(v)&0xFFFFFFFF), int32(uint64(v)>>32)
		if x != w[0] || z != w[1] {
			t.Fatalf("forget %d = %d,%d want %d,%d", i, x, z, w[0], w[1])
		}
	}
}

// freshColumns lists what a move from one column to another takes on:
// the columns the streamed window around the new column holds and the
// one around the old column did not, in the order sendWindow queues them.
//
// The window rule itself is pinned against the real server by
// TestChunkWindowParity, so the tests here derive their expectations from
// it rather than listing columns that a change in shape would strand.
func freshColumns(viewDistance, fromCX, fromCZ, toCX, toCZ int) [][2]int32 {
	var out [][2]int32
	for dx := -viewDistance - 1; dx <= viewDistance+1; dx++ {
		for dz := -viewDistance - 1; dz <= viewDistance+1; dz++ {
			if !chunkInWindow(viewDistance, dx, dz) {
				continue
			}
			cx, cz := toCX+dx, toCZ+dz
			if chunkInWindow(viewDistance, cx-fromCX, cz-fromCZ) {
				continue
			}
			out = append(out, [2]int32{int32(cx), int32(cz)})
		}
	}
	return out
}

// forgotColumns lists what the same move forgets: every column of the
// old window past the prune border, told to the client in the ascending
// (x, z) order the forget pass sorts to.
func forgotColumns(viewDistance, fromCX, fromCZ, toCX, toCZ int) [][2]int32 {
	prune := viewDistance + pruneBorder
	var out [][2]int32
	for dx := -viewDistance - 1; dx <= viewDistance+1; dx++ {
		for dz := -viewDistance - 1; dz <= viewDistance+1; dz++ {
			if !chunkInWindow(viewDistance, dx, dz) {
				continue
			}
			cx, cz := fromCX+dx, fromCZ+dz
			if chunkInWindow(prune, cx-toCX, cz-toCZ) {
				continue
			}
			out = append(out, [2]int32{int32(cx), int32(cz)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}
