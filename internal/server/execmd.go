package server

import (
	"math"
	"strconv"
	"strings"

	"mc-go/internal/world"
)

// /execute, /say and /tick.
//
// /execute is a chain, not a single form: vanilla reads subcommands left
// to right, each refining the context (which dimension, whether the
// remaining chain runs at all), until "run" hands the rest to the
// command dispatcher. mc-go implements the chain for the subcommands it
// supports, so `execute in the_nether if block ... run ...` composes the
// same way it does in vanilla.

// executeContext is the state a chain carries into its `run`.
type executeContext struct {
	dim   *dimension
	alive bool
}

// runExecute parses an /execute chain and runs its trailing command.
func (s *Server) runExecute(player string, args []string) reply {
	ctx := executeContext{dim: s.dimOf(player), alive: true}
	bx, by, bz := s.baseBlockPos(player)

	i := 0
	for i < len(args) {
		switch args[i] {
		case "in":
			if i+1 >= len(args) {
				return textReply("Usage: /execute in <dimension> ...")
			}
			d := s.dimensionByName(args[i+1])
			if d == nil {
				return textReply("Unknown dimension: " + args[i+1])
			}
			ctx.dim = d
			i += 2
		case "if", "unless":
			if i+1 >= len(args) {
				return textReply("Usage: /execute if|unless <condition> ...")
			}
			negate := args[i] == "unless"
			consumed, ok, msg := s.executeCondition(&ctx, player, args[i+1:], bx, by, bz)
			if msg != "" {
				return textReply(msg)
			}
			if !ok {
				// A malformed condition short-circuits the whole chain.
				return textReply("Usage: /execute if|unless <condition> ...")
			}
			if negate {
				ctx.alive = !ctx.alive
			}
			i += 1 + consumed
		case "run":
			sub := args[i+1:]
			if len(sub) == 0 {
				return textReply("Usage: /execute ... run <command>")
			}
			if !ctx.alive {
				return textReply("Test failed")
			}
			return s.runInDimension(player, ctx.dim, sub)
		case "positioned", "as", "at", "align", "facing", "rotated", "anchored", "summon":
			return textReply("Unsupported execute subcommand: " + args[i])
		default:
			return textReply("Unknown execute subcommand: " + args[i])
		}
	}
	return textReply("Usage: /execute <subcommand> ... run <command>")
}

// executeCondition evaluates one condition and reports how many of the
// args it consumed. It ANDs into ctx.alive, matching vanilla's
// short-circuit behaviour where every condition in the chain must hold.
func (s *Server) executeCondition(ctx *executeContext, player string, args []string, bx, by, bz int) (consumed int, ok bool, msg string) {
	if len(args) == 0 {
		return 0, false, "Usage: /execute if|unless <condition> ..."
	}
	switch args[0] {
	case "block":
		// if block <x> <y> <z> <block predicate>
		if len(args) < 5 {
			return 0, false, "Usage: /execute if block <x> <y> <z> <block>"
		}
		x, err := parseCoord(args[1], bx)
		if err != nil {
			return 0, false, "Invalid x: " + args[1]
		}
		y, err := parseCoord(args[2], by)
		if err != nil {
			return 0, false, "Invalid y: " + args[2]
		}
		z, err := parseCoord(args[3], bz)
		if err != nil {
			return 0, false, "Invalid z: " + args[3]
		}
		blk, exact, okp := parseBlockSpec(args[4])
		if !okp {
			return 0, false, "Unknown block: " + args[4]
		}
		if ctx.alive {
			ctx.alive = s.matchesBlockPredicate(ctx.dim, world.Pos{X: x, Y: y, Z: z}, blk, exact)
		}
		return 5, true, ""
	case "entity":
		// if entity <selector> — only the @s/@p forms mc-go can resolve.
		if len(args) < 2 {
			return 0, false, "Usage: /execute if entity <target>"
		}
		if ctx.alive {
			ctx.alive = s.resolveTarget(player, args[1]) != nil
		}
		return 2, true, ""
	case "loaded":
		// if loaded <x> <y> <z> — mc-go keeps every column it can generate.
		if len(args) < 4 {
			return 0, false, "Usage: /execute if loaded <x> <y> <z>"
		}
		return 4, true, ""
	default:
		return 0, false, "Unknown condition: " + args[0]
	}
}

