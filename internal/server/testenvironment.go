package server

import (
	"fmt"
	"mc-go/internal/nbt"
)

func parseGameTestEnvironmentLeaf(kind string, in map[string]any) (map[string]any, error) {
	key := ""
	var choices []string
	switch kind {
	case "minecraft:difficulty":
		key = "difficulty"
		choices = []string{"peaceful", "easy", "normal", "hard"}
	case "minecraft:weather":
		key = "weather"
		choices = []string{"clear", "rain", "thunder"}
	default:
		return nil, fmt.Errorf("unsupported test environment%s", kind)
	}
	value, ok := in[key].(string)
	if ok {
		for _, choice := range choices {
			if value == choice {
				return map[string]any{"type": kind, key: value}, nil
			}
		}
	}
	return nil, fmt.Errorf("invalid environment%s", key)
}

func (s *Server) testEnvironmentDefinition(name string) map[string]any {
	if s.testPack != nil {
		return s.testPack.environments[name]
	}
	return capturedTestEnvironments()[name]
}

// An activation owns the values it captured, not the editor/controller record.
// Composition restores the most recently applied child first.
func (s *Server) activateTestEnvironment(d *dimension, definition map[string]any) func() {
	kind, _ := definition["type"].(string)
	switch kind {
	case "minecraft:all_of":
		list, _ := definition["definitions"].(nbt.List)
		restores := make([]func(), 0, len(list.Items))
		for _, value := range list.Items {
			child, _ := value.(map[string]any)
			restores = append(restores, s.activateTestEnvironment(d, child))
		}
		return func() {
			for i := len(restores) - 1; i >= 0; i-- {
				restores[i]()
			}
		}
	case "minecraft:difficulty":
		s.rulesMu.RLock()
		previous := s.difficulty
		s.rulesMu.RUnlock()
		requested, _ := definition["difficulty"].(string)
		for value, name := range []string{"peaceful", "easy", "normal", "hard"} {
			if name == requested {
				s.setDifficulty(byte(value))
				break
			}
		}
		return func() { s.setDifficulty(previous) }
	case "minecraft:weather":
		previous := "clear"
		if d.name == dimOverworld {
			if s.isThundering() {
				previous = "thunder"
			} else if s.isRaining() {
				previous = "rain"
			}
		}
		requested, _ := definition["weather"].(string)
		s.applyTestEnvironmentWeather(requested)
		return func() { s.applyTestEnvironmentWeather(previous) }
	}
	return func() {}
}
func (s *Server) applyTestEnvironmentWeather(mode string) {
	s.weatherRaining.Store(mode == "rain" || mode == "thunder")
	s.weatherThunder.Store(mode == "thunder")
	s.weatherTicks.Store(100000)
}
