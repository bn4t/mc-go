package server

import (
	"fmt"
	"sync"
	"sync/atomic"

	"mc-go/internal/protocol"
)

// Entity is one spawned world entity. Dim is the dimension registry
// name it lives in; empty means the overworld.
type Entity struct {
	ID                  int32
	UUID                [16]byte
	Type                int32
	Dim                 string
	X, Y, Z             float64
	Yaw, Pitch, HeadYaw float32
	Data                int32
	VX, VY, VZ          int16
	// Item is set on item entities (Type minecraft:item): the stack
	// the entity represents, with a vanilla-like pickup delay and
	// float vertical motion for drop physics.
	Item        ItemStack
	PickupDelay int
	MotionY     float64
	// Living mobs carry health and take damage from attacks.
	Living    bool
	Health    float32
	MaxHealth float32
	// WanderTicks counts AI steps; the wander angle derives from it so
	// movement is deterministic for tests.
	WanderTicks int64
	Grounded    bool
}

// Manager owns entity ids and the live set. All methods are safe for
// concurrent use; entity AI ticks read snapshots.
type Manager struct {
	mu     sync.RWMutex
	byID   map[int32]*Entity
	nextID atomic.Int32
}

// NewManager starts player-adjacent ids at 2 (entity 1 is the player).
func NewManager() *Manager {
	m := &Manager{byID: make(map[int32]*Entity)}
	m.nextID.Store(2)
	return m
}

// Spawn registers an entity and returns it. A preset UUID (players
// carry their profile id) is kept; zero UUIDs get a synthetic one.
// An empty Dim defaults to the overworld.
func (m *Manager) Spawn(e *Entity) *Entity {
	e.ID = m.nextID.Add(1) - 1
	if e.UUID == ([16]byte{}) {
		e.UUID = offlineUUID(fmt.Sprintf("entity-%d", e.ID))
	}
	if e.Dim == "" {
		e.Dim = dimOverworld
	}
	m.mu.Lock()
	m.byID[e.ID] = e
	m.mu.Unlock()
	return e
}

// SetDimension moves an entity between dimensions.
func (m *Manager) SetDimension(id int32, dim string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.byID[id]; ok {
		e.Dim = dim
	}
}

// Update moves an entity, reporting whether it existed. Movers publish
// the change; the manager only stores it.
func (m *Manager) Update(id int32, x, y, z float64, yaw, pitch float32, onGround bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return false
	}
	e.X, e.Y, e.Z = x, y, z
	e.Yaw, e.Pitch, e.HeadYaw = yaw, pitch, yaw
	_ = onGround
	return true
}

// Damage subtracts from a living entity's health under the manager
// lock, returning the new health.
func (m *Manager) Damage(id int32, amount float32) (float32, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return 0, false
	}
	e.Health -= amount
	if e.Health < 0 {
		e.Health = 0
	}
	return e.Health, true
}

// Remove deletes an entity, reporting whether it existed.
func (m *Manager) Remove(id int32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		return false
	}
	delete(m.byID, id)
	return true
}

// Get returns a copy of an entity.
func (m *Manager) Get(id int32) (Entity, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.byID[id]
	if !ok {
		return Entity{}, false
	}
	return *e, true
}

// Count reports live entities.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byID)
}

// encodeSpawn builds a Spawn Entity body: VarInt id, UUID, VarInt
// type, position, LpVec3 velocity, rotation, head yaw, data. Field
// order and types follow the 776 protocol docs.
func encodeSpawn(e *Entity) []byte {
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.UUID(e.UUID)
	b.VarInt(e.Type)
	b.Double(e.X)
	b.Double(e.Y)
	b.Double(e.Z)
	b.Raw(lpVec3(float64(e.VX)/8000, float64(e.VY)/8000, float64(e.VZ)/8000))
	b.Byte(angleByte(e.Pitch))
	b.Byte(angleByte(e.Yaw))
	b.Byte(angleByte(e.HeadYaw))
	b.VarInt(e.Data)
	return b.Bytes()
}

