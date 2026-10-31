package server

// Native player contact settlement uses the twenty-tick inactive baseline.
// This is separate from clearFire's positive-to-zero operation: a contact
// that raises an inactive counter must retain that progress toward ignition.
func settledPlayerFire(before, after int32) int32 {
	if after > 0 || after > before {
		return after
	}
	return -20
}
