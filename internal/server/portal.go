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
		if sess.state.Load() != StatePlay || sess.dead || sess.mode() == GameModeSpectator {
			return true
		}
		x, y, z, _, _, _ := sess.Pos()
		d := sess.dimension()
		feet := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
		insidePortal := isNetherPortal(d.w.GetBlock(feet))
		// Vanilla's post-travel cooldown only counts down OUTSIDE a
		// portal: standing in one freezes it, so re-travel means
		// stepping out, waiting it off, and stepping back in.
		if !insidePortal && sess.portalCooldownTicks > 0 {
			sess.portalCooldownTicks--
		}
		if !insidePortal {
			sess.portalTicks = 0
			return true
		}
		// In a portal: spectators are excluded above; check the
		// game rule and the dimension pair (nether portals bridge
		// overworld and nether only).
		if !s.gameRuleBool("minecraft:allow_entering_nether_using_portals") {
			return true
		}
		if sess.portalCooldownTicks > 0 {
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
	factor := dst.coordScale / cur.coordScale
	tx := math.Floor(float64(feet.X) / factor)
	tz := math.Floor(float64(feet.Z) / factor)
	const border = 29999984
	if tx > border {
		tx = border
	} else if tx < -border {
		tx = -border
	}
	if tz > border {
		tz = border
	} else if tz < -border {
		tz = -border
	}
	px, pz := int(tx), int(tz)
	py := s.findPortalOrGround(dst, px, pz)
	sess.portalCooldownTicks = 300 // vanilla's post-travel portal cooldown
	s.changeDimension(sess, dst, float64(px)+1.5, float64(py), float64(pz)+0.5, -1)
	if cur.name == dimNether && sess.portalEntrySet {
		dx := float64(px) + 1.5 - sess.portalEntryX
		dz := float64(pz) + 1.5 - sess.portalEntryZ
		s.fireNetherTravel(sess, dx*dx+dz*dz)
		sess.portalEntrySet = false
	}
}

// findPortalOrGround locates an existing nether portal near (px, pz)
// in the destination dimension; failing that it builds a small one on
// the ground and returns the arrival Y.
func (s *Server) findPortalOrGround(dst *dimension, px, pz int) int {
	// Existing portal within vanilla's search box wins. Scan a modest
	// cube around the arrival point (vanilla scans 16 columns nether /
	// 128 overworld; the full column scan is bounded by dim height).
	if y, ok := s.findPortalNearby(dst, px, pz); ok {
		return y
	}
	// Build one on the first solid ground under the arrival column,
	// falling back to the dimension's deck height.
	py := groundLevel(dst, px, pz)
	s.buildPortal(dst, px, py, pz)
	return py
}

// findPortalNearby scans columns within 8 blocks of (px, pz) for a
// nether_portal block, returning its base Y.
func (s *Server) findPortalNearby(dst *dimension, px, pz int) (int, bool) {
	for dx := -8; dx <= 8; dx++ {
		for dz := -8; dz <= 8; dz++ {
			cx, cz := px+dx, pz+dz
			for y := dst.minY; y < dst.minY+dst.height-1; y++ {
				if isNetherPortal(dst.w.GetBlock(world.Pos{X: cx, Y: y, Z: cz})) {
					return y, true
				}
			}
		}
	}
	return 0, false
}

// groundLevel returns the lowest standable Y in the column: the first
// air cell with solid ground below and headroom above. Scanning from
// the floor up keeps the nether's bedrock ceiling from being mistaken
// for ground. All-air columns fall back to the dimension's arrival
// deck.
func groundLevel(dst *dimension, px, pz int) int {
	for y := dst.minY + 1; y < dst.minY+dst.height-1; y++ {
		if dst.w.GetBlock(world.Pos{X: px, Y: y, Z: pz}) == world.Air &&
			dst.w.GetBlock(world.Pos{X: px, Y: y - 1, Z: pz}) != world.Air &&
			dst.w.GetBlock(world.Pos{X: px, Y: y + 1, Z: pz}) == world.Air {
			return y
		}
	}
	return dst.arrivalY
}

// buildPortal creates a minimal destination portal at (px,py,pz): an
// obsidian floor, two portal blocks, and an obsidian back wall, then
// broadcasts the edits to the dimension's residents.
func (s *Server) buildPortal(dst *dimension, px, py, pz int) {
	obsidian := world.BlockStateMust("obsidian")
	portal := world.BlockStateMust("nether_portal")
	base := world.Pos{X: px, Y: py - 1, Z: pz}
	var edits []world.Pos
	put := func(p world.Pos, b world.Block) {
		dst.w.SetBlock(p, b)
		edits = append(edits, p)
	}
	// Two-block floor under the portal plus a back wall and cap, so
	// the portal survives (blocks only, no frame-shape validation).
	put(base, obsidian)
	put(world.Pos{X: px, Y: py - 1, Z: pz + 1}, obsidian)
	put(world.Pos{X: px, Y: py, Z: pz}, portal)
	put(world.Pos{X: px, Y: py + 1, Z: pz}, portal)
	put(world.Pos{X: px - 1, Y: py - 1, Z: pz}, obsidian)
	put(world.Pos{X: px - 1, Y: py, Z: pz}, obsidian)
	put(world.Pos{X: px - 1, Y: py + 1, Z: pz}, obsidian)
	put(world.Pos{X: px, Y: py + 2, Z: pz}, obsidian)
	put(world.Pos{X: px - 1, Y: py + 2, Z: pz}, obsidian)
	for _, p := range edits {
		s.broadcastBlockUpdateIn(dst.name, p, dst.w.GetBlock(p))
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
