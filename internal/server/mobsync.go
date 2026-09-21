package server

import (
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
// mobPairingAttrTypes are the entity types whose syncable attributes
// ride inside the pairing burst; every type with syncable attributes
// also emits update_attributes once on the first dirty tick.
var mobPairingAttrTypes = map[int32]bool{}

// mobSyncableSpeed is Attributes.MOVEMENT_SPEED's per-type base — the
// only client-syncable attribute the measured mobs carry.
var mobSyncableSpeed = map[string]float64{
	"zombie_villager": 0.23000000417232513,
	"villager":        0.5,
}

func init() {
	if id, ok := EntityTypeID("zombie_villager"); ok {
		mobPairingAttrTypes[id] = true
	}
}

// mobSyncAttrs builds update_attributes' body for a mob: one
// movement_speed entry, no modifiers.
func mobSyncAttrs(e *Entity) []byte {
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return nil
	}
	speed, ok := mobSyncableSpeed[name]
	if !ok {
		return nil
	}
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.VarInt(1)  // attribute count
	b.VarInt(26) // minecraft:movement_speed registry id
	b.Double(speed)
	b.VarInt(0) // modifiers
	return b.Bytes()
}

func (s *Server) broadcastMobAttrs(e *Entity) {
	body := mobSyncAttrs(e)
	if body == nil {
		return
	}
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
			return true
		}
		_ = other.write(IDPlayUpdateAttributes, body)
		return true
	})
}

// broadcastMobMoveZero emits move_entity_pos with zero deltas — the
// packet vanilla sends after a summon snapTo and every 60 sync ticks
// for a stationary mob.
func (s *Server) broadcastMobMoveZero(e *Entity) {
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.Short(0)
	b.Short(0)
	b.Short(0)
	b.Bool(false)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
			return true
		}
		_ = other.write(IDPlayEntityUpdatePos, body)
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

// mobSyncFlush is the per-mob sendChanges pass: a zero-delta
// move_entity_pos when the summon marked the position dirty or every
// 60 sync ticks, the full metadata resend while entries are dirty
// (effect particles included — vanilla computes them lazily), then the
// attribute dirty resend.
func (s *Server) mobSyncFlush(e *Entity) {
	if e.PosDirty || e.SyncCount%60 == 0 {
		e.PosDirty = false
		s.broadcastMobMoveZero(e)
	}
	if e.MetaDirty {
		e.MetaDirty = false
		s.broadcastEntityMetadata(e.ID, encodeLivingMetadata(e, s.effectParticlesValue(e)))
		e.ParticlesSent = s.effectParticlesValue(e)
	} else if p := s.effectParticlesValue(e); !bytesEq(p, e.ParticlesSent) {
		e.ParticlesSent = p
		s.broadcastEntityMetadata(e.ID, encodeParticlesEntry(p))
	}
	if e.AttrDirty {
		e.AttrDirty = false
		s.broadcastMobAttrs(e)
	}
	e.SyncCount++
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
