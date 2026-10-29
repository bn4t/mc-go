package worldgen

import (
	"encoding/json"
	"math"
	"strings"
	"sync"
)

func jsonUnmarshal(raw json.RawMessage, v any) {
	if err := json.Unmarshal(raw, v); err != nil {
		panic(err)
	}
}

// BiomeInfo is the part of a biome definition generation reads.
type BiomeInfo struct {
	Name        string
	Temperature float32
	Frozen      bool // temperature_modifier frozen
	raw         obj

	featOnce, featSetOnce sync.Once
	features              [][]string
	featSet               map[string]bool
}

// BiomeInfo returns a biome's definition from the state's data.
func (rs *RandomState) BiomeInfo(name string) *BiomeInfo {
	rs.biomeMu.Lock()
	defer rs.biomeMu.Unlock()
	if rs.biomes == nil {
		rs.biomes = map[string]*BiomeInfo{}
	}
	m := rs.biomes
	if b, ok := m[name]; ok {
		return b
	}
	raw, ok := rs.data.Get("biome/" + name)
	if !ok {
		panic("worldgen: unknown biome " + name)
	}
	var o obj
	jsonUnmarshal(raw, &o)
	b := &BiomeInfo{Name: name, raw: o}
	b.Temperature = parseF32(o["temperature"])
	if tm, ok := o["temperature_modifier"]; ok {
		var s string
		jsonUnmarshal(tm, &s)
		b.Frozen = s == "frozen"
	}
	m[name] = b
	return b
}

// PerlinSimplexNoise is the legacy simplex octave stack biomes use for
// temperature variation.
type PerlinSimplexNoise struct {
	levels      []*SimplexNoise
	inFactor    float64
	valueFactor float64
}

func newPerlinSimplex(r Random, octaves []int) *PerlinSimplexNoise {
	first, last := octaves[0], octaves[0]
	set := map[int]bool{}
	for _, o := range octaves {
		set[o] = true
		first = min(first, o)
		last = max(last, o)
	}
	n := -first + last + 1
	p := &PerlinSimplexNoise{levels: make([]*SimplexNoise, n)}
	base := NewSimplexNoise(r)
	zero := last
	if zero >= 0 && zero < n && set[0] {
		p.levels[zero] = base
	}
	for i := zero + 1; i < n; i++ {
		if i >= 0 && set[zero-i] {
			p.levels[i] = NewSimplexNoise(r)
		} else {
			r.Consume(262)
		}
	}
	if last > 0 {
		panic("worldgen: positive simplex octaves unsupported")
	}
	p.inFactor = math.Pow(2, float64(last))
	p.valueFactor = 1 / (math.Pow(2, float64(n)) - 1)
	return p
}

func (p *PerlinSimplexNoise) value(x, z float64, offsets bool) float64 {
	var sum float64
	in := p.inFactor
	vf := p.valueFactor
	for _, lvl := range p.levels {
		if lvl != nil {
			ox, oy := 0.0, 0.0
			if offsets {
				ox, oy = lvl.xo, lvl.yo
			}
			sum += float64(lvl.Value2D(float64(x*in)+ox, float64(z*in)+oy) * vf)
		}
		in /= 2
		vf *= 2
	}
	return sum
}

// worldgenRandomLegacy is WorldgenRandom over a LegacyRandomSource, which
// draws its bits straight from the LCG.
func worldgenRandomLegacy(seed int64) Random { return NewLegacy(seed) }

var (
	biomeNoiseOnce                                           sync.Once
	temperatureNoise, frozenTemperatureNoise, biomeInfoNoise *PerlinSimplexNoise
)

func biomeNoises() {
	biomeNoiseOnce.Do(func() {
		temperatureNoise = newPerlinSimplex(worldgenRandomLegacy(1234), []int{0})
		frozenTemperatureNoise = newPerlinSimplex(worldgenRandomLegacy(3456), []int{-2, -1, 0})
		biomeInfoNoise = newPerlinSimplex(worldgenRandomLegacy(2345), []int{0})
	})
}

// TemperatureAt is Biome.getTemperature(pos, seaLevel) (uncached form).
func (b *BiomeInfo) TemperatureAt(x, y, z, seaLevel int32) float32 {
	biomeNoises()
	t := b.Temperature
	if b.Frozen {
		d := float64(frozenTemperatureNoise.value(float64(float64(x)*0.05), float64(float64(z)*0.05), false) * 7)
		e := biomeInfoNoise.value(float64(float64(x)*0.2), float64(float64(z)*0.2), false)
		if d+e < 0.3 {
			if biomeInfoNoise.value(float64(float64(x)*0.09), float64(float64(z)*0.09), false) < 0.8 {
				t = 0.2
			}
		}
	}
	limit := seaLevel + 17
	if y > limit {
		f := float32(temperatureNoise.value(float64(float32(x)/8), float64(float32(z)/8), false) * 8)
		return t - float32(float32(f+float32(y)-float32(limit))*0.05)/40
	}
	return t
}

// ColdEnoughToSnow is Biome.coldEnoughToSnow.
func (b *BiomeInfo) ColdEnoughToSnow(x, y, z, seaLevel int32) bool {
	return !(b.TemperatureAt(x, y, z, seaLevel) >= 0.15)
}

func trimNS(s string) string { return strings.TrimPrefix(s, "minecraft:") }
