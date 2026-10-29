// Blackbox parity pin for AbstractCubeMob.push → dealDamage on an
// iron golem (26.2 jar, monster/cubemob/AbstractCubeMob; javap -c):
// when the golem's pushEntities shoves an isDealsDamage cube, the
// cube's push(golem) runs dealDamage — isWithinMeleeAttackRange +
// hasLineOfSight, then mob_attack at getAttackDamage (a size-2 slime:
// 2.0) plus entity.slime.attack. A NoAI golem still pushes
// (pushEntities sits outside isEffectiveAi); the slime targets it
// through its IronGolem NearestAttackableTargetGoal and hops in.
package blackbox

import (
	"math"
	"testing"
	"time"
)

func TestCubeGolemPushParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		hits     int
		health   float64
		attackSd bool
	}
	run := func(t *testing.T, name, addr string) obs {
		var o obs
		v := joinVehPair(t, addr)
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "cubepush", v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)
		tp := startHostileTap(v)
		for _, p := range []string{"BOATA", "BOATB", "BOATC"} {
			sendCommand(t, v.pwa, "gamemode spectator "+p)
		}
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -58 18.5")
		sendCommand(t, v.pwa, "tp BOATB 8.5 -58 20.5")
		sendCommand(t, v.pwa, "tp BOATC 8.5 -58 22.5")
		hostileWatch(t, tp, nil, 800*time.Millisecond)

		lg := newHostileLog()
		sendCommand(t, v.pwa, "summon minecraft:iron_golem 8.5 -60 8.5 {NoAI:1b}")
		sendCommand(t, v.pwa, "summon minecraft:slime 8.5 -60 12.5 {Size:1}")
		hostileWatchUntil(t, tp, lg, 25*time.Second, func(l *hostileLog) bool {
			g := l.firstEid("iron_golem")
			return g != 0 && len(l.damages(g)) >= 2
		})
		g := lg.firstEid("iron_golem")
		o.hits = len(lg.damages(g))
		o.attackSd = len(lg.sounds("entity.slime.attack")) > 0
		sendCommand(t, v.pwa, "kill @e[type=slime]")
		hostileWatch(t, tp, lg, 300*time.Millisecond)
		_, o.health = dataGetSel(t, sink, pwc, "@e[type=iron_golem,limit=1]", "Health")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		hostileWatch(t, tp, nil, 300*time.Millisecond)
		return o
	}
	check := func(t *testing.T, name string, o obs) {
		t.Logf("%s: %+v", name, o)
		if o.hits < 2 || !o.attackSd {
			t.Errorf("%s: golem hits=%d slime.attack=%v, want >=2 and the sound", name, o.hits, o.attackSd)
		}
		// Every hit is the size-2 slime's 2.0 — the golem's health
		// sits on an even number below 100.
		lost := 100 - o.health
		if lost < 3.99 || math.Abs(lost/2-math.Round(lost/2)) > 1e-3 {
			t.Errorf("%s: golem health %.2f, want 100 - 2k (k >= 2)", name, o.health)
		}
	}
	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vo)
	check(t, "mc-go", mo)
}
