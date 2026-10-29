package server

import "sort"

func (s *Server) hasStartupAdvancementOverride() bool {
	return s.customAdvancement(advRecipesRoot) || s.customAdvancement(advCraftingTable)
}

// ServerPlayer.tick fires the TICK trigger after menu broadcast/validity and
// before the later player inventory-touch pass. This connection tick continues
// while the world is frozen. Completed listeners stop until explicitly revoked.
func (s *Server) fireTickAdvancements(sess *Session) {
	registry := s.advancementPack
	if registry == nil {
		return
	}
	ids := make([]string, 0, len(registry.custom))
	for id := range registry.custom {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		meta := registry.entries[id]
		criteria, _ := meta.raw["criteria"].(map[string]any)
		names := append([]string(nil), meta.criteria...)
		sort.Strings(names)
		for _, name := range names {
			criterion, _ := criteria[name].(map[string]any)
			trigger, _ := criterion["trigger"].(string)
			if normalizeID(trigger) != "minecraft:tick" {
				continue
			}
			if !unconditionalTickCriterion(criterion["conditions"]) {
				continue
			}
			sess.grantGameCriterion(meta, name)
		}
	}
}

// Predicate-free tick criteria are supported here; fail closed for player
// predicates until their context codec/evaluator is modeled independently.
func unconditionalTickCriterion(raw any) bool {
	if raw == nil {
		return true
	}
	conditions, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	player, exists := conditions["player"]
	if !exists || player == nil {
		return true
	}
	if list, ok := player.([]any); ok {
		return len(list) == 0
	}
	return false
}
