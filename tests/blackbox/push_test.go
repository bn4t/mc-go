package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// pushObserve summons two pigs near the observer and tracks their
// tracked positions (spawn + delta + teleport streams) plus
// set_entity_motion counts over the window. Returns the final
// separation and per-entity motion emit counts.
func pushObserve(t *testing.T, tag, addr, name, secondPos string, window time.Duration) (float64, map[int32]int) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	// Bounded chunk drain: survivors of an earlier leg keep emitting on
	// mc-go (an un-paused world), so waiting for a quiet gap can starve.
	// Take the stream for at most 3s, still acking batches so the join
	// window lands.
	drainEnd := time.Now().Add(3 * time.Second)
	for time.Now().Before(drainEnd) {
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		if p.ID == server.IDPlayChunkBatchDone {
			_ = ackChunkBatch(pw)
		} else if p.ID == server.IDPlayKeepAliveCB {
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		}
	}
	sendCommand(t, pw, "summon minecraft:pig 8 -60 8 "+secondPos)
	time.Sleep(300 * time.Millisecond)
	sendCommand(t, pw, "summon minecraft:pig 8.4 -60 8 "+secondPos)
	pos := map[int32][3]float64{}
	pigIDs := map[int32]bool{}
	motion := map[int32]int{}
	// One absolute deadline for the whole window: a pair of active pigs
	// streams a packet every few ms, so per-read 80ms deadlines never
	// fire and the loop would never see the window end.
	_ = c.SetReadDeadline(time.Now().Add(window))
	for {
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		{
			if p.ID == server.IDPlayKeepAliveCB {
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				continue
			}
			if p.ID == server.IDPlayChunkBatchDone {
				_ = ackChunkBatch(pw)
				continue
			}
			switch int32(p.ID) {
			case 1:
				r := protocol.NewPacketReader(p.Data)
				eid, _ := r.VarInt()
				r.UUID()
				ty, _ := r.VarInt()
				if n, _ := server.EntityTypeName(ty); n != "pig" {
					continue
				}
				x, _ := r.Double()
				y, _ := r.Double()
				z, _ := r.Double()
				pos[eid] = [3]float64{x, y, z}
				pigIDs[eid] = true
			case 53, 54:
				r := protocol.NewPacketReader(p.Data)
				eid, err := r.VarInt()
				if err != nil || !pigIDs[eid] {
					continue
				}
				dx, _ := r.Short()
				dy, _ := r.Short()
				dz, _ := r.Short()
				cur := pos[eid]
				cur[0] += float64(dx) / 4096
				cur[1] += float64(dy) / 4096
				cur[2] += float64(dz) / 4096
				pos[eid] = cur
			case 125:
				r := protocol.NewPacketReader(p.Data)
				eid, err := r.VarInt()
				if err != nil || !pigIDs[eid] {
					continue
				}
				x, _ := r.Double()
				y, _ := r.Double()
				z, _ := r.Double()
				pos[eid] = [3]float64{x, y, z}
			case 101:
				r := protocol.NewPacketReader(p.Data)
				eid, err := r.VarInt()
				if err != nil || !pigIDs[eid] {
					continue
				}
				motion[eid]++
			}
		}
	}
	var ids []int32
	for id := range pigIDs {
		ids = append(ids, id)
	}
	if len(ids) != 2 {
		t.Fatalf("%s: expected 2 pigs got %d", tag, len(ids))
	}
	a, b := pos[ids[0]], pos[ids[1]]
	sep := math.Hypot(a[0]-b[0], a[2]-b[2])
	// Leave the world clean for the next leg: leftover pigs would
	// respawn to the next client (4 tracked pigs) and keep the join
	// drain saturated. Corpses linger ~20 ticks before removal.
	sendCommand(t, pw, "kill @e[type=minecraft:pig]")
	time.Sleep(1300 * time.Millisecond)
	return sep, motion
}

// TestMobPushParity covers LivingEntity.pushEntities' two measured
// behaviors: overlapping mobs shove apart through their deltaMovement,
// and NoAI mobs — whose travel never integrates the delta — sit
// overlapping while their growing delta storms set_entity_motion.
func TestMobPushParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("PUSH"),
	}, nil)
	defer targets.cleanup()

	// NoAI: the delta is pushed but never integrated — positions stay
	// overlapping and set_entity_motion fires every tick.
	t.Run("noai_overlap", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			sep, motion := pushObserve(t, tc.tag, tc.addr, "PUSH", "{NoAI:1b}", 6*time.Second)
			t.Logf("%s: sep=%.3f motion=%v", tc.tag, sep, motion)
			if sep > 0.3 {
				t.Fatalf("%s: NoAI pigs separated to %.3f — deltaMovement must not integrate without travel", tc.tag, sep)
			}
			for eid, n := range motion {
				if n < 20 {
					t.Fatalf("%s: pig %d emitted %d motion packets — the push delta should emit every tick", tc.tag, eid, n)
				}
			}
		}
	})

	// AI: the same push integrates into position — pigs separate past
	// the box-overlap distance (~0.9) and strolls widen it further.
	t.Run("ai_separates", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			sep, _ := pushObserve(t, tc.tag, tc.addr, "PUSH", "{}", 6*time.Second)
			t.Logf("%s: sep=%.3f", tc.tag, sep)
			if sep < 0.8 {
				t.Fatalf("%s: AI pigs stayed overlapped (sep=%.3f) — pushEntities should separate them", tc.tag, sep)
			}
		}
	})
}
