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
	ids := make([]string, 0, len(registry.tickers))
	for id := range registry.tickers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		names := make([]string, 0, len(registry.tickers[id]))
		for name := range registry.tickers[id] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sess.advMu.Lock()
			done := sess.advDone[id]
			_, obtained := sess.advCrits[id][name]
			sess.advMu.Unlock()
			if done || obtained {
				continue
			}
			if registry.tickers[id][name](s, sess) == advancementPredicateTrue {
				sess.grantGameCriterion(registry.entries[id], name)
			}
		}
	}
}

// Recognize the predicate-free fast path before compiling contextual player
// predicates. This helper alone never accepts a nonempty contextual predicate.
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
