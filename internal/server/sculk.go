package server

// Sculk sensors — the whole vibration pipeline vanilla models as
// VibrationSystem: a gameEvent emitted at a source position, picked
// up by listener block entities in a radius-8 box, queued through
// a single-slot selector, and traveled to the sensor over distance
// ticks before it activates the block. Vanilla measures live in
// SculkSensorBlock{,Entity}, CalibratedSculkSensorBlockEntity,
// VibrationSystem$Listener/Data/Ticker/Selector/User (26.2 jar).

import (
	"math"
	"strconv"
	"strings"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// gameEventFrequency is VibrationSystem.VIBRATION_FREQUENCY_FOR_EVENT
// — the loudness class a game event falls into. Frequency-0 entries
// (resonate_* and sculk_sensor_tendrils_clicking itself) are not
// mappable to a vibration and never reach a listener.
var gameEventFrequency = map[string]int{
	"step": 1, "swim": 1, "flap": 1,
	"projectile_land": 2, "hit_ground": 2, "splash": 2, "bounce": 2,
	"item_interact_finish": 3, "projectile_shoot": 3, "instrument_play": 3,
	"entity_action": 4, "elytra_glide": 4, "unequip": 4,
	"entity_dismount": 5, "equip": 5,
	"entity_interact": 6, "shear": 6, "entity_mount": 6,
	"entity_damage": 7,
	"drink":         8, "eat": 8,
	"container_close": 9, "block_close": 9, "block_deactivate": 9, "block_detach": 9,
	"container_open": 10, "block_open": 10, "block_activate": 10, "block_attach": 10,
	"prime_fuse": 10, "note_block_play": 10,
	"block_change":  11,
	"block_destroy": 12, "fluid_pickup": 12,
	"block_place": 13, "fluid_place": 13,
	"entity_place": 14, "lightning_strike": 14, "teleport": 14,
	"entity_die": 15, "explode": 15,
}

func init() {
	// The tail of vanilla's frequency-map init loops
	// map.put(getResonanceEventByFrequency(i), i) over i 1..15 —
	// every resonate_N event carries frequency N, so an amethyst
	// relay preserves the loudness class across the hop.
	for i := 1; i <= 15; i++ {
		gameEventFrequency["resonate_"+strconv.Itoa(i)] = i
	}
}

// sneakDampenedEvents is the ignore_vibrations_sneaking game_event
// tag — a sneaking source suppresses exactly these events.
var sneakDampenedEvents = map[string]bool{
	"hit_ground": true, "projectile_shoot": true, "step": true,
	"swim": true, "item_interact_start": true, "item_interact_finish": true,
}

// VibrationUser.getListenerRadius: the plain sensor hears events
// within 8 blocks; CalibratedSculkSensorBlockEntity's user reaches
// 16 — the radius gates the notification distSqr and divides the
// power falloff.
const (
	sculkListenRadius      = 8.0
	calibratedListenRadius = 16.0
)

// sculkRadiusFor reports the sensor's listener radius.
func sculkRadiusFor(short string) float64 {
	if short == "calibrated_sculk_sensor" {
		return calibratedListenRadius
	}
	return sculkListenRadius
}

// vibration is VibrationInfo: the event the listener heard and its
// frequency class, the source position, the listener-to-source
// distance that feeds the travel delay and power falloff, and the
// tick it was selected on.
type vibration struct {
	event      string
	freq       int
	x, y, z    float64
	dist       float64
	selectTick int64
}

// sculkSensor is a sensor block entity's vibration state —
// VibrationSystem$Data (the traveling vibration + the one selection
// slot) plus the saved last_vibration_frequency.
type sculkSensor struct {
	sel      *vibration
	cur      *vibration
	travel   int
	lastFreq int
}

func newSculkSensor() *sculkSensor { return &sculkSensor{lastFreq: -1} }

// isSculkSensor covers both sensor blocks; the calibrated variant's
// differences all hang off its facing property.
func isSculkSensor(short string) bool {
	return short == "sculk_sensor" || short == "calibrated_sculk_sensor"
}

// sculkSensorShort strips the namespace only when the block is a
// sensor block entity candidate.
func sculkSensorShort(blk world.Block) (string, bool) {
	owner, ok := world.StateOwner(blk)
	if !ok {
		return "", false
	}
	short := strings.TrimPrefix(owner, "minecraft:")
	return short, isSculkSensor(short)
}

// emitGameEvent is ServerLevel.gameEvent's dispatch through the
// GameEventDispatcher: every listener block entity in the event's
// notificationRadius box gets handleGameEvent. src is the causing
// player session (nil for command- and block-sourced events) and
// affected is the placed/changed block's short name for the
// dampens_vibrations context check ("" when the event has no state).
func (s *Server) emitGameEvent(d *dimension, event string, x, y, z float64, src *Session, affected string) {
	freq := gameEventFrequency[event]
	if freq == 0 {
		return
	}
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	now := s.TickCount()
	rad := int(calibratedListenRadius)
	for px := bx - rad; px <= bx+rad; px++ {
		for py := by - rad; py <= by+rad; py++ {
			for pz := bz - rad; pz <= bz+rad; pz++ {
				p := world.Pos{X: px, Y: py, Z: pz}
				blk := d.w.GetBlock(p)
				short, ok := sculkSensorShort(blk)
				if !ok {
					continue
				}
				lx, ly, lz := float64(px)+0.5, float64(py)+0.5, float64(pz)+0.5
				dx, dy, dz := x-lx, y-ly, z-lz
				dist2 := dx*dx + dy*dy + dz*dz
				// The dispatcher keeps only listeners whose floored
				// BlockPos distSqr is at most listenerRadius².
				lr := sculkRadiusFor(short)
				bdx, bdy, bdz := bx-px, by-py, bz-pz
				if float64(bdx*bdx+bdy*bdy+bdz*bdz) > lr*lr {
					continue
				}
				sen := d.sculk[p]
				// handleGameEvent's first gate: a listener busy
				// traveling a vibration hears nothing.
				if sen != nil && sen.cur != nil {
					continue
				}
				// VibrationSystem$User.isValidVibration: a sneaking
				// source suppresses the tagged events — and earns the
				// avoid_vibration criterion when a listener heard it.
				if src != nil && src.sneaking && sneakDampenedEvents[event] {
					s.avoidVibration(src)
					continue
				}
				// The context's affected state dampens the event when
				// it is wool or a wool carpet.
				if affected != "" && blockInTag("dampens_vibrations", affected) {
					continue
				}
				// SculkSensorBlockEntity$VibrationUser.canReceiveVibration:
				// the sensor never hears its own placement or break,
				// and only an INACTIVE sensor can receive.
				if (event == "block_destroy" || event == "block_place") &&
					p.X == bx && p.Y == by && p.Z == bz {
					continue
				}
				prop := rsPropsOf(blk)
				if prop("sculk_sensor_phase") != "inactive" {
					continue
				}
				if short == "calibrated_sculk_sensor" {
					// CalibratedSculkSensorBlockEntity's user gates on
					// getBackSignal: the redstone signal arriving on
					// the side opposite FACING must be 0 or exactly
					// the event's frequency.
					if dir, ok := rsDirOfName[prop("facing")]; ok {
						if sig := rsSignalFrom(d, p, rsOpposite(dir)); sig != 0 && sig != freq {
							continue
						}
					}
				}
				if s.vibrationOccluded(d, x, y, z, lx, ly, lz) {
					continue
				}
				if sen == nil {
					sen = newSculkSensor()
					d.sculk[p] = sen
				}
				// Listener.scheduleVibration →
				// VibrationSelector.addCandidate: the single slot is
				// replaced when empty, or on the same tick when the
				// new event is closer — or equally close but louder.
				cand := &vibration{event: event, freq: freq, x: x, y: y, z: z, dist: math.Sqrt(dist2), selectTick: now}
				if sen.sel == nil ||
					(cand.selectTick == sen.sel.selectTick &&
						(cand.dist < sen.sel.dist ||
							(cand.dist == sen.sel.dist &&
								freq > gameEventFrequency[sen.sel.event]))) {
					sen.sel = cand
				}
			}
		}
	}
}

// emitContainerOpen is ContainerOpenersCounter.onOpen's emission:
// CONTAINER_OPEN at the container block. mc-go emits per open —
// vanilla counts openers and fires only on the 0→1 transition.
func (s *Server) emitContainerOpen(sess *Session, pos world.Pos) {
	s.emitGameEvent(sess.dimension(), "container_open",
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		sess, "")
}

// avoidVibration grants the avoid_vibration criterion — fired when a
// sneaking player's dampened emission reaches a listener.
func (s *Server) avoidVibration(sess *Session) {
	for _, meta := range gameAdvs {
		for _, vc := range meta.vibrates {
			if vc.impossible {
				continue
			}
			sess.grantGameCriterion(meta, vc.crit)
		}
	}
}

// vibrationOccluded is Listener.isOccluded: both endpoints snap to
// their block centre (floor+0.5), then each of the six axis-aligned
// rays — the start nudged by 1e-5 along that direction — must reach
// the other end without crossing an occludes_vibration_signals cell
// (the 16 wool colours; carpets do not occlude).
func (s *Server) vibrationOccluded(d *dimension, x1, y1, z1, x2, y2, z2 float64) bool {
	x1 = math.Floor(x1) + 0.5
	y1 = math.Floor(y1) + 0.5
	z1 = math.Floor(z1) + 0.5
	x2 = math.Floor(x2) + 0.5
	y2 = math.Floor(y2) + 0.5
	z2 = math.Floor(z2) + 0.5
	for _, dir := range rsOff {
		if s.segmentWool(d,
			x1+float64(dir.X)*1e-5, y1+float64(dir.Y)*1e-5, z1+float64(dir.Z)*1e-5,
			x2, y2, z2) {
			return true
		}
	}
	return false
}

// segmentWool reports whether the straight line from (x1,y1,z1) to
// (x2,y2,z2) crosses a wool cell — ClipBlockStateContext's per-cell
// isOccluder march, approximated with a fine step.
func (s *Server) segmentWool(d *dimension, x1, y1, z1, x2, y2, z2 float64) bool {
	dx, dy, dz := x2-x1, y2-y1, z2-z1
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length == 0 {
		return false
	}
	dx, dy, dz = dx/length, dy/length, dz/length
	last := world.Pos{X: math.MaxInt32, Y: math.MaxInt32, Z: math.MaxInt32}
	for t := 0.0; t <= length; t += 0.1 {
		p := world.Pos{
			X: int(math.Floor(x1 + dx*t)),
			Y: int(math.Floor(y1 + dy*t)),
			Z: int(math.Floor(z1 + dz*t)),
		}
		if p != last {
			last = p
			if name, ok := world.StateOwner(d.w.GetBlock(p)); ok &&
				blockInTag("occludes_vibration_signals", strings.TrimPrefix(name, "minecraft:")) {
				return true
			}
		}
	}
	return false
}

// vibrationTickStep is VibrationSystem$Ticker.tick for every sensor
// in the loaded dimensions: idle sensors pick the tick-ripened
// candidate in their selector slot; a traveling vibration counts
// down and is received when its delay ends.
func (s *Server) vibrationTickStep() {
	now := s.TickCount()
	for _, d := range s.dims {
		for p, sen := range d.sculk {
			if sen == nil {
				continue
			}
			if sen.cur == nil {
				// getChosenCandidate: the slot only ripens when its
				// select tick is strictly before now.
				if sen.sel != nil && sen.sel.selectTick < now {
					sen.cur, sen.sel = sen.sel, nil
					sen.travel = int(math.Floor(sen.cur.dist))
					s.vibrationParticle(d, p, sen.cur)
				}
			}
			if sen.cur == nil {
				continue
			}
			sen.travel--
			if sen.travel > 0 {
				continue
			}
			v := sen.cur
			sen.cur = nil
			s.sculkReceive(d, p, sen, v)
		}
	}
}

// vibrationParticle is the Ticker's vibration burst: one particle at
// the event's source aimed at the sensor's BlockPositionSource with
// the measured travel ticks — level_particles id 55 + the
// VibrationParticleOption tail.
func (s *Server) vibrationParticle(d *dimension, pos world.Pos, v *vibration) {
	var b protocol.Buffer
	b.Byte(0) // overrideLimiter
	b.Byte(0) // alwaysShow
	b.Double(v.x)
	b.Double(v.y)
	b.Double(v.z)
	b.Float(0) // offsets
	b.Float(0)
	b.Float(0)
	b.Float(0)   // speed
	b.Int(1)     // count
	b.VarInt(55) // minecraft:vibration
	b.VarInt(0)  // position_source_type: block
	b.Position(int32(pos.X), int32(pos.Y), int32(pos.Z))
	b.VarInt(int32(math.Floor(v.dist))) // arrivalTicks
	body := b.Bytes()
	s.sessions.Range(func(_, val any) bool {
		other := val.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != d.name {
			return true
		}
		_ = other.write(other.ids().PlayLevelParticles, body)
		return true
	})
}

// sculkReceive is VibrationUser.onReceiveVibration for a sensor:
// when the block can still activate it latches the frequency,
// publishes the distance-derived power, and flips the phase.
func (s *Server) sculkReceive(d *dimension, pos world.Pos, sen *sculkSensor, v *vibration) {
	blk := d.w.GetBlock(pos)
	if _, ok := sculkSensorShort(blk); !ok {
		return
	}
	prop := rsPropsOf(blk)
	if prop("sculk_sensor_phase") != "inactive" {
		return
	}
	sen.lastFreq = v.freq
	// receiveVibration passes distanceBetweenInBlocks — the sqrt of
	// the floored-BlockPos distSqr — and the user's listener radius
	// divides the falloff (8 plain, 16 calibrated).
	short, _ := sculkSensorShort(blk)
	ex, ey, ez := int(math.Floor(v.x))-pos.X, int(math.Floor(v.y))-pos.Y, int(math.Floor(v.z))-pos.Z
	blockDist := math.Sqrt(float64(ex*ex + ey*ey + ez*ez))
	power := 15 - int(math.Floor(blockDist*15.0/sculkRadiusFor(short)))
	if power < 1 {
		power = 1
	}
	s.sculkActivate(d, pos, blk, power, v.freq)
}

// sculkActivate is SculkSensorBlock.activate: write the ACTIVE state
// carrying the measured power, schedule the deactivate tick in
// getActiveTicks (30 plain, 10 calibrated), fan neighbours out,
// resonate the frequency through any adjacent amethyst, and play
// the click.
func (s *Server) sculkActivate(d *dimension, pos world.Pos, blk world.Block, power, freq int) {
	nb, _ := world.WithProperty(blk, "sculk_sensor_phase", "active")
	nb, _ = world.WithProperty(nb, "power", strconv.Itoa(power))
	d.w.SetBlock(pos, nb)
	s.broadcastBlockState(d, pos, nb)
	activeTicks := int64(30)
	if short, _ := sculkSensorShort(blk); short == "calibrated_sculk_sensor" {
		activeTicks = 10
	}
	s.scheduleBlockTick(d, pos, activeTicks)
	s.sculkUpdateNeighbours(d, pos)
	prop := rsPropsOf(nb)
	if prop("waterlogged") != "true" {
		pitch := float32(0.8 + s.rng.nextDouble()*0.2)
		s.playSoundAt(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
			1420 /* block.sculk_sensor.clicking */, 1.0, pitch)
	}
	s.tryResonateVibration(d, pos, freq)
}

// tryResonateVibration is SculkSensorBlock.tryResonateVibration: for
// each of the six neighbours whose block is in vibration_resonators
// (amethyst), re-emit the heard frequency as resonate_<freq> at the
// resonator's position and chime it — the relay that lets a
// frequency hop sensor→amethyst→sensor across a listening gap.
func (s *Server) tryResonateVibration(d *dimension, pos world.Pos, freq int) {
	for _, off := range [6]world.Pos{
		{X: 1}, {X: -1}, {Y: 1}, {Y: -1}, {Z: 1}, {Z: -1},
	} {
		npos := world.Pos{X: pos.X + off.X, Y: pos.Y + off.Y, Z: pos.Z + off.Z}
		owner, ok := world.StateOwner(d.w.GetBlock(npos))
		if !ok || !blockInTag("vibration_resonators", strings.TrimPrefix(owner, "minecraft:")) {
			continue
		}
		s.emitGameEvent(d, "resonate_"+strconv.Itoa(freq),
			float64(npos.X)+0.5, float64(npos.Y)+0.5, float64(npos.Z)+0.5, nil, "")
		s.playSoundAt(d.name, float64(npos.X)+0.5, float64(npos.Y)+0.5, float64(npos.Z)+0.5,
			34 /* block.amethyst_block.resonate */, 1.0, resonancePitchBend(freq))
	}
}

// sculkResonanceNotes is vanilla's RESONANCE_PITCHES table — the
// note index each frequency chimes at, bent into a pitch by
// NoteBlock.getPitchFromNote's 2^((note-12)/12).
var sculkResonanceNotes = [16]int{0, 0, 1, 2, 4, 6, 7, 9, 10, 12, 14, 15, 18, 21, 22, 24}

func resonancePitchBend(freq int) float32 {
	if freq < 0 || freq > 15 {
		return 1.0
	}
	return float32(math.Pow(2, float64(sculkResonanceNotes[freq]-12)/12.0))
}

// sculkSensorTick is SculkSensorBlock.tick — the scheduled phase
// machine. ACTIVE deactivates to COOLDOWN with power 0 and schedules
// the cooldown-clearing tick in deactivate's constant 10; COOLDOWN
// falls back to INACTIVE (and plays clicking_stop when dry).
func (s *Server) sculkSensorTick(d *dimension, pos world.Pos, blk world.Block) {
	prop := rsPropsOf(blk)
	switch prop("sculk_sensor_phase") {
	case "active":
		nb, _ := world.WithProperty(blk, "sculk_sensor_phase", "cooldown")
		nb, _ = world.WithProperty(nb, "power", "0")
		d.w.SetBlock(pos, nb)
		s.broadcastBlockState(d, pos, nb)
		s.scheduleBlockTick(d, pos, 10)
		s.sculkUpdateNeighbours(d, pos)
	case "cooldown":
		nb, _ := world.WithProperty(blk, "sculk_sensor_phase", "inactive")
		d.w.SetBlock(pos, nb)
		s.broadcastBlockState(d, pos, nb)
		s.sculkUpdateNeighbours(d, pos)
		if prop("waterlogged") != "true" {
			pitch := float32(0.8 + s.rng.nextDouble()*0.2)
			s.playSoundAt(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
				1421 /* block.sculk_sensor.clicking_stop */, 1.0, pitch)
		}
	}
}

// sculkUpdateNeighbours is SculkSensorBlock.updateNeighbours:
// updateNeighborsAt on the sensor pos plus the block below it (the
// signal reads through the floor for dust underneath).
func (s *Server) sculkUpdateNeighbours(d *dimension, pos world.Pos) {
	s.rsNeighborFanOut(d, pos)
	s.rsNeighborFanOut(d, world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
}

// sculkNBT renders the block entity's saveAdditional body: the last
// received frequency plus the VibrationSystem$Data codec — an
// event_delay, the selector slot (always written, tick -1 when
// empty), and the traveling event when one is in flight.
func sculkNBT(extra map[string]any, sen *sculkSensor) {
	extra["last_vibration_frequency"] = int32(sen.lastFreq)
	sel := map[string]any{"tick": int64(-1)}
	if sen.sel != nil {
		sel["tick"] = sen.sel.selectTick
		sel["event"] = vibrationNBT(sen.sel)
	}
	listener := map[string]any{
		"event_delay": int32(sen.travel),
		"selector":    sel,
	}
	if sen.cur != nil {
		listener["event"] = vibrationNBT(sen.cur)
	}
	extra["listener"] = listener
}

// vibrationNBT is VibrationInfo's codec — the event key, the float
// distance, and the source position as a 3-double Vec3.
func vibrationNBT(v *vibration) map[string]any {
	return map[string]any{
		"game_event": "minecraft:" + v.event,
		"distance":   float32(v.dist),
		"pos": nbt.List{Elem: nbt.TagDouble, Items: []any{
			v.x, v.y, v.z,
		}},
	}
}

// sculkFromNBT rebuilds the sensor's vibration state from a saved
// block entity compound — the mirror of sculkNBT.
func sculkFromNBT(be map[string]any) *sculkSensor {
	sen := newSculkSensor()
	if f, ok := nbtNumber(be["last_vibration_frequency"]); ok {
		sen.lastFreq = int(f)
	}
	l, ok := be["listener"].(map[string]any)
	if !ok {
		return sen
	}
	sen.travel = int(nbtNumberOr(l["event_delay"], 0))
	if ev, ok := l["event"].(map[string]any); ok {
		sen.cur = vibrationFromNBT(ev, -1)
	}
	if sel, ok := l["selector"].(map[string]any); ok {
		if ev, ok := sel["event"].(map[string]any); ok {
			sen.sel = vibrationFromNBT(ev, int64(nbtNumberOr(sel["tick"], -1)))
		}
	}
	return sen
}

// vibrationFromNBT reads a saved VibrationInfo; the selector's tick
// is supplied by the caller (the traveling vibration carries none).
func vibrationFromNBT(m map[string]any, tick int64) *vibration {
	v := &vibration{selectTick: tick}
	if ev, ok := m["game_event"].(string); ok {
		v.event = strings.TrimPrefix(ev, "minecraft:")
	}
	if df, ok := nbtFloat(m["distance"]); ok {
		v.dist = float64(df)
	}
	if pos, ok := nbtVec3(m["pos"]); ok {
		v.x, v.y, v.z = pos[0], pos[1], pos[2]
	}
	return v
}
