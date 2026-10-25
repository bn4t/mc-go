package blackbox

// Which columns a joining client is sent first.
//
// A client that never acknowledges a chunk batch is sent one batch of
// nine columns and then nothing at all (TestChunkThrottleParity), so
// those nine are all the world that client will ever have. It has to be
// able to stand in them: a client sent nine columns from the corner of
// its view has no ground under its feet and renders nothing, however
// many columns its window holds.
//
// The real server's order is not reproducible from outside - measured on
// 26.2 at view distance 3 with a client that keeps acknowledging, its
// eighty-one columns arrived [0 -1] [1 -1] [-1 -1] [0 0] [0 -2] [1 0]
// [-1 -2] [-1 0] [1 -2] [2 -1] ... which is the order its own generation
// finished in and not a sort of anything. What is comparable is the
// property the order has to have: the player's own column is in the
// opening batch, on both servers. mc-go used to fail it - its nine were
// the leftmost edge of the window, which is what a queue built by
// scanning dx then dz produces - and that is the bug this test is here
// for.
//
// The rest of the stream is paced by the client's acknowledgement and
// nothing else, so there is no second batch to compare the order of: a
// client that has acknowledged is sent ten batches at the rate it asked
// for, and which columns land in them is a size question, which is what
// TestChunkThrottleParity measures.
import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// openingBatch is the size of the one batch a client is sent before it
// has acknowledged anything, measured on 26.2.
const openingBatch = 9

// TestChunkOpeningBatchParity requires both servers to open a player's
// stream with the ground the player is standing on.
func TestChunkOpeningBatchParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	// The client's own column: the shared harness spawns players at the
	// flat world's origin, which is the corner of column (0,0).
	own := [2]int{0, 0}

	for _, srv := range []struct{ tag, addr string }{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	} {
		cols, done := openingColumns(t, srv.addr, "Open"+srv.tag)
		if len(cols) != openingBatch {
			t.Errorf("%s: opening batch of %d columns, want %d", srv.tag, len(cols), openingBatch)
		}
		found := false
		for _, c := range cols {
			if c == own {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: the opening batch does not hold the player's own column %v: %v",
				srv.tag, own, cols)
		}
		// The columns are also all close to the player: the real server's
		// opening nine are its nearest nine by generation order, and a
		// batch of columns from across the window would satisfy the check
		// above while still leaving the client unable to see itself. Two
		// columns is what the measurement allows.
		for _, c := range cols {
			if d := abs(c[0] - own[0]); d > 2 {
				t.Errorf("%s: opening batch holds column %v, %d columns away from the player",
					srv.tag, c, d)
			}
			if d := abs(c[1] - own[1]); d > 2 {
				t.Errorf("%s: opening batch holds column %v, %d columns away from the player",
					srv.tag, c, d)
			}
		}
		t.Logf("%s: opening batch %v (batches: %v)", srv.tag, cols, done)
	}
}

// openingColumns joins a server, announces a view distance, and returns
// the columns of the first chunk batch it is sent - the batch that
// arrives before the client has acknowledged anything.
func openingColumns(t *testing.T, addr, name string) ([][2]int, []int) {
	t.Helper()
	c, pr, pw := joinPlayQuiet(t, addr, name)
	defer c.Close()
	sendClientInfo(t, pw, 3)

	var cols [][2]int
	var sizes []int
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := c.SetReadDeadline(deadline); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s: no opening batch: %v", name, err)
		}
		switch p.ID {
		case server.IDPlayChunkData:
			r := protocol.NewPacketReader(p.Data)
			x, e1 := r.Int()
			z, e2 := r.Int()
			if e1 == nil && e2 == nil {
				cols = append(cols, [2]int{int(x), int(z)})
			}
		case server.IDPlayChunkBatchDone:
			if n, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				sizes = append(sizes, int(n))
			}
			return cols, sizes
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		}
	}
	t.Fatalf("%s: the first batch never finished", name)
	return nil, nil
}

// abs is the absolute value of an int.
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
