package server

import (
	"math"
	"testing"
)

func TestNPCMovementEffectAttributeLifecycle(t *testing.T) {
	s := New("unused")
	e := s.entities.Spawn(&Entity{Type: mustEntityType("cow"), Living: true})
	speed, _ := MobEffectID("speed")
	slow, _ := MobEffectID("slowness")
	check := func(want float64) {
		t.Helper()
		if got := s.mobAttr(e, "cow", "movement_speed", 0); math.Abs(got-want) > 1e-8 {
			t.Fatalf("movement speed %v want %v", got, want)
		}
	}
	base := mobAttrSupplierBase("cow", "movement_speed")
	s.mobGiveEffect(e, speed, 0, 100, false)
	check(base * (1 + 0.20000000298023224))
	s.mobGiveEffect(e, speed, 1, 5, false)
	s.mobGiveEffect(e, slow, 0, 100, false)
	check(base * (1 + 0.4000000059604645) * (1 - 0.15000000596046448))
	s.clock.count = 5
	s.mobEffectTickStep()
	check(base * (1 + 0.20000000298023224) * (1 - 0.15000000596046448))
	s.mobClearEffect(e, speed)
	check(base * (1 - 0.15000000596046448))
	s.mobClearAllEffects(e)
	check(base)
}

// The wither skeleton hunt consumes the effective attribute as its walk input.
func TestNPCMovementEffectHuntConsumer(t *testing.T) {
	s := New("unused")
	prey := s.entities.Spawn(&Entity{Type: mustEntityType("piglin"), Dim: dimOverworld, Living: true, X: 8, Y: 100, Health: 20})
	makeHunter := func() *Entity {
		return s.entities.Spawn(&Entity{Type: mustEntityType("wither_skeleton"), Dim: dimOverworld, Living: true, Y: 100, Health: 20, HuntMob: prey.ID, HuntScan: 5})
	}
	plain, fast := makeHunter(), makeHunter()
	speed, _ := MobEffectID("speed")
	s.mobGiveEffect(fast, speed, 0, 100, false)
	if !s.witherPiglinStep(plain) || !s.witherPiglinStep(fast) {
		t.Fatal("hunt not admitted")
	}
	if fast.WalkSpeed <= plain.WalkSpeed {
		t.Fatalf("walk speed plain=%v fast=%v", plain.WalkSpeed, fast.WalkSpeed)
	}
}

func TestNPCMovementEffectAttributePackets(t *testing.T) {
	s := New("unused")
	e := s.entities.Spawn(&Entity{Type: mustEntityType("cow"), Living: true})
	id, _ := MobEffectID("speed")
	s.mobGiveEffect(e, id, 0, 100, false)
	for name, body := range map[string][]byte{"pair": pairingAttrs(e, 0), "update": mobSyncAttrs(e, 0)} {
		r, _, n := readAttrHead(t, body)
		found := false
		for i := int32(0); i < n; i++ {
			a := readAttrEntry(t, r)
			if a.id != attrIDByName["movement_speed"] {
				continue
			}
			found = true
			if len(a.mods) != 1 || a.mods[0].key != "minecraft:effect.speed" || a.mods[0].op != 2 || a.mods[0].amount != 0.20000000298023224 {
				t.Fatalf("%s modifiers %+v", name, a.mods)
			}
		}
		if !found {
			t.Fatalf("%s missing movement speed", name)
		}
	}
}

func TestNPCMovementEffectEqualExpiryDoesNotResurrect(t *testing.T) {
	s := New("unused")
	e := s.entities.Spawn(&Entity{Type: mustEntityType("cow"), Living: true})
	id, _ := MobEffectID("speed")
	s.mobGiveEffect(e, id, 0, 20, false)
	s.mobGiveEffect(e, id, 1, 20, false)
	s.clock.count = 20
	s.mobEffectTickStep()
	if _, ok := e.Effects[id]; ok {
		t.Fatal("equal-duration effect survived expiry")
	}
	if _, ok := e.AttrMods["movement_speed"]["minecraft:effect.speed"]; ok {
		t.Fatal("equal-duration modifier survived expiry")
	}
}
