package server

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// reply is what a command produced. Vanilla words its built-in replies
// as translatable components, so a command can answer with a component
// tree; plugins answer with plain text, which is sent as a string.
type reply struct {
	comp   component
	styled bool
	text   string
	// extra holds follow-up messages, for commands whose reply is more
	// than one line.
	extra []component
	// fail marks a reply as a failed parse, which the real server
	// follows with a second "context" message showing where the parse
	// stopped. Failures the real server reports at run time - a /tick
	// step while the game is running, a time marker that does not exist -
	// carry no mark and get no context message.
	fail *failMark
	// result is the command's numeric result — brigadier's return
	// value, which `execute store` writes into its target. hasResult is
	// set once the chain's leaf actually ran; parse errors and dead
	// chains leave it off and store writes nothing.
	result    int
	hasResult bool
	// opsEcho is sendSuccess's broadcastToOps flag: every op but the
	// caller also hears the reply, as the gray italic admin line.
	opsEcho bool
}

// failMark locates a parse failure in the input, in tokens: the token
// the parse failed on, and whether the cursor sits at its end rather
// than its start. Vanilla reports the end of the token for a value it
// read but rejected, and the start of it for one it could not read at
// all. A reader that stops mid-token - an id with a valid prefix and
// junk after it - reports past the prefix, which off carries.
type failMark struct {
	token int
	end   bool
	off   int
	// abs pins the cursor to an absolute input offset — component args
	// span several tokens, so token-relative marks cannot express a
	// failure mid-way through them.
	abs    int
	hasAbs bool
}

// tokenBounds finds one token's start and end offsets in the input. A
// token index past the last token means the input ran out mid-command,
// which puts both at the end.
func tokenBounds(input string, token int) (start, end int) {
	i, n := 0, 0
	for {
		for i < len(input) && input[i] == ' ' {
			i++
		}
		start = i
		for i < len(input) && input[i] != ' ' {
			i++
		}
		if start == i {
			return len(input), len(input)
		}
		if n == token {
			return start, i
		}
		n++
	}
}

// cursor is the offset in the input the real server would report for
// this failure.
func (f failMark) cursor(input string) int {
	if f.hasAbs {
		if f.abs > len(input) {
			return len(input)
		}
		return f.abs
	}
	start, end := tokenBounds(input, f.token)
	if f.end {
		return end
	}
	if c := start + f.off; c <= end {
		return c
	}
	return end
}

// textReply is a plain-text reply.
func textReply(text string) reply { return reply{text: text} }

// componentReply is a component reply.
func componentReply(c component) reply { return reply{comp: c, styled: true} }

// componentReplyWith is a component reply followed by more messages.
func componentReplyWith(c component, extra ...component) reply {
	return reply{comp: c, styled: true, extra: extra}
}

// opsEchoReply is sendSuccess(_, broadcastToOps=true): the reply reaches
// its caller and echoes to the other ops as the admin line.
func opsEchoReply(c component) reply {
	return reply{comp: c, styled: true, opsEcho: true}
}

// errToken is a parse failure at the start of a token: the real server
// could not read the value there.
func errToken(key string, token int, with ...any) reply {
	return reply{
		comp:   commandError(key, with...),
		styled: true,
		fail:   &failMark{token: token},
	}
}

// errTokenEnd is a parse failure at the end of a token: the real server
// read the value and rejected it.
func errTokenEnd(key string, token int, with ...any) reply {
	return reply{
		comp:   commandError(key, with...),
		styled: true,
		fail:   &failMark{token: token, end: true},
	}
}

// errTokenOff is a parse failure past a token's valid prefix: the real
// server read that much and stopped, as with an id the registry does
// not hold where the prefix parses but the rest does not.
func errTokenOff(key string, token, off int, with ...any) reply {
	return reply{
		comp:   commandError(key, with...),
		styled: true,
		fail:   &failMark{token: token, off: off},
	}
}

// asComponent renders any reply as a component, which is what /execute
// needs: it forwards whatever the inner command produced.
func (r reply) asComponent() component {
	if r.styled {
		return r.comp
	}
	return literal(r.text)
}

