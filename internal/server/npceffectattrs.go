package server

// npcMovementEffectTemplate restricts this lifecycle to measured movement effects.
func npcMovementEffectTemplate(id int32) (playerEffectAttributeTemplate, bool) {
	name, ok := MobEffectName(id)
	if !ok || (name != "speed" && name != "slowness") {
		return playerEffectAttributeTemplate{}, false
	}
	ts := playerEffectAttributeTemplates[name]
	if len(ts) != 1 {
		return playerEffectAttributeTemplate{}, false
	}
	return ts[0], true
}
func setNPCMovementEffectAttribute(e *Entity, id int32, inst mobEffectInst) {
	t, ok := npcMovementEffectTemplate(id)
	if !ok {
		return
	}
	if e.AttrMods == nil {
		e.AttrMods = map[string]map[string]attrMod{}
	}
	if e.AttrMods[t.attribute] == nil {
		e.AttrMods[t.attribute] = map[string]attrMod{}
	}
	e.AttrMods[t.attribute][t.id] = attrMod{value: t.amount * float64(inst.amplifier+1), op: t.operation, permanent: true}
	e.markAttrDirty(t.attribute)
}
func removeNPCMovementEffectAttribute(e *Entity, id int32) {
	t, ok := npcMovementEffectTemplate(id)
	if !ok {
		return
	}
	if _, ok := e.AttrMods[t.attribute][t.id]; ok {
		delete(e.AttrMods[t.attribute], t.id)
		e.markAttrDirty(t.attribute)
	}
}
