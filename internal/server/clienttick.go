package server

import "mc-go/internal/protocol"

// ServerGamePacketListenerImpl.handlePlayerKnownMovement records accepted
// movement until the next client frame boundary, independently of world ticks.
func (sess *Session) setKnownMovement(delta [3]float64) {
	sess.lastDelta = delta
	sess.lastHorizSq = delta[0]*delta[0] + delta[2]*delta[2]
	sess.receivedMovementThisTick = true
}

// A frame containing movement retains its final vector. The next frame with
// no movement clears it; this feeds sweep attacks and kinetic spear speed.
func (s *Server) handleClientTickEnd(sess *Session, _ protocol.Packet) {
	if !sess.receivedMovementThisTick {
		sess.lastDelta = [3]float64{}
		sess.lastHorizSq = 0
	}
	sess.receivedMovementThisTick = false
}
