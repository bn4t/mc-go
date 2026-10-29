package worldgen

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Climate parameter axes in vanilla's ParameterPoint order.
const (
	climTemperature = iota
	climHumidity
	climContinentalness
	climErosion
	climDepth
	climWeirdness
	climAxes
)

// quantizeCoord is Climate.quantizeCoord: (long)(f * 10000f).
func quantizeCoord(f float32) int64 { return f2l(f * 10000) }

func f2l(f float32) int64 { return d2l(float64(f)) }

type climParam struct{ min, max int64 }

func (p climParam) distance(t int64) int64 {
	l := t - p.max
	m := p.min - t
	if l > 0 {
		return l
	}
	return max(m, 0)
}

type biomeEntry struct {
	biome  string
	params [climAxes]climParam
	offset int64
}

func (e *biomeEntry) fitness(t *[climAxes]int64) int64 {
	var sum int64
	for i := 0; i < climAxes; i++ {
		d := e.params[i].distance(t[i])
		sum += d * d
	}
	return sum + e.offset*e.offset
}

// BiomeSource is a multi-noise biome source over a parameter list.
type BiomeSource struct {
	entries []biomeEntry
	tree    *rtree
}

// NewMultiNoiseBiomeSource loads a biome_parameters list ("overworld",
// "nether").
func NewMultiNoiseBiomeSource(data Data, preset string) (*BiomeSource, error) {
	raw, ok := data.Get("biome_parameters/" + preset)
	if !ok {
		return nil, fmt.Errorf("worldgen: unknown biome parameter list %q", preset)
	}
	var doc struct {
		Biomes []struct {
			Biome      string                     `json:"biome"`
			Parameters map[string]json.RawMessage `json:"parameters"`
		} `json:"biomes"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	names := [climAxes]string{"temperature", "humidity", "continentalness", "erosion", "depth", "weirdness"}
	bs := &BiomeSource{}
	for _, b := range doc.Biomes {
		e := biomeEntry{biome: strings.TrimPrefix(b.Biome, "minecraft:")}
		for i, n := range names {
			lo, hi, err := parseSpan(b.Parameters[n])
			if err != nil {
				return nil, fmt.Errorf("worldgen: %s %s: %w", b.Biome, n, err)
			}
			e.params[i] = climParam{quantizeCoord(lo), quantizeCoord(hi)}
		}
		off, _, err := parseSpan(b.Parameters["offset"])
		if err != nil {
			return nil, err
		}
		e.offset = quantizeCoord(off)
		bs.entries = append(bs.entries, e)
	}
	bs.tree = newRTree(bs.entries)
	return bs, nil
}

func parseSpan(raw json.RawMessage) (float32, float32, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, 0, fmt.Errorf("missing")
	}
	if s[0] == '[' {
		var parts []json.RawMessage
		if err := json.Unmarshal(raw, &parts); err != nil || len(parts) != 2 {
			return 0, 0, fmt.Errorf("bad span %s", s)
		}
		return parseF32(parts[0]), parseF32(parts[1]), nil
	}
	v, err := strconv.ParseFloat(s, 32)
	return float32(v), float32(v), err
}

// Biome returns the biome at quart position (qx, qy, qz). nc supplies
// the chunk's cached sampler (nil for an uncached lookup).
func (bs *BiomeSource) Biome(rs *RandomState, nc *NoiseChunk, qx, qy, qz int32) string {
	t := rs.sampleClimate(nc, qx, qy, qz)
	return bs.find(&t)
}

func (rs *RandomState) sampleClimate(nc *NoiseChunk, qx, qy, qz int32) [climAxes]int64 {
	c := &Ctx{X: quartToBlock(qx), Y: quartToBlock(qy), Z: quartToBlock(qz), nc: nc}
	r := &rs.router
	return [climAxes]int64{
		quantizeCoord(float32(r.Temperature.Compute(c))),
		quantizeCoord(float32(r.Vegetation.Compute(c))),
		quantizeCoord(float32(r.Continents.Compute(c))),
		quantizeCoord(float32(r.Erosion.Compute(c))),
		quantizeCoord(float32(r.Depth.Compute(c))),
		quantizeCoord(float32(r.Ridges.Compute(c))),
	}
}

func (bs *BiomeSource) find(t *[climAxes]int64) string {
	if bs.tree != nil {
		return bs.tree.search(t).biome
	}
	best := -1
	var bestD int64
	for i := range bs.entries {
		d := bs.entries[i].fitness(t)
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return bs.entries[best].biome
}

// ChunkBiomes fills a chunk's 4x4x4 biome cells, indexed
// ((qy-minQY)*4 + qz)*4 + qx for local quart x/z. Cells are resolved in
// vanilla's order — sections bottom up, then x, y, z within a section —
// with each lookup seeded by the previous result, which is how the real
// server's search breaks exact ties.
func (rs *RandomState) ChunkBiomes(bs *BiomeSource, nc *NoiseChunk, cx, cz int32) []string {
	minQY := floorDiv(rs.settings.minY, 4)
	n := rs.settings.height / 4
	out := make([]string, 16*n)
	var last *rnode
	for sy := int32(0); sy < n; sy += 4 {
		for qx := int32(0); qx < 4; qx++ {
			for dy := int32(0); dy < 4 && sy+dy < n; dy++ {
				qy := sy + dy
				for qz := int32(0); qz < 4; qz++ {
					t := rs.sampleClimate(nc, cx*4+qx, minQY+qy, cz*4+qz)
					var b string
					if bs.tree != nil {
						last = bs.tree.searchFrom(&t, last)
						b = last.leaf.biome
					} else {
						b = bs.find(&t)
					}
					out[(qy*4+qz)*4+qx] = b
				}
			}
		}
	}
	return out
}

// NewChunk builds the chunk's NoiseChunk (exported for staged callers).
func (rs *RandomState) NewChunk(cx, cz int32) *NoiseChunk { return newNoiseChunk(rs, cx, cz) }
