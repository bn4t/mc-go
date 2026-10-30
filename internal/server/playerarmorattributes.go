package server

// updatePlayerArmorAttributes materializes default armor modifiers per slot.
// Other slot changes leave explicitly removed modifiers alone. Snapshots use
// equipMatch's existing scalar/component-string comparison; mutable nested
// Contents transitions are outside that comparison's current guarantees.
func (s *Server) updatePlayerArmorAttributes(sess *Session) {
	player, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return
	}
	current := [5]struct {
		slot  string
		stack ItemStack
	}{
		{"head", sess.stackAt(slotArmorStart)},
		{"chest", sess.stackAt(slotArmorStart + 1)},
		{"legs", sess.stackAt(slotArmorStart + 2)},
		{"feet", sess.stackAt(slotArmorStart + 3)},
		{"body", sess.bodyArmor},
	}
	initialized := player.playerArmorAttrsInit
	for index, part := range current {
		previous := player.playerArmorStacks[index]
		if initialized && equipMatch(previous, part.stack) {
			continue
		}
		player.playerArmorStacks[index] = part.stack
		if !previous.Empty() {
			if name, valid := itemNameOf(previous.ID); valid {
				for _, modifier := range itemArmorAttributes[name] {
					if modifier.slot != part.slot {
						continue
					}
					if _, exists := player.AttrMods[modifier.attribute][modifier.key]; exists {
						delete(player.AttrMods[modifier.attribute], modifier.key)
						player.markAttrDirty(modifier.attribute)
					}
				}
			}
		}
		if !part.stack.Empty() {
			if name, valid := itemNameOf(part.stack.ID); valid {
				for _, modifier := range itemArmorAttributes[name] {
					if modifier.slot != part.slot {
						continue
					}
					if player.AttrMods == nil {
						player.AttrMods = map[string]map[string]attrMod{}
					}
					if player.AttrMods[modifier.attribute] == nil {
						player.AttrMods[modifier.attribute] = map[string]attrMod{}
					}
					player.AttrMods[modifier.attribute][modifier.key] = attrMod{value: modifier.value}
					player.markAttrDirty(modifier.attribute)
				}
			}
		}
	}
	player.playerArmorAttrsInit = true
}
