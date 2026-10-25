package server

import (
	"mc-go/api/registry"
	"mc-go/internal/world"
)

// BlockCatalog returns immutable identities for this runtime's block handles.
// Callers may retain it for the runtime lifetime; persist StateKey, not handles.
func (s *Server) BlockCatalog() *registry.BlockCatalog   { return world.BlockCatalog() }
func (a serverAPI) BlockCatalog() *registry.BlockCatalog { return a.s.BlockCatalog() }
