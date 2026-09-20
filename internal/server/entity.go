package server

import (
	"fmt"
	"math"
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
	// MotionX/MotionZ carry a thrown item's horizontal drift, applied
	// each tick with vanilla's 0.98 air / ~0.55 grounded decay.
	MotionX, MotionZ float64
	// Living mobs carry health and take damage from attacks.
	Living bool
	// NoAI (from the summon NBT) freezes gravity and wander like
	// vanilla's Mob.NoAi flag — the entity still takes knockback.
	NoAI      bool
	Health    float32
	MaxHealth float32
	// invulnTicks/lastHurt are the same twenty-tick post-hit grace
	// players have: a hit inside it only lands for the amount it
	// exceeds the last hit by.
	invulnTicks int32
	lastHurt    float32
	// Dying mobs lie as a corpse for twenty ticks (vanilla's
	// deathTime window) before the death-poof event and removal.
	Dying      bool
	deathTicks int32
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

// SetMotion stores a fresh entity velocity (knockback writes here; the
// next tracker sync or explicit motion packet ships it).
func (m *Manager) SetMotion(id int32, x, y, z float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return false
	}
	e.MotionX, e.MotionY, e.MotionZ = x, y, z
	return true
}

// Damage applies damage to a living entity: the hit lands fully
// outside the post-hit grace window; inside it only the excess over
// the last hit applies (vanilla's invulnerableTime rule). It reports
// the remaining health, the amount dealt, whether the entity exists,
// and whether the hit ran inside the grace window — vanilla skips the
// damage event and knockback on that excess path.
func (m *Manager) Damage(id int32, amount float32) (float32, float32, bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok || e.Dying {
		return 0, 0, false, false
	}
	orig := amount
	graced := e.invulnTicks > 10
	if graced {
		if orig <= e.lastHurt {
			return e.Health, 0, true, true
		}
		amount = orig - e.lastHurt
	}
	e.lastHurt = orig
	e.invulnTicks = 20
	// Vanilla's damage_dealt is the health the target actually lost:
	// the hit's amount clamped to the pre-hit health.
	if amount > e.Health {
		amount = e.Health
	}
	e.Health -= amount
	if e.Health < 0 {
		e.Health = 0
	}
	return e.Health, amount, true, graced
}

// SetDying marks a living entity as dying (health exhausted).
func (m *Manager) SetDying(id int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.byID[id]; ok {
		e.Dying = true
	}
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

// All snapshots the live entity set — entity copies, so mutating a
// returned Entity does not write back (use SetMotion/Damage for that).
func (m *Manager) All() []Entity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Entity, 0, len(m.byID))
	for _, e := range m.byID {
		out = append(out, *e)
	}
	return out
}

// TickDeath advances a dying mob's corpse countdown, reporting the
// tick at which vanilla fires the death-poof event and removes the
// entity (deathTime == 20).
func (m *Manager) TickDeath(id int32) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byID[id]
	if !ok {
		return false
	}
	e.deathTicks++
	return e.deathTicks >= 20
}

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

// lpVec3 encodes the 26.2 low-precision Vec3 wire format (LpVec3):
// a zero/tiny vector (< 3.051944088384301e-5 max abs) is one 0x00
// byte; otherwise each axis is quantized to 15 bits over [-1,1] times
// a ceil-rounded shared scale in the low bits. Bits: scale low-2 at
// 0-1, continuation at 2, x/y/z at 3/18/33 — the packed word goes out
// as two low bytes then a big-endian int of the upper 32, plus a
// VarInt scale-extension when the scale needs more than 2 bits.
func lpVec3(x, y, z float64) []byte {
	san := func(v float64) float64 {
		if math.IsNaN(v) {
			return 0
		}
		return math.Max(-1.7179869183e10, math.Min(1.7179869183e10, v))
	}
	x, y, z = san(x), san(y), san(z)
	max := math.Max(math.Abs(x), math.Max(math.Abs(y), math.Abs(z)))
	if max < 3.051944088384301e-5 {
		return []byte{0}
	}
	scale := int64(math.Ceil(max))
	var scaleBits uint64
	cont := scale > 3
	if cont {
		scaleBits = uint64(scale&3) | 4
	} else {
		scaleBits = uint64(scale)
	}
	pack := func(v float64) uint64 {
		return uint64(math.Round((v/float64(scale)*0.5 + 0.5) * 32766))
	}
	word := scaleBits | pack(x)<<3 | pack(y)<<18 | pack(z)<<33
	hi := uint32(word >> 16)
	out := []byte{byte(word), byte(word >> 8), byte(hi >> 24), byte(hi >> 16), byte(hi >> 8), byte(hi)}
	if cont {
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
