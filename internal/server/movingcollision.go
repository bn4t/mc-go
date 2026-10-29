package server

import "mc-go/internal/world"

// Each move has two half-block animation ticks and a later cleanup tick.
func movingPistonProgress(mp movingPiston) float32 {
	p := float32(3-mp.ticks) / 2
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// The carried shape is displaced from its destination opposite motion.
// A retracting source additionally retains the stationary extended base.
func movingPistonCollisionShape(mp movingPiston, progress float32) []cellBox {
	carried := mp.state
	var fixed []cellBox
	if mp.source {
		face := mp.dir
		if !mp.extend {
			face = rsOpposite(face)
		}
		short := mp.extend != (1-progress < .25)
		shortName := "false"
		if short {
			shortName = "true"
		}
		if head, ok := world.BlockStateOf("piston_head", map[string]string{"facing": rsDirName(face), "short": shortName}); ok {
			carried = head
		}
		if !mp.extend {
			if base, ok := world.WithProperty(mp.state, "extended", "true"); ok {
				fixed = measuredCollisionShapeAt(base, mp.pos)
			}
		}
	}
	boxes := measuredCollisionShapeAt(carried, mp.pos)
	out := make([]cellBox, 0, len(fixed)+len(boxes))
	out = append(out, fixed...)
	off := rsOff[mp.dir]
	shift := progress - 1
	dx, dy, dz := float64(shift*float32(off.X)), float64(shift*float32(off.Y)), float64(shift*float32(off.Z))
	for _, box := range boxes {
		box[0] += dx
		box[3] += dx
		box[1] += dy
		box[4] += dy
		box[2] += dz
		box[5] += dz
		out = append(out, box)
	}
	return out
}

func (s *Server) startPistonBaseRetraction(d *dimension, pos world.Pos, block world.Block, facing rsDir) {
	closed, ok := world.WithProperty(block, "extended", "false")
	if !ok {
		return
	}
	typ := "normal"
	if rsPistonSticky(block) {
		typ = "sticky"
	}
	moving, ok := world.BlockStateOf("moving_piston", map[string]string{"facing": rsDirName(facing), "type": typ})
	if !ok {
		d.w.SetBlock(pos, closed)
		return
	}
	d.w.SetBlock(pos, moving)
	s.movingPistons = append(s.movingPistons, movingPiston{dim: d.name, pos: pos, state: closed, dir: rsOpposite(facing), extend: false, source: true, ticks: 3})
}
