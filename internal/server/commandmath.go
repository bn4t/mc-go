package server

import "math"

// These angular units are public26.2 Mth constants. Local command bases
// calculate in float32 before widening their vector components.
const commandRadiansPerDegree = float32(0x1.1df46ap-6)
const commandSinePeriod = 65536

// Build the periodic sine samples independently using standard math. The
// public API census pins all65536 float32 values against the native runtime.
var commandSineSamples = func() (values [commandSinePeriod]float32) {
	for i := range values {
		values[i] = float32(math.Sin(float64(i) * 2 * math.Pi / commandSinePeriod))
	}
	return
}()

func commandPhaseIndex(value float64) uint16 {
	if math.IsNaN(value) {
		return 0
	}
	if value >= math.MaxInt32 {
		return uint16(math.MaxInt32 & 65535)
	}
	if value <= math.MinInt32 {
		return 0
	}
	return uint16(int32(value))
}
func commandSinRadians(angle float64) float32 {
	return commandSineSamples[commandPhaseIndex(angle*(commandSinePeriod/(2*math.Pi)))]
}
func commandCosRadians(angle float64) float32 {
	return commandSineSamples[commandPhaseIndex(angle*(commandSinePeriod/(2*math.Pi))+commandSinePeriod/4)]
}

// Use the forward direction at the given yaw/tilt; a90-degree upward tilt
// gives the up direction. Their cross product supplies the left direction.
func commandForward(yaw, pitch float32) [3]float64 {
	heading := (yaw + 90) * commandRadiansPerDegree
	tilt := -pitch * commandRadiansPerDegree
	horizontal := commandCosRadians(float64(tilt))
	return [3]float64{float64(commandCosRadians(float64(heading)) * horizontal), float64(commandSinRadians(float64(tilt))), float64(commandSinRadians(float64(heading)) * horizontal)}
}
func commandLocalBasis(rotation [2]float32) (left, up, forward [3]float64) {
	forward = commandForward(rotation[0], rotation[1])
	up = commandForward(rotation[0], rotation[1]-90)
	left = [3]float64{up[1]*forward[2] - up[2]*forward[1], up[2]*forward[0] - up[0]*forward[2], up[0]*forward[1] - up[1]*forward[0]}
	return
}
