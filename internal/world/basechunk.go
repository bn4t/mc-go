package world

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"mc-go/internal/event"
	"mc-go/internal/nbt"
)

// BaseChunk is a finished generated (or loaded) chunk column: the blocks
// the world reads wherever it holds no override, and the chunk's 4x4x4
// biome cells.
type BaseChunk struct {
	MinY, Height int
	// Sections holds 4096 states per section in YZX order; a nil
	// section is all air.
	Sections [][]Block
	// Biomes holds 64 cells per section ((qy*4+qz)*4+qx within the
	// section), full ids like "minecraft:plains".
	Biomes []string
}

// At returns the state at chunk-local x/z and absolute y.
func (c *BaseChunk) At(x, y, z int) Block {
	if y < c.MinY || y >= c.MinY+c.Height {
		return Air
	}
	s := c.Sections[(y-c.MinY)>>4]
	if s == nil {
		return Air
	}
	return s[((y-c.MinY)&15)<<8|z<<4|x]
}

// BiomeCell returns the biome of the quart cell holding (x, y, z),
// chunk-local x/z, absolute y, clamped into the column.
func (c *BaseChunk) BiomeCell(x, y, z int) string {
	if len(c.Biomes) == 0 {
		return ""
	}
	qy := (y - c.MinY) >> 2
	if qy < 0 {
		qy = 0
	}
	if max := c.Height/4 - 1; qy > max {
		qy = max
	}
	sec := qy >> 2
	return c.Biomes[sec*64+((qy&3)*4+(z>>2))*4+(x>>2)]
}

// ChunkProvider supplies base chunks on demand (generating them). It
// must be safe for concurrent use; the world calls it once per chunk.
type ChunkProvider interface {
	ProvideChunk(cx, cz int) *BaseChunk
}

type baseEntry struct {
	once  sync.Once
	chunk atomic.Pointer[BaseChunk]
}

func (e *baseEntry) get() *BaseChunk  { return e.chunk.Load() }
func (e *baseEntry) set(c *BaseChunk) { e.chunk.Store(c) }

// baseCache is a world's base chunk store over a provider. Lookups are
// on every block read, so the index is a sync.Map.
type baseCache struct {
	provider ChunkProvider
	chunks   sync.Map // [2]int -> *baseEntry
}

func (b *baseCache) entry(cx, cz int) *baseEntry {
	k := [2]int{cx, cz}
	if e, ok := b.chunks.Load(k); ok {
		return e.(*baseEntry)
	}
	e, _ := b.chunks.LoadOrStore(k, &baseEntry{})
	return e.(*baseEntry)
}

func (b *baseCache) chunk(cx, cz int) *BaseChunk {
	e := b.entry(cx, cz)
	e.once.Do(func() { e.set(b.provider.ProvideChunk(cx, cz)) })
	return e.get()
}

// peek returns the chunk if it is already generated or loaded.
func (b *baseCache) peek(cx, cz int) *BaseChunk {
	if e, ok := b.chunks.Load([2]int{cx, cz}); ok {
		return e.(*baseEntry).get()
	}
	return nil
}

// install records a chunk read from disk as the base for (cx, cz),
// replacing generation.
func (b *baseCache) install(cx, cz int, c *BaseChunk) {
	e := b.entry(cx, cz)
	e.once.Do(func() {})
	e.set(c)
}

