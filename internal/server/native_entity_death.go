package server

// Frozen native lethal-damage saves retain generic living corpses as roots
// and passengers until physical removal. Special dragon and nonliving removal
// paths retain their existing admission rules; this is not a lifecycle rewrite.
func nativeEntityRootPresentForSave(e *Entity) bool {
	return e != nil && (!e.Dying || e.Living && e.dragon == nil)
}

// Native cold corpse saves retain Health=0 and the signed-short DeathTime.
// Reconstruct the countdown without invoking die(), which would replay loot,
// XP, credit and damage events already processed before the save.
func restoreNativeLivingDeathSave(e *Entity, save map[string]any) {
	if e == nil || !e.Living || e.dragon != nil {
		return
	}
	e.deathTicks = 0
	if age, ok := nbtInt32(save["DeathTime"]); ok {
		e.deathTicks = int32(int16(age))
	}
	e.Dying = e.Health <= 0
}
