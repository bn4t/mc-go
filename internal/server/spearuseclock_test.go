package server

import (
	"mc-go/internal/world"
	"testing"
)

func spearClockScene(t *testing.T) (*Server, *Session) {
	t.Helper()
	s := simulationTestServer(t)
	s.GameMode = GameModeSurvival
	sess, _ := enforceSession(t, s, "SpearClock")
	sess.setPos(.5, -60.4125, .8, 0, 0, true)
	id, _ := world.ItemID("wooden_spear")
	sess.inv[slotHotbarStart] = ItemStack{ID: id, Count: 1}
	sess.lastDelta = [3]float64{0, 0, .3}
	s.spearStart(sess, id, "wooden_spear")
	return s, sess
}

func spearClockVictim(s *Server) *Entity {
	return s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Dim: dimOverworld, Living: true, NoAI: true, X: .5, Y: -59.5, Z: 4.5, Health: 100, MaxHealth: 100, AttrBase: map[string]float64{"knockback_resistance": 1}})
}

func TestSpearUseClockDelayWithoutSimulation(t *testing.T) {
	s, sess := spearClockScene(t)
	victim := spearClockVictim(s)
	before := s.TickCount()
	for i := 0; i < 15; i++ {
		s.spearJabTick(sess)
	}
	if victim.Health != 100 {
		t.Fatal("cold use damaged target", victim.Health)
	}
	s.spearJabTick(sess)
	if victim.Health != 95 || s.TickCount() != before {
		t.Fatal("active player use", victim.Health, s.TickCount(), before)
	}
}

func TestSpearContactClockDoesNotFollowUseAge(t *testing.T) {
	s, sess := spearClockScene(t)
	victim := spearClockVictim(s)
	sess.lastDelta = [3]float64{}
	for i := 0; i < 16; i++ {
		s.spearJabTick(sess)
	}
	if _, present := sess.stabbed[victim.ID]; !present || victim.Health != 100 {
		t.Fatal("remembered ineligible contact", present, victim.Health)
	}
	sess.lastDelta = [3]float64{0, 0, .3}
	for i := 0; i < 20; i++ {
		s.spearJabTick(sess)
	}
	if victim.Health != 100 {
		t.Fatal("player-use age expired level-time cooldown", victim.Health)
	}
	s.clock.mu.Lock()
	s.clock.count += 10
	s.clock.mu.Unlock()
	s.spearJabTick(sess)
	if victim.Health != 95 {
		t.Fatal("simulation-time contact cooldown did not expire", victim.Health)
	}
}

func TestSpearUseClockDamageWindow(t *testing.T) {
	for _, tc := range []struct {
		name   string
		passes int
		health float32
	}{{"last eligible", 315, 95}, {"expired", 316, 100}} {
		t.Run(tc.name, func(t *testing.T) {
			s, sess := spearClockScene(t)
			for i := 0; i < tc.passes; i++ {
				s.spearJabTick(sess)
			}
			victim := spearClockVictim(s)
			s.spearJabTick(sess)
			if victim.Health != tc.health {
				t.Fatal(victim.Health, tc.health)
			}
		})
	}
}
