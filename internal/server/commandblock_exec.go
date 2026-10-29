package server

import "mc-go/internal/world"

func commandBlockStep(pos world.Pos, dir rsDir) world.Pos {
	off := rsOff[dir]
	return world.Pos{X: pos.X + off.X, Y: pos.Y + off.Y, Z: pos.Z + off.Z}
}

func (s *Server) queueCommandBlockTick(d *dimension, pos world.Pos, name string) {
	kind := tickCommandImpulse
	if name == "repeating_command_block" {
		kind = tickCommandRepeat
	} else if name != "command_block" {
		return
	}
	s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: kind}, s.TickCount()+1)
}

func (s *Server) commandBlockNeighborChanged(d *dimension, pos world.Pos, state world.Block) {
	g := s.commandBlockAt(d, pos)
	if g == nil {
		return
	}
	powered := rsHasNeighborSignal(d, pos)
	if powered == nbtTruth(g.fields["powered"]) {
		return
	}
	g.fields["powered"] = nbtBool(powered)
	name, _ := world.StateOwner(state)
	if powered && !nbtTruth(g.fields["auto"]) && name != "chain_command_block" {
		s.commandBlockSchedule(d, pos, g)
	}
}

// Repeating conditions are latched for the next tick before the previous
// latch is consumed. An already queued repeat runs once after power is lost.
func (s *Server) commandBlockTick(d *dimension, pos world.Pos, state world.Block) {
	g := s.commandBlockAt(d, pos)
	if g == nil {
		return
	}
	name, _ := world.StateOwner(state)
	condition := nbtTruth(g.fields["conditionMet"])
	if name == "repeating_command_block" {
		s.commandBlockMarkCondition(d, pos, g)
	}
	if condition {
		command, _ := g.fields["Command"].(string)
		if command != "" {
			s.performCommandBlock(d, pos, state, g)
		} else {
			g.fields["SuccessCount"] = int32(0)
		}
		facing, _ := world.StateProperty(state, "facing")
		s.commandBlockChain(d, pos, rsDirOfName[facing])
	} else if conditional, _ := world.StateProperty(state, "conditional"); conditional == "true" {
		g.fields["SuccessCount"] = int32(0)
	}
	if name == "repeating_command_block" && (nbtTruth(g.fields["powered"]) || nbtTruth(g.fields["auto"])) {
		// Keep the original expected type even if the command replaced
		// its own block while executing.
		s.queueCommandBlockTick(d, pos, name)
	}
	s.rsOutputSignalWake(d, pos)
}

func (s *Server) commandBlockChain(d *dimension, pos world.Pos, dir rsDir) {
	for remaining := s.gameRuleInt("minecraft:max_command_sequence_length"); remaining > 0; remaining-- {
		pos = commandBlockStep(pos, dir)
		state := d.w.GetBlock(pos)
		if !isBlockNamed(state, "chain_command_block") {
			return
		}
		g := s.commandBlockAt(d, pos)
		if nbtTruth(g.fields["powered"]) || nbtTruth(g.fields["auto"]) {
			if s.commandBlockMarkCondition(d, pos, g) {
				if !s.performCommandBlock(d, pos, state, g) {
					return
				}
				s.rsOutputSignalWake(d, pos)
			} else if conditional, _ := world.StateProperty(state, "conditional"); conditional == "true" {
				g.fields["SuccessCount"] = int32(0)
			}
		}
		facing, _ := world.StateProperty(state, "facing")
		dir = rsDirOfName[facing]
	}
}

func commandLong(value any, fallback int64) int64 {
	if n, ok := value.(int64); ok {
		return n
	}
	if n, ok := nbtNumber(value); ok {
		return int64(n)
	}
	return fallback
}

// CommandBlockEntity's source uses Vec2(0, Direction.toYRot()).
// Rotation here is stored as yaw,pitch, matching the player context.
func commandBlockRotation(facing string) [2]float32 {
	switch facing {
	case "north":
		return [2]float32{180, 0}
	case "east", "up", "down":
		// Both vertical directions have data2d=-1, so (&3)*90 is 270.
		return [2]float32{270, 0}
	case "west":
		return [2]float32{90, 0}
	}
	return [2]float32{}
}

func (s *Server) commandBlockUpdated(d *dimension, pos world.Pos) {
	s.broadcastBlockState(d, pos, d.w.GetBlock(pos))
}

// Command blocks have a centered, non-entity source; the common base
// dispatch is shared with command minecarts' entity-bound source.
func (s *Server) performCommandBlock(d *dimension, pos world.Pos, state world.Block, g *genericBlockEntity) bool {
	frame := commandSourceFrame{dim: d, pos: [3]float64{float64(pos.X) + 0.5, float64(pos.Y) + 0.5, float64(pos.Z) + 0.5}, level: 2, display: literal("@")}
	if name, ok := g.fields["CustomName"]; ok {
		frame.display = componentFromAny(name)
	}
	facing, _ := world.StateProperty(state, "facing")
	frame.rotation = commandBlockRotation(facing)
	return s.performCommandBase(func() map[string]any { return g.fields }, frame, func() { s.commandBlockUpdated(d, pos) })
}
