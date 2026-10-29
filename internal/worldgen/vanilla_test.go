package worldgen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// vanillaChunk decodes one saved chunk's block states into names
// ("stone", "water[level=0]") indexed ((y-minY)*16+z)*16+x.
func vanillaChunk(t *testing.T, regionDir string, cx, cz int32, minY, height int32) ([]string, map[string]any) {
	t.Helper()
	rx, rz := cx>>5, cz>>5
	chunks, err := world.ReadRegionFile(filepath.Join(regionDir, fmt.Sprintf("r.%d.%d.mca", rx, rz)))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := chunks[[2]int32{cx & 31, cz & 31}]
	if !ok {
		t.Fatalf("chunk %d,%d not in region", cx, cz)
	}
	out := make([]string, 16*16*height)
	sections, _ := c["sections"].(nbt.List)
	for _, sAny := range sections.Items {
		sec := sAny.(map[string]any)
		sy := int32(sec["Y"].(int8))
		states, ok := sec["block_states"].(map[string]any)
		if !ok {
			continue
		}
		pal := states["palette"].(nbt.List)
		names := make([]string, len(pal.Items))
		for i, pAny := range pal.Items {
			pc := pAny.(map[string]any)
			name := strings.TrimPrefix(pc["Name"].(string), "minecraft:")
			if pm, ok := pc["Properties"].(map[string]any); ok {
				var kv []string
				for k, v := range pm {
					kv = append(kv, k+"="+v.(string))
				}
				sort.Strings(kv)
				name += "[" + strings.Join(kv, ",") + "]"
			}
			names[i] = name
		}
		idx := make([]int32, 4096)
		if data, ok := states["data"].([]int64); ok && len(names) > 1 {
			bpe := 4
			for 1<<uint(bpe) < len(names) {
				bpe++
			}
			per := 64 / bpe
			mask := int64(1<<uint(bpe)) - 1
			for i := 0; i < 4096; i++ {
				idx[i] = int32((data[i/per] >> (uint(i%per) * uint(bpe))) & mask)
			}
		}
		for i := 0; i < 4096; i++ {
			y := sy*16 + int32(i>>8)
			if y < minY || y >= minY+height {
				continue
			}
			z := int32(i>>4) & 15
			x := int32(i) & 15
			out[((y-minY)*16+z)*16+x] = names[idx[i]]
		}
	}
	return out, c
}

var substanceNames = map[Substance]string{
	SubDefault: "stone", SubAir: "air", SubWater: "water[level=0]", SubLava: "lava[level=0]",
	SubCopperOre: "copper_ore", SubRawCopperBlock: "raw_copper_block", SubGranite: "granite",
	SubDeepslateIronOre: "deepslate_iron_ore", SubRawIronBlock: "raw_iron_block", SubTuff: "tuff",
}

