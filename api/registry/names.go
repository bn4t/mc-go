package registry

import "fmt"

// NameID is a dense handle local to one immutable NameCatalog. Its value is not
// a wire ID and is not portable across catalogs or saved data. Persist Name.
type NameID int32

// NameCatalog describes a named registry such as items or component types.
// It supplies identities only; adding a name does not implement game behavior.
type NameCatalog struct {
	names []string
	ids   map[string]NameID
}

func NewNameCatalog(names []string) (*NameCatalog, error) {
	if len(names) == 0 || uint64(len(names)) > 1<<31 {
		return nil, fmt.Errorf("registry: invalid named catalog size")
	}
	c := &NameCatalog{names: append([]string(nil), names...), ids: make(map[string]NameID, len(names))}
	for i, name := range c.names {
		if !identifier(name) {
			return nil, fmt.Errorf("registry: invalid namespaced name %q", name)
		}
		if _, exists := c.ids[name]; exists {
			return nil, fmt.Errorf("registry: duplicate name %q", name)
		}
		c.ids[name] = NameID(i)
	}
	return c, nil
}
func (c *NameCatalog) Len() int { return len(c.names) }
func (c *NameCatalog) Name(id NameID) (string, bool) {
	if id < 0 || int64(id) >= int64(len(c.names)) {
		return "", false
	}
	return c.names[id], true
}
func (c *NameCatalog) Lookup(name string) (NameID, bool) { id, ok := c.ids[name]; return id, ok }
