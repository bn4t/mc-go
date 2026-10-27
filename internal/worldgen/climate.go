package worldgen

import "sync"

// biomeClimate is a biome's temperature settings.
type biomeClimate struct {
	temperature   float32
	frozen        bool
	precipitation bool
}

// The fixed legacy noises biome temperatures use: a height-decay
// wobble, and the frozen modifier's patch and info noises.
var (
	climateNoiseOnce sync.Once
	temperatureNoise *PerlinSimplex
	frozenNoise      *PerlinSimplex
	biomeInfoNoise   *PerlinSimplex
)

func climateNoises() {
	climateNoiseOnce.Do(func() {
		temperatureNoise = NewPerlinSimplex(NewLegacy(1234), []int{0})
		frozenNoise = NewPerlinSimplex(NewLegacy(3456), []int{-2, -1, 0})
		biomeInfoNoise = NewPerlinSimplex(NewLegacy(2345), []int{0})
	})
}

// BiomeTemperature is a biome's temperature at a block: the base value
// (frozen biomes drop to 0.2 in patches of warm water), decaying with
// height above seaLevel+17 with a noise wobble.
func BiomeTemperature(biome string, x, y, z int32, seaLevel int32) float32 {
	c := biomeClimates[biome]
	climateNoises()
	t := c.temperature
	if c.frozen {
		patch := float64(frozenNoise.Value(float64(x)*0.05, float64(z)*0.05) * 7)
		info := biomeInfoNoise.Value(float64(x)*0.2, float64(z)*0.2)
		if patch+info < 0.3 && biomeInfoNoise.Value(float64(x)*0.09, float64(z)*0.09) < 0.8 {
			t = 0.2
		}
	}
	boundary := seaLevel + 17
	if y > boundary {
		wobble := float32(temperatureNoise.Value(float64(float32(x)/8), float64(float32(z)/8)) * 8)
		return t - float32(float32(wobble+float32(y)-float32(boundary))*0.05)/40
	}
	return t
}

// ColdEnoughToSnow is the biome's freezing test at a block.
func ColdEnoughToSnow(biome string, x, y, z, seaLevel int32) bool {
	return !(BiomeTemperature(biome, x, y, z, seaLevel) >= 0.15)
}
