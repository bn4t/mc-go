// Package server embeds the Minecraft runtime and attaches protocol frontends.
// Java Edition protocols 775 and 776 share one canonical 776 game runtime.
// A 775 viewer is disconnected if required canonical content has no exact
// representation in that version. Configuration must precede Start or attachment.
package server

import core "mc-go/internal/server"

// Server owns the authoritative world, game clock, plugins and attached listeners.
// Closing a listener detaches that frontend; call Stop to release the runtime.
type Server = core.Server

func New(addr string) *Server { return core.New(addr) }

// JavaProfileSupport distinguishes admitted protocols from measured candidates.
type JavaProfileSupport = core.JavaProfileSupport

func JavaProfiles() []JavaProfileSupport { return core.JavaProfiles() }
