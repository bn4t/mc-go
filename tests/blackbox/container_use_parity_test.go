package blackbox

// Use-gate parity: what a Use Item On against a menu block answers.
// Measured cell by cell on vanilla 26.2: an in-reach use opens only
// once every server teleport is confirmed, otherwise the server acks
// the sequence and posts the build.tooHigh action bar; an out-of-reach
// use is acked silently whether teleports are confirmed or not;
// digging is never gated. Every cell runs on both servers with parsed
// assertions (open menu + title, acked sequences, action-bar chat).

import (
	"bytes"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// usePkt is one captured clientbound packet.
type usePkt struct {
	id   int32
	data []byte
}

// useClient is a scripted client that answers keep-alive always and
// teleports only when confirm is set, capturing every small packet
// for parsed assertions.
type useClient struct {
	mu      sync.Mutex
	confirm bool
	pkts    []usePkt
	err     error
}

func newUseClient(pr *protocol.Reader, pw *protocol.Writer, confirm bool, captureLarge ...int32) *useClient {
	u := &useClient{confirm: confirm}
	var wmu sync.Mutex
	go func() {
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				u.mu.Lock()
				u.err = err
				u.mu.Unlock()
				return
			}
			u.mu.Lock()
			capture := len(p.Data) < 4096
			for _, id := range captureLarge {
				if p.ID == id {
					capture = true
					break
				}
			}
			if capture {
				cp := make([]byte, len(p.Data))
				copy(cp, p.Data)
				u.pkts = append(u.pkts, usePkt{id: p.ID, data: cp})
			}
			u.mu.Unlock()
			wmu.Lock()
			switch p.ID {
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				if confirm {
					if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
						var b protocol.Buffer
						b.VarInt(id)
						_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					}
				}
			}
			wmu.Unlock()
		}
	}()
	return u
}

func (u *useClient) take() []usePkt {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := u.pkts
	u.pkts = nil
	return out
}

// has reports whether a captured payload mentions the marker, so a
// useClient answers digSentinel round trips like a digSink does.
func (u *useClient) has(marker string) bool {
	for _, p := range u.take() {
		if bytes.Contains(p.data, []byte(marker)) {
			return true
		}
	}
	return false
}

// isContainerPkt reports whether a clientbound packet carries menu,
// chat, or acknowledgement traffic: the packets a use or click can
// answer with. Container Property is deliberately absent — a running
// brew (or any ticking menu) streams it every tick, so treating it as
// an answer keeps the settle window open forever; it is still folded
// whenever it arrives.
func isContainerPkt(id int32) bool {
	switch id {
	case server.IDPlayOpenScreen, server.IDPlayContainerContent,
		server.IDPlayContainerSlot, server.IDPlaySetCarriedItem,
		server.IDPlayContainerClose,
		server.IDPlaySystemChatCB, server.IDPlayBlockChangedAck:
		return true
	}
	return false
}

// settleUse drains the sink until a full gap passes with no packet of
// interest, folding everything taken. Fixed sleeps skew on a cold
// server (a first click answered after the window closed lands in the
// next step); quiet-based settling attributes every answer to the
// step that caused it. The gap covers cold-JVM pauses between the
// packets of one answer.
func settleUse(t *testing.T, sink *useClient, fold func([]usePkt), tag string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	quiet := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		pkts := sink.take()
		fold(pkts)
		for _, p := range pkts {
			if isContainerPkt(p.id) {
				quiet = time.Now().Add(800 * time.Millisecond)
				break
			}
		}
		if time.Now().After(quiet) {
			return
		}
	}
	t.Fatalf("%s: traffic never settled", tag)
}

// useVerdict is the parsed answer to one use: which sequences were
// acked, whether a menu opened (with its title), and whether the
// build.tooHigh action bar fired.
type useVerdict struct {
	acks    []int32
	open    bool
	menu    int32
	title   []byte
	tooHigh bool
	chats   int
}

