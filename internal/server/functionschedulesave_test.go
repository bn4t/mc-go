package server

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

func TestFunctionScheduleNativeSnapshotDetached(t *testing.T) {
	s, _ := randSim(t)
	s.WorldDir = t.TempDir()
	s.functionSchedule = []scheduledFunction{{name: "pin:later", target: "pin:target", callbackType: functionScheduleCallback, age: 25, serial: 1}, {name: "pin:earlier", target: "pin:target", callbackType: functionScheduleCallback, age: 15, serial: 2}}
	snapshot := s.captureNativeWorldSaveOwned()
	s.functionSchedule[0].target = "pin:changed"
	s.clearFunctionSchedule("pin:earlier")
	if err := snapshot.write(); err != nil {
		t.Fatal(err)
	}
	root, err := readPlayerGzipNBT(filepath.Join(s.WorldDir, "data", "minecraft", "scheduled_events.dat"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := &Server{}
	fresh.restoreFunctionSchedule(root)
	if len(fresh.functionSchedule) != 2 || fresh.functionSchedule[0].name != "pin:earlier" || fresh.functionSchedule[1].target != "pin:target" {
		t.Fatalf("snapshot retained live mutation: %v", fresh.functionSchedule)
	}
}

func TestFunctionScheduleRestorePartialAndOpaqueTagCallback(t *testing.T) {
	event := func(name, kind, target string, age int64) any {
		return map[string]any{"id": name, "trigger_time": age, "callback": map[string]any{"type": kind, "id": target}}
	}
	payload := map[string]any{"data": map[string]any{"events": nbt.List{Elem: 10, Items: []any{
		event("arbitrary event key", functionScheduleCallback, "pin:a", -1),
		event("arbitrary event key", functionScheduleCallback, "pin:b", -1),
		map[string]any{"id": "invalid missing callback", "trigger_time": int64(10)},
		event("unknown callback", "minecraft:unknown", "pin:a", 10),
		event("#pin:tag", "minecraft:function_tag", "pin:tag", 20),
	}}}}
	s := &Server{}
	s.restoreFunctionSchedule(payload)
	if len(s.functionSchedule) != 2 || s.functionSchedule[0].target != "pin:a" || s.functionSchedule[1].callbackType != "minecraft:function_tag" {
		t.Fatalf("partial queue %v", s.functionSchedule)
	}
	saved := s.captureFunctionSchedule()
	fresh := &Server{}
	fresh.restoreFunctionSchedule(saved)
	if !reflect.DeepEqual(s.functionSchedule, fresh.functionSchedule) {
		t.Fatalf("tag/keys changed on roundtrip: %v ->%v", s.functionSchedule, fresh.functionSchedule)
	}
	if n := fresh.clearFunctionSchedule("#pin:tag"); n != 1 {
		t.Fatalf("clear native tag key=%d", n)
	}
}

func TestFunctionScheduleRestoredTargetResolvedAtDispatch(t *testing.T) {
	s, d := randSim(t)
	s.functionSchedule = []scheduledFunction{{name: "key is not a resource id", target: "pin:target", callbackType: functionScheduleCallback, age: 0, serial: 1}}
	s.functions = map[string][]string{"pin:target": {"setblock 8 5 8 diamond_block"}}
	saved := s.captureFunctionSchedule()
	s.functionSchedule = nil
	s.restoreFunctionSchedule(saved)
	s.functions["pin:target"] = []string{"setblock 8 5 8 gold_block"}
	s.functionScheduleStep()
	if !isBlockNamed(d.w.GetBlock(world.Pos{X: 8, Y: 5, Z: 8}), "gold_block") {
		t.Fatal("restored key was used as function ID or stale definition captured")
	}
	if len(s.functionSchedule) != 0 {
		t.Fatal("executed event retained")
	}
}

func TestSavedFunctionAgeUsesNativeLongConversion(t *testing.T) {
	for _, c := range []struct {
		input any
		want  int64
	}{{int64(math.MaxInt64), math.MaxInt64}, {float64(1.9), 1}, {float64(-1.9), -1}, {math.Inf(1), math.MaxInt64}, {math.Inf(-1), math.MinInt64}, {math.NaN(), 0}} {
		got, ok := savedFunctionAge(c.input)
		if !ok || got != c.want {
			t.Errorf("%v =>%d,%v want%d", c.input, got, ok, c.want)
		}
	}
	if _, ok := savedFunctionAge("12"); ok {
		t.Fatal("string accepted as number")
	}
}
