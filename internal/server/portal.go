package server

import (
	"fmt"
	"math"
	"strconv"

	"mc-go/internal/world"
)

// Nether portal travel, the player-visible part of the dimension
// milestone. Vanilla's rules the code mirrors:
//   - standing inside a nether_portal block counts ticks toward a
//     departure: players_nether_portal_default_delay (80) in
//     survival/adventure, players_nether_portal_creative_delay (0) in
//     creative; spectators never travel;
//   - allow_entering_nether_using_portals gates the mechanic;
//   - arriving coordinates scale by source/destination coordinate
//     scale (8:1 between the overworld and the nether);
//   - a destination portal is built when none is nearby, and the
//     traveler lands beside it with a re-entry cooldown.

// portalDelay reads the per-mode portal wait from the game-rule table.
func (s *Server) portalDelay(sess *Session) int {
	key := "minecraft:players_nether_portal_default_delay"
	if sess.mode() == GameModeCreative {
		key = "minecraft:players_nether_portal_creative_delay"
	}
	s.rulesMu.RLock()
	v := s.gameRules[key]
	s.rulesMu.RUnlock()
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return 80
}

// gameRuleBool reads a boolean game rule ("true"/"false").
func (s *Server) gameRuleBool(name string) bool {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	return s.gameRules[name] == "true"
}

// portalTickStep counts portal occupancy and fires the travel once the
// per-mode delay elapses. Runs on the 20 TPS coordinator.
func (s *Server) portalTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.gameplayTickReady() || sess.dead || sess.mode() == GameModeSpectator {
			return true
		}
		x, y, z, _, _, _ := sess.Pos()
		d := sess.dimension()
		feet := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
		insidePortal := isNetherPortal(d.w.GetBlock(feet))
		// Vanilla (Entity.handlePortal + setAsInsidePortal +
		// PortalProcessor): the cooldown decrements every tick; while a
		// cooling entity stands inside a portal, entry re-arms the
		// cooldown to the full dimension-changing delay instead of
		// marking the processor inside — travel can only fire after
		// stepping out long enough for the cooldown to drain. Outside a
		// portal the accumulated portal time decays 4/tick rather than
		// resetting. A player's dimension-changing delay is 10 ticks;
		// non-players get 300 (only players travel here).
		const playerPortalCooldown = 10
		if insidePortal && sess.portalCooldownTicks > 0 {
			sess.portalCooldownTicks = playerPortalCooldown
		} else if sess.portalCooldownTicks > 0 {
			sess.portalCooldownTicks--
		}
		if !insidePortal || sess.portalCooldownTicks > 0 {
			if sess.portalTicks > 4 {
				sess.portalTicks -= 4
			} else {
				sess.portalTicks = 0
			}
			return true
		}
		// In a portal off cooldown: check the game rule and the
		// dimension pair (nether portals bridge overworld and nether
		// only).
		if !s.gameRuleBool("minecraft:allow_entering_nether_using_portals") {
			return true
		}
		sess.portalTicks++
		if sess.portalTicks >= s.portalDelay(sess) {
			sess.portalTicks = 0
			s.travelNetherPortal(sess, feet)
		}
		return true
	})
}

func isNetherPortal(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && name == "nether_portal"
}

// portalCooldownStep is Entity.baseTick's handlePortal opener —
// processPortalCooldown decrements every ticking entity's
// portalCooldown by one per tick while positive. Non-living
// entities (items, carts, projectiles) tick it the same way mobs
// do, so the pass sweeps the whole entity table.
func (s *Server) portalCooldownStep() {
	s.entities.mu.RLock()
	list := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		list = append(list, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range list {
		if e.presenceOnly {
			continue
		}
		if e.PortalCooldown > 0 {
			e.PortalCooldown--
		}
	}
}

// travelNetherPortal moves the player through the portal: overworld
// and nether trade places at 8:1 coordinate scale. A destination
// portal is created when none is found near the arrival point, and the
// player lands next to it with a re-entry cooldown.
func (s *Server) travelNetherPortal(sess *Session, feet world.Pos) {
	cur := sess.dimension()
	var dst *dimension
	switch cur.name {
	case dimOverworld:
		dst = s.dims[dimNether]
	case dimNether:
		dst = s.dims[dimOverworld]
	default:
		return // portals bridge overworld and nether only
	}
	if cur.name == dimOverworld {
		// Leaving for the nether latches the overworld entry point —
		// nether_travel's distance is measured against it.
		sess.portalEntryX = float64(feet.X)
		sess.portalEntryZ = float64(feet.Z)
		sess.portalEntrySet = true
	}
	x, y, z, yaw, pitch, _ := sess.Pos()
	ax, ay, az, turn, ok := s.portalExit(cur, dst, feet, x, y, z, sess.sneaking)
	if !ok {
		return
	}
	// Player.getDimensionChangingDelay is 10 ticks; Entity's 300 only
	// applies to non-player travelers (a ridden vehicle defers to its
	// player passenger).
	sess.portalCooldownTicks = 10
	s.changeDimensionFacing(sess, dst, ax, ay, az, yaw+turn, pitch, true, turn, -1)
	if cur.name == dimNether && sess.portalEntrySet {
		dx := ax - sess.portalEntryX
		dz := az - sess.portalEntryZ
		s.fireNetherTravel(sess, dx*dx+dz*dz)
		sess.portalEntrySet = false
	}
}

// runExecuteTeleport implements "tp/teleport [@s|<player>] x y z"
// inside an execute-in chain, moving the target into the dimension.
func (s *Server) runExecuteTeleport(player string, d *dimension, args []string) string {
	target := player
	coords := args
	if len(args) == 4 {
		target = args[0]
		coords = args[1:]
	} else if len(args) != 3 {
		return "Usage: execute in <dim> run tp [@s|<player>] <x> <y> <z>"
	}
	if target == "@s" || target == "@p" {
		target = player
	}
	ts := s.findSessionByName(target)
	if ts == nil {
		return "Unknown player: " + target
	}
	bx, by, bz := s.baseExactPos(target)
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
	s.changeDimension(ts, d, x, y, z, 0)
	return fmt.Sprintf("Teleported %s to %s %.2f %.2f %.2f", target, d.name, x, y, z)
}