// lpVec3 encodes a low-precision velocity vector. Zero (and any vector
// whose largest component is below 1/32766) is a single 0x00 byte,
// which covers every velocity the server currently emits. Non-zero
// vectors are quantized to 15 bits per axis with a 2-bit scale and a
// continuation bit for larger magnitudes, matching the documented
// LpVec3 bit distribution.
func lpVec3(x, y, z float64) []byte {
	max := 0.0
	for _, v := range []float64{x, y, z} {
		if v < 0 {
			v = -v
		}
		if v > max {
			max = v
		}
	}
	if max < 1.0/32766.0 {
		return []byte{0}
	}
	scale := int64(max)
	if float64(scale) < max {
		scale++ // rounded up
	}
	q := func(v float64) uint64 {
		u := (v/float64(scale) + 1) / 2 * 32766
		if u < 0 {
			u = 0
		}
		if u > 32766 {
			u = 32766
		}
		return uint64(u + 0.5)
	}
	cont := uint64(0)
	if scale >= 3 {
		cont = 1
	}
	word := q(x) | q(y)<<15 | q(z)<<30 | cont<<45 | uint64(scale&0x3)<<46
	out := []byte{
		byte(word), byte(word >> 8),
		byte(word >> 40), byte(word >> 32), byte(word >> 24), byte(word >> 16),
	}
	if cont == 1 {
		var vb protocol.Buffer
		vb.VarInt(int32(scale >> 2))
		out = append(out, vb.Bytes()...)
	}
	return out
}

// encodeEntityTeleport builds a 776 Teleport Entity (Entity Position
// Sync) body: VarInt id, position, velocity, rotation, on-ground flag.
func encodeEntityTeleport(id int32, x, y, z float64, yaw, pitch float32, onGround bool) []byte {
	var b protocol.Buffer
	b.VarInt(id)
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Double(0) // velocity x
	b.Double(0) // velocity y
	b.Double(0) // velocity z
	b.Float(yaw)
	b.Float(pitch)
	b.Bool(onGround)
	return b.Bytes()
}

// playerEntity snapshots a live player as a spawnable entity: profile
// UUID, player type, current position and look.
func playerEntity(sess *Session) (Entity, bool) {
	eid := sess.entityID.Load()
	m := sess.server.entities
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.byID[eid]
	if !ok {
		return Entity{}, false
	}
	return *e, true
}

// sendEntitySpawnsTo sends every other spawned player in the session's
// dimension to it (join burst); failures only fail the join.
func (s *Server) sendEntitySpawnsTo(sess *Session) error {
	var first error
	dim := sess.dimName()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == sess || other.state.Load() != StatePlay || !other.spawned.Load() || other.getName() == "" {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		e, ok := playerEntity(other)
		if !ok {
			return true
		}
		if err := sess.write(sess.ids().PlaySpawnEntity, encodeSpawn(&e)); err != nil && first == nil {
			first = err
		}
		return true
	})
	return first
}

// broadcastEntitySpawn announces one entity to every other spawned
// play-stage session in the entity's dimension.
func (s *Server) broadcastEntitySpawn(except *Session, e Entity) {
	s.broadcastEntitySpawnIn(e.Dim, except, e)
}

// broadcastEntitySpawnIn announces one entity to spawned sessions in
// dimension dim.
func (s *Server) broadcastEntitySpawnIn(dim string, except *Session, e Entity) {
	body := encodeSpawn(&e)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		_ = sess.write(sess.ids().PlaySpawnEntity, body)
		return true
	})
}

// broadcastEntityTeleport fans one entity movement to every other
// spawned play-stage session sharing the entity's dimension.
func (s *Server) broadcastEntityTeleport(except *Session, id int32, x, y, z float64, yaw, pitch float32, onGround bool) {
	dim := dimOverworld
	if except != nil {
		dim = except.dimName()
	} else if e, ok := s.entities.Get(id); ok {
		dim = e.Dim
	}
	body := encodeEntityTeleport(id, x, y, z, yaw, pitch, onGround)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		_ = sess.write(sess.ids().PlayEntityTeleportCB, body)
		return true
	})
}

// broadcastEntityRemove drops entity ids from every spawned play-stage
// session's tracker in the entity's dimension. Unknown ids are ignored
// client-side, so a missed spawn (early join failure) cannot corrupt
// anyone.
func (s *Server) broadcastEntityRemove(ids []int32) {
	s.broadcastEntityRemoveIn(dimOverworld, nil, ids)
}

// broadcastEntityRemoveIn drops entity ids for sessions in dimension
// dim, optionally skipping one session.
func (s *Server) broadcastEntityRemoveIn(dim string, except *Session, ids []int32) {
	if len(ids) == 0 {
		return
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		for _, id := range ids {
			_ = sess.write(sess.ids().PlayRemoveEntity, encodeRemove(id))
		}
		return true
	})
}

// encodeRemove builds a Remove Entities body for one id.
func encodeRemove(id int32) []byte {
	var b protocol.Buffer
	b.VarInt(1)
	b.VarInt(id)
	return b.Bytes()
}

// angleByte compresses degrees into a protocol angle byte,
// wrapping full turns and negatives into [0, 256).
func angleByte(deg float32) byte {
	v := int(float64(deg)*256.0/360.0) % 256
	if v < 0 {
		v += 256
	}
	return byte(v)
}
