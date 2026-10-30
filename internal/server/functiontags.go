package server

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type functionTagEntry struct {
	id            string
	tag, required bool
}

func parseFunctionTagFile(raw []byte) ([]functionTagEntry, bool, bool) {
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return nil, false, false
	}
	values, ok := object["values"].([]any)
	if !ok {
		return nil, false, false
	}
	replace := false
	if v := object["replace"]; v != nil {
		var ok bool
		replace, ok = v.(bool)
		if !ok {
			return nil, false, false
		}
	}
	entries := make([]functionTagEntry, 0, len(values))
	for _, value := range values {
		required := true
		var name string
		switch v := value.(type) {
		case string:
			name = v
		case map[string]any:
			var ok bool
			name, ok = v["id"].(string)
			if !ok {
				return nil, false, false
			}
			if r := v["required"]; r != nil {
				required, ok = r.(bool)
				if !ok {
					return nil, false, false
				}
			}
		default:
			return nil, false, false
		}
		tag := strings.HasPrefix(name, "#")
		if tag {
			name = name[1:]
		}
		id, ok := jigsawIdentifier(name)
		if !ok {
			return nil, false, false
		}
		entries = append(entries, functionTagEntry{id, tag, required})
	}
	return entries, replace, true
}

// Resolve hard dependencies first. Optional dependencies can order a build
// only when their edge does not close a cycle in the dependency graph.
func resolveFunctionTags(definitions map[string][]functionTagEntry, functions map[string][]string) map[string][]string {
	ids := make([]string, 0, len(definitions))
	for id := range definitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	dependencies := map[string][]string{}
	add := func(id, dependency string) {
		for _, old := range dependencies[id] {
			if old == dependency {
				return
			}
		}
		dependencies[id] = append(dependencies[id], dependency)
	}
	for _, id := range ids {
		for _, entry := range definitions[id] {
			if entry.tag && entry.required {
				if _, ok := definitions[entry.id]; ok {
					add(id, entry.id)
				}
			}
		}
	}
	reaches := func(from, to string) bool {
		seen := map[string]bool{}
		var visit func(string) bool
		visit = func(id string) bool {
			if id == to {
				return true
			}
			if seen[id] {
				return false
			}
			seen[id] = true
			for _, dependency := range dependencies[id] {
				if visit(dependency) {
					return true
				}
			}
			return false
		}
		return visit(from)
	}
	for _, id := range ids {
		for _, entry := range definitions[id] {
			if entry.tag && !entry.required {
				if _, ok := definitions[entry.id]; ok && !reaches(entry.id, id) {
					add(id, entry.id)
				}
			}
		}
	}
	var order []string
	state := map[string]byte{}
	var visit func(string)
	visit = func(id string) {
		if state[id] != 0 {
			return
		}
		state[id] = 1
		for _, dependency := range dependencies[id] {
			visit(dependency)
		}
		state[id] = 2
		order = append(order, id)
	}
	for _, id := range ids {
		visit(id)
	}
	result := map[string][]string{}
	for _, id := range order {
		var names []string
		seen := map[string]bool{}
		valid := true
		for _, entry := range definitions[id] {
			var refs []string
			exists := false
			if entry.tag {
				refs, exists = result[entry.id]
			} else {
				_, exists = functions[entry.id]
				if exists {
					refs = []string{entry.id}
				}
			}
			if !exists {
				if entry.required {
					valid = false
					break
				}
				continue
			}
			for _, name := range refs {
				if !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
		if valid {
			result[id] = names
		}
	}
	return result
}

func (s *Server) loadFunctionTags() {
	definitions := map[string][]functionTagEntry{}
	for _, pack := range folderPackRoots(s.WorldDir) {
		dataRoot := filepath.Join(pack, "data")
		_ = filepath.WalkDir(dataRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
				return nil
			}
			rel, err := filepath.Rel(dataRoot, path)
			if err != nil {
				return nil
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) < 4 || parts[1] != "tags" || parts[2] != "function" {
				return nil
			}
			id, ok := jigsawIdentifier(parts[0] + ":" + strings.TrimSuffix(strings.Join(parts[3:], "/"), ".json"))
			if !ok {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			entries, replace, ok := parseFunctionTagFile(raw)
			if !ok {
				return nil
			}
			if replace {
				definitions[id] = append([]functionTagEntry(nil), entries...)
			} else {
				definitions[id] = append(definitions[id], entries...)
			}
			return nil
		})
	}
	s.functionTags = resolveFunctionTags(definitions, s.functions)
	s.functionTagLoadPending = true
}

func (s *Server) functionTagsStep() {
	if s.functionTagLoadPending {
		s.functionTagLoadPending = false
		s.runServerFunctionList(s.functionTags["minecraft:load"])
	}
	s.runServerFunctionList(s.functionTags["minecraft:tick"])
}

// Nonempty function calls without /return produce feedback but do not invoke
// the enclosing execute-store result consumer.
func functionCommandReply(c component) reply {
	r := opsEchoReply(c)
	r.outcome = commandOutcomeUnknown
	return r
}

func (s *Server) runFunctionTag(player, id string) reply {
	names := s.functionTags[id]
	s.executeFunctionNames(player, names, true)
	switch len(names) {
	case 0:
		r := componentReply(commandError("commands.function.scheduled.no_functions", id))
		r.hasResult = true
		r.outcome = commandFailed
		return r
	case 1:
		return functionCommandReply(translatable("commands.function.scheduled.single", names[0]))
	}
	joined := component{text: ""}
	for i, name := range names {
		if i != 0 {
			joined.extra = append(joined.extra, component{color: "gray", text: ", "})
		}
		joined.extra = append(joined.extra, component{emptyKey: name})
	}
	return functionCommandReply(translatable("commands.function.scheduled.multiple", joined))
}

func (s *Server) runServerFunctionList(names []string) {
	if len(names) == 0 {
		return
	}
	sx, sy, sz := s.WorldSpawn()
	frame := commandSourceFrame{dim: s.dims[dimOverworld], pos: [3]float64{float64(floor(sx)), float64(floor(sy)), float64(floor(sz))}, display: literal("Server"), level: functionPermLevel}
	s.withCommandFrame(&frame, func() reply {
		for _, name := range names {
			s.executeFunctionNames(neutralCommandActor, []string{name}, false)
		}
		return reply{}
	})
}
