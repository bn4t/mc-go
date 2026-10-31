package server

import (
	"math"
	"strings"
	"sync"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// The ender dragon, measured from the 26.2 jar
// (net.minecraft.world.entity.boss.enderdragon.EnderDragon and its
// phases) and the wire:
//
//   - Construction allocates the dragon's id and then eight
//     EnderDragonPart ids (head, neck, body, tail x3, wing x2); the
//     client rebuilds the parts as id+1..id+8 from the add_entity
//     packet, so the next entity spawned takes id+9. The dragon has
//     noPhysics, 200 max health, DATA_PHASE (16, INT) defaulting to
//     HOVERING (10); the pairing pack carries health 200 and the phase.
//   - aiStep: a dead dragon only rises in tickDeath; otherwise the
//     crystal check (heal 1 every 10 ticks while a nearest crystal
//     stands; a 1-in-10 search for the nearest crystal within 32), the
//     flight-history sample, the current phase's server tick (twice if
//     it switched), then the flight toward the phase's target: the
//     vertical pull clamped by the phase's fly speed, the turn
//     (yRotA *= 0.8, += clamp(angle, +-50) * turnSpeed, yaw += 0.1 *
//     yRotA), a 0.06-scaled forward impulse and the 0.91/0.8-0.95 drag.
//     Parts are placed from the yaw and history; the wings knock
//     non-creative living entities away (5 damage when they were not
//     hurt by a mob in the last 2 ticks, not while sitting), head and
//     neck hit for 10; the head, neck and body boxes break every
//     non-air block outside dragon_transparent unless mob_griefing is
//     off or the block is dragon_immune (then the dragon is "in wall",
//     slowed to 0.8), with level event 2008 after a break.
//   - Phases (EnderDragonPhase ids): 0 HoldingPattern, 1 StrafePlayer,
//     2 LandingApproach, 3 Landing, 4 Takeoff, 5 SittingFlaming,
//     6 SittingScanning, 7 SittingAttacking, 8 ChargingPlayer, 9 Dying,
//     10 Hover; the 24-node A* graph (12 outer nodes at radius 60, 8 at
//     40, 4 at 20, each at max(73, surface + 5/15/5)) drives the
//     flying phases.
//   - hurt: a dying dragon takes nothing; sitting phases turn arrows
//     and wind charges away (ignited); a non-head part takes
//     amount/4 + min(amount, 1); only player-caused or explosion damage
//     lands, and 25% of max health taken while sitting makes it take
//     off. The killing blow (not sitting) leaves 1 health and switches
//     to DYING: the dragon flies to the podium, then tickDeath runs
//     200 ticks - global level event 1028 on the first, experience
//     orbs worth 8% of 12000 (500 after the first kill) every 5 ticks
//     past 150, 20% on the last, rising 0.1 a tick - before the fight
//     records the kill and the dragon is removed. /kill removes it at
//     once and records the kill.

// EnderDragonPhase ids.
const (
	phaseHoldingPattern = iota
	phaseStrafePlayer
	phaseLandingApproach
	phaseLanding
	phaseTakeoff
	phaseSittingFlaming
	phaseSittingScanning
	phaseSittingAttacking
	phaseChargingPlayer
	phaseDying
	phaseHover
	phaseCount
)

type dvec struct{ x, y, z float64 }

func (a dvec) sub(b dvec) dvec      { return dvec{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a dvec) dot(b dvec) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }
func (a dvec) dist2(b dvec) float64 { d := a.sub(b); return d.dot(d) }
func (a dvec) normalize() dvec {
	l := math.Sqrt(a.dot(a))
	if l < 1e-5 {
		return dvec{}
	}
	return dvec{a.x / l, a.y / l, a.z / l}
}

// dragonSample is a DragonFlightHistory sample.
type dragonSample struct {
	y    float64
	yRot float32
}

// pfNode is a pathfinder Node of the dragon's graph.
type pfNode struct {
	x, y, z  int32
	g, h, f  float32
	closed   bool
	cameFrom *pfNode
	heapIdx  int
}

func (n *pfNode) distTo(o *pfNode) float32 {
	dx, dy, dz := float32(o.x-n.x), float32(o.y-n.y), float32(o.z-n.z)
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func (n *pfNode) dist2(o *pfNode) float32 {
	dx, dy, dz := float32(o.x-n.x), float32(o.y-n.y), float32(o.z-n.z)
	return dx*dx + dy*dy + dz*dz
}

// dragonPath is a pathfinder Path over node copies.
type dragonPath struct {
	nodes []world.Pos
	next  int
}

func (p *dragonPath) done() bool           { return p.next >= len(p.nodes) }
func (p *dragonPath) advance()             { p.next++ }
func (p *dragonPath) nextNode() world.Pos  { return p.nodes[p.next] }
func pathDone(p *dragonPath) bool          { return p != nil && p.done() }
func pathMissingOrDone(p *dragonPath) bool { return p == nil || p.done() }

// dragonPart is one EnderDragonPart.
type dragonPart struct {
	x, y, z float64
	w, h    float64
}

func (p dragonPart) box() [6]float64 {
	return [6]float64{p.x - p.w/2, p.y, p.z - p.w/2, p.x + p.w/2, p.y + p.h, p.z + p.w/2}
}

// dragonPartDims are the parts' EntityDimensions.scalable sizes.
var dragonPartDims = [8][2]float64{{1, 1}, {3, 3}, {5, 3}, {2, 2}, {2, 2}, {2, 2}, {4, 2}, {4, 2}}

const (
	partHead = iota
	partNeck
	partBody
	partTail1
	partTail2
	partTail3
	partWing1
	partWing2
)

// dragonPhaseState is one phase instance's fields (the phase manager
// keeps one instance per phase for the dragon's life).
type dragonPhaseState struct {
	target *dvec
	path   *dragonPath
}

type enderDragon struct {
	fight     bool
	phase     int
	phaseSent int
	ph        [phaseCount]dragonPhaseState

	yRotA  float32
	inWall bool
	hist   [64]dragonSample
	head   int

	parts [8]dragonPart

	nodes    [24]*pfNode
	nodeAdj  [24]int32
	nodesSet bool

	nearestCrystal int32
	deathTime      int32
	sittingDamage  float32
	lastMobHit     map[int32]int64

	// Phase-specific state.
	holdingClockwise bool
	strafeClockwise  bool
	fireballCharge   int
	strafeTarget     int32 // attack target entity id (player entity ids too)
	timeSinceCharge  int
	takeoffFirstTick bool
	scanningTime     int
	attackingTicks   int
	flameTicks       int
	flameCount       int
	flameCloud       int32
	deathPhaseTime   int

	rng *javaRand
}

var dragonRandMu sync.Mutex
var dragonRandSeq int64

func newDragonRand() *javaRand {
	dragonRandMu.Lock()
	dragonRandSeq++
	seed := time.Now().UnixNano() ^ dragonRandSeq*0x5DEECE66D
	dragonRandMu.Unlock()
	r := &javaRand{}
	r.setSeed(seed)
	return r
}

func (r *javaRand) nextBoundedInt(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.nextIntN(int32(n)))
}

// Mth.sin / Mth.cos: the 65536-entry float table.
var mthSinTable = func() []float32 {
	t := make([]float32, 65536)
	for i := range t {
		t[i] = float32(math.Sin(float64(i) * math.Pi * 2 / 65536))
	}
	return t
}()

func mthSin(v float64) float32 { return mthSinTable[int64(v*10430.378350470453)&65535] }
func mthCos(v float64) float32 { return mthSinTable[int64(v*10430.378350470453+16384)&65535] }

func wrapDeg32(v float32) float32 {
	f := float32(math.Mod(float64(v), 360))
	if f >= 180 {
		f -= 360
	}
	if f < -180 {
		f += 360
	}
	return f
}

func wrapDeg64(v float64) float64 {
	f := math.Mod(v, 360)
	if f >= 180 {
		f -= 360
	}
	if f < -180 {
		f += 360
	}
	return f
}

func clamp32(v, lo, hi float32) float32 {
	return float32(math.Max(float64(lo), math.Min(float64(hi), float64(v))))
}

// newEnderDragon constructs a dragon entity (not yet in the level).
func (s *Server) newEnderDragon(d *dimension, x, y, z float64) *Entity {
	e := &Entity{
		Type: mustEntityType("ender_dragon"),
		Dim:  d.name,
		X:    x, Y: y, Z: z,
		Living:    true,
		Health:    200,
		MaxHealth: 200,
		dragon: &enderDragon{phase: phaseHover, phaseSent: phaseHover, takeoffFirstTick: true, head: -1,
			lastMobHit: map[int32]int64{}, rng: newDragonRand()},
	}
	for i := range e.dragon.parts {
		e.dragon.parts[i] = dragonPart{x: x, y: y, z: z, w: dragonPartDims[i][0], h: dragonPartDims[i][1]}
	}
	return e
}

// spawnEnderDragon adds a dragon to the level: its id, then the eight
// part ids the client derives (id+1..id+8).
func (s *Server) spawnEnderDragon(e *Entity) *Entity {
	e = s.entities.Spawn(e)
	s.entities.nextID.Add(8)
	e.Dormant = s.shouldPark(e.Dim)
	if !e.Dormant {
		s.broadcastEntitySpawn(nil, *e)
	}
	return e
}

// summonEndEntity is /summon for end_crystal and ender_dragon.
func (s *Server) summonEndEntity(d *dimension, name string, x, y, z float64, tag *snbtTag) *Entity {
	if name == "end_crystal" {
		return s.summonEndCrystal(d, x, y, z, tag)
	}
	e := s.newEnderDragon(d, x, y, z)
	if tag != nil {
		if m, ok := snbtToValue(tag).(map[string]any); ok {
			applyDragonSave(e, m)
			e.dragon.phaseSent = phaseHover
		}
	}
	return s.spawnEnderDragon(e)
}

// applyDragonSave reads the dragon keys of a save compound.
func applyDragonSave(e *Entity, m map[string]any) {
	dr := e.dragon
	if v, ok := nbtInt32(m["DragonPhase"]); ok {
		p := int(v)
		if p < 0 || p >= phaseCount {
			p = phaseHoldingPattern
		}
		dr.phase = p
	}
	if v, ok := nbtInt32(m["DragonDeathTime"]); ok {
		dr.deathTime = v
	}
	if v, ok := nbtFloat(m["sitting_damage_received"]); ok {
		dr.sittingDamage = v
	}
	if v, ok := nbtFloat(m["Health"]); ok {
		e.Health = v
	}
	if rot, ok := nbtVec2f(m["Rotation"]); ok {
		e.Yaw, e.Pitch = rot[0], rot[1]
		e.HeadYaw = e.Yaw
	}
	if u, ok := m["UUID"].([]int32); ok && len(u) == 4 {
		e.UUID = uuidFromInts(u)
	}
	if v, ok := m["NoAI"]; ok {
		e.NoAI = nbtTruth(v)
	}
	if v, ok := m["Silent"]; ok {
		e.Silent = nbtTruth(v)
	}
	if mot, ok := nbtVec3(m["Motion"]); ok {
		e.MotionX, e.MotionY, e.MotionZ = mot[0], mot[1], mot[2]
	}
}

// dragonSaveFields adds EnderDragon.addAdditionalSaveData's keys.
func dragonSaveFields(e *Entity, m map[string]any) {
	if e.dragon == nil {
		return
	}
	m["DragonPhase"] = int32(e.dragon.phase)
	m["DragonDeathTime"] = e.dragon.deathTime
	m["sitting_damage_received"] = e.dragon.sittingDamage
}

// restoreEnderDragon respawns a saved dragon.
func (s *Server) restoreEnderDragon(d *dimension, save map[string]any, pos [3]float64) *Entity {
	e := s.newEnderDragon(d, pos[0], pos[1], pos[2])
	applyDragonSave(e, save)
	e.dragon.phaseSent = e.dragon.phase
	e = s.entities.Spawn(e)
	s.entities.nextID.Add(8)
	if f := d.fight; f != nil && f.dragonUUID != nil && *f.dragonUUID == e.UUID {
		e.dragon.fight = true
	}
	e.ensureMetaInit()
	e.metaSent.health = e.Health
	return e
}

// encodeDragonPhaseEntry writes DATA_PHASE.
func encodeDragonPhaseEntry(b *protocol.Buffer, phase int) {
	b.Byte(16)
	b.Byte(1) // INT
	b.VarInt(int32(phase))
}

// dragonMetaPairing is the DATA_PHASE share of the pairing pack.
func dragonMetaPairing(e *Entity, b *protocol.Buffer) {
	if e.dragon != nil && e.dragon.phase != phaseHover {
		encodeDragonPhaseEntry(b, e.dragon.phase)
	}
}

// dragonMetaDirty is the DATA_PHASE share of the dirty pack.
func dragonMetaDirty(e *Entity, b *protocol.Buffer) {
	if e.dragon != nil && e.dragon.phase != e.dragon.phaseSent {
		encodeDragonPhaseEntry(b, e.dragon.phase)
		e.dragon.phaseSent = e.dragon.phase
	}
}

func isSittingPhase(p int) bool {
	return p == phaseSittingFlaming || p == phaseSittingScanning || p == phaseSittingAttacking || p == phaseHover
}

// dragonSetPhase is EnderDragonPhaseManager.setPhase: end the current
// phase, switch (DATA_PHASE) and begin the new one.
func (s *Server) dragonSetPhase(e *Entity, p int) {
	dr := e.dragon
	if dr.phase == p {
		return
	}
	// end()
	if dr.phase == phaseSittingFlaming && dr.flameCloud != 0 {
		if c, ok := s.entities.GetPtr(dr.flameCloud); ok {
			s.entities.Remove(c.ID)
			s.broadcastEntityRemoveIn(c.Dim, nil, []int32{c.ID})
		}
		dr.flameCloud = 0
	}
	dr.phase = p
	e.MetaDirty = true
	// begin()
	st := &dr.ph[p]
	st.target, st.path = nil, nil
	switch p {
	case phaseStrafePlayer:
		dr.fireballCharge = 0
		dr.strafeTarget = 0
	case phaseTakeoff:
		dr.takeoffFirstTick = true
	case phaseChargingPlayer:
		dr.timeSinceCharge = 0
	case phaseSittingScanning:
		dr.scanningTime = 0
	case phaseSittingAttacking:
		dr.attackingTicks = 0
	case phaseSittingFlaming:
		dr.flameTicks = 0
		dr.flameCount++
	case phaseDying:
		dr.deathPhaseTime = 0
	}
}

// dragonTickStep ticks every dragon in the level's entity pass.
func (s *Server) dragonTickStep() {
	var drs []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.dragon != nil && !e.Dormant {
			drs = append(drs, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range drs {
		s.dragonTick(e)
	}
}

func (s *Server) dragonFightOf(e *Entity) *dragonFight {
	d := s.dims[e.Dim]
	if d == nil || d.fight == nil {
		return nil
	}
	if !e.dragon.fight && d.fight.dragonUUID != nil && *d.fight.dragonUUID == e.UUID {
		e.dragon.fight = true
	}
	if !e.dragon.fight {
		return nil
	}
	return d.fight
}

// dragonTick is the dragon's Entity/LivingEntity tick: baseTick's
// tracker flush, the countdowns, then tickDeath or aiStep.
func (s *Server) dragonTick(e *Entity) {
	// ServerEntity.sendChanges runs from the chunk source tick, early in
	// the level tick: what changed last tick (or in a packet or command
	// since) goes out before this tick's own changes.
	s.dragonMarkDirty(e)
	s.mobSyncFlush(e)
	e.TickCount++
	e.livingEffectTickSerial++
	if e.invulnTicks > 0 {
		e.invulnTicks--
	}
	if e.HurtTime > 0 {
		e.HurtTime--
	}
	if e.LastHurtByPlayerTicks > 0 {
		e.LastHurtByPlayerTicks--
	}
	e.FireTicks = 0
	if e.Health <= 0 {
		if s.dragonTickDeath(e) {
			return
		}
	} else {
		s.dragonAiStep(e)
	}
}

// dragonMarkDirty raises MetaDirty when a synched value moved.
func (s *Server) dragonMarkDirty(e *Entity) {
	e.ensureMetaInit()
	if e.Health != e.metaSent.health || e.Pose != e.metaSent.pose || e.dragon.phase != e.dragon.phaseSent {
		e.MetaDirty = true
	}
}

// dragonAiStep is EnderDragon.aiStep on the server.
func (s *Server) dragonAiStep(e *Entity) {
	dr := e.dragon
	d := s.dims[e.Dim]
	if d == nil {
		return
	}
	f := s.dragonFightOf(e)
	s.dragonCheckCrystals(e)
	e.Yaw = wrapDeg32(e.Yaw)
	if e.NoAI {
		return
	}
	// DragonFlightHistory.record
	sm := dragonSample{y: e.Y, yRot: e.Yaw}
	if dr.head < 0 {
		for i := range dr.hist {
			dr.hist[i] = sm
		}
	}
	dr.head = (dr.head + 1) & 63
	dr.hist[dr.head] = sm

	cur := dr.phase
	s.dragonPhaseTick(e, d, f)
	if dr.phase != cur {
		s.dragonPhaseTick(e, d, f)
	}
	if t := dr.flyTarget(); t != nil {
		dx, dy, dz := t.x-e.X, t.y-e.Y, t.z-e.Z
		dist2 := dx*dx + dy*dy + dz*dz
		speed := dr.flySpeed()
		hd := math.Sqrt(dx*dx + dz*dz)
		if hd > 0 {
			dy = math.Max(float64(-speed), math.Min(float64(speed), dy/hd))
		}
		e.MotionY += dy * 0.01
		e.Yaw = wrapDeg32(e.Yaw)
		dir := t.sub(dvec{e.X, e.Y, e.Z}).normalize()
		yr := float64(e.Yaw) * 0.017453292
		look := dvec{float64(mthSin(yr)), e.MotionY, float64(-mthCos(yr))}.normalize()
		f4 := float32(math.Max(float64((float32(look.dot(dir))+0.5)/1.5), 0))
		if math.Abs(dx) > 1e-5 || math.Abs(dz) > 1e-5 {
			f5 := clamp32(wrapDeg32(180-float32(math.Atan2(dx, dz))*57.295776-e.Yaw), -50, 50)
			dr.yRotA *= 0.8
			dr.yRotA += f5 * dr.turnSpeed(e)
			e.Yaw += dr.yRotA * 0.1
		}
		f6 := float32(2.0 / (dist2 + 1.0))
		amount := float64(0.06 * (f4*f6 + 1 - f6))
		yr = float64(e.Yaw) * 0.017453292
		// moveRelative(amount, (0, 0, -1)).
		e.MotionX += amount * float64(mthSin(yr))
		e.MotionZ += -amount * float64(mthCos(yr))
		scale := 1.0
		if dr.inWall {
			scale = 0.8
		}
		e.X += e.MotionX * scale
		e.Y += e.MotionY * scale
		e.Z += e.MotionZ * scale
		dn := dvec{e.MotionX, e.MotionY, e.MotionZ}.normalize()
		d6 := 0.8 + 0.15*(dn.dot(look)+1)/2
		e.MotionX *= d6
		e.MotionY *= 0.9100000262260437
		e.MotionZ *= d6
		e.PosDirty = true
	}
	e.HeadYaw = e.Yaw
	s.dragonPlaceParts(e, d)
	if f != nil {
		s.fightUpdateDragon(f, e)
	}
}

func (dr *enderDragon) sample(back int) dragonSample { return dr.hist[(dr.head-back)&63] }

// dragonPlaceParts is aiStep's part layout, the wing/head contact and
// the wall check.
func (s *Server) dragonPlaceParts(e *Entity, d *dimension) {
	dr := e.dragon
	f12 := float32(dr.sample(5).y-dr.sample(10).y) * 10 * 0.017453292
	c12, s12 := mthCos(float64(f12)), mthSin(float64(f12))
	f13 := e.Yaw * 0.017453292
	s13, c13 := mthSin(float64(f13)), mthCos(float64(f13))
	set := func(i int, dx, dy, dz float64) {
		dr.parts[i].x, dr.parts[i].y, dr.parts[i].z = e.X+dx, e.Y+dy, e.Z+dz
	}
	set(partBody, float64(s13*0.5), 0, float64(-c13*0.5))
	set(partWing1, float64(c13*4.5), 2, float64(s13*4.5))
	set(partWing2, float64(c13*-4.5), 2, float64(s13*-4.5))
	if e.HurtTime == 0 {
		s.dragonKnockBack(e, d, dr.parts[partWing1].box())
		s.dragonKnockBack(e, d, dr.parts[partWing2].box())
		s.dragonContactHurt(e, d, dr.parts[partHead].box())
		s.dragonContactHurt(e, d, dr.parts[partNeck].box())
	}
	ay := e.Yaw*0.017453292 - dr.yRotA*0.01
	f14, f15 := mthSin(float64(ay)), mthCos(float64(ay))
	headY := dr.headYOffset()
	set(partHead, float64(f14*6.5*c12), float64(headY+s12*6.5), float64(-f15*6.5*c12))
	set(partNeck, float64(f14*5.5*c12), float64(headY+s12*5.5), float64(-f15*5.5*c12))
	s5 := dr.sample(5)
	for k := 0; k < 3; k++ {
		sk := dr.sample(12 + k*2)
		f17 := e.Yaw*0.017453292 + wrapDeg32(sk.yRot-s5.yRot)*0.017453292
		f18, f19 := mthSin(float64(f17)), mthCos(float64(f17))
		f21 := float32(k+1) * 2
		set(partTail1+k, float64(-(s13*1.5+f18*f21)*c12),
			sk.y-s5.y-float64((f21+1.5)*s12)+1.5,
			float64((c13*1.5+f19*f21)*c12))
	}
	// Each part performs destruction even if an earlier part hit an
	// immune block. The measured aiStep checks all three before combining
	// their wall flags; a short-circuit would preserve blocks near the body.
	headBlocked := s.dragonCheckWalls(e, d, dr.parts[partHead].box())
	neckBlocked := s.dragonCheckWalls(e, d, dr.parts[partNeck].box())
	bodyBlocked := s.dragonCheckWalls(e, d, dr.parts[partBody].box())
	dr.inWall = headBlocked || neckBlocked || bodyBlocked
}

func (dr *enderDragon) headYOffset() float32 {
	if isSittingPhase(dr.phase) {
		return -1
	}
	return float32(dr.sample(5).y - dr.sample(0).y)
}

// dragonVictims lists the NO_CREATIVE_OR_SPECTATOR living entities and
// players whose boxes meet box.
func (s *Server) dragonVictims(e *Entity, box [6]float64) (mobs []*Entity, players []*Session) {
	meets := func(x, y, z, w, h float64) bool {
		return x-w/2 < box[3] && x+w/2 > box[0] && y < box[4] && y+h > box[1] && z-w/2 < box[5] && z+w/2 > box[2]
	}
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if !o.Living || o.Dying || o.Dormant || o.Dim != e.Dim || o.ID == e.ID || o.dragon != nil {
			continue
		}
		bb := entityBBDims(o)
		if meets(o.X, o.Y, o.Z, bb[0], bb[1]) {
			mobs = append(mobs, o)
		}
	}
	s.entities.mu.RUnlock()
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || p.dimName() != e.Dim || p.dead {
			return true
		}
		if m := p.mode(); m == GameModeCreative || m == GameModeSpectator {
			return true
		}
		x, y, z, _, _, _ := p.Pos()
		if meets(x, y, z, 0.6, 1.8) {
			players = append(players, p)
		}
		return true
	})
	return
}

