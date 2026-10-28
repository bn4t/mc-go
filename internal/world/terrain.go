package world

import (
	"strings"
	"sync/atomic"

	"mc-go/internal/worldgen"
)

// NoiseTerrain serves a noise generator's terrain as a world's base
// layer: each generated state, interned by the generator as a canonical
// "namespace:name[key=value,...]" string, resolves once to a block.
type NoiseTerrain struct {
	gen *worldgen.Generator
	// resolved is each generated state's block plus one, by state id;
	// zero until first resolved.
	resolved [1 << 16]atomic.Uint32
	// recent caches finished chunks by a hash of their position, so a
	// block read skips the generator's locks; a finished chunk never
	// changes, so any cached one is current.
	recent [256]atomic.Pointer[worldgen.TerrainChunk]
}

// NewNoiseTerrain wraps a generator.
func NewNoiseTerrain(gen *worldgen.Generator) *NoiseTerrain { return &NoiseTerrain{gen: gen} }

// Generator is the wrapped generator.
func (t *NoiseTerrain) Generator() *worldgen.Generator { return t.gen }

// Ready reports whether chunk (cx, cz)'s terrain is generated.
func (t *NoiseTerrain) Ready(cx, cz int) bool { return t.gen.Ready(int32(cx), int32(cz)) }

// Prefetch asks for chunk (cx, cz)'s terrain in the background.
func (t *NoiseTerrain) Prefetch(cx, cz int) { t.gen.Prefetch(int32(cx), int32(cz)) }

// Chunk is chunk (cx, cz) as generated, with what its features left
// besides blocks.
func (t *NoiseTerrain) Chunk(cx, cz int) *worldgen.TerrainChunk {
	return t.gen.Chunk(int32(cx), int32(cz))
}

// Block is the generated block at p.
func (t *NoiseTerrain) Block(p Pos) Block {
	cx, cz := int32(p.X>>4), int32(p.Z>>4)
	slot := &t.recent[uint32(cx*31+cz*17)&255]
	c := slot.Load()
	if c == nil || c.CX != cx || c.CZ != cz {
		c = t.gen.Chunk(cx, cz)
		slot.Store(c)
	}
	return t.resolve(c.Block(int32(p.X&15), int32(p.Y), int32(p.Z&15)))
}

// Biome is the generated biome at quart (qx, qy, qz).
func (t *NoiseTerrain) Biome(qx, qy, qz int) string {
	return t.gen.BiomeAt(int32(qx), int32(qy), int32(qz))
}

func (t *NoiseTerrain) resolve(id worldgen.StateID) Block {
	if v := t.resolved[id].Load(); v != 0 {
		return Block(v - 1)
	}
	b := blockOfCanonical(worldgen.StateName(id))
	t.resolved[id].Store(uint32(b) + 1)
	return b
}

// blockOfCanonical parses "minecraft:name[k=v,...]"; an unknown state
// reads as stone so terrain never has holes.
func blockOfCanonical(s string) Block {
	name, rest, _ := strings.Cut(s, "[")
	name = strings.TrimPrefix(name, "minecraft:")
	var props map[string]string
	if rest != "" {
		props = map[string]string{}
		for _, kv := range strings.Split(strings.TrimSuffix(rest, "]"), ",") {
			k, v, _ := strings.Cut(kv, "=")
			props[k] = v
		}
	}
	if b, ok := BlockStateOf(name, props); ok {
		return b
	}
	return Stone
}
