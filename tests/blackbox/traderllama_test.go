// Blackbox parity pin for TraderLlama's extra behaviors over Llama
// (decompile animal/equine/TraderLlama.java + the family goals in
// Llama.registerGoals):
//
//	maybeDespawn (aiStep): despawnDelay — NBT DespawnDelay, ctor
//	47999 — ticks once per aiStep while canDespawn holds (!tamed,
//	!leashed-to-non-trader, !one-player-passenger, !age-locked,
//	!persistence-required); leashed to a wandering_trader it
//	copies the trader's delay-1. At <=0: removeLeash + discard.
//	WanderingTrader's own arm is `despawnDelay > 0 && !isTrading()
//	&& --delay == 0 → discard`.
//
//	targetSelector: the whole family runs LlamaAttackWolfGoal —
//	NearestAttackableTarget(Wolf, mustSee, !isTame) at
//	follow_range×0.25 = 4 blocks — while trader_llama adds
//	NearestAttackableTarget(Zombie, !ZOMBIFIED_PIGLIN) and
//	(AbstractIllager) at the full 16. A held target re-arms
//	RangedAttackGoal's 40-tick attackTime every cycle.
//
//	PanicGoal speedModifier is 2.0 (vs llama's 1.2);
//	doPlayerRide refuses while the leash points at a
//	wandering_trader (the gate is in mountAllowed — not pinned
//	here since a trader leash link can't be summoned directly).
package blackbox

import (
	"testing"
	"time"
)

func TestTraderLlamaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		traderSpits   int  // llama_spit adds with a zombie-family mark
		llamaSpits    int  // same window, plain llama (want 0)
		wolfSpits     int  // trader_llama + wolf
		llamaWolfHits int  // llama + wolf
		despawnGone   bool // {DespawnDelay:60} discarded
		gateAlive     bool // PersistenceRequired survives the delay
		delayRead     bool // DespawnDelay readable and counting
		traderGone    bool // wandering_trader {DespawnDelay:60}
		huskHurt      int  // damage_events on the hunted husk
	}

	run := func(t *testing.T, name, addr string) obs {
		t.Helper()
		o := obs{}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "gamerule randomtickspeed 0")
		clear := func() {
			sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		}
		clear()
		sendCommand(t, v.pwa, "tp BOATA 20.5 -60 14.5")
		sendCommand(t, v.pwa, "tp BOATB 20.5 -60 26.5")

		watch := func(dur time.Duration, fold func(vehObs)) {
			deadline := time.Now().Add(dur)
			for time.Now().Before(deadline) {
				fold(vehWatch(t, v, false, 0, 400*time.Millisecond))
			}
		}

		// Phase 1 — trader_llama hunts a husk (Zombie family, no
		// daylight burn to fight). 40-tick RangedAttack arm + scan
		// cadence: first spit lands ~2-3s out, re-arms every 40t.
		tl := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"trader_llama", 20.5, -60, 20.5, "{}")
		hk := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"husk", 28.5, -60, 20.5, "{}")
		if tl == 0 || hk == 0 {
			t.Fatalf("%s: hunt summon tl=%d husk=%d", name, tl, hk)
		}
		watch(7*time.Second, func(wo vehObs) {
			o.traderSpits += wo.adds["llama_spit"]
			o.huskHurt += len(wo.dmgEv[hk])
		})

		// Phase 2 — plain llama ignores the same husk: the wolf-hunt
		// is the only mob hunt Llama registers.
		clear()
		ll := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"llama", 20.5, -60, 20.5, "{}")
		hk2 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"husk", 28.5, -60, 20.5, "{}")
		if ll == 0 || hk2 == 0 {
			t.Fatalf("%s: control summon ll=%d husk=%d", name, ll, hk2)
		}
		watch(6*time.Second, func(wo vehObs) {
			o.llamaSpits += wo.adds["llama_spit"]
		})

		// Phase 3 — the wolf hunt at follow_range×0.25 = 4 blocks.
		// Untamed wolf and hunter penned into a 3-cell corridor so
		// the pair can never drift out of the radius mid-window
		// (the arm draw is a 1/16 lottery once inside 4).
		sendCommand(t, v.pwa, "fill 18 -60 19 22 -58 19 glass")
		sendCommand(t, v.pwa, "fill 18 -60 21 22 -58 21 glass")
		sendCommand(t, v.pwa, "fill 18 -60 19 18 -58 21 glass")
		sendCommand(t, v.pwa, "fill 22 -60 19 22 -58 21 glass")
		sendCommand(t, v.pwa, "fill 19 -60 20 21 -58 20 air")
		clear()
		tl2 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"trader_llama", 19.5, -60, 20.5, "{}")
		wf := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"wolf", 21.5, -60, 20.5, "{}")
		watch(8*time.Second, func(wo vehObs) {
			o.wolfSpits += wo.adds["llama_spit"]
		})
		clear()
		ll2 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"llama", 19.5, -60, 20.5, "{}")
		wf2 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"wolf", 21.5, -60, 20.5, "{}")
		watch(8*time.Second, func(wo vehObs) {
			o.llamaWolfHits += wo.adds["llama_spit"]
		})
		sendCommand(t, v.pwa, "fill 18 -60 19 22 -58 21 air")
		if tl2 == 0 || wf == 0 || ll2 == 0 || wf2 == 0 {
			t.Fatalf("%s: wolf-hunt summons tl=%d wf=%d ll=%d wf=%d",
				name, tl2, wf, ll2, wf2)
		}

		// Phase 4 — maybeDespawn: {DespawnDelay:60} discards ~3s
		// out; a wandering_trader arm counts identically.
		clear()
		d1 := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"trader_llama", 20.5, -60, 20.5, "{DespawnDelay:60}")
		wt := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"wandering_trader", 24.5, -60, 20.5, "{DespawnDelay:60}")
		gone := map[int32]bool{}
		watch(6*time.Second, func(wo vehObs) {
			for eid := range wo.removed {
				gone[eid] = true
			}
		})
		o.despawnGone = gone[d1]
		o.traderGone = gone[wt]

		// Phase 5 — canDespawn's persistence gate: the same 60-tick
		// delay on a PersistenceRequired llama never counts.
		clear()
		g := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"trader_llama", 20.5, -60, 20.5,
			"{PersistenceRequired:1b,DespawnDelay:60}")
		watch(5*time.Second, func(wo vehObs) {
			for eid := range wo.removed {
				gone[eid] = true
			}
		})
		o.gateAlive = g != 0 && !gone[g]
		if _, d := dataGetSel(t, sink, pwc,
			"@e[type=minecraft:trader_llama,limit=1]", "DespawnDelay"); d > 0 {
			o.delayRead = true
		}
		return o
	}

	want := run(t, "vanilla", targets.vanilla)
	got := run(t, "mc-go", targets.mcgo)

	check := func(name string, o obs) {
		t.Helper()
		if o.traderSpits < 1 {
			t.Errorf("%s: trader_llama fired no spit at the husk", name)
		}
		if o.llamaSpits != 0 {
			t.Errorf("%s: plain llama fired %d spits at the husk (want 0)", name, o.llamaSpits)
		}
		if o.wolfSpits < 1 {
			t.Errorf("%s: trader_llama fired no spit at the wolf", name)
		}
		if o.llamaWolfHits < 1 {
			t.Errorf("%s: llama fired no spit at the wolf", name)
		}
		if !o.despawnGone {
			t.Errorf("%s: {DespawnDelay:60} trader_llama never discarded", name)
		}
		if !o.traderGone {
			t.Errorf("%s: {DespawnDelay:60} wandering_trader never discarded", name)
		}
		if !o.gateAlive {
			t.Errorf("%s: PersistenceRequired trader_llama despawned anyway", name)
		}
		if !o.delayRead {
			t.Errorf("%s: DespawnDelay not readable on the gated llama", name)
		}
		t.Logf("%s: traderSpits=%d llamaSpits=%d wolfSpits=%d llamaWolf=%d huskHurt=%d",
			name, o.traderSpits, o.llamaSpits, o.wolfSpits, o.llamaWolfHits, o.huskHurt)
	}
	check("vanilla", want)
	check("mc-go", got)
}