// dragonKnockBack is knockBack over a wing box inflated (4,2,4) and
// moved down 2: a 4-strong push from the body centre and a 5-point
// mob attack when the victim was not hurt by a mob in the last 2 ticks.
func (s *Server) dragonKnockBack(e *Entity, d *dimension, wing [6]float64) {
	dr := e.dragon
	box := [6]float64{wing[0] - 4, wing[1] - 2 - 2, wing[2] - 4, wing[3] + 4, wing[4] + 2 - 2, wing[5] + 4}
	b := dr.parts[partBody].box()
	cx, cz := (b[0]+b[3])/2, (b[2]+b[5])/2
	mobs, players := s.dragonVictims(e, box)
	now := s.gameTime()
	hit := func(id int32) bool {
		if isSittingPhase(dr.phase) {
			return false
		}
		last, ok := dr.lastMobHit[id]
		return !ok || last < now-2
	}
	for _, o := range mobs {
		dx, dz := o.X-cx, o.Z-cz
		dd := math.Max(dx*dx+dz*dz, 0.1)
		o.MotionX += dx / dd * 4
		o.MotionY += 0.2
		o.MotionZ += dz / dd * 4
		o.PosDirty = true
		if hit(o.ID) {
			dr.lastMobHit[o.ID] = now
			s.damageMob(o, 5, "mob_attack", nil, &damageInfo{typeName: "mob_attack", cause: e.ID, direct: e.ID})
		}
	}
	for _, p := range players {
		x, _, z, _, _, _ := p.Pos()
		dx, dz := x-cx, z-cz
		dd := math.Max(dx*dx+dz*dz, 0.1)
		s.broadcastEntityMotion(p.dimName(), p.entityID.Load(), dx/dd*4, 0.2, dz/dd*4)
		if hit(p.entityID.Load()) {
			dr.lastMobHit[p.entityID.Load()] = now
			s.dragonHurtPlayer(e, p, 5)
		}
	}
}

