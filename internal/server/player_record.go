package server

import (
	"sort"
	"sync"
	"sync/atomic"

	"mc-go/api/frontend"
	"mc-go/api/player"
)

// playerRecord is canonical player identity and pose, independent of transport.
// A Java Session embeds its actual record; neutral avatars allocate only this
// record and a typed mailbox. java is either a real connection or nil.
type playerRecord struct {
	incarnation player.ID
	core        *Server
	java        *Session
	avatar      *avatarConnection
	active      atomic.Bool
	published   atomic.Pointer[player.State] // immutable last committed observation
	counted     bool                         // simulation-owned reservation, including configuring Java
	admitted    bool                         // committed presence, simulation-owned
	left        bool                         // terminal incarnation, simulation-owned
	replacement bool                         // an exact-profile successor retains the Java tab row
	nameMu      sync.RWMutex
	name        string
	uuid        [16]byte
	posMu       sync.RWMutex
	px, py, pz  float64
	yaw, pitch  float32
	onGround    bool
	dim         atomic.Pointer[dimension]
	entityID    atomic.Int32
	// Simulation-owned current bounding-box scale refreshes on the connection frame, after
	// player touch work. Zero is the constructor default scale of one.
	collisionScale float32
	modeMu         sync.RWMutex
	gameMode       byte
}

func (p *playerRecord) ID() player.ID { return p.incarnation }

// markStateDirty records only committed incarnations. Publication waits for a
// complete owner action, so setters never emit an intermediate transaction.
func (p *playerRecord) markStateDirty() {
	if p.core != nil && p.active.Load() {
		p.core.markPlayerDirty(p)
	}
}

func (p *playerRecord) setDimension(d *dimension) {
	p.dim.Store(d)
	p.markStateDirty()
}

func (p *playerRecord) setEntityID(id int32) {
	p.collisionScale = 1 // a new physical player starts with constructor dimensions
	p.entityID.Store(id)
	p.markStateDirty()
}
func (p *playerRecord) profile() player.Profile {
	p.nameMu.RLock()
	defer p.nameMu.RUnlock()
	return player.Profile{Name: p.name, UUID: p.uuid}
}
func (p *playerRecord) position() frontend.Movement {
	p.posMu.RLock()
	defer p.posMu.RUnlock()
	return frontend.Movement{X: p.px, Y: p.py, Z: p.pz, Yaw: p.yaw, Pitch: p.pitch, OnGround: p.onGround}
}
func (p *playerRecord) setPosition(m frontend.Movement) {
	if p.java != nil {
		p.java.setPos(m.X, m.Y, m.Z, m.Yaw, m.Pitch, m.OnGround)
		return
	}
	p.posMu.Lock()
	p.px, p.py, p.pz, p.yaw, p.pitch, p.onGround = m.X, m.Y, m.Z, m.Yaw, m.Pitch, m.OnGround
	p.posMu.Unlock()
	p.markStateDirty()
}
func (p *playerRecord) dimension() *dimension {
	if d := p.dim.Load(); d != nil {
		return d
	}
	if p.core != nil {
		return p.core.dims[dimOverworld]
	}
	if p.java != nil {
		return p.java.server.dims[dimOverworld]
	}
	return nil
}
func (p *playerRecord) dimName() string {
	if d := p.dimension(); d != nil {
		return d.name
	}
	return dimOverworld
}
func (p *playerRecord) mode() byte {
	p.modeMu.RLock()
	defer p.modeMu.RUnlock()
	return p.gameMode
}
func (p *playerRecord) physicalDimName() string {
	dimension := p.dimName()
	// execute-in temporarily changes command context, not the player's
	// physical dimension. Tick-driving commands may yield ownership before
	// that context is restored, so observations must use the physical value.
	if p.java != nil && p.java.dimSwapDepth.Load() > 0 {
		if physical := p.java.dimSwapPrev.Load(); physical != nil {
			dimension = physical.name
		}
	}
	return dimension
}

func (p *playerRecord) snapshot() player.State {
	return player.State{ID: p.ID(), Profile: p.profile(), EntityID: p.entityID.Load(), Dimension: p.physicalDimName(), Pose: p.position(), Gamemode: int32(p.mode())}
}

func (s *Server) findPlayerByIDOwned(id player.ID) *playerRecord {
	s.playersMu.RLock()
	p := s.players[id]
	s.playersMu.RUnlock()
	if p == nil || !p.active.Load() {
		return nil
	}
	return p
}
func (s *Server) findPlayerByNameOwned(name string) *playerRecord {
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	for _, p := range s.players {
		if p.active.Load() && p.profile().Name == name {
			return p
		}
	}
	return nil
}
func (s *Server) activePlayersOwned() []*playerRecord {
	s.playersMu.RLock()
	defer s.playersMu.RUnlock()
	var out []*playerRecord
	for _, p := range s.players {
		if p.active.Load() {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].profile().Name < out[j].profile().Name })
	return out
}

// These queries are safe inside hooks as well as outside the simulation owner.
func (s *Server) playerIDByName(name string) (player.ID, bool) {
	p := s.findPlayerByNameOwned(name)
	if p == nil {
		return player.ID{}, false
	}
	return p.ID(), true
}
func (s *Server) playerStateByID(id player.ID) (player.State, bool) {
	p := s.findPlayerByIDOwned(id)
	if p == nil {
		return player.State{}, false
	}
	if state := p.published.Load(); state != nil {
		return *state, true
	}
	return player.State{}, false
}
