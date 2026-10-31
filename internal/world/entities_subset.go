package world

import (
	"encoding/binary"
	"errors"
	"fmt"
	"mc-go/internal/nbt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LoadEntityRegionChunks retains each record's physical global chunk key.
// Position inside an entity compound does not establish ownership of the
// source record: a moved entity must also retire its former stored position.
func LoadEntityRegionChunks(dir string) (map[[2]int32][]map[string]any, error) {
	files, err := filepath.Glob(filepath.Join(dir, "entities", "r.*.*.mca"))
	if err != nil {
		return nil, err
	}
	out := map[[2]int32][]map[string]any{}
	for _, file := range files {
		rx, rz, err := entityRegionFileCoords(file)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(file)
		if err != nil {
			return nil, err
		}
		// Native RegionFile construction can leave an unused zero-byte file.
		// It has no records; a nonempty short header remains a load error.
		if info.Size() == 0 {
			continue
		}
		if info.Size() < 2*regionSectors {
			return nil, fmt.Errorf("entity region %s: truncated header", file)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		if _, _, err := storedRegionRecords(raw); err != nil {
			return nil, fmt.Errorf("entity region %s: %w", file, err)
		}
		chunks, err := readRegionFile(file)
		if err != nil {
			return nil, err
		}
		for slot, root := range chunks {
			key := [2]int32{int32(rx*regionChunks) + slot.X, int32(rz*regionChunks) + slot.Z}
			list, ok := root["Entities"].(nbt.List)
			if !ok || (len(list.Items) != 0 && list.Elem != nbt.TagCompound) {
				return nil, fmt.Errorf("entity region %s: invalid Entities list", file)
			}
			out[key] = nil
			for _, value := range list.Items {
				if entity, ok := value.(map[string]any); ok {
					out[key] = append(out[key], entity)
				} else {
					return nil, fmt.Errorf("entity region %s: invalid entity compound", file)
				}
			}
		}
	}
	return out, nil
}

// MergeEntityRegions applies an explicit selection of entity chunks. Empty
// lists delete selected records; absent keys preserve raw records and their
// timestamps, including unsupported entity payloads and compression markers.
// Callers must serialize all writers to the same region. This does not provide
// a multi-file transaction or fsync guarantee. As with chunk merge, untouched
// external markers survive; oversized selected internal writes are rejected,
// and replacing a selected external record leaves its old sidefile orphaned.
func MergeEntityRegions(dir string, selection map[[2]int32][]map[string]any) error {
	byRegion := map[chunkKey]map[int][]byte{}
	for key, list := range selection {
		region := chunkKey{key[0] >> 5, key[1] >> 5}
		if byRegion[region] == nil {
			byRegion[region] = map[int][]byte{}
		}
		slot := int((key[0] & 31) + (key[1]&31)*32)
		if len(list) == 0 {
			byRegion[region][slot] = nil
			continue
		}
		payload, err := encodeEntityChunk(key, list)
		if err != nil {
			return err
		}
		byRegion[region][slot] = append([]byte{compressZlib}, payload...)
	}
	for region, updates := range byRegion {
		path := filepath.Join(dir, "entities", fmt.Sprintf("r.%d.%d.mca", region.X, region.Z))
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) || (err == nil && len(raw) == 0) {
			onlyEmpty := true
			for _, record := range updates {
				if len(record) != 0 {
					onlyEmpty = false
					break
				}
			}
			if onlyEmpty {
				continue
			}
			raw, err = make([]byte, 8192), nil
		}
		if err != nil {
			return err
		}
		records, timestamps, err := storedRegionRecords(raw)
		if err != nil {
			return fmt.Errorf("merge %s: %w", path, err)
		}
		for slot, record := range updates {
			if len(record) == 0 {
				delete(records, slot)
			} else {
				records[slot] = record
			}
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

// Only canonical region filenames establish physical ownership. Position inside
// the NBT record is intentionally informational: the slot is the record that
// must be replaced when a loaded entity moves or is removed.
func entityRegionFileCoords(path string) (int, int, error) {
	parts := strings.Split(filepath.Base(path), ".")
	if len(parts) != 4 || parts[0] != "r" || parts[3] != "mca" {
		return 0, 0, fmt.Errorf("invalid entity region filename %s", path)
	}
	coords := [2]int{}
	for i := range coords {
		value, err := strconv.ParseInt(parts[i+1], 10, 32)
		if err != nil || value < -67108864 || value > 67108863 || strconv.FormatInt(value, 10) != parts[i+1] {
			return 0, 0, fmt.Errorf("invalid entity region coordinate %s", path)
		}
		coords[i] = int(value)
	}
	return coords[0], coords[1], nil
}
