package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Mob victims take the knockback through the ordinary entity record:
// Player.causeExtraKnockback applies LivingEntity.knockback to the
// victim's delta for a non-player, and does not send anything itself;
// ServerEntity.sendChanges' hurt-marked tail publishes that delta once
// on the next tracker pass. A frozen world keeps the pig from moving
// afterwards, so every motion packet in the window is a candidate
// duplicate rather than physics.
func runMobKnockbackCount(t *testing.T, addr string) ([][3]float64, int) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "MKAtt")
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	var pos [3]float64
	obs := wearObs{}
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &obs, nil)

	sendCommand(t, pw, "tick freeze")
	sendCommand(t, pw, "summon minecraft:pig 6 -60 2 {NoAI:1b,PersistenceRequired:1b}")
	sendCommand(t, pw, "tp @s 4 -60 2 -90 0")
	var b protocol.Buffer
	b.Double(4.5)
	b.Double(-60)
	b.Double(2.5)
	b.Byte(0x01) // onGround
	if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	spawned := map[int32]int32{}
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &obs, spawned)
	pigType, _ := server.EntityTypeID("pig")
	pigEID := spawned[pigType]
	if pigEID == 0 {
		t.Fatalf("%s: no pig pairing arrived", addr)
	}

	obs = wearObs{}
	var atk protocol.Buffer
	atk.VarInt(pigEID)
	if err := pw.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &obs, nil)
	return obs.motionSeq[pigEID], obs.motionCounts[pigEID]
}

// TestMobKnockbackMotionCadenceParity pins one hit on a mob to the
// native two motion packets per tracking connection: the needsSync
// motion branch and the hurt-marked tail of the same sendChanges pass.
// mc-go used to emit the push inline from the attack handler as well,
// which made three.
func TestMobKnockbackMotionCadenceParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{
		"gamemode": "survival",
	}, map[string]string{
		"ops.json": opsJSONFor("MKAtt"),
	}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer tg.cleanup()

	wantDeltas, want := runMobKnockbackCount(t, tg.vanilla)
	gotDeltas, got := runMobKnockbackCount(t, tg.mcgo)
	if want != 2 {
		t.Errorf("vanilla emitted %d motion packets for the pig, want 2", want)
	}
	if got != 2 {
		t.Errorf("mc-go emitted %d motion packets for the pig, want 2", got)
	}
	// Both arms of the pass read the same record delta, so the two
	// packets must agree exactly; a duplicate carrying a stale or zero
	// value would still count two.
	sameDeltas := func(deltas [][3]float64) bool {
		for _, d := range deltas {
			if d != deltas[len(deltas)-1] {
				return false
			}
		}
		return true
	}
	if len(wantDeltas) == 2 && !sameDeltas(wantDeltas) {
		t.Errorf("vanilla's two motion packets differ: %v", wantDeltas)
	}
	if len(gotDeltas) == 2 && !sameDeltas(gotDeltas) {
		t.Errorf("mc-go's two motion packets differ: %v", gotDeltas)
	}
}
