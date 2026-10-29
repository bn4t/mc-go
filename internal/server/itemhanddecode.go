package server

// Native item-use codecs reject a hand outside the two enum values before
// dispatch, including before terrain/interaction readiness gates.
func (s *Server) rejectInvalidItemHand(sess *Session, hand int32, packet string) bool {
	if hand == 0 || hand == 1 {
		return false
	}
	reason := "Internal Exception: io.netty.handler.codec.DecoderException: Failed to decode packet 'serverbound/minecraft:" + packet + "'"
	s.disconnectWithComponent(sess, translatable("disconnect.genericReason", reason))
	return true
}
