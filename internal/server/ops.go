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
func parseDouble(tok string, base int) (float64, error) {
	if tok == "~" {
		return float64(base), nil
	}
	if strings.HasPrefix(tok, "~") {
		off, err := strconv.ParseFloat(strings.TrimPrefix(tok, "~"), 64)
		if err != nil {
			return 0, err
		}
		return float64(base) + off, nil
	}
	return strconv.ParseFloat(tok, 64)
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
	default:
		return "Usage: /tp <player> | /tp <x> <y> <z> | /tp <victim> <x> <y> <z>"
	}
}

func (s *Server) runTpCoords(victim, issuer string, args []string) string {
	const border = 29999984.0
	bx, by, bz := s.baseBlockPos(victim)
	x, err := parseDouble(args[0], bx)
	if err != nil {
		return "Invalid x: " + args[0]
	}
	y, err := parseDouble(args[1], by)
	if err != nil {
		return "Invalid y: " + args[1]
	}
	z, err := parseDouble(args[2], bz)
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
