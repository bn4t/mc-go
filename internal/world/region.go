package world

// Anvil region files (.mca): the on-disk chunk format the real 26.2
// server writes under world/dimensions/<namespace>/<path>/region/.
// Layout (documented publicly, and read back from a real 26.2 file by
// the blackbox suite, which boots the real server on a directory mc-go
// saved): a 4096-entry header split into two halves - 1024 four-byte
// chunk locations (24-bit big-endian sector offset plus an 8-bit
// sector count) and 1024 four-byte modification timestamps - then
// 4 KiB-aligned chunk records. Each record is a big-endian length, a
// compression byte (1 gzip, 2 zlib, 3 none), and an NBT chunk compound.
//
// mc-go stores only generator deltas as block overrides; a region
// write materializes every chunk that contains an override into the
// full vanilla section/palette layout so the file is loadable by the
// real game.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mc-go/internal/nbt"
)

const (
	regionSectors    = 4096 // bytes per sector
	regionChunks     = 32   // chunks per axis
	compressZlib     = 2
	compressGzip     = 1
	compressNone     = 3
	compressLZ4      = 4
	chunkBlockStates = "block_states"
)

// DataVersion26_2 is the data version the real 26.2 server writes into
// its chunk and level NBT, from the jar's own version.json
// (world_version). Vanilla refuses to read a world whose version is
// newer than it knows and migrates anything older, so this is not a
// cosmetic field.
const DataVersion26_2 = 4903

// regionFileName returns dir/region/r.<rx>.<rz>.mca.
func regionFileName(dir string, rx, rz int) string {
	return filepath.Join(dir, "region", fmt.Sprintf("r.%d.%d.mca", rx, rz))
}

// chunkKey pairs region-file coordinates.
type chunkKey struct{ X, Z int32 }

// RegionChunks groups a world snapshot's chunks by region file.
func regionChunksOf(snap map[Pos]Block, minY, height int) map[chunkKey][]chunkKey {
	out := map[chunkKey][]chunkKey{}
	for p := range snap {
		if p.Y < minY || p.Y >= minY+height {
			continue
		}
		r := chunkKey{int32(p.X >> 9), int32(p.Z >> 9)} // 32 chunks per axis
		c := chunkKey{int32(p.X >> 4), int32(p.Z >> 4)}
		found := false
		for _, have := range out[r] {
			if have == c {
				found = true
				break
			}
		}
		if !found {
			out[r] = append(out[r], c)
		}
	}
	return out
}

// packIndices packs 4096 palette indices bpe bits apiece, seven
// (or fewer) entries per long, YZX order — the vanilla section
// layout, identical to the wire encoding.
func packIndices(idx []int32, bpe int) []int64 {
	if bpe <= 0 {
		return nil
	}
	per := 64 / bpe
	out := make([]int64, (4096+per-1)/per)
	for i, v := range idx {
		out[i/per] |= int64(v) << (uint(i%per) * uint(bpe))
	}
	return out
}

