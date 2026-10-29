// Blackbox parity pin for the Vex (26.2 jar, monster/Vex +
// Vex$VexChargeAttackGoal / VexMoveControl / VexRandomMoveGoal /
// VexCopyOwnerTargetGoal, javap -c):
//
//   - Vex.tick runs outside isEffectiveAi: setNoGravity(true) every
//     tick (DATA_NO_GRAVITY, index 5, flips true on the first tick)
//     and the limited-life countdown — life_ticks reaching 0 hurts the
//     vex 1.0 on starve and re-arms 20, so a {life_ticks:5} summon
//     takes exactly two hits in 30 stepped ticks and ends at 12 health.
//   - VexChargeAttackGoal: start latches DATA_FLAGS_ID (16) bit 1 and
//     plays entity.vex.charge; the box-overlap hit (iron sword: 4 base
//   - 5 modifier = 9 on normal) drops the bit again.
package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestVexParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		starveHits  int
		noGrav      bool
		lifeHealth  float64
		chargeOn    bool
		chargeOff   bool
		chargeSound bool
		drops       []float64
	}
	run := func(t *testing.T, name, addr string) obs {
		var o obs
		v := joinVehPair(t, addr)
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "vex", v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)
		tp := startHostileTap(v)
		for _, p := range []string{"BOATA", "BOATB", "BOATC"} {
			sendCommand(t, v.pwa, "gamemode spectator "+p)
		}
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 20.5")
		sendCommand(t, v.pwa, "tp BOATB 8.5 -60 22.5")
		sendCommand(t, v.pwa, "tp BOATC 8.5 -60 24.5")
		hostileWatch(t, tp, newHostileLog(), 800*time.Millisecond)

		// Leg 1 — limited life on a NoAI vex summoned into a frozen
		// game, then stepped: life_ticks 5 → starve hits on stepped
		// ticks 5 and 25, none at 45.
		sendCommand(t, v.pwa, "tick freeze")
		hostileWatch(t, tp, newHostileLog(), 500*time.Millisecond)
		lg := newHostileLog()
		sendCommand(t, v.pwa, "summon minecraft:vex 8.5 -57 8.5 {NoAI:1b,life_ticks:5}")
		hostileWatch(t, tp, lg, 500*time.Millisecond)
		sendCommand(t, v.pwa, "tick step 40")
		hostileWatch(t, tp, lg, 4*time.Second)
		vex := lg.firstEid("vex")
		if vex == 0 {
			t.Fatalf("%s: no vex add_entity", name)
		}
		_, o.lifeHealth = dataGetSel(t, sink, pwc, "@e[type=vex,limit=1]", "Health")
		o.starveHits = len(lg.damages(vex))
		for _, m := range lg.metas(vex, 5) {
			if m.ser == 8 && m.val == 1 {
				o.noGrav = true
			}
		}
		sendCommand(t, v.pwa, "tick unfreeze")
		sendCommand(t, v.pwa, "kill @e[type=vex]")
		hostileWatch(t, tp, newHostileLog(), 600*time.Millisecond)

		// Leg 2 — the charge: a bare-summoned (iron sword) vex a few
		// blocks above and beside a survival BOATA. The noPhysics
		// random move can carry a vex under the floor before its
		// mustSee target acquisition rolls (no line of sight, no
		// target), so a summon that never lands a hit is retried.
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		hostileWatch(t, tp, newHostileLog(), 400*time.Millisecond)
		for attempt := 0; attempt < 3; attempt++ {
			lg = newHostileLog()
			sendCommand(t, v.pwa, "summon minecraft:vex 8.5 -57 16.5")
			if hostileWatchUntil(t, tp, lg, 15*time.Second, func(l *hostileLog) bool {
				return len(l.healthDrops(20)) > 0
			}) {
				break
			}
			sendCommand(t, v.pwa, "kill @e[type=vex]")
			hostileWatch(t, tp, nil, 400*time.Millisecond)
		}
		// A few more ticks for the charge-off emit.
		hostileWatch(t, tp, lg, 400*time.Millisecond)
		vex = lg.firstEid("vex")
		on := false
		for _, m := range lg.metas(vex, 16) {
			if m.val&1 == 1 {
				on = true
				o.chargeOn = true
			} else if on {
				o.chargeOff = true
			}
		}
		o.chargeSound = len(lg.sounds("entity.vex.charge")) > 0
		o.drops = lg.healthDrops(20)
		if !o.chargeSound {
			var ids []int
			for _, e := range lg.events {
				if e.kind == "sound" {
					ids = append(ids, e.idx)
				}
			}
			cid, _ := server.SoundEventID("entity.vex.charge")
			t.Logf("%s: sounds seen %v (charge=%d) vex pos %v", name, ids, cid+1, lg.pos[vex])
		}
		sendCommand(t, v.pwa, "kill @e[type=vex]")
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		hostileWatch(t, tp, newHostileLog(), 400*time.Millisecond)
		return o
	}
	check := func(t *testing.T, name string, o obs) {
		if o.starveHits != 2 {
			t.Errorf("%s: starve hits in 40 stepped ticks = %d, want 2 (ticks 5, 25)", name, o.starveHits)
		}
		if !o.noGrav {
			t.Errorf("%s: vex never emitted DATA_NO_GRAVITY=true", name)
		}
		if !approxEq(o.lifeHealth, 12, 0.01) {
			t.Errorf("%s: vex health after two starve hits = %v, want 12", name, o.lifeHealth)
		}
		if !o.chargeOn || !o.chargeSound {
			t.Errorf("%s: charge flag on=%v sound=%v, want both", name, o.chargeOn, o.chargeSound)
		}
		if !o.chargeOff {
			t.Errorf("%s: charge flag never cleared after the hit", name)
		}
		if len(o.drops) == 0 || !approxEq(o.drops[0], 9, 0.01) {
			t.Errorf("%s: first vex hit drops %v, want 9", name, o.drops)
		}
		t.Logf("%s: %+v", name, o)
	}
	_ = server.IDPlaySetHealth
	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vo)
	check(t, "mc-go", mo)
}
