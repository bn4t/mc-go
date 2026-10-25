//go:build blackbox

// Blackbox parity pin for FrogAi's TONGUE activity (ShootTongue):
// the FROG_ATTACKABLES sensor picks the nearest VISIBLE FROG_FOOD
// entity — slime or magma_cube at getSize()==1 — inside a fixed
// 10.0 detection distance; the frog walks at it at speedModifier
// 2.0, carries DATA_TONGUE_TARGET_ID (metadata 19,
// OPTIONAL_UNSIGNED_INT = target eid+1), and inside EATING_DISTANCE
// 1.75 plays the tongue sound, flips pose to USING_TONGUE(9), and
// yanks the target toward itself at 0.75. Six ticks later eatEntity
// plays the eat sound and doHurtTarget's mob_attack kills the cube;
// a magma_cube rolls its loot table's damage_source_properties
// source_entity frog gates and drops the froglight matching the
// frog's variant — a slime drops nothing (the inverted frog gate
// kills its only entry). stop() then clears idx19 → 0 and the pose.
// Measured from Frog.java + ShootTongue.java + FrogAttackablesSensor
// + slime.json/magma_cube.json on the 26.2 jar.
package blackbox

import (
	"strconv"
	"testing"
	"time"

	"mc-go/internal/server"
)

// frogTongueLeg is one leg's wire facts for one frog-and-prey pair.
type frogTongueLeg struct {
	tongue  []string       // "eid:v" idx19 emits on the frog
	pose9   int32          // eid that emitted pose USING_TONGUE(9)
	motion  map[int32]int  // set_entity_motion counts by eid
	removed map[int32]bool // remove_entities by eid
	items   map[string]int // add_entity counts by type
	itemID  []string       // Item.id of the nearest item entity after the eat
}

func TestFrogTongueParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, name, addr string) (slimeLeg, magmaLeg frogTongueLeg, frogA, frogB, slmA, magB int32) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, prc, pwc := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, "frogtongue", v.ca, v.pra, v.pwa, 0)
		sink := newChatSinkTP(prc, pwc)

		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "tp BOATC 60 -60 60")

		fold := func(leg *frogTongueLeg, o vehObs) {
			leg.tongue = append(leg.tongue, o.tongueHist...)
			if o.pose9Eid != 0 {
				leg.pose9 = o.pose9Eid
			}
			for me, n := range o.motion {
				leg.motion[me] += n
			}
			for re := range o.removed {
				leg.removed[re] = true
			}
			for k, n := range o.adds {
				leg.items[k] += n
			}
		}

		// Pair A — temperate frog + size-1 slime, 5 blocks east.
		slimeLeg = frogTongueLeg{motion: map[int32]int{}, removed: map[int32]bool{}, items: map[string]int{}}
		frogA = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "frog", 8.5, -60, 8.5, "{}")
		slmA = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "slime", 13.5, -60, 8.5, "{Size:0}")
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !slimeLeg.removed[slmA] {
			fold(&slimeLeg, vehWatch(t, v, false, 0, 300*time.Millisecond))
		}
		for i := 0; i < 4; i++ {
			fold(&slimeLeg, vehWatch(t, v, false, 0, 300*time.Millisecond))
		}
		slimeLeg.itemID = dataGetTokens(t, sink, pwc,
			"@e[type=minecraft:item,sort=nearest,limit=1]", "Item.id")
		// Clear the slime_ball so pair B's nearest-item query can't
		// see it.
		sendCommand(t, v.pwa, "kill @e[type=minecraft:item]")

		// Pair B — warm frog + size-1 magma_cube; the froglight it
		// drops maps to the variant (warm → pearlescent). BOATA is
		// tp'd near the pair first: its default view distance 2
		// untracks entities past 32 blocks on vanilla.
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 25")
		magmaLeg = frogTongueLeg{motion: map[int32]int{}, removed: map[int32]bool{}, items: map[string]int{}}
		frogB = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "frog", 28.5, -60, 8.5, `{variant:"minecraft:warm"}`)
		magB = summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "magma_cube", 33.5, -60, 8.5, "{Size:0}")
		deadline = time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !magmaLeg.removed[magB] {
			fold(&magmaLeg, vehWatch(t, v, false, 0, 300*time.Millisecond))
		}
		for i := 0; i < 4; i++ {
			fold(&magmaLeg, vehWatch(t, v, false, 0, 300*time.Millisecond))
		}
		magmaLeg.itemID = dataGetTokens(t, sink, pwc,
			"@e[type=minecraft:item,sort=nearest,limit=1]", "Item.id")
		return slimeLeg, magmaLeg, frogA, frogB, slmA, magB
	}

	check := func(t *testing.T, name string, leg frogTongueLeg, frogE, preyE int32, wantItem string) {
		// DATA_TONGUE_TARGET_ID: the prey eid+1 emit lands while the
		// goal runs, the 0 emit lands on stop(). Order matters.
		wantHook := strconv.Itoa(int(frogE)) + ":" + strconv.Itoa(int(preyE)+1)
		wantClear := strconv.Itoa(int(frogE)) + ":0"
		hooked, cleared := false, false
		for _, h := range leg.tongue {
			if h == wantHook {
				hooked = true
			}
			if hooked && h == wantClear {
				cleared = true
			}
		}
		if !hooked {
			t.Fatalf("%s: no DATA_TONGUE_TARGET_ID=%d emit on frog %d; tongue=%v",
				name, preyE+1, frogE, leg.tongue)
		}
		if !cleared {
			t.Fatalf("%s: DATA_TONGUE_TARGET_ID never cleared to 0; tongue=%v",
				name, leg.tongue)
		}
		if leg.pose9 != frogE {
			t.Fatalf("%s: no USING_TONGUE(9) pose emit on frog %d (saw %d)",
				name, frogE, leg.pose9)
		}
		if leg.motion[preyE] < 1 {
			t.Fatalf("%s: prey %d was never yanked toward the frog", name, preyE)
		}
		if !leg.removed[preyE] {
			t.Fatalf("%s: prey %d never removed after the eat", name, preyE)
		}
		// slime.json/magma_cube.json both make the frog-kill drop
		// deterministic: exactly one entry survives the
		// damage_source_properties source_entity gates.
		if leg.items["item"] < 1 {
			t.Fatalf("%s: no item entity spawned after the eat; adds=%v",
				name, leg.items)
		}
		if len(leg.itemID) == 0 || leg.itemID[0] != wantItem {
			t.Fatalf("%s: dropped item id %v, want %s",
				name, leg.itemID, wantItem)
		}
	}

	vSlm, vMag, vFrogA, vFrogB, vSlmA, vMagB := run(t, "vanilla", targets.vanilla)
	mSlm, mMag, mFrogA, mFrogB, mSlmA, mMagB := run(t, "mc-go", targets.mcgo)

	check(t, "vanilla-slime", vSlm, vFrogA, vSlmA, "minecraft:slime_ball")
	check(t, "vanilla-magma", vMag, vFrogB, vMagB, "minecraft:pearlescent_froglight")
	check(t, "mc-go-slime", mSlm, mFrogA, mSlmA, "minecraft:slime_ball")
	check(t, "mc-go-magma", mMag, mFrogB, mMagB, "minecraft:pearlescent_froglight")
}