// sectionNBT builds one 26.2 section compound for the 16x16x16 slice
// at section index sy (block Y = minY + sy*16), reading blocks out of
// the chunk's column array in the YZX order the data array uses.
// Vanilla omits the data array when a section is single-palette.
func sectionNBT(blocks []Block, minY, sy int, biome string, cells []string, lights *ChunkLight) map[string]any {
	y0 := minY + sy*16
	palette := []Block{}
	palIdx := map[Block]int{}
	idx := make([]int32, 4096)
	i := 0
	for y := sy * 16; y < sy*16+16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				b := blocks[(y*16+z)*16+x]
				pi, ok := palIdx[b]
				if !ok {
					pi = len(palette)
					palette = append(palette, b)
					palIdx[b] = pi
				}
				idx[i] = int32(pi)
				i++
			}
		}
	}
	pal := nbt.List{Elem: nbt.TagCompound}
	for _, b := range palette {
		// A palette entry names its block, and every state of a block
		// has that block's name: a sign facing east is written as
		// oak_sign with rotation=1, never as an unnamed state.
		name, ok := StateOwner(b)
		if !ok {
			name = "air"
		}
		entry := map[string]any{"Name": "minecraft:" + name}
		// Every property of the block, defaults included: the real
		// server saves grass_block as {snowy: "false"} and writes no
		// properties map at all for a block that has none. The key is
		// capitalised here - the saved chunk format spells it
		// "Properties", unlike the network palettes that carry global
		// state ids instead.
		if props := BlockProperties(b); len(props) > 0 {
			pm := make(map[string]any, len(props))
			for k, v := range props {
				pm[k] = v
			}
			entry["Properties"] = pm
		}
		pal.Items = append(pal.Items, entry)
	}
	states := map[string]any{"palette": pal}
	if len(palette) > 1 {
		// Four bits at least, else the fewest that index the palette
		// (the width a reader derives from the palette size).
		bpe := 4
		for 1<<uint(bpe) < len(palette) {
			bpe++
		}
		states["data"] = packIndices(idx, bpe)
	}
	sec := map[string]any{
		// The section's Y is absolute - the block Y it starts at divided
		// by sixteen - so the overworld's lowest section is -4, not 0.
		"Y":              int8(y0 >> 4),
		chunkBlockStates: states,
		"biomes":         biomesNBT(biome, cells, sy),
	}
	// SkyLight/BlockLight are absent where the layer never
	// materialized, a 2048-byte nibble array where it exists - the
	// same byte order the update_light packet ships.
	if lights != nil {
		if b := lights.Sky[sy]; b != nil {
			sec["SkyLight"] = b
		}
		if b := lights.Block[sy]; b != nil {
			sec["BlockLight"] = b
		}
	}
	return sec
}

// biomesNBT writes a section's biome container: the single biome, or
// the section's 64 cells as a palette in first-seen order with data
// packed at ceil(log2(palette)) bits, the width the real server saves.
func biomesNBT(biome string, cells []string, sy int) map[string]any {
	if len(cells) < (sy+1)*64 {
		return map[string]any{"palette": nbt.List{Elem: nbt.TagString, Items: []any{biome}}}
	}
	sec := cells[sy*64 : sy*64+64]
	var pal []any
	index := map[string]int{}
	idx := make([]int32, 64)
	for i, b := range sec {
		pi, ok := index[b]
		if !ok {
			pi = len(pal)
			index[b] = pi
			pal = append(pal, b)
		}
		idx[i] = int32(pi)
	}
	out := map[string]any{"palette": nbt.List{Elem: nbt.TagString, Items: pal}}
	if len(pal) > 1 {
		bpe := 0
		for 1<<uint(bpe) < len(pal) {
			bpe++
		}
		per := 64 / bpe
		data := make([]int64, (64+per-1)/per)
		for i, v := range idx {
			data[i/per] |= int64(v) << (uint(i%per) * uint(bpe))
		}
		out["data"] = data
	}
	return out
}

// ChunkLight is one chunk's saved light: Sky and Block map 0-based
// section indices to the 2048-byte nibble-packed layer the real
// server stores under SkyLight/BlockLight. A section with no entry
// writes no array, matching vanilla's absent-when-not-materialized
// rule.
type ChunkLight struct {
	Sky, Block map[int][]byte
}

// isLightOn is the chunk flag vanilla sets once the engine has lit a
// column: 1 whenever a light provider answered, else 0 so the real
// server relights on load instead of trusting absent arrays.
func isLightOn(l *ChunkLight) int8 {
	if l != nil {
		return 1
	}
	return 0
}

// biomeName is the single-biome palette entry for generated chunks.
// Overridden by World.SetBiome when a dimension uses a different one.
func (w *World) biomeName() string {
	if w.biome != "" {
		return w.biome
	}
	return "minecraft:plains"
}

