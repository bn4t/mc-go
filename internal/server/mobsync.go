package server

import (
	"math"
	"sort"

	"mc-go/internal/protocol"
)

// Vanilla's ServerEntity pairing + sendChanges wire model, measured on
// the 26.2 jar:
//
//   - sendPairingData (tracker attach) = spawn, set_entity_data
//     (every non-default entry — effect particles are always absent
//     since updateDirtyEffects hasn't run yet), update_attributes (only
//     when the mob's attribute map is materialized pre-attach — zombie
//     family yes, villager no), set_equipment, set_passengers.
//   - Dirty flags are NOT cleared by pairing, so the next tick's
//     sendChanges re-emits the full data set plus newly-dirtied entries
//     (effect particles land at index 10 then), the syncable attributes
//     again, and — because summon snapTo marks the position dirty —
//     a zero-delta move_entity_pos.
//   - sendChanges also forces a position resend at tickCount%60.
//
// attrSyncable is Attributes' client-syncable set — the attributes
// registered with setSyncable(true). It is the filter vanilla's
// AttributeMap.getSyncableAttributes applies over the instantiated
// (touched) attribute set when sendPairingData builds update_attributes.
// The rest (follow_range, attack_damage, spawn_reinforcements, ...) are
// never client-syncable.
var attrSyncable = map[string]bool{
	"air_drag_modifier": true, "armor": true, "armor_toughness": true,
	"attack_speed": true, "below_name_distance": true,
	"block_break_speed": true, "block_interaction_range": true,
	"bounciness": true, "burning_time": true, "camera_distance": true,
	"explosion_knockback_resistance": true,
	"entity_interaction_range":       true, "fall_damage_multiplier": true,
	"flying_speed": true, "friction_modifier": true, "gravity": true,
	"jump_strength": true, "luck": true, "max_absorption": true,
	"max_health": true, "mining_efficiency": true,
	"movement_efficiency": true, "movement_speed": true,
	"name_tag_distance": true, "oxygen_bonus": true,
	"safe_fall_distance": true, "scale": true, "sneaking_speed": true,
	"step_height": true, "submerged_mining_speed": true,
	"sweeping_damage_ratio": true, "water_movement_efficiency": true,
}

// mobSyncAttrs builds update_attributes' body for a mob: every dirty
// syncable attribute instance, at its current base, with the effect.speed
// modifier folded into movement_speed while a live speed instance is
// active (vanilla's SpeedEffect modifier is 0.2*(amplifier+1)
// add_multiplied_total). Emitted names are sorted for determinism —
// vanilla's emit order is fastutil hash order.
func mobSyncAttrs(e *Entity, now int64) []byte {
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return nil
	}
	bases := entityAttrBases[name]
	if len(bases) == 0 || len(e.AttrsDirty) == 0 {
		return nil
	}
	names := make([]string, 0, len(e.AttrsDirty))
	for a := range e.AttrsDirty {
		if attrSyncable[a] {
			if _, ok := attrIDByName[a]; ok {
				names = append(names, a)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.VarInt(int32(len(names)))
	for _, a := range names {
		var mods []attrModifier
		if a == "movement_speed" {
			if inst, ok := e.Effects[effectSpeed]; ok && (inst.expiry < 0 || inst.expiry > now) {
				mods = append(mods, attrModifier{
					key:    "minecraft:effect.speed",
					amount: 0.2 * float64(inst.amplifier+1),
					op:     2, // add_multiplied_total
				})
			}
		}
		base, has := e.AttrBase[a]
		if !has {
			base = bases[a]
		}
		writeAttrEntry(&b, attrIDByName[a], base, mods...)
	}
	return b.Bytes()
}

func (s *Server) broadcastMobAttrs(e *Entity) {
	body := mobSyncAttrs(e, s.clock.count)
	if body == nil {
		return
	}
	// The dirty set is consumed by the broadcast.
	e.AttrsDirty = nil
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
			return true
		}
		if !other.tracksEntity(e.ID) {
			return true
		}
		_ = other.write(IDPlayUpdateAttributes, body)
		return true
	})
}

