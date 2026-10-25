//go:build blackbox

// Cat$CatRelaxOnOwnerGoal — the lie-on-sleeping-owner's-bed wire,
// measured on the vanilla 26.2 jar:
//
//   - canUse: cat isTame && !isOrderedToSit && owner is a Player who
//     isSleeping && distanceToSqr(owner) < 100 && a bed-goal cell at
//     the head's FACING offset whose 2-inflate holds no other lying or
//     relaxing cat (self excluded).
//   - start: setInSittingPose(false); navigation.moveTo(goal, 1.1·
//     movement_speed) — the cat keeps walking each tick.
//   - tick: cat→owner distanceToSqr < 2.5 → onBedTicks++; over
//     adjustedTickDelay(16) → IS_LYING(idx21)=true, RELAX_STATE_ONE
//     (idx22)=false; otherwise lookAt(owner,45,45) + RELAX=true.
//     Outside the radius only IS_LYING clears.
//   - canContinueToUse re-checks tame/sit/owner-sleeping/goalPos/
//     spaceIsOccupied each tick; failing it runs stop().
//   - stop: setLying(false); owner sleepTimer >= 100 &&
//     rand < CAT_WAKING_UP_GIFT_CHANCE (day timeline: 0.7 in the
//     23667–362 window the dawn wake always lands in) → giveMorningGift
//     = cat_morning_gift loot (rabbit_hide/foot, chicken, feather,
//     rotten_flesh, string @10 + phantom_membrane @2); then
//     onBedTicks=0, setRelaxStateOne(false), navigation.stop().
//   - Vanilla's sleep status skips spectators — a watching spectator
//     never blocks the night skip.
package blackbox

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// dataGetLineDirect issues `data get entity <sel> <path>` and reads the
// connection inline until the reply (or a sentinel say) arrives, then
// clears the read deadline it armed. Unlike the chat sink it leaves no
// background reader behind: helpers like vehWatch that deadline-poll
// the same socket can interleave freely.
func dataGetLineDirect(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, sel, path string) string {
	t.Helper()
	sendCommand(t, pw, "data get entity "+sel+" "+path)
	sendCommand(t, pw, "say ZEND")
	defer c.SetReadDeadline(time.Time{})
	got := ""
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch int32(p.ID) {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		default:
			line := fmt.Sprintf("%#x:%s", p.ID, string(p.Data))
			// Keep every chat-ish payload for postmortems: error replies
			// (commands.*) and bed overlay messages land here too.
			if strings.Contains(line, "translate") || strings.Contains(line, "data.entity.query") {
				t.Logf("chatline: %q", line)
			}
			// The data-get reply is the query value (data.entity.query)
			// or the nothing_found error — both end the probe.
			if strings.Contains(line, "data.entity.query") || strings.Contains(line, "nbtpath.nothing_found") {
				got = line
			}
			if strings.Contains(line, "ZEND") {
				return got
			}
		}
	}
	return got
}