// chunkNBT builds the full vanilla chunk compound for column (cx,cz).
// Every field name and the vertical conventions were read off a chunk
// the real 26.2 server saved, and the fields that look redundant are
// not:
//
//   - yPos is the lowest section's index -4 in the overworld, not 0;
//   - Heightmaps carries four typed maps, and the real server trusts
//     them rather than recomputing, so a wrong one moves the terrain
//     the client draws;
//   - PostProcessing holds one list per section, empty and unused by a
//     world with no structure pieces;
//   - isLightOn says whether the light arrays in the sections can be
//     trusted. mc-go saves no light and says so, which is what makes
//     the real server light the chunk itself instead of drawing a dark
//     one;
//   - block_entities is the chunk's chests and signs, written empty
//     rather than omitted when there are none, and there is no
//     `entities` key at all: 26.2 keeps a dimension's mobs in its own
//     entities/ region files, not in the chunk.
func chunkNBT(w *World, cx, cz, minY, height int, blockEntities func(cx, cz int) []map[string]any, lights func(cx, cz int) *ChunkLight) map[string]any {
	blocks := make([]Block, 16*height*16)
	w.FillBox(
		Pos{X: cx * 16, Y: minY, Z: cz * 16},
		Pos{X: cx*16 + 15, Y: minY + height - 1, Z: cz*16 + 15},
		blocks,
	)
	biome := w.biomeName()
	cells := w.ChunkBiomes(cx, cz)
	var light *ChunkLight
	if lights != nil {
		light = lights(int(cx), int(cz))
	}
	var entities []map[string]any
	if blockEntities != nil {
		entities = blockEntities(cx, cz)
	}
	return capturedChunkNBT(cx, cz, minY, height, blocks, biome, cells, entities, light)
}

func capturedChunkNBT(cx, cz, minY, height int, blocks []Block, biome string, cells []string, entities []map[string]any, light *ChunkLight) map[string]any {
	sections := nbt.List{Elem: nbt.TagCompound}
	post := nbt.List{Elem: nbt.TagList}
	for sy := 0; sy < height/16; sy++ {
		sections.Items = append(sections.Items, sectionNBT(blocks, minY, sy, biome, cells, light))
		post.Items = append(post.Items, nbt.List{Elem: nbt.TagString})
	}
	be := nbt.List{Elem: nbt.TagCompound}
	if entities != nil {
		for _, e := range entities {
			be.Items = append(be.Items, e)
		}
	}
	return map[string]any{
		"DataVersion": int32(DataVersion26_2),
		"xPos":        int32(cx),
		"yPos":        int32(minY / 16),
		"zPos":        int32(cz),
		"Status":      "minecraft:full",
		// LastUpdate and InhabitedTime are game ticks, not wall-clock
		// ones: a chunk saved without having been ticked carries zero
		// for both, which is what a freshly generated chunk holds.
		"LastUpdate":    int64(0),
		"InhabitedTime": int64(0),
		"isLightOn":     isLightOn(light),
		"sections":      sections,
		"Heightmaps":    heightmaps(blocks, height, minY),
		// A world with no structures has both maps empty, but they have
		// to be there: the real server reads the compound unconditionally.
		"structures": map[string]any{
			"References": map[string]any{},
			"starts":     map[string]any{},
		},
		"block_entities": be,
		"block_ticks":    nbt.List{Elem: nbt.TagCompound},
		"fluid_ticks":    nbt.List{Elem: nbt.TagCompound},
		"PostProcessing": post,
	}
}

// heightmapBits is the bits per column the real server packs its
// saved heightmaps with: nine for a 384-block overworld and with room
// to spare for the 256-block dimensions, seven entries to a long.
const (
	heightmapBits = 9
	heightmapPer  = 64 / heightmapBits
)

// heightmaps computes the four heightmaps the real server saves, from
// blocks in the same YZX order the sections use. Each entry is the
// highest matching block's offset from the dimension floor, plus one,
// and zero for a column with nothing in it - the encoding the wire
// heightmaps use too, so the two cannot disagree.
//
// The types are vanilla's, and they differ only in what they stop at:
// WORLD_SURFACE is the highest block that is not air, OCEAN_FLOOR the
// highest that blocks motion, MOTION_BLOCKING that or a fluid, and
// MOTION_BLOCKING_NO_LEAVES the same while looking through leaves.
func heightmaps(blocks []Block, height, minY int) map[string]any {
	surface, ocean, motion, noLeaves := ColumnHeights(blocks, height)
	return map[string]any{
		"WORLD_SURFACE":             packHeightmap(surface),
		"OCEAN_FLOOR":               packHeightmap(ocean),
		"MOTION_BLOCKING":           packHeightmap(motion),
		"MOTION_BLOCKING_NO_LEAVES": packHeightmap(noLeaves),
	}
}

// isFluid reports whether a block name is a fluid, which MOTION_BLOCKING
// counts as something to stand on.
func isFluid(name string) bool { return name == "water" || name == "lava" }