// registerComponentCommand registers a command whose reply is a
// component rather than plain text. Vanilla's built-ins are all
// translatable components, so this is the path those take.
func (s *Server) registerComponentCommand(name string, fn func(player string, args []string) reply) {
	s.compMu.Lock()
	defer s.compMu.Unlock()
	if s.compCommands == nil {
		s.compCommands = map[string]func(string, []string) reply{}
	}
	s.compCommands[name] = fn
}

// componentCommand looks up a component command.
func (s *Server) componentCommand(name string) func(string, []string) reply {
	s.compMu.RLock()
	defer s.compMu.RUnlock()
	return s.compCommands[name]
}

// helpNames lists every command /help offers: plugin commands plus the
// built-ins that answer with components, which live in their own
// registry (/setblock moved there for its vanilla success reply).
func (s *Server) helpNames() []string {
	names := s.commands.Names()
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	s.compMu.RLock()
	extra := make([]string, 0, len(s.compCommands))
	for n := range s.compCommands {
		if !seen[n] {
			extra = append(extra, n)
		}
	}
	s.compMu.RUnlock()
	sort.Strings(extra)
	return append(names, extra...)
}

// commandLevels is vanilla's required permission level for each root
// literal, measured against the 26.2 server: everyone gets the
// zero-level set, world commands take 2, multiplayer administration
// takes 3, and server lifecycle takes 4. Unlisted commands need 2.
var commandLevels = map[string]int{
	"help": 0, "list": 0, "me": 0, "msg": 0, "random": 0,
	"teammsg": 0, "tell": 0, "tm": 0, "trigger": 0, "w": 0,

	"ban": 3, "ban-ip": 3, "banlist": 3, "debug": 3, "deop": 3,
	"kick": 3, "op": 3, "pardon": 3, "pardon-ip": 3, "raid": 3,
	"setidletimeout": 3, "tick": 3, "transfer": 3, "whitelist": 3,

	"jfr": 4, "perf": 4, "save-all": 4, "save-off": 4,
	"save-on": 4, "stop": 4,
}

// commandLevel reports the permission level a command needs.
func commandLevel(name string) int {
	if lvl, ok := commandLevels[name]; ok {
		return lvl
	}
	return 2
}

// commandNeedsArgs marks roots with no bare executable — vanilla's
// parser reaches the end of the name with nothing to run and reports
// command.unknown.command underlining the name, same shape as a
// gated command. Measured on 26.2 for the commands that diverged.
var commandNeedsArgs = map[string]bool{
	"data": true, "execute": true, "fill": true, "give": true,
	"raid": true, "scoreboard": true, "setblock": true, "summon": true,
	"team": true, "teleport": true, "tellraw": true,
	"title": true, "tp": true,
}

// runCommand runs any registered command - component command or plugin
// command - and reports whether it existed. A command the player is not
// leveled for reports unknown, the same as an unregistered name —
// vanilla gates the root literal the same way.
func (s *Server) runCommand(player, name string, args []string) (reply, bool) {
	return s.runCommandAt(player, s.permissionLevel(player), name, args)
}

// runCommandAt is runCommand with the caller's level spelled out —
// console sources (RCON) carry level 4 without an op entry.
func (s *Server) runCommandAt(player string, level int, name string, args []string) (reply, bool) {
	if level < commandLevel(name) {
		return reply{}, false
	}
	if len(args) == 0 && commandNeedsArgs[name] {
		return errTokenEnd("command.unknown.command", 0), true
	}
	if fn := s.componentCommand(name); fn != nil {
		return fn(player, args), true
	}
	out, ok := s.commands.Run(name, player, args)
	if !ok {
		return reply{}, false
	}
	return textReply(out), true
}

// unknownCommand is the component vanilla replies with for a command it
// does not know.
func unknownCommand() component { return commandError("command.unknown.command") }

// runTime implements vanilla's /time command surface: /time query
// gametime|<timeline>, and /time set|add <ticks|time marker>. Replies
// are the real server's components, so a client localizes them and the
// numbers keep their NBT types.
func (s *Server) runTime(player string, args []string) reply {
	if len(args) == 0 {
		// A bare "/time" stops at the end of its own name.
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "query":
		return s.runTimeQuery(args[1:])
	case "set":
		return s.runTimeSet(player, args[1:], false)
	case "add":
		return s.runTimeSet(player, args[1:], true)
	}
	return errToken("command.unknown.argument", 1)
}

