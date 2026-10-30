package world

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CaptureDimensionChunksWithTicks captures only the columns named by selection.
// An empty tick value still selects a column and clears its previous tick lists.
// Unlike whole-world capture, this does not enumerate overrides or cached base
// chunks. The caller must own simulation state while supplying its callbacks.
func (w *World) CaptureDimensionChunksWithTicks(minY, height int, entities func(cx, cz int) []map[string]any, lights func(cx, cz int) *ChunkLight, selection map[[2]int32]SavedChunkTicks) *DimensionSaveSnapshot {
	s := &DimensionSaveSnapshot{minY: minY, height: height, biome: w.biomeName(), regions: map[chunkKey][]chunkKey{}, chunks: map[chunkKey]dimensionSaveChunk{}}
	for key := range selection {
		s.includeChunk(chunkKey{key[0], key[1]})
	}
	w.captureDimensionColumns(s, entities, lights)
	if s.err != nil {
		return s
	}
	for key, records := range selection {
		k := chunkKey{key[0], key[1]}
		chunk := s.chunks[k]
		chunk.ticks.Blocks = append([]SavedTick(nil), records.Blocks...)
		chunk.ticks.Fluids = append([]SavedTick(nil), records.Fluids...)
		s.chunks[k] = chunk
	}
	return s
}

// WriteMerged updates only captured columns. Untouched records, including their
// compression marker and timestamps, survive without NBT decoding. Callers must
// serialize writers to the same region. Completion is not a multi-file atomic
// commit or an fsync guarantee, matching the existing native save API's scope.
// Untouched external records and their side files are preserved. Oversized
// selected payloads are rejected; replacing a selected external record with an
// internal record currently leaves the old .mcc file unreferenced.
func (s *DimensionSaveSnapshot) WriteMerged(worldDir, dimension string) error {
	if s.err != nil {
		return s.err
	}
	dir := DimensionDir(worldDir, dimension)
	for region, chunks := range s.regions {
		payloads, err := s.encodeChunkPayloads(chunks)
		if err != nil {
			return err
		}
		path := regionFileName(dir, int(region.X), int(region.Z))
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			raw, err = make([]byte, 8192), nil
		}
		if err != nil {
			return err
		}
		records, timestamps, err := storedRegionRecords(raw)
		if err != nil {
			return fmt.Errorf("merge %s: %w", path, err)
		}
		for key, payload := range payloads {
			slot := int((key.X & 31) + (key.Z&31)*32)
			records[slot] = append([]byte{compressZlib}, payload...)
			binary.BigEndian.PutUint32(timestamps[slot*4:], uint32(time.Now().Unix()))
		}
		data, err := encodeStoredRegion(records, timestamps)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := replaceMergedRegion(path, data, mode); err != nil {
			return err
		}
	}
	return nil
}

func storedRegionRecords(raw []byte) (map[int][]byte, []byte, error) {
	if len(raw) < 8192 {
		return nil, nil, fmt.Errorf("short region header")
	}
	records := map[int][]byte{}
	timestamps := append([]byte(nil), raw[4096:8192]...)
	used := map[uint32]bool{0: true, 1: true}
	for slot := 0; slot < 1024; slot++ {
		location := binary.BigEndian.Uint32(raw[slot*4:])
		if location == 0 {
			continue
		}
		offset, count := location>>8, location&255
		start, span := int64(offset)*4096, int64(count)*4096
		if offset < 2 || count == 0 || start+span > int64(len(raw)) {
			return nil, nil, fmt.Errorf("invalid chunk location at slot %d", slot)
		}
		for sector := offset; sector < offset+count; sector++ {
			if used[sector] {
				return nil, nil, fmt.Errorf("overlapping chunk allocation at slot %d", slot)
			}
			used[sector] = true
		}
		length := int64(binary.BigEndian.Uint32(raw[start:]))
		if length < 1 || length+4 > span {
			return nil, nil, fmt.Errorf("invalid chunk length at slot %d", slot)
		}
		records[slot] = append([]byte(nil), raw[start+4:start+4+length]...)
	}
	return records, timestamps, nil
}

func encodeStoredRegion(records map[int][]byte, timestamps []byte) ([]byte, error) {
	data := make([]byte, 8192)
	copy(data[4096:], timestamps)
	for slot := 0; slot < 1024; slot++ {
		record, ok := records[slot]
		if !ok {
			continue
		}
		sectors := (len(record) + 4 + 4095) / 4096
		if len(record) == 0 || sectors > 255 {
			return nil, fmt.Errorf("chunk slot %d requires unsupported external storage", slot)
		}
		offset := len(data) / 4096
		if offset > 0xffffff {
			return nil, fmt.Errorf("region sector offset overflow")
		}
		binary.BigEndian.PutUint32(data[slot*4:], uint32(offset<<8|sectors))
		start := len(data)
		data = append(data, make([]byte, sectors*4096)...)
		binary.BigEndian.PutUint32(data[start:], uint32(len(record)))
		copy(data[start+4:], record)
	}
	return data, nil
}

func replaceMergedRegion(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".region-merge-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
