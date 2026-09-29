//go:build blackbox

// FrogAi's TONGUE activity — ShootTongue, measured on the vanilla
// 26.2 jar:
//
//   - checkExtraStartConditions: ATTACK_TARGET from the
//     FROG_ATTACKABLES sensor (FROG_FOOD tag — slime/magma_cube —
//     at getSize()==1) inside follow_range, reachable, not
//     Pose.CROAKING.
//   - start: setTongueTarget → DATA_TONGUE_TARGET_ID (metadata 19,
//     OPTIONAL_UNSIGNED_INT serializer — wire value is eid+1);
//     WALK_TARGET at the target, speed modifier 2.0.
//   - tick MOVE: target.distanceTo < 1.75 → entity.frog.tongue
//     (NEUTRAL 2.0/1.0), Pose.USING_TONGUE(9), and the target's
//     deltaMovement = unit(target→frog)·0.75 — the tongue yank —
//     then CATCH_ANIMATION.
//   - tick CATCH: eatAnimationTimer ≥ 6 → eatEntity:
//     entity.frog.eat, then doHurtTarget (mob_attack off the frog's
//     attack_damage 10) — a size-1 slime (health 1²) dies and the
//     removal rides remove_entities.
//   - tick EAT: timer ≥ 10 → DONE; stop() erases the target id
//     (metadata 19 back to 0) and the pose returns to STANDING.
//   - AbstractCubeMob NBT: Size = getSize()-1 — {Size:0} is the
//     small edible slime; max_health follows size².
package blackbox

import (
	"testing"
	"time"
)

// TestFrogTongueParity runs the eat cycle on both legs: a frog and a
// size-1 slime inside tongue reach — DATA_TONGUE_TARGET_ID set to the
// slime's eid, USING_TONGUE pose, the yank motion, the slime's death
// removal, and the target id cleared when the eat finishes.
func TestFrogTongueParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, addr string) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "frogtongue", v.ca, v.pra, v.pwa, 0)

		sendCommand(t, v.pwa, "fill 4 -61 4 16 -61 16 minecraft:stone")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "tp BOATB 60 -60 60")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 14.5 0 0")

		frogEid := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "frog",
			8.5, -60, 8.5, "{PersistenceRequired:1b}")
		slimeEid := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "slime",
			10.5, -60, 8.5, "{Size:0}")
		if frogEid == 0 || slimeEid == 0 {
			t.Fatalf("frog/slime not tracked (frog=%d slime=%d)", frogEid, slimeEid)
		}

		targeted, tonguePose, yanked, eaten, cleared := false, false, false, false, false
		win := time.Now().Add(14 * time.Second)
		for time.Now().Before(win) && !(cleared && eaten) {
			o := vehWatch(t, v, false, frogEid, 700*time.Millisecond)
			// intMetaMax: the target id flips eid+1 → 0 fast enough
			// that a single window can hold only the cleared value.
			if o.intMetaMax[19] == slimeEid+1 {
				targeted = true
			}
			if o.intMetaMax[6] == 9 {
				tonguePose = true
			}
			if o.motion[slimeEid] > 0 {
				yanked = true
			}
			if o.removed[slimeEid] {
				eaten = true
			}
			if vv, ok := o.intMeta[19]; ok && vv == 0 && targeted {
				cleared = true
			}
		}
		if !targeted {
			t.Fatalf("DATA_TONGUE_TARGET_ID never set to the slime")
		}
		if !tonguePose {
			t.Fatalf("USING_TONGUE pose never emitted")
		}
		if !yanked {
			t.Fatalf("slime never yanked toward the frog")
		}
		if !eaten {
			t.Fatalf("slime never removed")
		}
		if !cleared {
			t.Fatalf("tongue target never cleared")
		}
	}

	t.Run("vanilla", func(t *testing.T) { run(t, targets.vanilla) })
	t.Run("mc-go", func(t *testing.T) { run(t, targets.mcgo) })
}