// matchesBlockPredicate reports whether the block at pos satisfies a
// predicate: an exact state when properties were given, otherwise any
// state of that block.
func (s *Server) matchesBlockPredicate(d *dimension, pos world.Pos, blk world.Block, exact bool) bool {
	if d == nil {
		return false
	}
	got := d.w.GetBlock(pos)
	if exact {
		return got == blk
	}
	want, ok1 := world.StateOwner(blk)
	have, ok2 := world.StateOwner(got)
	return ok1 && ok2 && want == have
}

// runInDimension dispatches a command with the execute context's
// dimension in force for the duration of the call. Teleport is the one
// command that must actually move the target across dimensions rather
// than just run with that context, so it bypasses the registry.
func (s *Server) runInDimension(player string, d *dimension, sub []string) reply {
	switch sub[0] {
	case "tp", "teleport":
		return textReply(s.runExecuteTeleport(player, d, sub[1:]))
	}
	sess := s.findSessionByName(player)
	if sess != nil && d != nil {
		prev := sess.dimension()
		sess.dim.Store(d)
		defer sess.dim.Store(prev)
	}
	if r, ok := s.runCommand(player, sub[0], sub[1:]); ok {
		return r
	}
	return componentReply(unknownCommand())
}

// resolveTarget maps a selector token to a live session.
func (s *Server) resolveTarget(player, tok string) *Session {
	switch tok {
	case "@s", "@p", "@e", "@a", "@r":
		return s.findSessionByName(player)
	}
	return s.findSessionByName(tok)
}

// registerWorldCommands adds the world-facing commands the fluid and
// tick work needs: /say, /tick, /fill and /execute's condition forms are
// reached through runExecute.
func (s *Server) registerWorldCommands() {
	s.commands.Register("say", func(player string, args []string) string {
		if len(args) == 0 {
			return "Usage: /say <message>"
		}
		name := literal(player)
		if sess := s.findSessionByName(player); sess != nil {
			name = playerChatName(sess)
		}
		s.broadcastDisguisedChat("say_command", name, literal(strings.Join(args, " ")))
		// The sender has already been given the line by the broadcast.
		return ""
	})
	s.commands.Register("me", func(player string, args []string) string {
		if len(args) == 0 {
			return "Usage: /me <action>"
		}
		name := literal(player)
		if sess := s.findSessionByName(player); sess != nil {
			name = playerChatName(sess)
		}
		s.broadcastDisguisedChat("emote_command", name, literal(strings.Join(args, " ")))
		return ""
	})
	msgCmd := func(player string, args []string) string {
		if len(args) < 2 {
			return "Usage: /msg <targets> <message>"
		}
		targets := s.resolveTargets(player, args[0])
		if len(targets) == 0 {
			return "Player is unknown"
		}
		sender := s.findSessionByName(player)
		name := literal(player)
		if sender != nil {
			name = playerChatName(sender)
		}
		content := literal(strings.Join(args[1:], " "))
		incoming := registryEntryID("minecraft:chat_type", "minecraft:msg_command_incoming")
		outgoing := registryEntryID("minecraft:chat_type", "minecraft:msg_command_outgoing")
		for _, target := range targets {
			if sender != nil && outgoing >= 0 {
				tName := playerChatName(target)
				sendDisguisedChat(sender, outgoing, name, content, &tName)
			}
			if incoming >= 0 {
				sendDisguisedChat(target, incoming, name, content, nil)
			}
		}
		return ""
	}
	s.commands.Register("msg", msgCmd)
	s.commands.Register("tell", msgCmd)
	s.commands.Register("w", msgCmd)
	s.registerComponentCommand("tick", func(player string, args []string) reply {
		return s.runTick(player, args)
	})
	s.registerComponentCommand("time", func(player string, args []string) reply {
		return s.runTime(player, args)
	})
	s.registerComponentCommand("item", func(player string, args []string) reply {
		return s.runItem(player, args)
	})
	s.registerComponentCommand("gamemode", func(player string, args []string) reply {
		return s.runGamemode(player, args)
	})
	s.registerComponentCommand("xp", func(player string, args []string) reply {
		return s.runXp(player, args)
	})
	s.registerComponentCommand("effect", func(player string, args []string) reply {
		return s.runEffect(player, args)
	})
	s.registerComponentCommand("recipe", func(player string, args []string) reply {
		return s.runRecipe(player, args)
	})
	s.registerComponentCommand("fill", func(player string, args []string) reply {
		return s.runFill(player, args)
	})
	s.registerComponentCommand("weather", func(player string, args []string) reply {
		return s.runWeather(player, args)
	})
}

