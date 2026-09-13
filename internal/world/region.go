package world

// Anvil region files (.mca): the on-disk chunk format vanilla writes
// under world/region/, world/DIM-1/region/, and world/DIM1/region/.
// Layout (documented publicly, verified against a real 26.2 file by
// the blackbox suite): a 4096-entry header split into two halves —
// 1024 four-byte chunk locations (24-bit big-endian sector offset plus
// an 8-bit sector count) and 1024 four-byte modification timestamps —
// then 4 KiB-aligned chunk records. Each record is a big-endian
// length, a compression byte (1 gzip, 2 zlib, 3 none), and an NBT
// chunk compound.
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

// dataVersion mirrors the DataVersion the real 26.2 server writes into
// its chunk NBT. The blackbox region test asserts the real value.
const DataVersion26_2 = 4674

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
// at section index sy (block Y = minY + sy*16). Vanilla omits the data
// array when a section is single-palette.
func sectionNBT(w *World, cx, cz, minY, sy int) map[string]any {
	y0 := minY + sy*16
	palette := []Block{}
	palIdx := map[Block]int{}
	idx := make([]int32, 4096)
	i := 0
	for y := y0; y < y0+16; y++ {
		for z := cz * 16; z < cz*16+16; z++ {
			for x := cx * 16; x < cx*16+16; x++ {
				b := w.GetBlock(Pos{X: x, Y: y, Z: z})
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
		name, ok := BlockName(b)
		if !ok {
			name = "air"
		}
		pal.Items = append(pal.Items, map[string]any{"Name": "minecraft:" + name})
	}
	states := map[string]any{"palette": pal}
	if len(palette) > 1 {
		bpe := 4
		for bits := 1 << uint(bpe); bits < len(palette); bpe++ {
			bits = 1 << uint(bpe)
		}
		states["data"] = packIndices(idx, bpe)
	}
	return map[string]any{
		"Y": int8(sy - minY/16), // section index, signed
		chunkBlockStates: states,
		"biomes": map[string]any{
			"palette": nbt.List{Elem: nbt.TagString, Items: []any{w.biomeName()}},
		},
	}
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
func chunkNBT(w *World, cx, cz, minY, height int) map[string]any {
	sections := nbt.List{Elem: nbt.TagCompound}
	for sy := 0; sy < height/16; sy++ {
		sections.Items = append(sections.Items, sectionNBT(w, cx, cz, minY, sy))
	}
	return map[string]any{
		"DataVersion":    int32(DataVersion26_2),
		"x_pos":          int32(cx),
		"y_pos":          int32(minY / 16),
		"z_pos":          int32(cz),
		"status":         "minecraft:full",
		"LastUpdate":     int64(time.Now().UnixMilli()),
		"InhabitedTime":  int64(0),
		"isLightOn":      int8(1),
		"sections":       sections,
		"block_entities": nbt.List{Elem: nbt.TagCompound},
		"block_ticks":    nbt.List{Elem: nbt.TagCompound},
		"fluid_ticks":    nbt.List{Elem: nbt.TagCompound},
		"PostProcessing": nbt.List{Elem: nbt.TagList},
	}
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

// SaveRegion writes every chunk containing overrides into Anvil
// region files under dir/region/. minY/height bound the vertical
// range so each dimension emits its own section count.
func (w *World) SaveRegion(dir string, minY, height int) error {
	snap := w.Snapshot()
	regions := regionChunksOf(snap, minY, height)
	for r, chunks := range regions {
		payloads := map[chunkKey][]byte{}
		for _, c := range chunks {
			var raw bytes.Buffer
			if err := nbt.WriteNamed(&raw, "", chunkNBT(w, int(c.X), int(c.Z), minY, height)); err != nil {
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
func (w *World) loadChunkOverrides(compound map[string]any, cx, cz, minY int) {
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
		blocks := make([]Block, len(pal.Items))
		for i, pAny := range pal.Items {
			pc, ok := pAny.(map[string]any)
			if !ok {
				continue
			}
			name, _ := pc["Name"].(string)
			blk, ok := BlockState(strings.TrimPrefix(name, "minecraft:"))
			if !ok {
				continue
			}
			blocks[i] = blk
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
