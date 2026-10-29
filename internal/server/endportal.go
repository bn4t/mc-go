package server

import (
	"math"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// End portals, measured from the 26.2 jar:
//
//   - EnderEyeItem.useOn: an eyeless end_portal_frame takes the eye
//     (eye=true, set without neighbour updates), the stack shrinks, and
//     level event 1503 plays at the frame; when the twelve frames of a
//     ring (three per side around a 3x3 hole, every one holding an eye
//     and facing inward) are complete the hole fills with end_portal
//     and global level event 1038 announces it from the hole's centre
//     to every player on the server.
//   - EndPortalBlock: no transition time - the first tick inside
//     travels. Outside the End the destination is the End spawn point
//     (100, 50, 0): EndPlatformFeature.createEndPlatform clears a 5x5
//     of three air layers over an obsidian floor at y 48 (z outer, x,
//     y inner order) and a player lands at (100.5, 49, 0.5) facing
//     west (yaw 90) with its pitch and motion kept (Relative X_ROT +
//     DELTA). Inside the End a player who has not seen the credits is
//     shown them (game event WIN_GAME 0) and leaves the level; the
//     client's perform-respawn then returns it to its respawn point
//     with everything kept. Otherwise the portal returns it there
//     directly.

// endSpawn is ServerLevel.END_SPAWN_POINT.
var endSpawn = world.Pos{X: 100, Y: 50, Z: 0}

func isEndPortal(b world.Block) bool {
	n, ok := world.StateOwner(b)
	return ok && n == "end_portal"
}

// useEnderEye is EnderEyeItem.useOn on an end portal frame.
func (s *Server) useEnderEye(sess *Session, d *dimension, clicked world.Pos) bool {
	return s.useEnderEyeInHand(sess, d, clicked, 0)
}

func (s *Server) useEnderEyeInHand(sess *Session, d *dimension, clicked world.Pos, hand int32) bool {
	held, slot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	if n, ok := itemNameOf(held.ID); !ok || strings.TrimPrefix(n, "minecraft:") != "ender_eye" {
		return false
	}
	cur := d.w.GetBlock(clicked)
	if n, _ := world.StateOwner(cur); n != "end_portal_frame" {
		return false
	}
	if e, _ := world.StateProperty(cur, "eye"); e == "true" {
		return false
	}
	filled, _ := world.WithProperty(cur, "eye", "true")
	s.setSilentBlock(d, clicked, filled)
	s.rsOutputSignalWake(d, clicked)
	sess.addItemStat(statUsed, held.ID, 1)
	if sess.mode() != GameModeCreative {
		sess.consumeSlot(slot, 1)
	}
	s.broadcastLevelEvent(d.name, 1503, clicked, 0)
	if center, ok := endPortalRing(d, clicked); ok {
		portal := world.BlockStateMust("end_portal")
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				s.setSilentBlock(d, world.Pos{X: center.X + dx, Y: center.Y, Z: center.Z + dz}, portal)
			}
		}
		s.broadcastGlobalLevelEvent(1038, center, 0)
	}
	return true
}

// endPortalRing finds a completed frame ring that p belongs to and
// returns the centre of its hole.
func endPortalRing(d *dimension, p world.Pos) (world.Pos, bool) {
	// Each side's frames face the hole: the north row (z-2) faces
	// south, the south row north, the west column east, the east
	// column west.
	type side struct {
		dx, dz int // frame offset from the centre for k = 0
		alongX bool
		facing string
	}
	sides := []side{{0, -2, true, "south"}, {0, 2, true, "north"}, {-2, 0, false, "east"}, {2, 0, false, "west"}}
	ok := func(c world.Pos) bool {
		for _, sd := range sides {
			for k := -1; k <= 1; k++ {
				q := world.Pos{X: c.X + sd.dx, Y: c.Y, Z: c.Z + sd.dz}
				if sd.alongX {
					q.X += k
				} else {
					q.Z += k
				}
				b := d.w.GetBlock(q)
				if n, _ := world.StateOwner(b); n != "end_portal_frame" {
					return false
				}
				if e, _ := world.StateProperty(b, "eye"); e != "true" {
					return false
				}
				if f, _ := world.StateProperty(b, "facing"); f != sd.facing {
					return false
				}
			}
		}
		return true
	}
	for _, sd := range sides {
		for k := -1; k <= 1; k++ {
			c := world.Pos{X: p.X - sd.dx, Y: p.Y, Z: p.Z - sd.dz}
			if sd.alongX {
				c.X -= k
			} else {
				c.Z -= k
			}
			if ok(c) {
				return c, true
			}
		}
	}
	return world.Pos{}, false
}

