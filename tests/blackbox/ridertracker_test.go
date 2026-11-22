package blackbox

import (
	"fmt"
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestStationaryRiderTrackerCadenceParity pins the wire cadence of a
// stationary mounted pair as seen by a third, unmounted client: a player
// rides a grounded oak_boat with no input for a full teleportDelay cycle
// while the observer records both entities' pos-family streams.
//
// Measured on vanilla 26.2 (wire probe plus ServerEntity.sendChanges
// bytecode): the boat is an ordinary tracked entity — a tick-0 pairing
// answer (zero-delta move_entity_pos), a settle absolute when the summon
// flips onGround, tickCount%60 zero-delta periodics, and the
// teleportDelay>400 forced absolute 401 gate-opens (interval 3 → 1203
// ticks) after the settle, landing between grid slots without breaking
// the grid. The rider's TrackedEntity takes the passenger branch: NO
// packets at mount (the client derives the seat from the vehicle;
// positionRider welds the rider record silently, and the passenger
// branch only ever sends rotation changes), the rider's own %60 grid
// phase keeps running across the whole ride, and dismount
// (Entity.stopRiding — the same path for sneak and /ride dismount)
// plays an absolute at the side-escape spot the vehicle's yaw picks,
// after which the rider's grid resumes at its pre-mount phase.
//
// Divergences kept out of scope: vanilla answers the dismount with a
// second absolute ~100ms later (the pin requires >=1); mc-go's frame is
// ~51.1ms against vanilla's 50ms, so every cadence check is
// frame-relative; the hurt-boat wobble path and mob riders' command
// dismount landing stay unmeasured.
func TestStationaryRiderTrackerCadenceParity(t *testing.T) {
	targets := requireBlackboxFiles(t,
		map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"},
		map[string]string{"ops.json": opsJSONFor("RT_Observer")},
		nil)
	defer targets.cleanup()

	const (
		spotX, spotY, spotZ = 12.5, -60.0, 8.5
		// Entity.stopRiding lands the rider at the side-escape
		// offset the vehicle's yaw picks (measured 1.272 on +z on
		// both servers).
		dismountZ = 9.772
		// The pairing answer rides the first gate-open after
		// pairing, so it lands within a few frames of the spawn.
		answerBudget = 250 * time.Millisecond
		// The settle absolute: onGround flips on the first physics
		// pass and the absolute rides the next interval-3
		// gate-open (~150ms measured on both servers).
		settleLow, settleHigh = 50 * time.Millisecond, 1 * time.Second
		// The window must cover the teleportDelay absolute at ~1203
		// ticks after the settle (~59.5s past the mount at mc-go's
		// measured ~51.1ms frame), plus the resumed grid beyond it.
		rideWindow = 60 * time.Second
		// The dismount tail keeps capturing until the mc-go delay
		// absolute (up to ~62s past the mount under sag) and one
		// post-dismount grid slot per entity are safely inside.
		tailWindow = 5 * time.Second
		// The teleportDelay absolute, in per-leg frames after the
		// settle: 401 gate-opens × interval 3 = 1203 frames
		// (measured vanilla 1203.0, mc-go 1201.6), padded for the
		// paced-loop sag a long window accumulates.
		delayLowFrames, delayHighFrames = 1180.0, 1225.0
		// Slots 1..19 of the 60-tick grid must all carry their
		// periodic; slots 20+ stay tolerant to frame sag pushing
		// them past the wall-clock dismount.
		pinnedSlots = 19
	)

	run := func(addr string) {
		oc, opr, opw, _ := joinPlayEid(t, addr, "RT_Observer")
		defer oc.Close()
		awaitPlayReadyBlackbox(t, addr, oc, opr, opw)
		osink := newFtCadenceSink(opr, opw)

		seq := 0
		cmd := func(s string) time.Time {
			seq++
			tag := fmt.Sprintf("RT_RECEIPT%d", seq)
			sendCommand(t, opw, s)
			sendCommand(t, opw, "say "+tag)
			ftWaitSay(t, osink, tag)
			return time.Now()
		}
		cmd("tp @s 8.5 -60 -2.5")

		// The rider joins second; its own sink only keeps the
		// connection alive — answering SyncPos with no movement
		// echo, so the body stays seated without any input.
		rc, rpr, rpw, reid := joinPlayEid(t, addr, "RT_Rider")
		defer rc.Close()
		awaitPlayReadyBlackbox(t, addr, rc, rpr, rpw)
		rsink := newFtCadenceSink(rpr, rpw)
		_ = rsink

		time.Sleep(500 * time.Millisecond)
		cmd(fmt.Sprintf("tp RT_Rider %g %g %g", spotX, spotY, spotZ))
		cmd(fmt.Sprintf("summon minecraft:oak_boat %g %g %g", spotX, spotY, spotZ))
		// The settle absolute (onGround flip) lands within the next
		// second; the mount receipt is the cadence t=0.
		time.Sleep(2 * time.Second)
		mountAt := cmd("ride RT_Rider mount @e[type=minecraft:oak_boat,limit=1]")
		time.Sleep(rideWindow)
		dismountAt := cmd("ride RT_Rider dismount")
		time.Sleep(tailWindow)

		boatType, _ := server.EntityTypeID("oak_boat")
		boatEid := int32(0)
		for _, e := range osink.snapshot() {
			if e.kind != "spawn" {
				continue
			}
			r := protocol.NewPacketReader(e.raw)
			sid, err := r.VarInt()
			if err != nil {
				continue
			}
			if _, err := r.UUID(); err != nil {
				continue
			}
			if typ, err := r.VarInt(); err == nil && typ == boatType {
				boatEid = sid
			}
		}
		if boatEid == 0 {
			t.Fatalf("%s: observer never saw the boat spawn", addr)
		}
		riderSpawnAt := osink.spawnTime(reid)
		boatSpawnAt := osink.spawnTime(boatEid)
		if riderSpawnAt.IsZero() || boatSpawnAt.IsZero() {
			t.Fatalf("%s: missing spawn pairing (rider %v boat %v)", addr, riderSpawnAt, boatSpawnAt)
		}

		var riderEv, boatEv []ftEvent
		for _, e := range osink.snapshot() {
			if e.kind == "spawn" {
				continue
			}
			switch e.id {
			case reid:
				riderEv = append(riderEv, e)
			case boatEid:
				boatEv = append(boatEv, e)
			}
		}

		// ---- The boat: an ordinary stationary tracked entity. ----
		var boatPairAt time.Time
		for _, e := range boatEv {
			if e.kind == "movepos" {
				if !ftZeroDelta(e.raw) {
					t.Errorf("%s boat pairing answer carried a nonzero delta", addr)
				}
				if d := e.at.Sub(boatSpawnAt); d > answerBudget {
					t.Errorf("%s boat pairing answer %.0fms after spawn, want within %v", addr, float64(d)/1e6, answerBudget)
				}
				boatPairAt = e.at
				break
			}
			if e.kind != "absolute" {
				t.Fatalf("%s: boat produced %s before any movepos", addr, e.kind)
			}
		}
		if boatPairAt.IsZero() {
			t.Fatalf("%s: no boat pairing answer (events: %d)", addr, len(boatEv))
		}

		var boatAbs, grid []ftEvent
		for _, e := range boatEv {
			switch e.kind {
			case "absolute":
				boatAbs = append(boatAbs, e)
			case "movepos":
				// The pairing answer itself is not a grid slot;
				// the first periodic sits one full 60-tick
				// multiple later.
				if !e.at.After(boatPairAt) {
					continue
				}
				grid = append(grid, e)
			default:
				t.Errorf("%s: boat got %s, want only pos/absolute family", addr, e.kind)
			}
		}
		if len(grid) < pinnedSlots+1 {
			t.Fatalf("%s: only %d boat periodics, want the grid", addr, len(grid))
		}
		for _, e := range grid {
			if !ftZeroDelta(e.raw) {
				t.Errorf("%s boat periodic carried a nonzero delta", addr)
			}
		}

		// Exactly two absolutes: the settle (onGround flip at the
		// summon spot) and the teleportDelay forced absolute ~1203
		// frames later, back at the same spot.
		if len(boatAbs) != 2 {
			t.Fatalf("%s: %d boat absolutes, want settle + teleportDelay", addr, len(boatAbs))
		}
		settle, delay := boatAbs[0], boatAbs[1]
		for _, e := range boatAbs {
			x, y, z, ok := ftAbsolutePos(e.raw)
			if !ok || x != spotX || y != spotY || z != spotZ {
				t.Errorf("%s boat absolute at (%.3f,%.3f,%.3f), want the summon spot", addr, x, y, z)
			}
		}
		if d := settle.at.Sub(boatPairAt); d < settleLow || d > settleHigh {
			t.Errorf("%s boat settle absolute %.0fms after pairing, want [%v,%v]", addr, float64(d)/1e6, settleLow, settleHigh)
		}
		frame := ftFramePeriod(grid)
		if frame < 35*time.Millisecond || frame > 70*time.Millisecond {
			t.Fatalf("%s frame period %.2fms outside the 20tps band", addr, float64(frame)/1e6)
		}
		delayFrames := float64(delay.at.Sub(settle.at)) / float64(frame)
		if delayFrames < delayLowFrames || delayFrames > delayHighFrames {
			t.Errorf("%s teleportDelay absolute %.1f frames after the settle, want ~1203", addr, delayFrames)
		}

		// The periodic grid: every boat movepos sits on a 60-tick
		// multiple of the pairing answer with sub-frame wobble,
		// no multiple skipped or doubled, and the delay absolute
		// (1203 ≡ 3 mod 60) lands between slots without breaking it.
		type slot struct {
			at  time.Time
			idx int
		}
		slots := map[int]slot{}
		var dev []float64
		for i, e := range grid {
			off := float64(e.at.Sub(boatPairAt)) / float64(frame)
			k := int(off/60 + .5)
			if k < 1 {
				t.Errorf("%s boat event %d at %.2f frames before the first periodic slot", addr, i, off)
				continue
			}
			dev = append(dev, off-float64(60*k))
			if prev, dup := slots[k]; dup {
				t.Errorf("%s two boat periodics at multiple %d", addr, k)
				_ = prev
			}
			slots[k] = slot{e.at, i}
		}
		phase := ftMedian(dev)
		// The per-event wobble bound is sized to the measured host
		// distribution, not to vanilla's best day: the 2026-11-22
		// sweep-2 and rider-rerun legs measured a 0.86-frame
		// single-event sag on VANILLA's own running grid (with gaps
		// 59.08..60.96), and the frozen-tracker legs the same day
		// sagged up to 2.76, so the bound sits above all of it at 3
		// frames. It still carries the structure: a leg with one
		// grid constant passes, a drifting or doubled grid does not.
		for i, d := range dev {
			if d-phase > 3 || d-phase < -3 {
				t.Errorf("%s boat periodic %d sits %.2f frames off its leg's fitted grid", addr, i, d-phase)
			}
		}
		// The grid's phase constant relative to the pairing answer is
		// NOT pinned: the pairing answer rides the mid-tick pairing
		// moment (when the summon command landed inside the tick —
		// arbitrary wall-clock luck per run) while the periodics
		// ride the tick flush, so the offset varies per boot
		// (measured −0.x at this pin's recording, −3.15 on the
		// 2026-11-22 mc-go leg; vanilla's playertracker pairing
		// answers spread −1.75..−11.82 across seven boots the same
		// day). What IS structure stays pinned above and below: the
		// fitted-grid wobble (one constant per leg), the exact
		// 60-multiple spacing with no slot skipped or doubled, the
		// zero-delta bodies and the ~1203-frame delay absolute.
		for k := 1; k <= pinnedSlots; k++ {
			if _, ok := slots[k]; !ok {
				t.Errorf("%s missing boat periodic at multiple %d (tick %d)", addr, k, 60*k)
			}
		}
		// Consecutive-gap spacing is NOT asserted: vanilla's own
		// running grid sagged 59.08..60.96-frame gaps on the
		// 2026-11-22 rerun leg while every structural check held
		// (all pinned slots present, no duplicates, one fitted grid
		// constant, zero-delta bodies). A missing or doubled slot
		// fails the completeness and duplicate checks above, which
		// is the gameplay structure; the raw gap mirrors the paced
		// loop's own sag, which vanilla does not promise either.
		// The grid survives the dismount: at least one boat
		// periodic after the receipt (measured: the next slot).
		tail := 0
		for _, e := range grid {
			if e.at.After(dismountAt) {
				tail++
			}
		}
		if tail == 0 {
			t.Errorf("%s boat grid stopped at the dismount, want the next periodic", addr)
		}

		// ---- The rider: the passenger branch stays silent. ----
		var riderPairAt time.Time
		for _, e := range riderEv {
			if e.kind == "movepos" {
				if !ftZeroDelta(e.raw) {
					t.Errorf("%s rider pairing answer carried a nonzero delta", addr)
				}
				if d := e.at.Sub(riderSpawnAt); d > answerBudget {
					t.Errorf("%s rider pairing answer %.0fms after spawn, want within %v", addr, float64(d)/1e6, answerBudget)
				}
				riderPairAt = e.at
				break
			}
			if e.kind != "absolute" {
				t.Fatalf("%s: rider produced %s before any movepos", addr, e.kind)
			}
		}
		if riderPairAt.IsZero() {
			t.Fatalf("%s: no rider pairing answer (events: %d)", addr, len(riderEv))
		}

		// The teleport spot absolute anchors the silent window. The
		// rider's join-landing absolute (the spawn platform settle,
		// position picked per boot) can land on either side of it —
		// pre-mount traffic stays unpinned exactly like the
		// stationary-player pin leaves it; only the spot receipt is
		// required.
		var riderTpAt time.Time
		for _, e := range riderEv {
			if e.kind != "absolute" || e.at.After(mountAt.Add(-500 * time.Millisecond)) {
				continue
			}
			x, y, z, _ := ftAbsolutePos(e.raw)
			if x != spotX || y != spotY || z != spotZ {
				continue
			}
			riderTpAt = e.at
			break
		}
		if riderTpAt.IsZero() {
			t.Fatalf("%s: no pre-mount rider absolute at the teleport spot", addr)
		}
		if d := mountAt.Sub(riderTpAt); d > 5*time.Second {
			t.Errorf("%s rider teleport absolute %.1fs before the mount, want the settle window", addr, float64(d)/1e9)
		}

		// Mounted silence: passengers are position-silent — the
		// tracker never sends the rider anything between the mount
		// and the dismount (measured zero events on both servers;
		// the client derives the seat from the vehicle). The window
		// opens AT the receipt marker, not before it: the marker and
		// the rider's pos-family events ride the observer's single
		// TCP stream, so anything arriving after the marker was
		// sent after the mount command processed — and a rider
		// event BEFORE the marker can only be pre-mount grid
		// traffic, which this pin leaves unpinned by design. The
		// old 150ms backward margin tried to cover the mount's own
		// broadcasts, but the mount emits no rider pos-family
		// packet to trackers, so it only ever misattributed the
		// rider's last pre-mount periodic — measured landing 100ms
		// before the receipt on the 2026-11-22 vanilla leg, inside
		// that margin, with the ride itself fully silent.
		for _, e := range riderEv {
			if e.at.After(mountAt) && e.at.Before(dismountAt.Add(-500*time.Millisecond)) {
				t.Errorf("%s rider got %s %.1fs into the ride, want the passenger branch silent", addr, e.kind, float64(e.at.Sub(mountAt))/1e9)
			}
		}

		// Dismount: at least one absolute at the side-escape spot
		// around the receipt (vanilla sends a second ~100ms later;
		// the pin requires >=1 and tolerates the extra).
		var riderDismAt time.Time
		for _, e := range riderEv {
			if e.kind != "absolute" || !e.at.After(mountAt) {
				continue
			}
			x, y, z, ok := ftAbsolutePos(e.raw)
			if !ok || math.Abs(x-spotX) > .01 || math.Abs(y-spotY) > .01 || math.Abs(z-dismountZ) > .01 {
				t.Errorf("%s rider dismount absolute at (%.3f,%.3f,%.3f), want the side-escape spot", addr, x, y, z)
				continue
			}
			if riderDismAt.IsZero() {
				riderDismAt = e.at
			}
		}
		if riderDismAt.IsZero() {
			t.Fatalf("%s: no rider dismount absolute at (%g,%g,%g)", addr, spotX, spotY, dismountZ)
		}
		if d := riderDismAt.Sub(dismountAt); d < -250*time.Millisecond || d > 750*time.Millisecond {
			t.Errorf("%s rider dismount absolute %v from the receipt", addr, d)
		}

		// Resumption: the rider's own %60 grid phase survives the
		// whole ride — the first post-dismount periodic lands on a
		// 60-tick multiple of the pre-mount pairing answer
		// (measured k=21 on both servers; k itself is wall-clock
		// noise, the congruence is the parity fact).
		var resumeAt time.Time
		for _, e := range riderEv {
			if e.kind == "movepos" && e.at.After(riderDismAt) {
				if !ftZeroDelta(e.raw) {
					t.Errorf("%s rider resumption periodic carried a nonzero delta", addr)
				}
				resumeAt = e.at
				break
			}
		}
		if resumeAt.IsZero() {
			t.Fatalf("%s: no rider periodic after the dismount", addr)
		}
		if d := resumeAt.Sub(riderDismAt); d > 4500*time.Millisecond {
			t.Errorf("%s rider grid resumed %.1fs after the dismount, want the next slot", addr, float64(d)/1e9)
		}
		off := float64(resumeAt.Sub(riderPairAt)) / float64(frame)
		k := int(off/60 + .5)
		if k < 19 || k > 23 {
			t.Errorf("%s rider resumption at grid multiple %d, want ~21", addr, k)
		}
		// The resumption's sub-slot phase relative to the pre-mount
		// pairing answer is NOT pinned: riderPairAt carries the
		// pairing answer's own mid-tick lag (vanilla measured −11.10
		// frames on the 2026-11-22 run, inside the same −1.75..−11.82
		// boot-luck spread measured across seven vanilla boots in the
		// stationary-player pin), so the residue is wall-clock luck,
		// not gameplay. The parity fact is the congruence the k
		// bound above pins: the post-dismount periodic lands on a
		// 60-tick multiple of the pre-mount grid — the rider's own
		// %60 grid phase survived the whole ride.
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
