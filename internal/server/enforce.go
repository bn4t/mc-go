package server

import (
	"encoding/json"
	"time"

	"mc-go/api/player"
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
// It reports whether an admitted player was retired.
func (s *Server) KickPlayer(name, reason string) bool {
	var applied bool
	s.simulationAction(func() { applied = s.kickPlayerOwned(name, reason) })
	return applied
}

func (s *Server) kickPlayerOwned(name, reason string) bool {
	if s.ShuttingDown() {
		return false
	}
	p := s.findPlayerByNameOwned(name)
	if p == nil {
		return false
	}
	return s.kickPlayerRecordOwned(p, reason)
}

func (s *Server) kickPlayerRecordOwned(p *playerRecord, reason string) bool {
	if s.ShuttingDown() || p == nil || !p.active.Load() {
		return false
	}
	if sess := p.java; sess != nil {
		sess.closing.Store(true)
		js, _ := json.Marshal(map[string]string{"text": reason})
		var b protocol.Buffer
		b.String(string(js))
		_ = sess.write(sess.ids().PlayDisconnect, b.Bytes())
		// Retire identity now; only the real Java transport needs a flush grace.
		go func() {
			time.Sleep(200 * time.Millisecond)
			_ = sess.conn.Close()
		}()
	}
	s.retirePlayerOwned(p, reason)
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
	p := s.findPlayerByNameOwned(name)
	if p == nil {
		return false
	}
	pose := p.position()
	pose.X, pose.Y, pose.Z = x, y, z
	return s.teleportPlayerRecordOwned(p, pose)
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
	p := s.findPlayerByNameOwned(name)
	if p == nil {
		return false
	}
	pose := p.position()
	pose.X, pose.Y, pose.Z, pose.Yaw, pose.Pitch = x, y, z, yaw, pitch
	return s.teleportPlayerRecordOwned(p, pose)
}

func (s *Server) teleportPlayerRecordOwned(p *playerRecord, pose player.Pose) bool {
	if s.ShuttingDown() || p == nil || !p.active.Load() || badFloat(pose.X) || badFloat(pose.Y) || badFloat(pose.Z) || badFloat(float64(pose.Yaw)) || badFloat(float64(pose.Pitch)) {
		return false
	}
	// Vanilla teleports leave the entity's onGround flag untouched —
	// only move packets update it via setOnGroundWithMovement — so a
	// ground→air rise right after /tp still reads as a jump.
	pose.OnGround = p.position().OnGround
	s.commitPlayerPoseOwned(p, pose)
	s.correctPlayerOwned(p, pose)
	// Stream the destination like a move does, so a far teleport does
	// not leave the client floating over unloaded columns until its
	// next movement packet arrives.
	if sess := p.java; sess != nil {
		cx, cz := int(pose.X)>>4, int(pose.Z)>>4
		_ = s.sendWindow(sess, cx, cz)
		s.sendCenterChunk(sess, cx, cz)
	}
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerTeleport, PlayerTeleport{ID: p.ID(), Name: p.profile().Name, X: pose.X, Y: pose.Y, Z: pose.Z})
	}
	return true
}

// PlayerStateOf snapshots a live player's observable state for
// plugins: position, look, grounded flag, last measured round-trip,
// and gamemode. The second result reports whether the player is online.
func (s *Server) PlayerStateOf(name string) (plugin.PlayerState, bool) {
	id, found := s.playerIDByName(name)
	if !found {
		return plugin.PlayerState{}, false
	}
	return s.pluginPlayerStateID(id)
}

func (s *Server) pluginPlayerStateID(id player.ID) (plugin.PlayerState, bool) {
	state, found := s.playerStateByID(id)
	if !found {
		return plugin.PlayerState{}, false
	}
	result := plugin.PlayerState{ID: state.ID, Name: state.Profile.Name, X: state.Pose.X, Y: state.Pose.Y, Z: state.Pose.Z, Yaw: state.Pose.Yaw, Pitch: state.Pose.Pitch, OnGround: state.Pose.OnGround, Gamemode: state.Gamemode}
	// Keep-alive latency is independent transport telemetry, not pose state.
	if p := s.findPlayerByIDOwned(id); p != nil && p.java != nil {
		result.Latency = p.java.latency()
	}
	return result, true
}

// ReportViolation publishes a plugin-raised anticheat violation for
// other plugins (loggers, punishers, dashboards) to consume.
func (s *Server) ReportViolation(v plugin.Violation) {
	if s.bus != nil {
		s.bus.Publish(event.TopicViolation, v)
	}
}
