package server

// cloudAfterDose applies per-victim lifetime changes after a completed dose.
// Radius removal happens first. The native -1 duration sentinel ignores
// DurationOnUse; other durations use signed int addition and retire at <=0.
func cloudAfterDose(e *Entity) bool {
	e.CloudRadius += e.CloudRadiusOnUse
	if e.CloudRadius < .5 {
		return false
	}
	if e.CloudDurationOnUse != 0 && e.CloudDuration != -1 {
		e.CloudDuration += e.CloudDurationOnUse
		if e.CloudDuration <= 0 {
			return false
		}
	}
	return true
}
