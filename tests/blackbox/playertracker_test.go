package blackbox

import (
	"fmt"
	"testing"
	"time"
)

// TestStationaryPlayerTrackerCadenceParity pins vanilla's stationary-player
// tracker cadence as seen by a second client: a player's TrackedEntity
// rides the same ChunkMap.tick / ServerEntity.sendChanges gate as every
// other tracked entity. Measured against vanilla 26.2 with two scripted
// clients: the pairing answer is a tick-0 zero-delta move_entity_pos, a
// teleport plays the absolute role and restarts the teleportDelay clock,
// forced tickCount%60 zero-delta periodics run on a creation-locked grid,
// and teleportDelay>400 forces exactly one entity_position_sync 802 ticks
// (401 gate-opens at the player's interval 2) after the last absolute
// without breaking the grid.
//
// Divergences kept out of scope on purpose: mc-go's moving-player stream
// is the session echo (a per-movement absolute; vanilla sends interval-2
// deltas), and vanilla's join-landing absolute (an onGround flip at the
// spawn platform) has no mc-go counterpart — both sides' pre-teleport
// traffic is left unpinned.
func TestStationaryPlayerTrackerCadenceParity(t *testing.T) {
	targets := requireBlackboxFiles(t,
		map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"},
		map[string]string{"ops.json": opsJSONFor("PT_Observer")},
		nil)
	defer targets.cleanup()

	const (
		tpX, tpY, tpZ = 2.5, 162.0, 0.5
		// The window must cover the 401-gate absolute at ~802 ticks
		// after the teleport absolute (~41s at mc-go's measured
		// ~51.2ms frame) plus the resumed grid beyond it.
		captureWindow = 55 * time.Second
		// The pairing answer rides the first gate-open after pairing
		// (interval 2), so it lands within a few frames of the spawn.
		answerBudget = 250 * time.Millisecond
		// The teleport absolute: mc-go answers from the session echo
		// (immediate), vanilla from the next gate-open (≤ 2 ticks).
		tpWindow = 400 * time.Millisecond
		// The 401-gate absolute, in per-leg frames after the
		// teleport absolute: 401 gate-opens × interval 2 = 802 frames
		// (measured vanilla 802.0, mc-go 801.4), padded for the
		// paced-loop sag a long window accumulates.
		absoluteLowFrames  = 780.0
		absoluteHighFrames = 815.0
		// Slots 1..14 of the 60-tick grid must all carry their
		// periodic; the window reaches ~18, later slots stay
		// tolerant to frame-sag clipping the tail.
		pinnedSlots = 14
	)

	run := func(addr string) {
		oc, opr, opw, _ := joinPlayEid(t, addr, "PT_Observer")
		defer oc.Close()
		awaitPlayReadyBlackbox(t, addr, oc, opr, opw)
		osink := newFtCadenceSink(opr, opw)

		seq := 0
		cmd := func(s string) time.Time {
			seq++
			tag := fmt.Sprintf("PT_RECEIPT%d", seq)
			sendCommand(t, opw, s)
			sendCommand(t, opw, "say "+tag)
			ftWaitSay(t, osink, tag)
			return time.Now()
		}
		cmd("tp @s 0.5 162 -4.5")

		// The watched player joins second; its own sink only keeps
		// the connection alive — answering the teleport's SyncPos
		// with no movement echo, so the body stays stationary.
		wc, wpr, wpw, weid := joinPlayEid(t, addr, "PT_Watched")
		defer wc.Close()
		awaitPlayReadyBlackbox(t, addr, wc, wpr, wpw)
		wsink := newFtCadenceSink(wpr, wpw)
		_ = wsink

		time.Sleep(500 * time.Millisecond)
		tpAt := cmd(fmt.Sprintf("tp PT_Watched %g %g %g", tpX, tpY, tpZ))

		spawnAt := osink.spawnTime(weid)
		if spawnAt.IsZero() {
			t.Fatalf("%s: watched player %d never spawned on observer", addr, weid)
		}

		time.Sleep(captureWindow)

		var ev []ftEvent
		for _, e := range osink.snapshot() {
			if e.id == weid && e.kind != "spawn" {
				ev = append(ev, e)
			}
		}
		if len(ev) < pinnedSlots+3 {
			t.Fatalf("%s: only %d pos-family packets for the watched player, want the grid", addr, len(ev))
		}

		// The pairing answer is the first zero-delta movepos before
		// the teleport; vanilla's join-landing absolute and any other
		// pre-teleport traffic stay unpinned on both legs.
		var answerAt time.Time
		for _, e := range ev {
			if e.at.Before(tpAt.Add(-100 * time.Millisecond)) && e.kind == "movepos" {
				if !ftZeroDelta(e.raw) {
					t.Errorf("%s pairing answer carried a nonzero delta", addr)
				}
				answerAt = e.at
				break
			}
		}
		if answerAt.IsZero() {
			t.Fatalf("%s: no pairing answer before the teleport (events: %d)", addr, len(ev))
		}
		if d := answerAt.Sub(spawnAt); d > answerBudget {
			t.Errorf("%s pairing answer %.0fms after spawn, want within %v", addr, float64(d)/1e6, answerBudget)
		}

		// Split the post-teleport stream: the teleport's absolute(s),
		// then grid periodics and at most one later absolute.
		var tpAbsAt time.Time
		tpAbses, grid, laterAbs := 0, []ftEvent(nil), []ftEvent(nil)
		for _, e := range ev {
			switch e.kind {
			case "absolute":
				if e.at.Before(tpAt.Add(-100 * time.Millisecond)) {
					// Pre-teleport absolutes (vanilla's
					// join-landing onGround flip) are
					// unpinned on both legs.
					continue
				}
				x, y, z, ok := ftAbsolutePos(e.raw)
				if !ok || x != tpX || y != tpY || z != tpZ {
					t.Errorf("%s absolute at (%.3f,%.3f,%.3f), want the teleport spot", addr, x, y, z)
					continue
				}
				if e.at.Before(tpAt.Add(tpWindow)) {
					tpAbses++
					if tpAbsAt.IsZero() {
						tpAbsAt = e.at
					}
				} else {
					laterAbs = append(laterAbs, e)
				}
			case "movepos":
				if !e.at.Before(tpAt.Add(tpWindow)) {
					grid = append(grid, e)
				}
			default:
				t.Errorf("%s unexpected %s packet in the stationary window", addr, e.kind)
			}
		}
		if tpAbses == 0 {
			t.Fatalf("%s: the teleport produced no absolute", addr)
		}
		// The teleportDelay absolute: exactly one, on the teleport
		// spot, 401 gate-opens after the teleport's absolute.
		if len(laterAbs) != 1 {
			t.Fatalf("%s: %d absolutes after the teleport window, want exactly the teleportDelay one", addr, len(laterAbs))
		}
		// The grid: derive this leg's frame period from the periodic
		// gaps, then every periodic sits on a 60-multiple of the
		// pairing answer with sub-frame wobble, is zero-delta, and
		// no 60-tick multiple is skipped or doubled.
		for _, e := range grid {
			if !ftZeroDelta(e.raw) {
				t.Errorf("%s periodic carried a nonzero delta", addr)
			}
		}
		if len(grid) < pinnedSlots {
			t.Fatalf("%s: only %d grid periodics, want at least %d", addr, len(grid), pinnedSlots)
		}
		frame := ftFramePeriod(grid)
		if frame < 35*time.Millisecond || frame > 70*time.Millisecond {
			t.Fatalf("%s frame period %.2fms outside the 20tps band", addr, float64(frame)/1e6)
		}
		absFrames := float64(laterAbs[0].at.Sub(tpAbsAt)) / float64(frame)
		if absFrames < absoluteLowFrames || absFrames > absoluteHighFrames {
			t.Errorf("%s teleportDelay absolute %.1f frames after the teleport, want ~802", addr, absFrames)
		}
		type slot struct {
			at  time.Time
			idx int
		}
		slots := map[int]slot{}
		var dev []float64
		for i, e := range grid {
			off := float64(e.at.Sub(answerAt)) / float64(frame)
			k := int(off/60 + .5)
			if k < 1 {
				t.Errorf("%s grid event %d at %.2f frames before the first periodic slot", addr, i, off)
				continue
			}
			dev = append(dev, off-float64(60*k))
			if prev, dup := slots[k]; dup {
				t.Errorf("%s two periodics at multiple %d", addr, k)
				_ = prev
			}
			slots[k] = slot{e.at, i}
		}
		phase := ftMedian(dev)
		// The per-event wobble bound is sized to the measured host
		// distribution, not to vanilla's best day: the slice's
		// export-verification run measured VANILLA's own leg sagging
		// 0.67 frames off its fitted grid with consecutive gaps
		// 59.33..60.70 — while mc-go's leg passed even the old 0.6
		// bound — and the frozen/rider legs the same day sagged up
		// to 2.76, so the bound sits above all of it at 3 frames,
		// matching those pins. It still carries the structure: a leg
		// with one grid constant passes, a drifting or doubled grid
		// does not.
		for i, d := range dev {
			if d-phase > 3 || d-phase < -3 {
				t.Errorf("%s periodic %d sits %.2f frames off its leg's fitted grid", addr, i, d-phase)
			}
		}
		// The grid's phase relative to the pairing answer is NOT
		// pinned: how many ticks the first gate-open trails the %60
		// origin is vanilla's own boot luck (measured −1.75 at this
		// pin's recording, and −4.00..−11.82 across seven boots on
		// 2026-11-22, each constant within its window) — wall-clock
		// phase minutiae the vanilla server itself does not hold
		// stably. What IS gameplay structure stays pinned above: the
		// per-event wobble (the grid is phase-LOCKED — one constant
		// plus sub-frame jitter), the exact 60-multiple spacing with
		// no slot skipped or doubled, zero-delta bodies, and the one
		// teleportDelay absolute ~802 frames after the teleport.
		for k := 1; k <= pinnedSlots; k++ {
			if _, ok := slots[k]; !ok {
				t.Errorf("%s missing periodic at multiple %d (tick %d)", addr, k, 60*k)
			}
		}
		// The teleportDelay absolute does not replace a multiple: 401
		// gate-opens at interval 2 is 802 ≡ 22 (mod 60), so the
		// absolute lands BETWEEN two periodics (measured on both
		// servers) — a replaced periodic would leave its multiple
		// missing, which the completeness check above fails.
		// Consecutive-gap spacing is deliberately NOT asserted: the
		// slice's export-verification run measured VANILLA's own
		// running grid at 59.33..60.70-frame gaps while every
		// structural check held (all pinned slots present, no
		// duplicates, one fitted grid constant, zero-delta bodies) —
		// a missing or doubled slot fails the completeness,
		// duplicate and wobble checks, which is the gameplay
		// structure; the raw gap mirrors the paced loop's own sag,
		// which vanilla does not promise.
	}

	run(targets.vanilla)
	run(targets.mcgo)
}
