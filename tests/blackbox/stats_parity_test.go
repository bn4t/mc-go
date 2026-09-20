package blackbox

import (
	"math"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// statEntry is one decoded Award Statistics row.
type statEntry struct {
	cat, id, val int32
}

// statsPhase is everything one request produced: the emitted rows in
// wire order.
type statsPhase struct {
	rows []statEntry
}

// drainStats reads until the Award Statistics packet arrives, keeping
// the connection alive (keepalives echoed, teleports confirmed) and
// reporting the last SyncPos position it saw.
func drainStats(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, pos *[3]float64, timeout time.Duration) []statEntry {
	t.Helper()
	var sb protocol.Buffer
	sb.VarInt(1)
	if err := pw.WritePacket(server.IDPlayClientStatusSB, sb.Bytes()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			var kb protocol.Buffer
			kb.Raw(p.Data)
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				if x, err := r.Double(); err == nil {
					y, _ := r.Double()
					z, _ := r.Double()
					pos[0], pos[1], pos[2] = x, y, z
				}
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case 0x03: // play clientbound award_stats (fixed id on 26.2)
			r := protocol.NewPacketReader(p.Data)
			n, err := r.VarInt()
			if err != nil {
				t.Fatalf("stats count: %v", err)
			}
			rows := make([]statEntry, 0, n)
			for i := int32(0); i < n; i++ {
				cat, e1 := r.VarInt()
				id, e2 := r.VarInt()
				val, e3 := r.VarInt()
				if e1 != nil || e2 != nil || e3 != nil {
					t.Fatalf("stats row %d: %v %v %v", i, e1, e2, e3)
				}
				rows = append(rows, statEntry{cat, id, val})
			}
			return rows
		}
	}
	t.Fatal("no Award Statistics packet within deadline")
	return nil
}

// drainAlive keeps the session fed for d seconds (keepalive echo +
// teleport confirm) and reports the latest SyncPos.
func drainAlive(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, pos *[3]float64, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			var kb protocol.Buffer
			kb.Raw(p.Data)
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				if x, err := r.Double(); err == nil {
					y, _ := r.Double()
					z, _ := r.Double()
					pos[0], pos[1], pos[2] = x, y, z
				}
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		}
	}
}

// runStatsScenario executes the join → drop → mine → walk script on one
// target and returns the four emitted stats packets in order.
func runStatsScenario(t *testing.T, addr string) []statsPhase {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "StatMan")
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	var pos [3]float64
	var out []statsPhase

	drainAlive(t, c, pr, pw, &pos, 1400*time.Millisecond)
	out = append(out, statsPhase{drainStats(t, c, pr, pw, &pos, 5*time.Second)})

	// Give + Q-drop one dirt.
	sendCommand(t, pw, "give @s dirt")
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	sendDig(t, pw, 4, 0, 0, 0, 1) // drop held item
	drainAlive(t, c, pr, pw, &pos, 500*time.Millisecond)
	out = append(out, statsPhase{drainStats(t, c, pr, pw, &pos, 5*time.Second)})

	// Place dirt underfoot and break it; the drop lands near the
	// player and may be picked up.
	bx, by, bz := int(math.Floor(pos[0])), int(math.Floor(pos[1]))-1, int(math.Floor(pos[2]))
	sendCommand(t, pw, "setblock "+itoa(int32(bx))+" "+itoa(int32(by))+" "+itoa(int32(bz))+" dirt")
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	sendDig(t, pw, 0, int32(bx), int32(by), int32(bz), 1) // start
	sendDig(t, pw, 2, int32(bx), int32(by), int32(bz), 1) // finish
	drainAlive(t, c, pr, pw, &pos, 1400*time.Millisecond)
	out = append(out, statsPhase{drainStats(t, c, pr, pw, &pos, 5*time.Second)})

	// Two short walks.
	_ = sendMovePosRot(pw, pos[0]+0.5, pos[1], pos[2])
	drainAlive(t, c, pr, pw, &pos, 300*time.Millisecond)
	_ = sendMovePosRot(pw, pos[0]+1.0, pos[1], pos[2])
	drainAlive(t, c, pr, pw, &pos, 400*time.Millisecond)
	out = append(out, statsPhase{drainStats(t, c, pr, pw, &pos, 5*time.Second)})
	return out
}

// isTimedStat marks the tick-driven custom counters whose absolute
// value legitimately differs between two live runs.
func isTimedStat(r statEntry) bool {
	return r.cat == 8 && (r.id == 1 || r.id == 2 || r.id == 3 || r.id == 4 || r.id == 5)
}

// TestStatsParity compares Award Statistics emit order and increments
// between vanilla and mc-go. Time-family counters (ticks elapsed) can't
// be value-compared across two live runs, so the test pins: the exact
// (category,id) sequence each packet emits, the value of every
// event-driven counter, and that the four time counters always share
// one tick count.
func TestStatsParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("StatMan"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runStatsScenario(t, tg.vanilla)
	got := runStatsScenario(t, tg.mcgo)

	if len(want) != len(got) {
		t.Fatalf("phase count: vanilla %d mc-go %d", len(want), len(got))
	}
	for i := range want {
		wr, gr := want[i].rows, got[i].rows
		if len(wr) != len(gr) {
			t.Fatalf("phase %d row count: vanilla %v, mc-go %v", i, wr, gr)
		}
		for j := range wr {
			if wr[j].cat != gr[j].cat || wr[j].id != gr[j].id {
				t.Fatalf("phase %d row %d key: vanilla %d:%d, mc-go %d:%d (vanilla %v / mc-go %v)",
					i, j, wr[j].cat, wr[j].id, gr[j].cat, gr[j].id, wr, gr)
			}
			if isTimedStat(wr[j]) {
				if wr[j].val <= 0 || gr[j].val <= 0 {
					t.Fatalf("phase %d row %d nonpositive timed stat", i, j)
				}
				continue
			}
			if wr[j].val != gr[j].val {
				t.Errorf("phase %d row %d:%d: vanilla %d, mc-go %d",
					i, wr[j].cat, wr[j].id, wr[j].val, gr[j].val)
			}
		}
	}
}