// mobEffectColors maps a mob_effect name to its ARGB color — the
// register() call's color argument, extracted from MobEffects bytecode.
var mobEffectColors = map[string]int32{
	"speed":                  3402751,
	"slowness":               9154528,
	"haste":                  14270531,
	"mining_fatigue":         4866583,
	"strength":               16762624,
	"instant_health":         16262179,
	"instant_damage":         11101546,
	"jump_boost":             16646020,
	"nausea":                 5578058,
	"regeneration":           13458603,
	"resistance":             9520880,
	"fire_resistance":        16750848,
	"water_breathing":        10017472,
	"invisibility":           16185078,
	"blindness":              2039587,
	"night_vision":           12779366,
	"hunger":                 5797459,
	"weakness":               4738376,
	"poison":                 8889187,
	"wither":                 7561558,
	"health_boost":           16284963,
	"absorption":             2445989,
	"saturation":             16262179,
	"glowing":                9740385,
	"levitation":             13565951,
	"luck":                   5882118,
	"unluck":                 12624973,
	"slow_falling":           15978425,
	"conduit_power":          1950417,
	"dolphins_grace":         8954814,
	"bad_omen":               745784,
	"hero_of_the_village":    4521796,
	"darkness":               2696993,
	"trial_omen":             1484454,
	"raid_omen":              14565464,
	"wind_charged":           12438015,
	"weaving":                7891290,
	"oozing":                 10092451,
	"infested":               9214860,
	"breath_of_the_nautilus": 65518,
}

