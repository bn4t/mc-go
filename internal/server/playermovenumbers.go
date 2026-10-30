package server

// Java movement validation substitutes zero for fields absent from this
// packet. Applying an accepted packet still retains the previous pose for
// those fields. Both movement families share the numeric predicate.
func invalidPlayerPacketNumbers(hasPosition, hasRotation bool, x, y, z float64, yaw, pitch float32) bool {
	if !hasPosition {
		x, y, z = 0, 0, 0
	}
	if !hasRotation {
		yaw, pitch = 0, 0
	}
	return invalidVehicleNumbers(x, y, z, yaw, pitch)
}
