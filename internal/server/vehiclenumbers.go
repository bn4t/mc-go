package server

import "math"

// Position infinities retain the existing clamp/speed-check path. NaN
// coordinates and non-finite rotations invalidate the packet itself.
func invalidVehicleNumbers(x, y, z float64, yaw, pitch float32) bool {
	for _, coordinate := range [...]float64{x, y, z} {
		if math.IsNaN(coordinate) {
			return true
		}
	}
	for _, angle := range [...]float32{yaw, pitch} {
		value := float64(angle)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return true
		}
	}
	return false
}