// packHeightmap packs 256 column heights nine bits apiece, seven to a
// long, in the X-fastest order the real server writes.
func packHeightmap(vals [256]int32) []int64 {
	out := make([]int64, (256+heightmapPer-1)/heightmapPer)
	for i, v := range vals {
		out[i/heightmapPer] |= int64(v) << (uint(i%heightmapPer) * heightmapBits)
	}
	return out
}

// encodeRegion renders one region file: header, then each chunk record
// at its sector offset. Returns nil when the region holds no chunks.
func encodeRegion(chunks map[chunkKey][]byte) []byte {
	if len(chunks) == 0 {
		return nil
	}
	header := make([]byte, 2*regionSectors)
	var body bytes.Buffer
	nextSector := 2 // header occupies sectors 0-1
	for _, ck := range sortedChunkKeys(chunks) {
		payload := chunks[ck]
		// Record: 4-byte length + 1-byte compression + zlib payload.
		recLen := 1 + len(payload)
		sectors := (4 + recLen + regionSectors - 1) / regionSectors
		lx, lz := int(ck.X&31), int(ck.Z&31)
		entry := lx + lz*regionChunks
		binary.BigEndian.PutUint32(header[entry*4:entry*4+4], uint32(nextSector<<8|sectors&0xFF))
		binary.BigEndian.PutUint32(header[regionSectors+entry*4:], uint32(time.Now().Unix()))
		rec := make([]byte, sectors*regionSectors)
		binary.BigEndian.PutUint32(rec, uint32(recLen))
		rec[4] = compressZlib
		copy(rec[5:], payload)
		body.Write(rec)
		nextSector += sectors
	}
	return append(header, body.Bytes()...)
}

func sortedChunkKeys(m map[chunkKey][]byte) []chunkKey {
	out := make([]chunkKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Z == out[j].Z {
			return out[i].X < out[j].X
		}
		return out[i].Z < out[j].Z
	})
	return out
}

// DimensionDir returns the directory a dimension's files live in:
// worldDir/dimensions/<namespace>/<path>, the 26.2 layout. A name with
// no namespace is taken as minecraft's.
func DimensionDir(worldDir, dimension string) string {
	ns, path := SplitDimension(dimension)
	return filepath.Join(worldDir, "dimensions", ns, path)
}

// SplitDimension splits a dimension name into namespace and path,
// defaulting to the minecraft namespace.
func SplitDimension(dimension string) (ns, path string) {
	ns, path = "minecraft", dimension
	for i := 0; i < len(dimension); i++ {
		if dimension[i] == ':' {
			return dimension[:i], dimension[i+1:]
		}
	}
	return ns, path
}

// SaveDimension writes one dimension's chunks into a world directory,
// under dimensions/<namespace>/<path>/region/. entities, when not nil,
// supplies each chunk's block entity compounds - the chests and signs
// a chunk holds - which are otherwise lost with the block they sit on;
// lights, when not nil, supplies the saved SkyLight/BlockLight arrays
// and flips isLightOn on like the real server.
func (w *World) SaveDimension(worldDir, dimension string, minY, height int, entities func(cx, cz int) []map[string]any, lights func(cx, cz int) *ChunkLight) error {
	return w.CaptureDimensionSave(minY, height, entities, lights).Write(worldDir, dimension)
}

// LoadDimension reads one dimension's chunks back out of a world
// directory. A dimension that was never saved loads as nothing, which
// is a fresh world rather than an error.
func (w *World) LoadDimension(worldDir, dimension string, minY, height int) error {
	dir := DimensionDir(worldDir, dimension)
	if _, err := os.Stat(filepath.Join(dir, "region")); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return w.LoadRegion(dir, minY, height)
}

// SaveRegion writes every chunk containing overrides into Anvil
// region files under dir/region/. minY/height bound the vertical
// range so each dimension emits its own section count.
func (w *World) SaveRegion(dir string, minY, height int) error {
	return w.SaveRegionEntities(dir, minY, height, nil, nil)
}

// SaveRegionEntities is SaveRegion with block entities: entities is
// asked for each written chunk's block entity compounds.
func (w *World) SaveRegionEntities(dir string, minY, height int, entities func(cx, cz int) []map[string]any, lights func(cx, cz int) *ChunkLight) error {
	return w.CaptureDimensionSave(minY, height, entities, lights).writeDir(dir)
}

