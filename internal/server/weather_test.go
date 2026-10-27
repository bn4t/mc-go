package server

import "testing"

func TestWeatherClearExpiryFlipsBothOn(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	s.gameRules["minecraft:advance_weather"] = "true"
	w := &s.weather
	w.clear = 3
	for i := 0; i < 3; i++ {
		s.weatherTickStep()
		if w.raining || w.thundering || w.rain != 1 || w.thunder != 1 {
			t.Fatalf("tick %d: clear branch state %+v", i, w)
		}
	}
	s.weatherTickStep()
	if !w.raining || !w.thundering || w.rain != 0 || w.thunder != 0 {
		t.Fatalf("clear expiry did not flip both on: %+v", w)
	}
	s.weatherTickStep()
	if w.rain < weatherRainDuration[0] || w.rain > weatherRainDuration[1] ||
		w.thunder < weatherThunderDuration[0] || w.thunder > weatherThunderDuration[1] {
		t.Fatalf("durations not sampled while on: rain=%d thunder=%d", w.rain, w.thunder)
	}
}

func TestWeatherEasingAndThresholds(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	s.gameRules["minecraft:advance_weather"] = "false"
	w := &s.weather
	w.raining = true
	var want float32
	crossed := -1
	for i := 1; i <= 120; i++ {
		s.weatherTickStep()
		want = min(want+0.01, 1)
		if w.rainLevel != want {
			t.Fatalf("tick %d: rain level %v want float32-accumulated %v", i, w.rainLevel, want)
		}
		if crossed < 0 && s.weatherRaining.Load() {
			crossed = i
		}
	}
	if crossed < 20 || crossed > 21 {
		t.Fatalf("isRaining crossed 0.2 at tick %d", crossed)
	}
	if s.weatherThunder.Load() {
		t.Fatal("thunder without the thundering flag")
	}
	// With the rule off nothing counts down: the rain never ends.
	if !w.raining || w.rain != 0 {
		t.Fatalf("advance_weather=false moved the cycle: %+v", w)
	}
}

func TestWeatherCommandSharesTheTimer(t *testing.T) {
	s := New("unused")
	defer s.Stop()
	s.gameRules["minecraft:advance_weather"] = "true"
	w := &s.weather
	w.clear, w.rain, w.thunder, w.raining, w.thundering = 0, 2, 2, true, false
	s.weatherTickStep()
	s.weatherTickStep()
	if w.raining || !w.thundering {
		t.Fatalf("a rain timer shared with thunder must stop rain and start thunder together: %+v", w)
	}
	if v, bad, ok := parseTimeArg("20"); !ok || v != 20 || bad != 20 {
		t.Fatalf("bare weather durations are ticks: %d", v)
	}
	if v, _, _ := parseTimeArg("2s"); v != 40 {
		t.Fatalf("2s = %d ticks", v)
	}
}
