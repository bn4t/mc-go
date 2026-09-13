package server

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestTpCoords(t *testing.T) {
	s := New("unused")
	enforceSession(t, s, "Warden")

	if out := s.runTp("Warden", nil); !strings.HasPrefix(out, "Usage:") {
		t.Fatalf("tp usage = %q", out)
	}
	if out := s.runTp("Warden", []string{"Ghost"}); out != "Unknown player: Ghost" {
		t.Fatalf("tp ghost = %q", out)
	}
	if out := s.runTp("Warden", []string{"100", "oops", "100"}); out != "Invalid y: oops" {
		t.Fatalf("tp bad y = %q", out)
	}
	if out := s.runTp("Warden", []string{"99999999", "70", "0"}); out != "Position outside the world" {
		t.Fatalf("tp border = %q", out)
	}
	out := s.runTp("Warden", []string{"100", "70", "-40"})
	if out != "Teleported to 100.00 70.00 -40.00" {
		t.Fatalf("tp = %q", out)
	}
	st, ok := s.PlayerStateOf("Warden")
	if !ok || st.X != 100 || st.Y != 70 || st.Z != -40 {
		t.Fatalf("state = %+v,%v", st, ok)
	}
	// "~" relativity resolves against the sender's block position.
	out = s.runTp("Warden", []string{"~1", "~", "~-1"})
	if out != "Teleported to 101.00 70.00 -41.00" {
		t.Fatalf("tp relative = %q", out)
	}
}

func TestTpPlayerToPlayer(t *testing.T) {
	s := New("unused")
	enforceSession(t, s, "Warden")
	enforceSession(t, s, "Mover")
	s.TeleportPlayer("Mover", 50, 66, 50)

	if out := s.runTp("Warden", []string{"Mover"}); out != "Teleported to Mover" {
		t.Fatalf("tp player = %q", out)
	}
	st, _ := s.PlayerStateOf("Warden")
	if st.X != 50 || st.Y != 66 || st.Z != 50 {
		t.Fatalf("state = %+v", st)
	}
	if out := s.runTp("Warden", []string{"Mover", "1", "65", "1", "extra"}); !strings.HasPrefix(out, "Usage:") {
		t.Fatalf("tp 5 args = %q", out)
	}
	if out := s.runTp("Warden", []string{"Ghost", "1", "65", "1"}); out != "Unknown player: Ghost" {
		t.Fatalf("tp ghost victim = %q", out)
	}
	if out := s.runTp("Warden", []string{"Mover", "1", "65", "1"}); out != "Teleported Mover to 1.00 65.00 1.00" {
		t.Fatalf("tp victim = %q", out)
	}
}

