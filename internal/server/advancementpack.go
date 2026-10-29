package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"mc-go/internal/data"
)

// Each server owns its loaded pack definitions; built-in generated metadata
// remains immutable and is also the fallback for small unit-test servers.
type advancementRegistry struct {
	entries  map[string]*advMeta
	children map[string][]string
	roots    []string
	custom   map[string]bool
	wire     map[string][]byte
	attached map[string]bool
}

func (s *Server) gameAdvTable() map[string]*advMeta {
	if s != nil && s.advancementPack != nil {
		return s.advancementPack.entries
	}
	return gameAdvs
}
func (s *Server) gameAdvChildren() map[string][]string {
	if s != nil && s.advancementPack != nil {
		return s.advancementPack.children
	}
	return gameAdvChild
}
func (s *Server) gameAdvRoots() []string {
	if s != nil && s.advancementPack != nil {
		return s.advancementPack.roots
	}
	return gameAdvRoots
}
func (s *Server) customAdvancement(id string) bool {
	return s != nil && s.advancementPack != nil && s.advancementPack.custom[id]
}

func (s *Server) loadAdvancementPacks() {
	if s.WorldDir == "" {
		s.advancementPack = nil
		return
	}
	reg := &advancementRegistry{entries: map[string]*advMeta{}, children: map[string][]string{}, custom: map[string]bool{}, wire: map[string][]byte{}, attached: map[string]bool{}}
	for id, m := range gameAdvs {
		reg.entries[id] = m
	}
	packs, _ := os.ReadDir(filepath.Join(s.WorldDir, "datapacks"))
	for _, pack := range packs {
		if !pack.IsDir() {
			continue
		}
		root := filepath.Join(s.WorldDir, "datapacks", pack.Name())
		if _, err := os.Stat(filepath.Join(root, "pack.mcmeta")); err != nil {
			continue
		}
		dataRoot := filepath.Join(root, "data")
		_ = filepath.WalkDir(dataRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				return nil
			}
			rel, err := filepath.Rel(dataRoot, path)
			if err != nil {
				return nil
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) < 3 || parts[1] != "advancement" {
				return nil
			}
			rawBytes, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			raw, decodeErr := decodeAdvancementJSON(rawBytes)
			if decodeErr != nil || !validPackAdvancement(raw) {
				return nil
			}
			var display *advancementDisplay
			if value, hasDisplay := raw["display"]; hasDisplay && value != nil {
				var err error
				display, err = parseAdvancementDisplay(value)
				if err != nil {
					return nil
				}
			}

			id := parts[0] + ":" + strings.TrimSuffix(strings.Join(parts[2:], "/"), ".json")
			parsed := parseGameAdvs(map[string]map[string]any{id: raw})[id]
			if parsed == nil || len(parsed.criteria) == 0 {
				return nil
			}
			parsed.display = display
			reg.entries[id] = parsed
			reg.custom[id] = true
			return nil
		})
	}
	// Resource definitions survive even when their parent is absent or cyclic.
	// Only nodes whose parent chain reaches a root join the visibility tree.
	for id, m := range reg.entries {
		attached := false
		seen := map[string]bool{}
		for at := id; ; {
			if at == "" {
				attached = true
				break
			}
			if seen[at] {
				break
			}
			seen[at] = true
			ancestor := reg.entries[at]
			if ancestor == nil {
				break
			}
			at = ancestor.parent
		}
		reg.attached[id] = attached
		if attached {
			if m.parent == "" {
				reg.roots = append(reg.roots, id)
			} else {
				reg.children[m.parent] = append(reg.children[m.parent], id)
			}
		}

	}
	finishAdvancementDisplayWires(reg)

	s.advancementPack = reg
}

// Criterion types come from the untouched built-in pack, plus the four
// registered vanilla triggers not used by that pack (CriteriaTriggers).
var packAdvancementTriggers = func() map[string]bool {
	out := map[string]bool{}
	for _, raw := range data.Advancements() {
		criteria, _ := raw["criteria"].(map[string]any)
		for _, value := range criteria {
			criterion, _ := value.(map[string]any)
			if trigger, ok := criterion["trigger"].(string); ok {
				out[normalizeID(trigger)] = true
			}
		}
	}
	for _, trigger := range []string{"any_block_use", "default_block_use", "used_ender_eye", "voluntary_exile"} {
		out["minecraft:"+trigger] = true
	}
	return out
}()

func validPackAdvancement(raw map[string]any) bool {
	criteria, ok := raw["criteria"].(map[string]any)
	if !ok || len(criteria) == 0 {
		return false
	}
	for _, value := range criteria {
		criterion, ok := value.(map[string]any)
		if !ok {
			return false
		}
		trigger, ok := criterion["trigger"].(string)
		if !ok || !packAdvancementTriggers[normalizeID(trigger)] {
			return false
		}
	}
	if requirements, defined := raw["requirements"]; defined && requirements != nil {
		groups, ok := requirements.([]any)
		if !ok {
			return false
		}
		mentioned := map[string]bool{}
		for _, group := range groups {
			names, ok := group.([]any)
			if !ok {
				return false
			}
			for _, value := range names {
				name, ok := value.(string)
				if !ok {
					return false
				}
				if _, exists := criteria[name]; !exists {
					return false
				}
				mentioned[name] = true
			}
		}
		if len(mentioned) != len(criteria) {
			return false
		}
	}
	if parent, exists := raw["parent"]; exists && parent != nil {
		if _, ok := parent.(string); !ok {
			return false
		}
	}
	if rewards, exists := raw["rewards"]; exists && rewards != nil {
		reward, ok := rewards.(map[string]any)
		if !ok {
			return false
		}
		if value, exists := reward["experience"]; exists {
			_, err := advancementJSONInt(value)
			if value != nil && err != nil {
				return false
			}
		}
		for _, field := range []string{"loot", "recipes"} {
			if value, exists := reward[field]; exists && value != nil {
				entries, ok := value.([]any)
				if !ok {
					return false
				}
				for _, entry := range entries {
					if _, ok := entry.(string); !ok {
						return false
					}
				}
			}
		}
		if value, exists := reward["function"]; exists && value != nil {
			if _, ok := value.(string); !ok {
				return false
			}
		}
	}
	return true
}
