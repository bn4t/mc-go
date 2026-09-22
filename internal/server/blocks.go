package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"mc-go/internal/world"
)

// BlockChange is published on TopicBlockChange for plugins. PX/PY/PZ
// snapshot the actor's feet position for reach checks.
type BlockChange struct {
	Player     string
	X, Y, Z    int
	Block      world.Block
	PX, PY, PZ float64
}

// parseBlockSpec maps a user token to a world block: a block name with
// an optional vanilla property list, e.g. "water[level=3]",
// "oak_stairs[facing=east,half=top]", or a bare "stone".
//
// props reports the properties the token pinned (nil for a bare name):
// vanilla's BlockPredicate is a subset test, so only named properties
// are compared and the rest are wildcards — "minecraft:cave_vines"
// matches any cave vine and "cave_vines[age=2]" ignores berries.
func parseBlockSpec(tok string) (blk world.Block, props map[string]string, ok bool) {
	s := strings.ToLower(strings.TrimSpace(tok))
	s = strings.TrimPrefix(s, "minecraft:")
	name := s
	if i := strings.IndexByte(s, '['); i >= 0 {
		if !strings.HasSuffix(s, "]") {
			return world.Air, nil, false
		}
		name = strings.TrimSpace(s[:i])
		body := strings.TrimSpace(s[i+1 : len(s)-1])
		props = map[string]string{}
		if body != "" {
			for _, kv := range strings.Split(body, ",") {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return world.Air, nil, false
				}
				props[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	// "grass" is the long-standing alias for grass_block.
	if name == "grass" {
		name = "grass_block"
	}
	b, ok := world.BlockStateOf(name, props)
	if !ok {
		return world.Air, nil, false
	}
	return b, props, true
}

// parseBlockName resolves a token to a concrete state, the default state
// for a bare name.
func parseBlockName(tok string) (world.Block, bool) {
	b, _, ok := parseBlockSpec(tok)
	return b, ok
}

// parseCoord resolves one axis token to an absolute block coordinate.
// Vanilla subset: "12" absolute, "~" base, "~-3"/"~2" relative.
// base is the sender's block coordinate (floor of position).
func parseCoord(tok string, base int) (int, error) {
	if tok == "~" {
		return base, nil
	}
	if strings.HasPrefix(tok, "~") {
		off, err := strconv.Atoi(strings.TrimPrefix(tok, "~"))
		if err != nil {
			return 0, err
		}
		return base + off, nil
	}
	v, err := strconv.Atoi(tok)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// findSessionByName returns the live session for a player name.
func (s *Server) findSessionByName(name string) *Session {
	var found *Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.getName() == name {
			found = sess
			return false
		}
		return true
	})
	return found
}

// baseBlockPos returns the sender's block coordinates for "~" resolution.
func (s *Server) baseBlockPos(player string) (int, int, int) {
	if sess := s.findSessionByName(player); sess != nil {
		if sess.execPos != nil {
			p := sess.execPos
			return int(math.Floor(p[0])), int(math.Floor(p[1])), int(math.Floor(p[2]))
		}
		if sess.execEnt != nil && sess.execEnt.mob != nil {
			m := sess.execEnt.mob
			return int(math.Floor(m.X)), int(math.Floor(m.Y)), int(math.Floor(m.Z))
		}
		x, y, z, _, _, _ := sess.Pos()
		return int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	}
	// Fallback to world spawn when the sender has no session (tests).
	sx, sy, sz := s.WorldSpawn()
	return int(math.Floor(sx)), int(math.Floor(sy)), int(math.Floor(sz))
}

// dimOf returns the acting player's dimension, or the overworld when
// the name has no session (tests, console).
func (s *Server) dimOf(player string) *dimension {
	if sess := s.findSessionByName(player); sess != nil {
		return sess.dimension()
	}
	return s.dims[dimOverworld]
}

// runSetblock implements "/setblock <x> <y> <z> <block>".
// It updates the sharded world, publishes a BlockChange event, and fans
// a Block Update out to tracking sessions (vanilla behavior for single
// edits; full columns still stream via Chunk Data on join/move).
func (s *Server) runSetblock(player string, args []string) reply {
	if len(args) != 4 {
		return textReply("Usage: /setblock <x> <y> <z> <stone|dirt|grass|air>")
	}
	bx, by, bz := s.baseBlockPos(player)
	x, err := parseCoord(args[0], bx)
	if err != nil {
		return textReply("Invalid x: " + args[0])
	}
	y, err := parseCoord(args[1], by)
	if err != nil {
		return textReply("Invalid y: " + args[1])
	}
	z, err := parseCoord(args[2], bz)
	if err != nil {
		return textReply("Invalid z: " + args[2])
	}
	d := s.dimOf(player)
	if y < d.minY || y > d.minY+d.height-1 {
		return textReply(fmt.Sprintf("Y out of range %d..%d", d.minY, d.minY+d.height-1))
	}
	// World border, matching movement validation.
	const border = 29999984
	if x < -border || x > border || z < -border || z > border {
		return textReply("Position outside world border")
	}
	// The block token may carry trailing block-entity data in braces:
	// "chest{LootTable:\"minecraft:chests/bastion_bridge\"}".
	spec := args[3]
	var tag *snbtTag
	if i := strings.IndexByte(spec, '{'); i >= 0 {
		t, next, err := parseSNBTTag(spec[i:], 0)
		if err != nil || next != len(spec[i:]) {
			return textReply("Invalid block data: " + args[3])
		}
		tag, spec = t, spec[:i]
	}
	blk, ok := parseBlockName(spec)
	if !ok {
		return textReply("Unknown block: " + args[3])
	}
	pos := world.Pos{X: x, Y: y, Z: z}
	// /setblock notifies neighbours with the NEW state — vanilla's
	// updateNeighboursOnBlockSet reads the cell post-write.
	if affected := s.setBlockAndBroadcastNotify(player, pos, blk, true); len(affected) > 0 {
		s.sendLightUpdate(d, pos.X>>4, pos.Z>>4, affected)
	}
	if tag != nil && isChestBlock(blk) {
		c := s.chestAt(d, pos)
		if lt, ok := tag.vals["LootTable"]; ok && lt.kind == snbtString {
			c.lootTable = lt.str
		}
		if sd, ok := tag.vals["LootSeed"]; ok && sd.kind == snbtNumber {
			if n, err := strconv.ParseInt(sd.num, 10, 64); err == nil {
				c.lootSeed = n
			}
		}
	}
	if isBeeNestBlock(blk) {
		if d.bees == nil {
			d.bees = map[world.Pos]int32{}
		}
		if tag != nil {
			// The block entity's save key is `bees` (lowercase, 26.2).
			if list, ok := tag.vals["bees"]; ok && list.kind == snbtList {
				d.bees[pos] = int32(len(list.items))
			} else {
				delete(d.bees, pos)
			}
		} else {
			delete(d.bees, pos)
		}
	}
	// Success is the real server's component, three plain ints - which
	// is what /item's own fixture setup compares.
	return componentReply(translatable("commands.setblock.success", int32(x), int32(y), int32(z)))
}

// broadcastChunk resends one column to every play-stage session holding
// it in dimension d.
func (s *Server) broadcastChunk(d *dimension, cx, cz int) {
	chunk := encodeChunk(d, cx, cz, s.chunkClientBlockEntities(d, cx, cz))
	light := encodeLightFor(d, cx, cz)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.hasChunk(cx, cz) {
			return true
		}
		_ = sess.write(sess.ids().PlayChunkData, chunk)
		_ = sess.write(sess.ids().PlayUpdateLight, light)
		return true
	})
}

