package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// temptObserve summons a pig ~7.6 blocks east of the player, puts its
// tempting item in the player's held slot, and records the pig's
// tracked positions for the window.
func temptObserve(t *testing.T, tag, addr, name, nbt string, window time.Duration) []panicSample {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(window + 30*time.Second))
	drainChunkWindow(t, tag, c, pr, pw, 400*time.Millisecond)
	sendCommand(t, pw, "tp @s 4.5 -60 8.5 90 0")
	var mv protocol.Buffer
	mv.Double(4.5)
	mv.Double(-60)
	mv.Double(8.5)
	mv.Byte(0x01)
	_ = pw.WritePacket(server.IDPlayMovePosSB, mv.Bytes())
	time.Sleep(400 * time.Millisecond)
	sendCommand(t, pw, "summon minecraft:pig 12 -60 8 "+nbt)
	// The tempting item lands directly in the selected hotbar slot.
	sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:carrot")
	// Mirror the client's held-slot select so both servers agree the
	// carrot is in hand.
	var hb protocol.Buffer
	hb.Short(0)
	_ = pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes())

	var pigEID int32 = -1
	end := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(end) && pigEID < 0 {
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
				r.Double()
				r.Double()
				r.Double()
				pigEID = eid
			}
		}
	}
	if pigEID < 0 {
		t.Fatalf("%s: pig never spawned", tag)
	}

	var samples []panicSample
	cur := [3]float64{12, -60, 8}
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
			if err != nil || eid != pigEID {
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
			if err != nil || eid != pigEID {
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

// TestMobTemptParity pins TemptGoal: a pig within TEMPT_RANGE (10) of a
// player holding a PIG_FOOD item walks to the 2.5-block stop distance
// and stands there; a NoAI pig never moves.
func TestMobTemptParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("TEMPT"),
	}, nil)
	defer targets.cleanup()

	playerPos := [3]float64{4.5, -60, 8.5}
	minDist := func(samples []panicSample, lo, hi int64) float64 {
		best := math.MaxFloat64
		for _, sm := range samples {
			if sm.tms >= lo && sm.tms <= hi {
				if d := math.Hypot(sm.pos[0]-playerPos[0], sm.pos[2]-playerPos[2]); d < best {
					best = d
				}
			}
		}
		return best
	}

	// AI pig + held carrot: closes ~5 blocks to the stop distance
	// inside the first seconds and stays latched near the player.
	t.Run("tempt_approach", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			samples := temptObserve(t, tc.tag, tc.addr, "TEMPT", "{}", 9*time.Second)
			best := minDist(samples, 0, 8000)
			t.Logf("%s: samples=%d minDist(0-8s)=%.2f", tc.tag, len(samples), best)
			if best >= 3.5 {
				t.Fatalf("%s: pig only closed to %.2f — TemptGoal should walk it to the 2.5 stop distance", tc.tag, best)
			}
		}
	})

	// NoAI pig + held carrot: the goal selector is off, so the pig
	// never approaches regardless of the held item.
	t.Run("noai_no_tempt", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			samples := temptObserve(t, tc.tag, tc.addr, "TEMPT", "{NoAI:1b}", 6*time.Second)
			best := minDist(samples, 1500, 5000)
			t.Logf("%s: samples=%d minDist(1.5-5s)=%.2f", tc.tag, len(samples), best)
			if best < 5.0 {
				t.Fatalf("%s: NoAI pig closed to %.2f — goals must be off for NoAI mobs", tc.tag, best)
			}
		}
	})
}
