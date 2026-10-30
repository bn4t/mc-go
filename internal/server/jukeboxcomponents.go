package server

import (
	"reflect"
	"strconv"
	"strings"

	"mc-go/internal/nbt"
)

// Native record serialization uses persistent component values. Component
// selection and the map crafting post-process instruction do not persist.
func jukeboxDiscComponents(disc ItemStack) map[string]any {
	base := disc
	base.Contents, base.Bundle = nil, ""
	out := itemComponentsNBT(base)
	if disc.Ominous {
		for key, value := range jukeboxOminousComponents() {
			out[key] = value
		}
	}
	if disc.RepairCost != 0 {
		out["minecraft:repair_cost"] = disc.RepairCost
	}
	if disc.StoredEnch != "" {
		out["minecraft:stored_enchantments"] = itemComponentsNBT(ItemStack{Ench: disc.StoredEnch})["minecraft:enchantments"]
	}
	if disc.MapID != 0 {
		out["minecraft:map_id"] = disc.MapID - 1
	}
	if disc.BannerPatterns != "" {
		list := nbt.List{Elem: nbt.TagCompound}
		for _, layer := range strings.Split(disc.BannerPatterns, ",") {
			pattern, color, ok := strings.Cut(layer, ":")
			if ok {
				list.Items = append(list.Items, map[string]any{"pattern": "minecraft:" + pattern, "color": color})
			}
		}
		out["minecraft:banner_patterns"] = list
	}
	if disc.BaseColor > 0 && disc.BaseColor <= int32(len(loomDyeNames)) {
		out["minecraft:base_color"] = loomDyeNames[disc.BaseColor-1]
	}
	if disc.Dye > 0 && disc.Dye <= int32(len(loomDyeNames)) {
		out["minecraft:dye"] = loomDyeNames[disc.Dye-1]
	}
	if disc.DyedColor != 0 {
		out["minecraft:dyed_color"] = disc.DyedColor
	}
	if disc.BottleAmpSet {
		out["minecraft:ominous_bottle_amplifier"] = disc.BottleAmp
	}
	if disc.StewEffects != "" {
		list := nbt.List{Elem: nbt.TagCompound}
		for _, entry := range strings.Split(disc.StewEffects, ",") {
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
			list.Items = append(list.Items, value)
		}
		out["minecraft:suspicious_stew_effects"] = list
	}
	if disc.PotDeco != "" {
		list := nbt.List{Elem: nbt.TagString}
		for _, name := range strings.Split(disc.PotDeco, ",") {
			list.Items = append(list.Items, "minecraft:"+name)
		}
		out["minecraft:pot_decorations"] = list
	}
	if disc.InstrumentRemoved {
		delete(out, "minecraft:instrument")
		out["!minecraft:instrument"] = map[string]any{}
	}
	if disc.FireworkExp != "" {
		out["minecraft:firework_explosion"] = jukeboxExplosionNBT(disc.FireworkExp)
	}
	if disc.Fireworks != "" {
		parts := strings.SplitN(disc.Fireworks, ";", 2)
		flight, _ := strconv.Atoi(parts[0])
		var explosions []string
		if len(parts) == 2 && parts[1] != "" {
			explosions = strings.Split(parts[1], ";")
		}
		value := map[string]any{}
		if flight != 0 {
			value["flight_duration"] = int8(flight)
		}
		if len(explosions) > 0 {
			list := nbt.List{Elem: nbt.TagCompound}
			for _, exp := range explosions {
				list.Items = append(list.Items, jukeboxExplosionNBT(exp))
			}
			value["explosions"] = list
		}
		out["minecraft:fireworks"] = value
	}
	if disc.Contents != nil {
		list := nbt.List{Elem: nbt.TagCompound}
		for slot, child := range disc.Contents {
			if child.Empty() {
				continue
			}
			value := jukeboxDiscNBT(child)
			if child.Count == 1 {
				delete(value, "count")
			}
			list.Items = append(list.Items, map[string]any{"slot": int32(slot), "item": value})
		}
		if len(list.Items) == 0 {
			list = nbt.List{Items: []any{}}
		}
		out["minecraft:container"] = list
	}
	if disc.Bundle != "" {
		list := nbt.List{Elem: nbt.TagCompound}
		for _, child := range bundleItems(disc) {
			list.Items = append(list.Items, jukeboxDiscNBT(child))
		}
		out["minecraft:bundle_contents"] = list
	}
	return out
}

