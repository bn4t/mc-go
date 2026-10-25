package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// wanderCounts summons a pig near the observer and returns packet-id →
// count for that entity over the window (pairing burst skipped).
func wanderCounts(t *testing.T, tag, addr, name string, window time.Duration) map[int32]int {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	drainChunkWindow(t, tag, c, pr, pw, 300*time.Millisecond)
	sendCommand(t, pw, "summon minecraft:pig 8 -60 8")
	time.Sleep(600 * time.Millisecond)
	deadline := time.Now().Add(window)
	var eid int32 = -1
	counts := map[int32]int{}
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == server.IDPlayKeepAliveCB {
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				continue
			}
			if p.ID == server.IDPlayChunkBatchDone {
				_ = ackChunkBatch(pw)
				continue
			}
			if eid < 0 && p.ID == server.IDPlaySpawnEntity {
				r := protocol.NewPacketReader(p.Data)
				id, _ := r.VarInt()
				r.UUID()
				ty, _ := r.VarInt()
				if n, _ := server.EntityTypeName(ty); n == "pig" {
					eid = id
				}
				continue
			}
			if eid < 0 {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			id, err := r.VarInt()
			if err == nil && id == eid {
				counts[int32(p.ID)]++
			}
		}
	}
	return counts
}

// TestWanderDensityParity compares per-class packet counts for an idle
// wandering pig over 15 seconds on both servers. Vanilla's ambient AI is
// episodic: RandomStrollGoal picks a target every ~6s, look goals re-aim
// the head, and TrackedDeltaMovement ships set_entity_motion on drift —
// the same classes must show up on mc-go at the same order of magnitude.
func TestWanderDensityParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("WDP"),
	}, nil)
	defer targets.cleanup()

	const posPos, posRot, rotHead, motion = 53, 54, 83, 101 // decimal ids
	van := wanderCounts(t, "vanilla", targets.vanilla, "WDP", 15*time.Second)
	mcg := wanderCounts(t, "mc-go", targets.mcgo, "WDP", 15*time.Second)
	t.Logf("vanilla: %v", van)
	t.Logf("mc-go:   %v", mcg)

	vanPos := van[posPos] + van[posRot]
	mcgPos := mcg[posPos] + mcg[posRot]
	if mcgPos == 0 {
		t.Fatal("mc-go emitted no position packets — the pig never strolled")
	}
	// Same order of magnitude: the deterministic stroll cadence is inside
	// vanilla's ~6s mean, so position emits stay within a 4x band.
	if mcgPos > 4*vanPos+20 || vanPos > 4*mcgPos+20 {
		t.Fatalf("position emit density diverged: vanilla %d mc-go %d", vanPos, mcgPos)
	}
	// Motion emits are the TrackedDeltaMovement regression pin — a stroll
	// must ship set_entity_motion (accel/decel/stop edges), never zero.
	if mcg[motion] == 0 {
		t.Fatal("mc-go emitted no set_entity_motion while strolling")
	}
	// Look goals produce rotate_head emits while idle and walking.
	if mcg[rotHead] == 0 {
		t.Fatal("mc-go emitted no rotate_head — look goals missing")
	}
}
