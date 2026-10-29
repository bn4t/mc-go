package server

import "testing"

func TestStartupAdvancementOverrideDoesNotSeedCapturedProgress(t *testing.T) {
	s := &Server{advancementPack: &advancementRegistry{custom: map[string]bool{advCraftingTable: true}}}
	sess := &Session{server: s}
	sess.initAdvancements(123)
	if len(sess.advSent) != 0 || len(sess.advCrits) != 0 || len(sess.advDone) != 0 {
		t.Fatal("overridden startup advancement inherited captured definition/progress")
	}
	plain := &Session{server: &Server{}}
	plain.initAdvancements(456)
	if !plain.advDone[advCraftingTable] || plain.advCrits[advCraftingTable]["unlock_right_away"] != 456 {
		t.Fatal("default captured startup state changed")
	}
}

func TestTickCriterionPredicatesFailClosed(t *testing.T) {
	for _, raw := range []any{nil, map[string]any{}, map[string]any{"player": nil}, map[string]any{"player": []any{}}} {
		if !unconditionalTickCriterion(raw) {
			t.Errorf("predicate-free tick rejected %v", raw)
		}
	}
	for _, raw := range []any{"bad", map[string]any{"player": map[string]any{"gamemode": "creative"}}, map[string]any{"player": []any{map[string]any{"condition": "minecraft:entity_properties"}}}} {
		if unconditionalTickCriterion(raw) {
			t.Errorf("unmodeled predicate treated as unconditional %v", raw)
		}
	}
}

func TestStartupRecipeOverrideDoesNotSeedCapturedUnlock(t *testing.T) {
	s := &Server{advancementPack: &advancementRegistry{custom: map[string]bool{advCraftingTable: true}}}
	sess := &Session{server: s, unlockedRecipes: map[int]bool{}}
	if err := s.sendRecipeBookUnlock(sess); err != nil {
		t.Fatal(err)
	}
	if len(sess.unlockedRecipes) != 0 {
		t.Fatal("overridden reward inherited captured crafting_table recipe")
	}
}
