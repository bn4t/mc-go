package server

import (
	"strconv"
	"strings"
)

// Vanilla-subset operator commands: /tp, /kick, /save-all, /stop.
// Registered alongside the building-block commands in registerBuiltins;
// plugin commands may override any of these names via Commands.Register.
//
// Permissions are intentionally open: every player may run them, matching
// a single-operator local server. An ops/permission milestone will gate
// these; until then the open behavior is documented, not accidental.

// registerOps adds the operator command set.
func (s *Server) registerOps() {
	s.registerComponentCommand("tp", func(player string, args []string) reply {
		return s.runTp(player, args)
	})
	s.registerComponentCommand("teleport", func(player string, args []string) reply {
		return s.runTp(player, args)
	})
	s.commands.Register("save-all", func(player string, args []string) string {
		if err := s.SaveWorld(); err != nil {
			return "Save failed: " + err.Error()
		}
		return "Saved the game"
	})
	s.commands.Register("stop", func(player string, args []string) string {
		// Async: the reply is already queued ahead of the kicks, so
		// the issuer sees it before the disconnect lands.
		go s.Shutdown("Server closed")
		return "Stopping the server"
	})
}

// parseDouble resolves one teleport axis: "12", "12.5", "~", "~-3.5".
// The base is the entity's exact position — vanilla's "~" moves from
// the player's fractional spot, not the block floor. When center is
// set (the horizontal axes of a teleport), a bare integer means the
// block column and lands on its center — vanilla's vec3 arg centers
// x/z on 0.5 while y stays exact.
func parseDouble(tok string, base float64, center bool) (float64, error) {
	if tok == "~" {
		return base, nil
	}
	if strings.HasPrefix(tok, "~") {
		off, err := strconv.ParseFloat(strings.TrimPrefix(tok, "~"), 64)
		if err != nil {
			return 0, err
		}
		return base + off, nil
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, err
	}
	if center && !strings.ContainsAny(tok, ".eE") {
		v += 0.5
	}
	return v, nil
}

// baseExactPos returns the player's exact position for "~" resolution
// in teleport-style arguments, falling back to world spawn.
func (s *Server) baseExactPos(player string) (float64, float64, float64) {
	if sess := s.findSessionByName(player); sess != nil {
		x, y, z, _, _, _ := sess.Pos()
		return x, y, z
	}
	return s.WorldSpawn()
}

// tpReply announces a teleport the way vanilla's sendSuccess does: the
// issuer gets the translatable component, the other operators a
// gray-italic chat.type.admin relay.
func (s *Server) tpReply(issuer string, msg component) reply {
	if iss := s.findSessionByName(issuer); iss != nil {
		s.broadcastAdmin(iss, msg)
	}
	return componentReply(msg)
}

// tpCoords renders the coordinate arguments the way vanilla's
// formatDouble does: String.format("%f"), always six decimals.
func tpCoords(x, y, z float64) (string, string, string) {
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 6, 64) }
	return f(x), f(y), f(z)
}

// runTp implements "/tp <player>", "/tp <x> <y> <z>", and
// "/tp <victim> <x> <y> <z>" with "~" relativity like vanilla. The @s
// and @p selectors resolve to the sender, matching vanilla when the
// sender is a player. Success replies are vanilla's translatable
// components: commands.teleport.success.entity.single for entity
// destinations and .location.single for coordinates.
func (s *Server) runTp(player string, args []string) reply {
	resolve := func(target string) *Session {
		return s.resolveTarget(player, target)
	}
	switch len(args) {
	case 1:
		dest := resolve(args[0])
		if dest == nil {
			return textReply("Unknown player: " + args[0])
		}
		issuer := s.findSessionByName(player)
		if issuer == nil {
			return textReply("Unknown player")
		}
		dx, dy, dz, _, _, _ := dest.Pos()
		if !s.TeleportPlayer(player, dx, dy, dz) {
			return textReply("Unknown player")
		}
		return s.tpReply(player, translatable("commands.teleport.success.entity.single",
			entityTargetComponent(issuer), entityTargetComponent(dest)))
	case 3:
		return s.runTpCoords(player, player, args)
	case 4:
		victim := resolve(args[0])
		if victim == nil {
			if r, ok := s.runTpEntities(player, args[0], args[1:]); ok {
				return r
			}
			return textReply("Unknown player: " + args[0])
		}
		return s.runTpCoords(victim.getName(), player, args[1:])
	case 5:
		// /tp <x> <y> <z> <yaw> <pitch> when the leading argument is a
		// coordinate; a name there means a victim form with arguments
		// missing, which vanilla also rejects.
		if _, err := parseDouble(args[0], 0, true); err != nil {
			return textReply("Usage: /tp <player> | /tp <x> <y> <z> [<yaw> <pitch>] | /tp <victim> <x> <y> <z> [<yaw> <pitch>]")
		}
		return s.runTpCoordsRot(player, player, args[:3], args[3:])
	case 6:
		victim := resolve(args[0])
		if victim == nil {
			return textReply("Unknown player: " + args[0])
		}
		return s.runTpCoordsRot(victim.getName(), player, args[1:4], args[4:])
	default:
		return textReply("Usage: /tp <player> | /tp <x> <y> <z> [<yaw> <pitch>] | /tp <victim> <x> <y> <z> [<yaw> <pitch>]")
	}
}

