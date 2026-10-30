package server

import (
	"math"
	"testing"
)

func TestPlayerHealingUsesCanonicalMaximum(t *testing.T) {
	s := simulationTestServer(t)
	sess := entityViewer(t, s, 776, "HealthGetter")
	s.simulationAction(func() {
		e, _ := s.entities.GetPtr(sess.entityID.Load())
		e.AttrBase = map[string]float64{"max_health": 30}
		e.AttrMods = map[string]map[string]attrMod{"max_health": {"minecraft:effect.health_boost": {value: 8, permanent: true}}}
		sess.health = 30
		s.applyInstantEffectScaled(sess, effectInstantHealth, 0, 1)
		if sess.health != 34 {
			t.Fatalf("partial heal %v", sess.health)
		}
		// An explicit same-ID override is canonical, not recomputed from an effect.
		e.AttrMods["max_health"]["minecraft:effect.health_boost"] = attrMod{value: 4, permanent: true}
		sess.healPlayer(100)
		if sess.health != 34 {
			t.Fatalf("override cap %v", sess.health)
		}
		delete(e.AttrMods, "max_health")
		for _, c := range []struct {
			base float64
			want float32
		}{{10.5, 10.5}, {math.NaN(), 1}, {math.Inf(1), 1024}, {-5, 1}} {
			e.AttrBase["max_health"] = c.base
			sess.health = 1
			sess.healPlayer(2048)
			if sess.health != c.want {
				t.Fatalf("base%v health%v want%v", c.base, sess.health, c.want)
			}
		}
		sess.health = 0
		sess.healPlayer(10)
		if sess.health != 0 {
			t.Fatal("healing revived dead body")
		}
	})
}

func TestPlayerHealthRefreshAndPresenceBoundary(t *testing.T) {
	s := simulationTestServer(t)
	sess := placeSession(t, s, "HealthFrame", .5, -60, .5)
	avatar, neutral := admitSharedAvatar(t, s, "HealthPresence")
	// Close schedules owned retirement. Wait for that state transition before
	// placeSession's synchronous teardown touches shared player publication.
	t.Cleanup(func() {
		_ = avatar.Close()
		awaitPlayerAction(t, func() bool {
			retired := false
			s.simulationAction(func() { retired = s.findPlayerByIDOwned(neutral.ID) == nil })
			return retired
		})
	})
	s.simulationAction(func() {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("player"), Dim: dimOverworld, Living: true, Health: 20, MaxHealth: 20, AttrBase: map[string]float64{"max_health": 30}})
		sess.setEntityID(e.ID)
		sess.health = 38
		// The write itself does not perform a living attribute refresh.
		if sess.health != 38 {
			t.Fatal("health changed before living refresh")
		}
		other, _ := s.entities.GetPtr(neutral.EntityID)
		other.Health = 20
		other.AttrBase = map[string]float64{"max_health": 1}
		s.playerTickOwned()
		if sess.health != 30 {
			t.Fatalf("frame cap %v", sess.health)
		}
		if !sess.healthDirty.Load() {
			t.Fatal("clamped health not queued for projection")
		}
		if other.Health != 20 {
			t.Fatal("presence-only avatar ran survival health simulation")
		}
	})
}