func TestKickCommand(t *testing.T) {
	s := New("unused")
	sess, _ := enforceSession(t, s, "Warden")

	if out := s.runKick("Op", nil); !strings.HasPrefix(out, "Usage:") {
		t.Fatalf("kick usage = %q", out)
	}
	if out := s.runKick("Op", []string{"Ghost"}); out != "Unknown player: Ghost" {
		t.Fatalf("kick ghost = %q", out)
	}
	if out := s.runKick("Op", []string{"Warden", "griefing"}); out != "Kicked Warden" {
		t.Fatalf("kick = %q", out)
	}
	select {
	case pkt := <-sess.sendCh:
		if pkt.id != IDPlayDisconnect {
			t.Fatalf("kick packet = %#x", pkt.id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no kick packet")
	}
}

func TestSaveWorldDisabledAndRoundtrip(t *testing.T) {
	s := New("unused")
	if err := s.SaveWorld(); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("save without path = %v", err)
	}
	if out, ok := s.commands.Run("save-all", "Op", nil); !ok || !strings.Contains(out, "failed") {
		t.Fatalf("save-all disabled = %q,%v", out, ok)
	}

	s.WorldPath = filepath.Join(t.TempDir(), "world.json")
	s.world.SetBlock(world.Pos{X: 8, Y: 65, Z: 8}, world.Stone)
	if out, ok := s.commands.Run("save-all", "Op", nil); !ok || out != "Saved the game" {
		t.Fatalf("save-all = %q,%v", out, ok)
	}
	raw, err := os.ReadFile(s.WorldPath)
	if err != nil || len(raw) == 0 {
		t.Fatalf("save file: %v %d bytes", err, len(raw))
	}
}

// stubListener is an in-memory listener so shutdown is testable where
// loopback binds are blocked.
type stubListener struct{ closed chan struct{} }

func (l *stubListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *stubListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}
func (l *stubListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestShutdown(t *testing.T) {
	s := New("unused")
	ln := &stubListener{closed: make(chan struct{})}
	s.addListener(ln)
	sess, _ := enforceSession(t, s, "Warden")

	s.Shutdown("bye")
	s.Shutdown("bye") // idempotent
	if !s.ShuttingDown() {
		t.Fatal("not marked shutting down")
	}
	select {
	case <-ln.closed:
	default:
		t.Fatal("listener not closed by shutdown")
	}
	select {
	case pkt := <-sess.sendCh:
		if pkt.id != IDPlayDisconnect {
			t.Fatalf("shutdown kick = %#x", pkt.id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no shutdown kick")
	}
}

// joinPlayPipe drives one scripted client through login and config to a
// quiet play state over an in-memory pipe (no TCP needed).
func joinPlayPipe(t *testing.T, s *Server, name string) (*protocol.Reader, *protocol.Writer) {
	t.Helper()
	_, pr, pw := joinPlayPipeConn(t, s, name)
	return pr, pw
}

func joinPlayPipeConn(t *testing.T, s *Server, name string) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	c, pr, pw := pipePair(t, s)
	if err := sendHandshake(pw, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var b protocol.Buffer
	b.String(name)
	var zero [16]byte
	b.Raw(zero[:])
	if err := pw.WritePacket(IDLoginStart, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	sc, err := pr.ReadPacket()
	if err != nil || sc.ID != IDLoginSetCompression {
		t.Fatalf("%s compression = %#x, err %v", name, sc.ID, err)
	}
	th, err := protocol.NewPacketReader(sc.Data).VarInt()
	if err != nil {
		t.Fatal(err)
	}
	pr.SetThreshold(th)
	pw.SetThreshold(th)
	if _, err := pr.ReadPacket(); err != nil { // Login Success
		t.Fatal(err)
	}
	if err := pw.WritePacket(IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	var kp protocol.Buffer
	writeKnownPacks(&kp, []packEntry{{"minecraft", "core", "99.9-test"}})
	if err := pw.WritePacket(IDConfigKnownPacks, kp.Bytes()); err != nil {
		t.Fatal(err)
	}
	// Drain configuration through Finish Configuration and ack it;
	// the server enters play only after the ack.
	for i := 0; i < 64; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s config: %v", name, err)
		}
		if p.ID == IDConfigFinish {
			break
		}
	}
	if err := pw.WritePacket(IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	// A real client reports its terrain loaded a fraction of a second
	// after entering play, which is what opens its world interactions;
	// this scripted client does the same so tests exercise the same
	// world (see interactionsOpen for the gate the report releases).
	if err := pw.WritePacket(IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	// Drain the join burst through our own tab-list row; anything else
	// (chunks, pig, abilities) is skipped, not asserted, here.
	for i := 0; i < 80; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("%s burst: %v", name, err)
		}
		if p.ID == IDPlayPlayerInfoUpdate {
			return c, pr, pw
		}
	}
	t.Fatalf("%s: no player info in join burst", name)
	return nil, nil, nil
}

func sendCmdPipe(t *testing.T, pw *protocol.Writer, cmd string) {
	t.Helper()
	var b protocol.Buffer
	b.String(cmd)
	b.Long(0)
	b.Long(0)
	if err := pw.WritePacket(IDPlayChatCommandSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// readChatPipe returns the next system-chat text, skipping join-burst
// and stream packets.
func readChatPipe(t *testing.T, pr *protocol.Reader) string {
	t.Helper()
	for i := 0; i < 80; i++ {
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("chat stream: %v", err)
		}
		if p.ID != IDPlaySystemChatCB {
			continue
		}
		root, err := nbt.ReadUnnamed(bytes.NewReader(p.Data))
		if err != nil {
			t.Fatalf("chat nbt: %v", err)
		}
		text, ok := root.(string)
		if !ok {
			t.Fatalf("chat root = %T, want string", root)
		}
		return text
	}
	t.Fatal("no system chat")
	return ""
}

func TestOpsFlowOverWire(t *testing.T) {
	s := New("unused")
	pra, pwa := joinPlayPipe(t, s, "Alice")
	prb, _ := joinPlayPipe(t, s, "Bob")
	// Alice observes Bob's join notice before commanding.
	if text := readChatPipe(t, pra); !strings.Contains(text, "Bob") || !strings.Contains(text, "joined") {
		t.Fatalf("join text = %q", text)
	}

	sendCmdPipe(t, pwa, "tp 100 70 100")
	if out := readChatPipe(t, pra); out != "Teleported to 100.00 70.00 100.00" {
		t.Fatalf("/tp = %q", out)
	}
	sendCmdPipe(t, pwa, "pos")
	if out := readChatPipe(t, pra); !strings.HasPrefix(out, "100.00 70.00 100.00") {
		t.Fatalf("/pos = %q", out)
	}

	sendCmdPipe(t, pwa, "kick Bob griefing")
	if out := readChatPipe(t, pra); out != "Kicked Bob" {
		t.Fatalf("/kick = %q", out)
	}
	// Bob's next packet is the kick disconnect.
	for i := 0; i < 256; i++ {
		p, err := prb.ReadPacket()
		if err != nil {
			t.Fatalf("bob stream: %v", err)
		}
		if p.ID == IDPlayDisconnect {
			break
		}
		if i == 255 {
			t.Fatalf("bob never kicked, last = %#x", p.ID)
		}
	}
	if text := readChatPipe(t, pra); !strings.Contains(text, "left") {
		t.Fatalf("leave text = %q", text)
	}

	sendCmdPipe(t, pwa, "stop")
	if out := readChatPipe(t, pra); out != "Stopping the server" {
		t.Fatalf("/stop = %q", out)
	}
	for i := 0; i < 20; i++ {
		p, err := pra.ReadPacket()
		if err != nil {
			break // socket close after the kick also ends the flow
		}
		if p.ID == IDPlayDisconnect {
			break
		}
		if i == 19 {
			t.Fatalf("alice never stopped, last = %#x", p.ID)
		}
	}
	if !s.ShuttingDown() {
		t.Fatal("server not shutting down after /stop")
	}
}
