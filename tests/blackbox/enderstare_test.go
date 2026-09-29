//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"strings"
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

// TestEnderCarryParity pins EndermanTakeBlockGoal +
// EndermanLeaveBlockGoal: a griefing-allowed enderman surrounded by
// reachable ENDERMAN_HOLDABLE cells takes one (~1/20 tick roll,
// stripping to the default state) — DATA_CARRY_STATE (16,
// optional_blockstate) emits nonzero and the cell writes air —
// while a 1/2000 roll places it on air over a full-collision
// non-bedrock support. mob_griefing=false and non-holdable pads
// both keep the metadata empty.
func TestEnderCarryParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type legOut struct {
		takeEmit   bool // idx16 serializer-15 emit>0 over grass pad
		takeSave   bool // carriedBlockState present in data get
		griefEmit  bool // emit with mob_griefing=false — want false
		stoneEmit  bool // emit over a stone pad — want false
		shownCarry bool // summon NBT attach shows idx16>0
		leaveEmit  bool // post-sprint idx16 reads 0
		leavePlace bool // post-sprint block_updates near the mob
	}

	run := func(t *testing.T, addr string) (res legOut) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "endercarry", v.ca, v.pra, v.pwa, 0)
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamerule mob_griefing true")
		sendCommand(t, v.pwa, "tp BOATA 20 -60 30")

		seeds := map[int32][3]float64{}
		watch := func(eid int32, window time.Duration) vehObs {
			return vehWatch(t, v, false, eid, window, seeds)
		}
		// pad builds a 3x3x2 block ring around the mob pocket — the
		// take goal's reachability clip must hit each target first,
		// so every holdable is a face-adjacent cell of the pocket.
		pad := func(x, z int, b string) {
			sendCommand(t, v.pwa, fmt.Sprintf("fill %d -60 %d %d -59 %d minecraft:%s",
				x-1, z-1, x+1, z+1, b))
			sendCommand(t, v.pwa, fmt.Sprintf("fill %d -60 %d %d -58 %d minecraft:air",
				x, z, x, z))
		}
		summon := func(x, z int, nbt string) int32 {
			e := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
				"enderman", float64(x)+0.5, -60, float64(z)+0.5, nbt)
			seeds[e] = [3]float64{float64(x) + 0.5, -60, float64(z) + 0.5}
			return e
		}
		killEnders := func() {
			sendCommand(t, v.pwa, "kill @e[type=minecraft:enderman]")
			time.Sleep(250 * time.Millisecond)
		}
		carryGet := func() string {
			return dataGetLineDirect(t, v.cb, v.prb, v.pwb,
				"@e[type=minecraft:enderman,limit=1]", "carriedBlockState")
		}
		// waitCarry polls the metadata until idx16 emits or the
		// window closes — the take roll averages ~3s over a 16-cell
		// reachable pad but wanders up.
		waitCarry := func(eid int32, max time.Duration) bool {
			deadline := time.Now().Add(max)
			for time.Now().Before(deadline) {
				if o := watch(eid, 1500*time.Millisecond); o.intMetaMax[16] > 0 {
					return true
				}
			}
			return false
		}

		// === Leg A: the take — grass pad ===
		pad(20, 20, "grass_block")
		e1 := summon(20, 20, "{}")
		res.takeEmit = waitCarry(e1, 20*time.Second)
		if res.takeEmit {
			// data get's reply carries only the value —
			// vanilla's codec prints {Name,Properties:{snowy:false}}
			// for the take's stripped default state, an empty
			// Optional prints nothing_found.
			res.takeSave = strings.Contains(carryGet(), "snowy")
		}
		killEnders()

		// === Leg B: mob_griefing=false — never takes ===
		sendCommand(t, v.pwa, "gamerule mob_griefing false")
		time.Sleep(300 * time.Millisecond)
		e2 := summon(20, 20, "{}")
		o := watch(e2, 8*time.Second)
		res.griefEmit = o.intMetaMax[16] > 0
		killEnders()
		sendCommand(t, v.pwa, "gamerule mob_griefing true")

		// === Leg C: non-holdable pad — never takes ===
		pad(20, 20, "stone")
		e3 := summon(20, 20, "{}")
		o = watch(e3, 8*time.Second)
		res.stoneEmit = o.intMetaMax[16] > 0
		killEnders()

		// === Leg D: the leave — pre-carried summon + tick sprint ===
		// grass only survives on dirt-family supports, so the
		// surface gets a guaranteed dirt floor inside a stone pen —
		// a penned mob can't wander onto unsuitable terrain mid-
		// sprint. random_tick_speed zero keeps grass-spread
		// block_updates out of the window — mob goal rolls are not
		// random ticks and keep running.
		// Midnight keeps both mobs penned: vanilla's
		// customServerAiStep teleports the enderman out of the
		// pen whenever isBrightOutside && canSeeSky — night gates
		// that whole branch. (The daylight flee itself is an
		// mc-go gap, noted in GOAL.md.)
		sendCommand(t, v.pwa, "gamerule advance_time false")
		sendCommand(t, v.pwa, "time set midnight")
		sendCommand(t, v.pwa, "gamerule random_tick_speed 0")
		sendCommand(t, v.pwa, "fill 27 -61 25 33 -61 31 minecraft:dirt")
		sendCommand(t, v.pwa, "fill 28 -60 26 32 -58 30 minecraft:stone")
		sendCommand(t, v.pwa, "fill 29 -60 27 31 -58 29 minecraft:air")
		e4 := summon(30, 28, `{carriedBlockState:{Name:"minecraft:grass_block"}}`)
		o = watch(e4, 1500*time.Millisecond)
		res.shownCarry = o.intMetaMax[16] > 0
		// Watch THROUGH the sprint: the enderman can re-take the
		// grass it just placed (a holdable cell at its own level),
		// so the final carry state is luck — a leave+retake inside
		// one diff batch also collapses to just the newest emit.
		// The deterministic pin is ANY carry-state change (metadata
		// or a world write) inside the window. The 1/2000 roll
		// wastes half its hits on y=-59 cells (air below), so the
		// leg retries a second sprint when the first draws blank.
		for attempt := 0; attempt < 2 && !res.leaveEmit && !res.leavePlace; attempt++ {
			watchCh := make(chan vehObs, 1)
			go func() { watchCh <- watch(e4, 45*time.Second) }()
			sendCommand(t, v.pwa, "tick sprint 30000")
			o = <-watchCh
			res.leaveEmit = len(o.carryHist) > 0
			for _, n := range o.blockUps {
				if n > 0 {
					res.leavePlace = true
				}
			}
		}
		killEnders()
		sendCommand(t, v.pwa, "gamerule random_tick_speed 3")
		sendCommand(t, v.pwa, "gamerule advance_time true")
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
	check("take emit", want.takeEmit, got.takeEmit)
	check("take carriedBlockState save", want.takeSave, got.takeSave)
	check("griefing gate", want.griefEmit, got.griefEmit)
	check("non-holdable pad", want.stoneEmit, got.stoneEmit)
	check("carried summon attach", want.shownCarry, got.shownCarry)
	check("leave emit", want.leaveEmit, got.leaveEmit)
	check("leave placement", want.leavePlace, got.leavePlace)
}

