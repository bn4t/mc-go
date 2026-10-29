package server

// ArmorStand public dimensions and flags: Marker has a zero box, no
// environmental motion or ordinary damage, and client flag bit 0x10.
func armorStandIsMarker(e *Entity) bool {
	return e.Type == mustEntityType("armor_stand") && e.ArmorStandMarker
}

func armorStandSavedFields(e *Entity, m map[string]any) {
	m["Small"] = nbtBool(e.Small)
	m["ShowArms"] = nbtBool(e.ShowArms)
	m["DisabledSlots"] = e.DisabledSlots
	delete(m, "Marker")
	if e.ArmorStandMarker {
		m["Marker"] = nbtBool(true)
	}
}

func armorStandLoadFields(e *Entity, m map[string]any) {
	e.Small, e.ShowArms, e.ArmorStandMarker = nbtTruth(m["Small"]), nbtTruth(m["ShowArms"]), nbtTruth(m["Marker"])
	e.DisabledSlots, _ = nbtInt32(m["DisabledSlots"])
}

// Public Entity.getPistonPushReaction overrides, measured across the registry.
func entityIgnoresPistonPush(e *Entity) bool {
	name, _ := EntityTypeName(e.Type)
	switch name {
	case "interaction", "marker", "ominous_item_spawner":
		return true
	case "armor_stand":
		return e.ArmorStandMarker
	}
	return false
}
