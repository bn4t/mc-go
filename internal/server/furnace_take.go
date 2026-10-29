package server

import (
	"math"
	"math/rand"
	"sort"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
)

// furnaceOutputTaken settles all stored recipes on any player take, even a
// partial take. Measured with javap25 AbstractFurnaceBlockEntity's award and
// createExperience operations: XP is rounded per recipe, emitted at the
// player's feet, and the recipe ledger is cleared only after the awards.
func (s *Server) furnaceOutputTaken(sess *Session, w *openWindow, taken ItemStack) {
	sess.addItemStat(statCrafted, taken.ID, taken.Count)
	w.dim.furnaces.mu.Lock()
	used := w.furnace.used
	var grid craftGrid
	copy(grid[:], w.furnace.slots[:])
	w.dim.furnaces.mu.Unlock()
	if len(used) == 0 {
		return
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	x, y, z, _, _, _ := sess.Pos()
	var entries protocol.Buffer
	var count int32
	for _, name := range names {
		recipe, ok := data.RecipeByID(strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			continue
		}
		exact := float32(used[name]) * float32(recipe.Experience)
		xp := int32(math.Floor(float64(exact)))
		if fraction := exact - float32(xp); fraction > 0 && rand.Float32() < fraction {
			xp++
		}
		s.awardExperience(w.dim.name, x, y, z, xp)
		if idx, ok := recipeIndex[strings.TrimPrefix(name, "minecraft:")]; ok && recipeBookable(data.Recipes[idx]) && sess.unlockRecipe(idx) {
			count += int32(recipeEntryForSession(sess, &entries, idx, recipeGiveFlags(data.Recipes[idx])))
			sess.grantRecipeCriterion(idx)
		}
	}
	if count > 0 {
		var b protocol.Buffer
		b.VarInt(count)
		b.Raw(entries.Bytes())
		b.Bool(false)
		_ = sess.write(sess.ids().PlayRecipeBookAdd, b.Bytes())
	}
	for _, name := range names {
		s.fireRecipeCrafted(sess, strings.TrimPrefix(name, "minecraft:"), grid)
	}
	w.dim.furnaces.mu.Lock()
	w.furnace.used = nil
	w.dim.furnaces.mu.Unlock()
}
