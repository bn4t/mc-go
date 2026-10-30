package server

import "testing"

func TestMobInvulnerabilitySourceExceptions(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	player := &Session{}
	player.entityID.Store(7)
	s.sessions.Store("attacker", player)
	defer s.sessions.Delete("attacker")
	victim := &Entity{Living: true, Invulnerable: true}
	for _, tc := range []struct {
		name, kind string
		mode       byte
		info       *damageInfo
		blocked    bool
	}{
		{"no source", "generic", GameModeSurvival, nil, true},
		{"kill bypass", "generic_kill", GameModeSurvival, nil, false},
		{"void bypass", "out_of_world", GameModeSurvival, nil, false},
		{"survival cause", "player_attack", GameModeSurvival, &damageInfo{cause: 7}, true},
		{"creative cause", "player_attack", GameModeCreative, &damageInfo{cause: 7}, false},
		{"spectator cause", "player_attack", GameModeSpectator, &damageInfo{cause: 7}, true},
		{"direct fallback", "generic", GameModeCreative, &damageInfo{direct: 7}, false},
		{"responsible mob", "generic", GameModeCreative, &damageInfo{cause: 8, direct: 7}, true},
	} {
		player.setMode(tc.mode)
		if got := s.mobInvulnerableTo(victim, tc.kind, tc.info); got != tc.blocked {
			t.Fatal(tc.name, got, tc.blocked)
		}
	}
	victim.Invulnerable = false
	if s.mobInvulnerableTo(victim, "generic", nil) {
		t.Fatal("ordinary target blocked")
	}
}

func TestMobInvulnerabilityRejectsBeforeHurt(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	victim := s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Living: true, Invulnerable: true, Health: 100, MaxHealth: 100})
	credit := &Session{}
	credit.setMode(GameModeCreative)
	dealt, graced, died := s.damageMob(victim, 5, "generic", credit, &damageInfo{typeName: "generic", cause: 8})
	if dealt != 0 || !graced || died || victim.Health != 100 {
		t.Fatal(dealt, graced, died, victim.Health)
	}
}
