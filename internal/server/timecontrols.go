package server

import (
	"math"
	"strconv"
	"strings"
)

type clockCommandTarget struct {
	id   int32
	name string
}

func (s *Server) commandClock(player string, explicit *clockCommandTarget) (clockCommandTarget, bool) {
	if explicit != nil {
		return *explicit, true
	}
	name := dimOverworld
	if d := s.dimOf(player); d != nil {
		name = d.name
	}
	id, ok := worldClockID(name)
	return clockCommandTarget{id, name}, ok
}
func clockCommandResult(r reply, value int) reply { r.result = value; r.hasResult = true; return r }
func clockWrappedResult(value int64) int          { return int(value % math.MaxInt32) }
func clockReplyOffset(r reply, offset int) reply {
	if r.fail != nil && !r.fail.hasAbs {
		f := *r.fail
		f.token += offset
		r.fail = &f
	}
	return r
}
func (s *Server) runExplicitClock(player string, args []string) reply {
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", 1)
	}
	name, valid := jigsawIdentifier(args[1])
	id, known := worldClockID(name)
	if !valid || !known {
		if !strings.Contains(args[1], ":") {
			name = "minecraft:" + args[1]
		} else {
			name = args[1]
		}
		return errTokenEnd("argument.resource.not_found", 2, name, "minecraft:world_clock")
	}
	target := clockCommandTarget{id, name}
	if len(args) == 2 {
		return errTokenEnd("command.unknown.command", 2)
	}
	return clockReplyOffset(s.runClockAction(player, args[2:], &target), 2)
}
func (s *Server) runClockAction(player string, args []string, target *clockCommandTarget) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "set":
		return s.runTimeSetForClock(player, args[1:], false, target)
	case "add":
		return s.runTimeSetForClock(player, args[1:], true, target)
	case "query":
		return s.runClockQuery(player, args[1:], target)
	case "pause", "resume":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		clock, ok := s.commandClock(player, target)
		if !ok {
			return componentReply(commandError("commands.time.no_default_clock", clock.name))
		}
		s.clock.mu.Lock()
		state := &s.clock.dayState
		if clock.id == 1 {
			state = &s.clock.endState
		}
		state.paused = args[0] == "pause"
		s.clock.mu.Unlock()
		s.broadcastUpdateTimeBody(s.worldClockSyncBody([]int32{clock.id}))
		return clockCommandResult(opsEchoReply(translatable("commands.time."+args[0], clock.name)), 1)
	case "rate":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 1)
		}
		rate, parseErr := parseClockRate(args[1])
		if parseErr != nil {
			return *parseErr
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		clock, ok := s.commandClock(player, target)
		if !ok {
			return componentReply(commandError("commands.time.no_default_clock", clock.name))
		}
		s.clock.mu.Lock()
		state := &s.clock.dayState
		if clock.id == 1 {
			state = &s.clock.endState
		}
		state.rate = rate
		s.clock.mu.Unlock()
		s.broadcastUpdateTimeBody(s.worldClockSyncBody([]int32{clock.id}))
		return clockCommandResult(opsEchoReply(translatable("commands.time.rate", dimCompound(clock.name), nestedCompound{key: "", value: rate})), 1)
	}
	return errToken("command.unknown.argument", 1)
}

func parseClockRate(arg string) (float32, *reply) {
	// Brigadier decimal numbers consume digits, minus and decimal point;
	// an exponent or other suffix remains an unconsumed command fragment.
	n := 0
	for n < len(arg) && (arg[n] >= '0' && arg[n] <= '9' || arg[n] == '-' || arg[n] == '.') {
		n++
	}
	if n == 0 {
		r := errToken("parsing.float.expected", 2)
		return 0, &r
	}
	value, err := strconv.ParseFloat(arg[:n], 32)
	if err != nil {
		if num, ok := err.(*strconv.NumError); !ok || num.Err != strconv.ErrRange {
			r := errToken("parsing.float.invalid", 2, arg[:n])
			return 0, &r
		}
	}
	rate := float32(value)
	if rate < float32(.00001) {
		r := errToken("argument.float.low", 2, float32(.00001), rate)
		return 0, &r
	}
	if rate > 1000 {
		r := errToken("argument.float.big", 2, float32(1000), rate)
		return 0, &r
	}
	if n < len(arg) {
		r := errTokenOff("command.unknown.argument", 2, n)
		return 0, &r
	}
	return rate, nil
}

