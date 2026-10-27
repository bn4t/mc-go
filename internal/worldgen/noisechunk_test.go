package worldgen

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"strings"
	"testing"
)

// noiseBlockNames are the block states the probe serializes for each
// noise-stage block in the overworld.
var noiseBlockNames = map[NoiseBlock]string{
	NBAir:              "minecraft:air",
	NBDefault:          "minecraft:stone",
	NBWater:            "minecraft:water[level=0]",
	NBLava:             "minecraft:lava[level=0]",
	NBCopperOre:        "minecraft:copper_ore",
	NBRawCopperBlock:   "minecraft:raw_copper_block",
	NBGranite:          "minecraft:granite",
	NBDeepslateIronOre: "minecraft:deepslate_iron_ore",
	NBRawIronBlock:     "minecraft:raw_iron_block",
	NBTuff:             "minecraft:tuff",
}

// columnRuns renders one filled column the way the probe does: runs of
// equal blocks bottom to top, "from-to=state" separated by spaces.
func columnRuns(f *NoiseChunkFiller, blocks []NoiseBlock, x, z int32) string {
	var out []string
	start := f.minY
	top := f.minY + f.height - 1
	for y := f.minY; y <= top; y++ {
		b := blocks[f.NoiseColumnIndex(x, y, z)]
		if y == top || blocks[f.NoiseColumnIndex(x, y+1, z)] != b {
			out = append(out, fmt.Sprintf("%d-%d=%s", start, y, noiseBlockNames[b]))
			start = y + 1
		}
	}
	return strings.Join(out, " ")
}

// TestNoiseChunkFillMatchesVanilla fills the probe's chunks — eight per
// seed for three seeds — and compares every column with the official
// generator's noise stage: terrain and caves, aquifer water and lava
// with their barriers, and copper/iron ore veins.
func TestNoiseChunkFillMatchesVanilla(t *testing.T) {
	file, err := os.Open("testdata/chunk_fill.tsv.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := Settings("minecraft:overworld")
	type chunkKey struct {
		seed   string
		cx, cz int32
	}
	fillers := map[string]*NoiseChunkFiller{}
	filled := map[chunkKey][]NoiseBlock{}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	columns, bad := 0, 0
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		seed := parts[1]
		var cx, cz, x, z int32
		fmt.Sscanf(parts[2], "%d,%d", &cx, &cz)
		fmt.Sscanf(parts[3], "%d,%d", &x, &z)
		f, ok := fillers[seed]
		if !ok {
			f = NewNoiseChunkFiller(NewRandomState(settings, atoi64(t, seed)))
			fillers[seed] = f
		}
		key := chunkKey{seed, cx, cz}
		blocks, ok := filled[key]
		if !ok {
			blocks, _ = f.Fill(cx, cz)
			filled[key] = blocks
		}
		got := columnRuns(f, blocks, x, z)
		if got != parts[4] {
			bad++
			if bad <= 10 {
				t.Errorf("seed %s chunk %d,%d column %d,%d:\n  got  %s\n  want %s", seed, cx, cz, x, z, got, parts[4])
			}
		}
		columns++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if columns != 3*8*256 {
		t.Fatalf("checked %d columns", columns)
	}
	if bad > 0 {
		t.Fatalf("%d of %d columns differ", bad, columns)
	}
}
