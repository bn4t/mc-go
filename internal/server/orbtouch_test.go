package server

import "testing"

func TestOrbPlayerTouchFrameChoiceAndCooldown(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	p := placeSession(t, s, "OrbTouch", .5, 162, .5)
	p.health = 20
	p.spawned.Store(true)
	p.setMode(GameModeCreative)
	a := s.entities.Spawn(&Entity{Type: mustEntityType("experience_orb"), Dim: dimOverworld, X: 1, Y: 162.5, Z: .5, OrbValue: 3, OrbCount: 2, NoGravity: true})
	b := s.entities.Spawn(&Entity{Type: mustEntityType("experience_orb"), Dim: dimOverworld, X: .5, Y: 162.5, Z: 1, OrbValue: 3, OrbCount: 2, NoGravity: true})
	s.playerItemTouchTick(p)
	if a.OrbCount+b.OrbCount != 3 || p.takeXpDelay != 2 {
		t.Fatalf("one player frame counts%d/%d delay%d", a.OrbCount, b.OrbCount, p.takeXpDelay)
	}
	s.playerItemTouchTick(p)
	if a.OrbCount+b.OrbCount != 3 {
		t.Fatal("cooldown allowed another take")
	}
	s.hungerTickStep()
	s.playerItemTouchTick(p)
	if a.OrbCount+b.OrbCount != 3 || p.takeXpDelay != 1 {
		t.Fatal("first player tick did not retain cooldown")
	}
	s.hungerTickStep()
	s.playerItemTouchTick(p)
	if a.OrbCount+b.OrbCount != 2 || p.takeXpDelay != 2 {
		t.Fatalf("second eligible frame counts%d/%d delay%d", a.OrbCount, b.OrbCount, p.takeXpDelay)
	}
}

func TestOrbPlayerTouchUsesOrbBoxAndPlayerGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		x      float64
		mode   byte
		health float32
		want   bool
	}{{"orb_reach", 2.04, GameModeCreative, 20, true}, {"outside", 2.06, GameModeCreative, 20, false}, {"spectator", 1, GameModeSpectator, 20, false}, {"dead", 1, GameModeSurvival, 0, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			p := placeSession(t, s, "OrbTouch", .5, 162, .5)
			p.health = tc.health
			p.spawned.Store(true)
			p.setMode(tc.mode)
			e := s.entities.Spawn(&Entity{Type: mustEntityType("experience_orb"), Dim: dimOverworld, X: tc.x, Y: 162.5, Z: .5, OrbValue: 1, OrbCount: 1, NoGravity: true})
			s.playerItemTouchTick(p)
			_, exists := s.entities.GetPtr(e.ID)
			if exists == tc.want {
				t.Fatalf("orb exists%t want%t", exists, !tc.want)
			}
		})
	}
}
