package blackbox

// Survival digging parity: the same scripted dig sequences are replayed
// against the real server and against mc-go, and the two must agree on
// what the server destroys. Vanilla is the authority on break timing --
// the client's finish (stop) only commits a dig whose progress the
// server measured itself -- so the sequences cover a start on its own,
// an abort, a duplicate start, a stop at once and after a delay, and a
// player who keeps swinging or ticking without stopping. Measured
// against the real server (26.2): grass bare-handed takes 18 ticks and
// stone 150, so a start without a stop never destroys anything.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// digEvent is one Block Update seen on the wire.
type digEvent struct {
	at      time.Time
	x, y, z int32
	state   int32
}

// digSink is a self-driving client sink: it answers keep-alive and
// teleport packets, records Block Updates with arrival times, and keeps
// small payloads as text.
type digSink struct {
	mu     sync.Mutex
	chat   []string
	events []digEvent
	err    error
}

func newDigSink(pr *protocol.Reader, pw *protocol.Writer) *digSink {
	s := &digSink{}
	var wmu sync.Mutex
	go func() {
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
			now := time.Now()
			s.mu.Lock()
			if p.ID == server.IDPlayBlockUpdateCB {
				r := protocol.NewPacketReader(p.Data)
				if x, y, z, err := r.Position(); err == nil {
					if st, err := r.VarInt(); err == nil {
						s.events = append(s.events, digEvent{at: now, x: x, y: y, z: z, state: st})
					}
				}
			}
			if len(p.Data) < 4096 {
				s.chat = append(s.chat, string(p.Data))
			}
			s.mu.Unlock()
			wmu.Lock()
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
				}
			}
			wmu.Unlock()
		}
	}()
	return s
}

func (s *digSink) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.chat
	s.chat = nil
	return out
}

func (s *digSink) has(marker string) bool { return sawMarker(s.take(), marker) }

func (s *digSink) takeEvents() []digEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.events
	s.events = nil
	return out
}

func digSentinel(t *testing.T, pw *protocol.Writer, s *digSink, tag string, timeout time.Duration) bool {
	t.Helper()
	sendCommand(t, pw, "say "+tag)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.has(tag) {
			return true
		}
		time.Sleep(60 * time.Millisecond)
	}
	return false
}

