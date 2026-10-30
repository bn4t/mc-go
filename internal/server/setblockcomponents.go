package server

import (
	"sort"
	"strings"
)

// setblockItemComponents reuses the modeled component decoder used by item
// commands. Unknown component models retain the loader's existing fallback;
// they do not prevent an independently modeled component from being restored.
func setblockItemComponents(base ItemStack, components map[string]any) ItemStack {
	keys := make([]string, 0, len(components))
	for key := range components {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		part := key
		if !strings.HasPrefix(key, "!") {
			part += "=" + componentSNBT(components[key])
		}
		if _, _, ok := parseItemPatch(part, 0); ok {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return base
	}
	patch, _, ok := parseItemPatch(strings.Join(parts, ","), 0)
	if !ok {
		return base
	}
	patch.ID, patch.Count = base.ID, base.Count
	return patch
}
