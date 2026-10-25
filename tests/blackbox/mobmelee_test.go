// Blackbox parity pin for the hostile melee attack path measured on
// the 26.2 decompile: MeleeAttackGoal ticks on a 20-game-tick canUse
// gate, approaches at speedModifier*movement_speed, and attacks when
// the mob's box inflated by DEFAULT_ATTACK_REACH (sqrt(2.04)-0.6)
// intersects the target's hitbox — swing animation + doHurtTarget on
// a 20-tick adjusted cooldown. ZombieAttackGoal runs the same base
// plus raiseArmTicks: the DATA_MOB_FLAGS aggressive bit (idx15 bit2)
// lights 5 ticks into the run only while the post-attack cooldown
// sits under half the interval, so the byte pulses 0<->4 across each
// swing cycle. The victim's hurtServer shoves it 0.4 away from the
// attacker — the player's own set_entity_motion shows the ~0.3 vy.
// Sources: MeleeAttackGoal.java, ZombieAttackGoal.java, Mob.java
// (isWithinMeleeAttackRange + doHurtTarget), LivingEntity.knockback.
package blackbox

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// meleeObs is one leg's wire facts for the attack window.
type meleeObs struct {
	flagSet   int // idx15 emits carrying bit2 (aggressive)
	flagClear int // idx15 emits with bit2 cleared
	knockSeen int // set_entity_motion emits with vy > 0.25 on any eid
	hits      int // health decreases observed while polling
	hpEnd     float64
}

func TestMobMeleeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) meleeObs {
		o := meleeObs{}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:zombie]")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "tp BOATA 13.5 -60 8.5")
		sendCommand(t, v.pwa, "tick rate 40")
		sendCommand(t, v.pwa, "effect clear BOATA")
		// A zombie 5 blocks out: inside follow_range 35, outside the
		// ~1.43 melee reach — it has to close before the first swing.
		zom := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"zombie", 8.5, -60, 8.5, "{}")
		if zom == 0 {
			t.Fatalf("%s: no zombie spawn packet", name)
		}

		fold := func(wo vehObs) {
			for _, f := range wo.flagsHist {
				if !strings.HasPrefix(f, strconv.Itoa(int(zom))+":") {
					continue
				}
				bv, _ := strconv.Atoi(strings.SplitN(f, ":", 2)[1])
				if bv&4 != 0 {
					o.flagSet++
				} else {
					o.flagClear++
				}
			}
			for _, vecs := range wo.motVec {
				for _, vec := range vecs {
					// The 0.4 hurt shove: vy spike when grounded, else
					// the horizontal push — either signature beats a
					// walking mob's ~0.23 cruise.
					if vec[1] > 0.25 || math.Hypot(vec[0], vec[2]) > 0.3 {
						o.knockSeen++
					}
				}
			}
		}

		_, hp := dataGetSel(t, sink, pwc, "BOATA", "Health")
		deadline := time.Now().Add(18 * time.Second)
		for time.Now().Before(deadline) {
			fold(vehWatch(t, v, false, 0, 400*time.Millisecond))
			_, h := dataGetSel(t, sink, pwc, "BOATA", "Health")
			if h < hp {
				o.hits++
				hp = h
			}
			if o.hits >= 4 {
				break
			}
		}
		o.hpEnd = hp
		return o
	}

	check := func(t *testing.T, name string, o meleeObs) {
		if o.hits < 3 {
			t.Fatalf("%s: only %d melee hits landed in the window, want >=3", name, o.hits)
		}
		if o.flagSet < 1 {
			t.Fatalf("%s: zombie never emitted the aggressive flag (idx15 bit2)", name)
		}
		if o.flagClear < 1 {
			t.Fatalf("%s: aggressive flag never cleared (the pulse never re-darkened)", name)
		}
		if o.knockSeen < 1 {
			t.Fatalf("%s: no hurt-knockback motion emit on any entity", name)
		}
		t.Logf("%s: hits=%d flagSet=%d flagClear=%d knock=%d hpEnd=%.0f",
			name, o.hits, o.flagSet, o.flagClear, o.knockSeen, o.hpEnd)
	}

	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", vo)
	check(t, "mc-go", mo)
}
