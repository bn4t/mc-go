package blackbox

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestConfigStageAdvance checks both servers move past login: mc-go
// must emit Finish Configuration; vanilla must emit at least one
// config-stage packet (registry data etc.). Exact vanilla bytes are
// version-specific and intentionally not asserted.
func TestConfigStageAdvance(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	const name = "ConfigProbe"

	c1, pr1, pw1 := loginStart(t, tg.mcgo, name)
	defer c1.Close()
	readLoginSuccess(t, pr1, pw1, "mc-go")
	if err := pw1.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	sendKnownPacks(t, pw1, "99.9-test")
	for i, want := range []int32{
		server.IDConfigPluginMessageCB,
		server.IDConfigFeatureFlags,
		server.IDConfigKnownPacksCB,
	} {
		p, err := pr1.ReadPacket()
		if err != nil || p.ID != want {
			t.Fatalf("mc-go config %d = %#x, err %v (want %#x)", i, p.ID, err, want)
		}
	}
	nregs := 0
	for {
		reg, err := pr1.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if reg.ID != server.IDConfigRegistryData {
			if reg.ID != server.IDConfigUpdateTags {
				t.Fatalf("mc-go after registries = %#x, want tags 0x0d", reg.ID)
			}
			break
		}
		nregs++
	}
	if nregs < 20 {
		t.Fatalf("mc-go registries = %d, want the full vanilla set", nregs)
	}
	finish, err := pr1.ReadPacket()
	if err != nil || finish.ID != server.IDConfigFinish {
		t.Fatalf("mc-go config = %#x, err %v (want finish 0x03)", finish.ID, err)
	}

	c2, pr2, pw2 := loginStart(t, tg.vanilla, name)
	defer c2.Close()
	readLoginSuccess(t, pr2, pw2, "vanilla")
	if err := pw2.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	sendKnownPacks(t, pw2, "99.9-test")
	if _, err := pr2.ReadPacket(); err != nil {
		t.Fatalf("vanilla sent nothing after known packs: %v", err)
	}
}

// TestBadHandshakeParity sends an unknown next-state to both servers;
// both must drop the connection yet stay alive for later probes.
func TestBadHandshakeParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, addr := range []string{tg.vanilla, tg.mcgo} {
		c, _, pw := client(t, addr)
		func() {
			defer c.Close()
			if err := handshake(pw, 99); err != nil {
				t.Fatal(err)
			}
			var b protocol.Buffer
			b.Long(1)
			_ = pw.WritePacket(server.IDPingRequest, b.Bytes())
			pr := protocol.NewReader(c)
			if p, err := pr.ReadPacket(); err == nil {
				t.Fatalf("%s: answered after bad handshake with %#x", addr, p.ID)
			}
		}()
		if !probeStatus(addr) {
			t.Fatalf("%s: not alive after bad handshake", addr)
		}
	}
}

// TestOversizeFrameParity sends a huge length prefix; both must drop
// the peer without affecting other connections.
func TestOversizeFrameParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, addr := range []string{tg.vanilla, tg.mcgo} {
		c, _, _ := client(t, addr)
		func() {
			defer c.Close()
			var b protocol.Buffer
			b.VarInt(1 << 30) // absurd length
			_, _ = c.Write(b.Bytes())
			pr := protocol.NewReader(c)
			_, _ = pr.ReadPacket() // expect failure/close, ignore result
		}()
		if !probeStatus(addr) {
			t.Fatalf("%s: not alive after oversize frame", addr)
		}
	}
}

// TestPlayJoinParity drives both servers through the full handshake to
// the first play packet. mc-go is asserted strictly against its own
// constants; vanilla's observed IDs are logged as ground truth and its
// first play packet must be a Join Game whose first field parses as an
// entity-id int. A mismatch fails with the exact IDs to reconcile.
func TestPlayJoinParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	drainConfig := func(tag string, pr *protocol.Reader, pw *protocol.Writer) {
		t.Helper()
		sendKnownPacks(t, pw, "99.9-test")
		var ids []int32
		for i := 0; i < 60; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s config stream: %v", tag, err)
			}
			ids = append(ids, p.ID)
			if p.ID == server.IDConfigFinish {
				break
			}
		}
		t.Logf("%s config ids: %#x", tag, ids)
		if err := pw.WritePacket(server.IDConfigFinishAck, nil); err != nil {
			t.Fatal(err)
		}
	}

	c1, pr1, pw1 := loginStart(t, tg.mcgo, "PlayProbe")
	defer c1.Close()
	readLoginSuccess(t, pr1, pw1, "mc-go")
	if err := pw1.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	drainConfig("mc-go", pr1, pw1)
	join, err := pr1.ReadPacket()
	if err != nil || join.ID != server.IDPlayJoinGame {
		t.Fatalf("mc-go first play = %#x, err %v (want join game)", join.ID, err)
	}

	c2, pr2, pw2 := loginStart(t, tg.vanilla, "PlayProbe")
	defer c2.Close()
	readLoginSuccess(t, pr2, pw2, "vanilla")
	if err := pw2.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	drainConfig("vanilla", pr2, pw2)
	vJoin, err := pr2.ReadPacket()
	if err != nil {
		t.Fatalf("vanilla first play: %v", err)
	}
	t.Logf("vanilla first play id = %#x", vJoin.ID)
	if vJoin.ID != server.IDPlayJoinGame {
		t.Fatalf("join game id: vanilla=%#x mc-go=%#x", vJoin.ID, server.IDPlayJoinGame)
	}
}

