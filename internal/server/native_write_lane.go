package server

// submitNativeWriteOwned serializes native region replacements in owner
// submission order. Capture has already detached every payload. Workers never
// acquire gameplay ownership, and closing done publishes their result. Full
// SaveWorld admission remains separate: a selective write is not a full save.
func (s *Server) submitNativeWriteOwned(done chan struct{}, write func()) {
	previous := s.nativeWriteTail
	s.nativeWriteTail = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		write()
	}()
}