// dragonContactHurt is hurt over the head/neck box inflated by 1:
// a 10-point mob attack on each living victim.
func (s *Server) dragonContactHurt(e *Entity, d *dimension, part [6]float64) {
	box := [6]float64{part[0] - 1, part[1] - 1, part[2] - 1, part[3] + 1, part[4] + 1, part[5] + 1}
	mobs, players := s.dragonVictims(e, box)
	for _, o := range mobs {
		e.dragon.lastMobHit[o.ID] = s.gameTime()
		s.damageMob(o, 10, "mob_attack", nil, &damageInfo{typeName: "mob_attack", cause: e.ID, direct: e.ID})
	}
	for _, p := range players {
		e.dragon.lastMobHit[p.entityID.Load()] = s.gameTime()
		s.dragonHurtPlayer(e, p, 10)
	}
}

func (s *Server) dragonHurtPlayer(e *Entity, p *Session, amount float32) {
	s.damageWith(p, amount, translatable("death.attack.mob", playerChatComponent(p), translatable("entity.minecraft.ender_dragon")),
		damageSource{typeName: "mob_attack", cause: e.ID})
}

// dragonCheckWalls is checkWalls over one part box.
func (s *Server) dragonCheckWalls(e *Entity, d *dimension, box [6]float64) bool {
	x0, y0, z0 := int(math.Floor(box[0])), int(math.Floor(box[1])), int(math.Floor(box[2]))
	x1, y1, z1 := int(math.Floor(box[3])), int(math.Floor(box[4])), int(math.Floor(box[5]))
	grief := s.gameRuleBool("minecraft:mob_griefing")
	blocked, removed := false, false
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			for z := z0; z <= z1; z++ {
				p := world.Pos{X: x, Y: y, Z: z}
				b := d.w.GetBlock(p)
				if b == world.Air || worldgen.IsAir(b) {
					continue
				}
				name, _ := world.StateOwner(b)
				name = strings.TrimPrefix(name, "minecraft:")
				if blockTagHas("dragon_transparent", name) || name == "fire" || name == "soul_fire" {
					continue
				}
				if !grief || blockTagHas("dragon_immune", name) {
					blocked = true
					continue
				}
				s.setNaturalBlock(d, p, world.Air)
				removed = true
			}
		}
	}
	if removed {
		r := e.dragon.rng
		p := world.Pos{X: x0 + r.nextBoundedInt(x1-x0+1), Y: y0 + r.nextBoundedInt(y1-y0+1), Z: z0 + r.nextBoundedInt(z1-z0+1)}
		s.levelEventNear(d, 2008, p, 0)
	}
	return blocked
}

