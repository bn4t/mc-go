package server

import (
	"fmt"
	"sync"
)

// javaBuiltinDomain is an immutable projection between official named registry
// entries and wire IDs. Payload shapes are deliberately not implied by a table.
type javaBuiltinDomain struct {
	names   []string
	byName  map[string]int32
	from776 []int32
	to776   []int32
}

var javaBuiltinDomains struct {
	sync.Once
	protocols map[int32]map[string]*javaBuiltinDomain
}

func loadJavaBuiltinDomains() {
	javaBuiltinDomains.Do(func() {
		javaBuiltinDomains.protocols = make(map[int32]map[string]*javaBuiltinDomain)
		canonical := javaBuiltinRegistryFacts[776]
		for protocolNumber, registries := range javaBuiltinRegistryFacts {
			domains := make(map[string]*javaBuiltinDomain, len(registries))
			for key, names := range registries {
				d := &javaBuiltinDomain{names: names, byName: make(map[string]int32, len(names)), from776: make([]int32, len(canonical[key])), to776: make([]int32, len(names))}
				for i := range d.from776 {
					d.from776[i] = -1
				}
				for i, name := range names {
					d.byName[name], d.to776[i] = int32(i), -1
				}
				for id, name := range canonical[key] {
					if target, ok := d.byName[name]; ok {
						d.from776[id], d.to776[target] = target, int32(id)
					}
				}
				domains[key] = d
			}
			javaBuiltinDomains.protocols[protocolNumber] = domains
		}
	})
}

func builtinDomain(protocolNumber int32, key string) *javaBuiltinDomain {
	loadJavaBuiltinDomains()
	return javaBuiltinDomains.protocols[protocolNumber][key]
}

// builtinRegistryNames returns a detached copy for catalog bootstrap callers.
func builtinRegistryNames(protocolNumber int32, key string) []string {
	if domain := builtinDomain(protocolNumber, key); domain != nil {
		return append([]string(nil), domain.names...)
	}
	return nil
}

func (p *javaProfile) builtinRegistryID(key, name string) (int32, error) {
	if domain := builtinDomain(p.version.Protocol, key); domain != nil {
		if id, ok := domain.byName[name]; ok {
			return id, nil
		}
	}
	return -1, fmt.Errorf("Java protocol %d has no %s entry %q", p.version.Protocol, key, name)
}

func (p *javaProfile) builtinRegistryName(key string, id int32) string {
	if d := builtinDomain(p.version.Protocol, key); d != nil && id >= 0 && int(id) < len(d.names) {
		return d.names[id]
	}
	return ""
}

func (p *javaProfile) projectBuiltinRegistryID(key string, canonical776ID int32) (int32, error) {
	if d := builtinDomain(p.version.Protocol, key); d != nil && canonical776ID >= 0 && int(canonical776ID) < len(d.from776) && d.from776[canonical776ID] >= 0 {
		return d.from776[canonical776ID], nil
	}
	return -1, fmt.Errorf("Java protocol %d cannot represent %s canonical entry %d", p.version.Protocol, key, canonical776ID)
}

func (p *javaProfile) canonicalBuiltinRegistryID(key string, wireID int32) (int32, error) {
	if d := builtinDomain(p.version.Protocol, key); d != nil && wireID >= 0 && int(wireID) < len(d.to776) && d.to776[wireID] >= 0 {
		return d.to776[wireID], nil
	}
	return -1, fmt.Errorf("Java protocol %d %s wire entry %d has no canonical representation", p.version.Protocol, key, wireID)
}