// timeMarkers are vanilla's named times of day, in ticks.
var timeMarkers = map[string]int64{
	"day":      1000,
	"noon":     6000,
	"night":    13000,
	"midnight": 18000,
}

// runTimeQuery handles /time query gametime and /time query <timeline>.
// The only timeline the vanilla world ships is minecraft:day; a query
// for anything else is an unknown resource, exactly as the real server
// reports it.
func (s *Server) runTimeQuery(args []string) reply {
	if len(args) != 1 {
		// "/time query" with no timeline: the parse stops after "query".
		return errTokenEnd("command.unknown.command", 1)
	}
	switch args[0] {
	case "gametime":
		r := componentReply(translatable("commands.time.query.gametime", s.gameTime()))
		r.result, r.hasResult = int(s.gameTime()), true
		return r
	case "day", "minecraft:day":
		v := s.timelineTime()
		r := componentReply(translatable("commands.time.query.timeline",
			nestedCompound{key: "", value: "minecraft:day"},
			nestedCompound{key: "", value: v}))
		r.result, r.hasResult = int(v), true
		return r
	}
	name := args[0]
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	// The timeline is read as a registry resource, so an unknown one
	// reports the resource error with the cursor past its name.
	return errTokenEnd("argument.resource.not_found", 2, name, "minecraft:timeline")
}

// timelineTime is the position within the day timeline: the daytime
// modulo the 24000-tick day, which is what the real server reports.
func (s *Server) timelineTime() int64 {
	day := s.dayTime() % 24000
	if day < 0 {
		day += 24000
	}
	return day
}

// runTimeSet handles /time set <ticks|time marker> and /time add
// <ticks>. Vanilla's add takes a tick count only, while set also
// accepts a named time of day, and a value that parses as neither is a
// marker lookup failure for set and a parse error for add.
func (s *Server) runTimeSet(player string, args []string, add bool) reply {
	if len(args) != 1 {
		// "/time set" with no value: the parse stops after "set".
		return errTokenEnd("command.unknown.command", 1)
	}
	arg := args[0]
	dim := s.dimNameOf(player)
	ticks, err := strconv.ParseFloat(arg, 64)
	if err != nil {
		if add {
			return errToken("parsing.float.expected", 2)
		}
		marker := arg
		if !strings.Contains(marker, ":") {
			marker = "minecraft:" + marker
		}
		value, ok := timeMarkers[strings.TrimPrefix(marker, "minecraft:")]
		if !ok {
			// Vanilla names the missing marker by its registry key. The
			// marker lookup happens after parsing, so this failure has no
			// context message of its own.
			return componentReply(commandError("commands.time.no_time_marker_found",
				"ResourceKey[minecraft:clock_time_marker / "+marker+"]", dim))
		}
		s.setDayTime(value)
		return componentReply(translatable("commands.time.set.time_marker", dim, marker))
	}
	// Vanilla converts the float argument to whole ticks, rounding to
	// the nearest as it goes.
	value := roundTicks(ticks)
	if add {
		// The argument is an int, so it saturates before it is added,
		// and the sum is reported as a long: it is a sum with the
		// world's existing clock.
		s.setDayTime(s.dayTime() + saturateInt(value))
		return componentReply(translatable("commands.time.set.absolute",
			dimCompound(dim), nestedCompound{key: "", value: s.dayTime()}))
	}
	// The tick count is an int, and the real server saturates rather
	// than wrapping: "/time set 3000000000" sets 2147483647.
	s.setDayTime(saturateInt(value))
	return componentReply(translatable("commands.time.set.absolute",
		dimCompound(dim), nestedCompound{key: "", value: int32(saturateInt(value))}))
}

// saturateInt narrows a tick count to the int range the real server's
// time argument uses, clamping rather than wrapping.
func saturateInt(v int64) int64 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return v
}

// dimCompound renders a dimension name as the compound vanilla passes
// to the translation.
func dimCompound(dim string) nestedCompound {
	return nestedCompound{key: "", value: dim}
}

// dimNameOf reports the dimension a player is in, for command replies
// that name it.
func (s *Server) dimNameOf(player string) string {
	if sess := s.findSessionByName(player); sess != nil {
		return sess.dimName()
	}
	return "minecraft:overworld"
}
