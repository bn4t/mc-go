package server

import (
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

// smithingResult mirrors TransmuteRecipe.assemble: the result is the
// base item transmuted to the recipe's result item, its component
// patch carried whole — a renamed, enchanted diamond chestplate comes
// out a renamed, enchanted netherite chestplate. smithing_trim needs
// the unmodeled trim component, so only transforms produce one today.
func smithingCompute(tmpl, base, add ItemStack) ItemStack {
	if tmpl.Empty() || base.Empty() || add.Empty() {
		return ItemStack{}
	}
	tname, ok := itemNameOf(tmpl.ID)
	if !ok {
		return ItemStack{}
	}
	bname, ok := itemNameOf(base.ID)
	if !ok {
		return ItemStack{}
	}
	aname, ok := itemNameOf(add.ID)
	if !ok {
		return ItemStack{}
	}
	for i := range data.Recipes {
		r := &data.Recipes[i]
		if r.Type != data.SmithingTransform {
			continue
		}
		if !r.Template.Accepts(tname) || !r.Base.Accepts(bname) || !r.Addition.Accepts(aname) {
			continue
		}
		id, ok := world.ItemID(r.Result.Item)
		if !ok {
			continue
		}
		out := base
		out.ID = id
		return out
	}
	return ItemStack{}
}

// smithingConsume mirrors SmithingMenu.onTake: every result take
// shrinks the three ingredient slots by one regardless of which
// recipe produced it.
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
