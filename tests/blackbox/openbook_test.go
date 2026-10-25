package blackbox

// TestOpenBookParity uses a book item against both servers and diffs
// the open_book clientbound packet: vanilla's WritableBookItem and
// WrittenBookItem use() both land on Player.openItemGui, which sends
// ClientboundOpenBookPacket{hand} unconditionally. The payload is a
// single varint — the hand that was used — so the compare is exact.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func useItem(t *testing.T, pw *protocol.Writer, hand int32) {
	t.Helper()
	var ub protocol.Buffer
	ub.VarInt(hand)
	ub.VarInt(0) // sequence
	ub.Float(0)  // yaw
	ub.Float(0)  // pitch
	if err := pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenBookParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("BkP"),
	}, nil)
	defer targets.cleanup()

	openBooks := func(c interface {
		SetReadDeadline(time.Time) error
	}, pr *protocol.Reader, d time.Duration) [][]byte {
		var got [][]byte
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
			for {
				p, err := pr.ReadPacket()
				if err != nil {
					break
				}
				if p.ID == server.IDPlayOpenBook {
					got = append(got, append([]byte(nil), p.Data...))
				}
			}
		}
		return got
	}

	run := func(addr string) (mainHand, offHand, bare [][]byte) {
		c, pr, pw := joinPlayBlackbox(t, addr, "BkP")
		defer c.Close()
		sendCommand(t, pw, `item replace entity @s weapon.mainhand with minecraft:written_book[written_book_content={title:{raw:"T"},author:"dev",generation:0,pages:[{raw:"p1"}]}]`)
		sendCommand(t, pw, `item replace entity @s weapon.offhand with minecraft:writable_book[written_book_content={title:{raw:"T"},author:"dev"}]`)
		time.Sleep(600 * time.Millisecond)
		// drain whatever the gives produced
		deadline := time.Now().Add(150 * time.Millisecond)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(60 * time.Millisecond))
			for {
				if _, err := pr.ReadPacket(); err != nil {
					break
				}
			}
		}
		useItem(t, pw, 0)
		mainHand = openBooks(c, pr, 1400*time.Millisecond)
		_ = mainHand
		useItem(t, pw, 1)
		offHand = openBooks(c, pr, 1400*time.Millisecond)
		// A bare book carries no written_book_content — openItemGui
		// skips the packet entirely on vanilla.
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:written_book")
		time.Sleep(400 * time.Millisecond)
		useItem(t, pw, 0)
		bare = openBooks(c, pr, 1000*time.Millisecond)
		return
	}

	vMain, vOff, vBare := run(targets.vanilla)
	mMain, mOff, mBare := run(targets.mcgo)

	if len(vMain) != 1 {
		t.Fatalf("vanilla main-hand written_book: %d open_book, want 1", len(vMain))
	}
	if len(mMain) != 1 {
		t.Fatalf("mc-go main-hand written_book: %d open_book, want 1", len(mMain))
	}
	if len(vOff) != 1 {
		t.Fatalf("vanilla off-hand writable_book: %d open_book, want 1", len(vOff))
	}
	if len(mOff) != 1 {
		t.Fatalf("mc-go off-hand writable_book: %d open_book, want 1", len(mOff))
	}
	if string(vMain[0]) != string(mMain[0]) {
		t.Fatalf("main-hand open_book payload differs: vanilla %x vs mc-go %x", vMain[0], mMain[0])
	}
	if string(vOff[0]) != string(mOff[0]) {
		t.Fatalf("off-hand open_book payload differs: vanilla %x vs mc-go %x", vOff[0], mOff[0])
	}
	if len(vBare) != 0 {
		t.Fatalf("vanilla bare written_book: %d open_book, want 0", len(vBare))
	}
	if len(mBare) != 0 {
		t.Fatalf("mc-go bare written_book: %d open_book, want 0", len(mBare))
	}
}

// TestDebugSampleParity subscribes to dedicated_server_tick_time on
// both servers and requires the debug_sample stream to arrive on the
// wall tick (~20 Hz), with the vanilla shape {long[] sample, varint 0
// TICK_TIME}. Sample values are real tick nanos — inherently unequal —
// so only the shape and cadence are compared.
func TestDebugSampleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("DSP"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) (count int, sawShape bool, unsubNone bool) {
		c, pr, pw := joinPlayBlackbox(t, addr, "DSP")
		defer c.Close()
		// Window with no subscription must carry no debug_sample.
		var unsub int
		deadline := time.Now().Add(600 * time.Millisecond)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
			for {
				p, err := pr.ReadPacket()
				if err != nil {
					break
				}
				if p.ID == 0x1e {
					unsub++
				}
			}
		}
		unsubNone = unsub == 0
		var sub protocol.Buffer
		sub.VarInt(1)
		sub.VarInt(0) // dedicated_server_tick_time
		if err := pw.WritePacket(server.IDPlayDebugSubscriptionRequestSB, sub.Bytes()); err != nil {
			t.Fatal(err)
		}
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			for {
				p, err := pr.ReadPacket()
				if err != nil {
					break
				}
				if p.ID == 0x1e {
					count++
					r := protocol.NewPacketReader(p.Data)
					if n, err := r.VarInt(); err == nil && n >= 1 {
						ok := true
						for i := int32(0); i < n; i++ {
							if _, err := r.Long(); err != nil {
								ok = false
								break
							}
						}
						if ty, err := r.VarInt(); ok && err == nil && ty == 0 {
							sawShape = true
						}
					}
				}
			}
		}
		return
	}

	vCount, vShape, vUnsub := run(targets.vanilla)
	mCount, mShape, mUnsub := run(targets.mcgo)
	// 2s at ~20 Hz; allow generous slack for scheduling jitter.
	if vCount < 20 {
		t.Fatalf("vanilla emitted %d debug_sample in 2s, want ~40", vCount)
	}
	if mCount < 20 {
		t.Fatalf("mc-go emitted %d debug_sample in 2s, want ~40", mCount)
	}
	if !vShape {
		t.Fatal("vanilla debug_sample payload never parsed as {long[], varint 0}")
	}
	if !mShape {
		t.Fatal("mc-go debug_sample payload never parsed as {long[], varint 0}")
	}
	if !vUnsub {
		t.Fatal("vanilla emitted debug_sample before any subscription")
	}
	if !mUnsub {
		t.Fatal("mc-go emitted debug_sample before any subscription")
	}
}
