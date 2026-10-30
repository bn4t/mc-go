package server

import "math"

// Wire-measured mace wave constants; the direction threshold is also the
// public Vec3.normalize branch constant in Java 26.2.
const (
	maceWaveStrength         = float64(float32(.7))
	maceWaveDirectionEpsilon = float64(float32(1e-5))
)

// maceWaveMotion adds a smash impulse to an existing velocity. Distance
// attenuates the horizontal push, while an affected target gets a fixed lift.
func maceWaveMotion(motion, offset [3]float64, resistance float64, heavy bool) ([3]float64, bool) {
	distance := math.Sqrt(offset[0]*offset[0] + offset[1]*offset[1] + offset[2]*offset[2])
	power := (3.5 - distance) * maceWaveStrength * (1 - resistance)
	if heavy {
		power *= 2
	}
	if !(power > 0) {
		return motion, false
	}
	if distance >= maceWaveDirectionEpsilon {
		motion[0] += offset[0] / distance * power
		motion[2] += offset[2] / distance * power
	}
	motion[1] += maceWaveStrength
	return motion, true
}
