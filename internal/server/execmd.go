package server

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"mc-go/internal/data"
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
	// pos is the chain's origin — where ~ resolves from; positioned,
	// at and align move it. ent is the executor an `as`/`summon` fork
	// binds for @s reads inside the tail.
	pos        [3]float64
	rotation   [2]float32
	anchorEyes bool
	ent        *dataEntity
	// forked marks a chain running inside an entity-fork modifier
	// (as/at/positioned-as/facing-entity/rotated-as): its leaf's
	// runtime failures are swallowed — only parse errors escape.
	forked bool
	// hasCond marks that an if/unless ran; when the chain ends on one
	// it is itself the leaf, answering conditional.pass/fail on the
	// wire. entityCount is the last `if entity`'s match count — the
	// count conditional.pass_count prints and `store` reads back.
	hasCond      bool
	entityCount  int
	entityCountd bool
	// condErr is a condition failure that only reports when the chain
	// ends on it — mid-chain and `run` swallow it silently.
	condErr *reply
}

// runExecute parses an /execute chain and runs its trailing command.
func (s *Server) runExecute(player string, args []string) reply {
	if plan, err, handled := s.compileExecutePlan(player, args, 1); handled {
		if err != nil {
			return *err
		}
		return s.runExecutePlan(player, plan)
	}
	ctx := executeContext{dim: s.dimOf(player), alive: true}
	ctx.pos = s.execOrigin(player)
	ctx.rotation = s.functionSource(player).rotation
	if frame := s.commandFrame(player); frame != nil {
		ctx.ent = frame.executor
	}
	source := s.commandFrame(player)
	if source == nil {
		source = s.functionSource(player)
		source.level = s.permissionLevel(player)
	}
	ctx.ent = source.executor
	ctx.anchorEyes = source.anchorEyes
	return s.withCommandFrame(source, func() reply { return s.execTail(&ctx, player, args, 1) })
}

// execOrigin returns the sender's feet position — the point the
// chain's ~ tokens start from.
func (s *Server) execOrigin(player string) [3]float64 {
	if frame := s.commandFrame(player); frame != nil {
		return frame.pos
	}
	if sess := s.findSessionByName(player); sess != nil {
		x, y, z, _, _, _ := sess.Pos()
		return [3]float64{x, y, z}
	}
	sx, sy, sz := s.WorldSpawn()
	return [3]float64{sx, sy, sz}
}

// execTail evaluates a chain fragment starting at args[0]; argTokBase
// maps args[0] onto its token index in the full input. `store` recurses
// here for the chain its result comes from.
func (s *Server) execTail(ctx *executeContext, player string, args []string, argTokBase int) reply {
	source := s.commandFrame(player)
	if source != nil {
		defer func() { s.activeCommandSource = source }()
	}
	i := 0
	for i < len(args) {
		if source != nil {
			s.activeCommandSource = executeSourceFrame(source, ctx)
		}
		bx := int(math.Floor(ctx.pos[0]))
		by := int(math.Floor(ctx.pos[1]))
		bz := int(math.Floor(ctx.pos[2]))
		switch args[i] {
		case "in":
			if i+1 >= len(args) {
				return textReply("Usage: /execute in <dimension> ...")
			}
			d := s.dimensionByName(args[i+1])
			if d == nil {
				return textReply("Unknown dimension: " + args[i+1])
			}
			setExecuteDimension(ctx, d)
			i += 2
		case "if", "unless":
			if i+1 >= len(args) {
				return textReply("Usage: /execute if|unless <condition> ...")
			}
			negate := args[i] == "unless"
			ctx.condErr = nil
			consumed, cond, count, counted, err := s.executeCondition(ctx, player, args[i+1:], bx, by, bz, argTokBase+i+1)
			if err != nil {
				return *err
			}
			ctx.hasCond = true
			ctx.entityCount, ctx.entityCountd = count, counted
			// unless runs the chain when the condition reads false —
			// the negation folds into the AND, not after it.
			if ctx.condErr != nil {
				ctx.alive = false
			} else if negate {
				cond = !cond
			}
			ctx.alive = ctx.alive && cond
			i += 1 + consumed
		case "store":
			return s.executeStore(ctx, player, args[i+1:], argTokBase+i+1, bx, by, bz)
		case "positioned":
			return s.executePositioned(ctx, player, args[i+1:], argTokBase+i+1)
		case "as", "at":
			return s.executeFork(ctx, player, args[i], args[i+1:], argTokBase+i+1)
		case "rotated", "facing":
			return s.executeAim(ctx, player, args[i], args[i+1:], argTokBase+i+1)
		case "align":
			// align <axes> floors the origin on each named axis.
			if i+1 >= len(args) {
				return errToken("command.unknown.argument", argTokBase+i+1)
			}
			for _, c := range args[i+1] {
				switch c {
				case 'x':
					ctx.pos[0] = math.Floor(ctx.pos[0])
				case 'y':
					ctx.pos[1] = math.Floor(ctx.pos[1])
				case 'z':
					ctx.pos[2] = math.Floor(ctx.pos[2])
				default:
					return errToken("command.unknown.argument", argTokBase+i+1)
				}
			}
			i += 2
		case "anchored":
			// The eye offset applies only when resolving local coordinates.
			if i+1 >= len(args) {
				return errToken("argument.anchor.invalid", argTokBase+i+1)
			}
			if args[i+1] != "eyes" && args[i+1] != "feet" {
				return errToken("argument.anchor.invalid", argTokBase+i+1, args[i+1])
			}
			ctx.anchorEyes = args[i+1] == "eyes"
			i += 2
		case "on":
			// on <relation> forks onto the relation's entities — mc-go
			// models no relations, so the fork list comes up empty.
			if i+1 >= len(args) {
				return errToken("command.unknown.argument", argTokBase+i+1)
			}
			switch args[i+1] {
			case "attacker", "controller", "leasher", "origin", "owner",
				"passengers", "target", "vehicle":
			default:
				return errToken("command.unknown.argument", argTokBase+i+1)
			}
			return reply{}
		case "summon":
			return s.executeSummon(ctx, player, args[i+1:], argTokBase+i+1)
		case "run":
			sub := args[i+1:]
			if len(sub) == 0 {
				return textReply("Usage: /execute ... run <command>")
			}
			if !ctx.alive {
				// A failed conditional ends the chain with no output
				// at all — vanilla's only reply is the run command's.
				return reply{}
			}
			return s.execRun(ctx, player, sub, argTokBase+i+1)
		default:
			return errToken("command.unknown.argument", argTokBase+i)
		}
	}
	// Ending on a condition rather than `run` makes the condition the
	// leaf: pass or fail, pass_count when `if entity` counted more
	// than one match, and the result `store` writes back.
	if ctx.hasCond {
		if !ctx.alive {
			if ctx.condErr != nil {
				return *ctx.condErr
			}
			r := componentReply(commandError("commands.execute.conditional.fail"))
			r.result, r.hasResult = 0, true
			return r
		}
		var r reply
		switch {
		case ctx.entityCountd:
			// `if entity` answers with its match count — even one.
			r = componentReply(translatable("commands.execute.conditional.pass_count", int32(ctx.entityCount)))
			r.result = ctx.entityCount
		default:
			r = componentReply(translatable("commands.execute.conditional.pass"))
			r.result = 1
		}
		r.hasResult = true
		return r
	}
	return textReply("Usage: /execute <subcommand> ... run <command>")
}

