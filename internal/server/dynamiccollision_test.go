package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestShulkerCollisionAnimation(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.world.Stop)
	d := s.dims[dimOverworld]
	pos := world.Pos{Y: 160}
	st := mkBlockState(t, "shulker_box", map[string]string{"facing": "east"})
	d.w.SetBlock(pos, st)
	c := s.containerAt(d, pos)
	check := func(want float64) {
		t.Helper()
		got := s.worldCollisionShapeAt(d, st, pos)
		if len(got) != 1 || got[0][3] != want {
			t.Fatalf("shape=%v want maxX=%v", got, want)
		}
	}
	check(1)
	s.shulkerViewerChange(d, c, 1)
	check(1)
	s.containerTickStep()
	check(1 + float64(float32(.1))/2)
	s.shulkerViewerChange(d, c, 1)
	s.shulkerViewerChange(d, c, -1)
	for i := 0; i < 9; i++ {
		s.containerTickStep()
	}
	check(1.5)
	s.shulkerViewerChange(d, c, -1)
	check(1.5)
	for i := 0; i < 10; i++ {
		s.containerTickStep()
	}
	check(1)
}

func TestShulkerCollisionFacing(t *testing.T) {
	for _, tc := range []struct {
		dir   string
		axis  int
		value float64
	}{{"down", 1, -.25}, {"up", 4, 1.25}, {"north", 2, -.25}, {"south", 5, 1.25}, {"west", 0, -.25}, {"east", 3, 1.25}} {
		st := mkBlockState(t, "shulker_box", map[string]string{"facing": tc.dir})
		got := shulkerCollisionShape(st, .5)
		if got[0][tc.axis] != tc.value {
			t.Errorf("%s shape=%v", tc.dir, got)
		}
	}
}
