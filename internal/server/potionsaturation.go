package server

// applySaturationDose is the shared FoodData dose for active Saturation ticks
// and direct potion delivery. The caller determines whether a tick is due.
func (sess *Session) applySaturationDose(amplifier int32) {
	nutr := amplifier + 1
	sess.food += nutr
	if sess.food > defaultFood {
		sess.food = defaultFood
	}
	sess.saturation += float32(nutr) * 2
	if sess.saturation > float32(sess.food) {
		sess.saturation = float32(sess.food)
	}
	sess.healthDirty.Store(true)
}
