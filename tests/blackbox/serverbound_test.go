package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Tests for the serverbound packets the router gained beyond the core
// movement/dig/container set. Each one drives both servers with the
// same scripted packet and compares the reply, so "mc-go handles this
// packet" means "mc-go answers like vanilla", not merely "mc-go does
// not crash".

// playSession is a logged-in client parked in the play stage with the
// join burst drained.
type playSession struct {
	conn interface {
		Close() error
		SetDeadline(time.Time) error
	}
	pr *protocol.Reader
	pw *protocol.Writer
}

// joinPlayOp joins addr and parks the session in the play stage. The
// join burst is not drained first: both servers keep sending on their
// own schedule (world time every second, chunks as batches are
// acknowledged), so there is no quiet moment to wait for. Callers
// drive their packet and use readUntil to pick the reply out of the
// stream, which answers liveness traffic as it goes.
func joinPlayOp(t *testing.T, addr, name string) playSession {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	// Vanilla disconnects a play-stage client that never announced its
	// settings, so the probe sends Client Information before anything
	// else, exactly as a real client does.
	sendClientInformation(t, pw)
	return playSession{conn: c, pr: pr, pw: pw}
}

// sendClientInformation sends a Client Information packet with the
// settings a vanilla client uses by default.
func sendClientInformation(t *testing.T, pw *protocol.Writer) {
	t.Helper()
	sendClientInformationVD(t, pw, 8)
}

func sendClientInformationVD(t *testing.T, pw *protocol.Writer, viewDist byte) {
	t.Helper()
	var b protocol.Buffer
	b.String("en_us")
	b.Byte(viewDist) // view distance
	b.VarInt(0)      // chat mode: enabled
	b.Bool(true)     // chat colors
	b.Byte(0x7F)     // skin parts: all
	b.VarInt(1)      // main hand: right
	b.Bool(false)    // text filtering
	b.Bool(true)     // server listings allowed
	b.VarInt(0)      // particle status: all
	if err := pw.WritePacket(server.IDPlayClientInfoSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// answerLiveness keeps the session alive against the packets a server
// sends on its own schedule, reporting whether the packet was one of
// them.
func answerLiveness(t *testing.T, s playSession, p protocol.Packet) bool {
	t.Helper()
	switch p.ID {
	case server.IDPlayKeepAliveCB:
		r := protocol.NewPacketReader(p.Data)
		if id, err := r.Long(); err == nil {
			var b protocol.Buffer
			b.Long(id)
			if err := s.pw.WritePacket(server.IDPlayKeepAliveSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
		return true
	case server.IDPlayChunkBatchDone:
		var b protocol.Buffer
		b.Float(25)
		if err := s.pw.WritePacket(server.IDPlayChunkBatchRecvSB, b.Bytes()); err != nil {
			t.Fatal(err)
		}
		return true
	}
	return false
}

// readUntil reads packets, answering liveness traffic, until match
// accepts one, which is then returned.
func readUntil(t *testing.T, addr string, s playSession, match func(protocol.Packet) bool) protocol.Packet {
	t.Helper()
	for i := 0; i < 20000; i++ {
		p, err := s.pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		if answerLiveness(t, s, p) {
			continue
		}
		if match(p) {
			return p
		}
	}
	t.Fatalf("%s: no matching packet", addr)
	return protocol.Packet{}
}

// TestGameRulesParity sends Client Status action 2 to both servers and
// requires the same rule table back. This is the wire-level proof that
// mc-go's default game rules are vanilla's.
func TestGameRulesParity(t *testing.T) {
	// Vanilla gates the game-rule table behind operator permission, so
	// the probe is listed in ops.json; mc-go's default server is open.
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("RulesProbe"),
	}, nil)
	defer tg.cleanup()

	read := func(addr string) map[string]string {
		s := joinPlayOp(t, addr, "RulesProbe")
		defer s.conn.Close()
		var req protocol.Buffer
		req.VarInt(2)
		if err := s.pw.WritePacket(server.IDPlayClientStatusSB, req.Bytes()); err != nil {
			t.Fatal(err)
		}
		p := readUntil(t, addr, s, func(p protocol.Packet) bool {
			return p.ID == server.IDPlayGameRuleValuesCB
		})
		r := protocol.NewPacketReader(p.Data)
		n, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s: rule count: %v", addr, err)
		}
		out := make(map[string]string, n)
		for j := int32(0); j < n; j++ {
			k, err := r.String()
			if err != nil {
				t.Fatalf("%s: rule %d name: %v", addr, j, err)
			}
			v, err := r.String()
			if err != nil {
				t.Fatalf("%s: rule %d value: %v", addr, j, err)
			}
			out[k] = v
		}
		return out
	}

	want := read(tg.vanilla)
	got := read(tg.mcgo)
	if len(want) == 0 {
		t.Fatal("vanilla returned no game rules")
	}
	for name, wv := range want {
		gv, ok := got[name]
		if !ok {
			t.Errorf("mc-go is missing game rule %s (vanilla %q)", name, wv)
			continue
		}
		if gv != wv {
			t.Errorf("game rule %s: mc-go %q, vanilla %q", name, gv, wv)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("mc-go has game rule %s that vanilla does not", name)
		}
	}
}

// TestPlayPingParity sends the play-stage Ping Request to both servers
// and requires the same Pong Response id back. The body is a Long in
// both directions: vanilla disconnects a client that sends the 4-byte
// Int form, which is what the wiki's omission of this packet's shape
// originally led mc-go to do.
func TestPlayPingParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	check := func(addr string) int64 {
		s := joinPlayOp(t, addr, "PingProbe")
		defer s.conn.Close()
		var req protocol.Buffer
		req.Long(0x5EED1234)
		if err := s.pw.WritePacket(server.IDPlayPingRequestSB, req.Bytes()); err != nil {
			t.Fatal(err)
		}
		p := readUntil(t, addr, s, func(p protocol.Packet) bool {
			return p.ID == server.IDPlayPongResponseCB
		})
		r := protocol.NewPacketReader(p.Data)
		id, err := r.Long()
		if err != nil {
			t.Fatalf("%s: pong id: %v", addr, err)
		}
		return id
	}

	want := check(tg.vanilla)
	got := check(tg.mcgo)
	if want != 0x5EED1234 {
		t.Fatalf("vanilla pong id = %#x, want the request id", want)
	}
	if got != want {
		t.Fatalf("mc-go pong id = %#x, vanilla %#x", got, want)
	}
}

