package blackbox

// Chunk streaming throttle parity: how each server paces the columns it
// sends, and what a client has to do to be sent any at all.
//
// A real client reports how fast it can take chunks. Every time a Chunk
// Batch Done arrives it answers with Chunk Batch Received, carrying the
// number of chunks a tick it wants, and measured on 26.2 that
// acknowledgement is what releases the stream. One acknowledgement buys
// exactly ten batches:
//
//   - before any acknowledgement the client is sent one batch of nine
//     columns and then nothing at all, however long it waits and whatever
//     view distance it asks for;
//   - an acknowledgement of R chunks a tick buys ten batches of R
//     columns, one batch a tick, with no further acknowledgements needed.
//     For a view distance of 3 that turns the remaining 72 columns into
//     25, 25 and 22 after a rate of 25, and into an exact ten columns
//     after a rate of 1, which then falls silent again;
//   - the rate is truncated and clamped to [1, 64], so 1000 buys batches
//     of 64;
//   - a rate below one is still a batch of one column, but spaced
//     `ceil(1/R)` ticks apart, so 0.1 gives ten columns over five
//     seconds.
//
// The client scripts below run against both servers and the batch sizes
// they see are compared exactly, because a stream that is merely the
// right size is not enough: the sizes say whether the pacing is right.
import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

const throttleName = "ChunkThr"

// throttleCase is one scripted client. A client that never acknowledges
// is the case that matters most - it is what tells a server that paces
// its streaming from one that simply sends the world.
type throttleCase struct {
	name string
	vd   int
	// rate is what the client answers the first finished batch with; a
	// negative rate means it never answers at all.
	rate float32
	// wait is how long it holds that answer back. A real client answers a
	// batch the moment it lands, which is what every case but one does; a
	// rate of 1000 wants a batch of 64 columns at once, and the real
	// server can only send what it has generated, so that case gives it
	// two seconds to finish the window first.
	wait time.Duration
	// after is how long it keeps listening once that answer is out, or
	// from the start for a client that never answers.
	after time.Duration
	// chunks and sizes are what the real server was measured to send.
	chunks int
	sizes  []int
}

// The hold-back is not decoration. A batch a server sends at a rate above
// one is only as large as it has columns to send, and the real server
// generates a column when it is first asked for it: measured on 26.2, a
// view distance of 3 acknowledged at 25 without the pause came back as
// 9, 25, 25, 15, 7 - the last twenty-two columns split at fifteen and
// seven because only fifteen had finished generating by the tick the
// batch was due. Two seconds first makes every column of the window
// ready, and the same acknowledgement then comes back 9, 25, 25, 22.
// mc-go has its whole world in memory, so the split is a property of its
// pacing alone, which is what this test compares.

var throttleCases = []throttleCase{
	{"Open2", 2, -1, 0, 2 * time.Second, 9, []int{9}},
	{"Open8", 8, -1, 0, 2 * time.Second, 9, []int{9}},
	{"Ack25", 3, 25, 2 * time.Second, 3 * time.Second, 81, []int{9, 25, 25, 22}},
	{"Ack1", 3, 1, 0, 3 * time.Second, 19, []int{9, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}},
	{"AckHuge", 3, 1000, 2 * time.Second, 3 * time.Second, 81, []int{9, 64, 8}},
	{"AckSlow", 3, 0.1, 0, 6 * time.Second, 19, []int{9, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}},
}

