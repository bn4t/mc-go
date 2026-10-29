package world

import (
	"fmt"

	"mc-go/internal/nbt"
)

// DimensionSaveSnapshot owns a detached copy of every column selected for a
// native dimension save. It retains neither a world nor provider callbacks.
// Capture must run while the caller owns simulation state; Write may run after
// that ownership is released, including after the world has stopped.
type DimensionSaveSnapshot struct {
	minY, height int
	biome        string
	regions      map[chunkKey][]chunkKey
	chunks       map[chunkKey]dimensionSaveChunk
	err          error
}

type dimensionSaveChunk struct {
	blocks   []Block
	biomes   []string
	entities []map[string]any
	light    *ChunkLight
}

// CaptureDimensionSave materializes generator terrain and overrides, and copies
// all NBT and light supplied by the callbacks. It performs no serialization or IO.
func (w *World) CaptureDimensionSave(minY, height int, entities func(cx, cz int) []map[string]any, lights func(cx, cz int) *ChunkLight) *DimensionSaveSnapshot {
	s := &DimensionSaveSnapshot{minY: minY, height: height, biome: w.biomeName(), regions: regionChunksOf(w.Snapshot(), minY, height), chunks: make(map[chunkKey]dimensionSaveChunk)}
	if w.base != nil {
		// A provided world saves every chunk it generated or loaded, as
		// the real server saves its loaded chunks: regenerating one
		// later could differ where neighbours' features overlapped.
		for _, k := range w.base.keys() {
			r := chunkKey{int32(k[0] >> 5), int32(k[1] >> 5)}
			c := chunkKey{int32(k[0]), int32(k[1])}
			found := false
			for _, have := range s.regions[r] {
				found = found || have == c
			}
			if !found {
				s.regions[r] = append(s.regions[r], c)
			}
		}
	}
	for _, keys := range s.regions {
		for _, key := range keys {
			cx, cz := int(key.X), int(key.Z)
			c := dimensionSaveChunk{blocks: make([]Block, 16*height*16), biomes: w.ChunkBiomes(cx, cz)}
			w.FillBox(Pos{cx * 16, minY, cz * 16}, Pos{cx*16 + 15, minY + height - 1, cz*16 + 15}, c.blocks)
			if entities != nil {
				source := entities(cx, cz)
				c.entities = make([]map[string]any, len(source))
				for i, entity := range source {
					copy, err := copyDimensionNBT(entity)
					if err != nil {
						s.err = err
						return s
					}
					c.entities[i] = copy.(map[string]any)
				}
			}
			if lights != nil {
				if light := lights(cx, cz); light != nil {
					c.light = &ChunkLight{Sky: copyLightLayers(light.Sky), Block: copyLightLayers(light.Block)}
				}
			}
			s.chunks[key] = c
		}
	}
	return s
}

// Write serializes and writes the captured dimension without consulting live state.
func (s *DimensionSaveSnapshot) Write(worldDir, dimension string) error {
	return s.writeDir(DimensionDir(worldDir, dimension))
}

func copyLightLayers(source map[int][]byte) map[int][]byte {
	if source == nil {
		return nil
	}
	out := make(map[int][]byte, len(source))
	for key, layer := range source {
		if layer != nil {
			out[key] = make([]byte, len(layer))
			copy(out[key], layer)
		} else {
			out[key] = nil
		}
	}
	return out
}

// copyDimensionNBT handles exactly the codec's value model. Unsupported values
// become a deferred write error rather than retaining arbitrary mutable objects.
func copyDimensionNBT(value any) (any, error) {
	switch v := value.(type) {
	case int8, int16, int32, int64, float32, float64, string:
		return v, nil
	case []byte:
		return append([]byte(nil), v...), nil
	case []int32:
		return append([]int32(nil), v...), nil
	case []int64:
		return append([]int64(nil), v...), nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, value := range v {
			copy, err := copyDimensionNBT(value)
			if err != nil {
				return nil, err
			}
			out[key] = copy
		}
		return out, nil
	case nbt.List:
		out := nbt.List{Elem: v.Elem, Items: make([]any, len(v.Items))}
		for i, value := range v.Items {
			copy, err := copyDimensionNBT(value)
			if err != nil {
				return nil, err
			}
			out.Items[i] = copy
		}
		return out, nil
	default:
		return nil, fmt.Errorf("dimension save: unsupported NBT value %T", value)
	}
}