// TestFrogTongueUnreachable pins UNREACHABLE_TONGUE_TARGETS: a
// FROG_FOOD cube the sensor can SEE but whose path can't end inside
// the 1.75 eating distance gets written to the unreachable FIFO —
// cap 5, head evicts, every add refreshes the 100-tick memory
// expiry — and skipped on every repick. Its DATA_TONGUE_TARGET_ID
// never emits (the behavior never starts, so the walk and the
// metadata hook never happen) while a same-window reachable cube is
// hooked and eaten normally. Geometry: a 3-block stone pillar the
// eye-line clears but no ground path climbs; the memory's expiry
// re-feeds the cube every ~100 ticks, where the path check re-fails.
func TestFrogTongueUnreachable(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	run := func(t *testing.T, addr string) (nearEmit, farEmit bool) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "frogtongue-unreach", v.ca, v.pra, v.pwa, 0)
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		// Park inside tracking range — past ~32 blocks vanilla
		// untracks the entities and no metadata reaches the client.
		sendCommand(t, v.pwa, "tp BOATA 50 -60 48")
		sendCommand(t, v.pwa, "tp BOATB 50 -60 48")

		sendCommand(t, v.pwa, "setblock 52 -59 52 minecraft:stone")
		sendCommand(t, v.pwa, "setblock 52 -58 52 minecraft:stone")
		sendCommand(t, v.pwa, "setblock 52 -57 52 minecraft:stone")
		frogE := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "frog", 50.5, -60, 44.5, "{}")
		farE := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "slime", 52.5, -56, 52.5, "{Size:0,NoAI:1b}")
		nearE := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "slime", 50.5, -60, 52.5, "{Size:0,NoAI:1b}")

		var tongue []string
		removed := false
		deadline := time.Now().Add(14 * time.Second)
		for time.Now().Before(deadline) && !removed {
			o := vehWatch(t, v, false, 0, 300*time.Millisecond)
			tongue = append(tongue, o.tongueHist...)
			if o.removed[nearE] {
				removed = true
			}
		}
		// Watch past the eat: the unreachable memory's 100-tick
		// expiry re-feeds the pillar cube through the sensor, where
		// the path check re-fails — no emit may ever land for it.
		for i := 0; i < 20; i++ {
			o := vehWatch(t, v, false, 0, 300*time.Millisecond)
			tongue = append(tongue, o.tongueHist...)
		}
		farMark := strconv.Itoa(int(frogE)) + ":" + strconv.Itoa(int(farE)+1)
		nearMark := strconv.Itoa(int(frogE)) + ":" + strconv.Itoa(int(nearE)+1)
		for _, h := range tongue {
			nearEmit = nearEmit || h == nearMark
			farEmit = farEmit || h == farMark
		}
		return
	}

	vN, vF := run(t, targets.vanilla)
	mN, mF := run(t, targets.mcgo)
	t.Logf("unreachable: vanilla near=%v far=%v; mc-go near=%v far=%v", vN, vF, mN, mF)
	if !vN || !mN {
		t.Errorf("reachable slime not tongue-hooked: vanilla=%v mc-go=%v", vN, mN)
	}
	if vF || mF {
		t.Errorf("unreachable slime emitted a tongue target: vanilla=%v mc-go=%v", vF, mF)
	}
}
