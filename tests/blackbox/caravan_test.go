// Blackbox parity pin for LlamaFollowCaravanGoal (goal priority 2 on
// llama + trader_llama — it outranks the family's PanicGoal(3),
// BreedGoal(4), TemptGoal(5) and the WaterAvoidingRandomStroll(7)).
// Measured off the 26.2 decompile (goal/LlamaFollowCaravanGoal.java):
//
//	canUse: an unleashed, un-caravanned llama scans a 9×4×9 box —
//	prefers the closest caravan member with no tail of its own (the
//	chain end), else the closest leashed llama with no tail; a join
//	is refused under dist²=4 or when the candidate's head-chain
//	never reaches a leashed llama within 8 hops.
//
//	tick: the follower paths to a point trailing its head by two
//	blocks at speedModifier×movement_speed (2.1 base, ×1.2 ramps to
//	a 3.0 cap past 26 blocks, 40 ticks at the cap drops the link).
//
//	stop/leave: firstIsLeashed fails — the chain dissolves, and the
//	ex-follower falls back to idle goals where it stands.
package blackbox

import (
	"math"
	"testing"
	"time"
)

func TestLlamaCaravanParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "BOATC"),
	}, nil)
	defer targets.cleanup()

	type obs struct {
		bA   float64 // B's final distance to the leashed head A
		cB   float64 // C's final distance to its head B
		bNet float64 // B's net travel during the join window
		bFar float64 // B's distance to A's tp'd cell after the break
		aA   float64 // A's leash drift from its summon cell (info)
	}

	// runLeg drives one server: leash llama A to a fence, summon B
	// and C nearby — B joins A's caravan, C joins behind B. After
	// convergence, kill the knot and teleport A far away: with the
	// chain's leash gone firstIsLeashed fails and B stays put.
	run := func(t *testing.T, name, addr string) obs {
		t.Helper()
		o := obs{}
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		cc, _, _ := joinPlayBlackbox(t, addr, "BOATC")
		defer cc.Close()
		drainChunkWindow(t, name, v.ca, v.pra, v.pwa, 0)

		sendCommand(t, v.pwa, "gamemode spectator BOATC")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "gamerule randomtickspeed 0")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:llama]")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:trader_llama]")
		sendCommand(t, v.pwa, "kill @e[type=minecraft:leash_knot]")
		sendCommand(t, v.pwa, "gamemode survival BOATA")
		sendCommand(t, v.pwa, "gamemode survival BOATB")
		// The knot needs a real fence to survive; A's leash hangs on
		// it for the whole join window.
		sendCommand(t, v.pwa, "setblock 40 -60 41 minecraft:oak_fence")
		// BOATA stands just south of A: near enough to track every
		// llama, and the distance=..2 selector below singles out A.
		sendCommand(t, v.pwa, "tp BOATA 40.5 -60 39.0")
		sendCommand(t, v.pwa, "tp BOATB 30.5 -60 30.5")

		a := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"llama", 40.5, -60, 40.0, "{leash:[I;40,-60,41]}")
		b := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"llama", 44.5, -60, 44.5, "{}")
		c := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
			"llama", 47.5, -60, 47.5, "{}")
		if a == 0 || b == 0 || c == 0 {
			t.Fatalf("%s: summon eids a=%d b=%d c=%d", name, a, b, c)
		}

		// Absolute xz = spawn + Σ posTrail deltas (summonTrackedNBT
		// consumed each add_entity, so lastPos has no base).
		merged := vehObs{
			posNet:   map[int32]float64{},
			posTrail: map[int32][][2]float64{},
		}
		watch := func(dur time.Duration) {
			deadline := time.Now().Add(dur)
			for time.Now().Before(deadline) {
				wo := vehWatch(t, v, false, b, 400*time.Millisecond)
				for eid, d := range wo.posNet {
					merged.posNet[eid] += d
				}
				for eid, tr := range wo.posTrail {
					merged.posTrail[eid] = append(merged.posTrail[eid], tr...)
				}
			}
		}
		absXZ := func(eid int32, sx, sz float64) [2]float64 {
			p := [2]float64{sx, sz}
			for _, d := range merged.posTrail[eid] {
				p[0] += d[0]
				p[1] += d[1]
			}
			return p
		}
		dist := func(p, q [2]float64) float64 {
			return math.Hypot(p[0]-q[0], p[1]-q[1])
		}

		// Join window: 10s is dozens of converge ticks at the 2.1×
		// speed ramp — B trails A by ~2, C trails B by ~2.
		watch(10 * time.Second)
		aPos := absXZ(a, 40.5, 40.0)
		bPos := absXZ(b, 44.5, 44.5)
		cPos := absXZ(c, 47.5, 47.5)
		o.aA = dist(aPos, [2]float64{40.5, 40.0})
		o.bA = dist(bPos, aPos)
		o.cB = dist(cPos, bPos)
		o.bNet = merged.posNet[b]
		t.Logf("%s: join a=%v b=%v c=%v bA=%.2f cB=%.2f bNet=%.2f",
			name, aPos, bPos, cPos, o.bA, o.cB, o.bNet)

		// Break: kill the knot → A's leash drops → firstIsLeashed
		// fails up the chain → B and C leave their caravans. Then
		// teleport A far away; a still-linked B would chase the head
		// at the 1.2× ramp, a freed one stays put.
		sendCommand(t, v.pwa, "kill @e[type=minecraft:leash_knot]")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, v.pwa,
			"tp @e[type=minecraft:llama,distance=..2,sort=nearest,limit=1] 75.5 -60 75.5")
		watch(10 * time.Second)
		bAfter := absXZ(b, 44.5, 44.5)
		o.bFar = math.Hypot(bAfter[0]-75.5, bAfter[1]-75.5)
		t.Logf("%s: break bAfter=%v bFar=%.2f", name, bAfter, o.bFar)
		return o
	}

	want := run(t, "vanilla", targets.vanilla)
	got := run(t, "mc-go", targets.mcgo)

	// Join asserts are side-agnostic: the caravan goal runs on both
	// servers, so each leg must show the same convergence shape.
	for _, o := range []struct {
		name string
		o    obs
	}{{"vanilla", want}, {"mc-go", got}} {
		if o.o.bA > 4.5 {
			t.Errorf("%s: follower B ended %.2f from head (want ≤4.5)", o.name, o.o.bA)
		}
		if o.o.cB > 4.5 {
			t.Errorf("%s: chain link C ended %.2f from B (want ≤4.5)", o.name, o.o.cB)
		}
		if o.o.bNet < 3 {
			t.Errorf("%s: B traveled only %.2f joining the caravan (want ≥3)", o.name, o.o.bNet)
		}
		if o.o.bFar < 15 {
			t.Errorf("%s: after the leash break B chased the head %.2f blocks off (want ≥15 clear)", o.name, o.o.bFar)
		}
	}
	// Cross-leg sanity on the measured values.
	if math.Abs(want.bA-got.bA) > 2 {
		t.Errorf("bA divergence vanilla=%.2f mc-go=%.2f", want.bA, got.bA)
	}
}
