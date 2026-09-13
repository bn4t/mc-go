package server

import (
	"errors"
	"net"
	"os"
	"strings"
	"time"
)

// addListener tracks a serving listener for Shutdown.
func (s *Server) addListener(ln net.Listener) {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	if s.listeners == nil {
		s.listeners = make(map[net.Listener]struct{})
	}
	s.listeners[ln] = struct{}{}
}

// removeListener untracks a closed listener.
func (s *Server) removeListener(ln net.Listener) {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	delete(s.listeners, ln)
}

// ShuttingDown reports whether Shutdown has begun. main uses it to
// treat the resulting Accept error as a clean exit, not a crash.
func (s *Server) ShuttingDown() bool { return s.shuttingDown.Load() }

// SaveWorld persists every dimension's block overrides to WorldPath
// (suffixed per non-overworld dimension). It reports an error when
// persistence is disabled so /save-all answers honestly.
func (s *Server) SaveWorld() error {
	if s.WorldPath == "" {
		return errors.New("world saving is disabled (no --world)")
	}
	for name, d := range s.dims {
		path := s.WorldPath
		if name != dimOverworld {
			path += "." + strings.TrimPrefix(name, "minecraft:")
		}
		if err := d.w.Save(path); err != nil {
			return err
		}
	}
	return nil
}

// LoadWorld restores block overrides at boot, per dimension. Missing
// files mean a fresh world, not an error, mirroring vanilla's
// first-boot generate.
func (s *Server) LoadWorld() error {
	if s.WorldPath == "" {
		return nil
	}
	for name, d := range s.dims {
		path := s.WorldPath
		if name != dimOverworld {
			path += "." + strings.TrimPrefix(name, "minecraft:")
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err := d.w.Load(path); err != nil {
			return err
		}
	}
	return nil
}

// disconnectSoon kicks one session and closes the socket after the
// writer flushes the kick.
func (s *Server) disconnectSoon(sess *Session, reason string) {
	s.SendPlayDisconnect(sess, reason)
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = sess.conn.Close()
	}()
}

// Shutdown stops the server like vanilla /stop: listeners close (Serve
// unblocks), every player is kicked with reason, and the world halts.
// Safe to call multiple times and from any goroutine, including a
// command handler.
func (s *Server) Shutdown(reason string) {
	if reason == "" {
		reason = "Server closed"
	}
	s.shuttingDown.Store(true)
	s.listenersMu.Lock()
	for ln := range s.listeners {
		_ = ln.Close()
	}
	s.listenersMu.Unlock()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		s.disconnectSoon(sess, reason)
		return true
	})
	for _, d := range s.dims {
		d.w.Stop()
	}
}
