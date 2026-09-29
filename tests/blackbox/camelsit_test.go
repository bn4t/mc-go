//go:build blackbox

// Blackbox parity pin for Camel's sitting state machine, measured off
// the 26.2 decompile (Camel.java + CamelAi.RandomSitting) and the
// live jar:
//
//	DATA_LAST_POSE_CHANGE_TICK (metadata 20, LONG) is the sitting
//	state itself: negative while sitting, so the wire shows a
//	negative VarLong for sit-down and a positive one for stand-up.
//	DATA_POSE (metadata 6, POSE) flips SITTING(10) ↔ STANDING(0).
//
//	sitDown():   CAMEL_SIT + pose=10 + tick=-gameTime
//	standUp():   CAMEL_STAND + pose=0 + tick=+gameTime
//	standUpInstantly() (hurt/water/leash/panic-start): silent +
//	tick=max(0,t-53), unconditional — a hurt sitting camel emits
//	two positive ticks (actuallyHurt's arm, then CamelPanic's
//	start a few ticks later).
//
//	/summon replays the NBT verbatim — Camel.finalizeSpawn's
//	resetLastPoseChangeTickToFullStand does NOT run on the summon
//	path, so {LastPoseTick:-5} produces a real sitter (measured:
//	vanilla emits idx20=-5 on the attach).
package blackbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// camelLeg accumulates one leg's camel-pose wire facts.
type camelLeg struct {
	pose   []string // "eid:v" idx6 ser20 emits (10=SITTING, 0=STANDING)
	tick   []string // "eid:v" idx20 ser2 emits (negative=sitting)
	dash   []string // "eid:v" idx19 ser8 emits (1=dashing)
	sounds map[int32]int
}

func (l *camelLeg) fold(o vehObs) {
	l.pose = append(l.pose, o.poseHist...)
	l.tick = append(l.tick, o.poseTkHist...)
	l.dash = append(l.dash, o.dashHist...)
	if l.sounds == nil {
		l.sounds = map[int32]int{}
	}
	for _, sid := range o.soundIds {
		l.sounds[sid]++
	}
}

// hasEmit reports whether the hist saw "eid:value" on its index.
func hasEmit(hist []string, eid int32, want string) bool {
	p := strconv.Itoa(int(eid)) + ":" + want
	for _, h := range hist {
		if h == p {
			return true
		}
	}
	return false
}

// anyTickSign reports whether the eid emitted an idx20 whose sign
// matches the request — negative is the sitting marker.
func anyTickSign(hist []string, eid int32, neg bool) bool {
	p := strconv.Itoa(int(eid)) + ":"
	for _, h := range hist {
		if strings.HasPrefix(h, p) {
			v, err := strconv.ParseInt(strings.TrimPrefix(h, p), 10, 64)
			if err == nil && (v < 0) == neg {
				return true
			}
		}
	}
	return false
}

func TestCamelSitWireParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) (leg camelLeg, eidS, eidM, eidW int32) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "camelsit", v.ca, v.pra, v.pwa, 0)
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "tp BOATA 70 -60 70")
		sendCommand(t, v.pwa, "tp BOATB 70 -60 70")
		leg.sounds = map[int32]int{}
		tag := fmt.Sprintf("cs%d", time.Now().UnixNano()%1_000_000)

		// Summon {LastPoseTick:-5}: /summon replays the NBT without
		// finalizeSpawn — the attach carries a REAL sitter, pose=10
		// behind tick=-5 (measured on the 26.2 jar).
		eidS = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "camel",
			66.5, -60, 66.5,
			fmt.Sprintf("{Tags:[\"%ss\"],LastPoseTick:-5}", tag))
		leg.fold(vehWatch(t, v, false, eidS, 1200*time.Millisecond))

		// data merge lands readAdditionalSaveData without
		// finalizeSpawn: {LastPoseTick:-5} makes a real sitter —
		// tick<0 + pose=10.
		eidM = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "camel",
			70.5, -60, 66.5,
			fmt.Sprintf("{Tags:[\"%sm\"]}", tag))
		sendCommand(t, v.pwa, fmt.Sprintf(
			"data merge entity @e[type=minecraft:camel,tag=%sm,limit=1] {LastPoseTick:-5}", tag))
		leg.fold(vehWatch(t, v, false, eidM, 1200*time.Millisecond))
		// actuallyHurt → standUpInstantly: silent stand, tick≈t-53.
		sendCommand(t, v.pwa, fmt.Sprintf(
			"damage @e[type=minecraft:camel,tag=%sm,limit=1] 4 minecraft:mob_attack", tag))
		leg.fold(vehWatch(t, v, false, eidM, 1500*time.Millisecond))

		// tick()'s water arm: a sitting camel standing in water is
		// stood up instantly the same way.
		eidW = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "camel",
			74.5, -60, 66.5,
			fmt.Sprintf("{Tags:[\"%sw\"]}", tag))
		sendCommand(t, v.pwa, fmt.Sprintf(
			"data merge entity @e[type=minecraft:camel,tag=%sw,limit=1] {LastPoseTick:-5}", tag))
		leg.fold(vehWatch(t, v, false, eidW, 1200*time.Millisecond))
		sendCommand(t, v.pwa, "setblock 74 -60 66 minecraft:water")
		leg.fold(vehWatch(t, v, false, eidW, 1500*time.Millisecond))
		sendCommand(t, v.pwa, "setblock 74 -60 66 minecraft:air")
		return leg, eidS, eidM, eidW
	}

	vLeg, vS, vM, vW := run(t, "vanilla", targets.vanilla)
	mLeg, mS, mM, mW := run(t, "mcgo", targets.mcgo)

	for _, tc := range []struct {
		name   string
		checks func(leg *camelLeg, eid int32) []string
		eidV   int32
		eidM   int32
	}{
		{"summon-neg-tick", func(leg *camelLeg, eid int32) []string {
			got := []string{}
			if hasEmit(leg.pose, eid, "10") {
				got = append(got, "pose10")
			}
			if anyTickSign(leg.tick, eid, true) {
				got = append(got, "tick-")
			}
			return got
		}, vS, mS},
		{"merge-sit", func(leg *camelLeg, eid int32) []string {
			got := []string{}
			if hasEmit(leg.pose, eid, "10") {
				got = append(got, "pose10")
			}
			if anyTickSign(leg.tick, eid, true) {
				got = append(got, "tick-")
			}
			if hasEmit(leg.pose, eid, "0") {
				got = append(got, "pose0")
			}
			if anyTickSign(leg.tick, eid, false) {
				got = append(got, "tick+")
			}
			return got
		}, vM, mM},
		{"water-stand", func(leg *camelLeg, eid int32) []string {
			got := []string{}
			if anyTickSign(leg.tick, eid, true) {
				got = append(got, "tick-")
			}
			if hasEmit(leg.pose, eid, "0") {
				got = append(got, "pose0")
			}
			if anyTickSign(leg.tick, eid, false) {
				got = append(got, "tick+")
			}
			return got
		}, vW, mW},
	} {

		vGot := tc.checks(&vLeg, tc.eidV)
		mGot := tc.checks(&mLeg, tc.eidM)
		want := func() []string {
			switch tc.name {
			case "summon-neg-tick":
				return []string{"pose10", "tick-"}
			case "merge-sit":
				return []string{"pose10", "tick-", "pose0", "tick+"}
			case "water-stand":
				return []string{"tick-", "pose0", "tick+"}
			}
			return nil
		}()
		if fmt.Sprint(vGot) != fmt.Sprint(want) || fmt.Sprint(mGot) != fmt.Sprint(want) {
			t.Fatalf("%s: want %v vanilla=%v mcgo=%v\nvanilla pose=%v tick=%v\nmcgo pose=%v tick=%v",
				tc.name, want, vGot, mGot, vLeg.pose, vLeg.tick, mLeg.pose, mLeg.tick)
		}
	}
}

// TestCamelRandomSittingParity is the long-window pin for
// CamelAi.RandomSitting: an idle camel on open ground eventually
// sits (tick<0 + pose=10) and stands back up (tick>0 + pose=0),
// each direction gated on the 400-tick pose-age minimum.
func TestCamelRandomSittingParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) (sat, stood bool, sitT, standT time.Duration) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "camelsit", v.ca, v.pra, v.pwa, 0)
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "tp BOATA 70 -60 70")
		sendCommand(t, v.pwa, "tp BOATB 70 -60 70")
		tag := fmt.Sprintf("cr%d", time.Now().UnixNano()%1_000_000)
		eid := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "camel",
			70.5, -60, 74.5, fmt.Sprintf("{Tags:[\"%s\"]}", tag))

		// Sit window: pose-age eligibility lands ~17 s after summon;
		// repicks draw 1/4 each — 150 s leaves the p(miss) tail at
		// ~1e-6 per leg.
		var leg camelLeg
		sitDeadline := time.Now().Add(150 * time.Second)
		start := time.Now()
		for time.Now().Before(sitDeadline) && !sat {
			leg.fold(vehWatch(t, v, false, eid, 5*time.Second))
			sat = anyTickSign(leg.tick, eid, true) && hasEmit(leg.pose, eid, "10")
		}
		sitT = time.Since(start)
		if !sat {
			return false, false, sitT, 0
		}
		// Stand window: minimalPoseTicks 400 + repicks mean the sit
		// lasts ~22-25 s; 60 s covers the tail.
		standDeadline := time.Now().Add(60 * time.Second)
		sstart := time.Now()
		for time.Now().Before(standDeadline) && !stood {
			leg.fold(vehWatch(t, v, false, eid, 5*time.Second))
			stood = anyTickSign(leg.tick, eid, false) && hasEmit(leg.pose, eid, "0")
		}
		standT = time.Since(sstart)
		return true, stood, sitT, standT
	}

	vSat, vStood, vSitT, vStandT := run(t, "vanilla", targets.vanilla)
	mSat, mStood, mSitT, mStandT := run(t, "mcgo", targets.mcgo)
	if !vSat || !mSat {
		t.Fatalf("idle sit: vanilla sat=%v (%v) mcgo sat=%v (%v)", vSat, vSitT, mSat, mSitT)
	}
	if !vStood || !mStood {
		t.Fatalf("idle stand: vanilla stood=%v (%v) mcgo stood=%v (%v)", vStood, vStandT, mStood, mStandT)
	}
	t.Logf("camel sit at vanilla=%v mcgo=%v; stand after vanilla=%v mcgo=%v", vSitT, mSitT, vStandT, mStandT)
}

