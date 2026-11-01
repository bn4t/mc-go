package server

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func TestTpCoords(t *testing.T) {
	s := New("unused")
	runTp := func(args []string) (out reply) {
		s.simulationAction(func() { out = s.runTp("Warden", args) })
		return
	}
	enforceSession(t, s, "Warden")

	if out := runTp(nil); out.comp.extra[0].translate != "command.unknown.command" {
		t.Fatalf("tp usage = %+v", out.comp)
	}
	if out := runTp([]string{"Ghost"}); out.comp.extra[0].translate != "argument.entity.notfound.entity" {
		t.Fatalf("tp ghost = %+v", out.comp)
	}
	if out := runTp([]string{"100", "oops", "100"}); out.comp.extra[0].translate != "command.unknown.argument" {
		t.Fatalf("tp bad y = %+v", out.comp)
	}
	if out := runTp([]string{"99999999", "70", "0"}); out.comp.extra[0].translate != "commands.teleport.invalidPosition" {
		t.Fatalf("tp border = %+v", out.comp)
	}
	out := runTp([]string{"100", "70", "-40"})
	if out.comp.translate != "commands.teleport.success.location.single" {
		t.Fatalf("tp = %+v", out.comp)
	}
	// Vanilla's vec3 arg centers bare integers on the block column:
	// x and z land on .5, y never centers.
	if len(out.comp.with) != 4 || out.comp.with[1].(nestedCompound).value != "100.500000" ||
		out.comp.with[2].(nestedCompound).value != "70.000000" ||
		out.comp.with[3].(nestedCompound).value != "-39.500000" {
		t.Fatalf("tp coords = %v", out.comp.with)
	}
	st, ok := s.PlayerStateOf("Warden")
	if !ok || st.X != 100.5 || st.Y != 70 || st.Z != -39.5 {
		t.Fatalf("state = %+v,%v", st, ok)
	}
	// "~" relativity resolves against the sender's exact position —
	// the centered (100.5, 70, -39.5) spot.
	out = runTp([]string{"~1", "~", "~-1"})
	if len(out.comp.with) != 4 || out.comp.with[1].(nestedCompound).value != "101.500000" ||
		out.comp.with[2].(nestedCompound).value != "70.000000" ||
		out.comp.with[3].(nestedCompound).value != "-40.500000" {
		t.Fatalf("tp relative = %v", out.comp.with)
	}
}

func TestTpPlayerToPlayer(t *testing.T) {
	s := New("unused")
	runTp := func(args []string) (out reply) {
		s.simulationAction(func() { out = s.runTp("Warden", args) })
		return
	}
	enforceSession(t, s, "Warden")
	enforceSession(t, s, "Mover")
	s.TeleportPlayer("Mover", 50, 66, 50)

	if out := runTp([]string{"Mover"}); out.comp.translate != "commands.teleport.success.entity.single" {
		t.Fatalf("tp player = %+v", out.comp)
	}
	st, _ := s.PlayerStateOf("Warden")
	if st.X != 50 || st.Y != 66 || st.Z != 50 {
		t.Fatalf("state = %+v", st)
	}
	if out := runTp([]string{"Mover", "1", "65", "1", "extra"}); out.comp.extra[0].translate != "command.unknown.argument" {
		t.Fatalf("tp 5 args = %+v", out.comp)
	}
	if out := runTp([]string{"Ghost", "1", "65", "1"}); out.comp.extra[0].translate != "argument.entity.notfound.entity" {
		t.Fatalf("tp ghost victim = %+v", out.comp)
	}
	if out := runTp([]string{"Mover", "1", "65", "1"}); out.comp.translate != "commands.teleport.success.location.single" {
		t.Fatalf("tp victim = %+v", out.comp)
	}
}

