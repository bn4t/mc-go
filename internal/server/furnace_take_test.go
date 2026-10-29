package server

import (
	"testing"

	"mc-go/internal/world"
)

// Exercise the actual result-click path and parsed recipe_crafted criteria.
// The probe requires glass remaining in the post-take furnace grid, rejects
// consumed sand, and distinguishes the recipe's namespaced ledger ID from
// the criterion matcher's canonical ID. No custom datapacks are loaded by Go.
func TestFurnaceTakeRecipeCraftedCriterion(t *testing.T) {
	saved := gameAdvs
	gameAdvs = make(map[string]*advMeta, len(saved)+3)
	for id, meta := range saved {
		gameAdvs[id] = meta
	}
	defer func() { gameAdvs = saved }()
	probe := func(id, recipe, item string) *advMeta {
		m := &advMeta{id: id, criteria: []string{"take"}, reqs: [][]string{{"take"}}}
		m.recipes = []recipeCond{parseRecipeCond("take", map[string]any{"recipe_id": recipe, "ingredients": []any{map[string]any{"items": item}}})}
		gameAdvs[id] = m
		return m
	}
	good := probe("mcgo:test/furnace_glass", "minecraft:glass", "minecraft:glass")
	consumed := probe("mcgo:test/furnace_sand", "minecraft:glass", "minecraft:sand")
	other := probe("mcgo:test/furnace_other_recipe", "minecraft:stone", "minecraft:glass")
	s := New("unused")
	sess := placeSession(t, s, "RecipeSmelter", 8.5, -60, 2.5)
	pos := world.Pos{X: 8, Y: -61, Z: 0}
	block, _ := world.BlockState("furnace")
	s.setBlockAndBroadcast("RecipeSmelter", pos, block)
	f := s.furnaceAt(sess.dimension(), pos)
	f.slots[furnaceInput] = ItemStack{ID: world.ItemIDs["sand"], Count: 2}
	f.slots[furnaceFuel] = ItemStack{ID: world.ItemIDs["coal"], Count: 1}
	for tick := 0; tick < 2*int(furnaceCookTicks)+2; tick++ {
		s.furnaceTickStep()
	}
	if got := f.slots[furnaceOutput]; got.ID != world.ItemIDs["glass"] || got.Count != 2 {
		t.Fatalf("cooked output=%+v", got)
	}
	s.openFurnace(sess, pos)
	for len(sess.sendCh) > 0 {
		materializeOutboundPacket(t, sess, <-sess.sendCh)
	}
	s.handleClickContainer(sess, clickWindowPacket(t, sess, sess.window.id, furnaceOutput, 1, 0))
	if f.slots[furnaceOutput].Count != 1 || sess.carriedBuffer().Count != 1 {
		t.Fatal("right-click did not take half of real smelted output")
	}
	sess.advMu.Lock()
	defer sess.advMu.Unlock()
	if _, ok := sess.advCrits[good.id]["take"]; !ok || !sess.advDone[good.id] {
		t.Fatal("parsed recipe_crafted criterion did not complete through furnace onTake")
	}
	if _, ok := sess.advCrits[consumed.id]["take"]; ok {
		t.Fatal("recipe_crafted matched consumed sand instead of post-take grid")
	}
	if _, ok := sess.advCrits[other.id]["take"]; ok {
		t.Fatal("recipe_crafted matched the wrong recipe")
	}
}
