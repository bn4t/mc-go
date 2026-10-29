package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestBonemealDoublePlantLimits(t *testing.T) {
	s, d := randSim(t)
	pos := world.Pos{X: 30, Y: d.minY + d.height - 1, Z: 30}
	randPut(t, d, pos.X, pos.Y-1, pos.Z, "dirt", nil)
	randPut(t, d, pos.X, pos.Y, pos.Z, "short_grass", nil)
	if s.bonemealGrow(d, pos) {
		t.Fatal("grass grew beyond build height")
	}
	pos.Y = -60
	randPut(t, d, pos.X, pos.Y-1, pos.Z, "stone", nil)
	randPut(t, d, pos.X, pos.Y, pos.Z, "fern", nil)
	if s.bonemealGrow(d, pos) {
		t.Fatal("fern grew without valid soil")
	}
	randPut(t, d, pos.X, pos.Y-1, pos.Z, "farmland", nil)
	if !s.bonemealGrow(d, pos) {
		t.Fatal("fern failed to grow on valid soil")
	}
	if randName(d, pos.X, pos.Y, pos.Z) != "large_fern" || randName(d, pos.X, pos.Y+1, pos.Z) != "large_fern" {
		t.Fatal("fern did not produce both halves")
	}
}