// effectParticlesValue encodes DATA_EFFECT_PARTICLES' value (metadata
// index 10, serializer 17): a VarInt count plus one entity_effect
// particle per live effect — VarInt particle id + Int32-BE ARGB color.
// An instance stays visible for the tick where its duration runs out:
// vanilla computes the particle list in updateDataBeforeSync before the
// entity's own tickEffects sweeps the expired entry.
func (s *Server) effectParticlesValue(e *Entity) []byte {
	ids := make([]int32, 0, len(e.Effects))
	for id, inst := range e.Effects {
		if inst.expiry >= 0 && inst.expiry+1 < s.clock.count {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	entityEffect, _ := ParticleTypeID("entity_effect")
	var b protocol.Buffer
	b.VarInt(int32(len(ids)))
	for _, id := range ids {
		color := int32(0)
		if name, ok := MobEffectName(id); ok {
			color = mobEffectColors[name]
		}
		b.VarInt(entityEffect)
		b.Int(int32(uint32(0xFF000000) | uint32(color)))
	}
	return b.Bytes()
}

// encodeParticlesEntry wraps a DATA_EFFECT_PARTICLES value as a lone
// metadata entry — the dirty pack vanilla emits when the live-effect
// set shrinks.
func encodeParticlesEntry(value []byte) []byte {
	var b protocol.Buffer
	b.Byte(10)
	b.Byte(17) // PARTICLES serializer
	if len(value) == 0 {
		b.VarInt(0)
	} else {
		b.Raw(value)
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// encodeCloudMeta packs AreaEffectCloud's non-default synced entries —
// DATA_RADIUS (index 8, float serializer 3) when the radius differs
// from the 3.0 defineSynchedData default, and DATA_PARTICLE (index 10,
// particle serializer 16) as an entity_effect holder carrying the
// mixed potion ARGB when the cloud carries effects. A bare cloud's
// packDirty is empty and vanilla sends no set_entity_data at all.
func encodeCloudMeta(e *Entity) []byte {
	var b protocol.Buffer
	if e.CloudRadius != 3.0 {
		b.Byte(8)
		b.Byte(3)
		b.Float(e.CloudRadius)
	}
	if len(e.Effects) > 0 {
		entityEffect, _ := ParticleTypeID("entity_effect")
		b.Byte(10)
		b.Byte(16)
		b.VarInt(entityEffect)
		b.Int(cloudParticleARGB(e.Effects))
	}
	if b.Len() == 0 {
		return nil
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// encodeCloudWaiting packs the single-entry dirty pack for
// DATA_WAITING (index 9, bool serializer 8) flipping at a wait-phase
// boundary.
func encodeCloudWaiting(w bool) []byte {
	var b protocol.Buffer
	b.Byte(9)
	b.Byte(8)
	if w {
		b.Byte(1)
	} else {
		b.Byte(0)
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// cloudParticleARGB mixes the cloud's effect colors the way
// PotionContents.getColor does — a per-channel integer average over
// the live effects with a fully-opaque alpha.
func cloudParticleARGB(effects map[int32]mobEffectInst) int32 {
	ids := make([]int32, 0, len(effects))
	for id := range effects {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var r, g, b, n int32
	for _, id := range ids {
		color := int32(0)
		if name, ok := MobEffectName(id); ok {
			color = mobEffectColors[name]
		}
		r += (color >> 16) & 0xFF
		g += (color >> 8) & 0xFF
		b += color & 0xFF
		n++
	}
	if n > 0 {
		r, g, b = r/n, g/n, b/n
	}
	return int32(uint32(0xFF000000) | uint32(r<<16) | uint32(g<<8) | uint32(b))
}

// latchSyncBaseline establishes the entity's send baseline the way
// vanilla's ServerEntity.broadcast does at pairing: the spawn packet
// carried position and rotation, so deltas measure from there.
// sendEntityTo latches at pair time so entities nobody ticks (boats,
// orbs between ticks) still measure their first flush against the
// spawned position, not the already-moved one.
func (s *Server) latchSyncBaseline(e *Entity) {
	e.syncInit = true
	e.syncPX, e.syncPY, e.syncPZ = mobFixed(e.X), mobFixed(e.Y), mobFixed(e.Z)
	e.lastYRotB = angleByte(e.Yaw)
	e.lastXRotB = angleByte(e.Pitch)
	e.lastYHeadB = angleByte(e.HeadYaw)
	e.wasOnGround = e.Grounded
	// ServerEntity's constructor also latches lastSentMovement =
	// getDeltaMovement() — the drift baseline is the attach-time
	// delta, so a hurting projectile summoned already moving emits
	// its power bundle at the first gate-open.
	e.SentMotionSet = true
	e.SentMotionX, e.SentMotionY, e.SentMotionZ = e.MotionX, e.MotionY, e.MotionZ
}

// mobSyncFlush is the per-mob sendChanges pass: position/rotation
// through the vanilla packet selection, the full metadata resend while
// entries are dirty (effect particles included — vanilla computes them
// lazily), then the attribute dirty resend.
func (s *Server) mobSyncFlush(e *Entity) {
	if !e.syncInit {
		// Entities that never paired still latch on first flush —
		// latching here (not at first gate-fire) keeps an interval-20
		// item's first emit carrying the fall since spawn.
		s.latchSyncBaseline(e)
	}
	// Vanilla's sendChanges rounds the entity's own tickCount up to the
	// next updateInterval multiple when a teleport marked it
	// (entity.syncPosition) — the position packet rides THIS tick and
	// the cadence phase restarts a full interval later. PosDirty is
	// mc-go's needsSync; the >400 teleportDelay is the same latch.
	if e.teleportDelay == 401 {
		iv := s.entitySyncInterval(e)
		e.SyncCount = (e.SyncCount/iv)*iv + iv
	}
	// Vanilla's gate reads the entity's own tickCount BEFORE this tick's
	// increment: a fresh tracker emits on its first sendChanges
	// (0 % interval == 0) — e.g. an item's first fall tick rides a
	// move_entity_pos one tick after the spawn packet.
	if e.SyncCount%s.entitySyncInterval(e) == 0 || e.PosDirty || e.MetaDirty {
		s.mobMoveSync(e)
	}
	e.SyncCount++
	if len(e.dataSync) > 0 && s.gameTime() >= e.dataSyncTick {
		data := e.dataSync
		e.dataSync = nil
		s.broadcastEntityMetadata(e.ID, data)
	} else if e.MetaDirty {
		e.MetaDirty = false
		// packDirty sends only the changed entries — an empty diff
		// (a stale latch, or a merge that already dataSynced the
		// change) means no packet at all, like vanilla's sendChanges.
		if meta := encodeLivingMetadataDirty(e, s.effectParticlesValue(e)); len(meta) > 0 {
			s.broadcastEntityMetadata(e.ID, meta)
		}
		e.ParticlesSent = s.effectParticlesValue(e)
	} else if p := s.effectParticlesValue(e); !bytesEq(p, e.ParticlesSent) {
		e.ParticlesSent = p
		s.broadcastEntityMetadata(e.ID, encodeParticlesEntry(p))
	}
	if e.dataSyncAttrs && s.gameTime() >= e.dataSyncTick {
		e.dataSyncAttrs = false
		s.broadcastMobAttrs(e)
		e.AttrDirty = len(e.AttrsDirty) > 0
	} else if e.AttrDirty && !(e.dataSyncAttrs && s.gameTime() < e.dataSyncTick) {
		// A pending dataSyncAttrs resend holds the attr emit for the
		// same boundary as the meta pack — vanilla's sendChanges
		// flushes the dirty set and the attribute table together,
		// meta first.
		s.broadcastMobAttrs(e)
		e.AttrDirty = len(e.AttrsDirty) > 0
	}
	// A living mob's first AI tick reads its movement speed — the
	// AttributeInstance materializes then, marked dirty on creation,
	// so vanilla emits it once on the following sendChanges.
	if e.Living && !e.AttrTouched["movement_speed"] {
		if name, ok := EntityTypeName(e.Type); ok {
			if _, ok := entityAttrBases[name]["movement_speed"]; ok {
				e.markAttrTouched("movement_speed")
			}
		}
	}
	// ServerEntity.sendChanges' last block: a hurt-marked entity
	// flushes set_entity_motion unconditionally — the drift gate and
	// the updateInterval cadence are both bypassed, and the emit is
	// self-inclusive (sendToTrackingPlayersAndSelf) so a player
	// victim's own session receives it too.
	if e.HurtMarked {
		e.HurtMarked = false
		e.SentMotionX, e.SentMotionY, e.SentMotionZ = e.MotionX, e.MotionY, e.MotionZ
		s.broadcastEntityMotion(e.Dim, e.ID, e.MotionX, e.MotionY, e.MotionZ)
	}
}

// mobFixed is the tracker's fixed-point position scale: one block is
// 4096 units, and positions encode floored like vanilla's
// TrackedPosition.
func mobFixed(v float64) int64 {
	return int64(math.Floor(v * 4096))
}

func mobByteDiff(a, b byte) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}

// mobMoveSync is vanilla ServerEntity.sendChanges' position branch:
// the delta against the last sent base picks move_entity_pos,
// move_entity_pos_rot, or move_entity_rot, while an out-of-range delta,
// a long silence, an onGround flip, or a ride boundary promotes to
// entity_position_sync. The head's packed yaw rides rotate_head on its
// own slot. Runs only on sendChanges-eligible ticks.
func (s *Server) mobMoveSync(e *Entity) {
	// TrackedDeltaMovement (ServerEntity.sendChanges): the motion
	// branch itself is gated — it runs when needsSync is flagged, when
	// the entity's type is NOT one of EntityType.trackDeltas' ten
	// exclusions (the bytecode returns false only for those — every
	// other type runs the drift check every gate-open), or a living
	// entity is fall-flying. LivingEntity.aiStep's push latches
	// needsSync every living tick, so a mob is always gated open and
	// the drift check decides. Inside it, emit set_entity_motion when
	// the current delta drifts farther than 1e-7 squared from the last
	// sent one, or when it just turned exactly zero — a walking mob
	// emits on accel/decel and stop, not per tick at cruise.
	motionGate := e.NeedsSync || (e.Living && e.FallFlying)
	if !motionGate {
		if tn, ok := EntityTypeName(e.Type); ok {
			motionGate = !trackDeltaTypes[tn]
		}
	}
	if !e.SentMotionSet {
		e.SentMotionSet = true
		e.SentMotionX, e.SentMotionY, e.SentMotionZ = e.MotionX, e.MotionY, e.MotionZ
	} else if motionGate {
		dx, dy, dz := e.MotionX-e.SentMotionX, e.MotionY-e.SentMotionY, e.MotionZ-e.SentMotionZ
		drift := dx*dx + dy*dy + dz*dz
		curLen := e.MotionX*e.MotionX + e.MotionY*e.MotionY + e.MotionZ*e.MotionZ
		if drift > 1e-7 || (drift > 0 && curLen == 0) {
			e.SentMotionX, e.SentMotionY, e.SentMotionZ = e.MotionX, e.MotionY, e.MotionZ
			// ServerEntity wraps the motion emit in a bundle with
			// projectile_power for AbstractHurtingProjectile; every
			// other type ships the bare set_entity_motion.
			if name, ok := EntityTypeName(e.Type); ok && hurtingProjectileTypes[name] {
				s.broadcastHurtingMotion(e)
			} else {
				s.broadcastEntityMotion(e.Dim, e.ID, e.MotionX, e.MotionY, e.MotionZ)
			}
		}
	}
	yRotB := angleByte(e.Yaw)
	xRotB := angleByte(e.Pitch)
	rotChanged := mobByteDiff(yRotB, e.lastYRotB) >= 1 || mobByteDiff(xRotB, e.lastXRotB) >= 1
	e.teleportDelay++
	sx, sy, sz := mobFixed(e.X), mobFixed(e.Y), mobFixed(e.Z)
	dx, dy, dz := sx-e.syncPX, sy-e.syncPY, sz-e.syncPZ
	// TrackedPosition.delta().lengthSqr() threshold, block space.
	posChanged := float64(dx*dx+dy*dy+dz*dz)/(4096.0*4096.0) >= 7.62939453125e-6
	// Vanilla's periodic-resync reads the ENTITY's tickCount — one past
	// this tracker's on the first sendChanges — so it never forces a
	// position packet on the tick-0 gate. PosDirty (vanilla's
	// syncPosition needsSync, latched by snapTo at summon) forces the
	// position branch even when the delta is zero — the pairing burst
	// answers with a zero-delta move_entity_pos.
	pos := posChanged || e.PosDirty || (e.SyncCount+1)%60 == 0
	deltaTooBig := dx < -32768 || dx > 32767 || dy < -32768 || dy > 32767 || dz < -32768 || dz > 32767
	var sentPos, sentRot bool
	switch {
	case deltaTooBig || e.teleportDelay > 400 || e.wasOnGround != e.Grounded:
		e.teleportDelay = 0
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, e.Grounded)
		sentPos, sentRot = true, true
	case pos && rotChanged:
		var b protocol.Buffer
		b.VarInt(e.ID)
		b.Short(int16(dx))
		b.Short(int16(dy))
		b.Short(int16(dz))
		b.Byte(yRotB)
		b.Byte(xRotB)
		b.Bool(e.Grounded)
		s.broadcastToTrackers(e, b.Bytes(), func(ids idSet) int32 { return ids.PlayMoveEntityPosRot })
		sentPos, sentRot = true, true
	case pos:
		var b protocol.Buffer
		b.VarInt(e.ID)
		b.Short(int16(dx))
		b.Short(int16(dy))
		b.Short(int16(dz))
		b.Bool(e.Grounded)
		s.broadcastToTrackers(e, b.Bytes(), func(ids idSet) int32 { return ids.PlayMoveEntityPos })
		sentPos = true
	case rotChanged:
		var b protocol.Buffer
		b.VarInt(e.ID)
		b.Byte(yRotB)
		b.Byte(xRotB)
		b.Bool(e.Grounded)
		s.broadcastToTrackers(e, b.Bytes(), func(ids idSet) int32 { return ids.PlayMoveEntityRot })
		sentRot = true
	}
	if sentPos {
		e.syncPX, e.syncPY, e.syncPZ = sx, sy, sz
	}
	if sentRot {
		e.lastYRotB, e.lastXRotB = yRotB, xRotB
	}
	e.wasOnGround = e.Grounded
	e.PosDirty = false
	// ServerEntity.sendChanges' tail clears needsSync — latched
	// again next living tick by aiStep's push.
	e.NeedsSync = false
	yHeadB := angleByte(e.HeadYaw)
	if mobByteDiff(yHeadB, e.lastYHeadB) >= 1 {
		e.lastYHeadB = yHeadB
		var b protocol.Buffer
		b.VarInt(e.ID)
		b.Byte(yHeadB)
		s.broadcastToTrackers(e, b.Bytes(), func(ids idSet) int32 { return ids.PlayRotateHead })
	}
}

// idSet is the per-version packet-id table on a session.
type idSet = ProtoIDs

// broadcastToTrackers writes a mob-tracking packet to every spawned
// play-stage session in the entity's dimension.
func (s *Server) broadcastToTrackers(e *Entity, body []byte, id func(idSet) int32) {
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
			return true
		}
		if !other.tracksEntity(e.ID) {
			return true
		}
		// sendChanges only reaches sessions still paired after the
		// section-change eval — mob positions are live on e, so a live
		// pairable check approximates vanilla's post-eval seenBy.
		if s.isPlayerEntity(e.ID) || s.pairable(other, e) {
			_ = other.write(id(other.ids()), body)
		}
		return true
	})
}

func bytesEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hurtingProjectileTypes is AbstractHurtingProjectile's live
// subclasses: Fireball's two concrete types, DragonFireball,
// WitherSkull, and AbstractWindCharge's pair. Only these carry
// accelerationPower and bundle it with their motion emits.
var hurtingProjectileTypes = map[string]bool{
	"fireball": true, "small_fireball": true, "dragon_fireball": true,
	"wither_skull": true, "wind_charge": true, "breeze_wind_charge": true,
}

// broadcastHurtingMotion ships a hurting projectile's delta sync the
// way ServerEntity.sendChanges does: one ClientboundBundlePacket
// holding set_entity_motion(id, delta) + projectile_power(id,
// accelerationPower), delimiters around it.
func (s *Server) broadcastHurtingMotion(e *Entity) {
	motion := encodeEntityMotion(e.ID, e.MotionX, e.MotionY, e.MotionZ)
	var pb protocol.Buffer
	pb.VarInt(e.ID)
	pb.Double(e.AccelPower)
	power := pb.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != e.Dim {
			return true
		}
		if !other.tracksEntity(e.ID) && other.entityID.Load() != e.ID {
			return true
		}
		ids := other.ids()
		_ = other.write(ids.PlayBundleDelimiter, nil)
		_ = other.write(ids.PlaySetEntityMotion, motion)
		_ = other.write(ids.PlayProjectilePower, power)
		_ = other.write(ids.PlayBundleDelimiter, nil)
		return true
	})
}