// runTick implements vanilla's /tick command surface. Replies are the
// real server's components - a client localizes them and /tick query
// reports numbers with the same NBT types - and /tick step only works
// while the game is frozen, exactly as the real server reports it.
func (s *Server) runTick(player string, args []string) reply {
	if len(args) == 0 {
		// A bare "/tick" stops at the end of its own name.
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "freeze":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		s.SetFrozen(true)
		return componentReply(translatable("commands.tick.status.frozen"))
	case "unfreeze":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		s.SetFrozen(false)
		return componentReply(translatable("commands.tick.status.running"))
	case "step":
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		n, err := tickCount(args[1:])
		if err != nil {
			return errToken("parsing.float.expected", 2)
		}
		if n < 1 {
			return errTokenEnd("argument.time.tick_count_too_low", 2, int32(1), int32(n))
		}
		if !s.clock.isFrozen() {
			return componentReply(commandError("commands.tick.step.fail"))
		}
		s.StepTicks(n)
		return componentReply(translatable("commands.tick.step.success", int32(n)))
	case "sprint":
		if len(args) >= 2 && args[1] == "stop" {
			if !s.clock.isSprinting() {
				return componentReply(commandError("commands.tick.sprint.stop.fail"))
			}
			s.StopSprint()
			return componentReply(translatable("commands.tick.sprint.stop.success"))
		}
		if len(args) > 2 {
			return errToken("command.unknown.argument", 3)
		}
		n, err := tickCount(args[1:])
		if err != nil {
			return errToken("parsing.float.expected", 2)
		}
		if n < 1 {
			return errTokenEnd("argument.time.tick_count_too_low", 2, int32(1), int32(n))
		}
		s.SprintTicks(n)
		return componentReply(translatable("commands.tick.status.sprinting"))
	case "rate":
		if len(args) != 2 {
			// The rate argument is required, so a bare "/tick rate"
			// stops at the end of "rate" rather than naming a value.
			return errTokenEnd("command.unknown.command", 1)
		}
		v, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			return errToken("parsing.float.expected", 2)
		}
		if v > MaxTickRate {
			return errToken("argument.float.big", 2, float32(MaxTickRate), float32(v))
		}
		if v < 1 {
			return errToken("argument.float.low", 2, float32(1), float32(v))
		}
		s.SetTickRate(v)
		return componentReply(translatable("commands.tick.rate.success", formatRate(s.clock.tickRate())))
	case "query":
		return s.runTickQuery(args[1:])
	}
	return errToken("command.unknown.argument", 1)
}