// executeCondition evaluates one condition and reports how many of the
// args it consumed plus the raw condition result — the caller folds
// unless's negation into ctx.alive. The third return is a parse error
// reply when the condition itself is malformed.
func (s *Server) executeCondition(ctx *executeContext, player string, args []string, bx, by, bz, tokBase int) (consumed int, cond bool, count int, counted bool, err *reply) {
	if len(args) == 0 {
		r := textReply("Usage: /execute if|unless <condition> ...")
		return 0, false, 0, false, &r
	}
	switch args[0] {
	case "block":
		// if block <x> <y> <z> <block predicate>
		x, y, z, cerr := blockPosArgs(args, bx, by, bz, tokBase)
		if cerr != nil {
			return 0, false, 0, false, cerr
		}
		if len(args) < 5 {
			r := errToken("command.unknown.argument", tokBase+4)
			return 0, false, 0, false, &r
		}
		if strings.HasPrefix(args[4], "#") {
			predicate, perr := parseBlockTagPredicate(args[4], tokBase+4)
			if perr != nil {
				return 0, false, 0, false, perr
			}
			if ctx.alive && s.executeBlockReadReady(ctx, world.Pos{X: x, Y: y, Z: z}) {
				cond = predicate.matches(s, ctx.dim, world.Pos{X: x, Y: y, Z: z})
			}
			return 5, cond, 0, false, nil
		}
		blk, props, pattern, perr := parseBlockPredicateNBT(args[4], tokBase+4)
		if perr != nil {
			return 0, false, 0, false, perr
		}
		if ctx.alive && s.executeBlockReadReady(ctx, world.Pos{X: x, Y: y, Z: z}) {
			pos := world.Pos{X: x, Y: y, Z: z}
			cond = s.matchesBlockPredicate(ctx.dim, pos, blk, props)
			if cond && pattern != nil {
				cond = s.blockPredicateNBTMatches(ctx.dim, pos, pattern)
			}
		}
		return 5, cond, 0, false, nil
	case "blocks":
		// if blocks <from> <to> <dest> (all|masked) — nine int blockpos
		// args then the mode keyword, which only accepts all/masked.
		c, cerr := blockPosTriple(args, bx, by, bz, tokBase)
		if cerr != nil {
			return 0, false, 0, false, cerr
		}
		if len(args) < 11 || (args[10] != "all" && args[10] != "masked") {
			r := errToken("command.unknown.argument", tokBase+10)
			return 0, false, 0, false, &r
		}
		if ctx.alive {
			cond = s.blocksMatch(ctx.dim, c, args[10] == "masked")
		}
		return 11, cond, 0, false, nil
	case "entity":
		// if entity <selector> — a selector matching nothing fails
		// silently; a bare name matching no player is a parse error.
		if len(args) < 2 {
			r := textReply("Usage: /execute if entity <target>")
			return 0, false, 0, false, &r
		}
		if ctx.alive {
			if len(args[1]) > 0 && args[1][0] != '@' {
				// A bare player name reads like an empty selector —
				// the condition fails silently on the wire.
				cond = s.findSessionByName(args[1]) != nil
				if cond {
					count, counted = 1, true
				}
			} else {
				cands, serr := s.selectDataEntities(s.findSessionByName(player), args[1], tokBase+1, false, ctx.dim)
				if serr != nil {
					return 0, false, 0, false, serr
				}
				cond = len(cands) > 0
				count, counted = len(cands), true
			}
		}
		return 2, cond, count, counted, nil
	case "items":
		c, cd, e := s.executeIfItems(ctx, player, args, bx, by, bz, tokBase)
		return c, cd, 0, false, e
	case "data":
		c, cd, e := s.executeIfData(ctx, player, args, bx, by, bz, tokBase)
		return c, cd, 0, false, e
	case "score":
		c, cd, e := s.executeIfScore(ctx, player, args, tokBase)
		return c, cd, 0, false, e
	case "dimension":
		if len(args) < 2 {
			r := textReply("Usage: /execute if dimension <id>")
			return 0, false, 0, false, &r
		}
		if ctx.alive && ctx.dim != nil {
			cond = ctx.dim == s.dimensionByName(args[1])
		}
		return 2, cond, 0, false, nil
	case "biome":
		// if biome <x> <y> <z> <id> — quart overrides via biomeAt.
		if len(args) < 5 {
			r := textReply("Usage: /execute if biome <x> <y> <z> <biome>")
			return 0, false, 0, false, &r
		}
		if ctx.alive && ctx.dim != nil {
			name := args[4]
			if !strings.Contains(name, ":") {
				name = "minecraft:" + name
			}
			if x, y, z, berr := blockPosArgs(args, bx, by, bz, tokBase); berr == nil {
				cond = s.biomeAt(ctx.dim, world.Pos{X: x, Y: y, Z: z}) == name
			}
		}
		return 5, cond, 0, false, nil
	case "predicate":
		if len(args) < 2 {
			r := textReply("Usage: /execute if predicate <id>")
			return 0, false, 0, false, &r
		}
		// mc-go loads no datapack predicates, so every id misses the
		// registry — argument.resource_or_id.no_such_element.
		r := errTokenEnd("argument.resource_or_id.no_such_element", tokBase+1, args[1], "minecraft:predicate")
		return 0, false, 0, false, &r
	case "loaded":
		// if loaded <x> <y> <z> — true while the column is live in the
		// dimension, which for mc-go means a play session tracks it.
		x, _, z, cerr := blockPosArgs(args, bx, by, bz, tokBase)
		if cerr != nil {
			return 0, false, 0, false, cerr
		}
		if ctx.alive {
			if available, known := s.scheduledPositionAvailable(ctx.dim, x>>4, z>>4, true); known {
				cond = available
			} else {
				cond = s.chunkLoaded(ctx.dim, x>>4, z>>4)
			}
		}
		return 4, cond, 0, false, nil
	case "function":
		// if function <id> — mc-go keeps no function table, so every id
		// (or #tag) misses and the chain dies quietly.
		if len(args) < 2 {
			r := errToken("command.unknown.argument", tokBase+1)
			return 0, false, 0, false, &r
		}
		return 2, false, 0, false, nil
	default:
		r := errToken("command.unknown.argument", tokBase)
		return 0, false, 0, false, &r
	}
}

// blockPosArgs reads one int blockpos (three tokens, ~ allowed) out of
// args starting at index 1, reporting a bad or missing coordinate as
// parsing.int.expected at that token — the shape the real server gives
// a BlockPosArgument failure.
func blockPosArgs(args []string, bx, by, bz, tokBase int) (x, y, z int, r *reply) {
	base := [3]int{bx, by, bz}
	out := [3]int{}
	for k := 0; k < 3; k++ {
		if 1+k >= len(args) {
			r := errToken("parsing.int.expected", tokBase+1+k)
			return 0, 0, 0, &r
		}
		v, err := parseCoord(args[1+k], base[k])
		if err != nil {
			r := errToken("parsing.int.expected", tokBase+1+k)
			return 0, 0, 0, &r
		}
		out[k] = v
	}
	return out[0], out[1], out[2], nil
}

// blockPosTriple reads three blockpos in a row for `if blocks`
// (start, end, destination) with the same error shape.
func blockPosTriple(args []string, bx, by, bz, tokBase int) (c [9]int, r *reply) {
	base := [3]int{bx, by, bz}
	for k := 0; k < 9; k++ {
		if 1+k >= len(args) {
			r := errToken("parsing.int.expected", tokBase+1+k)
			return c, &r
		}
		v, err := parseCoord(args[1+k], base[k%3])
		if err != nil {
			r := errToken("parsing.int.expected", tokBase+1+k)
			return c, &r
		}
		c[k] = v
	}
	return c, nil
}

// chunkLoaded reports whether any play session in d is tracking the
// column (cx, cz) — mc-go's notion of a live chunk.
func (s *Server) chunkLoaded(d *dimension, cx, cz int) bool {
	if d == nil {
		return false
	}
	found := false
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay && sess.dimension() == d && sess.hasChunk(cx, cz) {
			found = true
			return false
		}
		return true
	})
	return found
}

// blocksMatch implements `if blocks`: every offset in the source box
// [start,end] is compared against the destination box that starts at
// dest. `masked` skips positions where the source holds air. An
// untracked column on either side fails the condition outright.
func (s *Server) blocksMatch(d *dimension, c [9]int, masked bool) bool {
	if d == nil {
		return false
	}
	sx0, sx1 := minmax(c[0], c[3])
	sy0, sy1 := minmax(c[1], c[4])
	sz0, sz1 := minmax(c[2], c[5])
	dx, dy, dz := sx1-sx0, sy1-sy0, sz1-sz0
	// Both regions must sit in tracked columns.
	for x := sx0 >> 4; x <= sx1>>4; x++ {
		for z := sz0 >> 4; z <= sz1>>4; z++ {
			if !s.chunkLoaded(d, x, z) {
				return false
			}
		}
	}
	for x := c[6] >> 4; x <= (c[6]+dx)>>4; x++ {
		for z := c[8] >> 4; z <= (c[8]+dz)>>4; z++ {
			if !s.chunkLoaded(d, x, z) {
				return false
			}
		}
	}
	for ox := 0; ox <= dx; ox++ {
		for oy := 0; oy <= dy; oy++ {
			for oz := 0; oz <= dz; oz++ {
				src := d.w.GetBlock(world.Pos{X: sx0 + ox, Y: sy0 + oy, Z: sz0 + oz})
				if masked && src == world.Air {
					continue
				}
				dst := d.w.GetBlock(world.Pos{X: c[6] + ox, Y: c[7] + oy, Z: c[8] + oz})
				if src != dst {
					return false
				}
			}
		}
	}
	return true
}

