package server

import "math"

// Public26.2 angular units and an independently synthesized IEEE reciprocal-
// root seed. The seed is identified by inverting a standard Newton refinement
// against twelve public fastInvSqrt observations (see the numerical tool).
const commandDegreesPerRadian = float32(0x1.ca5dcp5)
const commandReciprocalRootSeed = uint64(0x5fe6eb50c7b537aa)

func commandReciprocalRoot(value float64) float64 {
	first := math.Float64frombits(commandReciprocalRootSeed - (math.Float64bits(value) >> 1))
	return first * (1.5 - (.5*value)*first*first)
}

// Model the command angle through a normalized complex phase. Its nearest
// sine-grid reference plus a cubic inverse-sine residual reproduces the
// measured float-degree outputs without translating the native atan method.
func commandFacingRadians(y, x float64) float64 {
	squared := x*x + y*y
	if squared == 0 {
		return 0
	}
	ax, ay := math.Abs(x), math.Abs(y)
	small, big := math.Min(ax, ay), math.Max(ax, ay)
	inverse := commandReciprocalRoot(squared)
	reference := math.Asin(math.RoundToEven(small*inverse*256) / 256)
	phase := math.Atan2(small, big)
	residual := math.Sin(phase-reference) * (math.Sqrt(squared) * inverse)
	correction := residual + residual*residual*residual/6
	angle := reference + correction
	if ay > ax {
		angle = math.Pi/2 - angle
	}
	if x < 0 {
		angle = math.Pi - angle
	}
	if y < 0 {
		angle = -angle
	}
	return angle
}
func commandFacingRotation(origin, target [3]float64) [2]float32 {
	dx, dy, dz := target[0]-origin[0], target[1]-origin[1], target[2]-origin[2]
	yaw := float32(commandFacingRadians(dz, dx)*float64(commandDegreesPerRadian)) - 90
	pitch := float32(-commandFacingRadians(dy, math.Sqrt(dx*dx+dz*dz)) * float64(commandDegreesPerRadian))
	return [2]float32{tpWrapDeg(yaw), tpWrapDeg(pitch)}
}
func (s *Server) executeFacingRotation(ctx *executeContext, target [3]float64) [2]float32 {
	origin := ctx.pos
	if ctx.anchorEyes {
		origin[1] += s.commandSourceEyeHeight(ctx.ent)
	}
	return commandFacingRotation(origin, target)
}
func (s *Server) executeFacingEntities(ctx *executeContext, player string, args []string, tokBase int) reply {
	selected, err := s.selectDataEntities(s.findSessionByName(player), args[0], tokBase, false, ctx.dim)
	if err != nil {
		return *err
	}
	var merged reply
	if !ctx.alive {
		return merged
	}
	for _, entity := range selected {
		next := *ctx
		target := entity.position()
		if args[1] == "eyes" {
			target[1] += s.commandSourceEyeHeight(entity)
		}
		next.rotation = s.executeFacingRotation(&next, target)
		next.forked = true
		r := s.execTail(&next, player, args[2:], tokBase+2)
		if r.fail != nil {
			return r
		}
		mergeReplies(&merged, r)
	}
	return merged
}
