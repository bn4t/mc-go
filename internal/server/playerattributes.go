package server

// updatePlayerEquipmentAttributes materializes generated main-hand modifiers.
// Rewriting an identical stack leaves explicit /attribute removals intact;
// changing components or switching away and back applies the modifiers again.
func (s *Server) updatePlayerEquipmentAttributes(sess *Session) {
	player, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return
	}
	held := sess.heldStack()
	if player.playerEquipmentAttrsInit && equipMatch(player.playerAttributeStack, held) {
		return
	}
	previous := player.playerAttributeStack
	player.playerAttributeStack = held
	player.playerEquipmentAttrsInit = true
	for _, spec := range []struct {
		attr, key string
		values    map[string]float64
	}{
		{"attack_damage", "minecraft:base_attack_damage", itemAttackDamageMod},
		{"attack_speed", "minecraft:base_attack_speed", itemAttackSpeedMod},
	} {
		oldPresent := false
		if !previous.Empty() {
			if name, valid := itemNameOf(previous.ID); valid {
				_, oldPresent = spec.values[name]
			}
		}
		amount, newPresent := 0.0, false
		if !held.Empty() {
			if name, valid := itemNameOf(held.ID); valid {
				amount, newPresent = spec.values[name]
			}
		}
		if !oldPresent && !newPresent {
			continue
		}
		if oldPresent {
			delete(player.AttrMods[spec.attr], spec.key)
		}
		if newPresent {
			if player.AttrMods == nil {
				player.AttrMods = map[string]map[string]attrMod{}
			}
			if player.AttrMods[spec.attr] == nil {
				player.AttrMods[spec.attr] = map[string]attrMod{}
			}
			player.AttrMods[spec.attr][spec.key] = attrMod{value: amount}
		}
		player.markAttrDirty(spec.attr)
	}
}

// flushPlayerAttributes includes the owning client as well as trackers. Bases
// and modifiers stay raw on the wire; clients evaluate the ranged result.
func (s *Server) flushPlayerAttributes(sess *Session) {
	player, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return
	}
	body := mobSyncAttrs(player, s.clock.count)
	if body == nil {
		return
	}
	player.AttrsDirty = nil
	player.AttrDirty = false
	cache := javaEntityBodyCache{}
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != player.Dim {
			return true
		}
		if other != sess && !other.tracksEntity(player.ID) {
			return true
		}
		_ = cache.writeAttributes(other, body)
		return true
	})
}