// registerBuiltins adds vanilla-subset commands that are always present:
// /help, /list, /pos, /setblock, /tp, /kick, /save-all, /stop. Plugin
// commands registered later may override these names.
func (s *Server) registerBuiltins() {
	s.registerOps()
	s.registerAdvancementCommand()
	s.commands.Register("help", func(player string, args []string) string {
		return "Commands: " + strings.Join(s.helpNames(), ", ")
	})
	s.commands.Register("pos", func(player string, args []string) string {
		sess := s.findSessionByName(player)
		if sess == nil {
			return "Unknown player"
		}
		x, y, z, yaw, pitch, _ := sess.Pos()
		return fmt.Sprintf("%.2f %.2f %.2f yaw=%.1f pitch=%.1f", x, y, z, yaw, pitch)
	})
	s.registerComponentCommand("setblock", func(player string, args []string) reply {
		return s.runSetblock(player, args)
	})
	s.registerComponentCommand("give", func(player string, args []string) reply {
		return s.runGive(player, args)
	})
	s.registerComponentCommand("summon", func(player string, args []string) reply {
		return s.runSummon(player, args)
	})
	// /execute forwards whatever the inner command produced, component
	// or text, so the inner reply reaches the client unchanged.
	s.registerComponentCommand("execute", func(player string, args []string) reply {
		return s.runExecute(player, args)
	})
	s.registerTextCommands()
	s.registerUtilCommands()
	s.registerSpawnCommands()
	s.registerGameRuleCommand()
	s.registerAdminCommands()
	s.registerTagCommand()
	s.registerWorldBorder()
	s.registerBossbar()
	registerFxCommands(s)
	registerMiscCommands(s)
	s.registerEntCommands()
	s.registerInvCommands()
	s.registerWorldStateCommands()
	s.registerAdminTwoCommands()
	s.registerComponentCommand("scoreboard", func(player string, args []string) reply {
		return s.runScoreboard(player, args)
	})
	s.registerComponentCommand("kill", func(player string, args []string) reply {
		return s.runKill(player, args)
	})
}

