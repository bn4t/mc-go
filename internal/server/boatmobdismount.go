package server

import (
	"math"
	"mc-go/internal/world"
)

// boatMobEscape computes the horizontal clearance for an ordinary living
// rider. Width scaling and angle conversion retain the native float steps;
// the clearance and normalization then use doubles.
func boatMobEscape(boatWidth, riderWidth float64, yaw float32) (float64, float64) {
	boatDiagonal := float64(float32(boatWidth) * float32(math.Sqrt2))
	clearance := (boatDiagonal + float64(float32(riderWidth)) + float64(float32(1e-5))) / 2
	angle := float64(yaw * float32(math.Pi/180))
	x, z := -float64(mthSin(angle)), float64(mthCos(angle))
	largest := math.Max(math.Abs(x), math.Abs(z))
	return x * clearance / largest, z * clearance / largest
}

// boatMobDismountPosition searches the two escape-column floor candidates
// using the server's modeled full-block floors and the rider's own bounds.
func (s *Server) boatMobDismountPosition(boat, rider *Entity) [3]float64 {
	boatBounds, riderBounds := entityBBDims(boat), entityBBDims(rider)
	top := boat.Y + boatBounds[1]
	fallback := [3]float64{boat.X, top, boat.Z}
	w := s.dimWorld(boat.Dim)
	if w == nil {
		return fallback
	}
	dx, dz := boatMobEscape(boatBounds[0], riderBounds[0], rider.Yaw)
	x, z := boat.X+dx, boat.Z+dz
	floorY := int(math.Floor(top))
	column := world.Pos{X: int(math.Floor(x)), Y: floorY - 1, Z: int(math.Floor(z))}
	if name, ok := world.StateOwner(w.GetBlock(column)); ok && name == "water" {
		return fallback
	}
	for _, y := range [2]int{floorY, floorY - 1} {
		pos := world.Pos{X: column.X, Y: y, Z: column.Z}
		height, valid := s.minecartFloorHeight(w, pos)
		if !valid {
			continue
		}
		foot := float64(y) + height
		width, tall := float64(float32(riderBounds[0])), float64(float32(riderBounds[1]))
		if !s.entityBoxCollides(w, x, foot, z, width/2, tall) {
			return [3]float64{x, foot, z}
		}
	}
	return fallback
}

// dismountSpearBoatRider applies the measured boat transition. Other vehicle
// escape searches remain separate from this narrowly scoped consumer.
func (s *Server) dismountSpearBoatRider(rider *Entity) bool {
	if !rider.Living || rider.Riding == 0 || armorStandIsMarker(rider) {
		return false
	}
	riderType, valid := EntityTypeName(rider.Type)
	if !valid || riderType == "player" {
		return false
	}
	boat, ok := s.entities.GetPtr(rider.Riding)
	if !ok {
		return false
	}
	name, ok := EntityTypeName(boat.Type)
	if !ok || !isBoatEntityName(name) {
		return false
	}
	index := -1
	for i, id := range boat.Passengers {
		if id == rider.ID {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	position := s.boatMobDismountPosition(boat, rider)
	passengers := make([]int32, 0, len(boat.Passengers)-1)
	passengers = append(passengers, boat.Passengers[:index]...)
	passengers = append(passengers, boat.Passengers[index+1:]...)
	boat.Passengers = passengers
	rider.Riding = 0
	rider.BoardingCooldown = 60
	rider.WeldedPos = nil
	rider.X, rider.Y, rider.Z = position[0], position[1], position[2]
	rider.PosDirty = true
	rider.NeedsSync = true
	s.broadcastSetPassengers(boat)
	s.mobSyncFlush(rider)
	return true
}