// broadcastGlobalLevelEvent is ServerLevel.globalLevelEvent: the event
// goes to every player on the server with the global flag set.
func (s *Server) broadcastGlobalLevelEvent(event int32, pos world.Pos, data int32) {
	var b protocol.Buffer
	b.Int(event)
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.Int(data)
	b.Bool(true)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() == StatePlay && sess.spawned.Load() {
			_ = sess.write(sess.ids().PlayLevelEvent, body)
		}
		return true
	})
}

// endPortalTouch reports whether a player standing with its feet at
// (x, y, z) is inside an end portal block's 12/16-high shape.
func endPortalTouch(d *dimension, x, y, z float64) bool {
	feet := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
	return isEndPortal(d.w.GetBlock(feet)) && y-float64(feet.Y) < 0.75
}

// travelEndPortal runs an end portal trip for a player inside one.
func (s *Server) travelEndPortal(sess *Session) {
	cur := sess.dimension()
	if cur.name == dimEnd {
		if !sess.seenCredits {
			s.showEndCredits(sess)
			return
		}
		s.returnThroughEnd(sess)
		return
	}
	end := s.dims[dimEnd]
	if end == nil {
		return
	}
	s.createEndPlatform(end, world.Pos{X: endSpawn.X, Y: endSpawn.Y - 1, Z: endSpawn.Z})
	_, _, _, _, pitch, _ := sess.Pos()
	x, y, z := float64(endSpawn.X)+0.5, float64(endSpawn.Y)-1, float64(endSpawn.Z)+0.5
	s.changeDimensionSync(sess, end, x, y, z, 90, pitch, syncRelPitchDelta, 90, 0, -1)
}

// createEndPlatform is EndPlatformFeature.createEndPlatform at pos:
// obsidian one below, air for three layers.
func (s *Server) createEndPlatform(d *dimension, pos world.Pos) {
	obsidian := world.BlockStateMust("obsidian")
	for dz := -2; dz <= 2; dz++ {
		for dx := -2; dx <= 2; dx++ {
			for dy := -1; dy < 3; dy++ {
				p := world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
				want := world.Air
				if dy == -1 {
					want = obsidian
				}
				if d.w.GetBlock(p) != want {
					s.setNaturalBlock(d, p, want)
				}
			}
		}
	}
}

// showEndCredits is ServerPlayer.showEndCredits: the player leaves the
// level (others stop seeing it) and is shown the credits once.
func (s *Server) showEndCredits(sess *Session) {
	if sess.wonGame {
		return
	}
	sess.wonGame = true
	sess.seenCredits = true
	s.broadcastEntityRemoveIn(sess.dimName(), sess, []int32{sess.entityID.Load()})
	_ = s.sendGameEvent(sess, 4, 0)
}

// returnThroughEnd sends a player home from the End with everything
// kept (PlayerList.respawn after the credits or through an exit
// portal): its respawn point, else the world spawn.
func (s *Server) returnThroughEnd(sess *Session) {
	sess.wonGame = false
	overworld := s.dims[dimOverworld]
	sess.posMu.Lock()
	hasSpawn, spawnPos, spawnDim := sess.hasSpawnPos, sess.spawnPos, sess.spawnDim
	spawnYaw, spawnPitch := sess.spawnYaw, sess.spawnPitch
	sess.posMu.Unlock()
	dest := overworld
	var x, y, z float64
	var yaw, pitch float32
	if hasSpawn {
		if d, ok := s.dims[spawnDim]; ok {
			dest = d
		}
		x, y, z = float64(spawnPos[0])+0.5, float64(spawnPos[1]), float64(spawnPos[2])+0.5
		yaw, pitch = spawnYaw, spawnPitch
	} else {
		x, y, z = s.playerSpawn()
	}
	s.changeDimensionSync(sess, dest, x, y, z, yaw, pitch, 0, 0, 0, -1)
}
