// Blackbox parity pin for the Ravager (26.2 jar, monster/Ravager;
// javap -c):
//
//  1. Melee — MeleeAttackGoal's first swing lands Ravager.doHurtTarget:
//     attackTick = 10, entity event 4, entity.ravager.attack, then the
//     12.0 attack_damage hit (normal difficulty: unscaled).
//  2. Leaves — a ravager driving into a 2-high persistent oak_leaves
//     ring around its target's pillar breaks the leaves its box
//     (inflated 0.2) touches: aiStep's horizontalCollision arm under
//     mob_griefing, ServerLevel.destroyBlock → block_update to air.
//  3. The same ring with mob_griefing=false stays whole.
package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestRavagerParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		attackEvent bool
		attackSound bool
		drop        float64
		broken      int
		brokenOff   int
	}
	leafRing := func(cx, cz int) []string {
		// A 2-high ring of persistent leaves at Chebyshev distance 3
		// from the pillar (cx, cz).
		var cmds []string
		for _, seg := range [][4]int{
			{cx - 3, cz - 3, cx + 3, cz - 3}, {cx - 3, cz + 3, cx + 3, cz + 3},
			{cx - 3, cz - 3, cx - 3, cz + 3}, {cx + 3, cz - 3, cx + 3, cz + 3},
		} {
			cmds = append(cmds, fmt.Sprintf("fill %d -60 %d %d -59 %d minecraft:oak_leaves[persistent=true]",
				seg[0], seg[1], seg[2], seg[3]))
		}
		return cmds
	}
	countBroken := func(lg *hostileLog) int {
		seen := map[[3]int32]bool{}
		for _, e := range lg.events {
			if e.kind == "block" && e.block == 0 && (e.bpos[1] == -60 || e.bpos[1] == -59) {
				seen[e.bpos] = true
			}
		}
		return len(seen)
	}
	run := func(t *testing.T, name, addr string) obs {
		var o obs
		v := joinVehPair(t, addr)
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "ravager", v.ca, v.pra, v.pwa, 0)
		_ = newChatSinkTP(prc, pwc)
		tp := startHostileTap(v)
		for _, p := range []string{"BOATA", "BOATB", "BOATC"} {
			sendCommand(t, v.pwa, "gamemode spectator "+p)
		}
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		sendCommand(t, v.pwa, "tp BOATB 40.5 -60 40.5")
		sendCommand(t, v.pwa, "tp BOATC 40.5 -60 42.5")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 16.5")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		hostileWatch(t, tp, nil, 800*time.Millisecond)

		// 1 — the melee hit.
		lg := newHostileLog()
		sendCommand(t, v.pwa, "summon minecraft:ravager 8.5 -60 8.5")
		hostileWatchUntil(t, tp, lg, 20*time.Second, func(l *hostileLog) bool {
			return len(l.healthDrops(20)) > 0
		})
		sendCommand(t, v.pwa, "kill @e[type=ravager]")
		hostileWatch(t, tp, lg, 300*time.Millisecond)
		rv := lg.firstEid("ravager")
		o.attackEvent = len(lg.entityEvents(rv, 4)) > 0
		o.attackSound = len(lg.sounds("entity.ravager.attack")) > 0
		if d := lg.healthDrops(20); len(d) > 0 {
			o.drop = d[0]
		}
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "effect give BOATA minecraft:instant_health 1 10")
		hostileWatch(t, tp, nil, 600*time.Millisecond)

		// 2 / 3 — the leaf ring, griefing on then off. BOATA stands on
		// a 3-high pillar inside the ring: reachable by line of sight,
		// not by path.
		for leg, griefing := range []bool{true, false} {
			cx, cz := 8, 20
			if leg == 1 {
				cx = 24
			}
			sendCommand(t, v.pwa, fmt.Sprintf("gamerule mob_griefing %v", griefing))
			for _, c := range leafRing(cx, cz) {
				sendCommand(t, v.pwa, c)
			}
			sendCommand(t, v.pwa, fmt.Sprintf("fill %d -60 %d %d -58 %d minecraft:stone", cx, cz, cx, cz))
			sendCommand(t, v.pwa, fmt.Sprintf("tp BOATA %d.5 -57 %d.5", cx, cz))
			sendCommand(t, v.pwa, "gamemode survival BOATA")
			hostileWatch(t, tp, nil, 600*time.Millisecond)
			lg = newHostileLog()
			sendCommand(t, v.pwa, fmt.Sprintf("summon minecraft:ravager %d.5 -60 %d.5", cx, cz-9))
			if griefing {
				hostileWatchUntil(t, tp, lg, 20*time.Second, func(l *hostileLog) bool { return countBroken(l) >= 4 })
				hostileWatch(t, tp, lg, 500*time.Millisecond)
				o.broken = countBroken(lg)
			} else {
				hostileWatch(t, tp, lg, 8*time.Second)
				o.brokenOff = countBroken(lg)
			}
			sendCommand(t, v.pwa, "kill @e[type=ravager]")
			sendCommand(t, v.pwa, "gamemode spectator BOATA")
			hostileWatch(t, tp, nil, 400*time.Millisecond)
		}
		sendCommand(t, v.pwa, "gamerule mob_griefing true")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		return o
	}
	check := func(t *testing.T, name string, o obs) {
		t.Logf("%s: %+v", name, o)
		if !o.attackEvent || !o.attackSound {
			t.Errorf("%s: attack event4=%v sound=%v, want both", name, o.attackEvent, o.attackSound)
		}
		if !approxEq(o.drop, 12, 0.01) {
			t.Errorf("%s: first ravager hit drops %.2f, want 12", name, o.drop)
		}
		if o.broken < 4 {
			t.Errorf("%s: leaves broken under mob_griefing = %d, want >=4", name, o.broken)
		}
		if o.brokenOff != 0 {
			t.Errorf("%s: leaves broken with mob_griefing=false = %d, want 0", name, o.brokenOff)
		}
	}
	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vo)
	check(t, "mc-go", mo)
}
