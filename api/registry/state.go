// Package registry defines protocol-neutral block identities and immutable
// runtime catalogs. Resolve names at boundaries; simulation and replication use
// dense catalog-local handles. Handles are never protocol numeric identifiers.
package registry

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// BlockStateID is a dense handle valid for the lifetime of one BlockCatalog.
// Persist StateKey instead: another catalog may assign different handles.
type BlockStateID uint32

// StateKey is an immutable, comparable, complete block-state identity. Names are
// namespaced identifiers; properties include defaults and are sorted by name.
// The zero value is invalid. Constructors copy properties from their input.
type StateKey struct{ name, properties string }

func identifier(name string) bool {
	namespace, path, ok := strings.Cut(name, ":")
	if !ok || namespace == "" || path == "" {
		return false
	}
	for i, part := range []string{namespace, path} {
		for _, r := range part {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' || i == 1 && r == '/' {
				continue
			}
			return false
		}
	}
	return true
}
func propertyName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func NewStateKey(name string, properties map[string]string) (StateKey, error) {
	if !identifier(name) {
		return StateKey{}, fmt.Errorf("registry: invalid namespaced block name %q", name)
	}
	names := make([]string, 0, len(properties))
	for key, value := range properties {
		if !propertyName(key) || value == "" || !utf8.ValidString(value) {
			return StateKey{}, fmt.Errorf("registry: invalid property %q=%q", key, value)
		}
		names = append(names, key)
	}
	sort.Strings(names)
	var out strings.Builder
	for i, key := range names {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(key)
		out.WriteByte('=')
		out.WriteString(url.QueryEscape(properties[key]))
	}
	return StateKey{name: name, properties: out.String()}, nil
}

func (s StateKey) Name() string { return s.name }
func (s StateKey) Valid() bool  { return s.name != "" }
func (s StateKey) String() string {
	if s.properties == "" {
		return s.name
	}
	return s.name + "[" + s.properties + "]"
}

// Properties returns a detached copy; changing it cannot change the key.
func (s StateKey) Properties() map[string]string {
	out := make(map[string]string)
	for rest := s.properties; rest != ""; {
		entry, next, _ := strings.Cut(rest, ",")
		rest = next
		key, value, _ := strings.Cut(entry, "=")
		decoded, _ := url.QueryUnescape(value)
		out[key] = decoded
	}
	return out
}

// ParseStateKey accepts a full namespaced identity. Property order is normalized
// and duplicates are rejected. Property values use URL query escaping only when
// necessary, keeping ordinary Minecraft states readable.
func ParseStateKey(text string) (StateKey, error) {
	name, tail, hasProps := strings.Cut(text, "[")
	if !hasProps {
		return NewStateKey(name, nil)
	}
	if !strings.HasSuffix(tail, "]") {
		return StateKey{}, fmt.Errorf("registry: unterminated block properties")
	}
	tail = strings.TrimSuffix(tail, "]")
	if tail == "" {
		return StateKey{}, fmt.Errorf("registry: empty property list")
	}
	props := map[string]string{}
	for _, entry := range strings.Split(tail, ",") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return StateKey{}, fmt.Errorf("registry: invalid property %q", entry)
		}
		if _, exists := props[key]; exists {
			return StateKey{}, fmt.Errorf("registry: duplicate property %q", key)
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			return StateKey{}, fmt.Errorf("registry: invalid property escape: %w", err)
		}
		props[key] = decoded
	}
	return NewStateKey(name, props)
}

func (s StateKey) MarshalText() ([]byte, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("registry: invalid zero state key")
	}
	return []byte(s.String()), nil
}
func (s *StateKey) UnmarshalText(text []byte) error {
	parsed, err := ParseStateKey(string(text))
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}
