//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// wireChunk is a Chunk Data packet's section data decoded to global
// ids: 4096 block states and 64 biome cells per section.
type wireChunk struct {
	x, z     int32
	hms      map[int32][]int64
	blocks   [][]int32
	biomes   [][]int32
	counts   [][2]int16
	spawnPos [3]int32
}

// readPaletted decodes one PalettedContainer (1.21.5+: no data-array
// length prefix) of n entries.
func readPaletted(r *protocol.PacketReader, n int) ([]int32, error) {
	bpe, err := r.Byte()
	if err != nil {
		return nil, err
	}
	out := make([]int32, n)
	if bpe == 0 {
		v, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i] = v
		}
		return out, nil
	}
	var palette []int32
	direct := (n == 4096 && bpe > 8) || (n == 64 && bpe > 3)
	if !direct {
		pn, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		palette = make([]int32, pn)
		for i := range palette {
			if palette[i], err = r.VarInt(); err != nil {
				return nil, err
			}
		}
	}
	per := 64 / int(bpe)
	longs := (n + per - 1) / per
	for li := 0; li < longs; li++ {
		l, err := r.Long()
		if err != nil {
			return nil, err
		}
		for j := 0; j < per && li*per+j < n; j++ {
			v := int32((uint64(l) >> (uint(j) * uint(bpe))) & (1<<uint(bpe) - 1))
			if !direct {
				if int(v) >= len(palette) {
					return nil, fmt.Errorf("palette index %d of %d", v, len(palette))
				}
				v = palette[v]
			}
			out[li*per+j] = v
		}
	}
	return out, nil
}

func parseWireChunk(t *testing.T, tag string, body []byte, sections int) wireChunk {
	t.Helper()
	x, z, hms, rest := splitHeightmaps(t, tag, body)
	c := wireChunk{x: x, z: z, hms: hms}
	r := protocol.NewPacketReader(rest)
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("%s data length: %v", tag, err)
	}
	data, err := r.Read(int(n))
	if err != nil {
		t.Fatalf("%s data: %v", tag, err)
	}
	dr := protocol.NewPacketReader(data)
	for s := 0; s < sections; s++ {
		bc, err1 := dr.Short()
		fc, err2 := dr.Short()
		if err1 != nil || err2 != nil {
			t.Fatalf("%s chunk %d,%d section %d counts: %v %v", tag, x, z, s, err1, err2)
		}
		bl, err := readPaletted(dr, 4096)
		if err != nil {
			t.Fatalf("%s chunk %d,%d section %d blocks: %v", tag, x, z, s, err)
		}
		bi, err := readPaletted(dr, 64)
		if err != nil {
			t.Fatalf("%s chunk %d,%d section %d biomes: %v", tag, x, z, s, err)
		}
		c.blocks = append(c.blocks, bl)
		c.biomes = append(c.biomes, bi)
		c.counts = append(c.counts, [2]int16{bc, fc})
	}
	if dr.Remaining() != 0 {
		t.Fatalf("%s chunk %d,%d: %d trailing section bytes", tag, x, z, dr.Remaining())
	}
	return c
}

// collectJoin drains a joined client's window: chunks keyed by
// position and the default spawn position.
func collectJoin(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer) (map[[2]int32][]byte, [3]int32) {
	t.Helper()
	chunks := map[[2]int32][]byte{}
	var spawn [3]int32
	// A normal world never goes quiet (animals move, time ticks), so
	// the window counts as streamed once no chunk came for a while.
	start := time.Now()
	last, quiet := start, 3*time.Minute
	for time.Since(last) < quiet {
		_ = c.SetReadDeadline(time.Now().Add(quiet))
		p, err := pr.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			t.Fatalf("%s stream: %v", tag, err)
		}
		switch p.ID {
		case server.IDPlayChunkData:
			r := protocol.NewPacketReader(p.Data)
			x, _ := r.Int()
			z, _ := r.Int()
			if len(chunks) == 0 {
				t.Logf("%s: first chunk after %v", tag, time.Since(start))
			}
			chunks[[2]int32{x, z}] = p.Data
			last, quiet = time.Now(), 20*time.Second
		case server.IDPlayDefaultSpawn:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.String(); err == nil {
				if v, err := r.Long(); err == nil {
					spawn = [3]int32{int32(v >> 38), int32(v << 52 >> 52), int32(v << 26 >> 38)}
				}
			}
		case server.IDPlayChunkBatchDone:
			if err := ackChunkBatch(pw); err != nil {
				t.Fatal(err)
			}
		case server.IDPlayKeepAliveCB:
			if err := pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); err != nil {
				t.Fatal(err)
			}
		case server.IDPlaySyncPos:
			if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
	t.Logf("%s: %d chunks in %v", tag, len(chunks), last.Sub(start))
	return chunks, spawn
}