// TestCommandSuggestionsShape drives a tab-completion request at both
// servers and requires the reply to be a well-formed Command
// Suggestions Response whose transaction id and replacement range cover
// the same span. The candidate lists differ (mc-go implements a subset
// of vanilla's commands), so the values are compared only where both
// servers offer them.
func TestCommandSuggestionsShape(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	type suggestion struct {
		start, length int32
		matches       []string
	}
	check := func(addr, text string) suggestion {
		s := joinPlayOp(t, addr, "SuggestProbe")
		defer s.conn.Close()
		var req protocol.Buffer
		req.VarInt(7)
		req.String(text)
		if err := s.pw.WritePacket(server.IDPlayCommandSuggestionSB, req.Bytes()); err != nil {
			t.Fatal(err)
		}
		p := readUntil(t, addr, s, func(p protocol.Packet) bool {
			return p.ID == server.IDPlayCommandSuggestionsCB
		})
		r := protocol.NewPacketReader(p.Data)
		tx, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s: tx: %v", addr, err)
		}
		if tx != 7 {
			t.Fatalf("%s: suggestion tx = %d, want 7", addr, tx)
		}
		start, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s: start: %v", addr, err)
		}
		length, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s: length: %v", addr, err)
		}
		n, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s: match count: %v", addr, err)
		}
		out := suggestion{start: start, length: length}
		for j := int32(0); j < n; j++ {
			m, err := r.String()
			if err != nil {
				t.Fatalf("%s: match %d: %v", addr, j, err)
			}
			if _, err := r.Bool(); err != nil {
				t.Fatalf("%s: match %d tooltip: %v", addr, j, err)
			}
			out.matches = append(out.matches, m)
		}
		return out
	}

	// "/gi" completes the command name itself, which both servers
	// resolve over the same span.
	want := check(tg.vanilla, "/gi")
	got := check(tg.mcgo, "/gi")
	if got.start != want.start || got.length != want.length {
		t.Fatalf("suggestion range mc-go (%d,%d), vanilla (%d,%d)",
			got.start, got.length, want.start, want.length)
	}
	// Every candidate mc-go offers must also be one vanilla offers,
	// since mc-go's registry is a subset of vanilla's.
	inVanilla := map[string]bool{}
	for _, m := range want.matches {
		inVanilla[m] = true
	}
	for _, m := range got.matches {
		if !inVanilla[m] {
			t.Errorf("mc-go suggested %q, which vanilla does not offer for %q", m, "/gi")
		}
	}
	if len(got.matches) == 0 {
		t.Errorf("mc-go offered no completion for %q (vanilla: %v)", "/gi", want.matches)
	}
}

// TestChangeGameModeParity sends Change Game Mode to both servers as an
// operator and requires the same Game Event packet back.
func TestChangeGameModeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ModeProbe"),
	}, func(s *server.Server) { s.OpsRequired = false })
	defer tg.cleanup()

	check := func(addr string) (byte, float32) {
		s := joinPlayOp(t, addr, "ModeProbe")
		defer s.conn.Close()
		var req protocol.Buffer
		req.VarInt(server.GameModeAdventure)
		if err := s.pw.WritePacket(server.IDPlayChangeGameModeSB, req.Bytes()); err != nil {
			t.Fatal(err)
		}
		p := readUntil(t, addr, s, func(p protocol.Packet) bool {
			if p.ID != server.IDPlayGameEvent {
				return false
			}
			r := protocol.NewPacketReader(p.Data)
			event, err := r.Byte()
			return err == nil && event == 3
		})
		r := protocol.NewPacketReader(p.Data)
		event, err := r.Byte()
		if err != nil {
			t.Fatalf("%s: game event: %v", addr, err)
		}
		value, err := r.Float()
		if err != nil {
			t.Fatalf("%s: game event value: %v", addr, err)
		}
		return event, value
	}

	wantEvent, wantValue := check(tg.vanilla)
	gotEvent, gotValue := check(tg.mcgo)
	if wantEvent != gotEvent || wantValue != gotValue {
		t.Fatalf("game event mc-go (%d,%v), vanilla (%d,%v)", gotEvent, gotValue, wantEvent, wantValue)
	}
}
