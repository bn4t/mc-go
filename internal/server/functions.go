package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Datapack functions. Vanilla discovers each enabled pack's
// data/<namespace>/function tree when the world loads and /function
// runs a file's lines as commands, in order, with the caller as the
// execution source and the server's function-permission-level
// (server.properties, default 2) as the command level.

// functionPermLevel is vanilla's default function-permission-level.
const functionPermLevel = 2

// loadFunctions scans WorldDir's datapacks for .mcfunction files. A
// pack contributes only when it carries a pack.mcmeta — vanilla's
// folder pack source skips directories without one.
// data/<ns>/function/<path>.mcfunction registers as <ns>:<path>, the
// path's subdirectories becoming name segments. Blank lines and lines
// opening with '#' drop; each remaining line is a command without its
// leading slash.
func (s *Server) loadFunctions() {
	s.functions = map[string][]string{}
	if s.WorldDir == "" {
		return
	}
	packs, err := os.ReadDir(filepath.Join(s.WorldDir, "datapacks"))
	if err != nil {
		return
	}
	for _, pack := range packs {
		if !pack.IsDir() {
			continue
		}
		pdir := filepath.Join(s.WorldDir, "datapacks", pack.Name())
		if _, err := os.Stat(filepath.Join(pdir, "pack.mcmeta")); err != nil {
			continue
		}
		dataRoot := filepath.Join(pdir, "data")
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
			name := strings.Join(parts[2:], "/")
			id := parts[0] + ":" + strings.TrimSuffix(name, ".mcfunction")
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			var lines []string
			for _, ln := range strings.Split(string(raw), "\n") {
				ln = strings.TrimSpace(ln)
				if ln == "" || strings.HasPrefix(ln, "#") {
					continue
				}
				lines = append(lines, strings.TrimPrefix(ln, "/"))
			}
			s.functions[id] = lines
			return nil
		})
	}
}
