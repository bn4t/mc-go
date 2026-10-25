package server

import (
	"encoding/json"
	"time"

	"mc-go/internal/event"
	"mc-go/internal/plugin"
	"mc-go/internal/protocol"
)

// PlayerTeleport is published on TopicPlayerTeleport for plugins (e.g.
// anticheat) whenever the server initiates a teleport. Client-driven
// movement never emits this; plugins use it to excuse the next
// movement spike instead of flagging it.
type PlayerTeleport = event.PlayerTeleport

// KickPlayer disconnects a live player with a plain-text reason.
// It reports whether the player was online. The normal leave path
// (tab-list removal, leave notice) runs when the read loop exits, so
// enforcement composes with presence instead of bypassing it.
func (s *Server) KickPlayer(name, reason string) bool {
	var applied bool
	s.simulationAction(func() { applied = s.kickPlayerOwned(name, reason) })
	return applied
}

func (s *Server) kickPlayerOwned(name, reason string) bool {
	if s.ShuttingDown() {
		return false
	}
	sess := s.findSessionByName(name)
	if sess == nil || sess.state.Load() != StatePlay {
		return false
	}
	sess.closing.Store(true)
	js, _ := json.Marshal(map[string]string{"text": reason})
	var b protocol.Buffer
	b.String(string(js))
	_ = sess.write(sess.ids().PlayDisconnect, b.Bytes())
	// Let the writer flush the kick before the socket goes away; the
	// read loop exits on the close and untracks the session.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = sess.conn.Close()
	}()
	return true
}

// TeleportPlayer server-teleports a live player, issuing a fresh
// teleport id the client confirms. It reports whether the player was
// online.
func (s *Server) TeleportPlayer(name string, x, y, z float64) bool {
	var applied bool
	s.simulationAction(func() { applied = s.teleportPlayerOwned(name, x, y, z) })
	return applied
}

func (s *Server) teleportPlayerOwned(name string, x, y, z float64) bool {
	sess := s.findSessionByName(name)
	if sess == nil {
		return false
	}
	_, _, _, yaw, pitch, _ := sess.Pos()
	return s.teleportPlayerRot(name, x, y, z, yaw, pitch)
}

// TeleportPlayerRot is TeleportPlayer with the look direction set too,
// the /tp <x> <y> <z> <yaw> <pitch> form vanilla supports.
func (s *Server) TeleportPlayerRot(name string, x, y, z float64, yaw, pitch float32) bool {
	var applied bool
	s.simulationAction(func() { applied = s.teleportPlayerRot(name, x, y, z, yaw, pitch) })
	return applied
}

func (s *Server) teleportPlayerRot(name string, x, y, z float64, yaw, pitch float32) bool {
	if s.ShuttingDown() || badFloat(x) || badFloat(y) || badFloat(z) || badFloat(float64(yaw)) || badFloat(float64(pitch)) {
		return false
	}
	sess := s.findSessionByName(name)
	if sess == nil || sess.state.Load() != StatePlay {
		return false
	}
	// Vanilla teleports leave the entity's onGround flag untouched —
	// only move packets update it via setOnGroundWithMovement — so a
	// ground→air rise right after /tp still reads as a jump.
	_, _, _, _, _, og := sess.Pos()
	sess.setPos(x, y, z, yaw, pitch, og)
	// Vanilla's teleportSetPosition repositions the entity itself —
	// the record follows immediately, before the client echoes back.
	if eid := sess.entityID.Load(); eid != 0 {
		s.entities.Update(eid, x, y, z, yaw, pitch, og)
		s.broadcastEntityTeleport(sess, eid, x, y, z, yaw, pitch, og)
	}
	if err := s.sendSyncPos(sess, x, y, z); err != nil {
		return false
	}
	// Stream the destination like a move does, so a far teleport does
	// not leave the client floating over unloaded columns until its
	// next movement packet arrives.
	cx, cz := int(x)>>4, int(z)>>4
	_ = s.sendWindow(sess, cx, cz)
	s.sendCenterChunk(sess, cx, cz)
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerTeleport, PlayerTeleport{Name: name, X: x, Y: y, Z: z})
	}
	return true
}

// PlayerStateOf snapshots a live player's observable state for
// plugins: position, look, grounded flag, last measured round-trip,
// and gamemode. The second result reports whether the player is online.
func (s *Server) PlayerStateOf(name string) (plugin.PlayerState, bool) {
	sess := s.findSessionByName(name)
	if sess == nil || sess.state.Load() != StatePlay {
		return plugin.PlayerState{}, false
	}
	x, y, z, yaw, pitch, onGround := sess.Pos()
	return plugin.PlayerState{
		Name:     name,
		X:        x,
		Y:        y,
		Z:        z,
		Yaw:      yaw,
		Pitch:    pitch,
		OnGround: onGround,
		Latency:  sess.latency(),
		Gamemode: int32(sess.mode()),
	}, true
}

// ReportViolation publishes a plugin-raised anticheat violation for
// other plugins (loggers, punishers, dashboards) to consume.
func (s *Server) ReportViolation(v plugin.Violation) {
	if s.bus != nil {
		s.bus.Publish(event.TopicViolation, v)
	}
}
