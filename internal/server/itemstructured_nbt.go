package server

import (
	"mc-go/internal/nbt"
	"strconv"
	"strings"
)

func itemStructuredComponentsNBT(components map[string]any, it ItemStack) {
	if it.StewEffects != "" {
		effects := nbt.List{Elem: nbt.TagCompound}
		for _, entry := range strings.Split(it.StewEffects, ",") {
			name, duration, ok := strings.Cut(entry, ":")
			if !ok {
				continue
			}
			ticks, err := strconv.Atoi(duration)
			if err != nil {
				continue
			}
			value := map[string]any{"id": "minecraft:" + name}
			if ticks != 160 {
				value["duration"] = int32(ticks)
			}
			effects.Items = append(effects.Items, value)
		}
		components["minecraft:suspicious_stew_effects"] = effects
	}
	if it.PotDeco != "" {
		sides := nbt.List{Elem: nbt.TagString}
		for _, name := range strings.Split(it.PotDeco, ",") {
			sides.Items = append(sides.Items, "minecraft:"+name)
		}
		components["minecraft:pot_decorations"] = sides
	}
}