// executeIfItems evaluates `if items block <x> <y> <z> <slots> <pred>`
// and `if items entity <sel> <slots> <pred>`. Vanilla counts the
// matching (entity, slot) pairs over the whole slot range and reads the
// condition true when the count is nonzero, so a wildcard slots spec
// like weapon.* passes if either hand matches.
func (s *Server) executeIfItems(ctx *executeContext, player string, args []string, bx, by, bz, tokBase int) (consumed int, cond bool, err *reply) {
	if len(args) < 2 {
		r := textReply("Usage: /execute if items <block|entity> ...")
		return 0, false, &r
	}
	var stacks []ItemStack
	var next int
	sess := s.findSessionByName(player)
	switch args[1] {
	case "block":
		if len(args) < 7 {
			r := textReply("Usage: /execute if items block <x> <y> <z> <slots> <item>")
			return 0, false, &r
		}
		x, e := parseCoord(args[2], bx)
		if e != nil {
			r := textReply("Invalid x: " + args[2])
			return 0, false, &r
		}
		y, e := parseCoord(args[3], by)
		if e != nil {
			r := textReply("Invalid y: " + args[3])
			return 0, false, &r
		}
		z, e := parseCoord(args[4], bz)
		if e != nil {
			r := textReply("Invalid z: " + args[4])
			return 0, false, &r
		}
		slots, ok := expandSlotRange(args[5])
		if !ok {
			r := errTokenEnd("slot.unknown", tokBase+5, args[5])
			return 0, false, &r
		}
		d := ctx.dim
		if ctx.alive {
			pos := world.Pos{X: x, Y: y, Z: z}
			for _, id := range slots {
				// A non-container read misses like the slot doesn't
				// exist, the same way vanilla's container lookup just
				// contributes zero to the count.
				if st, _, ok := s.readItemBlock(d, pos, id); ok {
					stacks = append(stacks, st)
				}
			}
		}
		next = 6
	case "entity":
		if len(args) < 5 {
			r := textReply("Usage: /execute if items entity <sel> <slots> <item>")
			return 0, false, &r
		}
		slots, ok := expandSlotRange(args[3])
		if !ok {
			r := errTokenEnd("slot.unknown", tokBase+3, args[3])
			return 0, false, &r
		}
		if ctx.alive {
			if len(args[2]) > 0 && args[2][0] != '@' {
				if ts := s.findSessionByName(args[2]); ts == nil {
					// Missing player — the slot reads contribute
					// nothing, like an empty selector.
				} else {
					for _, id := range slots {
						if st, ok := readEntitySlot(ts, id); ok {
							stacks = append(stacks, st)
						}
					}
				}
			} else {
				cands, serr := s.selectDataEntities(sess, args[2], tokBase+2, false, ctx.dim)
				if serr != nil {
					return 0, false, serr
				}
				for _, c := range cands {
					if c.sess == nil {
						if c.mob == nil {
							continue
						}
						for _, id := range slots {
							if st := readMobSlot(c.mob, id); !st.Empty() {
								stacks = append(stacks, st)
							}
						}
						continue
					}
					for _, id := range slots {
						if st, ok := readEntitySlot(c.sess, id); ok {
							stacks = append(stacks, st)
						}
					}
				}
			}
		}
		next = 4
	default:
		r := textReply("Usage: /execute if items <block|entity> ...")
		return 0, false, &r
	}
	pred, perr := parseItemPredicate(args[next], tokBase+next)
	if perr != nil {
		return 0, false, perr
	}
	if ctx.alive {
		for _, st := range stacks {
			if itemPredicateMatch(pred, st) {
				cond = true
				break
			}
		}
	}
	return next + 1, cond, nil
}

// expandSlotRange turns a SlotsArgument token into global slot indices:
// a plain name, or a family.* wildcard expanding to every index in the
// family a player can hold.
func expandSlotRange(tok string) ([]int32, bool) {
	if id, ok := parseItemSlot(tok); ok {
		return []int32{id}, true
	}
	if tok == "*" {
		// Every index a player answers.
		var out []int32
		for i := int32(0); i <= 35; i++ {
			out = append(out, i)
		}
		out = append(out, itemSlotMainhand, itemSlotOffhand,
			itemSlotFeet, itemSlotLegs, itemSlotChest, itemSlotHead,
			itemSlotBody, itemSlotCursor)
		for i := int32(0); i <= 26; i++ {
			out = append(out, 200+i)
		}
		for i := int32(0); i <= 3; i++ {
			out = append(out, 500+i)
		}
		return out, true
	}
	prefix, ok := strings.CutSuffix(tok, ".*")
	if !ok {
		return nil, false
	}
	switch prefix {
	case "container":
		// container.N runs 0-53 in the registry; a player only
		// answers the first 36, a block only its own depth.
		var out []int32
		for i := int32(0); i <= 53; i++ {
			out = append(out, i)
		}
		return out, true
	case "inventory":
		var out []int32
		for i := int32(0); i <= 26; i++ {
			out = append(out, 9+i)
		}
		return out, true
	case "hotbar":
		var out []int32
		for i := int32(0); i <= 8; i++ {
			out = append(out, i)
		}
		return out, true
	case "enderchest":
		var out []int32
		for i := int32(0); i <= 26; i++ {
			out = append(out, 200+i)
		}
		return out, true
	case "weapon":
		return []int32{itemSlotMainhand, itemSlotOffhand}, true
	case "armor":
		return []int32{itemSlotFeet, itemSlotLegs, itemSlotChest, itemSlotHead, itemSlotBody}, true
	case "player.crafting":
		return []int32{500, 501, 502, 503}, true
	}
	return nil, false
}

// itemPredicate is the slice of the item_predicate grammar mc-go
// evaluates: an item id or #tag, plus a count=N exact-count test. Any
// other [component...] test parses but can only match components the
// stack model carries.
type itemPredicate struct {
	id      string
	tag     string
	count   int32
	hasCnt  bool
	damage  int32
	hasDmg  bool
	dmgLo   int32
	dmgHi   int32
	hasDmgB bool
	durLo   int32
	durHi   int32
	hasDurB bool
	unknown bool
}

// parseItemPredicate reads `<id|#tag>[<tests>]`. A wildcard path
// (minecraft:*) fails the id read where vanilla's reader stops — the
// argument.item.id.invalid argument is the parsed prefix.
func parseItemPredicate(tok string, tokNum int) (itemPredicate, *reply) {
	var p itemPredicate
	body := tok
	if i := strings.IndexByte(tok, '['); i >= 0 {
		body = tok[:i]
	}
	if strings.HasPrefix(body, "#") {
		p.tag = normalizeItemName(body[1:])
	} else {
		id := idPrefix(body)
		if id != body || id == "" {
			r := errTokenOff("argument.item.id.invalid", tokNum, len(id), id)
			return p, &r
		}
		id = normalizeItemName(id)
		// ItemIDs keys bare names; any other namespace misses the
		// registry the same way vanilla reports a bad id.
		if _, ok := world.ItemIDs[strings.TrimPrefix(id, "minecraft:")]; !ok {
			r := errTokenEnd("argument.item.id.invalid", tokNum, id)
			return p, &r
		}
		p.id = strings.TrimPrefix(id, "minecraft:")
	}
	if i := strings.IndexByte(tok, '['); i >= 0 {
		if !strings.HasSuffix(tok, "]") {
			r := errTokenEnd("command.unknown.argument", tokNum)
			return p, &r
		}
		for _, part := range strings.Split(tok[i+1:len(tok)-1], ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 && kv[0] == "count" {
				n, err := strconv.Atoi(kv[1])
				if err != nil {
					r := errTokenEnd("command.unknown.argument", tokNum)
					return p, &r
				}
				p.count, p.hasCnt = int32(n), true
				continue
			}
			if len(kv) == 2 && (kv[0] == "damage" || kv[0] == "minecraft:damage") {
				n, err := strconv.Atoi(kv[1])
				if err != nil {
					r := errTokenEnd("command.unknown.argument", tokNum)
					return p, &r
				}
				p.damage, p.hasDmg = int32(n), true
				continue
			}
			// The damage sub-predicate: [minecraft:damage~{damage:N}]
			// with MinMaxBounds — a bare int is exact, {min:N}/{max:N}
			// bound it. `durability` bounds maxDamage minus the damage.
			// A min,max pair inside the bounds splits on the comma and
			// stays unparsed, like other unsupported components.
			if i := strings.IndexByte(part, '~'); i >= 0 &&
				(part[:i] == "damage" || part[:i] == "minecraft:damage") {
				inner := strings.TrimSuffix(strings.TrimPrefix(part[i+1:], "{"), "}")
				fld, val, ok := strings.Cut(inner, ":")
				if !ok || (fld != "damage" && fld != "durability") {
					p.unknown = true
					continue
				}
				lo, hi, ok := predBounds(val)
				if !ok {
					p.unknown = true
					continue
				}
				if fld == "damage" {
					p.dmgLo, p.dmgHi, p.hasDmgB = lo, hi, true
				} else {
					p.durLo, p.durHi, p.hasDurB = lo, hi, true
				}
				continue
			}
			p.unknown = true
		}
	}
	return p, nil
}