// dragonCheckCrystals is checkCrystals.
func (s *Server) dragonCheckCrystals(e *Entity) {
	dr := e.dragon
	if dr.nearestCrystal != 0 {
		if c, ok := s.entities.GetPtr(dr.nearestCrystal); !ok || c.Dying {
			dr.nearestCrystal = 0
		} else if e.TickCount%10 == 0 && e.Health < e.MaxHealth {
			e.Health = float32(math.Min(float64(e.Health+1), float64(e.MaxHealth)))
		}
	}
	if dr.rng.nextBoundedInt(10) == 0 {
		best, bestD := int32(0), math.MaxFloat64
		x0, x1 := e.X-8-32, e.X+8+32
		y0, y1 := e.Y-32, e.Y+8+32
		z0, z1 := e.Z-8-32, e.Z+8+32
		s.entities.mu.RLock()
		for _, c := range s.entities.byID {
			if c.crystal == nil || c.Dying || c.Dim != e.Dim {
				continue
			}
			if c.X+1 <= x0 || c.X-1 >= x1 || c.Y+2 <= y0 || c.Y >= y1 || c.Z+1 <= z0 || c.Z-1 >= z1 {
				continue
			}
			dd := (c.X-e.X)*(c.X-e.X) + (c.Y-e.Y)*(c.Y-e.Y) + (c.Z-e.Z)*(c.Z-e.Z)
			if dd < bestD {
				best, bestD = c.ID, dd
			}
		}
		s.entities.mu.RUnlock()
		dr.nearestCrystal = best
	}
}

// flyTarget is the current phase's getFlyTargetLocation.
func (dr *enderDragon) flyTarget() *dvec {
	switch dr.phase {
	case phaseHoldingPattern, phaseStrafePlayer, phaseLandingApproach, phaseLanding, phaseTakeoff,
		phaseChargingPlayer, phaseDying, phaseHover:
		return dr.ph[dr.phase].target
	}
	return nil
}

func (dr *enderDragon) flySpeed() float32 {
	switch dr.phase {
	case phaseLanding:
		return 1.5
	case phaseHover:
		return 1
	case phaseChargingPlayer, phaseDying:
		return 3
	}
	return 0.6
}

func (dr *enderDragon) turnSpeed(e *Entity) float32 {
	f := float32(math.Sqrt(e.MotionX*e.MotionX+e.MotionZ*e.MotionZ)) + 1
	f1 := float32(math.Min(float64(f), 40))
	if dr.phase == phaseLanding {
		return f1 / f
	}
	return 0.7 / f1 / f
}

// podiumTop is getHeightmapPos(type, EndPodiumFeature.getLocation(origin)).
func podiumTop(d *dimension, pred func(world.Block) bool) world.Pos {
	return world.Pos{X: 0, Y: heightmapTop(d, 0, 0, pred), Z: 0}
}

// dragonInitNodes builds the 24-node graph (findClosestNode's lazy init).
func (s *Server) dragonInitNodes(e *Entity, d *dimension) {
	dr := e.dragon
	if dr.nodesSet {
		return
	}
	for i := 0; i < 24; i++ {
		yAdd := 5
		var x, z int
		switch {
		case i < 12:
			a := float64(2 * (-3.1415927 + 0.2617994*float32(i)))
			x = int(math.Floor(float64(60 * mthCos(a))))
			z = int(math.Floor(float64(60 * mthSin(a))))
		case i < 20:
			k := i - 12
			a := float64(2 * (-3.1415927 + 0.3926991*float32(k)))
			x = int(math.Floor(float64(40 * mthCos(a))))
			z = int(math.Floor(float64(40 * mthSin(a))))
			yAdd += 10
		default:
			k := i - 20
			a := float64(2 * (-3.1415927 + 0.7853982*float32(k)))
			x = int(math.Floor(float64(20 * mthCos(a))))
			z = int(math.Floor(float64(20 * mthSin(a))))
		}
		y := max(73, heightmapTop(d, x, z, worldgen.MotionBlockingNoLeaves)+yAdd)
		dr.nodes[i] = &pfNode{x: int32(x), y: int32(y), z: int32(z), heapIdx: -1}
	}
	dr.nodeAdj = [24]int32{6146, 8197, 8202, 16404, 32808, 32848, 65696, 131392, 131712, 263424, 526848, 525313,
		1581057, 3166214, 2138120, 6373424, 4358208, 12910976, 9044480, 9706496, 15216640, 13688832, 11763712, 8257536}
	dr.nodesSet = true
}

