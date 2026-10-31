package world

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"mc-go/internal/nbt"
	"os"
	"path/filepath"
)

// Entity region files: <dim>/entities/r.<rx>.<rz>.mca. Same Anvil
// container as the chunk regions, but each record's root compound is
// {Position:int[2] (chunk coords), DataVersion:int, Entities:list of
// compounds} — the chunk fields (sections, heightmaps) do not exist
// here. Vanilla only writes a record for chunks that held entities,
// and only writes the file for regions any record landed in.
//
// The caller supplies entity save compounds keyed by GLOBAL chunk
// coordinate (block >> 4); the entity's own Pos carries its exact
// position inside the chunk.

// SaveEntityRegions writes one entities region file per region that
// holds at least one entity chunk under dir/entities/.
func SaveEntityRegions(dir string, ents map[[2]int32][]map[string]any) error {
	byRegion := map[chunkKey]map[chunkKey][]byte{}
	for c, list := range ents {
		if len(list) == 0 {
			continue
		}
		payload, err := encodeEntityChunk(c, list)
		if err != nil {
			return err
		}
		r := chunkKey{c[0] >> 5, c[1] >> 5}
		if byRegion[r] == nil {
			byRegion[r] = map[chunkKey][]byte{}
		}
		byRegion[r][chunkKey{c[0], c[1]}] = payload
	}
	for r, payloads := range byRegion {
		data := encodeRegion(payloads)
		if data == nil {
			continue
		}
		path := filepath.Join(dir, "entities", fmt.Sprintf("r.%d.%d.mca", r.X, r.Z))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// LoadEntityRegions flattens the Entities list of every chunk record
// under dir/entities/. A missing directory is an empty list, not an
// error — a world vanilla never saved entities for loads as none.
func LoadEntityRegions(dir string) ([]map[string]any, error) {
	chunks, err := LoadEntityRegionChunks(dir)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, list := range chunks {
		out = append(out, list...)
	}
	return out, nil
}

func encodeEntityChunk(c [2]int32, list []map[string]any) ([]byte, error) {
	items := make([]any, 0, len(list))
	for _, m := range list {
		items = append(items, m)
	}
	root := map[string]any{
		"Position":    []int32{c[0], c[1]},
		"DataVersion": int32(DataVersion26_2),
		"Entities":    nbt.List{Elem: nbt.TagCompound, Items: items},
	}
	var raw bytes.Buffer
	if err := nbt.WriteNamed(&raw, "", root); err != nil {
		return nil, err
	}
	var zc bytes.Buffer
	zw := zlib.NewWriter(&zc)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return zc.Bytes(), nil
}