// TestEnderFleeParity pins EnderMan.customServerAiStep's daylight
// flee: past 600 ticks since the last target change, a skylight
// dimension inside the monster-burn day window, light over the mob
// cell above 0.5 and open sky above → rand·30 < (br−0.4)·2 teleports
// the mob out. A penned enderman under open sky at day escapes; a
// roofed pen or a midnight clock keeps it inside. posNet measures
// the packet-driven net displacement over the concurrent sprint —
// an escape reads ≥8 (one ±64 teleport), a penned wander stays
// under the 3-cell pocket's drift bound.
func TestEnderFleeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer targets.cleanup()

	type legOut struct {
		fledDay   bool // open pen at day — flees the pen
		fledRoof  bool // roofed pen at day — canSeeSky fails, stays
		fledNight bool // open pen at midnight — isBrightOutside fails
	}

	run := func(t *testing.T, addr string) (res legOut) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "enderflee", v.ca, v.pra, v.pwa, 0)
		sendCommand(t, v.pwa, "gamerule do_mob_spawning false")
		sendCommand(t, v.pwa, "kill @e[type=!minecraft:player]")
		sendCommand(t, v.pwa, "gamemode spectator BOATA")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "gamerule random_tick_speed 0")
		sendCommand(t, v.pwa, "gamerule advance_time false")
		sendCommand(t, v.pwa, "tp BOATA 20 -60 30")

		seeds := map[int32][3]float64{}
		watch := func(eid int32, window time.Duration) vehObs {
			return vehWatch(t, v, false, eid, window, seeds)
		}
		summon := func(x, z int) int32 {
			e := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa,
				"enderman", float64(x)+0.5, -60, float64(z)+0.5, "{}")
			seeds[e] = [3]float64{float64(x) + 0.5, -60, float64(z) + 0.5}
			return e
		}
		killEnders := func() {
			sendCommand(t, v.pwa, "kill @e[type=minecraft:enderman]")
			time.Sleep(250 * time.Millisecond)
		}
		// pen builds a 5x5 stone pocket around (x,z): floor at -61,
		// walls to -57, a 3x3 air cell; roof=false leaves the top
		// open so canSeeSky holds.
		pen := func(x, z int, roof bool) {
			sendCommand(t, v.pwa, fmt.Sprintf("fill %d -61 %d %d -57 %d minecraft:stone",
				x-2, z-2, x+2, z+2))
			sendCommand(t, v.pwa, fmt.Sprintf("fill %d -60 %d %d -58 %d minecraft:air",
				x-1, z-1, x+1, z+1))
			if !roof {
				sendCommand(t, v.pwa, fmt.Sprintf("fill %d -57 %d %d -57 %d minecraft:air",
					x-1, z-1, x+1, z+1))
			}
		}
		// leg runs the eligibility window + rolls inside one sprint
		// (TickCount≥600 gates, then a ~4%/tick roll) while a
		// concurrent watch accumulates posNet — a teleport reads ≥8,
		// a penned wander ≤ ~5.
		leg := func(x, z int, roof bool, day bool) bool {
			pen(x, z, roof)
			eid := summon(x, z)
			if day {
				sendCommand(t, v.pwa, "time set day")
			} else {
				sendCommand(t, v.pwa, "time set midnight")
			}
			ch := make(chan vehObs, 1)
			go func() { ch <- watch(eid, 25*time.Second) }()
			sendCommand(t, v.pwa, "tick sprint 2000")
			o := <-ch
			killEnders()
			return o.posNet[eid] >= 8
		}

		res.fledDay = leg(20, 20, false, true)
		res.fledRoof = leg(20, 20, true, true)
		res.fledNight = leg(20, 20, false, false)
		sendCommand(t, v.pwa, "gamerule advance_time true")
		sendCommand(t, v.pwa, "gamerule random_tick_speed 3")
		return res
	}

	want := run(t, targets.vanilla)
	got := run(t, targets.mcgo)
	t.Logf("vanilla: %+v", want)
	t.Logf("mc-go:   %+v", got)
	if want.fledDay != got.fledDay {
		t.Errorf("daylight flee escape: vanilla=%v mc-go=%v", want.fledDay, got.fledDay)
	}
	if want.fledRoof != got.fledRoof {
		t.Errorf("roofed pen: vanilla=%v mc-go=%v", want.fledRoof, got.fledRoof)
	}
	if want.fledNight != got.fledNight {
		t.Errorf("midnight pen: vanilla=%v mc-go=%v", want.fledNight, got.fledNight)
	}
}
