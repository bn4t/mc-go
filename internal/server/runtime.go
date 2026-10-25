package server

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"mc-go/api/frontend"
)

// Start initializes one authoritative runtime. It is idempotent while running;
// after Stop the server cannot restart because its world workers are retired.
// Configure the server before the first call.
func (s *Server) Start() error {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	if s.ShuttingDown() {
		return net.ErrClosed
	}
	if s.runtimeStarted {
		return nil
	}
	if err := s.LoadWorld(); err != nil {
		return err
	}
	stopRcon, err := s.startRcon()
	if err != nil {
		return err
	}
	stopQuery, err := s.startQuery()
	if err != nil {
		stopRcon()
		return err
	}
	if s.StartFrozen {
		s.SetFrozen(true)
	}
	stopTicks := s.startRuntimeTicks()
	stopTime := s.startRuntimeTimeSync()
	// World shard workers are already live. Their optional standalone tick
	// loops must not run here: GameTick is the server's sole game clock.
	s.runtimeStop = func() {
		stopRcon()
		stopQuery()
		stopTime()
		stopTicks()
	}
	s.runtimeStarted = true
	return nil
}

// Stop shuts down all attachments and runtime resources. It is terminal and
// idempotent. Use listener.Close to stop one frontend while retaining the world.
// Gameplay hooks and command callbacks must request Stop asynchronously because
// it joins the simulation action they are running inside.
func (s *Server) Stop() { s.Shutdown("Server closed") }

// Attach serves a protocol-neutral frontend against this runtime. The frontend
// receives a restricted core boundary rather than the concrete server. Callers
// retain ownership of ln and should close it when Attach returns.
func (s *Server) Attach(fe frontend.Frontend, ln net.Listener) error {
	if fe == nil {
		return errors.New("nil frontend")
	}
	return s.attach(ln, func(ctx context.Context) error {
		return fe.Serve(ctx, frontendCore{s}, ln)
	})
}

func (s *Server) attach(ln net.Listener, serve func(context.Context) error) error {
	if ln == nil {
		return errors.New("nil listener")
	}
	if err := s.Start(); err != nil {
		return err
	}
	s.runtimeMu.Lock()
	if s.ShuttingDown() {
		s.runtimeMu.Unlock()
		return net.ErrClosed
	}
	s.listenersMu.Lock()
	if _, exists := s.listeners[ln]; exists {
		s.listenersMu.Unlock()
		s.runtimeMu.Unlock()
		return errors.New("listener already attached")
	}
	if s.listeners == nil {
		s.listeners = make(map[net.Listener]struct{})
	}
	s.listeners[ln] = struct{}{}
	s.listenersMu.Unlock()
	ctx, cancel := context.WithCancel(s.runtimeCtx)
	s.runtimeMu.Unlock()
	defer cancel()
	defer s.removeListener(ln)
	return serve(ctx)
}

func (s *Server) startRuntimeTimeSync() func() {
	stop, done := make(chan struct{}), make(chan struct{})
	every := s.TimeSyncEvery
	if every == 0 {
		every = time.Second
	}
	go func() {
		defer close(done)
		if every < 0 {
			return
		}
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.simulationAction(func() { s.broadcastUpdateTime() })
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop) }); <-done }
}

type frontendCore struct{ s *Server }

func (c frontendCore) Snapshot(ctx context.Context) (snapshot frontend.Snapshot, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if c.s.ShuttingDown() {
		return snapshot, net.ErrClosed
	}
	c.s.simulationAction(func() {
		if err = ctx.Err(); err != nil {
			return
		}
		if c.s.ShuttingDown() {
			err = net.ErrClosed
			return
		}
		snapshot = frontend.Snapshot{MOTD: c.s.MOTD, MaxPlayers: c.s.MaxPlayers,
			Tick: c.s.TickCount(), Frozen: c.s.clock.isFrozen()}
		c.s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay {
				return true
			}
			x, y, z, yaw, pitch, ground := sess.Pos()
			snapshot.Players = append(snapshot.Players, frontend.Player{Name: sess.getName(),
				X: x, Y: y, Z: z, Yaw: yaw, Pitch: pitch, OnGround: ground, Latency: sess.latency(), Gamemode: int32(sess.mode())})
			return true
		})
	})
	sort.Slice(snapshot.Players, func(i, j int) bool { return snapshot.Players[i].Name < snapshot.Players[j].Name })
	return
}

func (c frontendCore) Execute(ctx context.Context, action frontend.Action) (result frontend.Result, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	c.s.simulationAction(func() {
		if err = ctx.Err(); err != nil {
			return
		}
		if c.s.ShuttingDown() {
			err = net.ErrClosed
			return
		}
		switch action.Kind {
		case frontend.Command:
			result = frontend.Result{Applied: true, Output: c.s.rconRunOwned(action.Command)}
		case frontend.Teleport:
			m := action.Movement
			if badFloat(m.X) || badFloat(m.Y) || badFloat(m.Z) {
				err = errors.New("invalid teleport coordinates")
				return
			}
			result.Applied = c.s.teleportPlayerOwned(action.Player, m.X, m.Y, m.Z)
		case frontend.Kick:
			result.Applied = c.s.kickPlayerOwned(action.Player, action.Reason)
		case frontend.Move:
			sess := c.s.findSessionByName(action.Player)
			if sess == nil || sess.state.Load() != StatePlay {
				return
			}
			result.Applied = c.s.applyPlayerMoveOwned(sess, action.Movement)
		default:
			err = frontend.ErrUnsupportedAction
		}
	})
	return
}
