// Blackbox parity pin for the Spider (26.2 jar, monster/spider/Spider
// + Spider$SpiderTargetGoal / SpiderAttackGoal, ai/goal/
// LeapAtTargetGoal, LivingEntity.handleRelativeFrictionAndCalculate-
// Movement; javap -c), at midnight so SpiderTargetGoal's brightness
// gate is open:
//
//  1. Climb — a spider chasing a player standing on a 3-high plateau
//     walks into the wall: Spider.tick's setClimbing(horizontal-
//     Collision) raises DATA_FLAGS_ID (16) bit 0, onClimbable turns the
//     collision into a 0.2 climb, and the spider tops the wall.
//  2. Leap — on flat ground LeapAtTargetGoal fires inside distSqr
//     [4, 16]: delta.y = 0.4 (a set_entity_motion with a clear upward
//     component — nothing else lifts a spider on flat ground) with the
//     0.4 horizontal push toward the target.
package blackbox

import (
	"math"
	"testing"
	"time"
)

func TestSpiderClimbLeapParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		climbFlag  bool
		climbClear bool
		climbTopY  float64
		leapVy     float64
		leapHoriz  float64
	}
	run := func(t *testing.T, name, addr string) obs {
		var o obs
		v := joinVehPair(t, addr)
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "spider", v.ca, v.pra, v.pwa, 0)
		_ = newChatSinkTP(prc, pwc)
		tp := startHostileTap(v)
		for _, p := range []string{"BOATA", "BOATB", "BOATC"} {
			sendCommand(t, v.pwa, "gamemode spectator "+p)
		}
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "gamerule do_daylight_cycle false")
		sendCommand(t, v.pwa, "time set 18000")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		// The plateau: x 12..16, z 4..12, y -60..-58 — its top at -57.
		sendCommand(t, v.pwa, "fill 12 -60 4 16 -58 12 minecraft:stone")
		sendCommand(t, v.pwa, "tp BOATB 30.5 -60 30.5")
		sendCommand(t, v.pwa, "tp BOATC 30.5 -60 32.5")
		sendCommand(t, v.pwa, "tp BOATA 15.5 -57 8.5")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		hostileWatch(t, tp, nil, 800*time.Millisecond)

		// 1 — climb.
		lg := newHostileLog()
		sendCommand(t, v.pwa, "summon minecraft:spider 4.5 -60 8.5")
		hostileWatchUntil(t, tp, lg, 20*time.Second, func(l *hostileLog) bool {
			sp := l.firstEid("spider")
			return sp != 0 && l.maxY[sp] >= -57.05
		})
		hostileWatch(t, tp, lg, 1500*time.Millisecond)
		sp := lg.firstEid("spider")
		on := false
		for _, m := range lg.metas(sp, 16) {
			if m.val&1 == 1 {
				on, o.climbFlag = true, true
			} else if on {
				o.climbClear = true
			}
		}
		o.climbTopY = lg.maxY[sp]
		sendCommand(t, v.pwa, "kill @e[type=spider]")
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "fill 12 -60 4 16 -58 12 minecraft:air")
		hostileWatch(t, tp, nil, 600*time.Millisecond)

		// 2 — leap on flat ground.
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 24.5")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		hostileWatch(t, tp, nil, 500*time.Millisecond)
		lg = newHostileLog()
		sendCommand(t, v.pwa, "summon minecraft:spider 8.5 -60 14.5")
		hostileWatchUntil(t, tp, lg, 20*time.Second, func(l *hostileLog) bool {
			sp := l.firstEid("spider")
			for _, e := range l.events {
				if e.kind == "motion" && e.eid == sp && e.xyz[1] > 0.15 {
					return true
				}
			}
			return false
		})
		sp = lg.firstEid("spider")
		for _, e := range lg.events {
			if e.kind == "motion" && e.eid == sp && e.xyz[1] > o.leapVy {
				o.leapVy = e.xyz[1]
				o.leapHoriz = math.Hypot(e.xyz[0], e.xyz[2])
			}
		}
		sendCommand(t, v.pwa, "kill @e[type=spider]")
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		hostileWatch(t, tp, nil, 400*time.Millisecond)
		return o
	}
	check := func(t *testing.T, name string, o obs) {
		t.Logf("%s: %+v", name, o)
		if !o.climbFlag || !o.climbClear {
			t.Errorf("%s: climb flag set=%v cleared=%v, want both", name, o.climbFlag, o.climbClear)
		}
		if o.climbTopY < -57.05 {
			t.Errorf("%s: spider topped out at y=%.3f, want the -57 plateau", name, o.climbTopY)
		}
		// The leap's emitted delta: 0.4 lifted, decayed by at most a
		// few tracker ticks of gravity·drag before the send.
		if o.leapVy < 0.15 || o.leapVy > 0.42 || o.leapHoriz < 0.15 {
			t.Errorf("%s: leap motion vy=%.3f horiz=%.3f, want a 0.4 lift with a >=0.15 push", name, o.leapVy, o.leapHoriz)
		}
	}
	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vo)
	check(t, "mc-go", mo)
}