func (s *Server) dragonAliveCrystals(e *Entity) (int, bool) {
	if f := s.dragonFightOf(e); f != nil {
		return f.aliveCrystals, true
	}
	return 0, false
}

// dragonClosestNodeTo is findClosestNode(x, y, z): the outer ring only
// counts while a fight has crystals standing.
func (s *Server) dragonClosestNodeTo(e *Entity, x, y, z float64) int {
	dr := e.dragon
	probe := &pfNode{x: int32(math.Floor(x)), y: int32(math.Floor(y)), z: int32(math.Floor(z))}
	start := 0
	if n, ok := s.dragonAliveCrystals(e); !ok || n == 0 {
		start = 12
	}
	best, idx := float32(10000), 0
	for i := start; i < 24; i++ {
		if dr.nodes[i] == nil {
			continue
		}
		if dd := dr.nodes[i].dist2(probe); dd < best {
			best, idx = dd, i
		}
	}
	return idx
}

func (s *Server) dragonClosestNode(e *Entity, d *dimension) int {
	s.dragonInitNodes(e, d)
	return s.dragonClosestNodeTo(e, e.X, e.Y, e.Z)
}

// nodeHeap is the pathfinder BinaryHeap (min on f).
type nodeHeap struct{ h []*pfNode }

func (q *nodeHeap) insert(n *pfNode) {
	q.h = append(q.h, n)
	n.heapIdx = len(q.h) - 1
	q.up(n.heapIdx)
}

func (q *nodeHeap) up(i int) {
	n := q.h[i]
	for i > 0 {
		pi := (i - 1) >> 1
		p := q.h[pi]
		if !(n.f < p.f) {
			break
		}
		q.h[i] = p
		p.heapIdx = i
		i = pi
	}
	q.h[i] = n
	n.heapIdx = i
}

func (q *nodeHeap) down(i int) {
	n := q.h[i]
	for {
		l := 1 + (i << 1)
		r := l + 1
		if l >= len(q.h) {
			break
		}
		ln := q.h[l]
		var rn *pfNode
		rf := float32(math.Inf(1))
		if r < len(q.h) {
			rn = q.h[r]
			rf = rn.f
		}
		if ln.f < rf {
			if ln.f >= n.f {
				break
			}
			q.h[i] = ln
			ln.heapIdx = i
			i = l
		} else {
			if rf >= n.f {
				break
			}
			q.h[i] = rn
			rn.heapIdx = i
			i = r
		}
	}
	q.h[i] = n
	n.heapIdx = i
}

func (q *nodeHeap) pop() *pfNode {
	n := q.h[0]
	last := len(q.h) - 1
	q.h[0] = q.h[last]
	q.h = q.h[:last]
	if len(q.h) > 0 {
		q.down(0)
	}
	n.heapIdx = -1
	return n
}

func (q *nodeHeap) change(n *pfNode, f float32) {
	old := n.f
	n.f = f
	if f < old {
		q.up(n.heapIdx)
	} else {
		q.down(n.heapIdx)
	}
}

// dragonFindPath is findPath(start, end, finalNode).
func (s *Server) dragonFindPath(e *Entity, start, end int, final *pfNode) *dragonPath {
	dr := e.dragon
	for _, n := range dr.nodes {
		n.closed, n.f, n.g, n.h, n.cameFrom, n.heapIdx = false, 0, 0, 0, nil, -1
	}
	sn, en := dr.nodes[start], dr.nodes[end]
	sn.g = 0
	sn.h = sn.distTo(en)
	sn.f = sn.h
	var open nodeHeap
	open.insert(sn)
	closest := sn
	k := 0
	if n, ok := s.dragonAliveCrystals(e); !ok || n == 0 {
		k = 12
	}
	for len(open.h) > 0 {
		n := open.pop()
		if n.x == en.x && n.y == en.y && n.z == en.z {
			if final != nil {
				final.cameFrom = en
				en = final
			}
			return reconstructDragonPath(en)
		}
		if n.distTo(en) < closest.distTo(en) {
			closest = n
		}
		n.closed = true
		idx := 0
		for i, m := range dr.nodes {
			if m == n {
				idx = i
				break
			}
		}
		for j := k; j < 24; j++ {
			if dr.nodeAdj[idx]&(1<<j) <= 0 {
				continue
			}
			m := dr.nodes[j]
			if m.closed {
				continue
			}
			g := n.g + n.distTo(m)
			if m.heapIdx < 0 || g < m.g {
				m.cameFrom = n
				m.g = g
				m.h = m.distTo(en)
				if m.heapIdx >= 0 {
					open.change(m, m.g+m.h)
				} else {
					m.f = m.g + m.h
					open.insert(m)
				}
			}
		}
	}
	if closest == sn {
		return nil
	}
	if final != nil {
		final.cameFrom = closest
		closest = final
	}
	return reconstructDragonPath(closest)
}

func reconstructDragonPath(end *pfNode) *dragonPath {
	var out []world.Pos
	for n := end; n != nil; n = n.cameFrom {
		out = append([]world.Pos{{X: int(n.x), Y: int(n.y), Z: int(n.z)}}, out...)
	}
	return &dragonPath{nodes: out}
}

// navigateTo is the phases' navigateToNextPathNode: the next node with
// a random 0-20 lift.
func (dr *enderDragon) navigateTo(st *dragonPhaseState) {
	if st.path == nil || st.path.done() {
		return
	}
	n := st.path.nextNode()
	st.path.advance()
	y := float64(float32(n.Y) + dr.rng.nextFloat()*20)
	st.target = &dvec{float64(n.X), y, float64(n.Z)}
}

// pickRingNode turns findClosestNode's j into the holding ring target:
// an 1-in-8 direction flip (+6), one step along it, wrapped into the
// outer ring when crystals stand (outerWhenZero keeps the outer ring
// for a crystal-less fight - HoldingPattern's >= 0 test), else the
// middle ring.
func (s *Server) pickRingNode(e *Entity, j int, clockwise *bool, outerWhenZero bool) int {
	k := j
	if e.dragon.rng.nextBoundedInt(8) == 0 {
		*clockwise = !*clockwise
		k += 6
	}
	if *clockwise {
		k++
	} else {
		k--
	}
	n, ok := s.dragonAliveCrystals(e)
	outer := ok && n > 0
	if outerWhenZero {
		outer = ok && n >= 0
	}
	if outer {
		k %= 12
		if k < 0 {
			k += 12
		}
	} else {
		k -= 12
		k &= 7
		k += 12
	}
	return k
}

// dragonNearestPlayer is ServerLevel.getNearestPlayer under forCombat
// targeting (survival/adventure, alive) within maxDist of (x, y, z)
// (0 = unlimited), with an optional extra test.
func (s *Server) dragonNearestPlayer(e *Entity, x, y, z, maxDist float64, test func(*Session) bool) *Session {
	var best *Session
	bestD := math.MaxFloat64
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || p.dimName() != e.Dim || p.dead {
			return true
		}
		if m := p.mode(); m == GameModeCreative || m == GameModeSpectator {
			return true
		}
		px, py, pz, _, _, _ := p.Pos()
		dd := (px-x)*(px-x) + (py-y)*(py-y) + (pz-z)*(pz-z)
		if maxDist > 0 && dd > maxDist*maxDist {
			return true
		}
		if test != nil && !test(p) {
			return true
		}
		if dd < bestD {
			best, bestD = p, dd
		}
		return true
	})
	return best
}

func farOrNear(st *dragonPhaseState, e *Entity) bool {
	d2 := 0.0
	if st.target != nil {
		d2 = st.target.dist2(dvec{e.X, e.Y, e.Z})
	}
	return d2 < 100 || d2 > 22500
}

