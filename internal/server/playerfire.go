package server

import "math/rand"

// Native player contact settlement uses the twenty-tick inactive baseline.
// This is separate from clearFire's positive-to-zero operation: a contact
// that raises an inactive counter must retain that progress toward ignition.
func settledPlayerFire(before, after int32) int32 {
	if after > 0 || after > before {
		return after
	}
	return -20
}

// BaseFireBlock contact warms an inactive player one counter step at a time.
// Once nonnegative, the player adds one or two ticks per contact; ignition
// supplies an eight-second minimum without shortening an existing burn.
func playerFireContactTicks(before int32) int32 {
	next := before + 1
	if before >= 0 {
		next = before + int32(1+rand.Intn(2))
	}
	if next >= 0 && next < 160 {
		next = 160
	}
	return next
}
