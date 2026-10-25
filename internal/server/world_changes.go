package server

// flushWorldUpdatesOwned commits only invalidated watched cells. Storage's
// batched final reads are barriers behind the accepted shard writes, independent
// of Java's own block/light packet batching. All call sites are complete action
// or tick boundaries and precede releasing simulation ownership.
func (s *Server) flushWorldUpdatesOwned() {
	for _, name := range [...]string{dimOverworld, dimNether, dimEnd} {
		d := s.dims[name]
		if d == nil {
			continue
		}
		changes := d.w.DrainBlockChanges()
		if len(changes) == 0 {
			continue
		}
		d.worldviewRevision++
		s.publishWorldChangesOwned(d, d.worldviewRevision, changes)
	}
}

func (s *Server) worldviewRevisionOwned(d *dimension) uint64 {
	return d.worldviewRevision
}
