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
// Untouched external records and their side files are preserved. A selected
// record that grows past the external threshold moves its payload to a
// c.<x>.<z>.mcc file beside the region, and one that shrinks back - or a
// deleted column - reclaims the side file the way the native writer does.
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
		updates := map[int][]byte{}
		for key, payload := range payloads {
			slot := int((key.X & 31) + (key.Z&31)*32)
			records[slot] = append([]byte{s.compression.versionByte()}, payload...)
			binary.BigEndian.PutUint32(timestamps[slot*4:], uint32(time.Now().Unix()))
			updates[slot] = records[slot]
		}
		data, externals, err := encodeStoredRegion(records, timestamps)
		if err != nil {
			return err
		}
		if err := mergeRegionSideFiles(filepath.Dir(path), int(region.X), int(region.Z), externals, updates); err != nil {
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

// encodeStoredRegion renders one region file from raw records keyed by
// slot. A record of regionExternalChunkThreshold or more sectors is
// stored externally: it becomes the 5-byte stub with its version byte's
// external flag set, and its payload is returned for a c.<x>.<z>.mcc
// side file, exactly as the native writer splits an oversized record.
func encodeStoredRegion(records map[int][]byte, timestamps []byte) ([]byte, map[int][]byte, error) {
	data := make([]byte, 8192)
	copy(data[4096:], timestamps)
	externals := map[int][]byte{}
	for slot := 0; slot < 1024; slot++ {
		record, ok := records[slot]
		if !ok {
			continue
		}
		if len(record) == 0 {
			return nil, nil, fmt.Errorf("chunk slot %d has an empty record", slot)
		}
		version := record[0]
		recLen := len(record)
		sectors := (recLen + 4 + 4095) / 4096
		external := sectors >= regionExternalChunkThreshold
		if external {
			externals[slot] = record[1:]
			version |= 0x80
			recLen = 1
			sectors = 1
		}
		offset := len(data) / 4096
		if offset > 0xffffff {
			return nil, nil, fmt.Errorf("region sector offset overflow")
		}
		binary.BigEndian.PutUint32(data[slot*4:], uint32(offset<<8|sectors))
		start := len(data)
		data = append(data, make([]byte, sectors*4096)...)
		binary.BigEndian.PutUint32(data[start:], uint32(recLen))
		data[start+4] = version
		if !external {
			copy(data[start+5:], record[1:])
		}
	}
	return data, externals, nil
}

// mergeRegionSideFiles settles the side files of a merged region: a
// selected slot whose new record is external gets its payload written
// to the chunk's c.<x>.<z>.mcc file, and every other selected slot -
// including a deleted record - has its side file removed, which is
// the net effect of the native writer deleting it before every
// in-file write and on clear. Untouched slots keep theirs.
func mergeRegionSideFiles(regionDir string, rx, rz int, externals map[int][]byte, updates map[int][]byte) error {
	for slot, payload := range externals {
		if _, ok := updates[slot]; !ok {
			continue
		}
		x, z := int32(rx*32+slot%32), int32(rz*32+slot/32)
		path := externalChunkPath(regionDir, x, z)
		if err := os.MkdirAll(regionDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			return err
		}
	}
	for slot := range updates {
		if _, ok := externals[slot]; ok {
			continue
		}
		x, z := int32(rx*32+slot%32), int32(rz*32+slot/32)
		if err := reclaimExternalChunk(regionDir, x, z); err != nil {
			return err
		}
	}
	return nil
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