// TestCatRelaxParity pins IS_LYING/RELAX_STATE_ONE and the wake gift on
// both legs: a cat tamed to the sleeper relaxes as it arrives, lies
// after ~16 ticks, unwinds on wake, and drops a gift at ~70%/night.
func TestCatRelaxParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BOATA", "BOATB"),
	}, nil)
	defer targets.cleanup()

	run := func(t *testing.T, addr string) {
		v := joinVehPair(t, addr)
		defer v.ca.Close()
		defer v.cb.Close()
		drainChunkWindow(t, "catrelax", v.ca, v.pra, v.pwa, 0)

		// Flat floor for the cat's straight-line walk, bed head at
		// (8,-60,7) facing north (goal cell = (8,-60,6)), BOATB as a
		// far spectator — vanilla's sleep status ignores spectators,
		// so BOATA alone decides the skip.
		sendCommand(t, v.pwa, "fill 4 -61 4 14 -61 14 minecraft:stone")
		sendCommand(t, v.pwa, "setblock 8 -60 8 minecraft:red_bed[part=foot,facing=north]")
		sendCommand(t, v.pwa, "setblock 8 -60 7 minecraft:red_bed[part=head,facing=north]")
		sendCommand(t, v.pwa, "gamemode spectator BOATB")
		sendCommand(t, v.pwa, "tp BOATB 60 -60 60")
		sendCommand(t, v.pwa, "tp BOATA 8.5 -60 10.5 0 0")

		uuidInts := snbtIntDigits(dataGetLineDirect(t, v.ca, v.pra, v.pwa, "@s", "UUID"))
		if len(uuidInts) != 4 {
			t.Fatalf("BOATA UUID unreadable")
		}
		eid := summonTrackedNBT(t, v.ca, v.pra, v.pwa, v.pwa, "cat",
			10.5, -60, 9.5, fmt.Sprintf("{Owner:[I;%s,%s,%s,%s]}",
				uuidInts[0], uuidInts[1], uuidInts[2], uuidInts[3]))
		if eid == 0 {
			t.Fatalf("cat not tracked")
		}

		relaxed, cleared, giftItems := false, false, 0
		for night := 0; night < 3; night++ {
			// The previous night's skip lands at +5s; a still-sleeping
			// click is an occupied no-op, so wait for the stand-up
			// (sleeping_pos gone) before starting the next night.
			waitAwake := time.Now().Add(10 * time.Second)
			for time.Now().Before(waitAwake) {
				if l := dataGetLineDirect(t, v.ca, v.pra, v.pwa, "@s", "sleeping_pos"); strings.Contains(l, "nothing_found") {
					break
				}
			}
			sendCommand(t, v.pwa, "time set 13000")
			// Drain pending packets and confirm any outstanding SyncPos
			// before clicking — an unconfirmed teleport makes vanilla
			// silently drop use_item_on (awaitingPositionFromClient).
			pumpSleepClient(v.ca, v.pra, v.pwa, 400*time.Millisecond, nil)

			// Fresh click every night — the wake stand-up lands next
			// to the head, still inside the use reach.
			sendUseItemOn(t, v.pwa, 8, -60, 7, 1, 1)

			deadline := time.Now().Add(1500 * time.Millisecond)
			var dl string
			for time.Now().Before(deadline) && !strings.Contains(dl, "data.entity.query") {
				dl = dataGetLineDirect(t, v.ca, v.pra, v.pwa, "@s", "sleeping_pos")
			}
			if strings.Contains(dl, "data.entity.query") {
				t.Logf("night %d post-click sleeping_pos: %q", night, dl)
			} else {
				t.Fatalf("night %d: sleeping_pos=%q — sleep never started", night, dl)
			}

			// The cat paths to the goal cell; within ~1.58 blocks of
			// the sleeper it settles — RELAX first, then IS_LYING.
			nightLying := false
			settle := time.Now().Add(7 * time.Second)
			for time.Now().Before(settle) && !nightLying {
				o := vehWatch(t, v, false, eid, 700*time.Millisecond)
				if o.boolMeta[22] {
					relaxed = true
				}
				if o.boolMeta[21] {
					nightLying = true
				}
			}
			if !nightLying {
				pl := dataGetLineDirect(t, v.ca, v.pra, v.pwa,
					"@e[type=minecraft:cat,sort=nearest,limit=1]", "Pos")
				t.Fatalf("night %d: cat never lay down (relaxed=%v, cat Pos=%q)", night, relaxed, pl)
			}

			// Night skip at sleepTicks>=100 (~5s) → wake: both flags
			// clear (a false emit) and a gift item may pop out at the
			// cat (p=0.7).
			nightCleared := false
			wakeWin := time.Now().Add(9 * time.Second)
			for time.Now().Before(wakeWin) && !nightCleared {
				o := vehWatch(t, v, false, eid, 700*time.Millisecond)
				giftItems += len(o.itemEIDs)
				if vv, ok := o.boolMeta[21]; ok && !vv {
					nightCleared = true
					cleared = true
				}
			}
		}
		// Confirm the last wake actually landed — sleeping_pos gone
		// means the sleeper is out of bed on both servers.
		awake := false
		posWin := time.Now().Add(6 * time.Second)
		for time.Now().Before(posWin) && !awake {
			if l := dataGetLineDirect(t, v.ca, v.pra, v.pwa, "@s", "sleeping_pos"); strings.Contains(l, "nothing_found") {
				awake = true
			}
		}
		if !awake {
			t.Fatalf("never woke (Pos=%q sleeping_pos=%q SleepTimer=%q)",
				dataGetLineDirect(t, v.ca, v.pra, v.pwa, "@s", "Pos"),
				dataGetLineDirect(t, v.ca, v.pra, v.pwa, "@s", "sleeping_pos"),
				dataGetLineDirect(t, v.ca, v.pra, v.pwa, "@s", "SleepTimer"))
		}
		if !relaxed {
			t.Fatalf("cat never set RELAX_STATE_ONE(22)")
		}
		if !cleared {
			t.Fatalf("IS_LYING never cleared on wake")
		}
		if giftItems < 1 {
			t.Fatalf("no cat_morning_gift item over 3 nights (p=0.7/night)")
		}
	}

	t.Run("vanilla", func(t *testing.T) { run(t, targets.vanilla) })
	t.Run("mc-go", func(t *testing.T) { run(t, targets.mcgo) })
}