func jukeboxExplosionNBT(explosion string) map[string]any {
	shape, colors, fades, trail, twinkle := fireworkExpParse(explosion)
	out := map[string]any{"shape": shape}
	for key, values := range map[string][]int32{"colors": colors, "fade_colors": fades} {
		if len(values) > 0 {
			list := nbt.List{Elem: nbt.TagInt}
			for _, value := range values {
				list.Items = append(list.Items, value)
			}
			out[key] = list
		}
	}
	if trail {
		out["has_trail"] = int8(1)
	}
	if twinkle {
		out["has_twinkle"] = int8(1)
	}
	return out
}

func jukeboxDiscNBT(disc ItemStack) map[string]any {
	fields := itemNBT(ItemStack{ID: disc.ID, Count: disc.Count})
	if components := jukeboxDiscComponents(disc); len(components) != 0 {
		fields["components"] = components
	} else {
		delete(fields, "components")
	}
	return fields
}

// Nested record components use the same modeled item values as the root.
func jukeboxNestedComponents(disc ItemStack, components map[string]any) ItemStack {
	if raw, ok := components["minecraft:container"].(nbt.List); ok {
		disc.Contents = new([shulkerSlots]ItemStack)
		for _, value := range raw.Items {
			entry, ok := value.(map[string]any)
			if !ok {
				continue
			}
			slot := nbtNumberOr(entry["slot"], -1)
			item, ok := entry["item"].(map[string]any)
			if ok && slot >= 0 && slot < shulkerSlots {
				disc.Contents[slot] = jukeboxDiscFromNBT(item)
			}
		}
	}
	if raw, ok := components["minecraft:bundle_contents"].(nbt.List); ok {
		items := make([]ItemStack, 0, len(raw.Items))
		for _, value := range raw.Items {
			if item, ok := value.(map[string]any); ok {
				items = append(items, jukeboxDiscFromNBT(item))
			}
		}
		bundleSetItems(&disc, items)
	}
	return disc
}

// This aggregate is the already-modeled ominous banner's four-component patch.
func jukeboxOminousComponents() map[string]any {
	patterns := nbt.List{Elem: nbt.TagCompound}
	for _, layer := range ominousBannerLayers {
		patterns.Items = append(patterns.Items, map[string]any{"pattern": registryEntryName("minecraft:banner_pattern", layer.pattern), "color": loomDyeNames[layer.color]})
	}
	return map[string]any{
		"minecraft:banner_patterns": patterns,
		"minecraft:tooltip_display": map[string]any{"hidden_components": nbt.List{Elem: nbt.TagString, Items: []any{"minecraft:banner_patterns"}}},
		"minecraft:item_name":       map[string]any{"translate": "block.minecraft.ominous_banner"},
		"minecraft:rarity":          "uncommon",
	}
}
func jukeboxIsOminous(components map[string]any) bool {
	for key, want := range jukeboxOminousComponents() {
		got := components[key]
		if key == "minecraft:tooltip_display" {
			value, ok := got.(map[string]any)
			if !ok || len(value) > 2 {
				return false
			}
			if hide, present := value["hide_tooltip"]; present {
				n, ok := nbtNumber(hide)
				if !ok || n != 0 {
					return false
				}
			}
			got = map[string]any{"hidden_components": value["hidden_components"]}
		}
		if !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}
