//go:build blackbox

// Blackbox parity pin for the bee sting + anger metadata beats on
// the 26.2 decompile. Provocation arms NeutralMob's
// DATA_ANGER_END_TIME (idx19 varlong emit of the absolute end tick);
// Bee.customServerAiStep's setRolling rides DATA_FLAGS_ID (idx18)
// bit 0x02 while the angry un-stung bee sits inside 2 blocks of its
// target; Bee.doHurtTarget then flips bit 0x04 (hasStung) and
// stopBeingAngry emits idx19 -1 — vanilla packs the last two into a
// single 18/19 packet. All of these emit only on the dirty gate.
package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestBeeStingParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type beeObs struct {
		stungSeen  bool
		rollSeen   bool
		angerArmed bool
		angerClr   bool
		hpEnd      float64
	}

	run := func(t *testing.T, name, addr string) beeObs {
		o := beeObs{}
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
		sendCommand(t, v.pwa, "kill @e[type=minecraft:bee]")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "tp BOATA 10.9 -60 8.5")
		sendCommand(t, v.pwa, "effect clear BOATA")
		bee := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"bee", 8.5, -60, 8.5, "{}")
		if bee == 0 {
			t.Fatalf("%s: no bee spawn packet", name)
		}
		beeStr := strconv.Itoa(int(bee))

		fold := func(wo vehObs) {
			for _, f := range wo.beeFlHist {
				if !strings.HasPrefix(f, beeStr+":") {
					continue
				}
				bv, _ := strconv.Atoi(strings.SplitN(f, ":", 2)[1])
				if bv&0x04 != 0 {
					o.stungSeen = true
				}
				if bv&0x02 != 0 {
					o.rollSeen = true
				}
			}
			for _, f := range wo.angerHist {
				if !strings.HasPrefix(f, beeStr+":") {
					continue
				}
				av, _ := strconv.ParseInt(strings.SplitN(f, ":", 2)[1], 10, 64)
				if av > 0 {
					o.angerArmed = true
				} else if av < 0 {
					o.angerClr = true
				}
			}
		}

		// Provoke the faithful way — a real punch. NeutralMob
		// provocation arms AngryAt + AngryUntil (idx19 emit), the
		// angry bee closes in rolling (idx18 0x02), then stings:
		// idx18 0x04 + the idx19 -1 clear in one packet.
		sendAttackEntity(v.pwa, bee)
		time.Sleep(500 * time.Millisecond)
		sendAttackEntity(v.pwa, bee)
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			fold(vehWatch(t, v, false, 0, 500*time.Millisecond))
			if o.stungSeen && o.rollSeen && o.angerArmed && o.angerClr {
				break
			}
		}
		_, o.hpEnd = dataGetSel(t, sink, pwc, "BOATA", "Health")
		return o
	}

	check := func(t *testing.T, name string, o beeObs) {
		if !o.stungSeen {
			t.Fatalf("%s: never saw idx18 0x04 (stung) on the bee", name)
		}
		if !o.rollSeen {
			t.Fatalf("%s: never saw idx18 0x02 (rolling) on the bee", name)
		}
		if !o.angerArmed {
			t.Fatalf("%s: never saw idx19 armed (>0) on the bee", name)
		}
		if !o.angerClr {
			t.Fatalf("%s: never saw idx19 clear (-1) after the sting", name)
		}
		if o.hpEnd >= 20 {
			t.Fatalf("%s: BOATA lost no health to the sting (hpEnd=%v)", name, o.hpEnd)
		}
		t.Logf("%s: stung=%v roll=%v armed=%v cleared=%v hpEnd=%v",
			name, o.stungSeen, o.rollSeen, o.angerArmed, o.angerClr, o.hpEnd)
	}

	check(t, "vanilla", run(t, "vanilla", targets.vanilla))
	check(t, "mc-go", run(t, "mc-go", targets.mcgo))
}