// digProbe asks a paired predicate question and returns the answer.
func digProbe(t *testing.T, pw *protocol.Writer, s *digSink, cond string) bool {
	t.Helper()
	s.take()
	sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZZY", cond))
	sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZZN", cond))
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		lines := s.take()
		if sawMarker(lines, "ZZY") {
			return true
		}
		if sawMarker(lines, "ZZN") {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("probe %q never answered: %v", cond, s.err)
	return false
}

func digTickEnd(t *testing.T, pw *protocol.Writer) {
	t.Helper()
	if err := pw.WritePacket(server.IDPlayClientTickEndSB, nil); err != nil {
		t.Fatal(err)
	}
}

func digInput(t *testing.T, pw *protocol.Writer) {
	t.Helper()
	var b protocol.Buffer
	b.Byte(0)
	if err := pw.WritePacket(server.IDPlayPlayerInputSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

func digSwingArm(t *testing.T, pw *protocol.Writer) {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(0)
	if err := pw.WritePacket(server.IDPlaySwingArmSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// digCase is one dig-packet sequence, run on its own connection so no
// dig state can leak between cases.
type digCase struct {
	name  string
	block string
	at    [3]int32
	stand [3]float64
	yaw   float32
	pitch float32
	wait  time.Duration
	run   func(t *testing.T, pw *protocol.Writer)
}

// digOutcome is what a case produced: whether the target ended up air
// and, if it did, how many ticks after the sequence started the block
// update arrived.
type digOutcome struct {
	broke bool
	ticks float64
}

// runDigCase joins a fresh connection, places the target block, waits
// for the player to be ready, runs the packet sequence, and reports
// what happened to the block.
func runDigCase(t *testing.T, addr string, cs digCase) digOutcome {
	t.Helper()
	x, y, z := cs.at[0], cs.at[1], cs.at[2]
	c, pr, pw := joinPlayBlackbox(t, addr, "DigOracle")
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newDigSink(pr, pw)
	place := func() {
		sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d minecraft:%s", x, y, z, cs.block))
		digSentinel(t, pw, sink, "SBOK", 10*time.Second)
	}
	if !digSentinel(t, pw, sink, "READY", 30*time.Second) {
		t.Fatalf("%s: never ready: %v", cs.name, sink.err)
	}
	if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	sendCommand(t, pw, fmt.Sprintf("tp @s %v %v %v %v %v",
		cs.stand[0], cs.stand[1], cs.stand[2], cs.yaw, cs.pitch))
	digSentinel(t, pw, sink, "TPOK", 10*time.Second)
	place()
	if !digProbe(t, pw, sink, fmt.Sprintf("block %d %d %d minecraft:%s", x, y, z, cs.block)) {
		t.Fatalf("%s: block not set", cs.name)
	}
	time.Sleep(200 * time.Millisecond)
	sink.takeEvents()
	start := time.Now()
	cs.run(t, pw)
	time.Sleep(cs.wait)
	var out digOutcome
	for _, ev := range sink.takeEvents() {
		if ev.x == x && ev.y == y && ev.z == z && ev.state == 0 && !out.broke {
			out.broke = true
			out.ticks = ev.at.Sub(start).Seconds() * 20
		}
	}
	if !out.broke {
		if digProbe(t, pw, sink, fmt.Sprintf("block %d %d %d minecraft:air", x, y, z)) {
			t.Fatalf("%s: block became air with no update seen", cs.name)
		}
	}
	t.Logf("%-28s broke=%-5v ticks=%.1f", cs.name, out.broke, out.ticks)
	return out
}

// digCaseOrder is the fixed comparison order, and the sequences
// themselves. Every one targets grass under the player's feet unless it
// says otherwise; the stone case proves the slow rate for a block that
// needs a tool.
func digCaseList() []digCase {
	below := [3]int32{8, -61, 0}
	stand := [3]float64{8.5, -60, 0.5}
	grass := func(name string, wait time.Duration, run func(*testing.T, *protocol.Writer)) digCase {
		return digCase{name: name, block: "grass_block", at: below, stand: stand, yaw: 0, pitch: 90, wait: wait, run: run}
	}
	start := func(t *testing.T, pw *protocol.Writer, seq int32) {
		sendDig(t, pw, 0, below[0], below[1], below[2], seq)
	}
	stop := func(t *testing.T, pw *protocol.Writer, seq int32) {
		sendDig(t, pw, 2, below[0], below[1], below[2], seq)
	}
	grassWait := 3 * time.Second
	return []digCase{
		grass("start only", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 11)
		}),
		grass("start+stop", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 12)
			stop(t, pw, 13)
		}),
		grass("start+stop after 300ms", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 14)
			time.Sleep(300 * time.Millisecond)
			stop(t, pw, 15)
		}),
		grass("start+abort after 300ms", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 16)
			time.Sleep(300 * time.Millisecond)
			sendDig(t, pw, 1, below[0], below[1], below[2], 17)
		}),
		grass("start+start after 300ms", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 18)
			time.Sleep(300 * time.Millisecond)
			start(t, pw, 19)
		}),
		grass("start+swing every 50ms", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 20)
			for i := 0; i < 55; i++ {
				digSwingArm(t, pw)
				time.Sleep(50 * time.Millisecond)
			}
		}),
		grass("start+tickend every 50ms", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 21)
			for i := 0; i < 55; i++ {
				digTickEnd(t, pw)
				time.Sleep(50 * time.Millisecond)
			}
		}),
		grass("start+tickend+input 50ms", grassWait, func(t *testing.T, pw *protocol.Writer) {
			start(t, pw, 22)
			for i := 0; i < 55; i++ {
				digTickEnd(t, pw)
				digInput(t, pw)
				time.Sleep(50 * time.Millisecond)
			}
		}),
		{
			// Stone needs a pickaxe: bare hands run at a fifth of the
			// speed, 150 ticks instead of 30 for a hardness of 1.5.
			name: "stone start+stop", block: "stone", at: below, stand: stand, yaw: 0, pitch: 90,
			wait: 9 * time.Second,
			run: func(t *testing.T, pw *protocol.Writer) {
				start(t, pw, 23)
				stop(t, pw, 24)
			},
		},
	}
}

// digCaseNames is the fixed comparison order.
func digCaseNames() []string {
	names := make([]string, 0, 9)
	for _, cs := range digCaseList() {
		names = append(names, cs.name)
	}
	return names
}

// runDigCases replays every sequence against one server.
func runDigCases(t *testing.T, addr string) map[string]digOutcome {
	t.Helper()
	out := map[string]digOutcome{}
	for _, cs := range digCaseList() {
		out[cs.name] = runDigCase(t, addr, cs)
	}
	return out
}

// TestDigParity proves mc-go destroys the same blocks as the real server
// for the same client sequences, at the same tick-progress time.
func TestDigParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("DigOracle"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runDigCases(t, tg.vanilla)
	got := runDigCases(t, tg.mcgo)

	const tickSlack = 8.0
	for _, name := range digCaseNames() {
		w, g := want[name], got[name]
		if w.broke != g.broke {
			t.Errorf("%s: vanilla broke=%v, mc-go broke=%v", name, w.broke, g.broke)
			continue
		}
		if w.broke && (g.ticks-w.ticks > tickSlack || w.ticks-g.ticks > tickSlack) {
			t.Errorf("%s: break time %.1f ticks (vanilla) vs %.1f (mc-go)", name, w.ticks, g.ticks)
		}
	}
}
