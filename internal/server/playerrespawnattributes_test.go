package server

import (
	"math"
	"testing"

	"mc-go/internal/world"
)

func TestPlayerRespawnAttributeNewBody(t *testing.T) {
	s := simulationTestServer(t)
	sess := entityViewer(t, s, 776, "BodyAttrs")
	s.simulationAction(func() {
		e, _ := s.entities.GetPtr(sess.entityID.Load())
		e.AttrBase = map[string]float64{"movement_speed": .3, "max_health": 30, "luck": 10}
		e.AttrMods = map[string]map[string]attrMod{"luck": {"test:permanent": {value: .25, permanent: true}}}
		e.playerEquipmentAttrsInit, e.playerArmorAttrsInit = true, true
		sess.walkingSpeed, sess.walkingSpeedSet = .3, true
		sess.effects = map[int32]mobEffectInst{effectGlowing: {expiry: 100}, effectAbsorption: {expiry: 100}, effectLevitation: {expiry: 100}}
		sess.absorb = 4
		sess.levitationStart, sess.levitationStartTick = []float64{1, 2, 3}, 50
		sess.raidOmenPos = new(world.Pos)
		if got := s.resetPlayerBodyAttributes(sess); got != 30 {
			t.Fatalf("health %v", got)
		}
		if len(e.AttrMods) != 0 || len(sess.effects) != 0 {
			t.Fatal("old modifiers/effects survived")
		}
		if sess.absorb != 0 || sess.levitationStart != nil || sess.levitationStartTick != 0 || sess.raidOmenPos != nil {
			t.Fatal("old effect-dependent body state survived")
		}
		if e.AttrBase["movement_speed"] != .3 || e.AttrBase["luck"] != 10 {
			t.Fatal("raw bases changed")
		}
		if e.playerEquipmentAttrsInit || e.playerArmorAttrsInit {
			t.Fatal("equipment scan not reset")
		}
		if sess.playerWalkingSpeed() != .1 {
			t.Fatal("ability constructor default not restored")
		}
		if !e.AttrsDirty["luck"] || !e.AttrsDirty["max_health"] || !e.AttrsDirty["movement_speed"] {
			t.Fatal("new-body attribute instances not dirtied")
		}
		for _, tc := range []struct {
			base float64
			want float32
		}{{math.NaN(), 1}, {-4, 1}, {math.Inf(1), 1024}, {2048, 1024}} {
			e.AttrBase["max_health"] = tc.base
			if got := s.resetPlayerBodyAttributes(sess); got != tc.want {
				t.Fatalf("base %v health %v want %v", tc.base, got, tc.want)
			}
		}
	})
}
