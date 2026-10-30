package server

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// The queue belongs to the simulation owner. Its identity is the function
// name plus absolute world age; the callback target is independent.
// Insertion order breaks equal-age ties.
type scheduledFunction struct {
	name         string
	target       string
	callbackType string
	age          int64
	serial       uint64
}

func (s *Server) clearFunctionSchedule(name string) int {
	n := 0
	kept := s.functionSchedule[:0]
	for _, event := range s.functionSchedule {
		if event.name == name {
			n++
		} else {
			kept = append(kept, event)
		}
	}
	s.functionSchedule = kept
	return n
}

func (s *Server) runFunctionSchedule(args []string) reply {
	if len(args) == 0 {
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "clear":
		if len(args) < 2 {
			return errTokenEnd("command.unknown.command", 2)
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		// Clear's name is the scheduled event's exact key, rather than a
		// newly resolved function argument.
		n := s.clearFunctionSchedule(args[1])
		if n == 0 {
			return componentReply(commandError("commands.schedule.cleared.failure", args[1]))
		}
		r := opsEchoReply(translatable("commands.schedule.cleared.success", int32(n), args[1]))
		r.result = n
		r.hasResult = true
		r.outcome = commandSucceeded
		return r
	case "function":
		if len(args) < 3 {
			return errTokenEnd("command.unknown.command", len(args)+1)
		}
		isTag := strings.HasPrefix(args[1], "#")
		name := args[1]
		if isTag {
			name = name[1:]
		}
		id, ok := jigsawIdentifier(name)
		if !ok {
			return errToken("argument.id.invalid", 2)
		}
		end := timeArgumentTokenEnd(args[2])
		ticks, problem, number := functionScheduleTime(args[2][:end])
		switch problem {
		case "parsing.float.expected":
			return errToken(problem, 3)
		case "parsing.float.invalid":
			return errToken(problem, 3, number)
		case "argument.time.invalid_unit":
			return errTokenOff(problem, 3, end)
		}
		if ticks < 0 {
			return errTokenOff("argument.time.tick_count_too_low", 3, end, int32(0), ticks)
		}
		if end < len(args[2]) {
			return errTokenOff("command.expected.separator", 3, end)
		}
		replace := true
		if len(args) > 3 {
			switch args[3] {
			case "append":
				replace = false
			case "replace":
			default:
				return errToken("command.unknown.argument", 4)
			}
			if len(args) > 4 {
				return errToken("command.unknown.argument", 5)
			}
		}
		// Function resolution is an execution failure: complete argument
		// syntax must be validated before consulting the loaded registry.
		if _, ok := s.functions[id]; !isTag && !ok {
			return componentReply(commandError("arguments.function.unknown", id))
		}
		if ticks == 0 {
			return componentReply(commandError("commands.schedule.same_tick"))
		}
		eventName := id
		kind := functionScheduleCallback
		feedback := "commands.schedule.created.function"
		if isTag {
			eventName = "#" + id
			kind = "minecraft:function_tag"
			feedback = "commands.schedule.created.tag"
		}
		if replace {
			s.clearFunctionSchedule(eventName)
		}
		age := s.gameTime() + int64(ticks)
		found := false
		for _, event := range s.functionSchedule {
			if event.name == eventName && event.age == age {
				found = true
				break
			}
		}
		if !found {
			s.functionScheduleSerial++
			s.functionSchedule = append(s.functionSchedule, scheduledFunction{name: eventName, target: id, callbackType: kind, age: age, serial: s.functionScheduleSerial})
		}
		r := opsEchoReply(translatable(feedback, id, ticks, age))
		r.result = int(age % 2147483647)
		r.hasResult = true
		r.outcome = commandSucceeded
		return r
	}
	return errToken("command.unknown.argument", 1)
}

// Time arguments use single-precision input and round to a tick. Keep this
// parser separate from NBT/JSON integer conversion and world-age arithmetic.
func functionScheduleTime(raw string) (int32, string, string) {
	end := 0
	for end < len(raw) {
		c := raw[end]
		if !(c >= '0' && c <= '9' || c == '.' || c == '-') {
			break
		}
		end++
	}
	number := raw[:end]
	if number == "" {
		return 0, "parsing.float.expected", number
	}
	v, err := strconv.ParseFloat(number, 32)
	if err != nil && !math.IsInf(v, 0) {
		return 0, "parsing.float.invalid", number
	}
	factor := float32(1)
	switch raw[end:] {
	case "", "t":
	case "s":
		factor = 20
	case "d":
		factor = 24000
	default:
		return 0, "argument.time.invalid_unit", number
	}
	rounded := math.Floor(float64(float32(v)*factor) + 0.5)
	if rounded > math.MaxInt32 {
		return math.MaxInt32, "", number
	}
	if rounded < math.MinInt32 {
		return math.MinInt32, "", number
	}
	return int32(rounded), "", number
}

func (s *Server) functionScheduleStep() {
	age := s.gameTime()
	// Remove one event before running it: a callback can replace, clear or
	// append events without retaining an expired entry or losing new work.
	for {
		sort.SliceStable(s.functionSchedule, func(i, j int) bool {
			a, b := s.functionSchedule[i], s.functionSchedule[j]
			if a.age != b.age {
				return a.age < b.age
			}
			return a.serial < b.serial
		})
		if len(s.functionSchedule) == 0 || s.functionSchedule[0].age > age {
			return
		}
		event := s.functionSchedule[0]
		s.functionSchedule = s.functionSchedule[1:]
		switch event.callbackType {
		case functionScheduleCallback:
			s.runServerFunctionList([]string{event.target})
		case "minecraft:function_tag":
			s.runServerFunctionList(s.functionTags[event.target])
		}
	}
}
