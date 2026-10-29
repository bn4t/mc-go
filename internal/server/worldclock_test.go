package server

import (
	"math"
	"mc-go/internal/world"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWorldClockSavedStateAndCapturedEnvironment(t *testing.T) {
	s := New("unused")
	payload := map[string]any{"data": map[string]any{"minecraft:overworld": map[string]any{"total_ticks": int64(500), "partial_tick": float32(.25), "rate": float32(.5)}, "minecraft:the_end": map[string]any{"total_ticks": int64(42), "partial_tick": float32(.75), "rate": float32(2), "paused": int8(1)}}}
	if !s.restoreWorldClocks(payload) {
		t.Fatal("valid native state rejected")
	}
	s.GameTick()
	s.GameTick()
	if s.dayTime() != 501 || s.clock.dayState.partial != .25 || s.worldClockTotal(1) != 42 || s.clock.endState.partial != .75 || s.TickCount() != 2 {
		t.Fatalf("progress day%d state%#v end%d state%#v age%d", s.dayTime(), s.clock.dayState, s.worldClockTotal(1), s.clock.endState, s.TickCount())
	}
	restore := s.activateTestEnvironment(s.dims[dimOverworld], map[string]any{"type": "minecraft:clock_time", "clock": "minecraft:overworld", "time": int32(13000)})
	if s.dayTime() != 13000 || s.clock.dayState.partial != 0 || s.clock.dayState.baseRate() != .5 {
		t.Fatal("environment did not target total/fraction")
	}
	s.GameTick()
	s.GameTick()
	restore()
	if s.dayTime() != 501 || s.clock.dayState.partial != 0 || s.worldClockTotal(1) != 42 || s.clock.endState.partial != .75 || s.TickCount() != 4 {
		t.Fatal("restoration changed another clock or kept elapsed time")
	}
	s.rulesMu.Lock()
	s.gameRules["minecraft:advance_time"] = "false"
	s.rulesMu.Unlock()
	s.GameTick()
	if s.dayTime() != 501 || s.worldClockTotal(1) != 42 || s.TickCount() != 5 {
		t.Fatal("global rule did not pause clocks independently of age")
	}
}
func TestWorldClockSnapshotOwnsNativePayload(t *testing.T) {
	s := New("unused")
	s.clock.day = 501
	s.clock.dayState = worldClockState{rate: .5}
	s.clock.end = 42
	s.clock.endState = worldClockState{partial: .75, rate: 2, paused: true}
	payload := s.captureWorldClocks()
	snapshot := nativeWorldSaveSnapshot{level: world.LevelData{Dir: t.TempDir(), GameTime: 9000, DayTime: 9999, ClockStates: payload["data"].(map[string]any)}}
	s.clock.day = 999
	s.clock.dayState = worldClockState{rate: 3}
	s.clock.end = 888
	s.clock.endState = worldClockState{}
	if err := snapshot.write(); err != nil {
		t.Fatal(err)
	}
	saved, err := readPlayerGzipNBT(filepath.Join(snapshot.level.Dir, "data", "minecraft", "world_clocks.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved, payload) {
		t.Fatalf("save lost detached clock state %#v want%#v", saved, payload)
	}
	restored := New("unused")
	restored.WorldDir = snapshot.level.Dir
	restored.loadWorldClocks()
	if restored.dayTime() != 501 || restored.clock.dayState.baseRate() != .5 || restored.worldClockTotal(1) != 42 || restored.clock.endState.partial != .75 || !restored.clock.endState.paused {
		t.Fatal("native restart lost state")
	}
}
func TestWorldClockLeafCodec(t *testing.T) {
	for _, body := range []string{`{"type":"clock_time","clock":"overworld","time":0}`, `{"type":"clock_time","clock":"the_end","time":2147483647}`} {
		raw, _ := decodeGameTestJSON([]byte(body))
		if _, err := parseGameTestEnvironment(raw); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{`{"type":"clock_time","clock":"overworld","time":-1}`, `{"type":"clock_time","clock":"overworld"}`, `{"type":"clock_time","clock":"testpin:missing","time":1}`} {
		raw, _ := decodeGameTestJSON([]byte(body))
		if _, err := parseGameTestEnvironment(raw); err == nil {
			t.Fatal("unsupported clock accepted", body)
		}
	}
	for _, rate := range []float32{0, -1, float32(math.Inf(1)), float32(math.NaN())} {
		if _, _, ok := readWorldClock(map[string]any{"total_ticks": int64(0), "rate": rate}); ok {
			t.Fatal("invalid saved rate accepted", rate)
		}
	}
	if value, state, ok := readWorldClock(map[string]any{"total_ticks": int64(math.MaxInt64), "partial_tick": float32(math.Copysign(0, -1))}); !ok || value != math.MaxInt64 || math.Float32bits(state.partial) != 0x80000000 {
		t.Fatal("native numeric bits lost")
	}
}

func TestWorldClockPartialMapAndMissingDefaults(t *testing.T) {
	row := func(total int64) any { return map[string]any{"total_ticks": total} }
	for _, tc := range []struct {
		data     any
		day, end int64
	}{{nil, 0, 0}, {map[string]any{}, 0, 0}, {map[string]any{"minecraft:overworld": row(100)}, 100, 0}, {map[string]any{"minecraft:overworld": row(100), "minecraft:the_end": row(200), "testpin:absent": row(300)}, 100, 200}, {map[string]any{"minecraft:overworld": row(100), "minecraft:the_end": map[string]any{}}, 100, 0}, {map[string]any{"minecraft:overworld": map[string]any{"total_ticks": int64(100), "partial_tick": "bad"}, "minecraft:the_end": row(200)}, 0, 200}} {
		s := New("unused")
		s.setGameTimeBase(4000)
		s.clock.day = 8000
		s.clock.end = 4000
		s.restoreWorldClocks(map[string]any{"data": tc.data})
		if s.dayTime() != tc.day || s.worldClockTotal(1) != tc.end || s.TickCount() != 4000 {
			t.Fatalf("partial data %#v: day%d end%d age%d", tc.data, s.dayTime(), s.worldClockTotal(1), s.TickCount())
		}
	}
}

func TestWorldClockCommandsKeepRateAndPause(t *testing.T) {
	s := New("unused")
	s.clock.day = 500
	s.clock.dayState = worldClockState{partial: .25, rate: .5, paused: true}
	s.clock.end = 42
	s.clock.endState = worldClockState{partial: .75, rate: 2, paused: true}
	s.runTimeSet("", []string{"1"}, true)
	if s.dayTime() != 501 || s.clock.dayState.partial != .25 || s.clock.dayState.baseRate() != .5 || !s.clock.dayState.paused {
		t.Fatal("add changed state")
	}
	s.runTimeSet("", []string{"-1000"}, true)
	if s.dayTime() != 0 || s.clock.dayState.partial != .25 {
		t.Fatal("add did not clamp with fraction retained")
	}
	s.runTimeSet("", []string{"1234"}, false)
	if s.dayTime() != 1234 || s.clock.dayState.partial != 0 || !s.clock.dayState.paused || s.clock.dayState.baseRate() != .5 {
		t.Fatal("set changed rate/pause")
	}
	s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimEnd]}, func() reply { return s.runTimeSet(neutralCommandActor, []string{"2"}, true) })
	if s.worldClockTotal(1) != 44 || s.clock.endState.partial != .75 || s.dayTime() != 1234 {
		t.Fatal("End add wrong clock")
	}
	s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimNether]}, func() reply { return s.runTimeSet(neutralCommandActor, []string{"123"}, false) })
	s.runTimeSet("", []string{"-1"}, false)
	if s.dayTime() != 1234 || s.worldClockTotal(1) != 44 {
		t.Fatal("rejected command changed clocks")
	}
}