func (s *DimensionSaveSnapshot) writeDir(dir string) error {
	if s.err != nil {
		return s.err
	}

	for r, chunks := range s.regions {
		payloads := map[chunkKey][]byte{}
		for _, c := range chunks {
			var raw bytes.Buffer
			compound := capturedChunkNBT(int(c.X), int(c.Z), s.minY, s.height, s.chunks[c].blocks, s.biome, s.chunks[c].biomes, s.chunks[c].entities, s.chunks[c].light)
			compound["block_ticks"] = savedTickList(s.chunks[c].ticks.Blocks)
			compound["fluid_ticks"] = savedTickList(s.chunks[c].ticks.Fluids)
			if st := s.chunks[c].structures; st != nil {
				compound["structures"] = st
			}
			if err := nbt.WriteNamed(&raw, "", compound); err != nil {
				return err
			}
			var zc bytes.Buffer
			zw := zlib.NewWriter(&zc)
			if _, err := zw.Write(raw.Bytes()); err != nil {
				return err
			}
			if err := zw.Close(); err != nil {
				return err
			}
			payloads[c] = zc.Bytes()
		}
		data := encodeRegion(payloads)
		if data == nil {
			continue
		}
		path := regionFileName(dir, int(r.X), int(r.Z))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ReadRegionFile parses one .mca file into chunk NBT compounds keyed
// by region-local chunk coordinates. Exported for the blackbox suite,
// which parses a real vanilla region file.
func ReadRegionFile(path string) (map[[2]int32]map[string]any, error) {
	m, err := readRegionFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]int32]map[string]any, len(m))
	for k, v := range m {
		out[[2]int32{k.X, k.Z}] = v
	}
	return out, nil
}

// readRegionFile parses one .mca file into chunk NBT payloads.
func readRegionFile(path string) (map[chunkKey]map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 2*regionSectors {
		return nil, fmt.Errorf("region %s: truncated header", path)
	}
	out := map[chunkKey]map[string]any{}
	for i := 0; i < regionChunks*regionChunks; i++ {
		loc := binary.BigEndian.Uint32(raw[i*4 : i*4+4])
		off := int(loc >> 8)
		count := int(loc & 0xFF)
		if off == 0 || count == 0 {
			continue
		}
		start := off * regionSectors
		if start+5 > len(raw) {
			return nil, fmt.Errorf("region %s: chunk %d out of bounds", path, i)
		}
		recLen := int(binary.BigEndian.Uint32(raw[start : start+4]))
		comp := raw[start+4]
		payload := raw[start+5 : min(start+5+recLen-1, len(raw))]
		var plain []byte
		switch comp {
		case compressZlib:
			zr, err := zlib.NewReader(bytes.NewReader(payload))
			if err != nil {
				return nil, fmt.Errorf("region %s: chunk %d zlib: %w", path, i, err)
			}
			plain, err = io.ReadAll(zr)
			zr.Close()
			if err != nil {
				return nil, err
			}
		case compressGzip:
			return nil, fmt.Errorf("region %s: chunk %d gzip unsupported", path, i)
		case compressNone:
			plain = payload
		default:
			return nil, fmt.Errorf("region %s: chunk %d compression %d", path, i, comp)
		}
		name, root, err := nbt.ReadNamed(bytes.NewReader(plain))
		_ = name
		if err != nil {
			return nil, fmt.Errorf("region %s: chunk %d nbt: %w", path, i, err)
		}
		compound, ok := root.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("region %s: chunk %d root not compound", path, i)
		}
		out[chunkKey{int32(i % regionChunks), int32(i / regionChunks)}] = compound
	}
	return out, nil
}

// LoadRegion restores overrides from every region file under
// dir/region/. Blocks matching the generator are not stored, keeping
// the override set sparse. Missing directories mean a fresh world.
func (w *World) LoadRegion(dir string, minY, height int) error {
	w.blockEntities = nil
	regionDir := filepath.Join(dir, "region")
	files, err := filepath.Glob(filepath.Join(regionDir, "r.*.*.mca"))
	if err != nil {
		return err
	}
	for _, f := range files {
		chunks, err := readRegionFile(f)
		if err != nil {
			return err
		}
		for ck, compound := range chunks {
			rx, rz := regionFileCoords(f)
			cx := rx*regionChunks + int(ck.X)
			cz := rz*regionChunks + int(ck.Z)
			if w.base != nil {
				w.installBaseChunk(compound, cx, cz, minY, height)
				continue
			}
			w.loadChunkOverrides(compound, cx, cz, minY)
		}
	}
	return nil
}

