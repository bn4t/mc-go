package server

import (
	"mc-go/api/registry"
	"sync"
)

var itemCatalogs struct {
	sync.Once
	items, components *registry.NameCatalog
}

func loadItemCatalogs() {
	itemCatalogs.Do(func() {
		var err error
		itemCatalogs.items, err = registry.NewNameCatalog(builtinRegistryNames(776, "minecraft:item"))
		if err != nil {
			panic(err)
		}
		itemCatalogs.components, err = registry.NewNameCatalog(builtinRegistryNames(776, "minecraft:data_component_type"))
		if err != nil {
			panic(err)
		}
	})
}

// ItemCatalog resolves canonical ItemStack.ID handles. Runtime handles preserve
// builtin776 registration order for existing behavior tables, but every adapter
// must project them. Numeric legacy item JSON persistence is not yet migrated.
func (s *Server) ItemCatalog() *registry.NameCatalog { loadItemCatalogs(); return itemCatalogs.items }
func (s *Server) ComponentCatalog() *registry.NameCatalog {
	loadItemCatalogs()
	return itemCatalogs.components
}
