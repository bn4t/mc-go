package server

import (
	"reflect"
	"testing"
)

func TestPlayerEffectSaveRelativeHiddenLifetime(t *testing.T) {
	original := mobEffectInst{amplifier: 2, expiry: 110, born: 90, flags: 5, flagsSet: true, hidden: &mobEffectInst{amplifier: 0, expiry: 150, flags: 2, flagsSet: true}}
	saved := playerEffectSnapshot(effectSpeed, original, 100)
	if saved.Duration != 10 || saved.Hidden.Duration != 50 {
		t.Fatalf("relative durations%+v %+v", saved, saved.Hidden)
	}
	p := playerPersist{Effects: []playerEffectPersist{saved}}
	decoded := playerFromDat(playerDatCompound(p))
	if !reflect.DeepEqual(p.Effects, decoded.Effects) {
		t.Fatalf("effect native codec got%+v want%+v", decoded.Effects, p.Effects)
	}
	restored := playerEffectRestore(decoded.Effects[0], 1000)
	if restored.expiry != 1010 || restored.hidden.expiry != 1050 || restored.amplifier != 2 || restored.flags != 13 {
		t.Fatalf("new-clock lifetime%+v child%+v", restored, restored.hidden)
	}
	var clone dataSaveCloner
	detached := clone.player(p)
	detached.Effects[0].Hidden.Duration = 99
	if p.Effects[0].Hidden.Duration != 50 {
		t.Fatal("hidden-effect alias across detached snapshot")
	}
	if got := playerEffectRestore(playerEffectPersist{Duration: -2}, 1000).expiry; got != 1000 {
		t.Fatalf("expired hidden became infinite: %d", got)
	}
	if got := playerEffectRestore(playerEffectPersist{Duration: -1}, 1000).expiry; got != -1 {
		t.Fatal("infinite lifetime lost")
	}
}
func TestPlayerEffectAttributeRemovalKeepsUnrelatedCommands(t *testing.T) {
	s := New("unused")
	sess := &Session{server: s}
	e := s.entities.Spawn(&Entity{Type: mustEntityType("player"), AttrMods: map[string]map[string]attrMod{"luck": {"minecraft:effect.luck": {value: 7, permanent: true}}, "movement_speed": {"test:command": {value: .5, permanent: true}}}})
	sess.entityID.Store(e.ID)
	s.setPlayerEffectAttributes(sess, effectSpeed, mobEffectInst{amplifier: 1})
	expected := 2 * float64(float32(.2))
	if got := e.AttrMods["movement_speed"]["minecraft:effect.speed"]; got.value != expected || !got.permanent || got.op != 2 {
		t.Fatalf("permanent speed facts%+v", got)
	}
	// Command replacement during an active effect is removed by that effect's
	// real end; an unrelated effect-looking command ID must survive.
	e.AttrMods["movement_speed"]["minecraft:effect.speed"] = attrMod{value: .75, op: 2, permanent: true}
	sess.effects = nil
	s.removeMobEffect(sess, effectSpeed)
	if e.AttrMods["movement_speed"]["minecraft:effect.speed"].value != .75 {
		t.Fatal("absent-effect request removed same-ID command modifier")
	}
	s.removePlayerEffectAttributes(sess, effectSpeed)
	if _, present := e.AttrMods["movement_speed"]["minecraft:effect.speed"]; present {
		t.Fatal("expired effect ID orphaned")
	}
	if e.AttrMods["luck"]["minecraft:effect.luck"].value != 7 || e.AttrMods["movement_speed"]["test:command"].value != .5 {
		t.Fatal("unrelated permanent modifier filtered")
	}
}