func TestKickCommand(t *testing.T) {
	for _, issuer := range []string{"Op", "Warden"} {
		t.Run(issuer, func(t *testing.T) {
			s := New("unused")
			sess, _ := enforceSession(t, s, "Warden")

			if out := s.runKickCmd(issuer, nil); out.fail == nil {
				t.Fatalf("kick usage = %+v", out)
			}
			if out := s.runKickCmd(issuer, []string{"Ghost"}); out.comp.translate != "" {
				t.Fatalf("kick ghost = %+v", out)
			}
			out := s.runKickCmd(issuer, []string{"Warden", "griefing"})
			if out.comp.translate != "commands.kick.success" || out.omitFeedbackFor != sess || out.outcome != commandSucceeded {
				t.Fatalf("kick = %+v", out)
			}
			if issuer == "Warden" {
				s.sendReply(sess, out)
			}
			select {
			case pkt := <-sess.sendCh:
				pkt = materializeOutboundPacket(t, sess, pkt)
				if pkt.id != IDPlayDisconnect {
					t.Fatalf("kick packet = %#x", pkt.id)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("no kick packet")
			}
			if !sess.closing.Load() {
				t.Fatal("kick did not mark target closing")
			}
			// The fake transport has no reader. Model its deferred cleanup
			// under the same owner, then prove repetition is harmless.
			s.simulationAction(func() { s.untrack(sess) })
			if !sess.untracked.Load() || !sess.playerRecord.left || sess.playerRecord.active.Load() {
				t.Fatal("deferred cleanup did not retire kick target")
			}
			if _, exists := s.sessions.Load(sess.id); exists {
				t.Fatal("kick target remains tracked")
			}
			if got := s.online.Load(); got != 0 {
				t.Fatalf("online after kick = %d", got)
			}
			sess.statMu.Lock()
			before := make(map[statKey]int32)
			for k, v := range sess.stats.counts {
				before[k] = v
			}
			sess.statMu.Unlock()
			savedBefore := s.savedPlayers["Warden"]
			// The eventual socket teardown must not repeat retirement, saved state
			// or leave stats, including when the command issuer kicks itself.
			s.simulationAction(func() { s.untrack(sess) })
			if !reflect.DeepEqual(savedBefore, s.savedPlayers["Warden"]) {
				t.Fatal("deferred untrack replaced saved player state")
			}
			sess.statMu.Lock()
			defer sess.statMu.Unlock()
			if len(before) != len(sess.stats.counts) {
				t.Fatal("deferred untrack added stats")
			}
			for k, v := range before {
				if sess.stats.counts[k] != v {
					t.Fatalf("deferred untrack repeated stat %v", k)
				}
			}
			if got := s.online.Load(); got != 0 {
				t.Fatalf("online after deferred untrack = %d", got)
			}

		})
	}
}

// Changing the executor must not change the actual feedback recipient.
func TestKickExecuteRecipientAndResult(t *testing.T) {
	for _, executor := range []string{"P", "Q"} {
		t.Run(executor, func(t *testing.T) {
			s := New("unused")
			p, _ := enforceSession(t, s, "P")
			q, _ := enforceSession(t, s, "Q")
			drain := func(sess *Session) {
				for {
					select {
					case <-sess.sendCh:
					default:
						return
					}
				}
			}
			drain(p)
			drain(q)
			invoke := func(line string) reply {
				var r reply
				s.simulationAction(func() {
					words := splitCommandArgs(line)
					var known bool
					r, known = s.runCommand("P", words[0], words[1:])
					if !known {
						t.Fatalf("unknown %s", line)
					}
				})
				return r
			}
			invoke("scoreboard objectives add kickProof dummy")
			r := invoke("execute as " + executor + " store result score result kickProof store success score success kickProof run kick Q reason")
			if r.outcome == commandFailed || !q.closing.Load() || p.untracked.Load() {
				t.Fatalf("execute kick state: %+v", r)
			}
			for _, holder := range []string{"result", "success"} {
				got := invoke("scoreboard players get " + holder + " kickProof")
				if !got.hasResult || got.result != 1 {
					t.Fatalf("%s store = %+v", holder, got)
				}
			}
			// Leave tracking packets may precede feedback; neither executor
			// rebinding nor the target's removal may suppress P's success.
			s.simulationAction(func() { s.sendReply(p, r) })
			successes := 0
			for len(p.sendCh) > 0 {
				packet := materializeOutboundPacket(t, p, <-p.sendCh)
				if packet.id != IDPlaySystemChatCB {
					continue
				}
				root, err := nbt.ReadUnnamed(bytes.NewReader(packet.data))
				if err != nil {
					t.Fatal(err)
				}
				if m, ok := root.(map[string]any); ok && m["translate"] == "commands.kick.success" {
					successes++
				}
			}
			if successes != 1 {
				t.Fatalf("issuer kick feedback count%d", successes)
			}
		})
	}
}

func TestKickFeedbackRecipientExclusion(t *testing.T) {
	for _, source := range []string{"direct", "execute", "rcon"} {
		t.Run(source, func(t *testing.T) {
			s := New("unused")
			p, _ := enforceSession(t, s, "P")
			q, _ := enforceSession(t, s, "Q")
			observer, _ := enforceSession(t, s, "R")
			s.ops = map[string]int{"P": 4, "Q": 4, "R": 4}
			for _, sess := range []*Session{p, q, observer} {
				for len(sess.sendCh) > 0 {
					<-sess.sendCh
				}
			}
			s.simulationAction(func() {
				if source == "rcon" {
					if text := s.rconRunOwned("kick Q reason"); !strings.Contains(text, "Q") {
						t.Fatalf("RCON feedback %q", text)
					}
					return
				}
				command := "kick Q reason"
				if source == "execute" {
					command = "execute as Q run kick Q reason"
				}
				words := splitCommandArgs(command)
				r, known := s.runCommand("P", words[0], words[1:])
				if !known {
					t.Fatal("unknown kick")
				}
				s.sendReply(p, r)
			})
			if _, ok := s.sessions.Load(q.id); !ok {
				t.Fatal("kick unexpectedly changed deferred transport cleanup")
			}
			chats := func(sess *Session) (direct, admin, disconnects int) {
				for len(sess.sendCh) > 0 {
					packet := materializeOutboundPacket(t, sess, <-sess.sendCh)
					if packet.id == IDPlayDisconnect {
						disconnects++
						continue
					}
					if packet.id != IDPlaySystemChatCB {
						continue
					}
					root, err := nbt.ReadUnnamed(bytes.NewReader(packet.data))
					if err != nil {
						t.Fatal(err)
					}
					if m, ok := root.(map[string]any); ok {
						switch m["translate"] {
						case "commands.kick.success":
							direct++
						case "chat.type.admin":
							admin++
						}
					}
				}
				return
			}
			direct, admin, disconnects := chats(q)
			if direct != 0 || admin != 0 || disconnects != 1 {
				t.Fatalf("target feedback direct%d admin%d disconnect%d", direct, admin, disconnects)
			}
			direct, admin, _ = chats(p)
			if source == "rcon" {
				if direct != 0 || admin != 1 {
					t.Fatalf("P RCON observer direct%d admin%d", direct, admin)
				}
			} else if direct != 1 || admin != 0 {
				t.Fatalf("P issuer direct%d admin%d", direct, admin)
			}
			direct, admin, _ = chats(observer)
			// Execute's existing operator-echo policy is outside this recipient
			// control; its proven contract here is P feedback and Q exclusion.
			if direct != 0 || source != "execute" && admin != 1 {
				t.Fatalf("R observer direct%d admin%d", direct, admin)
			}
			s.simulationAction(func() { s.untrack(q); s.untrack(q) })
		})
	}
}

func TestSaveWorldDisabledAndRoundtrip(t *testing.T) {
	s := New("unused")
	if err := s.SaveWorld(); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("save without path = %v", err)
	}
	r, ok := s.runCommand("Op", "save-all", nil)
	if !ok || len(r.comp.extra) == 0 || r.comp.extra[0].translate != "commands.save.failed" {
		t.Fatalf("save-all disabled = %+v,%v", r, ok)
	}

	s.WorldPath = filepath.Join(t.TempDir(), "world.json")
	s.world.SetBlock(world.Pos{X: 8, Y: 65, Z: 8}, world.Stone)
	r, ok = s.runCommand("Op", "save-all", nil)
	if !ok || r.comp.translate != "commands.save.saving" {
		t.Fatalf("save-all = %+v,%v", r, ok)
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
		pkt = materializeOutboundPacket(t, sess, pkt)
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
		return flattenChatText(root)
	}
	t.Fatal("no system chat")
	return ""
}

