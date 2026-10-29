package server

import (
	"fmt"
	"math"

	"mc-go/internal/world"
)

// underSpawnProtection is DedicatedServer.isUnderSpawnProtection: only
// in the world spawn's dimension, only while an ops list exists, never
// for an op, and only for a positive radius — the block lies within the
// spawn-protection radius when max(|dx|, |dz|) to the spawn block does.
func (s *Server) underSpawnProtection(d *dimension, pos world.Pos, sess *Session) bool {
	if d == nil || d.name != dimOverworld {
		return false
	}
	s.rulesMu.RLock()
	noOps := len(s.ops) == 0
	_, op := s.ops[sess.getName()]
	s.rulesMu.RUnlock()
	if noOps || op || s.SpawnProtection <= 0 {
		return false
	}
	x, _, z := s.WorldSpawn()
	sx, sz := int(math.Floor(x)), int(math.Floor(z))
	dx, dz := pos.X-sx, pos.Z-sz
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	return max(dx, dz) <= s.SpawnProtection
}

// mayInteract is ServerLevel.mayInteract for a player: outside spawn
// protection and inside the world border.
func (s *Server) mayInteract(sess *Session, d *dimension, pos world.Pos) bool {
	return !s.underSpawnProtection(d, pos, sess) && withinWorldBorder(s, d, pos)
}

// sendSpawnProtectionMessage is ServerPlayer.sendSpawnProtectionMessage:
// the red build.spawn_protection overlay naming the block "x, y, z".
func (s *Server) sendSpawnProtectionMessage(sess *Session, pos world.Pos) {
	s.sendActionBar(sess, component{
		translate: "build.spawn_protection",
		color:     "red",
		with:      []any{fmt.Sprintf("%d, %d, %d", pos.X, pos.Y, pos.Z)},
	})
}
