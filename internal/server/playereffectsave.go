package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
)

// Durations are remaining player ticks, not deadlines on the old process clock.
// Hidden effects retain their own independently decaying remaining durations.
type playerEffectPersist struct {
	ID        int32                `json:"id"`
	Amplifier int32                `json:"amplifier"`
	Duration  int64                `json:"duration"`
	Flags     byte                 `json:"flags"`
	Hidden    *playerEffectPersist `json:"hidden,omitempty"`
}

func playerEffectSnapshot(id int32, inst mobEffectInst, now int64) playerEffectPersist {
	duration := int64(-1)
	if inst.expiry >= 0 {
		duration = inst.expiry - now
	}
	saved := playerEffectPersist{ID: id, Amplifier: inst.amplifier, Duration: duration, Flags: potionStoredFlags(inst) & 7}
	if inst.hidden != nil {
		child := playerEffectSnapshot(id, *inst.hidden, now)
		saved.Hidden = &child
	}
	return saved
}
func playerEffectRestore(saved playerEffectPersist, now int64) mobEffectInst {
	expiry := now + max(saved.Duration, 0)
	if saved.Duration == -1 {
		expiry = -1
	}
	inst := mobEffectInst{amplifier: saved.Amplifier, expiry: expiry, born: now, hide: saved.Flags&2 == 0, flags: saved.Flags | 8, flagsSet: true}
	if saved.Hidden != nil {
		child := playerEffectRestore(*saved.Hidden, now)
		inst.hidden = &child
	}
	return inst
}
func snapshotPlayerEffects(sess *Session) []playerEffectPersist {
	if sess.left {
		return cloneSavedPlayerEffects(sess.retiredEffects)
	}
	sess.effMu.Lock()
	defer sess.effMu.Unlock()
	if sess.server == nil || len(sess.effects) == 0 {
		return nil
	}
	now := sess.effectNow()
	out := make([]playerEffectPersist, 0, len(sess.effects))
	for id, inst := range sess.effects {
		out = append(out, playerEffectSnapshot(id, inst, now))
	}
	return out
}
func playerEffectsSaveList(effects []playerEffectPersist) nbt.List {
	instances := map[int32]mobEffectInst{}
	for _, effect := range effects {
		instances[effect.ID] = playerEffectRestore(effect, 0)
	}
	return mobEffectsNBT(instances, 0)
}
func playerEffectsFromSave(value any) []playerEffectPersist {
	instances := mobEffectsFromNBT(value, 0)
	if len(instances) == 0 {
		return nil
	}
	effects := make([]playerEffectPersist, 0, len(instances))
	for id, inst := range instances {
		effects = append(effects, playerEffectSnapshot(id, inst, 0))
	}
	return effects
}
func cloneSavedPlayerEffects(effects []playerEffectPersist) []playerEffectPersist {
	out := cloneDataSaveSlice(effects)
	for i := range out {
		if out[i].Hidden != nil {
			child := cloneSavedPlayerEffects([]playerEffectPersist{*out[i].Hidden})[0]
			out[i].Hidden = &child
		}
	}
	return out
}
func (s *Server) restoreSavedPlayerEffects(sess *Session, effects []playerEffectPersist) {
	sess.effMu.Lock()
	defer sess.effMu.Unlock()
	sess.effects = nil
	if len(effects) > 0 {
		sess.effects = make(map[int32]mobEffectInst, len(effects))
		for _, effect := range effects {
			sess.effects[effect.ID] = playerEffectRestore(effect, sess.effectNow())
		}
	}
}
func (s *Server) sendSavedPlayerEffects(sess *Session) {
	sess.effMu.Lock()
	effects := make(map[int32]mobEffectInst, len(sess.effects))
	for id, inst := range sess.effects {
		effects[id] = inst
	}
	sess.effMu.Unlock()
	for id, inst := range effects {
		var body protocol.Buffer
		body.VarInt(sess.entityID.Load())
		body.VarInt(id)
		body.VarInt(inst.amplifier)
		remaining := inst.expiry - sess.effectNow()
		if inst.expiry < 0 {
			remaining = -1
		}
		body.VarInt(int32(remaining))
		body.Byte(potionStoredFlags(inst))
		_ = sess.writeMobEffect776(body.Bytes(), false)
	}
}
