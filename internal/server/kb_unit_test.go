package server

import (
	"testing"
)

func TestKnockbackMotionPacket(t *testing.T) {
	s := New("unused")
	s.GameMode = GameModeSurvival
	ca, pra, _ := joinPlay(t, s, "Alice")
	defer ca.Close()
	drainJoinBurst(t, pra)
	cb, prb, _ := joinPlay(t, s, "Bob")
	defer cb.Close()
	drainJoinBurst(t, prb)
	drainToQuiet(t, prb)
	alice := s.findSessionByName("Alice")
	bob := s.findSessionByName("Bob")
	bob.setPos(4, -60, 2, -90, 0, true)
	alice.setPos(6, -60, 2, 0, 0, true)
	bob.attackTicker = 200
	bob.sprinting = true
	s.handleAttack(bob, attackPacket(t, alice.entityID.Load()))
	var motion, flags bool
	for i := 0; i < 256; i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			break
		}
		if p.ID == 0x65 {
			t.Logf("motion: %x", p.Data)
			motion = true
		}
		if p.ID == 0x63 {
			t.Logf("flags: %x", p.Data)
			flags = true
		}
	}
	if !motion {
		t.Fatal("no set_entity_motion to victim")
	}
	if !flags {
		t.Fatal("no shared-flags entity_data on sprint reset")
	}
	if bob.sprinting {
		t.Fatal("attacker still sprinting after sprint hit")
	}
}
