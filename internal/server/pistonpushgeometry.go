package server

import (
	"math"
	"mc-go/internal/world"
)

// Swept forward faces select contacts; the greatest penetration of those
// faces determines one bounded move, independently of the box decomposition.
func pistonForwardContact(body cellBox, boxes []cellBox, axis int, sign, step float64) float64 {
	amount := 0.0
	for _, b := range boxes {
		if sign > 0 {
			b[axis] = b[axis+3]
			b[axis+3] += step
		} else {
			b[axis+3] = b[axis]
			b[axis] -= step
		}
		if !pistonBoxesIntersect(body, b) {
			continue
		}
		penetration := b[axis+3] - body[axis]
		if sign < 0 {
			penetration = body[axis+3] - b[axis]
		}
		amount = math.Max(amount, math.Min(step, penetration))
	}
	return amount
}

func pistonBoxesIntersect(a, b cellBox) bool {
	return a[0] < b[3] && a[3] > b[0] && a[1] < b[4] && a[4] > b[1] && a[2] < b[5] && a[5] > b[2]
}

func pistonMotionAxis(dir rsDir) (axis int, sign float64) {
	off := rsOff[dir]
	axis, sign = 0, 1
	if off.Y != 0 {
		axis = 1
	} else if off.Z != 0 {
		axis = 2
	}
	if []int{off.X, off.Y, off.Z}[axis] < 0 {
		sign = -1
	}
	return
}

func pistonPositionCarriedBoxes(mp movingPiston, boxes []cellBox, progress float32) []cellBox {
	off := rsOff[mp.dir]
	shift := [3]float64{float64(mp.pos.X) + float64(float32(off.X)*(progress-1)), float64(mp.pos.Y) + float64(float32(off.Y)*(progress-1)), float64(mp.pos.Z) + float64(float32(off.Z)*(progress-1))}
	positioned := make([]cellBox, len(boxes))
	for j, b := range boxes {
		for i := 0; i < 3; i++ {
			b[i] += shift[i]
			b[i+3] += shift[i]
		}
		positioned[j] = b
	}
	return positioned
}

// Backoff is bounded by the half-step and the distance out of the base.
// The rear face must remain on the base's interior side within .01: an
// entity already escaping behind the base is not pulled back through it.
func pistonBaseBackoff(body, base cellBox, axis int, motionSign, step float64) float64 {
	if !pistonBoxesIntersect(body, base) {
		return 0
	}
	rearCut, clearance := math.Max(0, base[axis]-body[axis]), base[axis+3]-body[axis]
	if motionSign > 0 {
		rearCut, clearance = math.Max(0, body[axis+3]-base[axis+3]), body[axis+3]-base[axis]
	}
	if rearCut >= .01 {
		return 0
	}
	return -motionSign * math.Min(step+.01, clearance+.02)
}

// The source base's physical head state is distinct from its world
// collision state: the carried rod shortens after old progress .25.
func pistonSourceBaseCarriedBoxes(mp movingPiston, progress float32) []cellBox {
	short := "false"
	if progress > .25 {
		short = "true"
	}
	typ := "normal"
	if rsPistonSticky(mp.state) {
		typ = "sticky"
	}
	head, ok := world.BlockStateOf("piston_head", map[string]string{"facing": rsDirName(rsOpposite(mp.dir)), "short": short, "type": typ})
	if !ok {
		return nil
	}
	return pistonPositionCarriedBoxes(mp, measuredCollisionShapeAt(head, mp.pos), progress)
}
