package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestShulkerOpeningSweepFacts(t *testing.T) {
	for _, tc := range []struct {
		face string
		axis int
		sign float64
	}{{"east", 0, 1}, {"west", 0, -1}, {"up", 1, 1}, {"down", 1, -1}, {"south", 2, 1}, {"north", 2, -1}} {
		st := mkBlockState(t, "shulker_box", map[string]string{"facing": tc.face})
		before, after := float32(.1), float32(.2)
		b, axis, delta := shulkerOpeningSweep(st, before, after)
		if axis != tc.axis || math.Abs(delta-tc.sign*(float64(after)-float64(before)+.01)) > 1e-12 {
			t.Fatalf("%s %v %d %v", tc.face, b, axis, delta)
		}
		lo, hi := 1+float64(before), 1+float64(after)
		if tc.sign < 0 {
			lo, hi = -float64(after), -float64(before)
		}
		if b[axis] != lo || b[axis+3] != hi {
			t.Fatalf("%s full-progress slab %v", tc.face, b)
		}
		if _, _, d := shulkerOpeningSweep(st, after, before); d != 0 {
			t.Fatal("closing sweep")
		}
		if _, _, d := shulkerOpeningSweep(st, after, after); d != 0 {
			t.Fatal("stationary sweep")
		}
	}
}

func TestShulkerItemMoveShapeClipping(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 2, Y: 160, Z: 0}
	d.w.SetBlock(pos, mkBlockState(t, "stone_slab", map[string]string{"type": "bottom"}))
	box := cellBox{1.375, 160.6, .375, 1.625, 160.85, .625}
	if got := s.clipShulkerItemMove(d, box, 0, 1); got != 1 {
		t.Fatalf("clear above slab %v", got)
	}
	d.w.SetBlock(pos, mkBlockState(t, "stone", nil))
	if got := s.clipShulkerItemMove(d, box, 0, 1); got != .375 {
		t.Fatalf("full face %v", got)
	}
	// Existing overlap is allowed to escape rather than trapping the item.
	box[0], box[3] = 2.1, 2.35
	if got := s.clipShulkerItemMove(d, box, 0, 1); got != 1 {
		t.Fatalf("overlap escape %v", got)
	}
}
