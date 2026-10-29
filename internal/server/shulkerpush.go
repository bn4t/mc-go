package server

import (
	"math"
	"mc-go/internal/world"
)

// The lid's entity sweep uses the full float progress, independently of
// its half-progress collision shape. Only the newly covered slab selects
// entities; each selected item moves by the slab thickness plus .01.
func shulkerOpeningSweep(st world.Block, before, after float32) (cellBox, int, float64) {
	var box cellBox
	if after <= before {
		return box, 0, 0
	}
	axis, sign := 1, 1.0
	facing, _ := world.StateProperty(st, "facing")
	switch facing {
	case "east":
		axis = 0
	case "west":
		axis, sign = 0, -1
	case "north":
		axis, sign = 2, -1
	case "south":
		axis = 2
	case "down":
		sign = -1
	}
	box = cellBox{0, 0, 0, 1, 1, 1}
	if sign > 0 {
		box[axis], box[axis+3] = 1+float64(before), 1+float64(after)
	} else {
		box[axis], box[axis+3] = -float64(after), -float64(before)
	}
	return box, axis, sign * (float64(after) - float64(before) + .01)
}

func (s *Server) shulkerPushItems(d *dimension, pos world.Pos, before, after float32) {
	slab, axis, delta := shulkerOpeningSweep(d.w.GetBlock(pos), before, after)
	if delta == 0 {
		return
	}
	for i, v := range []float64{float64(pos.X), float64(pos.Y), float64(pos.Z)} {
		slab[i] += v
		slab[i+3] += v
	}
	for _, e := range s.entities.All() {
		if e.Dim != d.name || e.Type != mustEntityType("item") || e.Dying || e.presenceOnly {
			continue
		}
		dims := entityBBDims(&e)
		half := dims[0] / 2
		box := cellBox{e.X - half, e.Y, e.Z - half, e.X + half, e.Y + dims[1], e.Z + half}
		if !(box[0] < slab[3] && box[3] > slab[0] && box[1] < slab[4] && box[4] > slab[1] && box[2] < slab[5] && box[5] > slab[2]) {
			continue
		}
		move := s.clipShulkerItemMove(d, box, axis, delta)
		s.entities.mu.Lock()
		if live := s.entities.byID[e.ID]; live != nil && !live.presenceOnly {
			switch axis {
			case 0:
				live.X += move
			case 1:
				live.Y += move
			case 2:
				live.Z += move
			}
			live.PosDirty = live.PosDirty || move != 0
		}
		s.entities.mu.Unlock()
	}
}

// This uniaxial mover resolves actual positioned block boxes. Existing
// overlaps do not trap an entity; only an approaching face limits travel.
func (s *Server) clipShulkerItemMove(d *dimension, box cellBox, axis int, delta float64) float64 {
	return s.clipBlockShapeMove(d, box, axis, delta, func(st world.Block, pos world.Pos) []cellBox { return s.worldCollisionShapeAt(d, st, pos) })
}

func (s *Server) clipBlockShapeMove(d *dimension, box cellBox, axis int, delta float64, shapeAt func(world.Block, world.Pos) []cellBox) float64 {
	region := box
	if delta < 0 {
		region[axis] += delta
	} else {
		region[axis+3] += delta
	}
	other1, other2 := (axis+1)%3, (axis+2)%3
	for x := int(math.Floor(region[0])) - 1; x <= int(math.Floor(region[3]))+1; x++ {
		for y := int(math.Floor(region[1])) - 1; y <= int(math.Floor(region[4]))+1; y++ {
			for z := int(math.Floor(region[2])) - 1; z <= int(math.Floor(region[5]))+1; z++ {
				p := world.Pos{X: x, Y: y, Z: z}
				for _, b := range shapeAt(d.w.GetBlock(p), p) {
					offsets := [3]float64{float64(x), float64(y), float64(z)}
					for i := 0; i < 3; i++ {
						b[i] += offsets[i]
						b[i+3] += offsets[i]
					}
					if box[other1] >= b[other1+3] || box[other1+3] <= b[other1] || box[other2] >= b[other2+3] || box[other2+3] <= b[other2] {
						continue
					}
					if delta > 0 && box[axis+3] <= b[axis]+1e-7 {
						delta = math.Min(delta, math.Max(0, b[axis]-box[axis+3]))
					}
					if delta < 0 && box[axis] >= b[axis+3]-1e-7 {
						delta = math.Max(delta, math.Min(0, b[axis+3]-box[axis]))
					}
				}
			}
		}
	}
	return delta
}