// dragonPhaseTick is the current phase's doServerTick.
func (s *Server) dragonPhaseTick(e *Entity, d *dimension, f *dragonFight) {
	dr := e.dragon
	st := &dr.ph[dr.phase]
	pos := dvec{e.X, e.Y, e.Z}
	switch dr.phase {
	case phaseHoldingPattern:
		if farOrNear(st, e) {
			s.holdingFindNewTarget(e, d, f)
		}
	case phaseStrafePlayer:
		s.strafeTick(e, d)
	case phaseLandingApproach:
		if farOrNear(st, e) {
			if pathMissingOrDone(st.path) {
				i := s.dragonClosestNode(e, d)
				podium := podiumTop(d, worldgen.MotionBlockingNoLeaves)
				var j int
				if p := s.dragonNearestPlayer(e, float64(podium.X), float64(podium.Y), float64(podium.Z), 0, nil); p != nil {
					px, _, pz, _, _, _ := p.Pos()
					v := dvec{px, 0, pz}.normalize()
					j = s.dragonClosestNodeTo(e, -v.x*40, 105, -v.z*40)
				} else {
					j = s.dragonClosestNodeTo(e, 40, float64(podium.Y), 0)
				}
				final := &pfNode{x: int32(podium.X), y: int32(podium.Y), z: int32(podium.Z), heapIdx: -1}
				st.path = s.dragonFindPath(e, i, j, final)
				if st.path != nil {
					st.path.advance()
				}
			}
			dr.navigateTo(st)
			if pathDone(st.path) {
				s.dragonSetPhase(e, phaseLanding)
			}
		}
	case phaseLanding:
		if st.target == nil {
			p := podiumTop(d, worldgen.MotionBlockingNoLeaves)
			st.target = &dvec{float64(p.X) + 0.5, float64(p.Y), float64(p.Z) + 0.5}
		}
		if st.target.dist2(pos) < 1 {
			dr.flameCount = 0
			s.dragonSetPhase(e, phaseSittingScanning)
		}
	case phaseTakeoff:
		if dr.takeoffFirstTick || st.path == nil {
			dr.takeoffFirstTick = false
			i := s.dragonClosestNode(e, d)
			look := s.dragonHeadLook(e, d)
			j := s.dragonClosestNodeTo(e, -look.x*40, 105, -look.z*40)
			if n, ok := s.dragonAliveCrystals(e); ok && n > 0 {
				j %= 12
				if j < 0 {
					j += 12
				}
			} else {
				j -= 12
				j &= 7
				j += 12
			}
			st.path = s.dragonFindPath(e, i, j, nil)
			if st.path != nil {
				st.path.advance()
				dr.navigateTo(st)
			}
		} else {
			p := podiumTop(d, worldgen.MotionBlockingNoLeaves)
			cx, cy, cz := float64(p.X)+0.5-e.X, float64(p.Y)+0.5-e.Y, float64(p.Z)+0.5-e.Z
			if !(cx*cx+cy*cy+cz*cz < 100) {
				s.dragonSetPhase(e, phaseHoldingPattern)
			}
		}
	case phaseSittingScanning:
		s.scanningTick(e, d)
	case phaseSittingAttacking:
		dr.attackingTicks++
		if dr.attackingTicks >= 40 {
			s.dragonSetPhase(e, phaseSittingFlaming)
		}
	case phaseSittingFlaming:
		s.flamingTick(e, d)
	case phaseChargingPlayer:
		if st.target == nil {
			s.dragonSetPhase(e, phaseHoldingPattern)
			return
		}
		if dr.timeSinceCharge > 0 {
			t := dr.timeSinceCharge
			dr.timeSinceCharge++
			if t >= 10 {
				s.dragonSetPhase(e, phaseHoldingPattern)
				return
			}
		}
		if farOrNear(st, e) {
			dr.timeSinceCharge++
		}
	case phaseDying:
		dr.deathPhaseTime++
		if st.target == nil {
			p := podiumTop(d, worldgen.MotionBlocking)
			st.target = &dvec{float64(p.X) + 0.5, float64(p.Y), float64(p.Z) + 0.5}
		}
		if farOrNear(st, e) {
			e.Health = 0
		} else {
			e.Health = 1
		}
	case phaseHover:
		if st.target == nil {
			st.target = &dvec{e.X, e.Y, e.Z}
		}
	}
}

// holdingFindNewTarget is DragonHoldingPatternPhase.findNewTarget.
func (s *Server) holdingFindNewTarget(e *Entity, d *dimension, f *dragonFight) {
	dr := e.dragon
	st := &dr.ph[phaseHoldingPattern]
	if pathDone(st.path) {
		podium := podiumTop(d, worldgen.MotionBlockingNoLeaves)
		crystals := 0
		if f != nil {
			crystals = f.aliveCrystals
		}
		if dr.rng.nextBoundedInt(crystals+3) == 0 {
			s.dragonSetPhase(e, phaseLandingApproach)
			return
		}
		p := s.dragonNearestPlayer(e, float64(podium.X), float64(podium.Y), float64(podium.Z), 0, nil)
		d0 := 64.0
		if p != nil {
			px, py, pz, _, _, _ := p.Pos()
			cx, cy, cz := float64(podium.X)+0.5-px, float64(podium.Y)+0.5-py, float64(podium.Z)+0.5-pz
			d0 = (cx*cx + cy*cy + cz*cz) / 512
		}
		if p != nil && (dr.rng.nextBoundedInt(int(d0+2)) == 0 || dr.rng.nextBoundedInt(crystals+2) == 0) {
			s.dragonStrafe(e, d, p)
			return
		}
	}
	if pathMissingOrDone(st.path) {
		j := s.dragonClosestNode(e, d)
		k := s.pickRingNode(e, j, &dr.holdingClockwise, true)
		st.path = s.dragonFindPath(e, j, k, nil)
		if st.path != nil {
			st.path.advance()
		}
	}
	dr.navigateTo(st)
}

// dragonStrafe is strafePlayer + DragonStrafePlayerPhase.setTarget.
func (s *Server) dragonStrafe(e *Entity, d *dimension, p *Session) {
	dr := e.dragon
	s.dragonSetPhase(e, phaseStrafePlayer)
	st := &dr.ph[phaseStrafePlayer]
	dr.strafeTarget = p.entityID.Load()
	px, py, pz, _, _, _ := p.Pos()
	i := s.dragonClosestNode(e, d)
	j := s.dragonClosestNodeTo(e, px, py, pz)
	bx, bz := int(math.Floor(px)), int(math.Floor(pz))
	dx, dz := float64(bx)-e.X, float64(bz)-e.Z
	d3 := math.Min(0.4000000059604645+math.Sqrt(dx*dx+dz*dz)/80-1, 10)
	final := &pfNode{x: int32(bx), y: int32(math.Floor(py + d3)), z: int32(bz), heapIdx: -1}
	st.path = s.dragonFindPath(e, i, j, final)
	if st.path != nil {
		st.path.advance()
		dr.navigateTo(st)
	}
}

// strafeTick is DragonStrafePlayerPhase.doServerTick.
func (s *Server) strafeTick(e *Entity, d *dimension) {
	dr := e.dragon
	st := &dr.ph[phaseStrafePlayer]
	tgt := s.sessionByEntityID(dr.strafeTarget)
	if dr.strafeTarget == 0 || tgt == nil || tgt.dead || tgt.dimName() != e.Dim {
		s.dragonSetPhase(e, phaseHoldingPattern)
		return
	}
	tx, ty, tz, _, _, _ := tgt.Pos()
	if pathDone(st.path) {
		dx, dz := tx-e.X, tz-e.Z
		d5 := math.Min(0.4000000059604645+math.Sqrt(dx*dx+dz*dz)/80-1, 10)
		st.target = &dvec{tx, ty + d5, tz}
	}
	if farOrNear(st, e) {
		if pathMissingOrDone(st.path) {
			j := s.dragonClosestNode(e, d)
			k := s.pickRingNode(e, j, &dr.strafeClockwise, false)
			st.path = s.dragonFindPath(e, j, k, nil)
			if st.path != nil {
				st.path.advance()
			}
		}
		dr.navigateTo(st)
	}
	dd := (tx-e.X)*(tx-e.X) + (ty-e.Y)*(ty-e.Y) + (tz-e.Z)*(tz-e.Z)
	if dd >= 4096 {
		if dr.fireballCharge > 0 {
			dr.fireballCharge--
		}
		return
	}
	if _, _, _, _, _, _, _, blocked := s.clipSolidSegment(d, e.X, e.Y+dragonEyeHeight, e.Z, tx, ty+1.62, tz); blocked {
		if dr.fireballCharge > 0 {
			dr.fireballCharge--
		}
		return
	}
	dr.fireballCharge++
	v6 := dvec{tx - e.X, 0, tz - e.Z}.normalize()
	yr := float64(e.Yaw) * 0.017453292
	v7 := dvec{float64(mthSin(yr)), 0, float64(-mthCos(yr))}.normalize()
	f9 := float32(math.Acos(float64(float32(v7.dot(v6))))*57.2957763671875) + 0.5
	if dr.fireballCharge >= 5 && f9 >= 0 && f9 < 10 {
		view := dragonViewVector(e.Pitch, e.Yaw)
		hp := dr.parts[partHead]
		hx, hy, hz := hp.x-view.x, hp.y+hp.h*0.5+0.5, hp.z-view.z
		dir := dvec{tx - hx, ty + 0.9 - hy, tz - hz}.normalize()
		if !e.Silent {
			s.levelEventNear(d, 1017, floorPos(e), 0)
		}
		s.spawnDragonFireball(e, hx, hy, hz, dir)
		dr.fireballCharge = 0
		if st.path != nil {
			for !st.path.done() {
				st.path.advance()
			}
		}
		s.dragonSetPhase(e, phaseHoldingPattern)
	}
}