func judgeUse(pkts []usePkt) useVerdict {
	var v useVerdict
	for _, p := range pkts {
		switch p.id {
		case server.IDPlayBlockChangedAck:
			if seq, err := protocol.NewPacketReader(p.data).VarInt(); err == nil {
				v.acks = append(v.acks, seq)
			}
		case server.IDPlayOpenScreen:
			r := protocol.NewPacketReader(p.data)
			id, err1 := r.VarInt()
			menu, err2 := r.VarInt()
			rest, err3 := r.Read(r.Remaining())
			if err1 == nil && err2 == nil && err3 == nil {
				v.open = true
				v.menu = menu
				v.title = rest
				_ = id
			}
		case server.IDPlaySystemChatCB:
			v.chats++
			if bytes.Contains(p.data, []byte("build.tooHigh")) {
				v.tooHigh = true
			}
		}
	}
	return v
}

// sendUseItemOn writes one Use Item On with the exact 0b layout the
// real client sends: hand, position, face, 3 cursor floats, inside,
// world-border hit, then the prediction sequence.
func sendUseItemOn(t *testing.T, pw *protocol.Writer, x, y, z, face, seq int32) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(0)
	b.Position(x, y, z)
	b.VarInt(face)
	b.Float(0.5)
	b.Float(1.0)
	b.Float(0.5)
	b.Bool(false)
	b.Bool(false)
	b.VarInt(seq)
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// useCellSetup joins, reads the READY sentinel, reports loaded, and
// optionally teleports to the stand point, placing the near and far
// chests. It returns the client and its sink.
func useCellSetup(t *testing.T, addr, probe string, confirm, teleport bool) (*useClient, *protocol.Writer) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, probe)
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Time{})
	sink := newUseClient(pr, pw, confirm)
	sendClientInformation(t, pw)
	if !digSentinel(t, pw, sink, "READY", 30*time.Second) {
		t.Fatalf("%s: never ready: %v", probe, sink.err)
	}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	if teleport {
		sendCommand(t, pw, "tp @s 6 -60 2 0 0")
		if !digSentinel(t, pw, sink, "TPOK", 10*time.Second) {
			t.Fatalf("%s: no tp", probe)
		}
	}
	sendCommand(t, pw, "setblock 8 -61 0 minecraft:chest")
	if !digSentinel(t, pw, sink, "SBOK", 10*time.Second) {
		t.Fatalf("%s: no setblock", probe)
	}
	sendCommand(t, pw, "setblock 30 -61 0 minecraft:chest")
	if !digSentinel(t, pw, sink, "SB2", 10*time.Second) {
		t.Fatalf("%s: no setblock2", probe)
	}
	sink.take()
	return sink, pw
}

// runUseCell sends one use and settles, returning the parsed verdict.
func runUseCell(t *testing.T, sink *useClient, pw *protocol.Writer, x, y, z, face, seq int32, tag string) useVerdict {
	t.Helper()
	var got []usePkt
	sendUseItemOn(t, pw, x, y, z, face, seq)
	settleUse(t, sink, func(pkts []usePkt) { got = append(got, pkts...) }, tag)
	return judgeUse(got)
}

func checkAcked(t *testing.T, cell string, v useVerdict, seq int32) {
	t.Helper()
	for _, a := range v.acks {
		if a == seq {
			return
		}
	}
	t.Fatalf("%s: acked %v, want sequence %d", cell, v.acks, seq)
}

// parseSyncPos reads the teleport id and target out of a Synchronize
// Player Position packet.
func parseSyncPos(data []byte) (float64, float64, float64, bool) {
	r := protocol.NewPacketReader(data)
	if _, err := r.VarInt(); err != nil {
		return 0, 0, 0, false
	}
	x, err1 := r.Double()
	y, err2 := r.Double()
	z, err3 := r.Double()
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return x, y, z, true
}

// posWatch records every Synchronize Player Position seen, keeping
// the latest coordinates. Join bursts carry the spawn sync ahead of
// chat echoes, so watching (instead of discarding) is what makes the
// spawn position observable at all.
type posWatch struct {
	x, y, z float64
	have    bool
}

func (w *posWatch) note(pkts []usePkt) {
	for _, p := range pkts {
		if p.id != server.IDPlaySyncPos {
			continue
		}
		if nx, ny, nz, ok := parseSyncPos(p.data); ok {
			w.x, w.y, w.z, w.have = nx, ny, nz, true
		}
	}
}

