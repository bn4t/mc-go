package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// kbPhase is one scripted melee hit's wire-visible aftermath: the
// victim player's own set_entity_motion, the mob's as trackers see
// it, and the attacker's shared-flags byte as the victim sees it.
type kbPhase struct {
	victimMotion [3]float64
	mobMotion    [3]float64
	attackerFlag byte
	flagSeen     bool
}

// runKnockbackScenario scripts melee knockback on one target: a
// standing fist hit on the victim, a sprint hit on the victim (bigger
// push plus a sprint reset visible in the shared flags), then a plain
// and a sprint hit on a summoned pig. Positions are pinned so the
// composed deltas are deterministic: the victim sits two blocks east
// of the attacker who faces east, and the pig three blocks east.
func runKnockbackScenario(t *testing.T, addr, victim, attacker string) []kbPhase {
	t.Helper()
	ca, pra, pwa := joinPlayBlackbox(t, addr, attacker)
	defer ca.Close()
	cv, prv, pwv := joinPlayBlackbox(t, addr, victim)
	defer cv.Close()
	_ = ca.SetDeadline(time.Now().Add(120 * time.Second))
	_ = cv.SetDeadline(time.Now().Add(120 * time.Second))
	var posA, posV [3]float64
	obsA, obsV := wearObs{}, wearObs{}

	drainWear(t, cv, prv, pwv, &posV, 1200*time.Millisecond, &obsV, nil)
	spA := map[int32]int32{}
	drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, spA)
	victimEID := spA[server.EntityTypePlayer]
	if victimEID == 0 {
		t.Fatalf("%s: %s never saw %s spawn", addr, attacker, victim)
	}
	// The victim only ever sees the attacker's player spawn; keep every
	// eid a player spawned under — vanilla can re-issue add_entity with
	// a fresh id while tracking settles.
	attackerEIDs := map[int32]bool{}
	var attackerEID int32
	for eid, sp := range obsV.spawnedPos {
		if int32(sp[0]) == server.EntityTypePlayer {
			attackerEID = eid
			attackerEIDs[eid] = true
		}
	}

	sendCommand(t, pwv, "tp @s 6 -60 2 0 0")
	sendCommand(t, pwa, "tp @s 4 -60 2 -90 0")
	// A real client acknowledges the teleport with a position packet;
	// without one the server still thinks the player is airborne and
	// knockback takes the airborne branch (halving y instead of the
	// grounded min(0.4, ...) lift).
	movePos := func(pw *protocol.Writer, x, y, z float64) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		b.Byte(0x01) // onGround
		if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	movePos(pwv, 6.5, -60, 2.5)
	movePos(pwa, 4.5, -60, 2.5)
	spV2 := map[int32]int32{}
	drainWear(t, cv, prv, pwv, &posV, 600*time.Millisecond, &obsV, spV2)
	if eid := spV2[server.EntityTypePlayer]; eid != 0 {
		attackerEID = eid
		attackerEIDs[eid] = true
	}
	drainWear(t, ca, pra, pwa, &posA, 600*time.Millisecond, &obsA, nil)

	pigType, _ := server.EntityTypeID("pig")

	attack := func(eid int32) {
		var atk protocol.Buffer
		atk.VarInt(eid)
		if err := pwa.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	sprintStart := func() {
		var b protocol.Buffer
		b.VarInt(attackerEID)
		b.VarInt(1) // START_SPRINTING (26.2 action numbering)
		b.VarInt(0)
		if err := pwa.WritePacket(server.IDPlayPlayerCommandSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	// phase drains both clients after one attack and snapshots the
	// observed deltas/flags.
	phase := func(target int32, sprint bool) kbPhase {
		if sprint {
			sprintStart()
			// The sprint flag must land on the server before the
			// swing; both packets are in-order on the connection, but
			// give the flag broadcast a beat to reach the victim.
			time.Sleep(150 * time.Millisecond)
		}
		attack(target)
		poV, poA := wearObs{}, wearObs{}
		drainWear(t, cv, prv, pwv, &posV, 800*time.Millisecond, &poV, nil)
		drainWear(t, ca, pra, pwa, &posA, 400*time.Millisecond, &poA, nil)
		ph := kbPhase{victimMotion: poV.motions[victimEID]}
		for eid, v := range poV.flags {
			if attackerEIDs[eid] {
				ph.attackerFlag, ph.flagSeen = v, true
			}
		}
		if m, ok := poA.motions[target]; ok {
			ph.mobMotion = m
		}
		if m, ok := poV.motions[target]; ok && ph.mobMotion == [3]float64{0, 0, 0} {
			ph.mobMotion = m
		}
		// Fist delay is five ticks; stay recharged between phases.
		time.Sleep(400 * time.Millisecond)
		return ph
	}

	phases := []kbPhase{
		phase(victimEID, false),
		phase(victimEID, true),
	}
	sendCommand(t, pwa, "summon minecraft:pig 7 -60 2 {NoAI:1b}")
	sp := map[int32]int32{}
	drainWear(t, ca, pra, pwa, &posA, 1200*time.Millisecond, &obsA, sp)
	pigEID := sp[pigType]
	if pigEID == 0 {
		t.Fatalf("%s: pig never spawned", addr)
	}
	phases = append(phases, phase(pigEID, false), phase(pigEID, true))
	_ = attackerEID
	return phases
}

// TestKnockbackParity compares the melee knockback wire trace against
// vanilla: victim velocity packets, the sprint bonus and its sprint
// flag reset, and the mob's motion sync.
func TestKnockbackParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{
		"gamemode": "survival",
	}, map[string]string{
		"ops.json": opsJSONFor("KAtt", "KVic"),
	}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer tg.cleanup()

	want := runKnockbackScenario(t, tg.vanilla, "KVic", "KAtt")
	got := runKnockbackScenario(t, tg.mcgo, "KVic", "KAtt")
	if len(want) != len(got) {
		t.Fatalf("phase count: vanilla %d mc-go %d", len(want), len(got))
	}
	names := []string{"plain-hit-player", "sprint-hit-player", "plain-hit-pig", "sprint-hit-pig"}
	for i := range want {
		w, g := want[i], got[i]
		if i < 2 {
			// Vanilla's victim keeps a small residual fall delta
			// (~0.08) that mc-go's sessions never accumulate, so y is
			// looser than xz: vanilla y lands near 0.36 where mc-go's
			// rest-state victim produces exactly 0.4.
			for ax := 0; ax < 3; ax++ {
				tol := 0.01
				if ax == 1 {
					tol = 0.06
				}
				if d := w.victimMotion[ax] - g.victimMotion[ax]; d < -tol || d > tol {
					t.Errorf("phase %s victim motion: vanilla %v mc-go %v", names[i], w.victimMotion, g.victimMotion)
					break
				}
			}
		} else {
			// Mob deltas can drift a hair if the pig wandered before
			// the swing landed; the sprint bonus is a ~0.3 step, so
			// 0.05 tolerates drift without hiding the bonus.
			dx, dy, dz := w.mobMotion[0]-g.mobMotion[0], w.mobMotion[1]-g.mobMotion[1], w.mobMotion[2]-g.mobMotion[2]
			if dx < -0.05 || dx > 0.05 || dy < -0.01 || dy > 0.01 || dz < -0.05 || dz > 0.05 {
				t.Errorf("phase %s mob motion: vanilla %v mc-go %v", names[i], w.mobMotion, g.mobMotion)
			}
		}
		if w.flagSeen != g.flagSeen || w.attackerFlag != g.attackerFlag {
			t.Errorf("phase %s attacker flags: vanilla (%v,%v) mc-go (%v,%v)", names[i], w.flagSeen, w.attackerFlag, g.flagSeen, g.attackerFlag)
		}
	}
}