// sndEnderDragonHurt is entity.ender_dragon.hurt's registry id (the
// wire carried 590 on a measured part hit).
const sndEnderDragonHurt = 590

// dragonEyeHeight is the dragon's eye height (EntityDimensions 16x8,
// default eye 0.85 of the height).
const dragonEyeHeight = 8 * 0.85

// dragonViewVector is Entity.calculateViewVector(xRot, yRot).
func dragonViewVector(pitch, yaw float32) dvec {
	f := float64(pitch) * 0.017453292
	g := float64(-yaw) * 0.017453292
	h, i := mthCos(g), mthSin(g)
	j, k := mthCos(f), mthSin(f)
	return dvec{float64(i * j), float64(-k), float64(h * j)}
}

// dragonHeadLook is getHeadLookVector(1).
func (s *Server) dragonHeadLook(e *Entity, d *dimension) dvec {
	dr := e.dragon
	switch {
	case dr.phase == phaseLanding || dr.phase == phaseTakeoff:
		p := podiumTop(d, worldgen.MotionBlockingNoLeaves)
		cx, cy, cz := float64(p.X)+0.5-e.X, float64(p.Y)+0.5-e.Y, float64(p.Z)+0.5-e.Z
		f6 := float32(math.Max(math.Sqrt(cx*cx+cy*cy+cz*cz)/4, 1))
		f7 := 6 / f6
		return dragonViewVector(-f7*1.5*5, e.Yaw)
	case isSittingPhase(dr.phase):
		return dragonViewVector(-45, e.Yaw)
	}
	return dragonViewVector(e.Pitch, e.Yaw)
}

// spawnDragonFireball is new DragonFireball(level, dragon, dir) snapped
// to the head: accelerationPower 0.1 along the normalized direction,
// owned by the dragon.
func (s *Server) spawnDragonFireball(e *Entity, x, y, z float64, dir dvec) {
	mx, my, mz := dir.x*0.1, dir.y*0.1, dir.z*0.1
	fb := s.entities.Spawn(&Entity{
		Type: mustEntityType("dragon_fireball"),
		Dim:  e.Dim,
		X:    x, Y: y, Z: z,
		MotionX: mx, MotionY: my, MotionZ: mz,
		VX: int16(mx * 8000), VY: int16(my * 8000), VZ: int16(mz * 8000),
		AccelPower:   0.1,
		ProjOwner:    e.UUID,
		HasProjOwner: true,
		arrowShooter: e.ID,
	})
	s.broadcastEntitySpawn(nil, *fb)
}

// scanningTick is DragonSittingScanningPhase.doServerTick.
func (s *Server) scanningTick(e *Entity, d *dimension) {
	dr := e.dragon
	dr.scanningTime++
	p := s.dragonNearestPlayer(e, e.X, e.Y, e.Z, 20, func(p *Session) bool {
		_, py, _, _, _, _ := p.Pos()
		return math.Abs(py-e.Y) <= 10
	})
	if p != nil {
		if dr.scanningTime > 25 {
			s.dragonSetPhase(e, phaseSittingAttacking)
			return
		}
		px, _, pz, _, _, _ := p.Pos()
		v := dvec{px - e.X, 0, pz - e.Z}.normalize()
		yr := float64(e.Yaw) * 0.017453292
		v1 := dvec{float64(mthSin(yr)), 0, float64(-mthCos(yr))}.normalize()
		f1 := float32(math.Acos(float64(float32(v1.dot(v))))*57.2957763671875) + 0.5
		if f1 < 0 || f1 > 10 {
			hp := dr.parts[partHead]
			d0, d1 := px-hp.x, pz-hp.z
			d2 := math.Max(-100, math.Min(100, wrapDeg64(180-math.Atan2(d0, d1)*57.2957763671875-float64(e.Yaw))))
			dr.yRotA *= 0.8
			f2 := float32(math.Sqrt(d0*d0+d1*d1)) + 1
			f3 := f2
			if f2 > 40 {
				f2 = 40
			}
			dr.yRotA += float32(d2) * (0.7 / f2 / f3)
			e.Yaw += dr.yRotA
		}
		return
	}
	if dr.scanningTime >= 100 {
		p := s.dragonNearestPlayer(e, e.X, e.Y, e.Z, 150, nil)
		s.dragonSetPhase(e, phaseTakeoff)
		if p != nil {
			s.dragonSetPhase(e, phaseChargingPlayer)
			px, py, pz, _, _, _ := p.Pos()
			dr.ph[phaseChargingPlayer].target = &dvec{px, py, pz}
		}
	}
}

// flamingTick is DragonSittingFlamingPhase.doServerTick: the breath
// cloud on tick 10, the phase change on tick 200.
func (s *Server) flamingTick(e *Entity, d *dimension) {
	dr := e.dragon
	dr.flameTicks++
	if dr.flameTicks >= 200 {
		if dr.flameCount >= 4 {
			s.dragonSetPhase(e, phaseTakeoff)
		} else {
			s.dragonSetPhase(e, phaseSittingScanning)
		}
		return
	}
	if dr.flameTicks != 10 {
		return
	}
	hp := dr.parts[partHead]
	v := dvec{hp.x - e.X, 0, hp.z - e.Z}.normalize()
	x, z := hp.x+v.x*5/2, hp.z+v.z*5/2
	d8 := hp.y + hp.h*0.5
	d10 := d8
	for d.w.GetBlock(world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(d10)), Z: int(math.Floor(z))}) == world.Air {
		d10--
		if d10 < 0 {
			d10 = d8
			break
		}
	}
	d10 = math.Floor(d10) + 1
	effects := map[int32]mobEffectInst{}
	if id, ok := MobEffectID("instant_damage"); ok {
		effects[id] = mobEffectInst{amplifier: 0, expiry: -1}
	}
	cloud := s.entities.Spawn(&Entity{
		Type: mustEntityType("area_effect_cloud"),
		Dim:  e.Dim,
		X:    x, Y: d10, Z: z,
		CloudWait:     20,
		CloudDuration: 200,
		CloudRadius:   5,
		CloudVictims:  map[int32]int32{},
		Effects:       effects,
		CloudOwnerID:  e.ID,
	})
	s.broadcastEntitySpawn(nil, *cloud)
	if meta := encodeCloudMeta(cloud); len(meta) > 0 {
		s.broadcastEntityMetadata(cloud.ID, meta)
	}
	dr.flameCloud = cloud.ID
}

// dragonCrystalDestroyed is EnderDragon.onCrystalDestroyed: the
// healing crystal's loss hits the head for 10 (explosion, credited to
// the attacker or the nearest player within 64), then the phase hears
// it (HoldingPattern strafes the player).
func (s *Server) dragonCrystalDestroyed(e *Entity, c *Entity, attacker *Session) {
	dr := e.dragon
	p := attacker
	if p == nil {
		p = s.dragonNearestPlayer(e, math.Floor(c.X), math.Floor(c.Y), math.Floor(c.Z), 64, nil)
	}
	if c.ID == dr.nearestCrystal {
		s.dragonHurt(e, partHead, 10, "explosion", p, &damageInfo{typeName: "explosion", cause: playerEID(p)})
	}
	if dr.phase == phaseHoldingPattern && p != nil {
		if m := p.mode(); m != GameModeCreative && m != GameModeSpectator {
			if d := s.dims[e.Dim]; d != nil {
				s.dragonStrafe(e, d, p)
			}
		}
	}
}

