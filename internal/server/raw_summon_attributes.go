package server

// Tagged living summons load their attribute instances before Health. Native
// frozen queries distinguish raw bases, permanent modifiers and effective
// values from the dimension refresh consumed by the first admitted frame.
func applyRawLivingSummonAttributes(e *Entity, tag *snbtTag) bool {
	if e == nil || !e.Living || tag == nil {
		return false
	}
	list, present := tag.vals["attributes"]
	if !present || list == nil || list.kind != snbtList {
		return false
	}
	applyPackedAttrs(e, snbtToValue(list))
	loadedMax := effectiveMobAttributeMaxHealth(e)
	e.Health = loadedMax
	if health, present := tag.vals["Health"]; present {
		e.Health = max(float32(0), min(float32(snbtNum(health)), loadedMax))
	}
	name, _ := EntityTypeName(e.Type)
	// Tamable class loading follows LivingEntity's Health read: a wild wolf
	// can initially retain Health40 while its effective maximum is already8.
	if wild, ok := tamableWildHealth[name]; ok && tag.vals["Owner"] == nil {
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		e.AttrBase["max_health"] = wild
		e.markAttrDirty("max_health")
	}
	e.MaxHealth = effectiveMobAttributeMaxHealth(e)
	return true
}

func effectiveMobAttributeMaxHealth(e *Entity) float32 {
	name, _ := EntityTypeName(e.Type)
	base := mobAttrSupplierBase(name, "max_health")
	if value, present := e.AttrBase["max_health"]; present {
		base = value
	}
	return float32(sanitizeAttributeValue("max_health", attrValue(base, e.AttrMods["max_health"])))
}

// A materialized max-health base override reaches its living consumer on an
// admitted frame even with NoAI. Raising the maximum does not itself heal.
func refreshMaterializedMobMaxHealth(e *Entity) {
	if e == nil || !e.Living || e.Dying || e.dragon != nil {
		return
	}
	if name, _ := EntityTypeName(e.Type); name == "player" {
		return
	}
	if _, present := e.AttrBase["max_health"]; !present {
		return
	}
	e.MaxHealth = effectiveMobAttributeMaxHealth(e)
	if e.Health > e.MaxHealth {
		e.Health = e.MaxHealth
		e.MetaDirty = true
	}
}
