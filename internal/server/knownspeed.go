package server

// sampleMobKnownSpeed records actual displacement at the mob's base tick.
// It is intentionally independent of delta movement: a NoAI body may retain
// velocity without changing position. Unticked bodies retain their last sample.
func sampleMobKnownSpeed(e *Entity) {
	position := [3]float64{e.X, e.Y, e.Z}
	if e.knownSpeedInit {
		for axis := range position {
			e.knownSpeed[axis] = position[axis] - e.knownSpeedPosition[axis]
		}
	} else {
		e.knownSpeed = [3]float64{}
		e.knownSpeedInit = true
	}
	e.knownSpeedPosition = position
}
