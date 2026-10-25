// Package server embeds the Minecraft runtime and attaches protocol frontends.
// Java Edition protocol 776 is the sole bundled game protocol. Runtime
// configuration must be completed before Start or the first attachment.
package server

import core "mc-go/internal/server"

// Server owns the authoritative world, game clock, plugins and attached listeners.
// Closing a listener detaches that frontend; call Stop to release the runtime.
type Server = core.Server

func New(addr string) *Server { return core.New(addr) }

// JavaProfileSupport distinguishes admitted protocols from measured candidates.
type JavaProfileSupport = core.JavaProfileSupport

func JavaProfiles() []JavaProfileSupport { return core.JavaProfiles() }
