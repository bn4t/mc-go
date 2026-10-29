package server

import (
	"fmt"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// readCanonicalSlot converts only the fields represented by ItemStack. The
// preceding shape projector has already bounded all data. Unsupported semantic
// payloads reject instead of silently losing custom data during creative ingress.
func readCanonicalSlot(r *protocol.PacketReader, depth int) (ItemStack, error) {
	count, err := slotWireCount(r, 127)
	if err != nil || count == 0 {
		return ItemStack{}, err
	}
	id, err := r.VarInt()
	if err != nil {
		return ItemStack{}, err
	}
	return readCanonicalStackTail(r, ItemStack{ID: id, Count: count}, depth)
}
func readCanonicalStackTail(r *protocol.PacketReader, it ItemStack, depth int) (ItemStack, error) {
	if depth > 16 {
		return ItemStack{}, fmt.Errorf("slot: semantic nesting exceeded")
	}
	added, err := slotWireCount(r, 256)
	if err != nil {
		return ItemStack{}, err
	}
	removed, err := slotWireCount(r, 256)
	if err != nil {
		return ItemStack{}, err
	}
	var itemName, tooltip, rarity bool
	for i := int32(0); i < added; i++ {
		typ, err := r.VarInt()
		if err != nil {
			return ItemStack{}, err
		}
		switch typ {
		case compInstrument:
			id, err := r.VarInt()
			if err != nil || id <= 0 {
				return ItemStack{}, fmt.Errorf("slot: inline instrument unsupported")
			}
			name := strings.TrimPrefix(registryEntryName("minecraft:instrument", id-1), "minecraft:")
			if _, ok := instrumentIDs[name]; !ok {
				return ItemStack{}, fmt.Errorf("slot: unmodeled instrument %q", name)
			}
			it.Instrument = name
		case compPotDeco:
			n, err := slotWireCount(r, 4)
			if err != nil || n != 4 {
				return ItemStack{}, fmt.Errorf("slot: partial pot decorations are outside the current four-side model")
			}
			parts := make([]string, 0, 4)
			for range 4 {
				id, err := r.VarInt()
				if err != nil {
					return ItemStack{}, err
				}
				name, ok := itemNameOf(id)
				if !ok {
					return ItemStack{}, fmt.Errorf("slot: unknown pot decoration")
				}
				parts = append(parts, name)
			}
			it.PotDeco = strings.Join(parts, ",")
		case compPotionContents:
			present, err := slotBool(r)
			if err != nil {
				return ItemStack{}, err
			}
			if present {
				id, err := r.VarInt()
				if err != nil {
					return ItemStack{}, err
				}
				name, ok := potionNamesByID[id]
				if !ok {
					return ItemStack{}, fmt.Errorf("slot: unknown potion")
				}
				it.Potion = name
			}
			color, err := slotBool(r)
			if err != nil || color {
				return ItemStack{}, fmt.Errorf("slot: custom potion color unmodeled")
			}
			effects, err := slotWireCount(r, 256)
			if err != nil || effects != 0 {
				return ItemStack{}, fmt.Errorf("slot: custom potion effects unmodeled")
			}
			customName, err := slotBool(r)
			if err != nil || customName {
				return ItemStack{}, fmt.Errorf("slot: custom potion name unmodeled")
			}
		case compBees:
			n, err := slotWireCount(r, 1024)
			if err != nil {
				return ItemStack{}, err
			}
			beeID, _ := EntityTypeID("bee")
			for j := int32(0); j < n; j++ {
				holder, err := r.VarInt()
				if err != nil || holder != beeID {
					return ItemStack{}, fmt.Errorf("slot: hive occupant type unmodeled")
				}
				value, err := (&slotProjector{}).readNBT(r, false, 0)
				if err != nil {
					return ItemStack{}, err
				}
				compound, ok := value.(map[string]any)
				if !ok || len(compound) != 0 {
					return ItemStack{}, fmt.Errorf("slot: custom hive entity data unmodeled")
				}
				ticks, err := r.VarInt()
				if err != nil {
					return ItemStack{}, err
				}
				minimum, err := r.VarInt()
				if err != nil {
					return ItemStack{}, err
				}
				if ticks != 0 || minimum != 600 {
					return ItemStack{}, fmt.Errorf("slot: custom hive timers unmodeled")
				}
			}
			it.Bees = n
		case compBundleContents:
			var err error
			it.Bundle, err = readBundleContents(r, depth)
			if err != nil {
				return ItemStack{}, err
			}
		case compContainer:
			n, err := slotWireCount(r, 27)
			if err != nil {
				return ItemStack{}, err
			}
			box := new([shulkerSlots]ItemStack)
			for j := int32(0); j < n; j++ {
				present, err := slotBool(r)
				if err != nil {
					return ItemStack{}, err
				}
				if !present {
					continue
				}
				id, err := r.VarInt()
				if err != nil {
					return ItemStack{}, err
				}
				count, err := slotWireCount(r, 127)
				if err != nil || count == 0 {
					return ItemStack{}, fmt.Errorf("slot: invalid contained count")
				}
				inner, err := readCanonicalStackTail(r, ItemStack{ID: id, Count: count}, depth+1)
				if err != nil {
					return ItemStack{}, err
				}
				box[j] = inner
			}
			it.Contents = box
		case compBannerPatterns:
			n, err := slotWireCount(r, 1024)
			if err != nil {
				return ItemStack{}, err
			}
			parts := make([]string, 0, n)
			for j := int32(0); j < n; j++ {
				holder, err := r.VarInt()
				if err != nil || holder <= 0 {
					return ItemStack{}, fmt.Errorf("slot: inline banner pattern unmodeled")
				}
				name := strings.TrimPrefix(registryEntryName("minecraft:banner_pattern", holder-1), "minecraft:")
				if name == "" {
					return ItemStack{}, fmt.Errorf("slot: unknown banner pattern")
				}
				color, err := slotWireCount(r, 15)
				if err != nil {
					return ItemStack{}, err
				}
				parts = append(parts, name+":"+loomDyeNames[color])
			}
			it.BannerPatterns = strings.Join(parts, ",")
		case compTooltipDisplay:
			hidden, err := slotBool(r)
			if err != nil || hidden {
				return ItemStack{}, fmt.Errorf("slot: custom tooltip visibility unmodeled")
			}
			n, err := slotWireCount(r, 256)
			if err != nil || n != 1 {
				return ItemStack{}, fmt.Errorf("slot: custom tooltip hidden set unmodeled")
			}
			typ, err := r.VarInt()
			if err != nil || typ != compBannerPatterns {
				return ItemStack{}, fmt.Errorf("slot: custom tooltip component unmodeled")
			}
			tooltip = true
		case compItemName:
			value, err := (&slotProjector{}).readNBT(r, false, 0)
			if err != nil {
				return ItemStack{}, err
			}
			compound, ok := value.(map[string]any)
			if !ok || len(compound) != 1 || compound["translate"] != "block.minecraft.ominous_banner" {
				return ItemStack{}, fmt.Errorf("slot: custom item_name unmodeled")
			}
			itemName = true
		case compRarity:
			value, err := r.VarInt()
			if err != nil || value != rarityUncommon {
				return ItemStack{}, fmt.Errorf("slot: custom rarity unmodeled")
			}
			rarity = true
		case compWritableBook, compWrittenBook:
			value, err := readCanonicalBook(r, typ == compWrittenBook)
			if err != nil {
				return ItemStack{}, err
			}
			if typ == compWrittenBook {
				it.BookWritten = snbtCompact(value)
			} else {
				it.BookWritable = snbtCompact(value)
			}
		case compBaseColor:
			value, err := slotWireCount(r, 15)
			if err != nil {
				return ItemStack{}, err
			}
			it.BaseColor = value + 1
		case compOminousBottleAmplifier:
			value, err := slotWireCount(r, 4)
			if err != nil {
				return ItemStack{}, err
			}
			it.BottleAmpSet = true
			it.BottleAmp = value
		case compMapID:
			value, err := r.VarInt()
			if err != nil || value < 0 || value == 1<<31-1 {
				return ItemStack{}, fmt.Errorf("slot: invalid map ID")
			}
			it.MapID = value + 1
		case compMapPost:
			value, err := slotWireCount(r, 1)
			if err != nil {
				return ItemStack{}, err
			}
			it.MapPost = value + 1
		case compDye:
			value, err := slotWireCount(r, 15)
			if err != nil {
				return ItemStack{}, err
			}
			it.Dye = value + 1
		case compDyedColor:
			value, err := r.Int()
			if err != nil || value == 0 {
				return ItemStack{}, fmt.Errorf("slot: zero dyed color is unmodeled")
			}
			it.DyedColor = value
		default:
			if err := readSlotComponent(r, &it, typ); err != nil {
				return ItemStack{}, err
			}
		}
	}
	for i := int32(0); i < removed; i++ {
		typ, err := r.VarInt()
		if err != nil {
			return ItemStack{}, err
		}
		if typ != compInstrument {
			return ItemStack{}, fmt.Errorf("slot: removal of component%d is unmodeled", typ)
		}
		it.InstrumentRemoved = true
	}
	if itemName || tooltip || rarity {
		if !itemName || !tooltip || !rarity {
			return ItemStack{}, fmt.Errorf("slot: incomplete ominous banner model")
		}
		want := make([]string, 0, len(ominousBannerLayers))
		for _, layer := range ominousBannerLayers {
			pattern := strings.TrimPrefix(registryEntryName("minecraft:banner_pattern", layer.pattern), "minecraft:")
			want = append(want, pattern+":"+loomDyeNames[layer.color])
		}
		if it.BannerPatterns != strings.Join(want, ",") {
			return ItemStack{}, fmt.Errorf("slot: custom ominous banner patterns unmodeled")
		}
		it.Ominous = true
		it.BannerPatterns = ""
	}
	return it, nil
}
func readCanonicalBook(r *protocol.PacketReader, written bool) (map[string]any, error) {
	filterText := func() (map[string]any, error) {
		raw, err := r.String()
		if err != nil {
			return nil, err
		}
		out := map[string]any{"raw": raw}
		filtered, err := slotBool(r)
		if err != nil {
			return nil, err
		}
		if filtered {
			value, err := r.String()
			if err != nil {
				return nil, err
			}
			out["filtered"] = value
		}
		return out, nil
	}
	out := map[string]any{}
	if written {
		title, err := filterText()
		if err != nil {
			return nil, err
		}
		author, err := r.String()
		if err != nil {
			return nil, err
		}
		generation, err := r.VarInt()
		if err != nil {
			return nil, err
		}
		out["title"], out["author"], out["generation"] = title, author, generation
	}
	n, err := slotWireCount(r, 100)
	if err != nil {
		return nil, err
	}
	pages := nbt.List{Elem: nbt.TagCompound}
	for i := int32(0); i < n; i++ {
		var page map[string]any
		if written {
			raw, err := nbt.ReadUnnamed(slotNBTReader{r})
			if err != nil {
				return nil, err
			}
			page = map[string]any{"raw": raw}
			filtered, err := slotBool(r)
			if err != nil {
				return nil, err
			}
			if filtered {
				value, err := nbt.ReadUnnamed(slotNBTReader{r})
				if err != nil {
					return nil, err
				}
				page["filtered"] = value
			}
		} else {
			page, err = filterText()
			if err != nil {
				return nil, err
			}
		}
		pages.Items = append(pages.Items, page)
	}
	out["pages"] = pages
	if written {
		resolved, err := slotBool(r)
		if err != nil {
			return nil, err
		}
		value := int8(0)
		if resolved {
			value = 1
		}
		out["resolved"] = value
	}
	return out, nil
}
