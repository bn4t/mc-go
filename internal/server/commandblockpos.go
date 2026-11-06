package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// commandSourceSnapshot returns the active command source for actor. Direct
// player commands have no explicit frame, so derive one through the same
// function-source path used by execute/function leaves: session position,
// view rotation and feet anchor.
func (s *Server) commandSourceSnapshot(actor string) *commandSourceFrame {
	if frame := s.commandFrame(actor); frame != nil {
		return frame
	}
	source := s.functionSource(actor)
	source.level = s.permissionLevel(actor)
	return source
}

// commandLocalBlockPos resolves a caret blockpos triple for a direct command.
// local is false when the triple has no caret token, leaving the caller's
// existing integer/~ parser in charge. The resolved point uses the command
// source's rotation and feet/eyes anchor, then floors to a block position.
func (s *Server) commandLocalBlockPos(actor string, toks []string, tokBase int) (pos world.Pos, bad *reply, local bool) {
	for k := 0; k < len(toks) && k < 3; k++ {
		if strings.HasPrefix(toks[k], "^") {
			local = true
			break
		}
	}
	if !local {
		return world.Pos{}, nil, false
	}
	if r := validateLocalTriple(toks, tokBase); r != nil {
		return world.Pos{}, r, true
	}
	source := s.commandSourceSnapshot(actor)
	origin := source.pos
	if source.anchorEyes {
		origin[1] += s.commandSourceEyeHeight(source.executor)
	}
	x, y, z := tpResolveSourceCoords(toks[:3], origin[0], origin[1], origin[2], source.rotation)
	return world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}, nil, true
}

// parseBlockPosCommand resolves a blockpos argument for a command that
// already has a Session-based integer/~ parser. Caret triples use the command
// source frame; ordinary syntax stays on parseBlockPos unchanged.
func (s *Server) parseBlockPosCommand(actor string, sender *Session, args []string, tokBase int) ([3]int, *reply) {
	if pos, r, local := s.commandLocalBlockPos(actor, args, tokBase); local {
		if r != nil {
			return [3]int{}, r
		}
		return [3]int{pos.X, pos.Y, pos.Z}, nil
	}
	return s.parseBlockPos(sender, args, tokBase)
}
