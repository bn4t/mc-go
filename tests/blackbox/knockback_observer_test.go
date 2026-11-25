package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// The melee knockback recipient set. Vanilla's Player.causeExtraKnockback
// sends ClientboundSetEntityMotionPacket through the victim's own
// connection (measured: the ServerPlayer branch sends connection.send,
// clears hurtMarked, then restores the pre-hit delta), so a third player
// tracking the victim must never receive the attack's push - the record
// delta is restored before any sendChanges pass can publish it. An
// observer can still receive the victim's ordinary tracker motion (a
// fall settle), so the pin compares the delta it sees against the
// victim's own knockback packet instead of expecting silence.

// runKnockbackObserverScenario stages one hit on a player victim and
// reports what the victim and a third viewer received. The attacker and
// victim join first, so the attacker's spawn table gives the victim's
// entity id (it is the only other player there); entity ids are global,
// and the observer then has to pair that same id before the hit.
func runKnockbackObserverScenario(t *testing.T, addr, attacker, victim, observer string) (observerMotions int, observerDelta [3]float64, victimMotions int, victimDelta [3]float64) {
	t.Helper()
	ca, pra, pwa := joinPlayBlackbox(t, addr, attacker)
	defer ca.Close()
	_ = ca.SetDeadline(time.Now().Add(120 * time.Second))
	cv, prv, pwv := joinPlayBlackbox(t, addr, victim)
	defer cv.Close()
	_ = cv.SetDeadline(time.Now().Add(120 * time.Second))

	var posA, posV [3]float64
	obsA, obsV := wearObs{}, wearObs{}
	spawnsA := map[int32]int32{}
	drainWear(t, cv, prv, pwv, &posV, 1200*time.Millisecond, &obsV, nil)
	drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, spawnsA)
	victimEID := spawnsA[server.EntityTypePlayer]
	if victimEID == 0 {
		t.Fatalf("%s: %s never saw %s spawn", addr, attacker, victim)
	}

	movePos := func(pw *protocol.Writer, x, y, z float64) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		b.Byte(0x01) // onGround: the grounded lift is what the hit composes against
		if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}

	// Park the victim far outside tracking range before the observer
	// joins; stepping it back into the arena is then a fresh pairing.
	sendCommand(t, pwa, "tp @s 4 -60 2 -90 0")
	sendCommand(t, pwv, "tp @s 1000 -60 2 0 0")
	drainWear(t, cv, prv, pwv, &posV, 900*time.Millisecond, &obsV, nil)
	drainWear(t, ca, pra, pwa, &posA, 900*time.Millisecond, &obsA, nil)

	co, pro, pwo := joinPlayBlackbox(t, addr, observer)
	defer co.Close()
	_ = co.SetDeadline(time.Now().Add(120 * time.Second))
	var posO [3]float64
	obsO := wearObs{}
	drainWear(t, co, pro, pwo, &posO, 900*time.Millisecond, &obsO, nil)

	sendCommand(t, pwv, "tp @s 6 -60 2 0 0")
	drainWear(t, cv, prv, pwv, &posV, 900*time.Millisecond, &obsV, nil)
	paired := false
	for try := 0; try < 12 && !paired; try++ {
		drainWear(t, co, pro, pwo, &posO, 400*time.Millisecond, &obsO, nil)
		if sp, ok := obsO.spawnedPos[victimEID]; ok && int32(sp[0]) == server.EntityTypePlayer {
			paired = true
		}
	}
	if !paired {
		t.Fatalf("%s: observer never tracked victim eid %d; spawn table %v", addr, victimEID, obsO.spawnedPos)
	}
	movePos(pwv, 6.5, -60, 2.5)
	movePos(pwa, 4.5, -60, 2.5)
	drainWear(t, cv, prv, pwv, &posV, 400*time.Millisecond, &obsV, nil)
	drainWear(t, ca, pra, pwa, &posA, 400*time.Millisecond, &obsA, nil)

	var atk protocol.Buffer
	atk.VarInt(victimEID)
	if err := pwa.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}
	poV, poO := wearObs{}, wearObs{}
	drainWear(t, cv, prv, pwv, &posV, 800*time.Millisecond, &poV, nil)
	drainWear(t, co, pro, pwo, &posO, 800*time.Millisecond, &poO, nil)
	return poO.motionCounts[victimEID], poO.motions[victimEID], poV.motionCounts[victimEID], poV.motions[victimEID]
}

// TestAttackKnockbackObserverParity pins the melee knockback recipient
// set: the victim gets exactly one motion packet carrying the composed
// push, and a third tracking player never gets that push.
func TestAttackKnockbackObserverParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{
		"gamemode": "survival",
	}, map[string]string{
		"ops.json": opsJSONFor("KAtt", "KVic", "KObs"),
	}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer tg.cleanup()

	wObs, wObsDelta, wVic, wDelta := runKnockbackObserverScenario(t, tg.vanilla, "KAtt", "KVic", "KObs")
	gObs, gObsDelta, gVic, gDelta := runKnockbackObserverScenario(t, tg.mcgo, "KAtt", "KVic", "KObs")
	samePush := func(obs, vic [3]float64) bool {
		return math.Abs(obs[0]-vic[0]) <= 0.1 && math.Abs(obs[2]-vic[2]) <= 0.1
	}
	if wObs > 0 && samePush(wObsDelta, wDelta) {
		t.Errorf("vanilla's observer received the victim's knockback %v (victim saw %v)", wObsDelta, wDelta)
	}
	if gObs > 0 && samePush(gObsDelta, gDelta) {
		t.Errorf("mc-go's observer received the victim's knockback %v (victim saw %v)", gObsDelta, gDelta)
	}
	if wVic != 1 || gVic != 1 {
		t.Errorf("victim saw %d/%d motion packets (vanilla/mc-go), want exactly one", wVic, gVic)
	}
	for ax := 0; ax < 3; ax++ {
		tol := 0.01
		if ax == 1 {
			// The victim keeps a small residual fall delta on vanilla,
			// like the two-client pin; xz is exact.
			tol = 0.06
		}
		if d := wDelta[ax] - gDelta[ax]; d < -tol || d > tol {
			t.Errorf("victim knockback: vanilla %v mc-go %v", wDelta, gDelta)
			break
		}
	}
}