// TestNormalWorldChunkParity joins a normal-terrain world on both
// servers (same seed, features stripped by the same data pack, carvers
// and surface on) and compares the world spawn and every chunk both
// windows streamed: heightmaps, section counts, block states, biome
// cells, sky and block light and the light masks. Fluid cells are exempt: vanilla lets
// carved-open water and lava flow once the chunk ticks, which mc-go
// does not model yet.
func TestNormalWorldChunkParity(t *testing.T) {
	normalWorldParity(t, "", 24, 0)
}

// TestNormalWorldSettledParity lets the generated chunks tick for a
// minute (generation post-processing ticks every marked fluid once, and
// the flows run on: cave waterfalls, lava falls, obsidian where they
// meet), rejoins, and compares the fresh window strictly — no fluid
// exemption.
func TestNormalWorldSettledParity(t *testing.T) {
	normalWorldParity(t, "", 24, time.Minute)
}

// TestNormalNetherChunkParity teleports into the generated nether and
// compares its streamed chunks the same way (block light from the lava
// sea and lava pockets included).
func TestNormalNetherChunkParity(t *testing.T) {
	normalWorldParity(t, "execute in minecraft:the_nether run tp @s 40.5 70 -20.5", 16, 0)
}

// TestNormalEndChunkParity does the same in the end's outer islands
// (away from the origin, where the dragon fight builds the exit
// portal).
func TestNormalEndChunkParity(t *testing.T) {
	normalWorldParity(t, "execute in minecraft:the_end run tp @s 1400.5 70 1400.5", 16, 0)
}

func normalWorldParity(t *testing.T, tp string, sections int, settle time.Duration) {
	const seed = 12345
	ov := worldgen.StageOverrides(worldgen.Vanilla(), func(string) bool { return false }, true, true)
	props := map[string]string{
		"level-type":         "minecraft:normal",
		"generator-settings": "{}",
		"level-seed":         fmt.Sprint(seed),
		// Structures are not generated by mc-go yet.
		"generate-structures": "false",
	}
	files := stageDatapack(ov)
	files["ops.json"] = opsJSONFor("TerrainProbe")
	tg := requireBlackboxFiles(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.WorldSeed = seed
		s.WorldgenOverlay = ov
	})
	defer tg.cleanup()
	t.Logf("vanilla %s, mc-go %s", tg.vanilla, tg.mcgo)
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute

	fetch := func(tag, addr string) (map[[2]int32][]byte, [3]int32) {
		c, pr, pw := joinPlayBlackbox(t, addr, "TerrainProbe")
		defer c.Close()
		if settle > 0 {
			// Random ticks (grass spreading onto dirt and dying back)
			// run on the level's runtime random; only the deterministic
			// fluid flow is under test.
			sendCommand(t, pw, "gamerule random_tick_speed 0")
		}
		chunks, spawn := collectJoin(t, tag, c, pr, pw)
		if tp != "" {
			sendCommand(t, pw, tp)
			chunks, _ = collectJoin(t, tag+" after tp", c, pr, pw)
		}
		if settle > 0 {
			idleClient(t, tag, c, pr, pw, settle)
			c.Close()
			c2, pr2, pw2 := joinPlayBlackbox(t, addr, "TerrainProbe")
			defer c2.Close()
			chunks, _ = collectJoin(t, tag+" rejoin", c2, pr2, pw2)
		}
		return chunks, spawn
	}
	vChunks, vSpawn := fetch("vanilla", tg.vanilla)
	mChunks, mSpawn := fetch("mc-go", tg.mcgo)
	t.Logf("spawn: vanilla %v mc-go %v; chunks vanilla %d mc-go %d", vSpawn, mSpawn, len(vChunks), len(mChunks))
	if vSpawn != mSpawn {
		t.Errorf("world spawn: vanilla %v mc-go %v", vSpawn, mSpawn)
	}

	compareChunkStreams(t, vChunks, mChunks, sections, settle == 0)
}

