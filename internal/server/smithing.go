package server

import (
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// smithingAccepts is the smithing table's per-slot mayPlace: each
// input slot takes whatever any registered smithing recipe lists for
// that role — the union vanilla's RecipeManager.getIngredient builds
// the menu's three slot filters from. The result slot is unreachable
// here (spec.results already refuses it).
func smithingAccepts(slot int, it ItemStack) bool {
	name, ok := itemNameOf(it.ID)
	if !ok {
		return false
	}
	for i := range data.Recipes {
		r := &data.Recipes[i]
		if r.Type != data.SmithingTransform && r.Type != data.SmithingTrim {
			continue
		}
		switch slot {
		case smithingTemplate:
			if r.Template.Accepts(name) {
				return true
			}
		case smithingBase:
			if r.Base.Accepts(name) {
				return true
			}
		case smithingAddition:
			if r.Addition.Accepts(name) {
				return true
			}
		}
	}
	return false
}

// smithingCompute returns the computed result plus the recipe that
// produced it — SmithingMenu.onTake feeds the recipe id to the
// recipe_crafted trigger (the trim advancements key off it).
//
// Transform mirrors TransmuteRecipe.assemble: the result is the base
// item transmuted to the recipe's result item, its component patch
// carried whole — a renamed, enchanted diamond chestplate comes out a
// renamed, enchanted netherite chestplate.
//
// Trim mirrors SmithingTrimRecipe.assemble: the result is a copy of
// the base with minecraft:trim set to (material from the addition's
// provides_trim_material component, pattern from the template's name
// suffix). Vanilla's checkMaterialEquality compares the RECIPE
// ingredient's material — the trim_materials tag's first member —
// against the base armor's, so it never rejects a real input:
// diamond-on-diamond trims emit fine (measured on 26.2).
func smithingCompute(tmpl, base, add ItemStack) (ItemStack, string) {
	if tmpl.Empty() || base.Empty() || add.Empty() {
		return ItemStack{}, ""
	}
	tname, ok := itemNameOf(tmpl.ID)
	if !ok {
		return ItemStack{}, ""
	}
	bname, ok := itemNameOf(base.ID)
	if !ok {
		return ItemStack{}, ""
	}
	aname, ok := itemNameOf(add.ID)
	if !ok {
		return ItemStack{}, ""
	}
	for i := range data.Recipes {
		r := &data.Recipes[i]
		if !r.Template.Accepts(tname) || !r.Base.Accepts(bname) || !r.Addition.Accepts(aname) {
			continue
		}
		switch r.Type {
		case data.SmithingTransform:
			id, ok := world.ItemID(r.Result.Item)
			if !ok {
				continue
			}
			out := base
			out.ID = id
			return out, r.ID
		case data.SmithingTrim:
			mat := trimMaterialForItem[aname]
			if mat == "" {
				continue
			}
			pat := strings.TrimSuffix(tname, "_armor_trim_smithing_template")
			if pat == tname {
				continue
			}
			out := base
			out.Trim = mat + "/" + pat
			return out, r.ID
		}
	}
	return ItemStack{}, ""
}

// trimMaterialForItem is the provides_trim_material component table
// (measured on 26.2's item component reports): which trim material an
// addition item supplies. Vanilla resolves it dynamically; the
// datapack ships these eleven and nothing else.
var trimMaterialForItem = map[string]string{
	"amethyst_shard":  "amethyst",
	"copper_ingot":    "copper",
	"diamond":         "diamond",
	"emerald":         "emerald",
	"gold_ingot":      "gold",
	"iron_ingot":      "iron",
	"lapis_lazuli":    "lapis",
	"netherite_ingot": "netherite",
	"quartz":          "quartz",
	"redstone":        "redstone",
	"resin_brick":     "resin",
}

// smithingTake is SmithingMenu.onTake's full contract: shrink the
// three inputs, then report the consumed recipe to recipe_crafted —
// the smithing_trim advancements (Crafting a New Look and friends)
// match on its recipe id, the grid passed as it stood pre-consume.
func (s *Server) smithingTake(sess *Session) {
	tmpl := sess.menuStack(smithingTemplate)
	base := sess.menuStack(smithingBase)
	add := sess.menuStack(smithingAddition)
	_, recipeID := smithingCompute(tmpl, base, add)
	smithingConsume(sess)
	if recipeID != "" {
		var grid craftGrid
		grid[0], grid[1], grid[2] = tmpl, base, add
		s.fireRecipeCrafted(sess, recipeID, grid)
	}
}

// smithingConsume shrinks the three ingredient slots by one
// regardless of which recipe produced the take.
func smithingConsume(sess *Session) {
	for _, sl := range []int{smithingTemplate, smithingBase, smithingAddition} {
		st := sess.menuStack(sl)
		st.Count--
		if st.Count <= 0 {
			st = ItemStack{}
		}
		sess.setMenuStack(sl, st)
	}
}
