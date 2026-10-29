package server

import (
	"math"
	"mc-go/internal/nbt"
	"testing"
)

func TestWeatherFloat32LevelsAndEffectiveThunder(t *testing.T) {
	s := New("unused")
	s.weatherRaining.Store(true)
	s.weatherThunder.Store(true)
	for tick := 1; tick <= 101; tick++ {
		s.weatherTickStep()
		if tick == 5 && uint32(s.weatherRain.Load()) != 0x3d4ccccc {
			t.Fatalf("tick5 rain bits%08x", uint32(s.weatherRain.Load()))
		}
		if tick == 94 && s.isThundering() {
			t.Fatal("thunder effective before tick95")
		}
		if tick == 95 && !s.isThundering() {
			t.Fatal("thunder not effective at tick95")
		}
	}
	if s.rainLevel() != 1 || s.thunderLevel() != 1 {
		t.Fatal("did not clamp", s.rainLevel(), s.thunderLevel())
	}
	s.weatherRaining.Store(false)
	s.weatherThunder.Store(false)
	for tick := 0; tick < 101; tick++ {
		s.weatherTickStep()
	}
	if math.Float32bits(s.rainLevel()) != 0 || math.Float32bits(s.thunderLevel()) != 0 {
		t.Fatal("did not clamp to zero")
	}
}
func TestTestEnvironmentCompositionRestoresReverseOrder(t *testing.T) {
	s := New("unused")
	s.setDifficultyQuiet(0)
	child := func(level string) any { return map[string]any{"type": "minecraft:difficulty", "difficulty": level} }
	definition := map[string]any{"type": "minecraft:all_of", "definitions": nbt.List{Elem: nbt.TagCompound, Items: []any{child("easy"), child("hard")}}}
	restore := s.activateTestEnvironment(s.dims[dimOverworld], definition)
	if s.difficulty != 3 {
		t.Fatal("setup", s.difficulty)
	}
	restore()
	if s.difficulty != 0 {
		t.Fatal("restore", s.difficulty)
	}
}
func TestTestEnvironmentWeatherCapturesEffectiveState(t *testing.T) {
	s := New("unused")
	s.weatherRaining.Store(true)
	definition := map[string]any{"type": "minecraft:weather", "weather": "thunder"}
	restore := s.activateTestEnvironment(s.dims[dimOverworld], definition)
	if !s.weatherThunder.Load() || s.weatherTicks.Load() != 100000 {
		t.Fatal("thunder preset")
	}
	restore()
	if s.weatherRaining.Load() || s.weatherThunder.Load() {
		t.Fatal("raw flag captured instead of effective clear state")
	}
	setWeatherLevel(&s.weatherRain, .7)
	restore = s.activateTestEnvironment(s.dims[dimOverworld], definition)
	restore()
	if !s.weatherRaining.Load() || s.weatherThunder.Load() {
		t.Fatal("effective rain not restored")
	}
}

func TestTestEnvironmentLeafCodec(t *testing.T) {
	for _, body := range []string{`{"type":"difficulty","difficulty":"hard","ignored":1}`, `{"type":"weather","weather":"thunder","ignored":1}`} {
		raw, err := decodeGameTestJSON([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		got, err := parseGameTestEnvironment(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatal("unknown fields retained", got)
		}
	}
	for _, body := range []string{`{"type":"difficulty","difficulty":"HARD"}`, `{"type":"weather","weather":1}`, `{"type":"weather"}`, `{"type":"functions"}`} {
		raw, _ := decodeGameTestJSON([]byte(body))
		if _, err := parseGameTestEnvironment(raw); err == nil {
			t.Fatal("unsupported environment accepted", body)
		}
	}
}
