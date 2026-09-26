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
type PlayerTeleport struct {
	Name    string
	X, Y, Z float64
}

// KickPlayer disconnects a live player with a plain-text reason.
// It reports whether the player was online. The normal leave path
// (tab-list removal, leave notice) runs when the read loop exits, so
// enforcement composes with presence instead of bypassing it.
func (s *Server) KickPlayer(name, reason string) bool {
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
	return s.teleportPlayerRot(name, x, y, z, yaw, pitch)
}

func (s *Server) teleportPlayerRot(name string, x, y, z float64, yaw, pitch float32) bool {
	sess := s.findSessionByName(name)
	if sess == nil || sess.state.Load() != StatePlay {
		return false
	}
	sess.setPos(x, y, z, yaw, pitch, false)
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
		Gamemode: 1, // creative, matching Join Game
	}, true
}

// ReportViolation publishes a plugin-raised anticheat violation for
// other plugins (loggers, punishers, dashboards) to consume.
func (s *Server) ReportViolation(v plugin.Violation) {
	if s.bus != nil {
		s.bus.Publish(event.TopicViolation, v)
	}
}