// runGive implements /give [target] <item> [count]: the item joins the
// target's inventory through the normal pickup path, syncing the slots,
// and the reply is vanilla's commands.give.success.single component.
// A leading token that resolves as a target (@s or a player name) is
// the recipient; without one the sender receives the item.
func (s *Server) runGive(player string, args []string) reply {
	sess := s.findSessionByName(player)
	if len(args) > 0 {
		if ts := s.resolveTarget(player, args[0]); ts != nil {
			sess = ts
			args = args[1:]
		}
	}
	if len(args) == 0 {
		return reply{text: "Usage: /give [target] <item> [count]"}
	}
	st, r, ok := s.parseItemStackMode(args, 1, true)
	if !ok {
		return r
	}
	if sess == nil {
		return reply{text: "Unknown player: " + player}
	}
	max := maxStackOfID(st.ID)
	if st.Count > max*100 {
		// commands.give.failed.toomanyitems — the cap is 100 stacks.
		return componentReply(commandError("commands.give.failed.toomanyitems"))
	}
	for remaining := st.Count; remaining > 0; {
		k := remaining
		if k > max {
			k = max
		}
		chunk := st
		chunk.Count = k
		remainder, _ := sess.addStack(chunk)
		if !remainder.Empty() {
			// Vanilla GiveCommand.giveItem: the leftover drops at the
			// player (no pickup delay, Owner set to the recipient).
			if e := s.dropItemLocked(sess, remainder); e != nil {
				e.PickupDelay = 0
				e.Owner = sess.getUUID()
				e.HasOwner = true
			}
		} else if e := s.dropItemLocked(sess, chunk); e != nil {
			// Fully added chunks still spawn a fake visual toss —
			// makeFakeItem = never pick up, despawn next tick.
			e.FakeItem = true
		}
		remaining -= k
	}
	_ = s.sendContainerContent(sess)
	return reply{styled: true, comp: translatable("commands.give.success.single",
		nestedCompound{key: "", value: st.Count},
		itemBrackets(st),
		s.entityTargetComponent(sess),
	)}
}