// waitChat waits for a chat marker like digSentinel, noting position
// syncs along the way.
func (w *posWatch) waitChat(t *testing.T, pw *protocol.Writer, sink *useClient, marker string, timeout time.Duration) bool {
	t.Helper()
	sendCommand(t, pw, "say "+marker)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		pkts := sink.take()
		w.note(pkts)
		for _, p := range pkts {
			if bytes.Contains(p.data, []byte(marker)) {
				return true
			}
		}
	}
	return false
}

// TestUseGateParity pins the Use Item On gates against both servers.
func TestUseGateParity(t *testing.T) {
	probes := []string{"GateNear", "GateDeny", "GateFar", "GateFarDeny", "GateNoTp", "GateLectern", "GateDig"}
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(probes[0]+"V", probes[1]+"V", probes[2]+"V", probes[3]+"V", probes[4]+"V", probes[5]+"V", probes[6]+"V"),
	}, nil)
	defer tg.cleanup()
	servers := []struct {
		tag  string
		addr string
	}{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	}
	for _, sv := range servers {
		sv := sv
		t.Run(sv.tag, func(t *testing.T) {
			suffix := "V"
			if sv.tag != "vanilla" {
				suffix = "M"
			}
			// In reach and confirmed: the chest opens.
			t.Run("nearConfirmedOpens", func(t *testing.T) {
				sink, pw := useCellSetup(t, sv.addr, probes[0]+suffix, true, true)
				v := runUseCell(t, sink, pw, 8, -61, 0, 1, 11, "nearConfirmed")
				checkAcked(t, "nearConfirmed", v, 11)
				if !v.open || v.menu != 2 || !bytes.Contains(v.title, []byte("container.chest")) {
					t.Fatalf("nearConfirmed: open=%v menu=%d title=%q, want menu 2 container.chest", v.open, v.menu, v.title)
				}
				if v.tooHigh {
					t.Fatal("nearConfirmed: unexpected build.tooHigh")
				}
			})
			// In reach but a teleport unconfirmed: ack plus the
			// build.tooHigh action bar, no menu.
			t.Run("nearUnconfirmedTooHigh", func(t *testing.T) {
				sink, pw := useCellSetup(t, sv.addr, probes[1]+suffix, false, true)
				v := runUseCell(t, sink, pw, 8, -61, 0, 1, 12, "nearUnconfirmed")
				checkAcked(t, "nearUnconfirmed", v, 12)
				if v.open {
					t.Fatal("nearUnconfirmed: menu opened, want denial")
				}
				if !v.tooHigh {
					t.Fatalf("nearUnconfirmed: no build.tooHigh in %d chats", v.chats)
				}
			})
			// Out of reach and confirmed: acked silently.
			t.Run("farConfirmedSilent", func(t *testing.T) {
				sink, pw := useCellSetup(t, sv.addr, probes[2]+suffix, true, true)
				v := runUseCell(t, sink, pw, 30, -61, 0, 1, 13, "farConfirmed")
				checkAcked(t, "farConfirmed", v, 13)
				if v.open || v.tooHigh {
					t.Fatalf("farConfirmed: open=%v tooHigh=%v, want silent", v.open, v.tooHigh)
				}
			})
			// Out of reach and unconfirmed: still silent, no chat.
			t.Run("farUnconfirmedSilent", func(t *testing.T) {
				sink, pw := useCellSetup(t, sv.addr, probes[3]+suffix, false, true)
				v := runUseCell(t, sink, pw, 30, -61, 0, 1, 14, "farUnconfirmed")
				checkAcked(t, "farUnconfirmed", v, 14)
				if v.open || v.tooHigh {
					t.Fatalf("farUnconfirmed: open=%v tooHigh=%v, want silent", v.open, v.tooHigh)
				}
			})
			// No teleport issued at all: nothing to confirm, so a
			// chest placed next to wherever spawn put the player
			// opens. World spawn is random per boot, so read it
			// first instead of assuming fixed coordinates.
			t.Run("noTeleportOpens", func(t *testing.T) {
				c, pr, pw := joinPlayBlackbox(t, sv.addr, probes[4]+suffix)
				t.Cleanup(func() { c.Close() })
				_ = c.SetDeadline(time.Time{})
				sink := newUseClient(pr, pw, true)
				sendClientInformation(t, pw)
				w := &posWatch{}
				if !w.waitChat(t, pw, sink, "READY", 30*time.Second) {
					t.Fatalf("noTp: never ready: %v", sink.err)
				}
				if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
					t.Fatal(err)
				}
				// Place the chest next to the actual spawn point and
				// re-place it if a later sync moves the player first.
				cx, cy, cz := 0, 0, 0
				lx, ly, lz := 0.0, 0.0, 0.0
				for tries := 0; tries < 3; tries++ {
					if !w.waitChat(t, pw, sink, "PING", 10*time.Second) {
						t.Fatal("noTp: no ping")
					}
					if !w.have {
						t.Fatal("noTp: no spawn sync")
					}
					lx, ly, lz = w.x, w.y, w.z
					cx, cy, cz = int(math.Floor(w.x))+2, int(math.Floor(w.y))-1, int(math.Floor(w.z))
					sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d minecraft:chest", cx, cy, cz))
					ox, oy, oz := w.x, w.y, w.z
					if !w.waitChat(t, pw, sink, "SBOK", 10*time.Second) {
						t.Fatal("noTp: no setblock")
					}
					if w.x == ox && w.y == oy && w.z == oz {
						break
					}
					lx, ly, lz = w.x, w.y, w.z
				}
				sink.take()
				v := runUseCell(t, sink, pw, int32(cx), int32(cy), int32(cz), 1, 15, "noTeleport")
				checkAcked(t, "noTeleport", v, 15)
				if !v.open || v.menu != 2 || !bytes.Contains(v.title, []byte("container.chest")) {
					t.Fatalf("noTeleport: open=%v menu=%d title=%q tooHigh=%v chats=%d acks=%v at [%v %v %v], want menu 2 container.chest", v.open, v.menu, v.title, v.tooHigh, v.chats, v.acks, lx, ly, lz)
				}
			})
			// An empty lectern has no menu: confirmed and in reach,
			// the use stays silent.
			t.Run("emptyLecternSilent", func(t *testing.T) {
				sink, pw := useCellSetup(t, sv.addr, probes[5]+suffix, true, true)
				sendCommand(t, pw, "setblock 8 -61 0 minecraft:lectern")
				if !digSentinel(t, pw, sink, "LECT", 10*time.Second) {
					t.Fatalf("%s: no lectern", probes[5]+suffix)
				}
				sink.take()
				v := runUseCell(t, sink, pw, 8, -61, 0, 1, 16, "emptyLectern")
				checkAcked(t, "emptyLectern", v, 16)
				if v.open || v.tooHigh {
					t.Fatalf("emptyLectern: open=%v tooHigh=%v, want silent", v.open, v.tooHigh)
				}
			})
			// Digging is not gated: an unconfirmed teleport still
			// breaks the block.
			t.Run("digUnconfirmedBreaks", func(t *testing.T) {
				sink, pw := useCellSetup(t, sv.addr, probes[6]+suffix, false, true)
				sendCommand(t, pw, "setblock 6 -61 3 minecraft:dirt")
				if !digSentinel(t, pw, sink, "DIRT", 10*time.Second) {
					t.Fatalf("%s: no dirt", probes[6]+suffix)
				}
				sink.take()
				sendDig(t, pw, 0, 6, -61, 3, 21)
				deadline := time.Now().Add(6 * time.Second)
				broke := false
				for time.Now().Before(deadline) && !broke {
					time.Sleep(100 * time.Millisecond)
					for _, p := range sink.take() {
						if p.id != server.IDPlayBlockUpdateCB {
							continue
						}
						r := protocol.NewPacketReader(p.data)
						x, y, z, err := r.Position()
						if err != nil {
							continue
						}
						st, err := r.VarInt()
						if err != nil {
							continue
						}
						if x == 6 && y == -61 && z == 3 && st == 0 {
							broke = true
						}
					}
				}
				if !broke {
					t.Fatal("digUnconfirmed: dirt at (6,-61,3) never broke to air")
				}
			})
		})
	}
}