// Builtin timeline metadata is factual data from the bundled26.2 resources.
// Custom clock/timeline resource overlays remain outside this command slice.
func builtinTimelinePeriod(name string) (int64, bool) {
	switch name {
	case "minecraft:day", "minecraft:villager_schedule":
		return 24000, true
	case "minecraft:moon":
		return 192000, true
	case "minecraft:early_game":
		return 0, true
	}
	return 0, false
}
func (s *Server) runClockQuery(player string, args []string, target *clockCommandTarget) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 1)
	}
	if target == nil && args[0] == "gametime" {
		if len(args) > 1 {
			return errToken("command.unknown.argument", 3)
		}
		age := s.gameTime()
		return clockCommandResult(componentReply(translatable("commands.time.query.gametime", age)), clockWrappedResult(age))
	}
	absolute := args[0] == "time"
	name := args[0]
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	period, known := builtinTimelinePeriod(name)
	if !absolute && !known {
		return errTokenEnd("argument.resource.not_found", 2, name, "minecraft:timeline")
	}
	repetition := len(args) > 1 && !absolute && args[1] == "repetition"
	if len(args) > 1 && !repetition {
		return errToken("command.unknown.argument", 3)
	}
	if len(args) > 2 {
		return errToken("command.unknown.argument", 4)
	}
	clock, ok := s.commandClock(player, target)
	if !ok {
		return componentReply(commandError("commands.time.no_default_clock", clock.name))
	}
	total := s.worldClockTotal(clock.id)
	if absolute {
		return clockCommandResult(componentReply(translatable("commands.time.query.absolute", dimCompound(clock.name), nestedCompound{key: "", value: total})), clockWrappedResult(total))
	}
	if clock.id != 0 {
		return componentReply(commandError("commands.time.wrong_timeline_for_clock", name, clock.name))
	}
	key := "commands.time.query.timeline"
	value := total
	if repetition {
		key += ".repetitions"
		value = 0
		if period != 0 {
			value = int64(int32(total / period))
		}
	} else if period != 0 {
		value = total % period
	}
	return clockCommandResult(componentReply(translatable(key, dimCompound(name), nestedCompound{key: "", value: value})), clockWrappedResult(value))
}

func (s *Server) runTimeSetForClock(player string, args []string, add bool, target *clockCommandTarget) reply {
	if len(args) != 1 {
		// "/time set" with no value: the parse stops after "set".
		return errTokenEnd("command.unknown.command", 1)
	}
	arg := args[0]
	dim := s.dimOf(player).name
	id, hasClock := worldClockID(dim)
	if target != nil {
		id, dim, hasClock = target.id, target.name, true
	}
	ticks, problem, number := functionScheduleTime(arg)
	if problem != "" || !add && ticks < 0 {
		if add {
			switch problem {
			case "parsing.float.invalid":
				return errToken(problem, 2, number)
			case "argument.time.invalid_unit":
				return errTokenEnd(problem, 2)
			default:
				return errToken("parsing.float.expected", 2)
			}
		}
		if !hasClock {
			return componentReply(commandError("commands.time.no_default_clock", dim))
		}
		// Identifier arguments can consume an empty prefix. A leading
		// character outside that alphabet leaves an unknown argument.
		if n := timeMarkerPrefix(arg); n < len(arg) {
			return errTokenOff("command.unknown.argument", 2, n)
		}
		marker, valid := jigsawIdentifier(arg)
		if !valid {
			marker = arg
			if !strings.Contains(marker, ":") {
				marker = "minecraft:" + marker
			}
		}
		value, ok := builtinTimeMarker(marker)
		if !ok || id != 0 {
			// Builtin day markers belong to the Overworld clock.
			// Vanilla names the missing marker by its registry key. The
			// marker lookup happens after parsing, so this failure has no
			// context message of its own.
			s.broadcastUpdateTimeBody(s.worldClockSyncBody([]int32{id}))
			return componentReply(commandError("commands.time.no_time_marker_found",
				"ResourceKey[minecraft:clock_time_marker / "+marker+"]", dim))
		}
		s.setWorldClockTotal(id, nextBuiltinTimeMarker(s.worldClockTotal(id), value))
		return clockCommandResult(opsEchoReply(translatable("commands.time.set.time_marker", dim, marker)), clockWrappedResult(s.worldClockTotal(id)))
	}
	if !hasClock {
		return componentReply(commandError("commands.time.no_default_clock", dim))
	}
	// The shared TimeArgument parser applies float32 unit scaling before
	// rounding and saturating to the command's integer tick count.
	value := int64(ticks)
	if add {
		// The argument is an int, so it saturates before it is added,
		// and the sum is reported as a long: it is a sum with the
		// world's existing clock.
		s.addWorldClockTicks(id, saturateInt(value))
		return clockCommandResult(opsEchoReply(translatable("commands.time.set.absolute",
			dimCompound(dim), nestedCompound{key: "", value: s.worldClockTotal(id)})), clockWrappedResult(s.worldClockTotal(id)))
	}
	// The tick count is an int, and the real server saturates rather
	// than wrapping: "/time set 3000000000" sets 2147483647.
	s.setWorldClockTotal(id, saturateInt(value))
	return clockCommandResult(opsEchoReply(translatable("commands.time.set.absolute",
		dimCompound(dim), nestedCompound{key: "", value: int32(saturateInt(value))})), int(saturateInt(value)))
}
