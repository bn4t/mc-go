package server

// Recipe display encoding for the 26.2 recipe book. Vanilla's
// RecipeBookAdd packet carries a full RecipeDisplayEntry per unlocked
// recipe - the recipe display id, the display structure the client
// draws, a recipe-book group, a book category, and the crafting
// requirements - and this file encodes all of it from data.Recipes so
// the bytes match the real server.

import (
	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Slot display type ids, from the minecraft:slot_display registry.
const (
	sdEmpty             = 0
	sdAnyFuel           = 1
	sdWithAnyPotion     = 2
	sdOnlyWithComponent = 3
	sdItem              = 4
	sdItemStack         = 5
	sdTag               = 6
	sdDyed              = 7
	sdSmithingTrim      = 8
	sdWithRemainder     = 9
	sdComposite         = 10
)

// Recipe display type ids, from the minecraft:recipe_display registry.
const (
	displayShapeless   = 0
	displayShaped      = 1
	displayFurnace     = 2
	displayStonecutter = 3
	displaySmithing    = 4
)

// Recipe book category ids, from minecraft:recipe_book_category.
const (
	bookCatBuilding      = 0
	bookCatRedstone      = 1
	bookCatEquipment     = 2
	bookCatMisc          = 3
	bookCatFurnaceFood   = 4
	bookCatFurnaceBlocks = 5
	bookCatFurnaceMisc   = 6
	bookCatBlastBlocks   = 7
	bookCatBlastMisc     = 8
	bookCatSmokerFood    = 9
	bookCatStonecutter   = 10
	bookCatSmithing      = 11
	bookCatCampfire      = 12
)

// dataComponentDye is the data_component_type id vanilla's
// only_with_component slot shows for dye ingredients.
const dataComponentDye = 43

// dataComponentStewEffects is the data_component_type id of
// suspicious_stew_effects.
const dataComponentStewEffects = 53

// stewEffects records the suspicious_stew_effects each stew recipe's
// result carries, as {effect id in minecraft:mob_effect, duration}.
var stewEffects = map[string][2]int32{
	"suspicious_stew_from_allium":             {11, 60},
	"suspicious_stew_from_azure_bluet":        {14, 220},
	"suspicious_stew_from_blue_orchid":        {22, 7},
	"suspicious_stew_from_closed_eyeblossom":  {8, 140},
	"suspicious_stew_from_cornflower":         {7, 100},
	"suspicious_stew_from_dandelion":          {22, 7},
	"suspicious_stew_from_golden_dandelion":   {22, 7},
	"suspicious_stew_from_lily_of_the_valley": {18, 220},
	"suspicious_stew_from_open_eyeblossom":    {14, 220},
	"suspicious_stew_from_orange_tulip":       {17, 140},
	"suspicious_stew_from_oxeye_daisy":        {9, 140},
	"suspicious_stew_from_pink_tulip":         {17, 140},
	"suspicious_stew_from_poppy":              {15, 100},
	"suspicious_stew_from_red_tulip":          {17, 140},
	"suspicious_stew_from_torchflower":        {15, 100},
	"suspicious_stew_from_white_tulip":        {17, 140},
	"suspicious_stew_from_wither_rose":        {19, 140},
}

// craftRemainder maps an item to the empty container it returns when
// crafted with (vanilla Item.getCraftingRemainder).
var craftRemainder = map[string]string{
	"milk_bucket":          "bucket",
	"water_bucket":         "bucket",
	"lava_bucket":          "bucket",
	"cod_bucket":           "bucket",
	"salmon_bucket":        "bucket",
	"pufferfish_bucket":    "bucket",
	"tropical_fish_bucket": "bucket",
	"axolotl_bucket":       "bucket",
	"tadpole_bucket":       "bucket",
	"powder_snow_bucket":   "bucket",
	"honey_bottle":         "glass_bottle",
	"dragon_breath":        "glass_bottle",
	"splash_potion":        "glass_bottle",
	"lingering_potion":     "glass_bottle",
	"potion":               "glass_bottle",
	"mushroom_stew":        "bowl",
	"rabbit_stew":          "bowl",
	"beetroot_soup":        "bowl",
	"suspicious_stew":      "bowl",
}

// trimPatternIDs are the minecraft:trim_pattern ids in registry order
// (alphabetical, matching the synced registry entries).
var trimPatternIDs = []string{
	"bolt", "coast", "dune", "eye", "flow", "host", "raiser", "rib",
	"sentry", "shaper", "silence", "snout", "spire", "tide", "vex",
	"ward", "wayfinder", "wild",
}

// slotItem writes an item slot display.
func slotItem(b *protocol.Buffer, itemID int32) {
	b.VarInt(sdItem)
	b.VarInt(itemID)
}

// slotStack writes an item_stack display: item id, count, then the
// component patch (total and removed counts, then each added entry as
// a component id and its value).
func slotStack(b *protocol.Buffer, itemID, count int32) {
	b.VarInt(sdItemStack)
	b.VarInt(itemID)
	b.VarInt(count)
	b.VarInt(0)
	b.VarInt(0)
}

// resultStack writes the recipe result's item_stack slot, carrying the
// one component suspicious-stew results show.
func resultStack(b *protocol.Buffer, r data.Recipe, itemID int32) {
	eff, ok := stewEffects[r.ID]
	if !ok {
		slotStack(b, itemID, int32(r.Result.Count))
		return
	}
	b.VarInt(sdItemStack)
	b.VarInt(itemID)
	b.VarInt(int32(r.Result.Count))
	b.VarInt(1)
	b.VarInt(0)
	b.VarInt(dataComponentStewEffects)
	b.VarInt(1)
	b.VarInt(eff[0])
	b.VarInt(eff[1])
}

// slotTag writes a tag slot display; the tag name is the item registry
// tag without '#'. Tags in data carry no namespace, and the wire form
// spells the minecraft namespace out.
func slotTag(b *protocol.Buffer, name string) {
	b.VarInt(sdTag)
	b.String("minecraft:" + name)
}

// ingredientSlot encodes a recipe ingredient's slot display: a named
// tag renders as a tag display, and an item list renders as a composite
// of its options (wrapping each in with_remainder when the item
// returns a container). An empty ingredient is the empty slot.
func ingredientSlot(b *protocol.Buffer, in data.Ingredient) {
	if in.Tag == "" && len(in.Items) == 0 {
		b.VarInt(sdEmpty)
		return
	}
	if in.Tag != "" {
		slotTag(b, in.Tag)
		return
	}
	b.VarInt(sdComposite)
	b.VarInt(int32(len(in.Items)))
	for _, name := range in.Items {
		id, _ := world.ItemID(name)
		writeItemOrRemainder(b, id, name)
	}
}

// writeItemOrRemainder writes an item slot, wrapping it in
// with_remainder when the item has a crafting remainder.
func writeItemOrRemainder(b *protocol.Buffer, itemID int32, name string) {
	if rem, ok := craftRemainder[name]; ok {
		b.VarInt(sdWithRemainder)
		slotItem(b, itemID)
		remID, _ := world.ItemID(rem)
		slotStack(b, remID, 1)
		return
	}
	slotItem(b, itemID)
}

// requirement writes an ingredient as a HolderSet: a named tag is a
// zero marker plus the tag's identifier, and a direct list is the
// entry count + 1 followed by each item id.
func requirement(b *protocol.Buffer, in data.Ingredient) {
	if in.Tag != "" {
		b.VarInt(0)
		b.String("minecraft:" + in.Tag)
		return
	}
	b.VarInt(int32(len(in.Items) + 1))
	for _, name := range in.Items {
		id, _ := world.ItemID(name)
		b.VarInt(id)
	}
}

// recipeBookable reports whether the recipe has a recipe-book display.
// crafting_special_* recipes and the decorated-pot recipe are not
// listed in the book.
func recipeBookable(r data.Recipe) bool {
	switch r.Type {
	case data.CraftingShaped, data.CraftingShapeless,
		data.CraftingDye, data.CraftingTransmute,
		data.CraftingImbue, data.Stonecutting,
		data.Smelting, data.Blasting, data.Smoking,
		data.CampfireCooking,
		data.SmithingTransform, data.SmithingTrim:
		return true
	}
	return false
}

// displayIDs maps each bookable recipe's data.Recipes index to its
// recipe display id, which counts only bookable recipes in order.
var displayIDs = func() map[int]int32 {
	m := map[int]int32{}
	var n int32
	for i, r := range data.Recipes {
		if !recipeBookable(r) {
			continue
		}
		m[i] = n
		n += recipeDisplayCount(r)
	}
	return m
}()

// recipeDisplayCount is how many display entries a recipe produces:
// transmute recipes with a material count range get one entry per
// count.
func recipeDisplayCount(r data.Recipe) int32 {
	if r.MaterialMax > 1 {
		return int32(r.MaterialMax)
	}
	return 1
}

// recipeGroupIDs assigns each recipe group its recipe-book index, the
// order groups first appear across the display list.
var recipeGroupIDs = func() map[string]int32 {
	m := map[string]int32{}
	for _, r := range data.Recipes {
		if !recipeBookable(r) || r.Group == "" {
			continue
		}
		if _, ok := m[r.Group]; !ok {
			m[r.Group] = int32(len(m))
		}
	}
	return m
}()

// bookCategoryID resolves the recipe-book category the display entry
// carries for a recipe.
func bookCategoryID(r data.Recipe) int32 {
	cat := r.Category
	switch r.Type {
	case data.Smelting:
		switch cat {
		case "food":
			return bookCatFurnaceFood
		case "blocks":
			return bookCatFurnaceBlocks
		}
		return bookCatFurnaceMisc
	case data.Blasting:
		if cat == "blocks" {
			return bookCatBlastBlocks
		}
		return bookCatBlastMisc
	case data.Smoking:
		return bookCatSmokerFood
	case data.CampfireCooking:
		return bookCatCampfire
	case data.Stonecutting:
		return bookCatStonecutter
	case data.SmithingTransform, data.SmithingTrim:
		return bookCatSmithing
	}
	switch cat {
	case "building":
		return bookCatBuilding
	case "redstone":
		return bookCatRedstone
	case "equipment":
		return bookCatEquipment
	}
	return bookCatMisc
}

// stationItem returns the crafting-station item a display carries.
func stationItem(r data.Recipe) string {
	switch r.Type {
	case data.Smelting:
		return "furnace"
	case data.Blasting:
		return "blast_furnace"
	case data.Smoking:
		return "smoker"
	case data.CampfireCooking:
		return "campfire"
	case data.Stonecutting:
		return "stonecutter"
	case data.SmithingTransform, data.SmithingTrim:
		return "smithing_table"
	}
	return "crafting_table"
}

// dyeSlot encodes the dyes requirement slot (an only_with_component
// display over the item dyes tag, keyed on the dye component).
func dyeSlot(b *protocol.Buffer) {
	b.VarInt(sdOnlyWithComponent)
	slotTag(b, "dyes")
	b.VarInt(dataComponentDye)
}

// recipeGiveFlags picks the entry flag byte for a /recipe give: the
// notify bit carries the recipe's own show_notification flag, and the
// highlight bit is always set.
func recipeGiveFlags(r data.Recipe) byte {
	if r.HideNotification {
		return 2
	}
	return 3
}

// recipeEntry writes one RecipeDisplayEntry (display id, display, group,
// category, requirements) plus the flags byte, for a bookable recipe.
// recipeEntry writes the recipe's display entries. It returns the
// number of entries written — usually one, but transmute recipes with
// a material count range emit one entry per count.
func recipeEntry(b *protocol.Buffer, idx int, flags byte) int {
	r := data.Recipes[idx]
	if r.Type == data.CraftingTransmute && r.MaterialMax > 1 {
		for c := int32(1); c <= int32(r.MaterialMax); c++ {
			transmuteEntry(b, r, displayIDs[idx]+c-1, c, flags)
		}
		return int(r.MaterialMax)
	}
	b.VarInt(displayIDs[idx])

	station, _ := world.ItemID(stationItem(r))
	result, _ := world.ItemID(r.Result.Item)

	writeSlot := func(in data.Ingredient) { ingredientSlot(b, in) }

	switch r.Type {
	case data.CraftingShaped:
		b.VarInt(displayShaped)
		// vanilla shows the pattern trimmed to its occupied box
		w := 0
		minX, maxX := -1, 0
		minY, maxY := -1, 0
		for y, row := range r.Pattern {
			if len(row) > w {
				w = len(row)
			}
			for x := 0; x < len(row); x++ {
				if _, ok := r.Key[row[x]]; ok {
					if minX < 0 || x < minX {
						minX = x
					}
					if x > maxX {
						maxX = x
					}
					if minY < 0 {
						minY = y
					}
					maxY = y
				}
			}
		}
		if minX < 0 {
			minX, maxX, minY, maxY = 0, -1, 0, -1
		}
		tw, th := maxX-minX+1, maxY-minY+1
		b.VarInt(int32(tw))
		b.VarInt(int32(th))
		var slots []data.Ingredient
		for y := minY; y <= maxY; y++ {
			row := r.Pattern[y]
			for x := minX; x <= maxX; x++ {
				if x < len(row) {
					if in, ok := r.Key[row[x]]; ok {
						slots = append(slots, in)
						continue
					}
				}
				slots = append(slots, data.Ingredient{})
			}
		}
		b.VarInt(int32(len(slots)))
		for _, in := range slots {
			writeSlot(in)
		}
		resultStack(b, r, result)
		slotItem(b, station)
		writeGroupCategory(b, r)
		writeRequirements(b, slots)
	case data.CraftingShapeless:
		b.VarInt(displayShapeless)
		b.VarInt(int32(len(r.Ingredients)))
		for _, in := range r.Ingredients {
			writeSlot(in)
		}
		resultStack(b, r, result)
		slotItem(b, station)
		writeGroupCategory(b, r)
		writeRequirements(b, r.Ingredients)
	case data.CraftingDye:
		b.VarInt(displayShapeless)
		b.VarInt(2)
		b.VarInt(sdComposite)
		b.VarInt(1)
		slotItem(b, result)
		dyeSlot(b)
		b.VarInt(sdDyed)
		dyeSlot(b)
		b.VarInt(sdComposite)
		b.VarInt(1)
		slotItem(b, result)
		slotItem(b, station)
		writeGroupCategory(b, r)
		b.Bool(true)
		b.VarInt(2)
		b.VarInt(2)
		b.VarInt(result)
		b.VarInt(0)
		b.String("minecraft:dyes")
		b.Byte(flags)
		return 1
	case data.CraftingTransmute:
		b.VarInt(displayShapeless)
		b.VarInt(2)
		writeSlot(r.Input)
		writeSlot(r.Material)
		resultStack(b, r, result)
		slotItem(b, station)
		writeGroupCategory(b, r)
		b.Bool(true)
		b.VarInt(2)
		requirement(b, r.Input)
		requirement(b, r.Material)
		b.Byte(flags)
		return 1
	case data.CraftingImbue:
		b.VarInt(displayShaped)
		b.VarInt(3)
		b.VarInt(3)
		b.VarInt(9)
		potion, _ := world.ItemID("lingering_potion")
		var reqs []data.Ingredient
		for i := 0; i < 9; i++ {
			if i == 4 {
				b.VarInt(sdWithAnyPotion)
				b.VarInt(sdComposite)
				b.VarInt(1)
				slotItem(b, potion)
				reqs = append(reqs, data.Ingredient{Items: []string{"lingering_potion"}})
				continue
			}
			writeSlot(r.Material)
			reqs = append(reqs, r.Material)
		}
		b.VarInt(sdWithAnyPotion)
		resultStack(b, r, result)
		slotItem(b, station)
		writeGroupCategory(b, r)
		b.Bool(true)
		b.VarInt(int32(len(reqs)))
		for _, in := range reqs {
			requirement(b, in)
		}
		b.Byte(flags)
		return 1
	case data.Smelting, data.Blasting, data.Smoking, data.CampfireCooking:
		b.VarInt(displayFurnace)
		writeSlot(r.Ingredients[0])
		b.VarInt(sdAnyFuel)
		resultStack(b, r, result)
		slotItem(b, station)
		cook := r.CookingTime
		if cook == 0 {
			cook = 200
			if r.Type == data.Smoking || r.Type == data.Blasting {
				cook = 100
			}
			if r.Type == data.CampfireCooking {
				cook = 600
			}
		}
		b.VarInt(int32(cook))
		b.Float(float32(r.Experience))
		writeGroupCategory(b, r)
		writeRequirements(b, r.Ingredients)
	case data.Stonecutting:
		b.VarInt(displayStonecutter)
		writeSlot(r.Ingredients[0])
		resultStack(b, r, result)
		slotItem(b, station)
		writeGroupCategory(b, r)
		writeRequirements(b, r.Ingredients)
	case data.SmithingTransform:
		b.VarInt(displaySmithing)
		writeSlot(r.Template)
		writeSlot(r.Base)
		writeSlot(r.Addition)
		resultStack(b, r, result)
		slotItem(b, station)
		writeGroupCategory(b, r)
		writeRequirements(b, []data.Ingredient{r.Template, r.Base, r.Addition})
	case data.SmithingTrim:
		b.VarInt(displaySmithing)
		writeSlot(r.Template)
		writeSlot(r.Base)
		writeSlot(r.Addition)
		b.VarInt(sdSmithingTrim)
		slotTag(b, "trimmable_armor")
		slotTag(b, "trim_materials")
		b.VarInt(int32(trimPatternID(r.Template) + 1))
		slotItem(b, station)
		writeGroupCategory(b, r)
		writeRequirements(b, []data.Ingredient{r.Template, r.Base, r.Addition})
	}
	b.Byte(flags)
	return 1
}

// transmuteEntry writes one display entry of a ranged transmute
// recipe (e.g. map cloning): the input plus mats copies of the
// material, a result of mats+1, and requirements listing the input
// once with the material at its maximum count.
func transmuteEntry(b *protocol.Buffer, r data.Recipe, dispID, mats int32, flags byte) {
	station, _ := world.ItemID(stationItem(r))
	result, _ := world.ItemID(r.Result.Item)
	b.VarInt(dispID)
	b.VarInt(displayShapeless)
	b.VarInt(1 + mats)
	ingredientSlot(b, r.Input)
	for i := int32(0); i < mats; i++ {
		ingredientSlot(b, r.Material)
	}
	b.VarInt(sdItemStack)
	b.VarInt(result)
	b.VarInt(mats + 1)
	b.VarInt(0)
	b.VarInt(0)
	slotItem(b, station)
	writeGroupCategory(b, r)
	b.Bool(true)
	b.VarInt(1 + int32(r.MaterialMax))
	requirement(b, r.Input)
	for i := int32(0); i < int32(r.MaterialMax); i++ {
		requirement(b, r.Material)
	}
	b.Byte(flags)
}

// writeRequirements writes the optional crafting-requirements list: a
// present marker, the ingredient count, then each ingredient as a
// holder set. Empty slots contribute no requirement.
func writeRequirements(b *protocol.Buffer, slots []data.Ingredient) {
	var reqs []data.Ingredient
	for _, in := range slots {
		if in.Tag != "" || len(in.Items) > 0 {
			reqs = append(reqs, in)
		}
	}
	b.Bool(true)
	b.VarInt(int32(len(reqs)))
	for _, in := range reqs {
		requirement(b, in)
	}
}

// writeGroupCategory writes the entry's group optional-int (group
// index + 1, 0 for none) and the book category, in wire order before
// the requirements list.
func writeGroupCategory(b *protocol.Buffer, r data.Recipe) {
	if g, ok := recipeGroupIDs[r.Group]; r.Group != "" && ok {
		b.VarInt(g + 1)
	} else {
		b.VarInt(0)
	}
	b.VarInt(bookCategoryID(r))
}

// trimPatternID resolves a smithing-trim template item to its pattern
// index in the trim_pattern registry.
func trimPatternID(template data.Ingredient) int {
	name := ""
	if len(template.Items) > 0 {
		name = template.Items[0]
	}
	name = trimSuffix(name, "_armor_trim_smithing_template")
	name = trimSuffix(name, "_smithing_template")
	for i, p := range trimPatternIDs {
		if p == name {
			return i
		}
	}
	return 0
}

func trimSuffix(s, suffix string) string {
	if len(s) > len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}
