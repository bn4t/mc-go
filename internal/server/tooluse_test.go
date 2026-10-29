package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestToolResourceFacePlacement(t *testing.T) {
	cases := []struct {
		name            string
		face            int32
		axis            int
		normal, impulse float64
	}{
		{"down", 0, 1, 19.75, 0}, {"up", 1, 1, 21, .2},
		{"north", 2, 2, 29.875, -.1}, {"south", 3, 2, 31.125, .1},
		{"west", 4, 0, 9.875, -.1}, {"east", 5, 0, 11.125, .1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, d := randSim(t)
			s.spawnToolResourceFromFace(d, world.Pos{X: 10, Y: 20, Z: 30}, tc.face, ItemStack{ID: world.ItemIDs["hanging_roots"], Count: 1})
			all := s.entities.All()
			if len(all) != 1 {
				t.Fatalf("drops=%d", len(all))
			}
			e := all[0]
			position := [3]float64{e.X, e.Y, e.Z}
			velocity := [3]float64{e.MotionX, e.MotionY, e.MotionZ}
			if position[tc.axis] != tc.normal || math.Abs(velocity[tc.axis]-tc.impulse) > 1e-15 {
				t.Fatalf("normal position%v velocity%v", position, velocity)
			}
			lower := [3]float64{10.25, 20.125, 30.25}
			upper := [3]float64{10.75, 20.625, 30.75}
			for axis := 0; axis < 3; axis++ {
				if axis == tc.axis {
					continue
				}
				if position[axis] < lower[axis] || position[axis] >= upper[axis] {
					t.Fatalf("tangent position%v", position)
				}
				low, high := -.1, .1
				if axis == 1 {
					low = 0
				}
				if velocity[axis] < low || velocity[axis] >= high {
					t.Fatalf("tangent velocity%v", velocity)
				}
			}
			if e.PickupDelay != 10 || e.Item.ID != world.ItemIDs["hanging_roots"] || e.Item.Count != 1 {
				t.Fatalf("drop=%+v", e)
			}
			s.runCommandAt("RCON", 4, "gamerule", []string{"minecraft:block_drops", "false"})
			s.spawnToolResourceFromFace(d, world.Pos{X: 10, Y: 20, Z: 30}, tc.face, ItemStack{ID: world.ItemIDs["hanging_roots"], Count: 1})
			if len(s.entities.All()) != 1 {
				t.Fatal("block_drops=false spawned resource")
			}
		})
	}
}

func TestToolActionGameEventCenter(t *testing.T) {
	s, d := randSim(t)
	d.sculk = map[world.Pos]*sculkSensor{}
	pos, sensor := world.Pos{X: 10, Y: 20, Z: 30}, world.Pos{X: 14, Y: 20, Z: 30}
	sess := placeSession(t, s, "ToolEvent", 10.5, 21, 27.5)
	sess.setMode(GameModeSurvival)
	sess.setStack(36, ItemStack{ID: world.ItemIDs["diamond_axe"], Count: 1})
	d.w.SetBlock(pos, world.BlockStateMust("oak_log"))
	d.w.SetBlock(sensor, world.BlockStateMust("sculk_sensor"))
	if !s.useToolOnBlock(sess, d, pos, 1, 0) {
		t.Fatal("strip action rejected")
	}
	listener := d.sculk[sensor]
	if listener == nil || listener.sel == nil {
		t.Fatal("tool change emitted no vibration")
	}
	event := listener.sel
	if event.event != "block_change" || event.freq != 11 || event.x != 10.5 || event.y != 20.5 || event.z != 30.5 || event.dist != 4 {
		t.Fatalf("tool event=%+v", event)
	}
}
