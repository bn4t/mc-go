package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// followParentObserve summons a NoAI adult pig at (5,-60,8) and a baby
// pig at (14,-60,8), then records the baby's tracked positions for the
// window. The adult is NoAI so it stays put — a static anchor.
func followParentObserve(t *testing.T, tag, addr, name, babyNBT string, window time.Duration) []panicSample {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(window + 30*time.Second))
	drainChunkWindow(t, tag, c, pr, pw, 400*time.Millisecond)
	sendCommand(t, pw, "tp @s 4.5 -60 -3.5 0 0")
	var mv protocol.Buffer
	mv.Double(4.5)
	mv.Double(-60)
	mv.Double(-3.5)
	mv.Byte(0x01)
	_ = pw.WritePacket(server.IDPlayMovePosSB, mv.Bytes())
	time.Sleep(300 * time.Millisecond)
	sendCommand(t, pw, "summon minecraft:pig 5 -60 8 {NoAI:1b}")
	sendCommand(t, pw, "summon minecraft:pig 12.5 -60 8 "+babyNBT)

	pigEIDs := map[int32]bool{}
	end := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(end) && len(pigEIDs) < 2 {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch int32(p.ID) {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case 1:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			r.UUID()
			ty, _ := r.VarInt()
			if n, _ := server.EntityTypeName(ty); n == "pig" {
				pigEIDs[eid] = true
			}
		}
	}
	if len(pigEIDs) < 2 {
		t.Fatalf("%s: wanted two pigs, saw %d", tag, len(pigEIDs))
	}
	// The baby is the second summon — the higher eid.
	var babyEID int32 = -1
	for eid := range pigEIDs {
		if eid > babyEID {
			babyEID = eid
		}
	}

	var samples []panicSample
	cur := [3]float64{12.5, -60, 8}
	start := time.Now()
	for time.Since(start) < window {
		_ = c.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch int32(p.ID) {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case 53, 54:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil || eid != babyEID {
				continue
			}
			dx, _ := r.Short()
			dy, _ := r.Short()
			dz, _ := r.Short()
			cur[0] += float64(dx) / 4096
			cur[1] += float64(dy) / 4096
			cur[2] += float64(dz) / 4096
			samples = append(samples, panicSample{time.Since(start).Milliseconds(), cur})
		case 35, 125:
			r := protocol.NewPacketReader(p.Data)
			eid, err := r.VarInt()
			if err != nil || eid != babyEID {
				continue
			}
			x, _ := r.Double()
			y, _ := r.Double()
			z, _ := r.Double()
			cur = [3]float64{x, y, z}
			samples = append(samples, panicSample{time.Since(start).Milliseconds(), cur})
		}
	}
	sendCommand(t, pw, "kill @e[type=minecraft:pig]")
	time.Sleep(1300 * time.Millisecond)
	return samples
}

// TestMobFollowParentParity pins FollowParentGoal: a baby pig within the
// follow window (3-16 blocks) of an adult of its species walks to ~3
// blocks of it and stays; a NoAI baby never approaches.
func TestMobFollowParentParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("FOLLOW"),
	}, nil)
	defer targets.cleanup()

	adultPos := [3]float64{5, -60, 8}
	minDist := func(samples []panicSample) float64 {
		best := math.MaxFloat64
		for _, sm := range samples {
			if d := math.Hypot(sm.pos[0]-adultPos[0], sm.pos[2]-adultPos[2]); d < best {
				best = d
			}
		}
		return best
	}

	t.Run("baby_follows", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			samples := followParentObserve(t, tc.tag, tc.addr, "FOLLOW", "{Age:-24000}", 9*time.Second)
			best := minDist(samples)
			t.Logf("%s: samples=%d minDistToAdult=%.2f", tc.tag, len(samples), best)
			if best >= 3.5 {
				t.Fatalf("%s: baby only closed to %.2f — FollowParentGoal should walk it to ~3 of the adult", tc.tag, best)
			}
		}
	})

	t.Run("noai_no_follow", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			samples := followParentObserve(t, tc.tag, tc.addr, "FOLLOW", "{Age:-24000,NoAI:1b}", 6*time.Second)
			best := minDist(samples)
			t.Logf("%s: samples=%d minDistToAdult=%.2f", tc.tag, len(samples), best)
			if best < 5.5 {
				t.Fatalf("%s: NoAI baby closed to %.2f — goals must be off for NoAI mobs", tc.tag, best)
			}
		}
	})
}
