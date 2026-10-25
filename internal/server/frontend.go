package server

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"

	"mc-go/api/frontend"
)

// Frontend is the legacy, Java-version-aware entry-point interface. New external
// adapters should implement api/frontend.Frontend and use Server.Attach. Java
// codecs and world replication still live in this package.
type Frontend interface {
	// Name identifies the frontend on the command line ("java", ...).
	Name() string
	// Versions lists the client versions this frontend speaks.
	// Non-versioned protocols return nil.
	Versions() []Version
	// Serve accepts connections on ln until it closes.
	Serve(ln net.Listener) error
}

var frontendBuildersMu sync.RWMutex

var frontendBuilders = map[string]func(*Server) Frontend{}

// RegisterFrontend makes a protocol available by name. Called from the
// frontend's init; duplicate names panic to fail fast at startup.
func RegisterFrontend(name string, build func(*Server) Frontend) {
	frontendBuildersMu.Lock()
	defer frontendBuildersMu.Unlock()
	if _, dup := frontendBuilders[name]; dup {
		panic(fmt.Sprintf("frontend %q registered twice", name))
	}
	frontendBuilders[name] = build
}

// NewFrontend builds a registered protocol frontend for s.
func NewFrontend(name string, s *Server) (Frontend, bool) {
	frontendBuildersMu.RLock()
	build, ok := frontendBuilders[name]
	frontendBuildersMu.RUnlock()
	if !ok {
		if fe, exists := frontend.New(name); exists {
			return externalFrontend{s: s, fe: fe}, true
		}
		return nil, false
	}
	return build(s), true
}

// FrontendNames lists registered protocols in ascending order.
func FrontendNames() []string {
	frontendBuildersMu.RLock()
	defer frontendBuildersMu.RUnlock()
	out := make([]string, 0, len(frontendBuilders))
	for n := range frontendBuilders {
		out = append(out, n)
	}
	for _, name := range frontend.Names() {
		if _, exists := frontendBuilders[name]; !exists {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// externalFrontend preserves the original name-based serving API for public
// protocol-neutral frontends; version metadata belongs to each wire adapter.
type externalFrontend struct {
	s  *Server
	fe frontend.Frontend
}

func (f externalFrontend) Name() string                { return f.fe.Name() }
func (f externalFrontend) Versions() []Version         { return nil }
func (f externalFrontend) Serve(ln net.Listener) error { return f.s.Attach(f.fe, ln) }

// JavaFrontend owns the bundled Java handshake, status, login, configuration
// and play transport. Its admitted 775 and 776 profiles own their codecs and
// registries while sharing one canonical 776 game runtime.
type JavaFrontend struct {
	s *Server
}

// NewJavaFrontend builds the Java Edition frontend over core s.
func NewJavaFrontend(s *Server) *JavaFrontend { return &JavaFrontend{s: s} }

// Name identifies this frontend for --frontend selection.
func (f *JavaFrontend) Name() string { return "java" }

// Versions returns the client versions this frontend speaks.
func (f *JavaFrontend) Versions() []Version { return SupportedVersions() }

// Serve attaches the Java codec to the shared runtime. Closing ln stops only
// this frontend; Stop owns world and game-loop shutdown.
func (f *JavaFrontend) Serve(ln net.Listener) error {
	return f.s.attach(ln, func(context.Context) error { return f.serve(ln) })
}

func (f *JavaFrontend) serve(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		if f.s.ShuttingDown() {
			c.Close()
			return net.ErrClosed
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
