package types

// Location represents a position in the world
type Location struct {
	World      string
	X, Y, Z    float64
	Yaw, Pitch float32
}

// ToBlockLocation converts to block coordinates
func (l Location) ToBlockLocation() BlockLocation {
	return BlockLocation{
		X:     int32(l.X),
		Y:     int32(l.Y),
		Z:     int32(l.Z),
		World: l.World,
	}
}

// Distance calculates the distance to another location
func (l Location) Distance(other Location) float64 {
	dx := l.X - other.X
	dy := l.Y - other.Y
	dz := l.Z - other.Z
	return dx*dx + dy*dy + dz*dz
}

// ChunkX returns the chunk X coordinate
func (l Location) ChunkX() int32 {
	return int32(l.X) >> 4
}

// ChunkZ returns the chunk Z coordinate
func (l Location) ChunkZ() int32 {
	return int32(l.Z) >> 4
}