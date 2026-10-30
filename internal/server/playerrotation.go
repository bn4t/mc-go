package server

import "math"

// javaPlayerRotation normalizes the Java movement proposal before it enters
// the shared frontend rule. Authoritative plugin replacements retain their
// existing shared movement semantics.
func javaPlayerRotation(yaw, pitch float32) (float32, float32) {
	wrap := func(value float32) float32 {
		value = float32(math.Mod(float64(value), 360))
		if value >= 180 {
			value -= 360
		}
		if value < -180 {
			value += 360
		}
		return value
	}
	yaw, pitch = wrap(yaw), wrap(pitch)
	if pitch > 90 {
		pitch = 90
	} else if pitch < -90 {
		pitch = -90
	}
	return yaw, pitch
}
