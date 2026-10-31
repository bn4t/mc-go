package server

import (
	"math"
	"mc-go/internal/world"
)

// mobSyncLivingFrame consumes the living dimension callback immediately
// before this entity's existing tracker flush; it never advances cadence twice.
func (s *Server) mobSyncLivingFrame(e *Entity) {
	s.refreshMobCollisionScale(e)
	s.mobSyncFlush(e)
}

// refreshMobCollisionScale runs outside NoAI after the entity's base frame.
// A command before its first frame is consumed after base-tick admission.
// Player dimensions retain their separate admitted player-frame path.
func (s *Server) refreshMobCollisionScale(e *Entity) {
	if !e.Living || e.Dying || e.dragon != nil || e.TickCount == 0 {
		return
	}
	name, _ := EntityTypeName(e.Type)
	if name == "player" {
		return
	}
	before := entityBBDims(e)
	scale := 1.0
	if v, ok := e.AttrBase["scale"]; ok {
		scale = v
	}
	nextScale := float32(sanitizeAttributeValue("scale", attrValue(scale, e.AttrMods["scale"])))
	oldScale := e.mobCollisionScale
	if oldScale == 0 {
		oldScale = 1
	}
	if nextScale == oldScale {
		return
	}
	e.mobCollisionScale = nextScale
	after := entityBBDims(e)
	for axis := range before {
		before[axis] = float64(float32(before[axis]))
		after[axis] = float64(float32(after[axis]))
	}
	// Vex holds noPhysics throughout its living super-tick; marker stands
	// also bypass collision. Both still consume scaled dimensions.
	noPhysics := name == "vex" || (name == "armor_stand" && e.ArmorStandMarker)
	if !mobResizeCanAdjust(before, after, false, noPhysics) {
		return
	}
	if w := s.dimWorld(e.Dim); w != nil {
		if p, ok := mobResizePosition(w, [3]float64{e.X, e.Y, e.Z}, before, after); ok {
			if p != [3]float64{e.X, e.Y, e.Z} {
				e.X, e.Y, e.Z = p[0], p[1], p[2]
				// Open the normal tracker gate without forcing a position resend:
				// sub-quantum adjustments must not emit a zero-delta packet.
				e.NeedsSync = true
			}
		}
	}
}

// Measured Entity.refreshDimensions guard facts. Construction-time refreshes
// do not move a first-tick entity; the scale callback above follows base tick.
func mobResizeCanAdjust(old, next [2]float64, firstTick, noPhysics bool) bool {
	return !firstTick && !noPhysics && next[0] <= 4 && next[1] <= 4 && (next[0] > old[0] || next[1] > old[1])
}

// mobResizePosition solves for the closest free body center, independently
// using axis-aligned box subtraction. Wire measurements pin the center anchor,
// float dimension arithmetic and the narrower-height fallback under solids.
// Static block shapes use the server's existing empty-context shape adapter.
func mobResizePosition(w *world.World, feet [3]float64, old, next [2]float64) ([3]float64, bool) {
	const margin = 1e-6
	anchor := [3]float64{feet[0], feet[1] + old[1]/2, feet[2]}
	reach := [3]float64{(math.Max(0, next[0]-old[0]) + margin) / 2, (math.Max(0, next[1]-old[1]) + margin) / 2, (math.Max(0, next[0]-old[0]) + margin) / 2}
	if center, ok := mobResizeFreeCenter(w, anchor, reach, next); ok {
		center[1] -= next[1] / 2
		return center, true
	}
	if next[0] > old[0] && next[1] > old[1] {
		reach[1] = margin / 2
		if center, ok := mobResizeFreeCenter(w, anchor, reach, [2]float64{next[0], old[1]}); ok {
			center[1] += -old[1]/2 + margin
			return center, true
		}
	}
	return feet, false
}

func mobResizeFreeCenter(w *world.World, anchor, reach [3]float64, dims [2]float64) ([3]float64, bool) {
	domain := aabb{anchor[0] - reach[0], anchor[1] - reach[1], anchor[2] - reach[2], anchor[0] + reach[0], anchor[1] + reach[1], anchor[2] + reach[2]}
	free := []aabb{domain}
	half := [3]float64{dims[0] / 2, dims[1] / 2, dims[0] / 2}
	// Scan a conservative support envelope. Only inflated boxes that intersect
	// the center domain can remove candidates; unrelated blocks are harmless.
	for x := int(math.Floor(domain[0]-dims[0])) - 1; x <= int(math.Floor(domain[3]+dims[0]))+1; x++ {
		for y := int(math.Floor(domain[1]-dims[1])) - 1; y <= int(math.Floor(domain[4]+dims[1]))+1; y++ {
			for z := int(math.Floor(domain[2]-dims[0])) - 1; z <= int(math.Floor(domain[5]+dims[0]))+1; z++ {
				pos := world.Pos{X: x, Y: y, Z: z}
				for _, local := range cellShapeAt(w.GetBlock(pos), pos) {
					obstacle := aabb{float64(x) + local[0] - half[0], float64(y) + local[1] - half[1], float64(z) + local[2] - half[2], float64(x) + local[3] + half[0], float64(y) + local[4] + half[1], float64(z) + local[5] + half[2]}
					remaining := make([]aabb, 0, len(free))
					for _, region := range free {
						remaining = append(remaining, subtractMobResizeBox(region, obstacle)...)
					}
					free = remaining
					if len(free) == 0 {
						return [3]float64{}, false
					}
				}
			}
		}
	}
	bestDistance := math.Inf(1)
	var best [3]float64
	for _, region := range free {
		var candidate [3]float64
		distance := 0.0
		for axis := range candidate {
			candidate[axis] = math.Max(region[axis], math.Min(region[axis+3], anchor[axis]))
			d := candidate[axis] - anchor[axis]
			distance += d * d
		}
		if distance < bestDistance {
			bestDistance = distance
			best = candidate
		}
	}
	return best, len(free) > 0
}

// Subtracting one solid box partitions a free box into disjoint slabs.
func subtractMobResizeBox(region, solid aabb) []aabb {
	if !region.intersects(solid) {
		return []aabb{region}
	}
	var out []aabb
	for axis := 0; axis < 3; axis++ {
		if region[axis] < solid[axis] {
			part := region
			part[axis+3] = solid[axis]
			out = append(out, part)
			region[axis] = solid[axis]
		}
		if region[axis+3] > solid[axis+3] {
			part := region
			part[axis] = solid[axis+3]
			out = append(out, part)
			region[axis+3] = solid[axis+3]
		}
	}
	return out
}