// joinPlayBlackbox runs the full scripted handshake through config
// finish-ack and returns at the play stage, having reported the terrain
// loaded. That report is what a real client sends within a fraction of a
// second of joining, and what the real server waits for before it acts
// on a player's uses and digs (see TestInteractionGateParity for the gate
// itself).
func joinPlayBlackbox(t *testing.T, addr, name string) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	c, pr, pw := joinPlayQuiet(t, addr, name)
	// A moment for the server to finish entering the play stage, then the
	// report.
	time.Sleep(50 * time.Millisecond)
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	return c, pr, pw
}

// joinPlayQuiet is the same handshake without the terrain-loaded report,
// for the tests that measure the interaction gate itself.
func joinPlayQuiet(t *testing.T, addr, name string) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	c, pr, pw := loginStart(t, addr, name)
	readLoginSuccess(t, pr, pw, addr)
	if err := pw.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	sendKnownPacks(t, pw, "99.9-test")
	for i := 0; i < 60; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s config stream: %v", addr, err)
		}
		if p.ID == server.IDConfigFinish {
			break
		}
	}
	if err := pw.WritePacket(server.IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	return c, pr, pw
}

func sendMovePosRot(pw *protocol.Writer, x, y, z float64) error {
	var b protocol.Buffer
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(0)
	b.Float(0)
	b.Byte(0x01) // flags: on ground
	return pw.WritePacket(server.IDPlayMovePosRotSB, b.Bytes())
}

// TestMovementParity sends a legal position update to both servers and
// requires neither to kick. A disconnect (or close) fails with the
// exact packet observed, which is the signal for reconciling the
// VERIFY-gated movement IDs against 26.2.
func TestMovementParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{
		{"mc-go", tg.mcgo},
		{"vanilla", tg.vanilla},
	} {
		c, pr, pw := joinPlayBlackbox(t, tc.addr, "Mover")
		func() {
			defer c.Close()
			// Skip the join burst until Join Game.
			for i := 0; i < 300; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Fatalf("%s play stream: %v", tc.tag, err)
				}
				if p.ID == server.IDPlayJoinGame {
					break
				}
			}
			if err := sendMovePosRot(pw, 10.5, 66, -3.25); err != nil {
				t.Fatal(err)
			}
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			for {
				p, err := pr.ReadPacket()
				if err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						return // no kick within window: pass
					}
					t.Fatalf("%s closed after move: %v", tc.tag, err)
				}
				if p.ID == server.IDPlayDisconnect {
					t.Fatalf("%s kicked after move", tc.tag)
				}
			}
		}()
	}
}

// sendFullChat writes a 776-shape chat packet: message, timestamp,
// salt, no signature, zero message count, empty 20-bit acknowledge
// set, zero checksum. Matches the vanilla protocol docs for Chat
// Message (serverbound).
func sendFullChat(t *testing.T, pw *protocol.Writer, id int32, text string) {
	t.Helper()
	var b protocol.Buffer
	b.String(text)
	b.Long(1)              // timestamp (ms since epoch)
	b.Long(0)              // salt
	b.Bool(false)          // signature: absent
	b.VarInt(0)            // message count: no signed messages seen
	b.Raw(make([]byte, 3)) // acknowledged Fixed BitSet (20)
	b.Byte(0)              // checksum
	if err := pw.WritePacket(id, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func drainToJoin(t *testing.T, tag string, pr *protocol.Reader) {
	t.Helper()
	for i := 0; i < 400; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s play stream: %v", tag, err)
		}
		if p.ID == server.IDPlayJoinGame {
			return
		}
	}
	t.Fatalf("%s: no join game in play burst", tag)
}

