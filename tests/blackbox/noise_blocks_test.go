//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// chunkBlocks decodes a Chunk Data body's block states, per section.
func chunkBlocks(t *testing.T, tag string, body []byte, sections int) (int32, int32, [][]int32) {
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
	var blocks [][]int32
	for sec := 0; sec < sections; sec++ {
		if _, err := sr.Short(); err != nil {
			t.Fatalf("%s block count: %v", tag, err)
		}
		if _, err := sr.Short(); err != nil {
			t.Fatalf("%s fluid count: %v", tag, err)
		}
		blocks = append(blocks, readContainerIDs(t, sr, 4096, 15))
		readContainerIDs(t, sr, 64, 7)
	}
	return x, z, blocks
}

// TestNoiseTerrainBlocksParity starts both servers on vanilla noise
// terrain without structures and compares every block of the chunks
// both send a player at spawn: noise, surface, carvers and every
// decoration feature.
//
// Features that reach across a chunk border see whatever the
// neighbouring chunks' features left, and vanilla runs those in the
// order its chunks happen to load: two vanilla servers on this seed
// differ in 0.42% of these blocks (MCGO_VANILLA_SELF=1 measures it
// again). mc-go runs each chunk's features against undecorated
// neighbours, exactly as vanilla would there, and differs in about 1.1%;
// the test holds it under 1.5%. MCGO_DUMP_CHUNKS=dir writes both
// servers' blocks for offline analysis.
func TestNoiseTerrainBlocksParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          "12345",
		"generate-structures": "false",
	}, nil, func(s *server.Server) {
		s.LevelType = "normal"
		s.WorldSeed = 12345
	})
	defer targets.cleanup()
	const sections = 24
	collect := func(label, addr string) map[[2]int32][][]int32 {
		c, pr, pw := joinPlayBlackbox(t, addr, "NT")
		defer c.Close()
		sendClientInformation(t, pw)
		got := map[[2]int32][][]int32{}
		deadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(deadline) && len(got) < 121 {
			_ = c.SetReadDeadline(deadline)
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayChunkData:
				x, z, b := chunkBlocks(t, label, p.Data, sections)
				got[[2]int32{x, z}] = b
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			}
		}
		_ = c.SetReadDeadline(time.Time{})
		return got
	}
	want := collect("vanilla", targets.vanilla)
	got := collect("mcgo", targets.mcgo)
	if dir := os.Getenv("MCGO_DUMP_CHUNKS"); dir != "" {
		for label, m := range map[string]map[[2]int32][][]int32{"vanilla": want, "mcgo": got} {
			var rows []string
			for pos, secs := range m {
				var ids []string
				for _, sec := range secs {
					for _, id := range sec {
						ids = append(ids, strconv.Itoa(int(id)))
					}
				}
				rows = append(rows, fmt.Sprintf("%d,%d\t%s", pos[0], pos[1], strings.Join(ids, ",")))
			}
			if err := os.WriteFile(filepath.Join(dir, label+".tsv"), []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	common, blocks, diff := compareChunkBlocks(t, "vanilla against mc-go", want, got)
	if os.Getenv("MCGO_VANILLA_SELF") != "" {
		// Measure vanilla against itself: a second vanilla on the same
		// seed generates its spawn area in its own chunk order.
		port, ok := freePort()
		if !ok {
			t.Fatal("no port for the second vanilla")
		}
		addr, stop := startVanillaCfgFiles(t, blackboxJar(t), port, map[string]string{
			"level-type":          "minecraft:normal",
			"generator-settings":  "{}",
			"level-seed":          "12345",
			"generate-structures": "false",
		}, nil)
		again := collect("vanilla again", addr)
		stop()
		compareChunkBlocks(t, "vanilla against vanilla", want, again)
	}
	if common < 90 {
		t.Fatalf("only %d chunks in common", common)
	}
	if float64(diff) > 0.015*float64(blocks) {
		t.Errorf("%d of %d blocks differ (%.2f%%), over 1.5%%", diff, blocks, 100*float64(diff)/float64(blocks))
	}
}

// compareChunkBlocks logs how the blocks of the chunks both sides sent
// differ, and returns the counts.
func compareChunkBlocks(t *testing.T, label string, want, got map[[2]int32][][]int32) (common, blocks, diff int) {
	t.Helper()
	mapping, _ := world.BlockMapping(server.ProtocolVersion)
	name := func(id int32) string {
		if s, ok := mapping.FromWire(id); ok {
			if k, ok := mapping.Catalog().State(s); ok {
				return fmt.Sprint(k)
			}
		}
		return fmt.Sprint(id)
	}
	pairs := map[[2]int32]int{}
	// byEdge counts differences by a block's distance to its chunk's
	// nearest side: features that cross a border reach a few blocks in.
	var byEdge [8]int
	for pos, wb := range want {
		gb, ok := got[pos]
		if !ok {
			continue
		}
		common++
		for sec := range wb {
			for i := range wb[sec] {
				blocks++
				if wb[sec][i] != gb[sec][i] {
					diff++
					x, z := i&15, i>>4&15
					byEdge[min(x, 15-x, z, 15-z)]++
					pairs[[2]int32{wb[sec][i], gb[sec][i]}]++
					if diff <= 5 {
						t.Logf("chunk %v section %d index %d: vanilla %s, mc-go %s", pos, sec, i, name(wb[sec][i]), name(gb[sec][i]))
					}
				}
			}
		}
	}
	type pair struct {
		k [2]int32
		n int
	}
	var top []pair
	for k, n := range pairs {
		top = append(top, pair{k, n})
	}
	sort.Slice(top, func(a, b int) bool { return top[a].n > top[b].n })
	for i, p := range top {
		if i == 12 {
			break
		}
		t.Logf("%6d× vanilla %s, mc-go %s", p.n, name(p.k[0]), name(p.k[1]))
	}
	t.Logf("%s: differences by distance to the chunk side: %v", label, byEdge)
	t.Logf("%s: %d and %d chunks sent, %d in common, %d of %d blocks differ", label, len(want), len(got), common, diff, blocks)
	return common, blocks, diff
}
