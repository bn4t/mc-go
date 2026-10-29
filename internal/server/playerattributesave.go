package server

import (
	"sort"
	"strings"

	"mc-go/internal/nbt"
)

// Player files carry instantiated raw bases and permanent modifiers. Equipment
// modifiers are runtime-only and are reapplied by equipment detection on join.
type playerAttributePersist struct {
	Name      string                           `json:"name"`
	Base      float64                          `json:"base"`
	Modifiers []playerAttributeModifierPersist `json:"modifiers,omitempty"`
}
type playerAttributeModifierPersist struct {
	ID        string  `json:"id"`
	Amount    float64 `json:"amount"`
	Operation int     `json:"operation"`
}

var playerSaveInitialAttributes = []string{"entity_interaction_range", "waypoint_transmit_range", "block_interaction_range", "movement_speed"}

func snapshotPlayerAttributes(sess *Session) []playerAttributePersist {
	if sess.server == nil || sess.server.entities == nil {
		return nil
	}
	e, ok := sess.server.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return clonePlayerSavedAttributes(sess.retiredAttributes)
	}
	names := map[string]bool{}
	for _, name := range playerSaveInitialAttributes {
		names[name] = true
	}
	for name := range e.AttrTouched {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	attrs := make([]playerAttributePersist, 0, len(sorted))
	for _, name := range sorted {
		base, valid := playerAttrBases[name]
		if !valid {
			continue
		}
		if raw, ok := e.AttrBase[name]; ok {
			base = raw
		}
		a := playerAttributePersist{Name: name, Base: base}
		ids := make([]string, 0, len(e.AttrMods[name]))
		for id, mod := range e.AttrMods[name] {
			if mod.permanent {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			m := e.AttrMods[name][id]
			a.Modifiers = append(a.Modifiers, playerAttributeModifierPersist{ID: id, Amount: m.value, Operation: m.op})
		}
		attrs = append(attrs, a)
	}
	return attrs
}

func playerAttributeSaveList(attrs []playerAttributePersist) nbt.List {
	if attrs == nil {
		for _, name := range playerSaveInitialAttributes {
			attrs = append(attrs, playerAttributePersist{Name: name, Base: playerAttrBases[name]})
		}
	}
	list := nbt.List{Elem: nbt.TagCompound}
	for _, a := range attrs {
		record := map[string]any{"id": "minecraft:" + a.Name, "base": a.Base}
		mods := nbt.List{Elem: nbt.TagCompound}
		for _, m := range a.Modifiers {
			if m.Operation >= 0 && m.Operation < len(attrOpNames) {
				mods.Items = append(mods.Items, map[string]any{"id": m.ID, "amount": m.Amount, "operation": attrOpNames[m.Operation]})
			}
		}
		if len(mods.Items) > 0 {
			record["modifiers"] = mods
		}
		list.Items = append(list.Items, record)
	}
	return list
}

func playerAttributesFromSave(value any) []playerAttributePersist {
	list, ok := value.(nbt.List)
	if !ok {
		return nil
	}
	attrs := make([]playerAttributePersist, 0, len(list.Items))
	for _, value := range list.Items {
		record, ok := value.(map[string]any)
		if !ok {
			continue
		}
		id, _ := record["id"].(string)
		name := strings.TrimPrefix(id, "minecraft:")
		if _, ok := playerAttrBases[name]; !ok {
			continue
		}
		base, ok := nbtDouble(record["base"])
		if !ok {
			base = playerAttrBases[name]
		}
		a := playerAttributePersist{Name: name, Base: base}
		if mods, ok := record["modifiers"].(nbt.List); ok {
			for _, value := range mods.Items {
				mod, ok := value.(map[string]any)
				if !ok {
					continue
				}
				id, _ := mod["id"].(string)
				op, _ := mod["operation"].(string)
				for n, name := range attrOpNames {
					if op == name && id != "" {
						amount, _ := nbtDouble(mod["amount"])
						a.Modifiers = append(a.Modifiers, playerAttributeModifierPersist{ID: id, Amount: amount, Operation: n})
						break
					}
				}
			}
		}
		attrs = append(attrs, a)
	}
	return attrs
}

// Attribute state is applied after the new physical entity exists. The scale
// bounding box still starts at constructor size until its normal refresh.
func (s *Server) applySavedPlayerAttributes(sess *Session) {
	p, ok := s.savedPlayerForSession(sess)
	if !ok {
		return
	}
	e, ok := s.entities.GetPtr(sess.entityID.Load())
	if !ok {
		return
	}
	for _, a := range p.Attributes {
		if _, valid := playerAttrBases[a.Name]; !valid {
			continue
		}
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		e.AttrBase[a.Name] = a.Base
		if e.AttrMods == nil {
			e.AttrMods = map[string]map[string]attrMod{}
		}
		mods := map[string]attrMod{}
		for _, m := range a.Modifiers {
			mods[m.ID] = attrMod{value: m.Amount, op: m.Operation, permanent: true}
		}
		e.AttrMods[a.Name] = mods
		e.markAttrDirty(a.Name)
	}
	// Player.readAdditionalSaveData applies abilities after raw attributes.
	// Only the movement base is replaced; its permanent modifiers remain.
	if p.WalkSpeed != nil {
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		e.AttrBase["movement_speed"] = float64(p.savedWalkingSpeed())
		e.markAttrDirty("movement_speed")
	}

}

func clonePlayerSavedAttributes(attrs []playerAttributePersist) []playerAttributePersist {
	out := cloneDataSaveSlice(attrs)
	for i := range out {
		out[i].Modifiers = cloneDataSaveSlice(out[i].Modifiers)
	}
	return out
}

func (sess *Session) playerWalkingSpeed() float32 {
	if sess.walkingSpeedSet {
		return sess.walkingSpeed
	}
	return .1
}
func (p playerPersist) savedWalkingSpeed() float32 {
	if p.WalkSpeed != nil {
		return float32(*p.WalkSpeed)
	}
	return .1
}
