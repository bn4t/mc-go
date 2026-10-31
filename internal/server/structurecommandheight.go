package server

import (
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"weak"
)

// Ordinary block edits leave the resident base identity intact. Replacing a
// loaded base or opening a new world gives a new identity and fresh _WG maps.
// Weak ownership avoids retaining an obsolete base after its replacement.
type commandWGEntry struct {
	base    weak.Pointer[world.BaseChunk]
	heights map[int32][256]int32
}

func (d *dimension) commandWGForBase(key [2]int, base *world.BaseChunk) *commandWGEntry {
	if base == nil {
		delete(d.commandWG, key)
		return nil
	}
	if d.commandWG == nil {
		d.commandWG = map[[2]int]*commandWGEntry{}
	}
	entry := d.commandWG[key]
	if entry == nil || entry.base.Value() != base {
		entry = &commandWGEntry{base: weak.Make(base), heights: map[int32][256]int32{}}
		d.commandWG[key] = entry
	}
	return entry
}

// Peek never materializes a future column to retrieve or retire metadata.
func prepareCommandWG(d *dimension, bounds worldgen.BoundingBox) (worldgen.CommandWGHeightmaps, map[[2]int]*world.BaseChunk) {
	maps := worldgen.CommandWGHeightmaps{}
	identities := map[[2]int]*world.BaseChunk{}
	for cx := int(bounds.MinX >> 4); cx <= int(bounds.MaxX>>4); cx++ {
		for cz := int(bounds.MinZ >> 4); cz <= int(bounds.MaxZ>>4); cz++ {
			key := [2]int{cx, cz}
			base := d.w.PeekBaseChunk(cx, cz)
			entry := d.commandWGForBase(key, base)
			if entry == nil {
				continue
			}
			identities[key] = base
			for kind, heights := range entry.heights {
				maps[[3]int32{int32(cx), int32(cz), kind}] = heights
			}
		}
	}
	return maps, identities
}

func commitCommandWG(d *dimension, maps worldgen.CommandWGHeightmaps, identities map[[2]int]*world.BaseChunk) {
	for key, heights := range maps {
		chunk := [2]int{int(key[0]), int(key[1])}
		base := d.w.PeekBaseChunk(chunk[0], chunk[1])
		entry := d.commandWGForBase(chunk, base)
		if base == nil || base != identities[chunk] || entry == nil {
			continue
		}
		entry.heights[key[2]] = heights
	}
}
