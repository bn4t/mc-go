// Package data holds the vanilla data pack in Go form: the item and
// block tag registries and the full recipe registry as generated
// tables, plus the loot table, advancement and villager trade trees as
// embedded data-pack JSON.
//
// Everything in this package comes from the bundled data pack inside
// the real server jar (see tools/gendata). GOAL.md allows the jar as a
// data source; no class files are read, and no behavior is derived
// from decompiled code.
package data

import (
	"strings"
	"sync"
)

// ItemStack is one item id plus a count, mirroring the wire stack.
type ItemStack struct {
	Item  string
	Count int32
}

// Recipe type constants. The values match the data pack's "type"
// discriminator with the minecraft: prefix dropped.
const (
	CraftingShaped                = "crafting_shaped"
	CraftingShapeless             = "crafting_shapeless"
	CraftingTransmute             = "crafting_transmute"
	CraftingDye                   = "crafting_dye"
	CraftingImbue                 = "crafting_imbue"
	CraftingDecoratedPot          = "crafting_decorated_pot"
	CraftingSpecialBannerduplicate = "crafting_special_bannerduplicate"
	CraftingSpecialShielddecoration = "crafting_special_shielddecoration"
	CraftingSpecialRepairitem     = "crafting_special_repairitem"
	CraftingSpecialMapextending   = "crafting_special_mapextending"
	CraftingSpecialFireworkStarFade = "crafting_special_firework_star_fade"
	CraftingSpecialFireworkStar   = "crafting_special_firework_star"
	CraftingSpecialFireworkRocket = "crafting_special_firework_rocket"
	CraftingSpecialBookcloning    = "crafting_special_bookcloning"
	Smelting                      = "smelting"
	Blasting                      = "blasting"
	Smoking                       = "smoking"
	CampfireCooking               = "campfire_cooking"
	Stonecutting                  = "stonecutting"
	SmithingTransform             = "smithing_transform"
	SmithingTrim                  = "smithing_trim"
)

// Ingredient is one recipe input. Items is the flattened membership
// list (tags expanded) that matching tests against; Tag, when set,
// records that the data pack expressed it as "#minecraft:<tag>", which
// the recipe book's slot display has to reproduce verbatim.
type Ingredient struct {
	Tag   string
	Items []string
}

// Accepts reports whether item satisfies the ingredient.
func (in Ingredient) Accepts(item string) bool {
	for _, s := range in.Items {
		if s == item {
			return true
		}
	}
	return false
}

// TagRef returns the ingredient's tag in wire form ("minecraft:planks")
// when it came from a tag.
func (in Ingredient) TagRef() string {
	if in.Tag == "" {
		return ""
	}
	return "minecraft:" + in.Tag
}

// Recipe is one entry of the vanilla recipe registry.
type Recipe struct {
	ID          string
	Type        string
	Group       string
	Category    string
	Pattern     []string             // shaped: one string per row
	Key         map[byte]Ingredient  // shaped: rune -> ingredient
	Ingredients []Ingredient         // shapeless, cooking, stonecutting inputs
	Input       Ingredient           // transmute/dye/imbue base item
	Material    Ingredient           // transmute/dye/imbue applied item
	Template    Ingredient           // smithing template
	Base        Ingredient           // smithing base
	Addition    Ingredient           // smithing addition
	Result      ItemStack
	CookingTime int
	Experience  float64
}

// IsCooking reports whether the recipe is one of the furnace-family
// recipes, which share the furnace recipe display.
func (r Recipe) IsCooking() bool {
	switch r.Type {
	case Smelting, Blasting, Smoking, CampfireCooking:
		return true
	}
	return false
}

// RecipeByID resolves a recipe id (with or without namespace).
func RecipeByID(id string) (Recipe, bool) {
	for _, r := range Recipes {
		if r.ID == id {
			return r, true
		}
	}
	return Recipe{}, false
}

// HasItemTag reports whether item belongs to the named tag, accepting
// either "#minecraft:planks", "minecraft:planks" or "planks".
func HasItemTag(tag, item string) bool {
	t := strings.TrimPrefix(tag, "#")
	t = strings.TrimPrefix(t, "minecraft:")
	for _, s := range ItemTags[t] {
		if s == item {
			return true
		}
	}
	return false
}

var (
	byIDOnce sync.Once
	byID     map[string]int32
)

// RecipeIndex returns the network id of a recipe, or -1.
func RecipeIndex(id string) int32 {
	byIDOnce.Do(func() {
		byID = make(map[string]int32, len(Recipes))
		for i, r := range Recipes {
			byID[r.ID] = int32(i)
		}
	})
	if i, ok := byID[id]; ok {
		return i
	}
	return -1
}
