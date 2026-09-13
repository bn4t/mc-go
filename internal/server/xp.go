package server

import (
	"strconv"
)

// Minimal /xp: set <targets> <amount> levels|points. This exists so
// blackbox setups (and operators) can grant exact levels; the reply
// text is a placeholder until measured against vanilla (TODO: xp reply
// parity, query/add forms, points-vs-levels clamping).
func (s *Server) runXp(player string, args []string) reply {
	sess := s.findSessionByName(player)
	if sess == nil || sess.opLevel() < 2 {
		return reply{comp: unknownCommand(), styled: true, fail: &failMark{}}
	}
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	if args[0] != "set" {
		return errToken("command.unknown.argument", 1)
	}
	if len(args) < 4 {
		return errTokenEnd("command.unknown.argument", len(args))
	}
	target := s.resolveTarget(player, args[1])
	if target == nil {
		return componentReply(commandError("argument.entity.notfound.player"))
	}
	amount, err := strconv.Atoi(args[2])
	if err != nil || amount < 0 {
		// TODO(xp parity): measure vanilla's bad-amount reply.
		return errToken("argument.integer.invalid", 3, args[2])
	}
	switch args[3] {
	case "levels":
		target.xpLevel = int32(amount)
	case "points":
		target.xpTotal += int32(amount)
		level, progress := experienceLevel(target.xpTotal)
		target.xpLevel = level
		target.xpProgress = progress
	default:
		return errToken("command.unknown.argument", 4)
	}
	if len(args) > 4 {
		return errToken("command.unknown.argument", 5)
	}
	_ = s.sendSetExperience(target, target.xpProgress, target.xpLevel, target.xpTotal)
	// TODO(xp parity): the success component key is unmeasured.
	return componentReply(translatable("commands.xp.success", int32(amount)))
}
