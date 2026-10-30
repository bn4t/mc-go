package world

import "mc-go/internal/nbt"

// SavedTick is a native chunk tick record. Delay is relative to capture/load
// time, not an absolute world age; Type is the registered resource identifier.
type SavedTick struct {
	Type     string
	Pos      Pos
	Delay    int32
	Priority int32
}

type SavedChunkTicks struct {
	Blocks []SavedTick
	Fluids []SavedTick
}

// CaptureDimensionSaveWithTicks extends the existing capture with detached
// primitive tick records. Tick-only columns are included even without block
// overrides; neither the input map nor its slices survive capture.
func (w *World) CaptureDimensionSaveWithTicks(minY, height int, entities func(cx, cz int) []map[string]any, lights func(cx, cz int) *ChunkLight, ticks map[[2]int32]SavedChunkTicks) *DimensionSaveSnapshot {
	keys := make([]chunkKey, 0, len(ticks))
	for key := range ticks {
		keys = append(keys, chunkKey{key[0], key[1]})
	}
	s := w.captureDimensionSave(minY, height, entities, lights, keys)
	if s.err != nil || ticks == nil {
		return s
	}
	for key, chunk := range s.chunks {
		records := ticks[[2]int32{key.X, key.Z}]
		chunk.ticks.Blocks = append([]SavedTick(nil), records.Blocks...)
		chunk.ticks.Fluids = append([]SavedTick(nil), records.Fluids...)
		s.chunks[key] = chunk
	}
	return s
}

func savedTickList(records []SavedTick) nbt.List {
	out := nbt.List{Elem: nbt.TagCompound}
	for _, record := range records {
		out.Items = append(out.Items, map[string]any{"i": record.Type, "x": int32(record.Pos.X), "y": int32(record.Pos.Y), "z": int32(record.Pos.Z), "t": record.Delay, "p": record.Priority})
	}
	return out
}