// sendClientInfo announces the settings a real client sends when it
// enters play, with the view distance it renders at. Vanilla disconnects
// a play-stage client that never announces them, so every client here
// does, as the tests that compare play-stage packets all do.
func sendClientInfo(t *testing.T, pw *protocol.Writer, vd int) {
	t.Helper()
	var b protocol.Buffer
	b.String("en_us")
	b.Byte(byte(vd))
	b.VarInt(0)   // chat mode: enabled
	b.Bool(true)  // chat colors
	b.Byte(0x7F)  // skin parts: all
	b.VarInt(1)   // main hand: right
	b.Bool(false) // text filtering
	b.Bool(true)  // allow server listings
	b.VarInt(0)   // particle status: all
	if err := pw.WritePacket(server.IDPlayClientInfoSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// throttleScript runs one case against one server: it joins, announces
// its view distance, answers the first finished batch if the case calls
// for it, and reports what it was sent.
func throttleScript(t *testing.T, addr string, tc throttleCase) (chunks int, sizes []int, batches []time.Duration) {
	t.Helper()
	c, pr, pw := joinPlayQuiet(t, addr, throttleName+tc.name)
	defer c.Close()
	sendClientInfo(t, pw, tc.vd)
	start := time.Now()
	deadline := start.Add(tc.wait + tc.after)
	answered := tc.rate < 0
	for {
		if !time.Now().Before(deadline) {
			return chunks, sizes, batches
		}
		if err := c.SetReadDeadline(deadline); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		p, err := pr.ReadPacket()
		if err != nil {
			// The listener ran out of time, which is the normal end of a
			// case: a server that keeps streaming would have kept reading.
			return chunks, sizes, batches
		}
		switch p.ID {
		case server.IDPlayChunkData:
			chunks++
		case server.IDPlayChunkBatchDone:
			batches = append(batches, time.Since(start))
			if n, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				sizes = append(sizes, int(n))
			}
			if !answered {
				answered = true
				// Nothing more is due until the answer goes out, so the
				// hold-back is spent here rather than in the read loop.
				if tc.wait > 0 {
					time.Sleep(tc.wait)
				}
				var b protocol.Buffer
				b.Float(tc.rate)
				if err := pw.WritePacket(server.IDPlayChunkBatchRecvSB, b.Bytes()); err != nil {
					t.Fatalf("%s: ack: %v", tc.name, err)
				}
				// What the acknowledgement buys is counted from here, so
				// the wait covers ten batches at a slow rate.
				deadline = time.Now().Add(tc.after)
			}
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		}
	}
}

// TestChunkThrottleParity requires both servers to pace their streaming
// the same way: the same batches of the same sizes, at the same times
// within a tick or two, for a client that never acknowledges and for
// clients whose single acknowledgement asks for every rate a real one
// might.
func TestChunkThrottleParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	for _, tc := range throttleCases {
		vChunks, vSizes, vAt := throttleScript(t, tg.vanilla, tc)
		mChunks, mSizes, mAt := throttleScript(t, tg.mcgo, tc)

		// The measurement itself first, so a case that stops measuring
		// anything at all cannot pass by accident.
		if vChunks != tc.chunks || !sameInts(vSizes, tc.sizes) {
			t.Errorf("%s: vanilla sent %d chunks in batches %v, want %d in %v",
				tc.name, vChunks, vSizes, tc.chunks, tc.sizes)
		}
		if mChunks != vChunks || !sameInts(mSizes, vSizes) {
			t.Errorf("%s: mc-go sent %d chunks in batches %v, vanilla %d in %v",
				tc.name, mChunks, mSizes, vChunks, vSizes)
			continue
		}
		// A batch is due one a tick, and the acknowledgement's first batch
		// at once, so the times have to line up batch for batch.
		if !sameTimes(vAt, mAt, 120*time.Millisecond) {
			t.Errorf("%s: batch times do not line up:\n vanilla %v\n mc-go   %v",
				tc.name, vAt, mAt)
		}
		t.Logf("%s: %d chunks in batches %v, batches at %v", tc.name, mChunks, mSizes, mAt)
	}
}

// TestChunkThrottleStaysBlocked proves the pre-acknowledgement silence is
// a property of the player rather than of the window it was sent: a
// client that never acknowledges stays silent when it is teleported four
// hundred blocks into country it has never seen, even though the whole
// new window is waiting for it and the teleport is confirmed.
func TestChunkThrottleStaysBlocked(t *testing.T) {
	const name = throttleName + "Traveler"
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(name)}, nil)
	defer tg.cleanup()

	for _, srv := range []struct{ tag, addr string }{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	} {
		c, pr, pw := joinPlayBlackbox(t, srv.addr, name)
		sendClientInfo(t, pw, 3)
		// Every teleport is confirmed as it arrives, the join one
		// included: a real client confirms it, and a server is entitled to
		// hold a window back from a client that has not.
		confirm := func(p protocol.Packet) {
			if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		}
		// The whole opening batch first: nine columns, and then silence.
		// Reading only as far as the first column would leave the rest of
		// the batch to be counted after the teleport as though the new
		// window were streaming.
		if err := c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		opened := 0
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s: no opening batch: %v", srv.tag, err)
			}
			switch p.ID {
			case server.IDPlayChunkData:
				opened++
			case server.IDPlayChunkBatchDone:
				if opened != 9 {
					t.Fatalf("%s: opening batch of %d columns, want 9", srv.tag, opened)
				}
				goto teleport
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				confirm(p)
			}
		}
	teleport:
		sendCommand(t, pw, "tp "+name+" 400.5 -60 400.5")

		// Answer the teleport and any keep-alive, and count what streams.
		chunks, teleports := 0, 0
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if err := c.SetReadDeadline(deadline); err != nil {
				t.Fatal(err)
			}
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayChunkData:
				chunks++
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				teleports++
				confirm(p)
			}
		}
		c.Close()
		// Without a teleport there is no new window waiting, and the
		// silence would prove nothing.
		if teleports == 0 {
			t.Fatalf("%s: the teleport was never applied", srv.tag)
		}
		if chunks != 0 {
			t.Errorf("%s: %d chunks streamed to a client that never acknowledged", srv.tag, chunks)
		}
		t.Logf("%s: opening batch, then %d chunks after a confirmed teleport", srv.tag, chunks)
	}
}

// sameInts reports whether two batch-size sequences are identical.
func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// sameTimes reports whether two batch-time sequences are paced the same
// way, comparing the gaps from the first batch rather than clock times: a
// batch is due one a tick and the acknowledgement's first batch at once,
// so a server that paces differently is out by whole ticks, not by the
// tolerance a batch a tick on two servers allows for.
func sameTimes(a, b []time.Duration, tol time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		d := (a[i] - a[0]) - (b[i] - b[0])
		if d > tol || d < -tol {
			return false
		}
	}
	return true
}
