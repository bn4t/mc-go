package server

import (
	"mc-go/internal/nbt"
	"strings"
)

func itemPaletteComponentsNBT(components map[string]any, it ItemStack) {
	if it.BaseColor > 0 && it.BaseColor <= int32(len(loomDyeNames)) {
		components["minecraft:base_color"] = loomDyeNames[it.BaseColor-1]
	}
	if it.Dye > 0 && it.Dye <= int32(len(loomDyeNames)) {
		components["minecraft:dye"] = loomDyeNames[it.Dye-1]
	}
	if it.BannerPatterns != "" {
		layers := nbt.List{Elem: nbt.TagCompound}
		for _, entry := range strings.Split(it.BannerPatterns, ",") {
			pattern, color, ok := strings.Cut(entry, ":")
			if !ok {
				continue
			}
			layers.Items = append(layers.Items, map[string]any{"pattern": "minecraft:" + pattern, "color": color})
		}
		components["minecraft:banner_patterns"] = layers
	}
}
