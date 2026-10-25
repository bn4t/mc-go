package blackbox

import (
	"math"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// panicSample is one tracked pig position with its offset from the
// attack in milliseconds — spawn + move/posRot/position_sync/teleport
// streams composed like every other tracker test.
type panicSample struct {
	tms int64
	pos [3]float64
}

// panicObserve summons a pig 2.5 blocks east of the player, hits it
// once, and records the pig's tracked positions for the window.
func panicObserve(t *testing.T, tag, addr, name, nbt string, window time.Duration) ([]panicSample, [3]float64) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(window + 30*time.Second))
	drainChunkWindow(t, tag, c, pr, pw, 400*time.Millisecond)
	sendCommand(t, pw, "tp @s 4.5 -60 8.5 90 0")
	// Grounded so the hit doesn't take the airborne branch.
	var mv protocol.Buffer
	mv.Double(4.5)
	mv.Double(-60)
	mv.Double(8.5)
	mv.Byte(0x01)
	_ = pw.WritePacket(server.IDPlayMovePosSB, mv.Bytes())
	time.Sleep(400 * time.Millisecond)
	sendCommand(t, pw, "summon minecraft:pig 7 -60 8 "+nbt)

	var pigEID int32 = -1
	var spawnPos [3]float64
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
				x, _ := r.Double()
				y, _ := r.Double()
				z, _ := r.Double()
				pigEID = eid
				spawnPos = [3]float64{x, y, z}
			}
		}
	}
	if pigEID < 0 {
		t.Fatalf("%s: pig never spawned", tag)
	}
	time.Sleep(300 * time.Millisecond)

	var atk protocol.Buffer
	atk.VarInt(pigEID)
	_ = pw.WritePacket(server.IDPlayAttackSB, atk.Bytes())

	var samples []panicSample
	cur := spawnPos
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
	return samples, spawnPos
}

// TestMobPanicParity pins PanicGoal: a pig hit once sprints away right
// after the knockback — vanilla runs one or two bursts totalling 3-14
// blocks — while a NoAI pig takes the knockback and stands still.
func TestMobPanicParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("PANIC"),
	}, nil)
	defer targets.cleanup()

	// AI pig: position packets in (1.0s, 3.5s) mean it is still moving
	// after the knockback decays, and it must clear the knockback's
	// ~1.5-block reach within 3.5s.
	t.Run("panic_run", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			samples, spawn := panicObserve(t, tc.tag, tc.addr, "PANIC", "{}", 10*time.Second)
			moving := 0
			maxDist := 0.0
			for _, sm := range samples {
				d := math.Hypot(sm.pos[0]-spawn[0], sm.pos[2]-spawn[2])
				if sm.tms >= 1000 && sm.tms <= 3500 {
					moving++
					if d > maxDist {
						maxDist = d
					}
				}
			}
			t.Logf("%s: samples=%d moving(1-3.5s)=%d maxDist(1-3.5s)=%.2f", tc.tag, len(samples), moving, maxDist)
			if moving == 0 {
				t.Fatalf("%s: pig stopped after the knockback — PanicGoal should sprint it away", tc.tag)
			}
			if maxDist < 2.0 {
				t.Fatalf("%s: pig only moved %.2f in the panic window — beyond-knockback travel missing", tc.tag, maxDist)
			}
		}
	})

	// NoAI pig: the goal selector is off, so the same hit lands the
	// knockback and nothing else — a NoAI mob's deltaMovement never
	// integrates into position (travel is AI-gated), so the tracked
	// position stays inside the knockback's ~2-block reach.
	t.Run("noai_no_panic", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			samples, spawn := panicObserve(t, tc.tag, tc.addr, "PANIC", "{NoAI:1b}", 6*time.Second)
			maxDist := 0.0
			for _, sm := range samples {
				if sm.tms >= 1500 && sm.tms <= 5000 {
					if d := math.Hypot(sm.pos[0]-spawn[0], sm.pos[2]-spawn[2]); d > maxDist {
						maxDist = d
					}
				}
			}
			t.Logf("%s: samples=%d maxDist(1.5-5s)=%.2f", tc.tag, len(samples), maxDist)
			if maxDist > 2.5 {
				t.Fatalf("%s: NoAI pig travelled %.2f — the goal selector must be off, the knockback must not integrate", tc.tag, maxDist)
			}
		}
	})
}