// TestCamelRiddenParity pins the ridden half of the pose machine:
// a sitting camel with a controlling rider declines the dash arm
// (canJump adds !refuseToMove over isSaddled), does NOT stand on
// forward input (tickRidden's zza>0 arm reads a field no remote
// server ever writes — dead code, measured), and never
// self-wanders while player-controlled (vanilla's travelRidden
// bypasses the walk-target chain, so the Brain's stroll picks
// can't move it).
func TestCamelRiddenParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) (leg camelLeg, eid int32, moved float64) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "camelride", v.ca, v.pra, v.pwa, 0)
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "tp BOATB 72 -60 68")
		sendCommand(t, v.pwa, "tp BOATA 71.5 -60 67.5")
		tag := fmt.Sprintf("cd%d", time.Now().UnixNano()%1_000_000)

		// Saddled sitter: the merge lands a real sit (tick<0 +
		// pose 10 on the wire).
		sendCommand(t, v.pwa, fmt.Sprintf(
			`summon minecraft:camel 70.5 -60 66.5 {Tags:["%s"],equipment:{saddle:{id:"minecraft:saddle",count:1}}}`, tag))
		eid = camelEID(t, v, 3*time.Second, "camel")
		sendCommand(t, v.pwa, fmt.Sprintf(
			"data merge entity @e[type=minecraft:camel,tag=%s,limit=1] {LastPoseTick:-5}", tag))
		leg.fold(vehWatch(t, v, false, eid, 1200*time.Millisecond))

		// Board the sitter — riding a sitting camel is allowed.
		sendInteractEntity(v.pwa, eid)
		if o := vehWatch(t, v, true, eid, 5*time.Second); o.passengers != 1 {
			t.Fatalf("%s: mount landed %d passengers, want 1", name, o.passengers)
		}

		// START_RIDING_JUMP on a ridden sitter: canJump's
		// !refuseToMove declines — no DASH emit, no dash sound.
		sendPlayerCommand(v.pwa, 3, 90)
		leg.fold(vehWatch(t, v, false, eid, 1500*time.Millisecond))

		// Forward input: tickRidden's zza>0 stand arm is dead code
		// server-side (a remote Player's zza is never written), so
		// a dedicated-server camel stays sitting — no stand emit.
		sendPlayerInput(v.pwa, 0x01)
		leg.fold(vehWatch(t, v, false, eid, 2*time.Second))
		sendPlayerInput(v.pwa, 0x00)

		// Still sitting under control: ten more seconds must not
		// self-wander either (travelRidden bypasses the walk chain).
		o := vehWatch(t, v, true, eid, 10*time.Second)
		moved = o.posNet[eid]
		return leg, eid, moved
	}

	vLeg, vE, vMoved := run(t, "vanilla", targets.vanilla)
	mLeg, mE, mMoved := run(t, "mcgo", targets.mcgo)

	// The full-run signature each leg must carry: the merge sit
	// (pose10+tick-), and then NOTHING — no stand from rider
	// input (dead zza arm), no DASH emit (sitting canJump gate).
	for _, tc := range []struct {
		name string
		leg  *camelLeg
		eid  int32
	}{
		{"vanilla", &vLeg, vE},
		{"mcgo", &mLeg, mE},
	} {
		if !hasEmit(tc.leg.pose, tc.eid, "10") || !anyTickSign(tc.leg.tick, tc.eid, true) {
			t.Fatalf("%s: merge sit missing: pose=%v tick=%v", tc.name, tc.leg.pose, tc.leg.tick)
		}
		if hasEmit(tc.leg.pose, tc.eid, "0") || anyTickSign(tc.leg.tick, tc.eid, false) {
			t.Fatalf("%s: ridden sitter stood (dead arm fired): pose=%v tick=%v", tc.name, tc.leg.pose, tc.leg.tick)
		}
		if hasEmit(tc.leg.dash, tc.eid, "1") {
			t.Fatalf("%s: sitting canJump gate let the dash arm fire: dash=%v", tc.name, tc.leg.dash)
		}
	}
	// Controlled freeze: ten riderless-input seconds of net travel
	// must be ~zero on both legs.
	if vMoved > 0.5 || mMoved > 0.5 {
		t.Fatalf("controlled wander: vanilla=%.3f mcgo=%.3f, want ~0", vMoved, mMoved)
	}
}
