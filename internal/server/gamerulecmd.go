package server

import (
	"strconv"
	"strings"
)

// registerGameRuleCommand wires /gamerule.
func (s *Server) registerGameRuleCommand() {
	s.registerComponentCommand("gamerule", func(player string, args []string) reply {
		return s.runGameRule(player, args)
	})
}

// runGameRule implements /gamerule <rule> [value]: bare names and
// minecraft:-prefixed names both resolve, bool rules take true/false,
// int rules take non-negative int32s, and the replies echo the bare
// rule name with the value as a string argument.
func (s *Server) runGameRule(player string, args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	name := strings.TrimPrefix(args[0], "minecraft:")
	key := "minecraft:" + name
	s.rulesMu.RLock()
	cur, ok := s.gameRules[key]
	s.rulesMu.RUnlock()
	if !ok {
		return errToken("command.unknown.argument", 1)
	}
	if len(args) == 1 {
		return componentReply(translatable("commands.gamerule.query", name, cur))
	}
	if len(args) > 2 {
		return errToken("command.unknown.argument", 3)
	}
	val := args[1]
	var r *reply
	if cur == "true" || cur == "false" {
		if val != "true" && val != "false" {
			r2 := errToken("parsing.bool.invalid", 2, val)
			r = &r2
		}
	} else {
		n, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			var r2 reply
			if _, perr := strconv.ParseInt(val, 10, 64); perr == nil {
				r2 = errToken("parsing.int.invalid", 2, val)
			} else {
				r2 = errToken("parsing.int.expected", 2)
			}
			r = &r2
		} else if n < 0 {
			r2 := errToken("argument.integer.low", 2, int32(0), int32(n))
			r = &r2
		}
	}
	if r != nil {
		return *r
	}
	s.rulesMu.Lock()
	s.gameRules[key] = val
	s.rulesMu.Unlock()
	return componentReply(translatable("commands.gamerule.set", name, val))
}