// regionFileCoords parses r.<rx>.<rz>.mca.
func regionFileCoords(path string) (int, int) {
	var rx, rz int
	base := filepath.Base(path)
	fmt.Sscanf(base, "r.%d.%d.mca", &rx, &rz)
	return rx, rz
}

// loadChunkOverrides expands a chunk compound's sections into block
// overrides, skipping blocks the generator would produce anyway.
//
// Only a chunk the real server marked "minecraft:full" is read. The
// real server saves every chunk it had loaded, and one still in an
// earlier generation stage - "minecraft:biomes", "minecraft:carvers",
// "minecraft:structure_starts" - carries placeholder sections that are
// all air and describe no terrain; applying them would erase the
// generator's world. The real server puts such a chunk back through its
// generator for the same reason, and mc-go, whose generator stands in
// for it, does the equivalent by leaving the column alone.
func (w *World) loadChunkOverrides(compound map[string]any, cx, cz, minY int) {
	if status, _ := compound["Status"].(string); status != "minecraft:full" {
		return
	}
	// The chunk's block entities come along with its blocks: they are
	// what a chest's contents and a sign's text live in, and they are
	// only meaningful for a chunk whose blocks were read.
	if list, ok := compound["block_entities"].(nbt.List); ok {
		for _, beAny := range list.Items {
			if be, ok := beAny.(map[string]any); ok {
				w.blockEntities = append(w.blockEntities, be)
			}
		}
	}
	sections, ok := compound["sections"].(nbt.List)
	if !ok {
		return
	}
	for _, secAny := range sections.Items {
		sec, ok := secAny.(map[string]any)
		if !ok {
			continue
		}
		syb, ok := sec["Y"].(int8)
		if !ok {
			continue
		}
		y0 := int(syb) * 16 // section Y is the absolute block Y / 16
		states, ok := sec[chunkBlockStates].(map[string]any)
		if !ok {
			continue
		}
		pal, ok := states["palette"].(nbt.List)
		if !ok || len(pal.Items) == 0 {
			continue
		}
		// Every entry has to resolve before a single block is written.
		// An entry that does not - a block this build has never heard
		// of, or a property value that changed name - would otherwise
		// leave air in its place, and a chunk half-read is worse than
		// one not read at all: the generator's own blocks stand for a
		// section mc-go cannot translate.
		blocks := make([]Block, len(pal.Items))
		resolved := true
		for i, pAny := range pal.Items {
			pc, ok := pAny.(map[string]any)
			if !ok {
				resolved = false
				break
			}
			name, _ := pc["Name"].(string)
			var props map[string]string
			if pm, ok := pc["Properties"].(map[string]any); ok {
				props = make(map[string]string, len(pm))
				for k, v := range pm {
					if s, ok := v.(string); ok {
						props[k] = s
					}
				}
			}
			blk, ok := BlockStateOf(strings.TrimPrefix(name, "minecraft:"), props)
			if !ok {
				resolved = false
				break
			}
			blocks[i] = blk
		}
		if !resolved {
			continue
		}
		var idx []int32
		if data, ok := states["data"].([]int64); ok && len(pal.Items) > 1 {
			idx = unpackIndices(data, len(pal.Items))
		} else {
			idx = make([]int32, 4096) // single palette entry
		}
		i := 0
		for y := 0; y < 16; y++ {
			for z := 0; z < 16; z++ {
				for x := 0; x < 16; x++ {
					b := blocks[idx[i]]
					p := Pos{X: cx*16 + x, Y: y0 + y, Z: cz*16 + z}
					if b != w.gen(p) {
						w.SetBlock(p, b)
					}
					i++
				}
			}
		}
	}
}

// unpackIndices expands packed longs into 4096 palette indices.
func unpackIndices(data []int64, paletteLen int) []int32 {
	bpe := 4
	for 1<<uint(bpe) < paletteLen {
		bpe++
	}
	per := 64 / bpe
	out := make([]int32, 4096)
	mask := int64(1<<uint(bpe)) - 1
	for i := 0; i < 4096 && i/per < len(data); i++ {
		out[i] = int32((data[i/per] >> (uint(i%per) * uint(bpe))) & mask)
	}
	return out
}
