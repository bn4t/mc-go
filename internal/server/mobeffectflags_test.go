package server

import "testing"

func TestMobWeakerDefaultEffectFlags(t *testing.T) {
	s := simulationTestServer(t)
	e := &Entity{Type: mustEntityType("villager"), Living: true, Effects: map[int32]mobEffectInst{effectSpeed: {amplifier: 1, expiry: 200, flags: 9, flagsSet: true, hide: true}}}
	s.simulationAction(func() {
		if !s.mobGiveEffect(e, effectSpeed, 0, 20, false) {
			t.Fatal("weaker visible dose did not update scalar flags")
		}
		got := e.Effects[effectSpeed]
		if got.amplifier != 1 || got.expiry != 200 || got.flags != 6 || got.hide {
			t.Fatalf("visible weaker merge%+v", got)
		}
		if !s.mobGiveEffect(e, effectSpeed, 0, 20, true) {
			t.Fatal("weaker hidden dose did not update scalar flags")
		}
		if got = e.Effects[effectSpeed]; got.flags != 0 || !got.hide {
			t.Fatalf("hidden weaker merge%+v", got)
		}
		if s.mobGiveEffectFlags(e, effectSpeed, 0, 400, 9) {
			t.Fatal("declined ambient-only weaker dose reported scalar update")
		}
		got = e.Effects[effectSpeed]
		if got.flags != 0 || got.hidden == nil || got.hidden.flags != 9 || got.hidden.expiry != 400 {
			t.Fatalf("hidden custom queue%+v", got)
		}
	})
}