func (s *Server) runTpCoords(victim, issuer string, args []string) reply {
	const border = 29999984.0
	bx, by, bz := s.baseExactPos(victim)
	x, err := parseDouble(args[0], bx, true)
	if err != nil {
		return textReply("Invalid x: " + args[0])
	}
	y, err := parseDouble(args[1], by, false)
	if err != nil {
		return textReply("Invalid y: " + args[1])
	}
	z, err := parseDouble(args[2], bz, true)
	if err != nil {
		return textReply("Invalid z: " + args[2])
	}
	if x < -border || x > border || z < -border || z > border || y < -2048 || y > 2048 {
		return textReply("Position outside the world")
	}
	if !s.TeleportPlayer(victim, x, y, z) {
		return textReply("Unknown player: " + victim)
	}
	target := s.findSessionByName(victim)
	if target == nil {
		return textReply("Unknown player: " + victim)
	}
	sx, sy, sz := tpCoords(x, y, z)
	return s.tpReply(issuer, translatable("commands.teleport.success.location.single",
		entityTargetComponent(target), sx, sy, sz))
}

// runKick implements "/kick <player> [reason...]".

// runTpCoordsRot is the /tp form that also turns the player.
func (s *Server) runTpCoordsRot(victim, issuer string, coords, rot []string) reply {
	const border = 29999984.0
	bx, by, bz := s.baseExactPos(victim)
	x, err := parseDouble(coords[0], bx, true)
	if err != nil {
		return textReply("Invalid x: " + coords[0])
	}
	y, err := parseDouble(coords[1], by, false)
	if err != nil {
		return textReply("Invalid y: " + coords[1])
	}
	z, err := parseDouble(coords[2], bz, true)
	if err != nil {
		return textReply("Invalid z: " + coords[2])
	}
	yaw, err := strconv.ParseFloat(rot[0], 32)
	if err != nil {
		return textReply("Invalid yaw: " + rot[0])
	}
	pitch, err := strconv.ParseFloat(rot[1], 32)
	if err != nil {
		return textReply("Invalid pitch: " + rot[1])
	}
	if x < -border || x > border || z < -border || z > border || y < -2048 || y > 2048 {
		return textReply("Position outside the world")
	}
	if !s.TeleportPlayerRot(victim, x, y, z, float32(yaw), float32(pitch)) {
		return textReply("Unknown player: " + victim)
	}
	target := s.findSessionByName(victim)
	if target == nil {
		return textReply("Unknown player: " + victim)
	}
	sx, sy, sz := tpCoords(x, y, z)
	return s.tpReply(issuer, translatable("commands.teleport.success.location.single",
		entityTargetComponent(target), sx, sy, sz))
}

// runTpEntities implements "/tp @e[type=name] <x> <y> <z>" against
// non-player entities — vanilla's selector teleports whatever it
// matches; a rider follows its vehicle on the next tick.
func (s *Server) runTpEntities(issuer, sel string, args []string) (reply, bool) {
	if !strings.HasPrefix(sel, "@e[") || !strings.HasSuffix(sel, "]") {
		return reply{}, false
	}
	var typ string
	for _, kv := range strings.Split(sel[3:len(sel)-1], ",") {
		k, v, _ := strings.Cut(kv, "=")
		if strings.TrimSpace(k) == "type" {
			typ = strings.TrimPrefix(strings.TrimSpace(v), "minecraft:")
		}
	}
	if typ == "" {
		return reply{}, false
	}
	var ents []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if n, _ := EntityTypeName(e.Type); n == typ {
			ents = append(ents, e)
		}
	}
	s.entities.mu.RUnlock()
	if len(ents) == 0 {
		return reply{}, false
	}
	const border = 29999984.0
	var lx, ly, lz float64
	for _, e := range ents {
		x, err := parseDouble(args[0], e.X, true)
		if err != nil {
			return textReply("Invalid x: " + args[0]), true
		}
		y, err := parseDouble(args[1], e.Y, false)
		if err != nil {
			return textReply("Invalid y: " + args[1]), true
		}
		z, err := parseDouble(args[2], e.Z, true)
		if err != nil {
			return textReply("Invalid z: " + args[2]), true
		}
		if x < -border || x > border || z < -border || z > border || y < -2048 || y > 2048 {
			return textReply("Position outside the world"), true
		}
		e.X, e.Y, e.Z = x, y, z
		lx, ly, lz = x, y, z
	}
	sx, sy, sz := tpCoords(lx, ly, lz)
	if len(ents) == 1 {
		return s.tpReply(issuer, translatable("commands.teleport.success.location.single",
			translatable("entity.minecraft."+typ), sx, sy, sz)), true
	}
	return s.tpReply(issuer, translatable("commands.teleport.success.location.multiple",
		int32(len(ents)), sx, sy, sz)), true
}
