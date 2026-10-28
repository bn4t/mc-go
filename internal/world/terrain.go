package world

import (
	"strings"
	"sync"

	"mc-go/internal/worldgen"
)

// NoiseTerrain serves a noise generator's terrain as a world's base
// layer: each generated state, interned by the generator as a canonical
// "namespace:name[key=value,...]" string, resolves once to a block.
type NoiseTerrain struct {
	gen    *worldgen.Generator
	mu     sync.RWMutex
	blocks []Block
}

// NewNoiseTerrain wraps a generator.
func NewNoiseTerrain(gen *worldgen.Generator) *NoiseTerrain { return &NoiseTerrain{gen: gen} }

// Generator is the wrapped generator.
func (t *NoiseTerrain) Generator() *worldgen.Generator { return t.gen }

// Ready reports whether chunk (cx, cz)'s terrain is generated.
func (t *NoiseTerrain) Ready(cx, cz int) bool { return t.gen.Ready(int32(cx), int32(cz)) }

// Prefetch asks for chunk (cx, cz)'s terrain in the background.
func (t *NoiseTerrain) Prefetch(cx, cz int) { t.gen.Prefetch(int32(cx), int32(cz)) }

// Block is the generated block at p.
func (t *NoiseTerrain) Block(p Pos) Block {
	return t.resolve(t.gen.BlockAt(int32(p.X), int32(p.Y), int32(p.Z)))
}

// Biome is the generated biome at quart (qx, qy, qz).
func (t *NoiseTerrain) Biome(qx, qy, qz int) string {
	return t.gen.BiomeAt(int32(qx), int32(qy), int32(qz))
}

func (t *NoiseTerrain) resolve(id worldgen.StateID) Block {
	t.mu.RLock()
	if int(id) < len(t.blocks) {
		b := t.blocks[id]
		t.mu.RUnlock()
		return b
	}
	t.mu.RUnlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	for int(id) >= len(t.blocks) {
		t.blocks = append(t.blocks, blockOfCanonical(worldgen.StateName(worldgen.StateID(len(t.blocks)))))
	}
	return t.blocks[id]
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