func TestWorldClockDefaultRoutingKeepsAddParseFailure(t *testing.T) {
	s := New("unused")
	r := s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimNether]}, func() reply { return s.runTimeSet(neutralCommandActor, []string{"abc"}, true) })
	if r.fail == nil {
		t.Fatal("invalid ADD token reached default-clock runtime lookup")
	}
}

// Native public Mth.floor(float) API measurements, after the clock's float32
// addition: NaN becomes zero and out-of-range values saturate in both directions.
func TestWorldClockNativeFloorBoundaries(t *testing.T) {
	for _, tc := range []struct {
		partial   float32
		whole     int64
		remainder float32
	}{
		{float32(math.NaN()), 0, float32(math.NaN())},
		{float32(math.Inf(-1)), math.MinInt32, float32(math.Inf(-1))},
		{math.Float32frombits(0xcf000001), math.MinInt32, -256},
		{float32(math.MinInt32), math.MinInt32, 0},
		{math.Float32frombits(0x4effffff), 2147483520, 0},
		{float32(2147483648), math.MaxInt32, 0},
		{float32(math.Inf(1)), math.MaxInt32, float32(math.Inf(1))},
		{-1.5, -1, .5},
	} {
		total := int64(0)
		state := worldClockState{partial: tc.partial, rate: 1}
		advanceWorldClock(&total, &state)
		nan := math.IsNaN(float64(tc.remainder)) && math.IsNaN(float64(state.partial))
		if total != tc.whole || !nan && math.Float32bits(state.partial) != math.Float32bits(tc.remainder) {
			t.Fatalf("partial%08x got total%d remainder%08x want%d/%08x", math.Float32bits(tc.partial), total, math.Float32bits(state.partial), tc.whole, math.Float32bits(tc.remainder))
		}
	}
}

func TestWorldClockEndRejectsOverworldMarker(t *testing.T) {
	s := New("unused")
	s.clock.end = 44
	s.clock.endState = worldClockState{partial: .75, rate: 2, paused: true}
	for _, marker := range []string{"day", "noon", "night", "midnight"} {
		r := s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimEnd]}, func() reply { return s.runTimeSet(neutralCommandActor, []string{marker}, false) })
		if r.outcome != commandFailed || s.clock.end != 44 || s.clock.endState.partial != .75 || !s.clock.endState.paused || s.clock.endState.baseRate() != 2 {
			t.Fatal("End accepted Overworld marker", marker)
		}
	}
}