func playerEID(p *Session) int32 {
	if p == nil {
		return 0
	}
	return p.entityID.Load()
}

// dragonHurtServer routes damageMob's hits on the dragon (its whole
// box stands for the body part).
func (s *Server) dragonHurtServer(e *Entity, amount float32, typeName string, killer *Session, info *damageInfo) (float32, bool, bool) {
	// Callers may hold a Manager.Get copy; the dragon's state lives on
	// the stored entity.
	if ep, ok := s.entities.GetPtr(e.ID); ok {
		e = ep
	}
	ok, dealt, died := s.dragonHurt(e, partBody, amount, typeName, killer, info)
	_ = ok
	return dealt, false, died
}

// dragonHurt is EnderDragon.hurt(level, part, source, amount).
func (s *Server) dragonHurt(e *Entity, part int, amount float32, typeName string, killer *Session, info *damageInfo) (bool, float32, bool) {
	dr := e.dragon
	if dr.phase == phaseDying || e.Health <= 0 { // LivingEntity.hurtServer refuses the dead
		return false, 0, false
	}
	if isSittingPhase(dr.phase) && dr.phase != phaseHover && info != nil && info.direct != 0 {
		if pe, ok := s.entities.GetPtr(info.direct); ok {
			if n, _ := EntityTypeName(pe.Type); n == "arrow" || n == "spectral_arrow" || n == "trident" || n == "wind_charge" {
				pe.FireTicks = max(pe.FireTicks, 20)
				amount = 0
			}
		}
	}
	if part != partHead {
		amount = amount/4 + float32(math.Min(float64(amount), 1))
	}
	if amount < 0.01 {
		return false, 0, false
	}
	byPlayer := killer != nil
	if !byPlayer && info != nil && info.cause != 0 && s.sessionByEntityID(info.cause) != nil {
		byPlayer = true
		killer = s.sessionByEntityID(info.cause)
	}
	if !byPlayer && !isExplosionDamage[typeName] {
		return true, 0, false
	}
	h := e.Health
	dealt, graced, died := s.mobHurtCore(e, amount, typeName, killer, info)
	if !died && !graced && dealt > 0 && !e.Silent {
		// playHurtSound: entity.ender_dragon.hurt, HOSTILE, volume 5,
		// the voice pitch - positional at the dragon.
		r := dr.rng
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, sndEnderDragonHurt, 5, 5, (r.nextFloat()-r.nextFloat())*0.2+1, nil)
	}
	if isSittingPhase(dr.phase) {
		dr.sittingDamage += h - e.Health
		if dr.sittingDamage > 0.25*e.MaxHealth {
			dr.sittingDamage = 0
			s.dragonSetPhase(e, phaseTakeoff)
		}
	}
	return true, dealt, died
}

// dragonDie is LivingEntity.die for the dragon: the kill credit, the
// ENTITY_DIE event, entity event 3, pose DYING - with
// handleKillingBlow leaving 1 health and switching to DYING unless the
// dragon sits (then it dies in place).
func (s *Server) dragonDie(e *Entity, killer *Session) {
	if current, ok := s.entities.GetPtr(e.ID); ok {
		e = current
	}
	if e.LastHurtByPlayerTicks > 0 {
		if src := s.sessionByUUID(e.LastHurtByPlayerUUID); src != nil {
			src.addStat(statKilled, e.Type, 1)
			s.awardKillScore(src, sprintUUID(e.UUID), false)
		}
	}
	if !isSittingPhase(e.dragon.phase) {
		e.Health = 1
		s.dragonSetPhase(e, phaseDying)
	}
	s.mobDeathSound(e)
	if d := s.dimensionByName(e.Dim); d != nil {
		s.emitGameEvent(d, "entity_die", e.X, e.Y, e.Z, nil, "")
	}
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 3)
	e.Pose = 7 // DYING
	e.MetaDirty = true
}

// dragonTickDeath is EnderDragon.tickDeath; it reports a removal.
func (s *Server) dragonTickDeath(e *Entity) bool {
	dr := e.dragon
	f := s.dragonFightOf(e)
	if f != nil {
		s.fightUpdateDragon(f, e)
	}
	dr.deathTime++
	total := int32(500)
	if f != nil && !f.previouslyKilled {
		total = 12000
	}
	drops := s.gameRuleBool("minecraft:mob_drops")
	if dr.deathTime > 150 && dr.deathTime%5 == 0 && drops {
		s.awardExperience(e.Dim, e.X, e.Y, e.Z, int32(math.Floor(float64(float32(total)*0.08))))
	}
	if dr.deathTime == 1 && !e.Silent {
		s.broadcastGlobalLevelEvent(1028, floorPos(e), 0)
	}
	e.Y += 0.1
	for i := range dr.parts {
		dr.parts[i].y += 0.1
	}
	e.PosDirty = true
	if dr.deathTime >= 200 {
		if drops {
			s.awardExperience(e.Dim, e.X, e.Y, e.Z, int32(math.Floor(float64(float32(total)*0.2))))
		}
		if f != nil {
			s.fightSetDragonKilled(f, e)
		}
		s.dragonRemove(e)
		if d := s.dimensionByName(e.Dim); d != nil {
			s.emitGameEvent(d, "entity_die", e.X, e.Y, e.Z, nil, "")
		}
		return true
	}
	return false
}

// awardExperience is ExperienceOrb.award: the amount split into the
// largest orb values first.
func (s *Server) awardExperience(dim string, x, y, z float64, amount int32) {
	for amount > 0 {
		v := orbValueFor(amount)
		amount -= v
		s.spawnXPOrb(dim, x, y, z, v)
	}
}

// orbValueFor is ExperienceOrb.getExperienceValue.
func orbValueFor(v int32) int32 {
	for _, t := range []int32{2477, 1237, 617, 307, 149, 73, 37, 17, 7, 3} {
		if v >= t {
			return t
		}
	}
	return 1
}

// dragonRemove takes the dragon out of the level.
func (s *Server) dragonRemove(e *Entity) {
	e.Dying = true
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
}

// dragonDiscard is Entity.discard.
func (s *Server) dragonDiscard(e *Entity) { s.dragonRemove(e) }

// dragonKill is EnderDragon.kill: removed at once, the fight told.
func (s *Server) dragonKill(e *Entity) {
	s.dragonRemove(e)
	if d := s.dimensionByName(e.Dim); d != nil {
		s.emitGameEvent(d, "entity_die", e.X, e.Y, e.Z, nil, "")
	}
	if f := s.dragonFightOf(e); f != nil {
		s.fightUpdateDragon(f, e)
		s.fightSetDragonKilled(f, e)
	}
}

// dragonPartOf resolves a part entity id (dragon id + 1..8).
func (s *Server) dragonPartOf(id int32) (*Entity, int) {
	var out *Entity
	part := -1
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.dragon != nil && id > e.ID && id <= e.ID+8 {
			out, part = e, int(id-e.ID-1)
			break
		}
	}
	s.entities.mu.RUnlock()
	return out, part
}

// attackDragonPart is Player.attack on an EnderDragonPart: the hit goes
// to the dragon through the part.
func (s *Server) attackDragonPart(sess *Session, e *Entity, part int) {
	p := e.dragon.parts[part]
	if !sess.withinAttackRange(p.x, p.y, p.z, [2]float64{p.w, p.h}) {
		return
	}
	plan := s.planAttack(sess)
	s.preAttackSound(sess, plan)
	sess.attackTicker = 0
	ok, _, died := s.dragonHurt(e, part, plan.damage, "player_attack", sess, &damageInfo{
		typeName: "player_attack", cause: sess.entityID.Load(), direct: sess.entityID.Load()})
	if !ok {
		s.broadcastAttackSound(sess, sndAttackNodamage)
		return
	}
	if died {
		s.onPlayerKilledMob(sess, *e, false)
	}
	plan.sweep = false
	s.attackAftermath(sess, plan, e.ID, 0, [2]float64{p.w, p.h})
}
