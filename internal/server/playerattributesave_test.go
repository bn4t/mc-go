package server

import (
	"reflect"
	"testing"
)

func TestPlayerAttributeNativeSaveRoundTrip(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "AttrSave")
	e := s.entities.Spawn(&Entity{Type: mustEntityType("player"), AttrBase: map[string]float64{"scale": 2, "luck": 2048}, AttrTouched: map[string]bool{"scale": true, "luck": true, "attack_damage": true}, AttrMods: map[string]map[string]attrMod{
		"scale":         {"test:permanent": {value: .25, permanent: true}},
		"attack_damage": {"minecraft:base_attack_damage": {value: 6, permanent: false}},
	}})
	sess.entityID.Store(e.ID)
	attrs := snapshotPlayerAttributes(sess)
	p := playerPersist{Attributes: attrs}
	restored := playerFromDat(playerDatCompound(p))
	if !reflect.DeepEqual(restored.Attributes, attrs) {
		t.Fatalf("raw save round trip\ngot%+v\nwant%+v", restored.Attributes, attrs)
	}
	for _, a := range attrs {
		if a.Name == "attack_damage" && len(a.Modifiers) != 0 {
			t.Fatal("transient gear promoted to permanent save")
		}
	}
	e.AttrMods["scale"]["test:permanent"] = attrMod{value: 9, permanent: true}
	var cloned dataSaveCloner
	detached := cloned.player(p)
	for i := range detached.Attributes {
		if detached.Attributes[i].Name == "scale" {
			detached.Attributes[i].Modifiers[0].Amount = 19
		}
	}
	for _, a := range p.Attributes {
		if a.Name == "scale" && a.Modifiers[0].Amount != .25 {
			t.Fatal("saved modifier aliases live or detached mutation")
		}
	}
	s.savedPlayers = map[string]playerPersist{"AttrSave": restored}
	fresh := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
	sess.entityID.Store(fresh.ID)
	s.applySavedPlayerAttributes(sess)
	if fresh.AttrBase["scale"] != 2 || fresh.AttrBase["luck"] != 2048 || fresh.AttrMods["scale"]["test:permanent"].value != .25 || !fresh.AttrMods["scale"]["test:permanent"].permanent {
		t.Fatalf("raw base/permanent restore lost: %+v %+v", fresh.AttrBase, fresh.AttrMods)
	}
	if _, has := fresh.AttrMods["attack_damage"]["minecraft:base_attack_damage"]; has {
		t.Fatal("equipment modifier restored as permanent")
	}
	if !fresh.AttrTouched["attack_damage"] {
		t.Fatal("touched supplier-base instance lost")
	}
	s.simulationAction(func() {
		sess.effects = map[int32]mobEffectInst{effectSpeed: {expiry: sess.effectNow() + 10, flags: 14, flagsSet: true}}
		s.retirePlayerOwned(&sess.playerRecord, "Attribute save test")
		s.clock.count += 100
	})
	effectsAfterRetire := snapshotPlayerEffects(sess)
	if len(effectsAfterRetire) != 1 || effectsAfterRetire[0].Duration != 10 {
		t.Fatalf("retired effect lifetime decayed offline: %+v", effectsAfterRetire)
	}
	afterRetire := snapshotPlayerAttributes(sess)
	wantAfterLoad := clonePlayerSavedAttributes(restored.Attributes)
	for i := range wantAfterLoad {
		if wantAfterLoad[i].Name == "movement_speed" {
			wantAfterLoad[i].Base = float64(restored.savedWalkingSpeed())
		}
	}
	if !reflect.DeepEqual(afterRetire, wantAfterLoad) {
		t.Fatalf("retirement removed raw attributes: got%+v want%+v", afterRetire, restored.Attributes)
	}

}
