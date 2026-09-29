//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestEnderStareParity pins EnderMan's isBeingStaredBy +
// EndermanLookForPlayerGoal + EndermanFreezeWhenLookedAt pipeline:
// a survival player staring inside the 0.025-distance-scaled cone at
// the enderman's eye (COLLIDER line of sight required) raises
// DATA_STARED_AT (18), latches the target ~5 ticks later raising
// DATA_CREEPY (17), and freezes the mob while the stare persists —
// inside dist²16 a stared enderman random-teleports. Carved pumpkin
// helmets, off-axis gazes, broken sightlines and creative mode all
// keep it calm.
func TestEnderStareParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB", "P"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type legOut struct {
		offAxisEmit  bool    // stared emit while looking 30° away — want false
		pumpkinEmit  bool    // emit while wearing carved_pumpkin — want false
		stareEmit    bool    // idx18 raise on a true stare — want true
		latchEmit    bool    // idx17 raise after the latch — want true
		frozenNet    float64 // posNet while staring held — want ~0
		chaseNet     float64 // posNet once the stare breaks — want >0
		blockedEmit  bool    // emit behind a sightline wall — want false
		closeEmit    bool    // idx18 on the close stare — want true
		teleportJump bool    // close stare moved the mob — want true
		creativeEmit bool    // emit on a creative player — want false
	}

	run := func(t *testing.T, addr string) (res legOut) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "enderstare", v.ca, v.pra, v.pwa, 0)

		cP, prP, pwP := joinPlayBlackbox(t, addr, "P")
		defer cP.Close()
		sinkP := newChatSinkTP(prP, pwP)
		defer func() {
			if t.Failed() {
				t.Logf("P stream: %s", sinkP.health())
			}
		}()
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamemode survival P")
		sendCommand(t, v.pwa, "effect give P minecraft:resistance 1000000 255 true")
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")

		set := func(x, y, z int, b string) {
			sendCommand(t, v.pwa, fmt.Sprintf("setblock %d %d %d %s", x, y, z, b))
		}
		// Pen walls keep the wander pinned — the stare cone is tight
		// enough that a wandering enderman's eye drifts out of it.
		pen := func(x, z int) {
			for _, y := range []int{-60, -59} {
				for _, dp := range [][2]int{{x - 1, z}, {x + 1, z}, {x, z - 1}, {x, z + 1}} {
					set(dp[0], y, dp[1], "minecraft:stone")
				}
			}
		}

		// aim sends the move_pos_rot that points P's view at the
		// enderman's eye (dims·0.85) from P's standing eye (+1.62).
		var ppx, ppy, ppz float64
		aim := func(ex, ey, ez, yawOff float64) {
			dx := ex - ppx
			dy := ey - (ppy + 1.62)
			dz := ez - ppz
			d := math.Sqrt(dx*dx + dy*dy + dz*dz)
			pitch := float32(-math.Asin(dy/d) * 180 / math.Pi)
			yaw := float32(math.Atan2(-dx, dz)*180/math.Pi + yawOff)
			var b protocol.Buffer
			b.Double(ppx)
			b.Double(ppy)
			b.Double(ppz)
			b.Float(yaw)
			b.Float(pitch)
			b.Byte(0x01)
			if err := pwP.WritePacket(server.IDPlayMovePosRotSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		tpP := func(x, y, z float64) {
			ppx, ppy, ppz = x, y, z
			sendCommand(t, pwP, fmt.Sprintf("tp P %v %v %v", x, y, z))
			time.Sleep(350 * time.Millisecond)
		}
		// heldAim re-sends the aimed move packet through the window —
		// the client's view vector is session state the servers keep
		// from the last packet, but the resend also keeps P's tracker
		// alive across the watch.
		holdAim := func(ex, ey, ez, yawOff float64, dur time.Duration) {
			end := time.Now().Add(dur)
			for time.Now().Before(end) {
				aim(ex, ey, ez, yawOff)
				time.Sleep(120 * time.Millisecond)
			}
		}
		// lastPos seeds persist across watches so the first pos
		// packet of a fresh eid reads its real delta, not its
		// distance from the origin (a ~29-block phantom here).
		seeds := map[int32][3]float64{}
		stareOut := func(eid int32, window time.Duration) vehObs {
			return vehWatch(t, v, false, eid, window, seeds)
		}

		const enderEye = -60 + 2.465
		// summonEnder pens a fresh enderman at (ex+0.5,-60,ez+0.5).
		summonEnder := func(ex, ez int) int32 {
			pen(ex, ez)
			e := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
				"enderman", float64(ex)+0.5, -60, float64(ez)+0.5, "{}")
			seeds[e] = [3]float64{float64(ex) + 0.5, -60, float64(ez) + 0.5}
			return e
		}
		killEnders := func() {
			sendCommand(t, v.pwa, "kill @e[type=minecraft:enderman]")
			time.Sleep(250 * time.Millisecond)
		}

		// Watch conn A — close enough to keep every pairing tracked.
		sendCommand(t, v.pwa, "tp BOATA 20 -60 30")

		// === Leg A+B: off-axis gaze and the pumpkin disguise ===
		// Enderman eye at (20.5,-57.535,20.5); P south of it.
		e1 := summonEnder(20, 20)
		tpP(20.5, -60, 28)
		holdAim(20.5, enderEye, 20.5, 30, 1500*time.Millisecond)
		o := stareOut(e1, 700*time.Millisecond)
		res.offAxisEmit = o.boolMeta[18]
		sendCommand(t, v.pwa, "item replace entity P armor.head with minecraft:carved_pumpkin")
		time.Sleep(400 * time.Millisecond)
		holdAim(20.5, enderEye, 20.5, 0, 1800*time.Millisecond)
		o = stareOut(e1, 500*time.Millisecond)
		res.pumpkinEmit = o.boolMeta[18]
		sendCommand(t, v.pwa, "item replace entity P armor.head with minecraft:air")
		time.Sleep(300 * time.Millisecond)
		killEnders()

		// === Leg C: the stare — idx18 up, then the latch ===
		// A FRESH enderman is mandatory: the stare pick can land in
		// the pumpkin-removal gap above, latching aggro before the
		// watch opens (its emits then arrive early and it chases
		// mid-stare — measured on vanilla). The watch itself runs
		// CONCURRENTLY with the aim — the pending and latch emits
		// land inside the aim window, so a watch opened afterward
		// sees only motion.
		e1b := summonEnder(20, 20)
		done := make(chan struct{})
		go func() {
			holdAim(20.5, enderEye, 20.5, 0, 4000*time.Millisecond)
			close(done)
		}()
		o = stareOut(e1b, 4200*time.Millisecond)
		<-done
		res.stareEmit = o.boolMeta[18]
		res.latchEmit = o.boolMeta[17]
		res.frozenNet = o.posNet[e1b]
		if len(o.posTrail[e1b]) > 0 {
			t.Logf("freeze trail (%d ticks): %v", len(o.posTrail[e1b]), o.posTrail[e1b])
		}

		// === Leg D: look away — the latch survives and it chases;
		// the approach burns ~0.4s, so the watch spans it live. The
		// 4th wall comes down so the chase has a path out.
		set(20, -60, 21, "minecraft:air")
		set(20, -59, 21, "minecraft:air")
		done = make(chan struct{})
		go func() {
			holdAim(20.5, enderEye, 20.5, 180, 2200*time.Millisecond)
			close(done)
		}()
		o = stareOut(e1b, 2300*time.Millisecond)
		<-done
		res.chaseNet = o.posNet[e1b]
		killEnders()

		// === Leg E: sightline wall — the eye-row ray crosses z=24 at
		// x≈25.8, so the wall must cover the 24..28 span (a 29..31
		// fill never touches the ray — measured).
		e2 := summonEnder(30, 20)
		sendCommand(t, v.pwa, "fill 24 -58 24 28 -56 24 minecraft:stone")
		holdAim(30.5, enderEye, 20.5, 0, 2200*time.Millisecond)
		o = stareOut(e2, 500*time.Millisecond)
		res.blockedEmit = o.boolMeta[18]
		sendCommand(t, v.pwa, "fill 24 -58 24 28 -56 24 minecraft:air")
		killEnders()

		// === Leg F: close stare — dist²<16 random teleports ===
		e3 := summonEnder(40, 20)
		tpP(40.5, -60, 24.2)
		holdAim(40.5, enderEye, 20.5, 0, 4500*time.Millisecond)
		o = stareOut(e3, 600*time.Millisecond)
		res.closeEmit = o.boolMeta[18]
		res.teleportJump = o.posNet[e3] > 1.0
		killEnders()

		// === Leg G: creative P — canBeSeenAsEnemy exempts ===
		sendCommand(t, v.pwa, "gamemode creative P")
		time.Sleep(350 * time.Millisecond)
		e4 := summonEnder(50, 20)
		tpP(50.5, -60, 28)
		holdAim(50.5, enderEye, 20.5, 0, 2200*time.Millisecond)
		o = stareOut(e4, 500*time.Millisecond)
		res.creativeEmit = o.boolMeta[18]
		killEnders()
		return res
	}

	want := run(t, targets.vanilla)
	got := run(t, targets.mcgo)
	t.Logf("vanilla: %+v", want)
	t.Logf("mc-go:   %+v", got)
	check := func(name string, w, g bool) {
		if w != g {
			t.Errorf("%s: vanilla=%v mc-go=%v", name, w, g)
		}
	}
	check("off-axis gaze emit", want.offAxisEmit, got.offAxisEmit)
	check("pumpkin disguise emit", want.pumpkinEmit, got.pumpkinEmit)
	check("stare emit", want.stareEmit, got.stareEmit)
	check("latch creepy emit", want.latchEmit, got.latchEmit)
	check("blocked sightline emit", want.blockedEmit, got.blockedEmit)
	check("close stare emit", want.closeEmit, got.closeEmit)
	check("close stare teleport", want.teleportJump, got.teleportJump)
	check("creative exempt", want.creativeEmit, got.creativeEmit)
	// Freeze vs chase: the stared window stays put (<1.5), looking
	// away releases the chase (>1.5 net on both legs).
	if (want.frozenNet < 1.5) != (got.frozenNet < 1.5) {
		t.Errorf("freeze: vanilla net=%.2f mc-go net=%.2f", want.frozenNet, got.frozenNet)
	}
	if (want.chaseNet > 1.5) != (got.chaseNet > 1.5) {
		t.Errorf("chase: vanilla net=%.2f mc-go net=%.2f", want.chaseNet, got.chaseNet)
	}
}
