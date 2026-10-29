package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Startup folder functions execute at the default function permission level.
// Macros, continuations and full argument-tree compilation remain unmodeled.
const functionPermLevel = 2

func (s *Server) loadFunctions() {
	s.functions = map[string][]string{}
	known := map[string]bool{}
	for _, name := range s.helpNames() {
		known[name] = true
	}
	for _, pack := range folderPackRoots(s.WorldDir) {
		dataRoot := filepath.Join(pack, "data")
		_ = filepath.WalkDir(dataRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".mcfunction") {
				return nil
			}
			rel, err := filepath.Rel(dataRoot, path)
			if err != nil {
				return nil
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) < 3 || parts[1] != "function" {
				return nil
			}
			id, ok := jigsawIdentifier(parts[0] + ":" + strings.TrimSuffix(strings.Join(parts[2:], "/"), ".mcfunction"))
			if !ok {
				return nil
			}
			// A selected higher-priority file replaces the prior function even if
			// that file cannot compile; function resources do not merge like tags.
			delete(s.functions, id)
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			var lines []string
			continued := false
			for _, line := range strings.Split(string(raw), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				unmodeled := continued || strings.HasPrefix(line, "$")
				continued = strings.HasSuffix(line, "\\")
				if !unmodeled {
					fields := splitCommandArgs(line)
					if strings.HasPrefix(line, "/") || len(fields) == 0 || !known[fields[0]] || commandLevel(fields[0]) > functionPermLevel || len(fields) == 1 && commandNeedsArgs[fields[0]] {
						return nil
					}
				}
				lines = append(lines, line)
			}
			s.functions[id] = lines
			return nil
		})
	}
}
