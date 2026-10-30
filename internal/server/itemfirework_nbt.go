package server

import (
	"mc-go/internal/nbt"
	"strconv"
	"strings"
)

func itemFireworkComponentsNBT(components map[string]any, it ItemStack) {
	if it.FireworkExp != "" {
		components["minecraft:firework_explosion"] = jukeboxExplosionNBT(it.FireworkExp)
	}
	if it.Fireworks != "" {
		parts := strings.SplitN(it.Fireworks, ";", 2)
		flight, _ := strconv.Atoi(parts[0])
		value := map[string]any{}
		if flight != 0 {
			value["flight_duration"] = int8(flight)
		}
		if len(parts) == 2 && parts[1] != "" {
			explosions := nbt.List{Elem: nbt.TagCompound}
			for _, exp := range strings.Split(parts[1], ";") {
				explosions.Items = append(explosions.Items, jukeboxExplosionNBT(exp))
			}
			value["explosions"] = explosions
		}
		components["minecraft:fireworks"] = value
	}
}