// itemPredicateMatch tests one stack against the predicate. An empty
// slot never matches — vanilla's ItemPredicate.test bails on
// ItemStack.EMPTY before checking anything, so `air` reads false too.
func itemPredicateMatch(p itemPredicate, st ItemStack) bool {
	if st.Count <= 0 || st.ID <= 0 {
		return false
	}
	if p.unknown {
		return false
	}
	name, _ := itemNameOf(st.ID)
	if p.tag != "" && !data.HasItemTag(p.tag, name) {
		return false
	}
	if p.id != "" && name != p.id {
		return false
	}
	if p.hasCnt && st.Count != p.count {
		return false
	}
	if p.hasDmg && st.Damage != p.damage {
		return false
	}
	if p.hasDmgB && (st.Damage < p.dmgLo || st.Damage > p.dmgHi) {
		return false
	}
	if p.hasDurB {
		max, ok := damageableItems[name]
		remaining := int32(max) - st.Damage
		if !ok || remaining < p.durLo || remaining > p.durHi {
			return false
		}
	}
	return true
}

// predBounds parses a MinMaxBounds.Ints body as it shows up inside the
// damage sub-predicate: a bare int is exact, {min:N}/{max:N} bound one
// side, and {min:N,max:M} — which never reaches here in one part —
// would bound both.
func predBounds(v string) (int32, int32, bool) {
	lo, hi := int32(math.MinInt32), int32(math.MaxInt32)
	if n, err := strconv.Atoi(v); err == nil {
		return int32(n), int32(n), true
	}
	for _, kv := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(v, "{"), "}"), ",") {
		k, x, ok := strings.Cut(kv, ":")
		if !ok {
			return 0, 0, false
		}
		n, err := strconv.Atoi(x)
		if err != nil {
			return 0, 0, false
		}
		switch k {
		case "min":
			lo = int32(n)
		case "max":
			hi = int32(n)
		default:
			return 0, 0, false
		}
	}
	return lo, hi, true
}

// executeIfData evaluates `if data <block|entity|storage> ...` — the
// read must resolve and any path must exist.
func (s *Server) executeIfData(ctx *executeContext, player string, args []string, bx, by, bz, tokBase int) (consumed int, cond bool, err *reply) {
	if len(args) < 3 {
		r := textReply("Usage: /execute if data <block|entity|storage> ...")
		return 0, false, &r
	}
	sess := s.findSessionByName(player)
	var base any
	exists := false
	var next int
	switch args[1] {
	case "block":
		if len(args) < 5 {
			r := textReply("Usage: /execute if data block <x> <y> <z> [path]")
			return 0, false, &r
		}
		x, e := parseCoord(args[2], bx)
		if e != nil {
			r := textReply("Invalid x: " + args[2])
			return 0, false, &r
		}
		y, e := parseCoord(args[3], by)
		if e != nil {
			r := textReply("Invalid y: " + args[3])
			return 0, false, &r
		}
		z, e := parseCoord(args[4], bz)
		if e != nil {
			r := textReply("Invalid z: " + args[4])
			return 0, false, &r
		}
		next = 5
		if ctx.alive {
			if be, ok := s.blockEntitySaveMap(ctx.dim, world.Pos{X: x, Y: y, Z: z}); ok {
				base, exists = be, true
			}
		}
	case "entity":
		next = 3
		if ctx.alive {
			cands, serr := s.selectDataEntities(sess, args[2], tokBase+2, true, ctx.dim)
			if serr != nil {
				return 0, false, serr
			}
			if len(cands) > 0 {
				base, exists = cands[0].saveMap(s.clock.count, s.entities).m, true
			}
		}
	case "storage":
		if len(args) < 3 {
			r := textReply("Usage: /execute if data storage <id> [path]")
			return 0, false, &r
		}
		next = 3
		if ctx.alive {
			if s.storageExists(args[2]) {
				base, exists = s.storageRead(args[2]), true
			}
		}
	default:
		r := textReply("Usage: /execute if data <block|entity|storage> ...")
		return 0, false, &r
	}
	// The path arg is required — `if data storage x` swallows the
	// next token (even `run`) as the path, and the keyword it expected
	// then errors as an unknown argument in the chain.
	if len(args) <= next {
		r := errToken("command.unknown.argument", tokBase+next)
		return 0, false, &r
	}
	// A compound pattern is a subset match on the whole tag
	// (`if data block ~ ~ ~ {Foo:1}`), not a path — vanilla reads it
	// as NbtUtils.compareNbt(pattern, full, partial=true).
	if strings.HasPrefix(args[next], "{") {
		pat, n, perr := parseSNBTTag(args[next], 0)
		if perr != nil || pat == nil || pat.kind != snbtCompound || n != len(args[next]) {
			r := errToken("command.unknown.argument", tokBase+next)
			return 0, false, &r
		}
		if exists {
			if m, ok := base.(map[string]any); ok {
				cond = nbtSubset(m, snbtToValue(pat).(map[string]any))
			}
		}
		return next + 1, cond, nil
	}
	steps, ok := parseNBTPath(args[next])
	if !ok {
		r := errToken("command.unknown.argument", tokBase+next)
		return 0, false, &r
	}
	if exists {
		_, cond = nbtGet(base, steps)
	}
	return next + 1, cond, nil
}

// executeIfScore evaluates `if score <holder> <obj> matches <range>`
// and the binary `if score <h1> <o1> <op> <h2> <o2>` forms against the
// scoreboard. A missing objective or score reads false, the silent
// failure vanilla produces.
func (s *Server) executeIfScore(ctx *executeContext, player string, args []string, tokBase int) (consumed int, cond bool, err *reply) {
	if len(args) < 5 {
		r := textReply("Usage: /execute if score <holder> <obj> <matches|op> ...")
		return 0, false, &r
	}
	name := s.scoreHolderName(player, args[1])
	if name == "" {
		// An unresolvable holder reads as a failed condition.
		return 0, false, nil
	}
	if !ctx.alive {
		// Dead chain — still consume the right token count so the
		// parser stays aligned.
		if args[3] == "matches" {
			return 5, false, nil
		}
		if len(args) >= 6 {
			return 6, false, nil
		}
		return 0, false, nil
	}
	s.board.mu.Lock()
	e1, ok1 := s.board.scores[name][args[2]]
	_, objOK := s.board.objectives[args[2]]
	s.board.mu.Unlock()
	v1 := scoreValue(e1)
	// A missing objective is a real error, but only when the chain
	// ends on this condition — mid-chain it reads false silently.
	if !objOK {
		r := componentReply(commandError("arguments.objective.notFound", args[2]))
		ctx.condErr = &r
	}
	if args[3] == "matches" {
		if !ok1 {
			return 5, false, nil
		}
		lo, hi, ok := parseIntRange(args[4])
		if !ok {
			r := componentReply(commandError("command.unknown.argument"))
			return 0, false, &r
		}
		return 5, int64(v1) >= lo && int64(v1) <= hi, nil
	}
	if len(args) < 6 {
		r := textReply("Usage: /execute if score <h> <o> <op> <h2> <o2>")
		return 0, false, &r
	}
	name2 := s.scoreHolderName(player, args[4])
	if name2 == "" {
		return 6, false, nil
	}
	s.board.mu.Lock()
	e2, ok2 := s.board.scores[name2][args[5]]
	s.board.mu.Unlock()
	v2 := scoreValue(e2)
	if !ok1 || !ok2 {
		return 6, false, nil
	}
	switch args[3] {
	case "=":
		cond = v1 == v2
	case "<":
		cond = v1 < v2
	case "<=":
		cond = v1 <= v2
	case ">":
		cond = v1 > v2
	case ">=":
		cond = v1 >= v2
	default:
		r := componentReply(commandError("command.unknown.argument"))
		return 0, false, &r
	}
	return 6, cond, nil
}

