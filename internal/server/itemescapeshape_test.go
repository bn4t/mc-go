package server

import (
	"encoding/hex"
	"mc-go/internal/world"
	"os"
	"strings"
	"testing"
)

func TestCollisionFullCubeNativeCensus(t *testing.T) {
	raw, err := os.ReadFile("testdata/collision-full26.2.hex")
	if err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "#") {
			body.WriteString(strings.TrimSpace(line))
		}
	}
	expected, err := hex.DecodeString(body.String())
	if err != nil {
		t.Fatal(err)
	}
	if world.StateCount() != 32366 || len(expected) != (world.StateCount()+7)/8 {
		t.Fatal("native full-collision census coverage changed")
	}
	full := 0
	for id := 0; id < world.StateCount(); id++ {
		want := expected[id/8]&(1<<uint(id%8)) != 0
		if want {
			full++
		}
		if got := collisionShapeIsFullCube(measuredCollisionShapeAt(world.Block(id), world.Pos{})); got != want {
			name, _ := world.StateOwner(world.Block(id))
			t.Errorf("state%d %s full%t want%t", id, name, got, want)
		}
	}
	if full != 3287 {
		t.Fatalf("full state count%d", full)
	}
}

func TestCollisionFullCubeOccupiedUnion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		boxes []cellBox
		want  bool
	}{
		{"empty", nil, false},
		{"unit", []cellBox{{0, 0, 0, 1, 1, 1}}, true},
		{"bottom", []cellBox{{0, 0, 0, 1, .5, 1}}, false},
		{"halves", []cellBox{{0, 0, 0, .5, 1, 1}, {.5, 0, 0, 1, 1, 1}}, true},
		{"overlap", []cellBox{{0, 0, 0, .75, 1, 1}, {.25, 0, 0, 1, 1, 1}}, true},
		{"tinygap", []cellBox{{0, 0, 0, .5, 1, 1}, {.5 + 5e-8, 0, 0, 1, 1, 1}}, true},
		{"gap", []cellBox{{0, 0, 0, .5, 1, 1}, {.5 + 2e-7, 0, 0, 1, 1, 1}}, false},
		{"tinyoutside", []cellBox{{0, 0, 0, 1 + 5e-8, 1, 1}}, true},
		{"outside", []cellBox{{0, 0, 0, 1 + 2e-7, 1, 1}}, false},
		{"cubeplusoutside", []cellBox{{0, 0, 0, 1, 1, 1}, {1, 0, 0, 1.25, 1, 1}}, false},
		{"hollow", []cellBox{{0, 0, 0, .125, 1, 1}, {.875, 0, 0, 1, 1, 1}, {0, 0, 0, 1, .125, 1}, {0, .875, 0, 1, 1, 1}, {0, 0, 0, 1, 1, .125}, {0, 0, .875, 1, 1, 1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := collisionShapeIsFullCube(tc.boxes); got != tc.want {
				t.Fatalf("full%t want%t", got, tc.want)
			}
		})
	}
}

func TestItemEscapeEligibilityUsesLiveWorldShape(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	for _, p := range []world.Pos{{Y: 162, Z: -1}, {Y: 162, Z: 1}, {X: -1, Y: 162}, {X: 1, Y: 162}, {Y: 163}} {
		d.w.SetBlock(p, world.Stone)
	}
	north := world.Pos{Y: 162, Z: -1}
	check := func(name string, axis int, sign float64) {
		t.Helper()
		e := &Entity{X: .25, Y: 162.3, Z: .6}
		s.setEmbeddedItemEscapeVelocity(d, e, .25)
		motion := [3]float64{e.MotionX, e.MotionY, e.MotionZ}
		if sign*motion[axis] < .1 || sign*motion[axis] > .3 {
			t.Fatalf("%s motion%v", name, motion)
		}
		for i, v := range motion {
			if i != axis && v != 0 {
				t.Fatalf("%s off-axis motion%v", name, motion)
			}
		}
	}
	d.w.SetBlock(north, mkBlockState(t, "moving_piston", map[string]string{"facing": "east"}))
	check("orphan moving piston", 2, -1)
	s.movingPistons = []movingPiston{{dim: d.name, pos: north, state: world.Stone, dir: rsEast, extend: true, ticks: 1}}
	check("completed moving cube", 1, 1)
	s.movingPistons[0].ticks = 3
	check("offset moving cube", 2, -1)
	s.movingPistons = nil
	d.w.SetBlock(north, mkBlockState(t, "shulker_box", map[string]string{"facing": "north"}))
	c := s.containerAt(d, north)
	check("closed shulker", 1, 1)
	d.containers.mu.Lock()
	c.shulkerProgress = .5
	d.containers.mu.Unlock()
	check("open shulker", 2, -1)
	d.containers.mu.Lock()
	c.shulkerProgress = 1e-8
	d.containers.mu.Unlock()
	check("epsilon-sized lid", 1, 1)
}
