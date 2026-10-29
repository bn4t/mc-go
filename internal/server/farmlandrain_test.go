package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestFarmlandRainHydration(t *testing.T) {
	for _, tc := range []struct {
		biome string
		roof  bool
		want  int
	}{
		{"minecraft:plains", false, 7},
		{"minecraft:plains", true, 2},
		{"minecraft:desert", false, 2},
		{"minecraft:snowy_plains", false, 2},
	} {
		roof := tc.roof
		s, d := randSim(t)
		d.biome = tc.biome
		pos := world.Pos{X: 40, Y: -60, Z: 40}
		for y := -58; y < d.minY+d.height; y++ {
			randPut(t, d, 40, y, 40, "air", nil)
		}
		randPut(t, d, 40, -60, 40, "farmland", map[string]string{"moisture": "3"})
		randPut(t, d, 40, -59, 40, "wheat", map[string]string{"age": "7"})
		if roof {
			randPut(t, d, 40, -57, 40, "glass", nil)
		}
		s.weatherRaining.Store(true)
		farmlandRule(s, d, pos, d.w.GetBlock(pos))
		got, _ := intProp(d.w.GetBlock(pos), "moisture")
		want := tc.want
		if got != want {
			t.Errorf("biome=%s roof=%v: moisture=%d, want %d", tc.biome, roof, got, want)
		}
	}
}
