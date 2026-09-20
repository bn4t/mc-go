package server

import (
	"fmt"
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
	s.commands.Register("tp", func(player string, args []string) string {
		return s.runTp(player, args)
	})
	s.commands.Register("kick", func(player string, args []string) string {
		return s.runKick(player, args)
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

// runTp implements "/tp <player>", "/tp <x> <y> <z>", and
// "/tp <victim> <x> <y> <z>" with "~" relativity like vanilla. The @s
// and @p selectors resolve to the sender, matching vanilla when the
// sender is a player.
func (s *Server) runTp(player string, args []string) string {
	resolve := func(target string) string {
		if target == "@s" || target == "@p" {
			return player
		}
		return target
	}
	switch len(args) {
	case 1:
		st, ok := s.PlayerStateOf(resolve(args[0]))
		if !ok {
			return "Unknown player: " + args[0]
		}
		if !s.TeleportPlayer(player, st.X, st.Y, st.Z) {
			return "Unknown player"
		}
		return "Teleported to " + args[0]
	case 3:
		return s.runTpCoords(player, player, args)
	case 4:
		return s.runTpCoords(resolve(args[0]), player, args[1:])
	case 5:
		// /tp <x> <y> <z> <yaw> <pitch> when the leading argument is a
		// coordinate; a name there means a victim form with arguments
		// missing, which vanilla also rejects.
		if _, err := parseDouble(args[0], 0, true); err != nil {
			return "Usage: /tp <player> | /tp <x> <y> <z> [<yaw> <pitch>] | /tp <victim> <x> <y> <z> [<yaw> <pitch>]"
		}
		return s.runTpCoordsRot(player, player, args[:3], args[3:])
	case 6:
		// /tp <victim> <x> <y> <z> <yaw> <pitch>
		return s.runTpCoordsRot(resolve(args[0]), player, args[1:4], args[4:])
	default:
		return "Usage: /tp <player> | /tp <x> <y> <z> [<yaw> <pitch>] | /tp <victim> <x> <y> <z> [<yaw> <pitch>]"
	}
}

func (s *Server) runTpCoords(victim, issuer string, args []string) string {
	const border = 29999984.0
	bx, by, bz := s.baseExactPos(victim)
	x, err := parseDouble(args[0], bx, true)
	if err != nil {
		return "Invalid x: " + args[0]
	}
	y, err := parseDouble(args[1], by, false)
	if err != nil {
		return "Invalid y: " + args[1]
	}
	z, err := parseDouble(args[2], bz, true)
	if err != nil {
		return "Invalid z: " + args[2]
	}
	if x < -border || x > border || z < -border || z > border || y < -2048 || y > 2048 {
		return "Position outside the world"
	}
	_ = issuer
	if !s.TeleportPlayer(victim, x, y, z) {
		return "Unknown player: " + victim
	}
	if victim == issuer {
		return fmt.Sprintf("Teleported to %.2f %.2f %.2f", x, y, z)
	}
	return fmt.Sprintf("Teleported %s to %.2f %.2f %.2f", victim, x, y, z)
}

// runKick implements "/kick <player> [reason...]".

// runTpCoordsRot is the /tp form that also turns the player.
func (s *Server) runTpCoordsRot(victim, issuer string, coords, rot []string) string {
	const border = 29999984.0
	bx, by, bz := s.baseExactPos(victim)
	x, err := parseDouble(coords[0], bx, true)
	if err != nil {
		return "Invalid x: " + coords[0]
	}
	y, err := parseDouble(coords[1], by, false)
	if err != nil {
		return "Invalid y: " + coords[1]
	}
	z, err := parseDouble(coords[2], bz, true)
	if err != nil {
		return "Invalid z: " + coords[2]
	}
	yaw, err := strconv.ParseFloat(rot[0], 32)
	if err != nil {
		return "Invalid yaw: " + rot[0]
	}
	pitch, err := strconv.ParseFloat(rot[1], 32)
	if err != nil {
		return "Invalid pitch: " + rot[1]
	}
	if x < -border || x > border || z < -border || z > border || y < -2048 || y > 2048 {
		return "Position outside the world"
	}
	_ = issuer
	if !s.TeleportPlayerRot(victim, x, y, z, float32(yaw), float32(pitch)) {
		return "Unknown player: " + victim
	}
	return fmt.Sprintf("Teleported to %.2f %.2f %.2f", x, y, z)
}

func (s *Server) runKick(player string, args []string) string {
	if len(args) < 1 {
		return "Usage: /kick <player> [reason...]"
	}
	reason := "Kicked by an operator"
	if len(args) > 1 {
		reason = strings.Join(args[1:], " ")
	}
	if !s.KickPlayer(args[0], reason) {
		return "Unknown player: " + args[0]
	}
	return "Kicked " + args[0]
}