// compareChunkStreams compares every chunk two servers both streamed:
// block states (fluid cells exempt when exemptFluids), biome cells,
// heightmaps, section counts, sky and block light and the light masks.
func compareChunkStreams(t *testing.T, vChunks, mChunks map[[2]int32][]byte, sections int, exemptFluids bool) {
	t.Helper()
	minY := -64
	if sections == 16 {
		minY = 0
	}
	mapping, _ := world.BlockMapping(server.ProtocolVersion)
	fluid := func(wire int32) bool {
		b, ok := mapping.FromWire(wire)
		return ok && worldgen.HasFluid(b)
	}
	name := func(wire int32) string {
		b, ok := mapping.FromWire(wire)
		if !ok {
			return fmt.Sprint(wire)
		}
		n, _ := world.StateOwner(b)
		return n
	}
	compared, fluidCells, skyDiff, blockDiff, maskDiff := 0, 0, 0, 0, 0
	for k, vb := range vChunks {
		mb, ok := mChunks[k]
		if !ok {
			continue
		}
		compared++
		v := parseWireChunk(t, "vanilla", vb, sections)
		m := parseWireChunk(t, "mc-go", mb, sections)
		vl, ml := parseChunkView(t, "vanilla", vb), parseChunkView(t, "mc-go", mb)
		for sec := range vl.block {
			// A section on neither block mask is dark.
			if vl.block[sec] == nil {
				vl.block[sec] = make([]byte, 2048)
			}
			if ml.block[sec] == nil {
				ml.block[sec] = make([]byte, 2048)
			}
		}
		if vm, mm := lightMasks(t, vb), lightMasks(t, mb); fmt.Sprint(vm) != fmt.Sprint(mm) {
			maskDiff++
			if maskDiff <= 3 {
				t.Logf("chunk %v light masks (sky, block, empty sky, empty block): vanilla %v mc-go %v", k, vm, mm)
			}
		}
		for sec := 0; sec < sections; sec++ {
			if !bytes.Equal(vl.sky[sec], ml.sky[sec]) {
				skyDiff++
				logLightDiff(t, minY, "sky", k, sec, vl.sky[sec], ml.sky[sec], v.blocks, name)
			}
			if !bytes.Equal(vl.block[sec], ml.block[sec]) {
				blockDiff++
				logLightDiff(t, minY, "block", k, sec, vl.block[sec], ml.block[sec], v.blocks, name)
			}
		}
		bad := 0
		dirtyCols := map[int]bool{}
		for s := 0; s < sections; s++ {
			for i := range v.blocks[s] {
				a, b := v.blocks[s][i], m.blocks[s][i]
				if a == b {
					continue
				}
				if exemptFluids && (fluid(a) || fluid(b)) {
					fluidCells++
					dirtyCols[i&255] = true
					continue
				}
				if bad < 5 {
					t.Errorf("chunk %v block (%d,%d,%d): vanilla %s mc-go %s", k, i&15, s*16+i>>8+minY, i>>4&15, name(a), name(b))
				}
				bad++
			}
			for i := range v.biomes[s] {
				if v.biomes[s][i] != m.biomes[s][i] {
					t.Errorf("chunk %v section %d biome cell %d: vanilla %d mc-go %d", k, s, i, v.biomes[s][i], m.biomes[s][i])
					break
				}
			}
		}
		if bad > 0 {
			t.Errorf("chunk %v: %d block mismatches", k, bad)
			continue
		}
		for typ, vv := range v.hms {
			if !heightmapsAgree(vv, m.hms[typ], dirtyCols) {
				t.Errorf("chunk %v heightmap %d differs", k, typ)
			}
		}
		if len(dirtyCols) == 0 {
			for s := range v.counts {
				if v.counts[s] != m.counts[s] {
					t.Errorf("chunk %v section %d counts: vanilla %v mc-go %v", k, s, v.counts[s], m.counts[s])
				}
			}
		}
	}
	t.Logf("compared %d chunks; %d fluid cells exempt; light sections differing: sky %d block %d; light masks differing in %d chunks", compared, fluidCells, skyDiff, blockDiff, maskDiff)
	if skyDiff+blockDiff+maskDiff > 0 {
		t.Errorf("light differs: %d sky sections, %d block sections, masks in %d chunks", skyDiff, blockDiff, maskDiff)
	}
	if compared < 25 {
		t.Fatalf("only %d chunks overlapped", compared)
	}
}

