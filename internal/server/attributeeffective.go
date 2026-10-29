package server

import "math"

// sanitizeAttributeValue covers the four measured ranged attributes used by
// player combat/scale/luck. Stored bases and modifier amounts remain raw.
// Other attributes retain their current behavior pending registry metadata.
func sanitizeAttributeValue(name string, value float64) float64 {
	var low, high float64
	switch name {
	case "attack_damage":
		low, high = 0, 2048
	case "attack_speed":
		low, high = 0, 1024
	case "scale":
		low, high = .0625, 16
	case "luck":
		low, high = -1024, 1024
	default:
		return value
	}
	if math.IsNaN(value) {
		return low
	}
	return max(low, min(high, value))
}