func (b *baseCache) keys() [][2]int {
	var out [][2]int
	b.chunks.Range(func(k, v any) bool {
		if v.(*baseEntry).get() != nil {
			out = append(out, k.([2]int))
		}
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// NewProvided builds a world whose terrain comes chunk by chunk from a
// provider (the noise generator, or chunks loaded from disk).
func NewProvided(bus *event.Bus, shards int, p ChunkProvider) *World {
	b := &baseCache{provider: p}
	gen := func(pos Pos) Block {
		c := b.chunk(pos.X>>4, pos.Z>>4)
		return c.At(pos.X&15, pos.Y, pos.Z&15)
	}
	w := newGenWorld(bus, shards, gen)
	w.base = b
	return w
}

// Provided reports whether the world's terrain comes from a provider.
func (w *World) Provided() bool { return w.base != nil }

// BaseChunk returns the provided base chunk for (cx, cz), generating it
// if needed; nil for a generator-function world.
func (w *World) BaseChunk(cx, cz int) *BaseChunk {
	if w.base == nil {
		return nil
	}
	return w.base.chunk(cx, cz)
}

// PeekBaseChunk returns the base chunk for (cx, cz) only when it has
// already been generated or loaded (nil otherwise, and for a
// generator-function world).
func (w *World) PeekBaseChunk(cx, cz int) *BaseChunk {
	if w.base == nil {
		return nil
	}
	return w.base.peek(cx, cz)
}

// BiomeAt returns the biome id at a block position: the provided
// chunk's quart cell, or the world's single biome.
func (w *World) BiomeAt(x, y, z int) string {
	if w.base != nil {
		if c := w.base.chunk(x>>4, z>>4); c != nil {
			if b := c.BiomeCell(x&15, y, z&15); b != "" {
				return b
			}
		}
	}
	return w.biomeName()
}

// ChunkBiomes returns a provided chunk's biome cells (nil when the
// world has a single biome).
func (w *World) ChunkBiomes(cx, cz int) []string {
	if w.base == nil {
		return nil
	}
	if c := w.base.chunk(cx, cz); c != nil {
		return c.Biomes
	}
	return nil
}

// installBaseChunk reads a saved full chunk straight into the base
// cache: a provided world trusts its saved terrain instead of
// regenerating and diffing it.
func (w *World) installBaseChunk(compound map[string]any, cx, cz, minY, height int) {
	if status, _ := compound["Status"].(string); status != "minecraft:full" {
		return
	}
	if list, ok := compound["block_entities"].(nbt.List); ok {
		for _, beAny := range list.Items {
			if be, ok := beAny.(map[string]any); ok {
				w.blockEntities = append(w.blockEntities, be)
			}
		}
	}
	c := &BaseChunk{MinY: minY, Height: height, Sections: make([][]Block, height/16), Biomes: make([]string, height/16*64)}
	for i := range c.Biomes {
		c.Biomes[i] = w.biomeName()
	}
	sections, _ := compound["sections"].(nbt.List)
	for _, secAny := range sections.Items {
		sec, ok := secAny.(map[string]any)
		if !ok {
			continue
		}
		syb, ok := sec["Y"].(int8)
		if !ok {
			continue
		}
		si := (int(syb)*16 - minY) / 16
		if si < 0 || si >= len(c.Sections) {
			continue
		}
		if states, ok := sec[chunkBlockStates].(map[string]any); ok {
			if blocks, ok := decodeSectionStates(states); ok {
				c.Sections[si] = blocks
			}
		}
		if b, ok := sec["biomes"].(map[string]any); ok {
			pal, _ := b["palette"].(nbt.List)
			names := make([]string, len(pal.Items))
			for i, p := range pal.Items {
				names[i], _ = p.(string)
			}
			if len(names) == 1 {
				for i := 0; i < 64; i++ {
					c.Biomes[si*64+i] = names[0]
				}
			} else if data, ok := b["data"].([]int64); ok && len(names) > 1 {
				bpe := 0
				for 1<<uint(bpe) < len(names) {
					bpe++
				}
				per := 64 / bpe
				for i := 0; i < 64 && i/per < len(data); i++ {
					v := int((data[i/per] >> (uint(i%per) * uint(bpe))) & (1<<uint(bpe) - 1))
					if v < len(names) {
						c.Biomes[si*64+i] = names[v]
					}
				}
			}
		}
	}
	w.base.install(cx, cz, c)
}

// decodeSectionStates expands a saved block_states container; false if
// any palette entry is unknown.
func decodeSectionStates(states map[string]any) ([]Block, bool) {
	pal, ok := states["palette"].(nbt.List)
	if !ok || len(pal.Items) == 0 {
		return nil, false
	}
	blocks := make([]Block, len(pal.Items))
	allAir := true
	for i, pAny := range pal.Items {
		pc, ok := pAny.(map[string]any)
		if !ok {
			return nil, false
		}
		name, _ := pc["Name"].(string)
		var props map[string]string
		if pm, ok := pc["Properties"].(map[string]any); ok {
			props = make(map[string]string, len(pm))
			for k, v := range pm {
				if s, ok := v.(string); ok {
					props[k] = s
				}
			}
		}
		blk, ok := BlockStateOf(strings.TrimPrefix(name, "minecraft:"), props)
		if !ok {
			return nil, false
		}
		blocks[i] = blk
		if blk != Air {
			allAir = false
		}
	}
	if allAir {
		return nil, true
	}
	out := make([]Block, 4096)
	if data, ok := states["data"].([]int64); ok && len(blocks) > 1 {
		idx := unpackIndices(data, len(blocks))
		for i := range out {
			if int(idx[i]) < len(blocks) {
				out[i] = blocks[idx[i]]
			}
		}
	} else {
		for i := range out {
			out[i] = blocks[0]
		}
	}
	return out, true
}