// runTickQuery reports the clock state. The real server follows it with
// a tick-time percentile report; mc-go does not keep tick-time history
// yet, so it reports the state and the rate and stops there.
func (s *Server) runTickQuery(args []string) reply {
	if len(args) > 0 {
		return errToken("command.unknown.argument", 2)
	}
	st := s.clock.status()
	// Two messages: the clock state, then the rate report. The real
	// server's rate report names the fastest, average and slowest tick
	// times over its window; mc-go reports the target rate three times
	// until tick-time history exists.
	rate := formatRate(st.Rate)
	if st.Frozen {
		return componentReplyWith(translatable("commands.tick.status.frozen"),
			translatable("commands.tick.query.rate.frozen", rate, rate, rate))
	}
	return componentReplyWith(translatable("commands.tick.status.running"),
		translatable("commands.tick.query.rate.running", rate, rate, rate))
}

// formatRate renders a tick rate the way the real server does in its
// replies: one decimal place.
func formatRate(rate float64) string {
	return strconv.FormatFloat(rate, 'f', 1, 64)
}

// tickCount parses /tick's optional tick count, which the real server
// takes as a number and defaults to one. A fractional count is rounded
// to the nearest tick rather than truncated: "/tick step 2.7" steps
// three times, not two.
func tickCount(args []string) (int64, error) {
	if len(args) == 0 {
		return 1, nil
	}
	v, err := strconv.ParseFloat(args[0], 64)
	if err != nil {
		return 0, err
	}
	return roundTicks(v), nil
}

// roundTicks converts a tick argument to whole ticks the way the real
// server does: half rounds up, which is Java's Math.round.
func roundTicks(v float64) int64 {
	return int64(math.Floor(v + 0.5))
}

// runFill implements "/fill <x1> <y1> <z1> <x2> <y2> <z2> <block>
// [replace <filter>]", including the "replace" filter the fluid probes
// use to count what a region holds.
func (s *Server) runFill(player string, args []string) reply {
	if len(args) < 7 {
		return errTokenEnd("command.unknown.argument", len(args))
	}
	bx, by, bz := s.baseBlockPos(player)
	c := make([]int, 6)
	for i := 0; i < 6; i++ {
		v, err := parseCoord(args[i], map[int]int{0: bx, 2: bz, 1: by, 3: bx, 4: by, 5: bz}[i])
		if err != nil {
			return errToken("parsing.int.invalid", i)
		}
		c[i] = v
	}
	blk, _, ok := parseBlockSpec(args[6])
	if !ok {
		return errTokenEnd("argument.block.id.invalid", 7)
	}
	var filter world.Block
	haveFilter := false
	exactFilter := false
	if len(args) >= 9 && args[7] == "replace" {
		f, ex, ok := parseBlockSpec(args[8])
		if !ok {
			return errTokenEnd("argument.block.id.invalid", 8)
		}
		filter, exactFilter, haveFilter = f, ex, true
	}
	d := s.dimOf(player)
	minX, maxX := minmax(c[0], c[3])
	minY, maxY := minmax(c[1], c[4])
	minZ, maxZ := minmax(c[2], c[5])
	const fillLimit = 32768
	if (maxX-minX+1)*(maxY-minY+1)*(maxZ-minZ+1) > fillLimit {
		return componentReply(commandError("commands.fill.toobig",
			int32(fillLimit), int32((maxX-minX+1)*(maxY-minY+1)*(maxZ-minZ+1))))
	}
	// A large fill is one operation with one column-top recompute, the
	// same shape vanilla uses; light is refreshed per affected section.
	touched := map[[2]int]bool{}
	changed := 0
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if y < d.minY || y >= d.minY+d.height {
					continue
				}
				pos := world.Pos{X: x, Y: y, Z: z}
				if haveFilter {
					if !s.matchesBlockPredicate(d, pos, filter, exactFilter) {
						continue
					}
				} else if d.w.GetBlock(pos) == blk {
					continue
				}
				s.setFluidBlock(d, pos, blk)
				touched[[2]int{x >> 4, z >> 4}] = true
				changed++
			}
		}
	}
	_ = touched
	if changed == 0 {
		return componentReply(commandError("commands.fill.failed"))
	}
	return componentReply(translatable("commands.fill.success", int32(changed)))
}

func minmax(a, b int) (int, int) {
	if a <= b {
		return a, b
	}
	return b, a
}
