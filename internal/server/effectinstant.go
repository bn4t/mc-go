package server

// Measured built-in MobEffect.isInstantaneous flags: command durations are
// ticks for these three effects, and their default duration is one tick.
func isInstantaneousEffect(id int32) bool {
	return id == effectInstantHealth || id == effectInstantDamage || id == effectSaturation
}
