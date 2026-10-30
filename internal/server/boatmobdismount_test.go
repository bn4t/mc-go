package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestBoatMobEscapeNativeVectors(t *testing.T) {
	for _, tc := range []struct {
		width float64
		yaw   float32
		x, z  float64
	}{
		{.6, 0, 0, 1.2722768119620014},
		{.6, 45, -1.2722768119620014, 1.2722768119620014},
		{.6, 90, -1.2722768119620014, 1.5580897933863563e-16},
		{.6, -90, 1.2722768119620014, 0},
		{.6, 180, -1.5580897933863563e-16, -1.2722768119620014},
		{.9, 0, 0, 1.4222767881201435},
		{.9, 45, -1.4222767881201437, 1.4222767881201437},
	} {
		x, z := boatMobEscape(1.375, tc.width, tc.yaw)
		if math.IsNaN(x) || math.IsNaN(z) || math.IsInf(x, 0) || math.IsInf(z, 0) || math.Abs(x-tc.x) > 1e-15 || math.Abs(z-tc.z) > 1e-15 {
			t.Fatalf("width=%g yaw=%g got=(%.17g,%.17g) want=(%.17g,%.17g)", tc.width, tc.yaw, x, z, tc.x, tc.z)
		}
	}
}

func TestBoatMobDismountTransitionAndFallbacks(t *testing.T) {
	s := simulationTestServer(t)
	w := s.dimWorld(dimOverworld)
	stone, _ := world.BlockState("stone")
	water, _ := world.BlockState("water")
	boat := s.entities.Spawn(&Entity{Type: mustEntityType("oak_boat"), Dim: dimOverworld, X: .5, Y: -60, Z: 4.5})
	rider := s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Living: true, Dim: dimOverworld, X: .5, Y: -59.8125, Z: 4.5, Riding: boat.ID})
	boat.Passengers = []int32{99, rider.ID, 100}
	w.SetBlock(world.Pos{X: 0, Y: -61, Z: 5}, stone)
	if !s.dismountSpearBoatRider(rider) {
		t.Fatal("transition rejected")
	}
	if rider.Riding != 0 || rider.BoardingCooldown != 60 || rider.WeldedPos != nil || len(boat.Passengers) != 2 || boat.Passengers[0] != 99 || boat.Passengers[1] != 100 {
		t.Fatal("passenger transition", rider.Riding, rider.BoardingCooldown, boat.Passengers)
	}
	want := [3]float64{.5, -60, 5.772276811962001}
	if got := [3]float64{rider.X, rider.Y, rider.Z}; got != want {
		t.Fatal(got, want)
	}
	if s.dismountSpearBoatRider(rider) {
		t.Fatal("detached rider accepted")
	}
	for _, blocked := range []bool{true, false} {
		if blocked {
			w.SetBlock(world.Pos{X: 0, Y: -59, Z: 5}, stone)
		} else {
			w.SetBlock(world.Pos{X: 0, Y: -59, Z: 5}, 0)
			w.SetBlock(world.Pos{X: 0, Y: -61, Z: 5}, water)
		}
		if got := s.boatMobDismountPosition(boat, rider); got != [3]float64{.5, -59.4375, 4.5} {
			t.Fatal("fallback", blocked, got)
		}
	}
}
