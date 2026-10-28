//go:build blackbox

package blackbox

import (
	"strconv"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// readContainerIDs decodes one paletted container of n entries from a
// section stream: its per-entry ids (palette indices resolved).
func readContainerIDs(t *testing.T, r *protocol.PacketReader, n, directBits int) []int32 {
	t.Helper()
	bits, err := r.Byte()
	if err != nil {
		t.Fatalf("container bits: %v", err)
	}
	out := make([]int32, n)
	if bits == 0 {
		v, err := r.VarInt()
		if err != nil {
			t.Fatalf("single value: %v", err)
		}
		for i := range out {
			out[i] = v
		}
		return out
	}
	var palette []int32
	direct := (n == 64 && bits > 3) || (n == 4096 && bits > 8)
	if !direct {
		count, err := r.VarInt()
		if err != nil {
			t.Fatalf("palette length: %v", err)
		}
		for i := int32(0); i < count; i++ {
			v, err := r.VarInt()
			if err != nil {
				t.Fatalf("palette entry: %v", err)
			}
			palette = append(palette, v)
		}
	} else {
		bits = byte(directBits)
	}
	perLong := 64 / int(bits)
	longs := (n + perLong - 1) / perLong
	mask := uint64(1)<<bits - 1
	i := 0
	for l := 0; l < longs; l++ {
		v, err := r.Long()
		if err != nil {
			t.Fatalf("container data: %v", err)
		}
		u := uint64(v)
		for k := 0; k < perLong && i < n; k++ {
			id := int32(u >> (uint(k) * uint(bits)) & mask)
			if !direct {
				id = palette[id]
			}
			out[i] = id
			i++
		}
	}
	return out
}

// chunkBiomes decodes a Chunk Data body's per-section biome ids for a
// dimension of the given section count.
func chunkBiomes(t *testing.T, tag string, body []byte, sections int) (int32, int32, [][]int32) {
	t.Helper()
	x, z, _, rest := splitHeightmaps(t, tag, body)
	r := protocol.NewPacketReader(rest)
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("%s data length: %v", tag, err)
	}
	data, err := r.Read(int(n))
	if err != nil {
		t.Fatalf("%s data: %v", tag, err)
	}
	sr := protocol.NewPacketReader(data)
	var biomes [][]int32
	for sec := 0; sec < sections; sec++ {
		if _, err := sr.Short(); err != nil {
			t.Fatalf("%s block count: %v", tag, err)
		}
		if _, err := sr.Short(); err != nil {
			t.Fatalf("%s fluid count: %v", tag, err)
		}
		readContainerIDs(t, sr, 4096, 15)
		biomes = append(biomes, readContainerIDs(t, sr, 64, 7))
	}
	return x, z, biomes
}

// noiseDimensions are the stops of the biome parity tour: the
// overworld at the join, then a teleport into the nether and the End,
// each dimension with its section count.
var noiseDimensions = []struct {
	name, tp string
	sections int
}{
	{"minecraft:overworld", "", 24},
	{"minecraft:the_nether", "execute in minecraft:the_nether run tp @s 0 64 0", 16},
	{"minecraft:the_end", "execute in minecraft:the_end run tp @s 1200 64 0", 16},
}

// TestNoiseBiomeParity starts both servers on vanilla noise terrain
// (level-type normal, seed 12345, no structures) and compares the
// biome of every quart of every section of every chunk both send the
// joining client — in the overworld, then after teleporting into the
// nether and to the End's outer islands. Biomes are the generator's
// first stage — climate noise and the parameter search, the End's
// distance and erosion rule — so they are comparable before carvers
// and features exist.
func TestNoiseBiomeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          "12345",
		"generate-structures": "false",
	}, map[string]string{"ops.json": opsJSONFor("NB")}, func(s *server.Server) {
		s.LevelType = "normal"
		s.WorldSeed = 12345
	})
	defer targets.cleanup()
	type chunks = map[[2]int32][][]int32
	collect := func(label, addr string) []chunks {
		c, pr, pw := joinPlayBlackbox(t, addr, "NB")
		defer c.Close()
		sendClientInformation(t, pw)
		var out []chunks
		for i, dim := range noiseDimensions {
			if dim.tp != "" {
				sendCommand(t, pw, "gamemode spectator")
				sendCommand(t, pw, dim.tp)
			}
			got := chunks{}
			entered := dim.tp == ""
			deadline := time.Now().Add(40 * time.Second)
			for time.Now().Before(deadline) && len(got) < 289 {
				_ = c.SetReadDeadline(deadline)
				p, err := pr.ReadPacket()
				if err != nil {
					break
				}
				switch p.ID {
				case server.IDPlayRespawn:
					if f := decodeRespawnBlackbox(t, label, p.Data); f.dim == dim.name {
						entered = true
					}
				case server.IDPlayChunkData:
					if entered {
						x, z, b := chunkBiomes(t, label+" "+dim.name, p.Data, dim.sections)
						got[[2]int32{x, z}] = b
					}
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pw)
				case server.IDPlayKeepAliveCB:
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				}
			}
			if !entered {
				t.Fatalf("%s never entered %s (stop %d)", label, dim.name, i)
			}
			out = append(out, got)
		}
		_ = c.SetReadDeadline(time.Time{})
		return out
	}
	want := collect("vanilla", targets.vanilla)
	got := collect("mcgo", targets.mcgo)
	for i, dim := range noiseDimensions {
		common, quarts, diff := 0, 0, 0
		for pos, wb := range want[i] {
			gb, ok := got[i][pos]
			if !ok {
				continue
			}
			common++
			for sec := range wb {
				for q := range wb[sec] {
					quarts++
					if wb[sec][q] != gb[sec][q] {
						diff++
						if diff <= 10 {
							t.Errorf("%s chunk %v section %d quart %d: vanilla biome %d, mc-go %d", dim.name, pos, sec, q, wb[sec][q], gb[sec][q])
						}
					}
				}
			}
		}
		t.Logf("%s: vanilla sent %d chunks, mc-go %d, %d in common, %d quarts compared", dim.name, len(want[i]), len(got[i]), common, quarts)
		if common < 25 {
			t.Errorf("%s: only %d chunks in common", dim.name, common)
		}
		if diff > 0 {
			t.Errorf("%s: %d of %d quart biomes differ", dim.name, diff, quarts)
		}
	}
}

