package server

import (
	"fmt"
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
func (s *Server) runExecute(player string, args []string) string {
	ctx := executeContext{dim: s.dimOf(player), alive: true}
	bx, by, bz := s.baseBlockPos(player)

	i := 0
	for i < len(args) {
		switch args[i] {
		case "in":
			if i+1 >= len(args) {
				return "Usage: /execute in <dimension> ..."
			}
			d := s.dimensionByName(args[i+1])
			if d == nil {
				return "Unknown dimension: " + args[i+1]
			}
			ctx.dim = d
			i += 2
		case "if", "unless":
			if i+1 >= len(args) {
				return "Usage: /execute if|unless <condition> ..."
			}
			negate := args[i] == "unless"
			consumed, ok, msg := s.executeCondition(&ctx, player, args[i+1:], bx, by, bz)
			if msg != "" {
				return msg
			}
			if !ok {
				// A malformed condition short-circuits the whole chain.
				return "Usage: /execute if|unless <condition> ..."
			}
			if negate {
				ctx.alive = !ctx.alive
			}
			i += 1 + consumed
		case "run":
			sub := args[i+1:]
			if len(sub) == 0 {
				return "Usage: /execute ... run <command>"
			}
			if !ctx.alive {
				return "Test failed"
			}
			return s.runInDimension(player, ctx.dim, sub)
		case "positioned", "as", "at", "align", "facing", "rotated", "anchored", "summon":
			return "Unsupported execute subcommand: " + args[i]
		default:
			return "Unknown execute subcommand: " + args[i]
		}
	}
	return "Usage: /execute <subcommand> ... run <command>"
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
func (s *Server) runInDimension(player string, d *dimension, sub []string) string {
	switch sub[0] {
	case "tp", "teleport":
		return s.runExecuteTeleport(player, d, sub[1:])
	}
	sess := s.findSessionByName(player)
	if sess != nil && d != nil {
		prev := sess.dimension()
		sess.dim.Store(d)
		defer sess.dim.Store(prev)
	}
	if out, ok := s.commands.Run(sub[0], player, sub[1:]); ok {
		return out
	}
	return "Unknown command: " + sub[0]
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
		s.broadcastSystemChat("[<" + player + ">] " + strings.Join(args, " "))
		// The sender has already been given the line by the broadcast.
		return ""
	})
	s.commands.Register("tick", func(player string, args []string) string {
		return s.runTick(args)
	})
	s.commands.Register("fill", func(player string, args []string) string {
		return s.runFill(player, args)
	})
}

// runTick implements vanilla's /tick command surface.
func (s *Server) runTick(args []string) string {
	if len(args) == 0 {
		return "Usage: /tick <freeze|unfreeze|step|sprint|rate|query>"
	}
	switch args[0] {
	case "freeze":
		s.SetFrozen(true)
		return "The game is frozen"
	case "unfreeze":
		s.SetFrozen(false)
		return "The game is unfrozen"
	case "step":
		n, ok := tickArg(args[1:])
		if !ok {
			return "Usage: /tick step [<ticks>]"
		}
		s.StepTicks(n)
		return fmt.Sprintf("Stepping the game %d tick(s)", n)
	case "sprint":
		if len(args) >= 2 && args[1] == "stop" {
			s.StopSprint()
			return "Stopped sprinting"
		}
		n, ok := tickArg(args[1:])
		if !ok {
			return "Usage: /tick sprint [<ticks>|stop]"
		}
		s.SprintTicks(n)
		return fmt.Sprintf("Sprinting the game %d tick(s)", n)
	case "rate":
		if len(args) < 2 {
			st := s.clock.status()
			return fmt.Sprintf("The current tick rate is %.1f", st.Rate)
		}
		v, err := strconv.ParseFloat(args[1], 64)
		if err != nil {
			return "Invalid rate: " + args[1]
		}
		s.SetTickRate(v)
		return fmt.Sprintf("The tick rate has been set to %.1f", s.clock.tickRate())
	case "query":
		st := s.clock.status()
		if st.Frozen {
			return fmt.Sprintf("The game is frozen. Current tick: %d, tick rate: %.1f. Frozen for %d tick(s)",
				st.Count, st.Rate, st.FrozenFor)
		}
		return fmt.Sprintf("The game is running normally. Current tick: %d, tick rate: %.1f", st.Count, st.Rate)
	}
	return "Unknown /tick subcommand: " + args[0]
}

// tickArg parses an optional tick count, defaulting to 1. The trailing
// "t"/"d"/"s" unit suffixes vanilla accepts are not modelled.
func tickArg(args []string) (int64, bool) {
	if len(args) == 0 {
		return 1, true
	}
	v, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}

// runFill implements "/fill <x1> <y1> <z1> <x2> <y2> <z2> <block>
// [replace <filter>]", including the "replace" filter the fluid probes
// use to count what a region holds.
func (s *Server) runFill(player string, args []string) string {
	if len(args) < 7 {
		return "Usage: /fill <x1> <y1> <z1> <x2> <y2> <z2> <block> [replace <filter>]"
	}
	bx, by, bz := s.baseBlockPos(player)
	c := make([]int, 6)
	for i := 0; i < 6; i++ {
		v, err := parseCoord(args[i], map[int]int{0: bx, 2: bz, 1: by, 3: bx, 4: by, 5: bz}[i])
		if err != nil {
			return "Invalid coordinate: " + args[i]
		}
		c[i] = v
	}
	blk, _, ok := parseBlockSpec(args[6])
	if !ok {
		return "Unknown block: " + args[6]
	}
	var filter world.Block
	haveFilter := false
	exactFilter := false
	if len(args) >= 9 && args[7] == "replace" {
		f, ex, ok := parseBlockSpec(args[8])
		if !ok {
			return "Unknown block: " + args[8]
		}
		filter, exactFilter, haveFilter = f, ex, true
	}
	d := s.dimOf(player)
	minX, maxX := minmax(c[0], c[3])
	minY, maxY := minmax(c[1], c[4])
	minZ, maxZ := minmax(c[2], c[5])
	const fillLimit = 32768
	if (maxX-minX+1)*(maxY-minY+1)*(maxZ-minZ+1) > fillLimit {
		return "Too many blocks in the specified area"
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
		return "No blocks were filled"
	}
	return fmt.Sprintf("Successfully filled %d blocks", changed)
}

func minmax(a, b int) (int, int) {
	if a <= b {
		return a, b
	}
	return b, a
}
