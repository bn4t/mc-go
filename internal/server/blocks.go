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
// exact reports whether the token pinned every property. A bare name is
// not exact, which is what makes it usable as a predicate: vanilla's
// "minecraft:water" matches any water state, while
// "minecraft:water[level=0]" matches only a source.
func parseBlockSpec(tok string) (blk world.Block, exact bool, ok bool) {
	s := strings.ToLower(strings.TrimSpace(tok))
	s = strings.TrimPrefix(s, "minecraft:")
	name := s
	var props map[string]string
	if i := strings.IndexByte(s, '['); i >= 0 {
		if !strings.HasSuffix(s, "]") {
			return world.Air, false, false
		}
		name = strings.TrimSpace(s[:i])
		body := strings.TrimSpace(s[i+1 : len(s)-1])
		props = map[string]string{}
		if body != "" {
			for _, kv := range strings.Split(body, ",") {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return world.Air, false, false
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
		return world.Air, false, false
	}
	return b, props != nil, true
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
		x, y, z, _, _, _ := sess.Pos()
		return int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	}
	// Fallback to world spawn when the sender has no session (tests).
	sx, sy, sz := s.world.Spawn()
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
func (s *Server) runSetblock(player string, args []string) string {
	if len(args) != 4 {
		return "Usage: /setblock <x> <y> <z> <stone|dirt|grass|air>"
	}
	bx, by, bz := s.baseBlockPos(player)
	x, err := parseCoord(args[0], bx)
	if err != nil {
		return "Invalid x: " + args[0]
	}
	y, err := parseCoord(args[1], by)
	if err != nil {
		return "Invalid y: " + args[1]
	}
	z, err := parseCoord(args[2], bz)
	if err != nil {
		return "Invalid z: " + args[2]
	}
	d := s.dimOf(player)
	if y < d.minY || y > d.minY+d.height-1 {
		return fmt.Sprintf("Y out of range %d..%d", d.minY, d.minY+d.height-1)
	}
	// World border, matching movement validation.
	const border = 29999984
	if x < -border || x > border || z < -border || z > border {
		return "Position outside world border"
	}
	blk, ok := parseBlockName(args[3])
	if !ok {
		return "Unknown block: " + args[3]
	}
	pos := world.Pos{X: x, Y: y, Z: z}
	if affected := s.setBlockAndBroadcast(player, pos, blk); len(affected) > 0 {
		s.sendLightUpdate(d, pos.X>>4, pos.Z>>4, affected)
	}
	return fmt.Sprintf("Set block %d %d %d to %s", x, y, z, strings.ToLower(args[3]))
}

// broadcastChunk resends one column to every play-stage session holding
// it in dimension d.
func (s *Server) broadcastChunk(d *dimension, cx, cz int) {
	chunk := encodeChunk(d, cx, cz)
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
	s.commands.Register("help", func(player string, args []string) string {
		return "Commands: " + strings.Join(s.commands.Names(), ", ")
	})
	s.commands.Register("list", func(player string, args []string) string {
		var names []string
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay {
				return true
			}
			if n := sess.getName(); n != "" {
				names = append(names, n)
			}
			return true
		})
		if len(names) == 0 {
			return "No players online"
		}
		return "Players (" + strconv.Itoa(len(names)) + "): " + strings.Join(names, ", ")
	})
	s.commands.Register("pos", func(player string, args []string) string {
		sess := s.findSessionByName(player)
		if sess == nil {
			return "Unknown player"
		}
		x, y, z, yaw, pitch, _ := sess.Pos()
		return fmt.Sprintf("%.2f %.2f %.2f yaw=%.1f pitch=%.1f", x, y, z, yaw, pitch)
	})
	s.commands.Register("setblock", func(player string, args []string) string {
		return s.runSetblock(player, args)
	})
	s.commands.Register("give", func(player string, args []string) string {
		return s.runGive(player, args)
	})
	s.commands.Register("summon", func(player string, args []string) string {
		return s.runSummon(player, args)
	})
	s.commands.Register("execute", func(player string, args []string) string {
		return s.runExecute(player, args)
	})
	s.commands.Register("scoreboard", func(player string, args []string) string {
		return s.runScoreboard(player, args)
	})
	s.commands.Register("kill", func(player string, args []string) string {
		target := player
		if len(args) > 0 {
			target = args[0]
		}
		sess := s.findSessionByName(target)
		if sess == nil {
			return "Unknown player: " + target
		}
		s.broadcastDamageEvent(sess.entityID.Load(), damageInfo{typeName: "generic_kill"})
		s.killPlayer(sess, "Killed by "+player)
		return "Killed " + target
	})
}

// runGive implements /give <item> [count]: the item joins the player's
// inventory through the normal pickup path, syncing the slots.
func (s *Server) runGive(player string, args []string) string {
	if len(args) == 0 {
		return "Usage: /give <item> [count]"
	}
	name := strings.TrimPrefix(args[0], "minecraft:")
	id, ok := world.ItemID(name)
	if !ok {
		return "Unknown item: " + args[0]
	}
	count := int32(1)
	if len(args) > 1 {
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 || n > maxStack {
			return "Invalid count: " + args[1]
		}
		count = int32(n)
	}
	sess := s.findSessionByName(player)
	if sess == nil {
		return "Unknown player: " + player
	}
	remainder, picked := sess.addStack(ItemStack{ID: id, Count: count})
	if !picked {
		return "Inventory full"
	}
	_ = s.sendContainerContent(sess)
	if remainder.Count > 0 {
		return "Partially gave " + name
	}
	return "Gave " + strconv.Itoa(int(count)) + " " + name
}