// scoreHolderName maps a score-holder token to its scoreboard key: a
// session's name, a mob's UUID string, or a literal fake-player name.
func (s *Server) scoreHolderName(sender, tok string) string {
	if len(tok) == 0 {
		return ""
	}
	if tok[0] != '@' {
		return tok
	}
	cands, _ := s.selectDataEntities(s.findSessionByName(sender), tok, 0, true, nil)
	if len(cands) == 0 {
		return ""
	}
	if cands[0].sess != nil {
		return cands[0].sess.getName()
	}
	// A mob's scoreboard name is its UUID string.
	u := cands[0].mob.UUID
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// parseIntRange parses vanilla's IntRange forms: N, N.., ..N, N..M.
func parseIntRange(tok string) (lo, hi int64, ok bool) {
	if strings.Contains(tok, "..") {
		parts := strings.SplitN(tok, "..", 2)
		lo, hi = math.MinInt64, math.MaxInt64
		if parts[0] != "" {
			v, err := strconv.ParseInt(parts[0], 10, 64)
			if err != nil {
				return 0, 0, false
			}
			lo = v
		}
		if parts[1] != "" {
			v, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return 0, 0, false
			}
			hi = v
		}
		return lo, hi, true
	}
	v, err := strconv.ParseInt(tok, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return v, v, true
}

// matchesBlockPredicate reports whether the block at pos satisfies a
// predicate — vanilla's BlockPredicate is a subset test: the block
// name must match and every property the spec pinned must equal, with
// the rest wildcarded. props is nil for a bare name.
func (s *Server) matchesBlockPredicate(d *dimension, pos world.Pos, blk world.Block, props map[string]string) bool {
	if d == nil {
		return false
	}
	got := d.w.GetBlock(pos)
	want, ok1 := world.StateOwner(blk)
	have, ok2 := world.StateOwner(got)
	if !ok1 || !ok2 || want != have {
		return false
	}
	for k, v := range props {
		if hv, ok := world.StateProperty(got, k); !ok || hv != v {
			return false
		}
	}
	return true
}

// runInDimension dispatches a command with the execute context's
// dimension in force for the duration of the call. Teleport is the one
// command that must actually move the target across dimensions rather
// than just run with that context, so it bypasses the registry.
func (s *Server) runInDimension(player string, d *dimension, sub []string) reply {
	if source := s.commandFrame(player); source != nil {
		child := *source
		child.dim = d
		return s.withCommandFrame(&child, func() reply {
			if r, ok := s.runCommand(player, sub[0], sub[1:]); ok {
				return r
			}
			return componentReply(unknownCommand())
		})
	}
	switch sub[0] {
	case "tp", "teleport":
		// The cross-dimension player hop is the only form the legacy
		// stub carries: every other tp — entity targets, @s bound to a
		// forked mob, same-dimension moves — belongs to the real
		// command, which resolves selectors and echoes vanilla-style.
		if s.tpNeedsDimHop(player, d, sub[1:]) {
			return textReply(s.runExecuteTeleport(player, d, sub[1:]))
		}
	}
	sess := s.findSessionByName(player)
	if sess != nil && d != nil {
		prev := sess.dimension()
		sess.setDimension(d)
		if sess.dimSwapDepth.Add(1) == 1 {
			sess.dimSwapPrev.Store(prev)
		}
		defer func() {
			sess.setDimension(prev)
			if sess.dimSwapDepth.Add(-1) == 0 {
				sess.dimSwapPrev.Store(nil)
			}
		}()
	}
	if r, ok := s.runCommand(player, sub[0], sub[1:]); ok {
		return r
	}
	return componentReply(unknownCommand())
}

// tpNeedsDimHop reports whether an execute-leaf tp is the
// cross-dimension player form the runExecuteTeleport stub carries —
// "tp [@s|<player>] x y z" whose resolved target is a session standing
// in a different dimension. @s bound to a forked mob (or any mob
// target) is not a hop: entity teleports inside a chain stay handled
// by the real tp command.
func (s *Server) tpNeedsDimHop(player string, d *dimension, args []string) bool {
	if d == nil {
		return false
	}
	target := player
	if len(args) == 4 {
		target = args[0]
	} else if len(args) != 3 {
		return false
	}
	var ts *Session
	if target == "@s" || target == "@p" {
		sess := s.findSessionByName(player)
		if sess != nil && sess.execEnt != nil {
			if sess.execEnt.sess == sess {
				ts = sess
			}
			// A mob executor (or another player) is the real tp's job.
		} else {
			ts = sess
		}
	} else {
		ts = s.findSessionByName(target)
	}
	return ts != nil && ts.dimension() != d
}

// resolveTarget maps a selector token to a live session.
func (s *Server) resolveTarget(player, tok string) *Session {
	if source := s.commandFrame(player); source != nil && strings.HasPrefix(tok, "@") {
		entities, err := s.selectDataEntities(nil, tok, 0, false, source.dim)
		if err == nil && len(entities) != 0 {
			return entities[0].sess
		}
		return nil
	}
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
	s.registerComponentCommand("tick", func(player string, args []string) reply {
		return s.runTickOwned(player, args)
	})
	s.registerComponentCommand("time", func(player string, args []string) reply {
		return s.runTime(player, args)
	})
	s.registerComponentCommand("item", func(player string, args []string) reply {
		return s.runItem(player, args)
	})
	s.registerComponentCommand("data", func(player string, args []string) reply {
		return s.runData(player, args)
	})
	s.registerComponentCommand("team", func(player string, args []string) reply {
		return s.runTeam(player, args)
	})
	s.registerComponentCommand("teammsg", func(player string, args []string) reply {
		return s.runTeamMsg(player, args)
	})
	s.registerComponentCommand("tm", func(player string, args []string) reply {
		return s.runTeamMsg(player, args)
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
func (s *Server) runTickOwned(player string, args []string) reply {
	if len(args) == 0 {
		// A bare "/tick" stops at the end of its own name.
		return errTokenEnd("command.unknown.command", 0)
	}
	switch args[0] {
	case "freeze":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		// setFreeze(true) interrupts a sprint (its report still goes
		// out) and a step before the freeze lands.
		s.stopSprintOwned()
		s.stopStep()
		s.SetFrozen(true)
		return opsEchoReply(translatable("commands.tick.status.frozen"))
	case "unfreeze":
		if len(args) > 1 {
			return errToken("command.unknown.argument", 2)
		}
		s.SetFrozen(false)
		return opsEchoReply(translatable("commands.tick.status.running"))
	case "step":
		if len(args) >= 2 && args[1] == "stop" {
			if len(args) > 2 {
				return errToken("command.unknown.argument", 3)
			}
			if !s.stopStep() {
				return componentReply(commandError("commands.tick.step.stop.fail"))
			}
			return opsEchoReply(translatable("commands.tick.step.stop.success"))
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
		if !s.clock.isFrozen() {
			return componentReply(commandError("commands.tick.step.fail"))
		}
		// stepGameIfPaused fills the owed counter and refreshes the
		// clients before the ack — the ticking-step packet precedes
		// "Stepping N tick(s)" on the wire.
		s.clock.mu.Lock()
		s.clock.stepRemain = n
		s.clock.mu.Unlock()
		s.broadcastTickingStep(int32(n))
		if sess := s.findSessionByName(player); sess != nil {
			ack := translatable("commands.tick.step.success", int32(n))
			if s.gameRuleBool("minecraft:send_command_feedback") {
				s.sendSystemChatComponent(sess, ack)
				s.sendOpsEcho(sess, ack)
			}
			s.stepTicksOwned(n)
			r := reply{}
			r.result, r.hasResult = 1, true
			return r
		}
		s.stepTicksOwned(n)
		return opsEchoReply(translatable("commands.tick.step.success", int32(n)))
	case "sprint":
		if len(args) >= 2 && args[1] == "stop" {
			if len(args) > 2 {
				return errToken("command.unknown.argument", 3)
			}
			if !s.stopSprintOwned() {
				return componentReply(commandError("commands.tick.sprint.stop.fail"))
			}
			return opsEchoReply(translatable("commands.tick.sprint.stop.success"))
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
		// Vanilla runs the sprint on the server thread: the ack leaves
		// first — "Interrupted" ahead of it when the request cut into a
		// running sprint — and everything queued behind it waits out the
		// run. The interrupting request itself never runs a leg: the
		// sprint's owner keeps it and finishes once.
		interrupted := s.requestSprint(n)
		if sess := s.findSessionByName(player); sess != nil {
			if s.gameRuleBool("minecraft:send_command_feedback") {
				if interrupted {
					ack := translatable("commands.tick.sprint.stop.success")
					s.sendSystemChatComponent(sess, ack)
					s.sendOpsEcho(sess, ack)
				}
				ack := translatable("commands.tick.status.sprinting")
				s.sendSystemChatComponent(sess, ack)
				s.sendOpsEcho(sess, ack)
			}
			if !interrupted {
				s.sprintRunOwned()
			}
			r := reply{}
			r.result, r.hasResult = 1, true
			return r
		}
		if !interrupted {
			s.sprintRunOwned()
		}
		return opsEchoReply(translatable("commands.tick.status.sprinting"))
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
		return opsEchoReply(translatable("commands.tick.rate.success", formatRate(s.clock.tickRate())))
	case "query":
		return s.runTickQuery(args[1:])
	}
	return errToken("command.unknown.argument", 1)
}

// runTickQuery reports the clock state the way the real server does:
// the status line, the rate line (the sprinting form drops the target),
// and the P50/P95/P99 percentile line read off the tick-time ring.
func (s *Server) runTickQuery(args []string) reply {
	if len(args) > 0 {
		return errToken("command.unknown.argument", 2)
	}
	st := s.clock.status()
	rate := formatRate(st.Rate)
	avg := nanosToMillis(s.clock.avgTickNanos())
	target := nanosToMillis(int64(float64(time.Second) / st.Rate))
	p50, p95, p99, sample := s.clock.tickPercentiles()
	percentiles := translatable("commands.tick.query.percentiles",
		p50, p95, p99, int32(sample))
	running := translatable("commands.tick.query.rate.running", rate, avg, target)
	switch {
	case st.Sprinting:
		return componentReplyWith(translatable("commands.tick.status.sprinting"),
			translatable("commands.tick.query.rate.sprinting", rate, avg), percentiles)
	case st.Frozen:
		return componentReplyWith(translatable("commands.tick.status.frozen"),
			running, percentiles)
	case int64(float64(time.Second)/st.Rate) < s.clock.avgTickNanos():
		// nanosecondsPerTick under the average: the game runs, but
		// can't keep up with the target rate.
		return componentReplyWith(translatable("commands.tick.status.lagging"),
			running, percentiles)
	}
	return componentReplyWith(translatable("commands.tick.status.running"),
		running, percentiles)
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
	var filter func(*Server, *dimension, world.Pos) bool
	// The optional mode literal rides vanilla's grammar: bare,
	// "replace [<filter>] [<mode>]", or one of keep/hollow/outline/
	// destroy/strict. keep is REPLACE with an air-only predicate,
	// strict is REPLACE with the no-update write; hollow/outline/
	// destroy pick the FillCommand.Mode variant.
	mode := ""
	keepOnly := false
	if len(args) >= 8 {
		switch args[7] {
		case "replace":
			if len(args) >= 9 {
				var err *reply
				filter, err = parseBlockFilterPredicate(args[8], 8)
				if err != nil {
					return *err
				}
				if len(args) >= 10 {
					switch args[9] {
					case "hollow", "outline", "destroy", "strict":
						mode = args[9]
					default:
						return errTokenEnd("command.unknown.argument", 9)
					}
				}
			}
		case "keep":
			keepOnly = true
		case "hollow", "outline", "destroy", "strict":
			mode = args[7]
		default:
			return errTokenEnd("command.unknown.argument", 7)
		}
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
	changed := 0
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				if y < d.minY || y >= d.minY+d.height {
					continue
				}
				pos := world.Pos{X: x, Y: y, Z: z}
				old := d.w.GetBlock(pos)
				if filter != nil && !filter(s, d, pos) {
					continue
				}
				if keepOnly && old != world.Air {
					continue
				}
				// Mode.DESTROY's affector runs before the filter's
				// placement: the cell is destroyed (2001 + drops) even
				// when the placement turns out a no-op.
				affected := false
				if mode == "destroy" && old != world.Air {
					affected = s.fillDestroyCell(d, pos, old)
				}
				target := blk
				interior := x != minX && x != maxX && y != minY && y != maxY && z != minZ && z != maxZ
				switch {
				case mode == "hollow" && interior:
					// Mode.HOLLOW's filter hands interior cells the
					// HOLLOW_CORE air input.
					target = world.Air
				case mode == "outline" && interior:
					// Mode.OUTLINE's filter returns null: the interior
					// stays, counted only via the destroy affector.
					if affected {
						changed++
					}
					continue
				}
				// BlockInput.place's setBlock returns false on an
				// identical state, so same-state cells are not counted —
				// hollow air cores and keep-predicate no-ops alike.
				if d.w.GetBlock(pos) == target {
					if affected {
						changed++
					}
					continue
				}
				if mode == "strict" {
					// Mode strict: flag 816 joins 2|256 — no neighbour
					// updates, no updateFromNeighbourShapes fold.
					s.setSilentBlockWithPolicy(d, pos, target, true, true)
				} else {
					s.structureRetireBlockEntity(d, pos, d.w.GetBlock(pos), target)
					// /fill notifies neighbours with the NEW state
					// (updateNeighboursOnBlockSet).
					s.setNaturalBlockNotify(d, pos, target, target)
					// BlockInput.place's updateFromNeighbourShapes fold.
					s.selfFoldShapes(d, pos)
					s.selfCheckSurvival(d, pos, target)
				}
				changed++
			}
		}
	}
	if changed == 0 {
		return componentReply(commandError("commands.fill.failed"))
	}
	return opsEchoReply(translatable("commands.fill.success", int32(changed)))
}

// fillDestroyCell is /fill destroy's affector: vanilla's
// level.destroyBlock(pos, true) — the block's loot drops at the cell
// with an empty tool context (without the player mining-tool gate),
// then the destroy game event posts. The placement write lands after
// in the caller's loop.
func (s *Server) fillDestroyCell(d *dimension, pos world.Pos, broken world.Block) bool {
	if commandAir(broken) {
		return false
	}
	bname, _ := world.StateOwner(broken)
	if bname != "fire" && bname != "soul_fire" {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(broken))
	}
	_, hasLoot := data.LootTables()["blocks/"+bname]
	// The container removal path creates the component-bearing shulker
	// item itself; the generic loot item would duplicate that box.
	blockDrops := s.gameRuleBool("minecraft:block_drops")
	if hasLoot && !isShulkerBoxBlock(broken) && blockDrops {
		ctx := &lootCtx{
			tool:  ItemStack{},
			block: bname,
			state: broken,
			pos:   pos,
			dim:   d,
			rng:   rand.New(rand.NewSource(rand.Int63())),
		}
		for _, st := range rollLootDepth(ctx, "blocks/"+bname, 0) {
			s.spawnItemDrop(d.name, pos, st)
		}
	}
	remaining := world.Air
	if kind, level, has := fluidOf(broken); has {
		remaining = fluidState(kind, level)
	}
	if isShulkerBoxBlock(broken) && !blockDrops {
		// A shulker's boxed inventory belongs to its resource drop;
		// ordinary container pre-removal contents still spill below.
		s.structureRetireBlockEntity(d, pos, broken, remaining)
	}
	s.setBlockAndBroadcastInOwned(d, "", pos, remaining, false)
	if remaining != broken {
		s.emitGameEvent(d, "block_destroy", float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, nil, bname)
	}
	return remaining != broken
}

func commandAir(state world.Block) bool {
	name, _ := world.StateOwner(state)
	return name == "air" || name == "cave_air" || name == "void_air"
}

func minmax(a, b int) (int, int) {
	if a <= b {
		return a, b
	}
	return b, a
}

// executeStore handles `store (result|success) <target>`: the target's
// args parse eagerly — a bad block entity, missing bossbar or unknown
// objective fails before the trailing chain runs — then the tail runs
// and its leaf's numeric result (or success bit) is written back. A
// tail that never reached a leaf — a dead chain, a parse error —
// propagates its reply untouched and stores nothing.
func (s *Server) executeStore(ctx *executeContext, player string, args []string, tokBase, bx, by, bz int) reply {
	if len(args) < 2 {
		return errToken("command.unknown.argument", tokBase+len(args))
	}
	wantResult := args[0] == "result"
	if !wantResult && args[0] != "success" {
		return errToken("command.unknown.argument", tokBase)
	}
	// parseNBT type + scale tail shared by the storage/entity/block forms.
	parseTypedPath := func(at int) (steps []nbtPathStep, typ string, scale float64, consumed int, r *reply) {
		if len(args) <= at {
			return nil, "", 0, 0, errp(errToken("command.unknown.argument", tokBase+at))
		}
		steps, ok := parseNBTPath(args[at])
		if !ok {
			return nil, "", 0, 0, errp(errToken("command.unknown.argument", tokBase+at))
		}
		if len(args) <= at+1 {
			return nil, "", 0, 0, errp(errToken("command.unknown.argument", tokBase+at+1))
		}
		switch args[at+1] {
		case "byte", "short", "int", "long", "float", "double":
			typ = args[at+1]
		default:
			return nil, "", 0, 0, errp(errToken("command.unknown.argument", tokBase+at+1))
		}
		if len(args) <= at+2 {
			return nil, "", 0, 0, errp(errToken("parsing.double.expected", tokBase+at+2))
		}
		v, err := strconv.ParseFloat(args[at+2], 64)
		if err != nil {
			return nil, "", 0, 0, errp(errToken("parsing.double.expected", tokBase+at+2))
		}
		return steps, typ, v, at + 3, nil
	}

	var write func(v int64)
	var next int
	switch args[1] {
	case "storage":
		if len(args) < 3 {
			return errToken("command.unknown.argument", tokBase+2)
		}
		id := normalizeItemName(args[2])
		steps, typ, scale, n, r := parseTypedPath(3)
		if r != nil {
			return *r
		}
		next = n
		write = func(v int64) {
			m := s.storageWrite(id)
			nbtSet(m, steps, typedStoreValue(typ, v, scale))
		}
	case "score":
		if len(args) < 4 {
			return errToken("command.unknown.argument", tokBase+len(args))
		}
		holder := s.scoreHolderName(player, args[2])
		objective := args[3]
		s.board.mu.Lock()
		_, known := s.board.objectives[objective]
		s.board.mu.Unlock()
		// An unknown objective fails at parse time — the leaf never runs.
		if !known {
			return componentReply(commandError("arguments.objective.notFound", objective))
		}
		if holder == "" {
			return reply{}
		}
		next = 4
		write = func(v int64) {
			s.board.mu.Lock()
			s.setScoreLocked(holder, objective, int32(v))
			s.board.mu.Unlock()
			s.broadcastScore(holder, objective, int32(v))
		}
	case "entity":
		if len(args) < 3 {
			return errToken("command.unknown.argument", tokBase+2)
		}
		sel := args[2]
		steps, typ, scale, n, r := parseTypedPath(3)
		if r != nil {
			return *r
		}
		next = n
		write = func(v int64) {
			cands, serr := s.selectDataEntities(s.findSessionByName(player), sel, tokBase+2, true, ctx.dim)
			if serr != nil || len(cands) == 0 || cands[0].sess != nil {
				return // players are unwritable; no match stores nothing
			}
			save := cands[0].saveMap(s.clock.count, s.entities)
			if nbtSet(save.m, steps, typedStoreValue(typ, v, scale)) {
				s.applyDataEntity(cands[0], save)
			}
		}
	case "block":
		x, y, z, cerr := blockPosArgs(args[1:], bx, by, bz, tokBase+1)
		if cerr != nil {
			return *cerr
		}
		steps, typ, scale, n, r := parseTypedPath(5)
		if r != nil {
			return *r
		}
		pos := world.Pos{X: x, Y: y, Z: z}
		be, ok := s.blockEntitySaveMap(ctx.dim, pos)
		if !ok {
			return componentReply(commandError("commands.data.block.invalid"))
		}
		delete(be, "keepPacked")
		next = n
		write = func(v int64) {
			if nbtSet(be, steps, typedStoreValue(typ, v, scale)) {
				s.restoreBlockEntity(ctx.dim, be)
				s.queueBlockEntityDirty(ctx.dim, pos)
			}
		}
	case "bossbar":
		if len(args) < 4 {
			return errToken("command.unknown.argument", tokBase+len(args))
		}
		if args[3] != "value" && args[3] != "max" {
			return errToken("command.unknown.argument", tokBase+3)
		}
		// mc-go keeps no bossbar table, so every id is unknown.
		return componentReply(commandError("commands.bossbar.unknown", args[2]))
	default:
		return errToken("command.unknown.argument", tokBase+1)
	}
	if len(args) <= next {
		return errToken("command.unknown.argument", tokBase+next)
	}
	tail := s.execTail(ctx, player, args[next:], tokBase+next)
	if tail.fail != nil {
		return tail
	}
	v := int64(tail.result)
	if wantResult {
		if !tail.hasResult {
			return tail
		}
	} else {
		failure := tail.outcome == commandFailed || tail.comp.commandFailure
		success := !failure && (tail.outcome == commandSucceeded || tail.hasResult)
		if !success && !failure {
			return tail
		}
		if success {
			v = 1
		} else {
			v = 0
		}
	}
	write(v)
	return tail
}

func errp(r reply) *reply { return &r }

// typedStoreValue coerces a stored result into the NBT numeric type
// the store target declared: value = trunc(result*scale), wrapped into
// the type's width like the real server's casts.
func typedStoreValue(typ string, v int64, scale float64) any {
	f := float64(v) * scale
	switch typ {
	case "byte":
		return int8(int64(f))
	case "short":
		return int16(int64(f))
	case "int":
		return int32(int64(f))
	case "long":
		return int64(f)
	case "float":
		return float32(f)
	case "double":
		return f
	}
	return int32(int64(f))
}

// execRun hands the trailing command to the dispatcher with the
// chain's frame bound on the sender's session: the forked executor for
// @s reads, the forked origin for ~, restored after.
func (s *Server) execRun(ctx *executeContext, player string, sub []string, subTok int) reply {
	return s.functionExecuteLeaf(player, sub, func() reply {
		return s.execRunNow(ctx, player, sub, subTok)
	})
}

func (s *Server) execRunNow(ctx *executeContext, player string, sub []string, subTok int) reply {
	if source := s.commandFrame(player); source != nil && (!s.commandExists(sub[0]) || source.level < commandLevel(sub[0])) {
		return errToken("command.unknown.argument", subTok)
	}
	sess := s.findSessionByName(player)
	if sess == nil {
		return rebaseExecuteLeaf(s.runInDimension(player, ctx.dim, sub), subTok)
	}
	prevEnt, prevPos := sess.execEnt, sess.execPos
	sess.execEnt = ctx.ent
	pos := ctx.pos
	sess.execPos = &pos
	defer func() { sess.execEnt, sess.execPos = prevEnt, prevPos }()
	// The run subcommand parses with the chain: an unknown command
	// name is a chain parse error at the name token, not the outer
	// dispatch's unknown.command.
	if !s.commandExists(sub[0]) {
		return errToken("command.unknown.argument", subTok)
	}
	return rebaseExecuteLeaf(s.runInDimension(player, ctx.dim, sub), subTok)
}

// commandExists reports whether name dispatches to a registered
// command without running it.
func (s *Server) commandExists(name string) bool {
	if s.componentCommand(name) != nil {
		return true
	}
	for _, n := range s.commands.Names() {
		if n == name {
			return true
		}
	}
	return false
}

// mergeReplies folds a fork's reply into the accumulator — components
// append in order so multi-fork leaves read like the real server's
// back-to-back messages. The result fields track the last fork.
func mergeReplies(dst *reply, src reply) {
	if src.fail == nil && (src.outcome == commandFailed || src.comp.commandFailure) {
		// A forked leaf's runtime failure replies nothing — the
		// error only lands for a single-source (redirect) chain.
		dst.result, dst.hasResult = src.result, src.hasResult
		return
	}
	if src.fail != nil && dst.fail == nil {
		dst.fail = src.fail
	}
	if src.styled {
		if !dst.styled && dst.text == "" {
			dst.comp, dst.styled = src.comp, true
		} else {
			dst.extra = append(dst.extra, src.comp)
		}
	}
	if src.text != "" {
		dst.extra = append(dst.extra, literal(src.text))
	}
	dst.extra = append(dst.extra, src.extra...)
	dst.result, dst.hasResult = src.result, src.hasResult
}

// dataEntityPos returns the entity's feet position and its dimension.
func (d *dataEntity) position() [3]float64 {
	if d.sess != nil {
		x, y, z, _, _, _ := d.sess.Pos()
		return [3]float64{x, y, z}
	}
	return [3]float64{d.mob.X, d.mob.Y, d.mob.Z}
}

func (d *dataEntity) dimOf(s *Server) *dimension {
	if d.sess != nil {
		return d.sess.dimension()
	}
	return s.dimensionByName(d.mob.Dim)
}

// executeFork runs the entity-forking modifiers — `as` rebinds the
// executor, `at` additionally rebinds origin and dimension — running
// the tail once per matched entity like vanilla's chain forks.
func (s *Server) executeFork(ctx *executeContext, player, kind string, args []string, tokBase int) reply {
	if len(args) < 1 {
		return errToken("command.unknown.argument", tokBase)
	}
	sess := s.findSessionByName(player)
	var cands []*dataEntity
	if len(args[0]) > 0 && args[0][0] != '@' {
		// A bare player name resolves like `if entity` does.
		if ts := s.findSessionByName(args[0]); ts != nil {
			cands = []*dataEntity{{sess: ts}}
		}
	} else {
		var serr *reply
		cands, serr = s.selectDataEntities(sess, args[0], tokBase, false, ctx.dim)
		if serr != nil {
			return *serr
		}
	}
	if !ctx.alive || len(cands) == 0 || sess == nil && s.commandFrame(player) == nil {
		return reply{}
	}
	var merged reply
	for _, c := range cands {
		sub := *ctx
		sub.forked = true
		var prevEnt *dataEntity
		var prevPos *[3]float64
		if sess != nil {
			prevEnt, prevPos = sess.execEnt, sess.execPos
		}
		if kind == "at" {
			// at re-anchors the origin only: the executing entity
			// (@s) stays the sender — position and dimension change
			// to the anchor's, the executor does not.
			sub.pos = c.position()
			sub.dim = c.dimOf(s)
			sub.rotation = entityCommandRotation(c)
		} else {
			// as swaps ONLY the executor: position and dimension
			// stay at the command source — `as @e run tp ~ ~ ~`
			// moves each mob TO the sender, not to itself.
			sub.ent = c
			if sess != nil {
				sess.execEnt = c
			}
		}
		p := sub.pos
		if sess != nil {
			sess.execPos = &p
		}
		r := s.execTail(&sub, player, args[1:], tokBase+1)
		if sess != nil {
			sess.execEnt, sess.execPos = prevEnt, prevPos
		}
		if r.fail != nil {
			return r
		}
		mergeReplies(&merged, r)
	}
	return merged
}

// executeSummon implements `execute summon <entity>`: the mob spawns at
// the chain origin and the tail runs once with it as the executor.
func (s *Server) executeSummon(ctx *executeContext, player string, args []string, tokBase int) reply {
	if len(args) < 1 {
		return errToken("command.unknown.argument", tokBase)
	}
	sess := s.findSessionByName(player)
	// The chain must continue past summon — a bare `summon` leaf is
	// the whole-input unknown.command, and nothing spawns.
	if len(args) < 2 {
		return errTokenEnd("command.unknown.command", tokBase+len(args))
	}
	name := strings.TrimPrefix(normalizeItemName(args[0]), "minecraft:")
	// EntityType.Builder.noSummon: player and fishing_bobber reject
	// with entity.not_summonable even through the execute form.
	if name == "player" || name == "fishing_bobber" {
		return componentReply(commandError("entity.not_summonable", "minecraft:"+name))
	}
	e, ok := s.summonMobRaw(ctx.dim, name, ctx.pos[0], ctx.pos[1], ctx.pos[2], false, true)
	if !ok {
		return errTokenEnd("argument.resource.not_found", tokBase, args[0], "minecraft:entity_type")
	}
	// The execute form always summons without NBT (initialize=true),
	// so Mob.finalizeSpawn's variant rolls run like a bare /summon.
	if v := rollSpawnVariant(name, strings.TrimPrefix(ctx.dim.w.Biome(), "minecraft:")); v != "" {
		e.VariantName = v
	}
	e.SoundVariant = rollSoundVariant(name)
	if !ctx.alive || sess == nil && s.commandFrame(player) == nil {
		return reply{}
	}
	sub := *ctx
	sub.ent = &dataEntity{mob: e}
	if sess == nil {
		return s.execTail(&sub, player, args[1:], tokBase+1)
	}
	prevEnt, prevPos := sess.execEnt, sess.execPos
	sess.execEnt = sub.ent
	p := sub.pos
	sess.execPos = &p
	defer func() { sess.execEnt, sess.execPos = prevEnt, prevPos }()
	return s.execTail(&sub, player, args[1:], tokBase+1)
}

// executePositioned implements `positioned <x> <y> <z>` | `positioned
// as <sel>` | `positioned over <heightmap>` — it moves the chain
// origin; the entity and heightmap forms follow vanilla's variants.
func (s *Server) executePositioned(ctx *executeContext, player string, args []string, tokBase int) reply {
	if len(args) < 1 {
		return errToken("command.unknown.argument", tokBase)
	}
	switch args[0] {
	case "as":
		if len(args) < 2 {
			return errToken("command.unknown.argument", tokBase+1)
		}
		sess := s.findSessionByName(player)
		cands, serr := s.selectDataEntities(sess, args[1], tokBase+1, false, ctx.dim)
		if serr != nil {
			return *serr
		}
		if !ctx.alive || len(cands) == 0 || sess == nil && s.commandFrame(player) == nil {
			return reply{}
		}
		var merged reply
		for _, c := range cands {
			sub := *ctx
			sub.pos = c.position()
			sub.forked = true
			if sess == nil {
				r := s.execTail(&sub, player, args[2:], tokBase+2)
				if r.fail != nil {
					return r
				}
				mergeReplies(&merged, r)
				continue
			}
			prevPos := sess.execPos
			p := sub.pos
			sess.execPos = &p
			r := s.execTail(&sub, player, args[2:], tokBase+2)
			sess.execPos = prevPos
			if r.fail != nil {
				return r
			}
			mergeReplies(&merged, r)
		}
		return merged
	case "over":
		if len(args) < 2 {
			return errToken("command.unknown.argument", tokBase+1)
		}
		switch args[1] {
		case "world_surface", "motion_blocking", "motion_blocking_no_leaves", "ocean_floor":
		default:
			return errTokenEnd("argument.enum.invalid", tokBase+1, args[1])
		}
		x, z := int(math.Floor(ctx.pos[0])), int(math.Floor(ctx.pos[2]))
		ctx.pos[1] = float64(s.surfaceY(ctx.dim, x, z) + 1)
		return s.execTail(ctx, player, args[2:], tokBase+2)
	}
	if len(args) < 3 {
		return errToken("parsing.double.expected", tokBase+len(args))
	}
	ctx.anchorEyes = false
	for k := 0; k < 3; k++ {
		v, err := parseDouble(args[k], ctx.pos[k], k != 1)
		if err != nil {
			return errToken("parsing.double.expected", tokBase+k)
		}
		ctx.pos[k] = v
	}
	return s.execTail(ctx, player, args[3:], tokBase+3)
}

// executeAim refines source rotation; entity-sourced forms fork per match.
func (s *Server) executeAim(ctx *executeContext, player, kind string, args []string, tokBase int) reply {
	if len(args) < 1 {
		return errToken("command.unknown.argument", tokBase)
	}
	if kind == "rotated" && args[0] == "as" {
		return s.executeRotatedAs(ctx, player, args[1:], tokBase+1)
	}
	if kind == "facing" && args[0] == "entity" {
		if len(args) < 3 {
			return errToken("command.unknown.argument", tokBase+len(args))
		}
		if args[2] != "eyes" && args[2] != "feet" {
			return errToken("argument.anchor.invalid", tokBase+2, args[2])
		}
		return s.forkOver(ctx, player, append([]string{args[1]}, args[3:]...), tokBase+1)
	}
	need := 2 // rotated <yaw> <pitch>
	if kind == "facing" {
		need = 3 // facing <x> <y> <z>
	}
	if len(args) < need {
		return errToken("parsing.double.expected", tokBase+len(args))
	}
	for k := 0; k < need; k++ {
		// rotated's yaw/pitch are RotationArgument (no centering);
		// facing's position is vec3() — x/z center, y does not.
		base := float64(0)
		if kind == "rotated" {
			base = float64(ctx.rotation[k])
		}
		value, err := parseDouble(args[k], base, need == 3 && k != 1)
		if err != nil {
			return errToken("parsing.double.expected", tokBase+k)
		}
		if kind == "rotated" {
			ctx.rotation[k] = float32(value)
		}
	}
	return s.execTail(ctx, player, args[need:], tokBase+need)
}

// forkOver re-runs the tail once per matched entity — the shared
// per-entity loop for the entity-sourced rotated/facing forms.
func (s *Server) forkOver(ctx *executeContext, player string, args []string, tokBase int) reply {
	if len(args) < 1 {
		return errToken("command.unknown.argument", tokBase)
	}
	sess := s.findSessionByName(player)
	cands, serr := s.selectDataEntities(sess, args[0], tokBase, false, ctx.dim)
	if serr != nil {
		return *serr
	}
	if !ctx.alive || len(cands) == 0 {
		return reply{}
	}
	var merged reply
	for range cands {
		sub := *ctx
		sub.forked = true
		r := s.execTail(&sub, player, args[1:], tokBase+1)
		if r.fail != nil {
			return r
		}
		mergeReplies(&merged, r)
	}
	return merged
}

// surfaceY returns the topmost non-air block's y for the column — the
// value `positioned over` raises the origin past. Nothing found means
// the build floor.
func (s *Server) surfaceY(d *dimension, x, z int) int {
	if d == nil {
		return 0
	}
	for y := d.minY + d.height - 1; y >= d.minY; y-- {
		if d.w.GetBlock(world.Pos{X: x, Y: y, Z: z}) != world.Air {
			return y
		}
	}
	return d.minY
}
