package server

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (s *Server) testInstanceDefinition(name string) map[string]any {
	if s.testPack != nil {
		return s.testPack.tests[name]
	}
	return testInstanceDefinition(name)
}
func (s *Server) gameTestConfigBody(reg configRegistry) ([]byte, error) {
	if s.testPack == nil {
		return reg.body, nil
	}
	switch reg.key {
	case "minecraft:test_instance":
		return gameTestRegistryOverlay(reg, s.testPack.testOverrides)
	case "minecraft:test_environment":
		return gameTestRegistryOverlay(reg, s.testPack.environmentOverrides)
	}
	return reg.body, nil
}

func (s *Server) loadTestInstancePacks() error {
	s.testPack = nil
	pack := &testInstancePack{tests: map[string]map[string]any{}, environments: map[string]map[string]any{}, testOverrides: map[string]map[string]any{}, environmentOverrides: map[string]map[string]any{}}
	for id, value := range capturedTestInstances() {
		pack.tests[id] = value
	}
	for id, value := range capturedTestEnvironments() {
		pack.environments[id] = value
	}
	for _, root := range folderPackRoots(s.WorldDir) {
		dataRoot := filepath.Join(root, "data")
		err := filepath.WalkDir(dataRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				return nil
			}
			rel, err := filepath.Rel(dataRoot, path)
			if err != nil {
				return err
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) < 3 || (parts[1] != "test_instance" && parts[1] != "test_environment") {
				return nil
			}
			id := parts[0] + ":" + strings.TrimSuffix(strings.Join(parts[2:], "/"), ".json")
			if canonical, ok := jigsawIdentifier(id); !ok || canonical != id {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value, err := decodeGameTestJSON(raw)
			if err != nil {
				return fmt.Errorf("test resource%s: %w", id, err)
			}
			var parsed map[string]any
			if parts[1] == "test_instance" {
				parsed, err = parseGameTestDefinition(value)
			} else {
				parsed, err = parseGameTestEnvironment(value)
			}
			if err != nil {
				return fmt.Errorf("test resource%s: %w", id, err)
			}
			if parts[1] == "test_instance" {
				pack.tests[id] = parsed
				pack.testOverrides[id] = parsed
			} else {
				pack.environments[id] = parsed
				pack.environmentOverrides[id] = parsed
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(pack.testOverrides) == 0 && len(pack.environmentOverrides) == 0 {
		return nil
	}
	for id, value := range pack.tests {
		environment, _ := value["environment"].(string)
		if pack.environments[environment] == nil {
			return fmt.Errorf("test%s references missing environment%s", id, environment)
		}
	}
	s.testPack = pack
	return nil
}