// TestNoiseWorldSpawnParity starts both servers on vanilla noise
// terrain for two seeds and compares the world spawn each reports to a
// joining player (Set Default Spawn Position): the climate spawn search,
// then the chunk spiral to the first respawnable column. The player
// itself is placed within the respawn radius of it.
func TestNoiseWorldSpawnParity(t *testing.T) {
	for _, seed := range []string{"12345", "1"} {
		t.Run(seed, func(t *testing.T) {
			n, _ := strconv.ParseInt(seed, 10, 64)
			targets := requireBlackboxFiles(t, map[string]string{
				"level-type":          "minecraft:normal",
				"generator-settings":  "{}",
				"level-seed":          seed,
				"generate-structures": "false",
			}, nil, func(s *server.Server) {
				s.LevelType = "normal"
				s.WorldSeed = n
			})
			defer targets.cleanup()
			type joined struct {
				spawn   [3]int32
				x, y, z float64
			}
			read := func(label, addr string) joined {
				c, pr, pw := joinPlayBlackbox(t, addr, "WS")
				defer c.Close()
				var j joined
				haveSpawn, havePos := false, false
				_ = c.SetReadDeadline(time.Now().Add(60 * time.Second))
				for !haveSpawn || !havePos {
					p, err := pr.ReadPacket()
					if err != nil {
						t.Fatalf("%s: %v", label, err)
					}
					r := protocol.NewPacketReader(p.Data)
					switch p.ID {
					case server.IDPlayDefaultSpawn:
						if _, err := r.String(); err != nil {
							t.Fatal(err)
						}
						x, y, z, err := r.Position()
						if err != nil {
							t.Fatal(err)
						}
						j.spawn, haveSpawn = [3]int32{x, y, z}, true
					case server.IDPlaySyncPos:
						if _, err := r.VarInt(); err != nil {
							t.Fatal(err)
						}
						j.x, _ = r.Double()
						j.y, _ = r.Double()
						j.z, _ = r.Double()
						havePos = true
					case server.IDPlayKeepAliveCB:
						_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
					}
				}
				return j
			}
			want := read("vanilla", targets.vanilla)
			got := read("mcgo", targets.mcgo)
			t.Logf("vanilla spawn %v player %.1f,%.1f,%.1f; mc-go spawn %v player %.1f,%.1f,%.1f",
				want.spawn, want.x, want.y, want.z, got.spawn, got.x, got.y, got.z)
			// The spawn column's height includes whatever trees stand on
			// it, and features that reach across a chunk border run in
			// the order vanilla's chunks load: on seed 1 vanilla has
			// reported the same column at 70 and, once, on a tree at 78.
			// The column must match; its height may differ by a tree.
			if want.spawn[0] != got.spawn[0] || want.spawn[2] != got.spawn[2] ||
				got.spawn[1] > want.spawn[1] || want.spawn[1]-got.spawn[1] > 10 {
				t.Errorf("world spawn: vanilla %v, mc-go %v", want.spawn, got.spawn)
			}
			for _, j := range []joined{want, got} {
				if dx, dz := j.x-float64(j.spawn[0])-0.5, j.z-float64(j.spawn[2])-0.5; dx < -10 || dx > 10 || dz < -10 || dz > 10 {
					t.Errorf("player at %.1f,%.1f is outside the respawn radius of %v", j.x, j.z, j.spawn)
				}
			}
		})
	}
}
