package blackbox

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Chunk window parity: how much of the world each server streams, and in
// what shape.
//
// The window is not a square. Measured on 26.2 by joining with one view
// distance after another and listing every column streamed, it is the
// square of radius viewDistance+1 with the corners beyond a circle of
// radius viewDistance+2.75 cut away - 49 columns for the default, 81 for
// a view distance of 3, 117 for 4, 213 for 6, 329 for 8. A client that
// announces nothing is read as asking for 2, which is what the real
// server falls back to; announcing a distance late re-streams the wider
// window at once.
//
// The column sets below are compared exactly, so a window that is merely
// the right size is not enough.
const windowName = "ChunkWin"

// windowCases are the clients each server is asked for: a view distance
// and the number of columns the measurement says that distance streams.
var windowCases = []struct {
	name  string
	vd    int
	count int
}{
	{"WinNone", 0, 49},
	{"Win2", 2, 49},
	{"Win3", 3, 81},
	{"Win4", 4, 117},
	{"Win8", 8, 329},
}

// windowClient is a scripted client that answers what a real one has to
// answer - keep-alive, the teleport every /tp needs accepted, and the
// chunk batch rate the real server throttles its streaming on - and
// records which columns it was sent.
type windowClient struct {
	mu     sync.Mutex
	cols   map[[2]int]bool
	radius int
	syncs  int
	err    error
}

func newWindowClient(t *testing.T, addr, name string) (net.Conn, *windowClient, *protocol.Writer) {
	t.Helper()
	c, pr, pw := joinPlayQuiet(t, addr, name)
	_ = c.SetDeadline(time.Time{})
	w := &windowClient{cols: map[[2]int]bool{}}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	var writeMu sync.Mutex
	go func() {
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				w.mu.Lock()
				w.err = err
				w.mu.Unlock()
				return
			}
			switch p.ID {
			case server.IDPlayChunkData:
				r := protocol.NewPacketReader(p.Data)
				cx, err1 := r.Int()
				cz, err2 := r.Int()
				if err1 != nil || err2 != nil {
					continue
				}
				w.mu.Lock()
				w.cols[[2]int{int(cx), int(cz)}] = true
				w.mu.Unlock()
			case server.IDPlayChunkCacheRadius:
				if n, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					w.mu.Lock()
					w.radius = int(n)
					w.mu.Unlock()
				}
			case server.IDPlaySyncPos:
				w.mu.Lock()
				w.syncs++
				w.mu.Unlock()
			}
			writeMu.Lock()
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				// The real client asks for a rate, not a count: twenty-five
				// chunks a tick streams a whole view in a moment.
				var b protocol.Buffer
				b.Float(25)
				_ = pw.WritePacket(server.IDPlayChunkBatchRecvSB, b.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			}
			writeMu.Unlock()
		}
	}()
	return c, w, pw
}

// windowColumns returns the recorded columns as sorted "x,z" strings.
func (w *windowClient) windowColumns() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.cols))
	for k := range w.cols {
		out = append(out, fmt.Sprintf("%d,%d", k[0], k[1]))
	}
	sort.Strings(out)
	return out
}

// TestChunkWindowParity requires both servers to stream exactly the same
// columns for the same view distance, and to fall back to the same
// window for a client that announces nothing.
func TestChunkWindowParity(t *testing.T) {
	names := []string{windowName + "Setup"}
	for _, tc := range windowCases {
		names = append(names, windowName+tc.name)
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()

	// One server's side of the measurement: every client joins and is
	// put in the same place, all of them together so the wait is paid
	// once.
	run := func(tag, addr string) map[string][]string {
		setup, spr, spw := joinPlayBlackbox(t, addr, windowName+"Setup")
		_ = setup.SetDeadline(time.Time{})
		ssink := newChatSink(spr, spw)
		sendCommand(t, spw, "forceload add -64 -64 128 128")
		time.Sleep(400 * time.Millisecond)
		waitDrained(t, spw, ssink)
		for _, line := range ssink.take() {
			if strings.Contains(line, "nknown or incomplete") || strings.Contains(line, "ermission") {
				t.Errorf("%s: forceload refused: %s", tag, line)
			}
		}
		setup.Close()

		type conn struct {
			c net.Conn
			w *windowClient
		}
		var conns []conn
		var infos []*protocol.Writer
		for _, tc := range windowCases {
			name := windowName + tc.name
			c, w, pw := newWindowClient(t, addr, name)
			defer c.Close()
			conns = append(conns, conn{c, w})
			infos = append(infos, pw)
			before := w.syncs
			sendCommand(t, pw, fmt.Sprintf("tp %s 8.5 -60 3.5 180 10", name))
			if !waitClientSync(w, before, 5*time.Second) {
				t.Fatalf("%s/%s: no teleport to accept after /tp", tag, name)
			}
		}
		// Everyone has joined; now the settings packet arrives, which a
		// real client sends at once but which is also allowed to arrive
		// late. The real server answers a late one by streaming the new
		// window straight away, and mc-go has to do the same.
		time.Sleep(500 * time.Millisecond)
		for i, tc := range windowCases {
			if tc.vd == 0 {
				continue
			}
			var b protocol.Buffer
			b.String("en_us")
			b.Byte(byte(tc.vd))
			b.VarInt(0)
			b.Bool(true)
			b.Byte(0x7F)
			b.VarInt(1)
			b.Bool(false)
			b.Bool(true)
			b.VarInt(0)
			if err := infos[i].WritePacket(server.IDPlayClientInfoSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(6 * time.Second)

		out := map[string][]string{}
		for i, tc := range windowCases {
			w := conns[i].w
			cols := w.windowColumns()
			if tc.vd > 0 && len(cols) != tc.count {
				t.Errorf("%s/%s (view distance %d): %d columns, want %d",
					tag, tc.name, tc.vd, len(cols), tc.count)
			}
			if tc.vd == 0 && len(cols) != tc.count {
				t.Errorf("%s/%s (nothing announced): %d columns, want %d",
					tag, tc.name, len(cols), tc.count)
			}
			if w.radius != 0 {
				t.Errorf("%s/%s: chunk cache radius %d, but the real server sends none",
					tag, tc.name, w.radius)
			}
			out[tc.name] = cols
		}
		return out
	}

	v := run("vanilla", tg.vanilla)
	m := run("mc-go", tg.mcgo)
	for _, tc := range windowCases {
		if len(v[tc.name]) == 0 {
			t.Fatalf("vanilla streamed nothing to %s", tc.name)
		}
		if strings.Join(v[tc.name], " ") != strings.Join(m[tc.name], " ") {
			t.Errorf("%s: vanilla and mc-go streamed different columns\nvanilla:\n%s\nmc-go:\n%s",
				tc.name, wrapList(v[tc.name]), wrapList(m[tc.name]))
		}
	}
}

// waitClientSync waits for a client to be sent a teleport to accept.
func waitClientSync(w *windowClient, after int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		n := w.syncs
		w.mu.Unlock()
		if n > after {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// wrapList lays a column list out a few per line so a mismatch is
// readable.
func wrapList(cols []string) string {
	var sb strings.Builder
	for i, c := range cols {
		if i > 0 {
			sb.WriteByte(' ')
		}
		if i%12 == 0 && i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(c)
	}
	return sb.String()
}
