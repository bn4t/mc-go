//go:build blackbox

// Blackbox parity pin for FrogAi's IDLE RunOne Croak: a ground-idle
// frog picks Croak weight 3 against RandomStroll 1, lookwalk 1 and
// the onGround triggerIf no-op 2 — the no-op finishes the same tick
// and repicks, so the duration-bearing draw lands croak 3/5 of the
// time. Croak's start gates are WALK_TARGET absent, pose STANDING
// and !isInLiquid; it holds pose CROAKING(8) for exactly 60 ticks,
// then stop() drops the pose back to STANDING(0) and the RunOne
// repicks immediately (croaks chain back-to-back 36% of the time).
// Measured from Croak.java + FrogAi.java on the 26.2 jar.
package blackbox

import (
	"strconv"
	"testing"
	"time"

	"mc-go/internal/server"
)

// croakObs is one leg's wire facts: the pose emit sequence on the
// frog and how many frozen steps the croak had left when the freeze
// landed (the croak already ran some wall ticks before /tick freeze
// arrived, so the remaining count is asserted bounded, not exact).
type croakObs struct {
	poseSeq []string // "eid:v" DATA_POSE emits in wire order
	remain  int      // frozen steps until the pose-0 emit landed
	steps   int      // total frozen steps issued
}

func TestFrogCroakParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) (croakObs, int32) {
		out := croakObs{}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "frogcroak", v.ca, v.pra, v.pwa, 0)

		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		// No natural spawns and no prey anywhere near — a slime or
		// magma_cube inside 10 blocks would flip the frog into the
		// TONGUE activity (USING_TONGUE emits) and starve the IDLE
		// RunOne the croak lives in.
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:slime]")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:magma_cube]")
		frog := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"frog", 8.5, -60, 8.5, "{}")
		sendCommand(t, v.pwa, "tick rate 40")
		time.Sleep(400 * time.Millisecond)

		fold := func(o vehObs) { out.poseSeq = append(out.poseSeq, o.poseHist...) }
		hasPose := func(v int) bool {
			mark := strconv.Itoa(int(frog)) + ":" + strconv.Itoa(v)
			for _, h := range out.poseSeq {
				if h == mark {
					return true
				}
			}
			return false
		}

		// Wait for the first CROAKING(8) emit — the croak pick rides
		// the ~6s stroll repick at weight 3/5, then chains.
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) && !hasPose(8) {
			fold(vehWatch(t, v, false, 0, 300*time.Millisecond))
		}
		if !hasPose(8) {
			t.Fatalf("%s: frog %d never emitted pose CROAKING(8); poses=%v",
				name, frog, out.poseSeq)
		}

		// Freeze and single-step: the pose-0 emit lands when the
		// croak's 60-tick counter lapses — the croak already spent
		// some wall ticks before the freeze landed, so count the
		// remainder and bound it, don't pin an exact number.
		sendCommand(t, v.pwa, "tick freeze")
		time.Sleep(200 * time.Millisecond)
		fold(vehWatch(t, v, false, 0, 300*time.Millisecond))
		for i := 0; i < 61 && !hasPose(0); i++ {
			sendCommand(t, v.pwa, "tick step 1")
			fold(vehWatch(t, v, false, 0, 120*time.Millisecond))
			out.steps++
			if hasPose(0) {
				out.remain = i + 1
			}
		}
		sendCommand(t, v.pwa, "tick unfreeze")
		return out, frog
	}

	check := func(t *testing.T, name string, out croakObs, frog int32) {
		// Ordered emits: CROAKING(8) then STANDING(0); a lone idle
		// frog never emits USING_TONGUE(9).
		m8 := strconv.Itoa(int(frog)) + ":8"
		saw8, saw0, saw9 := false, false, false
		for _, h := range out.poseSeq {
			if h == m8 {
				saw8 = true
			}
			if saw8 && h == strconv.Itoa(int(frog))+":0" {
				saw0 = true
			}
			if h == strconv.Itoa(int(frog))+":9" {
				saw9 = true
			}
		}
		if !saw0 {
			t.Fatalf("%s: pose never returned to STANDING(0) after %d frozen steps; poses=%v",
				name, out.steps, out.poseSeq)
		}
		if saw9 {
			t.Fatalf("%s: idle frog emitted USING_TONGUE(9); poses=%v", name, out.poseSeq)
		}
		// The freeze lands within a few wall ticks of the emit — the
		// remaining counter is asserted bounded under the full 60
		// (vanilla's CROAK_TICKS) with enough left to prove the pose
		// actually held rather than flapping.
		if out.remain < 30 || out.remain > 61 {
			t.Fatalf("%s: croak ended %d frozen steps after the freeze; want 30-61 (60t run minus emit-to-freeze lag)",
				name, out.remain)
		}
		t.Logf("%s: croak ended %d steps after freeze (frog=%d seq=%v)",
			name, out.remain, frog, out.poseSeq)
	}

	van, vanFrog := run(t, "vanilla", targets.vanilla)
	mc, mcFrog := run(t, "mc-go", targets.mcgo)
	check(t, "vanilla", van, vanFrog)
	check(t, "mc-go", mc, mcFrog)
	if van.remain < 30 || mc.remain < 30 {
		// Already asserted inside check; log parity when both hold.
		t.Logf("croak remainder vanilla=%d mc-go=%d", van.remain, mc.remain)
	}
}
