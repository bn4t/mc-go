// Blackbox parity pin for the llama-spit retaliation on the 26.2
// decompile. LlamaHurtByTargetGoal (targetSelector 1) re-aims the hurt
// llama at its attacker; RangedAttackGoal(this, 1.25, 40, 20) ticks
// attackTime 40→0 — attackTime lerps dist/radius into [min,max] and
// min==max here — then performRangedAttack → spit() launches the one
// LlamaSpit (add_entity type 79 + entity.llama.spit positioned sound,
// registry 945 → wire 946). didSpit then fails the target goal's
// canContinueToUse, so each injury arms exactly one shot.
package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestLlamaSpitParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type spitObs struct {
		spits       int
		sounds      int
		hpEnd       float64
		spitHit     bool  // damage_event: BOATA hit by llama's spit
		spitDmgType int32 // damage_type id on that event
		boataEid    int32
		spitEid     int32
	}

	run := func(t *testing.T, name, addr string) spitObs {
		o := spitObs{}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:llama]")
		// BOATB stands adjacent in survival: the spit aims at the
		// puncher but the 10-divergence spray lands on either box —
		// two adjacent targets double the effective hit width.
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "gamemode survival BOATB")
		// A 1-wide corridor with a top-half-slab divider at z=11: the
		// 0.5-gap beneath the slab blocks the llama's entry, while the
		// eye-line (~-58.7) passes above its top (-59) — vanilla's
		// hasLineOfSight clips COLLIDER shapes, so a solid wall would
		// stop the spit from ever arming. Deterministic 1.7-2.7
		// separation: the 0.95-forward spawn lands outside the box
		// (a clip from inside misses) and the segment crosses it.
		sendCommand(t, v.pwa, "fill 8 -61 8 8 -58 13 minecraft:glass")
		sendCommand(t, v.pwa, "fill 10 -61 8 10 -58 13 minecraft:glass")
		sendCommand(t, v.pwa, "fill 8 -61 8 10 -58 8 minecraft:glass")
		sendCommand(t, v.pwa, "fill 8 -61 13 10 -58 13 minecraft:glass")
		sendCommand(t, v.pwa, "fill 9 -60 11 9 -60 11 minecraft:smooth_stone_slab[type=top]")
		sendCommand(t, v.pwa, "tp BOATA 9.4 -60 12.0")
		// The sound observer shares the target cell inside the
		// 16-block hear radius.
		sendCommand(t, v.pwa, "tp BOATB 9.9 -60 12.4")
		sendCommand(t, v.pwa, "effect clear BOATA")
		llama := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"llama", 9.5, -60, 9.5, "{}")
		if llama == 0 {
			t.Fatalf("%s: no llama spawn packet", name)
		}

		// Provoke with a real punch. ~40 ticks out the llama launches
		// its single LlamaSpit; the sound rides the same tick.
		_, pre := dataGetSel(t, sink, pwc, "@e[type=minecraft:llama,limit=1]", "Health")
		sendAttackEntity(v.pwa, llama)
		time.Sleep(300 * time.Millisecond)
		_, post := dataGetSel(t, sink, pwc, "@e[type=minecraft:llama,limit=1]", "Health")
		t.Logf("%s: llama health pre=%v post=%v llamaPos=%v", name, pre, post,
			dataGetFloats(t, sink, pwc, "@e[type=llama,limit=1]", "Pos"))
		// The panic flee can steal the MOVE flag until its walk ends —
		// RangedAttackGoal only starts counting once it's done, so the
		// spit can land seconds late. Watch long enough for both arms.
		deadline := time.Now().Add(10 * time.Second)
		var addsAll map[string]int
		var posNet float64
		spitPos := map[int32][3]float64{}
		spitVel := map[int32][3]float64{}
		var spitRemoved bool
		entEvAll := map[int32][]byte{}
		dmgEvAll := map[int32][][4]int32{}
		var players []int32
		fold := func(wo vehObs) {
			o.spits += wo.adds["llama_spit"]
			posNet += wo.posNet[llama]
			for _, se := range wo.spitEIDs {
				spitPos[se] = wo.lastPos[se]
				spitVel[se] = wo.addVel[se]
			}
			players = append(players, wo.playerEIDs...)
			for e, sts := range wo.entEvAll {
				entEvAll[e] = append(entEvAll[e], sts...)
			}
			for e, ds := range wo.dmgEv {
				dmgEvAll[e] = append(dmgEvAll[e], ds...)
			}
			for _, sid := range wo.soundIds {
				if sid == 946 {
					o.sounds++
				}
			}
			for k, n := range wo.adds {
				if addsAll == nil {
					addsAll = map[string]int{}
				}
				addsAll[k] += n
			}
			for se := range spitPos {
				if wo.removed[se] {
					spitRemoved = true
				}
				if p, ok := wo.lastPos[se]; ok {
					spitPos[se] = p
				}
			}
		}
		for time.Now().Before(deadline) && o.spits == 0 {
			fold(vehWatch(t, v, true, llama, 800*time.Millisecond))
		}
		if o.spits > 0 {
			// Keep watching — trace the spit's flight and removal.
			fold(vehWatch(t, v, true, llama, 1500*time.Millisecond))
		}
		// BOATA's eid: the punch's damage_event on the llama carries
		// cause=BOATA+1 / direct=BOATA+1. The spit hit is the event on
		// BOATA with cause=llama+1 and direct=spit+1.
		if evs := dmgEvAll[llama]; len(evs) > 0 {
			o.boataEid = evs[0][1] - 1
		}
		for se := range spitPos {
			o.spitEid = se
		}
		for victim, evs := range dmgEvAll {
			if victim == llama {
				continue
			}
			for _, ev := range evs {
				if ev[1] == llama+1 && ev[2] == o.spitEid+1 {
					o.spitHit = true
					o.spitDmgType = ev[0]
				}
			}
		}
		t.Logf("%s: 10s obs adds=%v posNet=%.1f sounds=%d spitPos=%v spitVel=%v removed=%v entEv=%v dmgEv=%v players=%v",
			name, addsAll, posNet, o.sounds, spitPos, spitVel, spitRemoved, entEvAll, dmgEvAll, players)
		t.Logf("%s: BOATA Pos=%v", name, dataGetFloats(t, sink, pwc, "BOATA", "Pos"))
		_, o.hpEnd = dataGetSel(t, sink, pwc, "BOATA", "Health")
		return o
	}

	vo := run(t, "vanilla", targets.vanilla)
	mo := run(t, "mc-go", targets.mcgo)
	check := func(name string, o spitObs) {
		t.Helper()
		if o.spits != 1 {
			t.Fatalf("%s: llama_spit spawns = %d, want 1", name, o.spits)
		}
		if o.sounds != 1 {
			t.Fatalf("%s: entity.llama.spit sounds = %d, want 1", name, o.sounds)
		}
		if !o.spitHit {
			t.Fatalf("%s: no spit damage_event on BOATA (boata=%d spit=%d)",
				name, o.boataEid, o.spitEid)
		}
	}
	check("vanilla", vo)
	check("mc-go", mo)
	if vo.spitDmgType != mo.spitDmgType {
		t.Fatalf("spit damage_type id: vanilla=%d mc-go=%d", vo.spitDmgType, mo.spitDmgType)
	}
	t.Logf("parity: 1 spit + 1 sound + spit damage_event (type %d) both legs",
		vo.spitDmgType)
}