// flattenChatText renders a decoded chat component to a searchable
// string: the translate key plus every nested text value.
func flattenChatText(root any) string {
	var out strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			out.WriteString(t)
			out.WriteByte(' ')
		case map[string]any:
			for _, k := range []string{"translate", "text"} {
				if s, ok := t[k].(string); ok {
					out.WriteString(s)
					out.WriteByte(' ')
				}
			}
			for _, k := range []string{"with", "extra"} {
				walk(t[k])
			}
		case []any:
			for _, it := range t {
				walk(it)
			}
		case nbt.List:
			for _, it := range t.Items {
				walk(it)
			}
		}
	}
	walk(root)
	return out.String()
}

// readChatPipeRoot returns the translate key of the next system chat,
// for the commands whose replies are translatable components rather
// than literals.
func readChatPipeRoot(t *testing.T, pr *protocol.Reader) any {
	t.Helper()
	for i := 0; i < 256; i++ {
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
		if s, ok := root.(string); ok {
			return s
		}
		m, ok := root.(map[string]any)
		if !ok {
			t.Fatalf("chat root = %T", root)
		}
		return m["translate"]
	}
	t.Fatal("no system chat")
	return nil
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
	if out := readChatPipeRoot(t, pra); out != "commands.teleport.success.location.single" {
		t.Fatalf("/tp translate = %v", out)
	}
	sendCmdPipe(t, pwa, "pos")
	if out := readChatPipe(t, pra); !strings.HasPrefix(out, "100.50 70.00 100.50") {
		t.Fatalf("/pos = %q", out)
	}

	sendCmdPipe(t, pwa, "kick Bob griefing")
	if out := readChatPipe(t, pra); !strings.Contains(out, "commands.kick.success") {
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
	if out := readChatPipe(t, pra); !strings.Contains(out, "commands.stop.stopping") {
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