// TestNoiseVsVanillaRegion compares the noise stage against a vanilla
// world generated with surface rules, carvers and features stripped
// (MCGO_WORLDGEN_REGION=<world>/dimensions/minecraft/overworld/region,
// seed MCGO_WORLDGEN_SEED, default 1).
func TestNoiseVsVanillaRegion(t *testing.T) {
	dir := os.Getenv("MCGO_WORLDGEN_REGION")
	if dir == "" {
		t.Skip("MCGO_WORLDGEN_REGION not set")
	}
	seed := int64(1)
	if s := os.Getenv("MCGO_WORLDGEN_SEED"); s != "" {
		fmt.Sscan(s, &seed)
	}
	rs, err := NewRandomState(Vanilla(), "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	areas := "0,0,3,3"
	if a := os.Getenv("MCGO_WORLDGEN_CHUNKS"); a != "" {
		areas = a
	}
	var chunks [][2]int32
	for _, a := range strings.Split(areas, ";") {
		var x0, z0, x1, z1 int32
		fmt.Sscanf(a, "%d,%d,%d,%d", &x0, &z0, &x1, &z1)
		for cx := x0; cx <= x1; cx++ {
			for cz := z0; cz <= z1; cz++ {
				chunks = append(chunks, [2]int32{cx, cz})
			}
		}
	}
	total := 0
	kinds := map[string]int{}
	defer func() { t.Logf("mismatch kinds: %v", kinds) }()
	for _, ch := range chunks {
		cx, cz := ch[0], ch[1]
		{
			want, _ := vanillaChunk(t, dir, cx, cz, rs.MinY(), rs.Height())
			got := rs.FillNoise(cx, cz)
			bad := 0
			for i, w := range want {
				g := substanceNames[got.Cells[i]]
				if g != w {
					kinds[g+"->"+w]++
					if bad < 3 {
						y := int32(i/256) + rs.MinY()
						t.Errorf("chunk %d,%d x=%d y=%d z=%d: got %s want %s", cx, cz, i&15, y, (i>>4)&15, g, w)
					}
					bad++
				}
			}
			if bad > 0 {
				t.Errorf("chunk %d,%d: %d/%d mismatches", cx, cz, bad, len(want))
				total++
			}
		}
	}
	t.Logf("%d/%d chunks mismatched", total, len(chunks))
}

// noiseGolden digests chunks whose noise stage was verified block for
// block against the real server (tests/blackbox TestTerrainNoiseParity,
// seed 12345), so unit runs catch regressions without the oracle.
var noiseGolden = map[[2]int32]string{
	{0, 0}:      "4401f531336b9d91",
	{-122, 188}: "52708683dc894707",
	{314, -436}: "39cabd6f7fe3abc7",
	{-17, -17}:  "32e81e568f9226c4",
}

func noiseDigest(c *NoiseColumn) string {
	h := sha256.New()
	b := make([]byte, len(c.Cells))
	for i, s := range c.Cells {
		b[i] = byte(s)
	}
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func TestNoiseGolden(t *testing.T) {
	rs, err := NewRandomState(Vanilla(), "overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range noiseGolden {
		if got := noiseDigest(rs.FillNoise(k[0], k[1])); got != want {
			t.Errorf("chunk %v: digest %s, want %s", k, got, want)
		}
	}
	if os.Getenv("MCGO_PRINT_GOLDEN") != "" {
		for _, k := range [][2]int32{{0, 0}, {-122, 188}, {314, -436}, {-17, -17}} {
			t.Logf("{%d, %d}: %q,", k[0], k[1], noiseDigest(rs.FillNoise(k[0], k[1])))
		}
	}
}

func vanillaBiomes(t *testing.T, c map[string]any, minY, height int32) []string {
	out := make([]string, 16*height/4)
	sections, _ := c["sections"].(nbt.List)
	for _, sAny := range sections.Items {
		sec := sAny.(map[string]any)
		sy := int32(sec["Y"].(int8))
		b, ok := sec["biomes"].(map[string]any)
		if !ok {
			continue
		}
		pal := b["palette"].(nbt.List)
		names := make([]string, len(pal.Items))
		for i, p := range pal.Items {
			names[i] = strings.TrimPrefix(p.(string), "minecraft:")
		}
		idx := make([]int, 64)
		if data, ok := b["data"].([]int64); ok && len(names) > 1 {
			bpe := 0
			for 1<<uint(bpe) < len(names) {
				bpe++
			}
			per := 64 / bpe
			for i := 0; i < 64; i++ {
				idx[i] = int((data[i/per] >> (uint(i%per) * uint(bpe))) & (1<<uint(bpe) - 1))
			}
		}
		for i := 0; i < 64; i++ {
			qy := sy*4 + int32(i>>4) - minY/4
			if qy < 0 || qy >= height/4 {
				continue
			}
			out[qy*16+int32(i&15)] = names[idx[i]]
		}
	}
	return out
}

func TestBiomesVsVanillaRegion(t *testing.T) {
	dir := os.Getenv("MCGO_WORLDGEN_REGION")
	if dir == "" {
		t.Skip()
	}
	seed := int64(1)
	if s := os.Getenv("MCGO_WORLDGEN_SEED"); s != "" {
		fmt.Sscan(s, &seed)
	}
	rs, _ := NewRandomState(Vanilla(), "overworld", seed)
	bs, err := NewMultiNoiseBiomeSource(Vanilla(), "overworld")
	if err != nil {
		t.Fatal(err)
	}
	var chunks [][2]int32
	for _, a := range strings.Split(os.Getenv("MCGO_WORLDGEN_CHUNKS"), ";") {
		var x0, z0, x1, z1 int32
		fmt.Sscanf(a, "%d,%d,%d,%d", &x0, &z0, &x1, &z1)
		for cx := x0; cx <= x1; cx++ {
			for cz := z0; cz <= z1; cz++ {
				chunks = append(chunks, [2]int32{cx, cz})
			}
		}
	}
	seen := map[string]int{}
	bad := 0
	for _, ch := range chunks {
		_, c := vanillaChunk(t, dir, ch[0], ch[1], rs.MinY(), rs.Height())
		want := vanillaBiomes(t, c, rs.MinY(), rs.Height())
		got := rs.ChunkBiomes(bs, rs.NewChunk(ch[0], ch[1]), ch[0], ch[1])
		for i := range want {
			seen[want[i]]++
			if want[i] != got[i] {
				if bad < 10 {
					t.Errorf("chunk %v cell %d: got %s want %s", ch, i, got[i], want[i])
				}
				bad++
			}
		}
	}
	t.Logf("%d mismatches; biomes %v", bad, seen)
}

func stateName(b world.Block) string {
	n, props, _ := world.DescribeState(b)
	if len(props) == 0 {
		return n
	}
	var kv []string
	for k, v := range props {
		kv = append(kv, k+"="+v)
	}
	sort.Strings(kv)
	return n + "[" + strings.Join(kv, ",") + "]"
}

func TestSurfaceVsVanillaRegion(t *testing.T) {
	dir := os.Getenv("MCGO_WORLDGEN_REGION")
	if dir == "" {
		t.Skip()
	}
	seed := int64(1)
	if s := os.Getenv("MCGO_WORLDGEN_SEED"); s != "" {
		fmt.Sscan(s, &seed)
	}
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", seed)
	if err != nil {
		t.Fatal(err)
	}
	g.Carve = os.Getenv("MCGO_WORLDGEN_CARVE") != ""
	var chunks [][2]int32
	for _, a := range strings.Split(os.Getenv("MCGO_WORLDGEN_CHUNKS"), ";") {
		var x0, z0, x1, z1 int32
		fmt.Sscanf(a, "%d,%d,%d,%d", &x0, &z0, &x1, &z1)
		for cx := x0; cx <= x1; cx++ {
			for cz := z0; cz <= z1; cz++ {
				chunks = append(chunks, [2]int32{cx, cz})
			}
		}
	}
	kinds := map[string]int{}
	badChunks := 0
	for _, ch := range chunks {
		want, _ := vanillaChunk(t, dir, ch[0], ch[1], g.RS.MinY(), g.RS.Height())
		p := g.Terrain(ch[0], ch[1])
		bad := 0
		for i, w := range want {
			gn := stateName(p.Blocks[i])
			if gn != w {
				if strings.Contains(w, "level=") && gn == "air" {
					continue
				}
				kinds[gn+"->"+w]++
				if bad < 3 {
					t.Errorf("chunk %v x=%d y=%d z=%d: got %s want %s", ch, i&15, int32(i>>8)+g.RS.MinY(), (i>>4)&15, gn, w)
				}
				bad++
			}
		}
		if bad > 0 {
			badChunks++
		}
	}
	t.Logf("%d/%d chunks bad; kinds %v", badChunks, len(chunks), kinds)
}

// terrainGolden digests surface-stage chunks verified against the real
// server (tests/blackbox TestTerrainSurfaceParity, seed 12345).
var terrainGolden = map[[2]int32]string{
	{-320, -288}: "ae4e01107c9238ac",
	{-176, 112}:  "0ecde9d6ec1e16a1",
	{368, 368}:   "473119006cae86bd",
	{112, -96}:   "c1cda900fe87d326",
	{48, -48}:    "28241f2747eb80f3",
}

func terrainDigest(p *ProtoChunk) string {
	h := sha256.New()
	for _, b := range p.Blocks {
		h.Write([]byte(stateName(b)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func TestTerrainGolden(t *testing.T) {
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	g.Carve = false
	for k, want := range terrainGolden {
		if got := terrainDigest(g.Terrain(k[0], k[1])); got != want {
			t.Errorf("chunk %v: digest %s, want %s", k, got, want)
		}
	}
	if os.Getenv("MCGO_PRINT_GOLDEN") != "" {
		for _, k := range [][2]int32{{-320, -288}, {-176, 112}, {368, 368}, {112, -96}, {48, -48}} {
			t.Logf("{%d, %d}: %q,", k[0], k[1], terrainDigest(g.Terrain(k[0], k[1])))
		}
	}
}
