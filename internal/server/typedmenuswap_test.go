package server

import (
	"testing"

	"mc-go/internal/world"
)

// These result slots award recipe_crafted from inputs before consuming them.
// Parsed criteria require every input, so an after-consumption callback or a
// namespaced/noncanonical recipe mismatch cannot pass this packet-path pin.
func TestTypedSwapRecipeCraftedCriteria(t *testing.T) {
	saved := gameAdvs
	probes := make(map[string]*advMeta, len(saved)+2)
	for id, meta := range saved {
		probes[id] = meta
	}
	for _, tc := range []struct {
		block, recipe string
		items         []string
		result        int16
	}{
		{"stonecutter", "stone_brick_slab_from_stone_stonecutting", []string{"stone"}, stonecutterResult},
		{"smithing_table", "netherite_chestplate_smithing", []string{"netherite_upgrade_smithing_template", "diamond_chestplate", "netherite_ingot"}, smithingResult},
	} {
		ingredients := make([]any, 0, len(tc.items))
		for _, item := range tc.items {
			ingredients = append(ingredients, map[string]any{"items": "minecraft:" + item})
		}
		id := "mcgo:test/swap_" + tc.block
		m := &advMeta{id: id, criteria: []string{"crafted"}, reqs: [][]string{{"crafted"}}}
		m.recipes = []recipeCond{parseRecipeCond("crafted", map[string]any{"recipe_id": "minecraft:" + tc.recipe, "ingredients": ingredients})}
		probes[id] = m
	}
	gameAdvs = probes
	defer func() { gameAdvs = saved }()
	for _, tc := range []struct {
		block  string
		items  []string
		result int16
	}{
		{"stonecutter", []string{"stone"}, stonecutterResult},
		{"smithing_table", []string{"netherite_upgrade_smithing_template", "diamond_chestplate", "netherite_ingot"}, smithingResult},
	} {
		t.Run(tc.block, func(t *testing.T) {
			s := New("unused")
			sess := placeSession(t, s, "RecipeSwapper", 8.5, -60, 2.5)
			drain := func() {
				for len(sess.sendCh) > 0 {
					materializeOutboundPacket(t, sess, <-sess.sendCh)
				}
			}
			pos := world.Pos{X: 8, Y: -61, Z: 0}
			block, _ := world.BlockState(tc.block)
			s.setBlockAndBroadcast("RecipeSwapper", pos, block)
			drain()
			s.openContainer(sess, pos)
			drain()
			for i, item := range tc.items {
				sess.setStack(slotHotbarStart+int16(i), ItemStack{ID: world.ItemIDs[item], Count: 1})
				drain()
				s.handleClickContainer(sess, clickWindowPacket(t, sess, sess.window.id, int16(i), byte(i), 2))
				drain()
			}
			if tc.block == "stonecutter" {
				s.stonecutterSelect(sess, sess.window, 5)
				drain()
			}
			if sess.menuStack(int(tc.result)).Empty() {
				t.Fatal("valid recipe did not produce a result")
			}
			s.handleClickContainer(sess, clickWindowPacket(t, sess, sess.window.id, tc.result, 8, 2))
			drain()
			for i := range tc.items {
				if !sess.menuStack(i).Empty() {
					t.Errorf("input%d was not consumed", i)
				}
			}
			if sess.stackAt(slotHotbarStart + 8).Empty() {
				t.Fatal("result did not reach hotbar")
			}
			id := "mcgo:test/swap_" + tc.block
			sess.advMu.Lock()
			defer sess.advMu.Unlock()
			if _, ok := sess.advCrits[id]["crafted"]; !ok || !sess.advDone[id] {
				t.Fatal("actual result SWAP did not complete parsed pre-consumption recipe_crafted criterion")
			}
		})
	}
}
