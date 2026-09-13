package server

import (
	"fmt"
	"net"
	"sort"
)

// Frontend is one protocol entry point into the server core.
// The core owns game state (world, entities, sessions, broadcast);
// frontends own wire framing and per-version dispatch. Adding a new
// protocol (Bedrock, RCON, ...) is a new Frontend implementation plus
// a RegisterFrontend call: core needs no changes.
type Frontend interface {
	// Name identifies the frontend on the command line ("java", ...).
	Name() string
	// Versions lists the client versions this frontend speaks.
	// Non-versioned protocols return nil.
	Versions() []Version
	// Serve accepts connections on ln until it closes.
	Serve(ln net.Listener) error
}

var frontendBuilders = map[string]func(*Server) Frontend{}

// RegisterFrontend makes a protocol available by name. Called from the
// frontend's init; duplicate names panic to fail fast at startup.
func RegisterFrontend(name string, build func(*Server) Frontend) {
	if _, dup := frontendBuilders[name]; dup {
		panic(fmt.Sprintf("frontend %q registered twice", name))
	}
	frontendBuilders[name] = build
}

// NewFrontend builds a registered protocol frontend for s.
func NewFrontend(name string, s *Server) (Frontend, bool) {
	build, ok := frontendBuilders[name]
	if !ok {
		return nil, false
	}
	return build(s), true
}

// FrontendNames lists registered protocols in ascending order.
func FrontendNames() []string {
	out := make([]string, 0, len(frontendBuilders))
	for n := range frontendBuilders {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// JavaFrontend is the Java Edition protocol frontend: handshake,
// status, login, configuration, and play stages for every supported
// client version. The per-session Version selects the packet-ID table;
// encode/decode shapes shared across versions live in the stage
// handlers, version-specific branches consult sess.ver.
type JavaFrontend struct {
	s *Server
}

// NewJavaFrontend builds the Java Edition frontend over core s.
func NewJavaFrontend(s *Server) *JavaFrontend { return &JavaFrontend{s: s} }

// Name identifies this frontend for --frontend selection.
func (f *JavaFrontend) Name() string { return "java" }

// Versions returns the client versions this frontend speaks.
func (f *JavaFrontend) Versions() []Version { return SupportedVersions() }

// Serve accepts on ln until it closes. Each connection runs on its own
// goroutines; this method blocks. It owns the world lifecycle (ticks +
// shards across every dimension) so every frontend boots the core the
// same way.
func (f *JavaFrontend) Serve(ln net.Listener) error {
	for _, d := range f.s.dims {
		d.w.Start()
	}
	defer func() {
		for _, d := range f.s.dims {
			d.w.Stop()
		}
	}()
	f.s.addListener(ln)
	defer f.s.removeListener(ln)
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		select {
		case f.s.sem <- struct{}{}:
		default:
			c.Close()
			continue
		}
		go func() {
			defer func() { <-f.s.sem }()
			f.s.handleConn(c)
		}()
	}
}

func init() {
	RegisterFrontend("java", func(s *Server) Frontend { return NewJavaFrontend(s) })
}