// TestChatParity connects a chatter and a listener to each server and
// requires the listener to receive the message bytes. This proves
// delivery behavior without pinning packet IDs: mc-go relays via
// System Chat, vanilla via signed Player Chat (see chat.go).
func TestChatParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{
		{"mc-go", tg.mcgo},
		{"vanilla", tg.vanilla},
	} {
		ca, pra, pwa := joinPlayBlackbox(t, tc.addr, "Chatter")
		defer ca.Close()
		drainToJoin(t, tc.tag+"-a", pra)
		cb, prb, _ := joinPlayBlackbox(t, tc.addr, "Listener")
		func() {
			defer cb.Close()
			drainToJoin(t, tc.tag+"-b", prb)
			const marker = "blackbox-hello-42"
			sendFullChat(t, pwa, server.IDPlayChatMessageSB, marker)
			_ = cb.SetDeadline(time.Now().Add(6 * time.Second))
			for {
				p, err := prb.ReadPacket()
				if err != nil {
					t.Fatalf("%s listener: %v (message never arrived)", tc.tag, err)
				}
				if p.ID == server.IDPlayDisconnect {
					t.Fatalf("%s listener kicked", tc.tag)
				}
				if bytes.Contains(p.Data, []byte(marker)) {
					if p.ID != server.IDPlayPlayerChatCB {
						t.Errorf("%s chat delivered as %#x, want player chat %#x", tc.tag, p.ID, server.IDPlayPlayerChatCB)
					}
					t.Logf("%s chat delivered as %#x (%d bytes)", tc.tag, p.ID, len(p.Data))
					return
				}
			}
		}()
	}
}

// TestJoinBurstSequence compares the vanilla-ordered pre-chunk join
// burst of both servers. Vanilla repeats Recipe Book Add with the real
// recipe data; mc-go sends the empty add once (crafting milestone), so
// consecutive duplicate adds are collapsed before comparing.
func TestJoinBurstSequence(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	collect := func(tag, addr string) []int32 {
		c, pr, _ := joinPlayBlackbox(t, addr, "BurstSeq")
		defer c.Close()
		var ids []int32
		for i := 0; i < 120; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s: %v", tag, err)
			}
			ids = append(ids, p.ID)
			if p.ID == server.IDPlaySetHealth {
				return ids
			}
		}
		t.Fatalf("%s: Set Health never arrived", tag)
		return nil
	}

	v := collect("vanilla", tg.vanilla)
	m := collect("mc-go", tg.mcgo)
	t.Logf("vanilla burst: %#x", v)
	t.Logf("mc-go burst:   %#x", m)

	// Keep only the first Recipe Book Add (vanilla repeats it with the
	// recipe data, a later milestone for mc-go) and drop tick-driven
	// traffic (Update Time, Keep Alive) that can interleave differently
	// between runs without changing the join state machine.
	collapse := func(ids []int32) []int32 {
		out := ids[:0:0]
		seenAdd := false
		for _, id := range ids {
			if id == server.IDPlayUpdateTime || id == server.IDPlayKeepAliveCB {
				continue
			}
			if id == server.IDPlayRecipeBookAdd {
				if seenAdd {
					continue
				}
				seenAdd = true
			}
			out = append(out, id)
		}
		return out
	}
	v = collapse(v)
	m = collapse(m)
	if len(v) != len(m) {
		t.Fatalf("burst length: vanilla %d, mc-go %d\nvanilla %#x\nmc-go   %#x", len(v), len(m), v, m)
	}
	for i := range v {
		if v[i] != m[i] {
			t.Fatalf("burst packet %d: vanilla %#x, mc-go %#x\nvanilla %#x\nmc-go   %#x", i, v[i], m[i], v, m)
		}
	}
}

// TestParallelStatus hammers both servers with concurrent status
// requests; every one must succeed.
func TestParallelStatus(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, addr := range []string{tg.vanilla, tg.mcgo} {
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, pr, pw := client(t, addr)
				defer c.Close()
				if err := handshake(pw, server.NextStatus); err != nil {
					errs <- err
					return
				}
				if err := pw.WritePacket(server.IDStatusRequest, nil); err != nil {
					errs <- err
					return
				}
				resp, err := pr.ReadPacket()
				if err != nil || resp.ID != server.IDStatusResponse {
					errs <- err
					return
				}
				errs <- nil
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("%s: parallel status failed: %v", addr, err)
			}
		}
	}
}
