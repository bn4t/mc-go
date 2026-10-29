package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestMovingPistonShapeFacts(t *testing.T) {
	stone := mkBlockState(t, "stone", nil)
	for _, tc := range []struct {
		dir  rsDir
		want cellBox
	}{{rsEast, cellBox{-.5, 0, 0, .5, 1, 1}}, {rsWest, cellBox{.5, 0, 0, 1.5, 1, 1}}, {rsUp, cellBox{0, -.5, 0, 1, .5, 1}}, {rsDown, cellBox{0, .5, 0, 1, 1.5, 1}}, {rsSouth, cellBox{0, 0, -.5, 1, 1, .5}}, {rsNorth, cellBox{0, 0, .5, 1, 1, 1.5}}} {
		got := movingPistonCollisionShape(movingPiston{state: stone, dir: tc.dir, extend: true}, .5)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("dir%d shape=%v want%v", tc.dir, got, tc.want)
		}
	}
	head := mkBlockState(t, "piston_head", map[string]string{"facing": "east"})
	got := movingPistonCollisionShape(movingPiston{state: head, dir: rsEast, extend: true, source: true}, .5)
	want := map[cellBox]bool{{.25, 0, 0, .5, 1, 1}: true, {-.5, .375, .375, .25, .625, .625}: true}
	if len(got) != len(want) {
		t.Fatalf("source head=%v", got)
	}
	for _, b := range got {
		if !want[b] {
			t.Errorf("unexpected source head box%v", b)
		}
	}
	base := mkBlockState(t, "piston", map[string]string{"facing": "east", "extended": "false"})
	boxes := movingPistonCollisionShape(movingPiston{state: base, dir: rsWest, source: true}, .5)
	inside := func(x, y, z float64) bool {
		for _, b := range boxes {
			if b[0] < x && x < b[3] && b[1] < y && y < b[4] && b[2] < z && z < b[5] {
				return true
			}
		}
		return false
	}
	for _, p := range []struct {
		x, y, z float64
		solid   bool
	}{{.1, .9, .1, true}, {1.4, .9, .9, true}, {1, .5, .5, true}, {1, .9, .9, false}, {.9, .9, .9, false}} {
		if inside(p.x, p.y, p.z) != p.solid {
			t.Errorf("source base point%v shape%v", p, boxes)
		}
	}
}

func TestMovingPistonLifetimeAndCleanup(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 2, Y: 160}
	moving := mkBlockState(t, "moving_piston", map[string]string{"facing": "east"})
	wet := mkBlockState(t, "stone_slab", map[string]string{"type": "bottom", "waterlogged": "true"})
	d.w.SetBlock(pos, moving)
	s.movingPistons = []movingPiston{{dim: d.name, pos: pos, state: wet, dir: rsEast, extend: true, ticks: 3}}
	for phase := 1; phase <= 2; phase++ {
		s.movingPistonStep()
		if d.w.GetBlock(pos) != moving || len(s.movingPistons) != 1 || movingPistonProgress(s.movingPistons[0]) != float32(phase)/2 {
			t.Fatalf("phase%d state%v moving%v", phase, d.w.GetBlock(pos), s.movingPistons)
		}
	}
	// An entity entering the fully-extended cell must not receive a third push.
	e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: d.name, X: 2.5, Y: 160, Z: .5})
	s.movingPistonStep()
	if len(s.movingPistons) != 0 || !propIs(d.w.GetBlock(pos), "waterlogged", "false") || e.X != 2.5 {
		t.Fatalf("cleanup state%v moving%v entityX%v", d.w.GetBlock(pos), s.movingPistons, e.X)
	}
	// Replacing the moving cell removes its pending settlement.
	emerald := mkBlockState(t, "emerald_block", nil)
	d.w.SetBlock(pos, emerald)
	s.movingPistons = []movingPiston{{dim: d.name, pos: pos, state: wet, dir: rsEast, extend: true, ticks: 1}}
	s.movingPistonStep()
	if d.w.GetBlock(pos) != emerald || len(s.movingPistons) != 0 {
		t.Fatal("removed moving block overwrote its replacement")
	}
}

func TestMovingPistonWorldLookup(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	a, b := s.dims[dimOverworld], s.dims[dimNether]
	pos := world.Pos{Y: 100}
	moving := mkBlockState(t, "moving_piston", nil)
	stone := mkBlockState(t, "stone", nil)
	slab := mkBlockState(t, "stone_slab", map[string]string{"type": "bottom"})
	a.w.SetBlock(pos, moving)
	b.w.SetBlock(pos, moving)
	s.movingPistons = []movingPiston{{dim: b.name, pos: pos, state: stone, dir: rsEast, extend: true, ticks: 2}, {dim: a.name, pos: pos, state: slab, dir: rsEast, extend: true, ticks: 2}}
	if got := s.worldCollisionShapeAt(a, moving, pos); len(got) != 1 || got[0][4] != .5 {
		t.Fatalf("overworld shape%v", got)
	}
	if got := s.worldCollisionShapeAt(b, moving, pos); len(got) != 1 || got[0][4] != 1 {
		t.Fatalf("nether shape%v", got)
	}
	s.movingPistons[1].extend = false
	if s.movingPistonAt(a.name, pos, rsEast, true) || !s.movingPistonAt(b.name, pos, rsEast, true) {
		t.Fatal("piston motion lookup crossed dimensions")
	}
	s.movingPistons = nil
	if got := s.worldCollisionShapeAt(a, moving, pos); len(got) != 0 {
		t.Fatalf("orphan shape%v", got)
	}
}

// The head is itself a destination: clearing pushed origins must never
// erase the head that replaced the nearest pushed block.
func TestMovingPistonPushedSourceHeadSurvives(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	stone := mkBlockState(t, "stone", nil)
	power := mkBlockState(t, "redstone_block", nil)
	for i, facing := range []rsDir{rsDown, rsUp, rsNorth, rsSouth, rsWest, rsEast} {
		pos := world.Pos{X: i * 8, Y: 160}
		front := rsShiftPos(pos, facing)
		dest := rsShiftPos(front, facing)
		base := mkBlockState(t, "piston", map[string]string{"facing": rsDirName(facing)})
		d.w.SetBlock(pos, base)
		d.w.SetBlock(front, stone)
		powerDir := rsEast
		if facing == rsEast || facing == rsWest {
			powerDir = rsNorth
		}
		d.w.SetBlock(rsShiftPos(pos, powerDir), power)
		if !s.rsPistonExtend(d, pos, base, facing) {
			t.Fatalf("dir%d extension failed", facing)
		}
		check := func(phase int) {
			t.Helper()
			if !isBlockNamed(d.w.GetBlock(front), "moving_piston") || !isBlockNamed(d.w.GetBlock(dest), "moving_piston") {
				t.Fatalf("dir%d phase%d head%v carried%v", facing, phase, d.w.GetBlock(front), d.w.GetBlock(dest))
			}
		}
		check(0)
		for phase := 1; phase <= 2; phase++ {
			s.movingPistonStep()
			check(phase)
		}
		s.movingPistonStep()
		if !isBlockNamed(d.w.GetBlock(front), "piston_head") || !propIs(d.w.GetBlock(front), "facing", rsDirName(facing)) || d.w.GetBlock(dest) != stone {
			t.Fatalf("dir%d settlement head%v carried%v", facing, d.w.GetBlock(front), d.w.GetBlock(dest))
		}
	}
}
