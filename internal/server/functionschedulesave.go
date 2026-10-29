package server

import (
	"math"
	"os"
	"path/filepath"
	"sort"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

const functionScheduleCallback = "minecraft:function"

// Payload construction runs under simulation ownership and allocates every
// mutable container anew. The save worker never traverses the live queue.
func (s *Server) captureFunctionSchedule() map[string]any {
	events := append([]scheduledFunction(nil), s.functionSchedule...)
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].age != events[j].age {
			return events[i].age < events[j].age
		}
		return events[i].serial < events[j].serial
	})
	entries := make([]any, 0, len(events))
	for _, event := range events {
		entries = append(entries, map[string]any{"id": event.name, "trigger_time": event.age, "callback": map[string]any{"type": event.callbackType, "id": event.target}})
	}
	return map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"events": nbt.List{Elem: 10, Items: entries}}}
}

func writeFunctionSchedule(dir string, payload map[string]any) error {
	path := filepath.Join(dir, "data", "minecraft", "scheduled_events.dat")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return writePlayerGzipNBT(path, payload)
}

func (s *Server) loadFunctionSchedule() {
	s.functionSchedule = nil
	s.functionScheduleSerial = 0
	if s.WorldDir == "" {
		return
	}
	root, err := readPlayerGzipNBT(filepath.Join(s.WorldDir, "data", "minecraft", "scheduled_events.dat"))
	if err != nil {
		return
	}
	s.restoreFunctionSchedule(root)
}

func (s *Server) restoreFunctionSchedule(root any) {
	compound, ok := root.(map[string]any)
	if !ok {
		return
	}
	data, ok := compound["data"].(map[string]any)
	if !ok {
		return
	}
	entries, ok := data["events"].(nbt.List)
	if !ok || entries.Elem != 10 {
		return
	}
	type identity struct {
		name string
		age  int64
	}
	seen := make(map[identity]bool)
	for _, entry := range entries.Items {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, ok := m["id"].(string)
		if !ok {
			continue
		}
		age, ok := savedFunctionAge(m["trigger_time"])
		if !ok {
			continue
		}
		callback, ok := m["callback"].(map[string]any)
		if !ok {
			continue
		}
		kind, ok := callback["type"].(string)
		if !ok {
			continue
		}
		kind, ok = jigsawIdentifier(kind)
		if !ok {
			continue
		}
		// Preserve native tag callback data for round trips and /schedule clear;
		// tag execution remains outside the currently modeled function registry.
		if kind != functionScheduleCallback && kind != "minecraft:function_tag" {
			continue
		}
		target, ok := callback["id"].(string)
		if !ok {
			continue
		}
		target, ok = jigsawIdentifier(target)
		if !ok {
			continue
		}
		key := identity{name, age}
		if seen[key] {
			continue
		}
		seen[key] = true
		s.functionScheduleSerial++
		s.functionSchedule = append(s.functionSchedule, scheduledFunction{name: name, target: target, callbackType: kind, age: age, serial: s.functionScheduleSerial})
	}
}

func savedFunctionAge(value any) (int64, bool) {
	var f float64
	switch n := value.(type) {
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float32:
		f = float64(n)
	case float64:
		f = n
	default:
		return 0, false
	}
	if math.IsNaN(f) {
		return 0, true
	}
	if f >= float64(math.MaxInt64) {
		return math.MaxInt64, true
	}
	if f <= float64(math.MinInt64) {
		return math.MinInt64, true
	}
	return int64(f), true
}
