package blackbox

// TestMoveStatParity pins checkMovementStatistics' 26.2 branch order
// — swim(3D) > eyes-under-water(3D) > feet-in-water(XZ) > climb >
// grounded sprint/crouch/walk > airborne fly (XZ, >25cm) — plus
// checkRidingStatistics' vehicle bucket (isPassenger returns early in
// the walk path; the ridden delta lands on the vehicle's own stat),
// by running an identical water/move/ride script on both servers and
// diffing the final Award Statistics table row for row. Deterministic
// legs compare values; the minecart leg compares key + positivity
// (roll distance over a wall-time window legitimately differs).

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func moveStatRun(t *testing.T, addr string) []statEntry {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "MoveStat")
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(180 * time.Second))
	var pos [3]float64

	move := func(x, y, z float64, onGround bool) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		var f byte
		if onGround {
			f = 0x01
		}
		b.Byte(f)
		if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	sprint := func(on bool) {
		var b protocol.Buffer
		b.VarInt(0) // entity id unused by the command action
		if on {
			b.VarInt(0) // start sprinting
		} else {
			b.VarInt(1) // stop sprinting
		}
		b.VarInt(0)
		if err := pw.WritePacket(server.IDPlayPlayerCommandSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
	}

	drainAlive(t, c, pr, pw, &pos, 1400*time.Millisecond)
	drainStats(t, c, pr, pw, &pos, 5*time.Second)

	bx, by, bz := int32(8), int32(-60), int32(8)
	sendCommand(t, pw, "tp @s 8 -60 8")
	drainAlive(t, c, pr, pw, &pos, 400*time.Millisecond)

	// Grounded walk: XZ leg only.
	move(float64(bx)+0.5, float64(by), float64(bz), true)
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)

	// Feet-in-water (eyes clear): water one cell deep underfoot.
	sendCommand(t, pw, "fill 7 -60 7 9 -60 9 minecraft:water")
	sendCommand(t, pw, "tp @s 8 -60 8")
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	move(float64(bx)+0.4, float64(by), float64(bz), true)
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)

	// Eyes under water: three cells deep — a diagonal move splits
	// XZ (40cm) from full 3D (50cm).
	sendCommand(t, pw, "fill 7 -59 7 9 -58 9 minecraft:water")
	sendCommand(t, pw, "tp @s 8 -60 8")
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	move(float64(bx)+0.4, float64(by)+0.3, float64(bz), false)
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)

	// Sprint while submerged: swim_one_cm takes the 3D leg.
	sprint(true)
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	move(float64(bx)+0.8, float64(by)+0.3, float64(bz), false)
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	sprint(false)

	// Airborne leg > 25cm horizontal: fly_one_cm, no fall stat. The
	// move stays clear of solids — a packet into a colliding cell is
	// rejected ("moved wrongly") on both servers before stats run.
	sendCommand(t, pw, "fill 7 -60 7 9 -58 9 minecraft:air")
	sendCommand(t, pw, "tp @s 8 -60 8")
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	move(float64(bx)+1.0, float64(by), float64(bz), false)
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)

	// Riding: a rolling minecart banks minecart_one_cm for its
	// passenger — the ride path, not checkMovementStatistics.
	sendCommand(t, pw, "summon minecraft:minecart 8 -60 8 {Motion:[0.4,0.0,0.0]}")
	sendCommand(t, pw, "ride @s mount @e[type=minecraft:minecart,limit=1]")
	drainAlive(t, c, pr, pw, &pos, 1500*time.Millisecond)

	return drainStats(t, c, pr, pw, &pos, 5*time.Second)
}

// isRidingStat marks the vehicle distance counters whose value a
// wall-time window legitimately differs across servers — minecart 14,
// boat 15, pig 16, happy_ghast 17, horse 18, strider 21, nautilus 22
// (aviate 19 and swim 20 are deterministic legs, not riding).
func isRidingStat(r statEntry) bool {
	return r.cat == 8 && (r.id >= 14 && r.id <= 18 || r.id == 21 || r.id == 22)
}

func TestMoveStatParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("MoveStat"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := moveStatRun(t, tg.vanilla)
	got := moveStatRun(t, tg.mcgo)

	if len(want) != len(got) {
		t.Fatalf("row count: vanilla %v, mc-go %v", want, got)
	}
	for i := range want {
		wr, gr := want[i], got[i]
		if wr.cat != gr.cat || wr.id != gr.id {
			t.Fatalf("row %d key: vanilla %d:%d, mc-go %d:%d (vanilla %v / mc-go %v)",
				i, wr.cat, wr.id, gr.cat, gr.id, want, got)
		}
		if isTimedStat(wr) || isRidingStat(wr) {
			if wr.val <= 0 || gr.val <= 0 {
				t.Errorf("row %d nonpositive exempt stat: vanilla %d mc-go %d", i, wr.val, gr.val)
			}
			continue
		}
		if wr.val != gr.val {
			t.Errorf("row %d %d:%d: vanilla %d, mc-go %d", i, wr.cat, wr.id, wr.val, gr.val)
		}
	}
}