// heightmapsAgree compares two packed 9-bit heightmaps column by
// column, skipping the columns in skip.
func heightmapsAgree(a, b []int64, skip map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for col := 0; col < 256; col++ {
		if skip[col] {
			continue
		}
		li, sh := col/7, uint(col%7)*9
		if (a[li]>>sh)&511 != (b[li]>>sh)&511 {
			return false
		}
	}
	return true
}

// logLightDiff logs how one light section differs.
func logLightDiff(t *testing.T, minY int, kind string, k [2]int32, sec int, v, m []byte, blocks [][]int32, name func(int32) string) {
	t.Helper()
	n, first := 0, -1
	for i := 0; i < 4096; i++ {
		a, b := v[i/2]>>(uint(i%2)*4)&15, m[i/2]>>(uint(i%2)*4)&15
		if a != b {
			if first < 0 {
				first = i
			}
			n++
		}
	}
	i := first
	above := "-"
	if i+256 < 4096 {
		above = name(blocks[sec][i+256])
	}
	t.Logf("%s light chunk %v section %d: %d cells differ; first (%d,%d,%d) %s (above %s) vanilla %d mc-go %d", kind, k, sec, n,
		i&15, sec*16+i>>8+minY, i>>4&15, name(blocks[sec][i]), above, v[i/2]>>(uint(i%2)*4)&15, m[i/2]>>(uint(i%2)*4)&15)
}

// lightMasks returns a chunk packet's four light masks.
func lightMasks(t *testing.T, body []byte) [4][]uint64 {
	t.Helper()
	_, _, _, rest := splitHeightmaps(t, "masks", body)
	r := protocol.NewPacketReader(rest)
	n, _ := r.VarInt()
	r.Read(int(n))
	r.VarInt() // block entities (none without structures)
	var out [4][]uint64
	for i := range out {
		out[i] = readLightMask(t, "masks", r)
	}
	return out
}

// idleClient keeps a joined client alive (keep-alives, chunk batch
// acknowledgements, teleport confirms) for d.
func idleClient(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		_ = c.SetReadDeadline(end)
		p, err := pr.ReadPacket()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			t.Fatalf("%s idle: %v", tag, err)
		}
		switch p.ID {
		case server.IDPlayChunkBatchDone:
			if err := ackChunkBatch(pw); err != nil {
				t.Fatal(err)
			}
		case server.IDPlayKeepAliveCB:
			if err := pw.WritePacket(server.IDPlayKeepAliveSB, p.Data); err != nil {
				t.Fatal(err)
			}
		case server.IDPlaySyncPos:
			if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				if err := pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	_ = c.SetReadDeadline(time.Time{})
}
