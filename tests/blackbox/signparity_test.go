package blackbox

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// Sign interaction parity: one script, run against the real server and
// against mc-go, compared step by step.
//
// What a sign does is only visible from the packets: which side the
// editor opens on, and the block entity data a dye, a glow ink sac, an
// ink sac or a honeycomb makes the server send. A second client stands
// by and records the same updates, so a change is known to reach every
// player tracking the sign and not only the one who made it.
const signParityName = "SignParity"

// signCase is one setup of the script: a sign block, a player standing
// some way from it, and whether the whole item sequence runs there.
type signCase struct {
	name  string
	block string
	pos   [3]int
	dx    float64
	dz    float64
	face  int32
	yaw   float32
	items bool
}

// signCases covers the side rule at its corners - a standing sign of
// rotation 0 with the player on each side of it, a rotation whose front
// faces west, a wall sign facing north seen from both sides, a hanging
// sign, and a bare oak_sign, whose default rotation is 8 rather than 0
// (measured: a player south of it is behind it). Two of them are asked
// from a corner, where the clicked face and the player's own side
// disagree, which is how the rule is known to follow the player.
//
// No case puts the player square on to a sign's edge, where the two
// sides meet: the real server's answer there is a knife edge (measured
// by walking a player across the line - at exactly the block's own
// coordinate the real server names the side the rule below does not, and
// at the line's centre it has answered differently for different
// rotations). Every case is a clear half-block or more to one side.
var signCases = []signCase{
	{"standing r0, player south", "minecraft:oak_sign[rotation=0]", [3]int{8, -60, 0}, 0, 3, 3, 180, true},
	{"standing r0, player north", "minecraft:oak_sign[rotation=0]", [3]int{24, -60, 0}, 0, -3, 2, 0, false},
	{"standing r4, player west", "minecraft:oak_sign[rotation=4]", [3]int{8, -60, -8}, -3, 0, 4, 270, false},
	{"standing r4, player south", "minecraft:oak_sign[rotation=4]", [3]int{24, -60, -8}, 1, 3, 3, 180, false},
	{"wall north, player north", "minecraft:oak_wall_sign[facing=north]", [3]int{8, -60, -16}, 0, -3, 2, 0, false},
	{"wall north, player south", "minecraft:oak_wall_sign[facing=north]", [3]int{24, -60, -16}, 0, 3, 3, 180, false},
	{"hanging, player south", "minecraft:oak_hanging_sign[rotation=0]", [3]int{8, -60, -24}, 0, 3, 3, 180, false},
	{"bare oak_sign, player south", "minecraft:oak_sign", [3]int{24, -60, -24}, 0, 3, 3, 180, false},
}

// signItemScript is what the acting player holds, in order: the dye, the
// glow ink sac, an ink sac that takes the glow away, a second one that
// has nothing to take away (it opens the editor instead), another dye
// that paints over the first, and a honeycomb that waxes the sign. The
// last dye then has to be refused.
var signItemScript = []string{"red_dye", "glow_ink_sac", "ink_sac", "ink_sac", "lime_dye", "honeycomb", "red_dye"}

// signEvent is one thing a server said while the script ran.
type signEvent struct {
	kind string // "editor" or "be"
	text string
}

// signSink reads a server's packets: it answers what a real client
// answers (keep-alive, the teleport confirmation every /tp needs before
// the server will listen to an interaction again, and the chunk batch
// acknowledgement the real server throttles its chunk streaming on) and
// records the sign packets.
//
// That last one matters to the comparison: the real server sends a
// player only the chunks it has acknowledged, and a block entity update
// reaches a player only for a chunk that player has. A silent client is
// sent the nine chunks around its own column and then nothing, so a sign
// placed sixteen blocks away is announced to nobody - measured, and the
// reason this sink answers the batch flow.
type signSink struct {
	mu     sync.Mutex
	events []signEvent
	syncs  int
	err    error
}

func newSignSink(pr *protocol.Reader, pw *protocol.Writer) *signSink {
	s := &signSink{}
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
			switch p.ID {
			case server.IDPlayBlockEntityData:
				r := protocol.NewPacketReader(p.Data)
				x, y, z, err := r.Position()
				if err != nil {
					continue
				}
				typ, err := r.VarInt()
				if err != nil {
					continue
				}
				rest, err := r.Read(r.Remaining())
				if err != nil {
					continue
				}
				body := "undecodable(" + fmt.Sprintf("%x", rest) + ")"
				if v, err := nbt.ReadUnnamed(bytes.NewReader(rest)); err == nil {
					body = renderNBT(v, true)
				}
				s.record("be", fmt.Sprintf("be(%d,%d,%d) type=%d %s", x, y, z, typ, body))
			case server.IDPlayOpenSignEditor:
				r := protocol.NewPacketReader(p.Data)
				x, y, z, err := r.Position()
				if err != nil {
					continue
				}
				front, err := r.Bool()
				if err != nil {
					continue
				}
				side := "back"
				if front {
					side = "front"
				}
				s.record("editor", fmt.Sprintf("editor(%d,%d,%d) side=%s", x, y, z, side))
			}
			wmu.Lock()
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				// The real client asks for a rate, not a count: twenty-five
				// chunks a tick is what the other scripted clients in this
				// suite ask for, and it streams a whole view in a moment.
				var b protocol.Buffer
				b.Float(25)
				_ = pw.WritePacket(server.IDPlayChunkBatchRecvSB, b.Bytes())
			case server.IDPlaySyncPos:
				s.mu.Lock()
				s.syncs++
				s.mu.Unlock()
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

func (s *signSink) record(kind, text string) {
	s.mu.Lock()
	s.events = append(s.events, signEvent{kind, text})
	s.mu.Unlock()
}

// syncCount is how many teleports the server has asked this client to
// accept so far.
func (s *signSink) syncCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncs
}

// waitSync blocks until the server has sent another teleport to accept,
// which is what the real server waits for before it will listen to an
// interaction from a teleported player.
func (s *signSink) waitSync(after int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if s.syncCount() > after {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// mark is a point in the packet stream: everything from here on belongs
// to the step about to run.
func (s *signSink) mark() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events)
}

// since renders the events recorded after mark, one per line, in order.
func (s *signSink) since(mark int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mark > len(s.events) {
		return nil
	}
	var out []string
	for _, e := range s.events[mark:] {
		out = append(out, e.text)
	}
	return out
}

// all returns every packet recorded so far.
func (s *signSink) all() []string {
	return s.since(0)
}

// signHold puts an item in the player's first hotbar slot in creative
// mode and selects it; an empty name clears the hand.
func signHold(t *testing.T, pw *protocol.Writer, item string) {
	t.Helper()
	var b protocol.Buffer
	b.Short(36) // menu slot 36 is hotbar slot 0
	count := int32(1)
	if item == "" {
		count = 0
	}
	b.VarInt(count)
	if count > 0 {
		id, ok := world.ItemIDs[item]
		if !ok {
			t.Fatalf("no item id for %q", item)
		}
		b.VarInt(id)
		b.VarInt(0) // no components added
		b.VarInt(0) // none removed
	}
	if err := pw.WritePacket(server.IDPlayCreativeSlotSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
	var h protocol.Buffer
	h.Short(0)
	if err := pw.WritePacket(server.IDPlayHeldChangeSB, h.Bytes()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(120 * time.Millisecond)
}

// signUse clicks a sign with whatever is in hand.
func signUse(t *testing.T, pw *protocol.Writer, pos [3]int, face int32) {
	t.Helper()
	var use protocol.Buffer
	use.VarInt(0)
	use.Position(int32(pos[0]), int32(pos[1]), int32(pos[2]))
	use.VarInt(face)
	use.Float(0.5)
	use.Float(1.0)
	use.Float(0.5)
	use.Bool(false)
	use.Bool(false)
	use.VarInt(1)
	if err := pw.WritePacket(server.IDPlayUseItemOnSB, use.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// signUpdate writes one side of a sign, as the client does after the
// editor opens.
func signUpdate(t *testing.T, pw *protocol.Writer, pos [3]int, front bool, lines [4]string) {
	t.Helper()
	var up protocol.Buffer
	up.Position(int32(pos[0]), int32(pos[1]), int32(pos[2]))
	up.Bool(front)
	for _, l := range lines {
		up.String(l)
	}
	if err := pw.WritePacket(server.IDPlayUpdateSignSB, up.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// signEditorSide reads the side an editor opened on out of a step's
// events, if one did.
func signEditorSide(events []string) (string, bool) {
	for _, e := range events {
		if !strings.HasPrefix(e, "editor(") {
			continue
		}
		if i := strings.Index(e, "side="); i >= 0 {
			return e[i+len("side="):], true
		}
	}
	return "", false
}

// signSteps runs the script against one server and returns one line per
// step, which is what the two servers are compared on.
func signSteps(t *testing.T, tag, addr, name string) (steps []string, observer []string) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	sink := newSignSink(pr, pw)
	_ = c.SetDeadline(time.Time{})

	// A second player, who never touches the sign, records what everyone
	// tracking the chunk is told.
	oc, opr, opw := joinPlayBlackbox(t, addr, name+"Watch")
	defer oc.Close()
	watch := newSignSink(opr, opw)
	_ = oc.SetDeadline(time.Time{})
	time.Sleep(1200 * time.Millisecond)

	// One step is marked, acted, and read back: a use waits long enough
	// for the reply to arrive, and one that has nothing to send reports
	// nothing, which is itself compared.
	observe := func(mark int, label string) []string {
		time.Sleep(500 * time.Millisecond)
		events := sink.since(mark)
		if len(events) == 0 {
			events = []string{"none"}
		}
		for _, e := range events {
			steps = append(steps, label+": "+e)
		}
		return events
	}

	for _, tc := range signCases {
		// The bare and rotated signs stand in the air, where setblock
		// puts them without complaint; the wall sign hangs off nothing,
		// which the real server allows too.
		sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d %s", tc.pos[0], tc.pos[1], tc.pos[2], tc.block))
		mark := sink.mark()
		time.Sleep(300 * time.Millisecond)
		observe(mark, tc.name+": setblock")

		// The real server ignores everything a player sends until the
		// teleport it is waiting for has been accepted, so the use has to
		// wait for the client's own answer to go out.
		before := sink.syncCount()
		sendCommand(t, pw, fmt.Sprintf("tp %s %g %g %g %g %v", name,
			float64(tc.pos[0])+0.5+tc.dx, float64(tc.pos[1]), float64(tc.pos[2])+0.5+tc.dz, tc.yaw, 10.0))
		if !sink.waitSync(before, 5*time.Second) {
			t.Errorf("%s/%s: no teleport to accept after /tp", tag, tc.name)
		}
		time.Sleep(400 * time.Millisecond)

		// The empty hand opens the editor on the side the player is on.
		signHold(t, pw, "")
		mark = sink.mark()
		signUse(t, pw, tc.pos, tc.face)
		events := observe(mark, tc.name+": use empty")
		side, opened := signEditorSide(events)
		if !opened {
			t.Errorf("%s/%s: the empty hand did not open an editor: %v", tag, tc.name, events)
			continue
		}
		steps = append(steps, tc.name+": editor side="+side)
		near, far := side == "front", side != "front"

		// Writing the side the editor named is taken and broadcast; the
		// other side is not, and is answered with nothing at all.
		mark = sink.mark()
		signUpdate(t, pw, tc.pos, near, [4]string{"hi", "", "", ""})
		observe(mark, tc.name+": text near")
		mark = sink.mark()
		signUpdate(t, pw, tc.pos, far, [4]string{"ho", "", "", ""})
		observe(mark, tc.name+": text far")

		if !tc.items {
			continue
		}
		for _, item := range signItemScript {
			signHold(t, pw, item)
			mark = sink.mark()
			signUse(t, pw, tc.pos, tc.face)
			observe(mark, tc.name+": use "+item)
		}
		// A waxed sign refuses the empty hand as well as every item.
		signHold(t, pw, "")
		mark = sink.mark()
		signUse(t, pw, tc.pos, tc.face)
		observe(mark, tc.name+": use empty on waxed")
	}

	if sink.err != nil && !strings.Contains(sink.err.Error(), "use of closed") {
		t.Errorf("%s: client stream: %v", tag, sink.err)
	}
	observer = dedupe(watch.all())
	return steps, observer
}

// dedupe drops repeats of a packet that arrived twice, which a chunk
// being resent can cause, so the two servers are compared on the
// sequence of states rather than on packet timing.
func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if len(out) > 0 && out[len(out)-1] == s {
			continue
		}
		out = append(out, s)
	}
	return out
}

// TestSignInteractionParity runs the sign script against both servers.
func TestSignInteractionParity(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(signParityName)}, nil)
	defer tg.cleanup()

	vSteps, vWatch := signSteps(t, "vanilla", tg.vanilla, signParityName)
	mSteps, mWatch := signSteps(t, "mc-go", tg.mcgo, signParityName)

	// The script only means something if the signs really changed, and
	// in the order the measurements say: the first dye paints the side
	// the text is on, the glow ink sac lights it, the first ink sac
	// takes the glow away, the second has nothing to take and opens the
	// editor instead, the honeycomb waxes the sign, and the dye after it
	// is refused along with every other use. Each of these is required
	// of both servers, so two servers that both said nothing cannot pass
	// by agreeing.
	want := []struct{ step, contains string }{
		{"standing r0, player south: use red_dye: ", `"front_text": {"color": "red"`},
		{"standing r0, player south: use red_dye: ", `"messages": ["hi", "", "", ""]`},
		{"standing r0, player south: use glow_ink_sac: ", `"color": "red", "has_glowing_text": b(1)`},
		{"standing r0, player south: use ink_sac: ", `"color": "red", "has_glowing_text": b(0)`},
		{"standing r0, player south: use ink_sac: ", "editor(8,-60,0) side=front"},
		{"standing r0, player south: use lime_dye: ", `"front_text": {"color": "lime"`},
		{"standing r0, player south: use lime_dye: ", `"back_text": {"color": "black"`},
		{"standing r0, player south: use honeycomb: ", `"is_waxed": b(1)`},
		{"standing r0, player south: use red_dye: ", "none"},
		{"standing r0, player south: use empty on waxed: ", "none"},
		// A hanging sign is a sign with a block entity type of its own: the
		// text written on it comes back as type 8, not 7. This case does
		// not run the item script, so the update is what carries it.
		{"hanging, player south: text near: ", "be(8,-60,-24) type=8"},
		// Writing the side the editor did not name is refused, on both
		// a sign whose text is behind the player and one in front.
		{"standing r0, player north: text near: ", "be(24,-60,0) type=7"},
		{"standing r0, player north: text far: ", "none"},
		{"standing r4, player west: text near: ", "be(8,-60,-8) type=7"},
		{"standing r4, player west: text far: ", "none"},
	}
	for _, w := range want {
		for _, got := range []struct {
			tag   string
			steps []string
		}{{"vanilla", vSteps}, {"mc-go", mSteps}} {
			if !sawStep(got.steps, w.step, w.contains) {
				t.Errorf("%s never produced a step %q containing %q; steps:\n%s",
					got.tag, w.step, w.contains, strings.Join(got.steps, "\n"))
			}
		}
	}

	compareSteps(t, "acting client", vSteps, mSteps)
	compareSteps(t, "watching client", vWatch, mWatch)

	t.Logf("vanilla last steps:\n%s", tailLines(vSteps, 4))
	t.Logf("mc-go   last steps:\n%s", tailLines(mSteps, 4))
}

func sawPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// sawStep reports whether a step whose label starts with step contains
// the given fragment - a packet the script required of that step.
func sawStep(lines []string, step, contains string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, step) && strings.Contains(l, contains) {
			return true
		}
	}
	return false
}

func tailLines(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// compareSteps fails on the first line the two servers disagree about,
// with both sequences around it for context.
func compareSteps(t *testing.T, what string, vanilla, mcgo []string) {
	t.Helper()
	for i := 0; i < len(vanilla) && i < len(mcgo); i++ {
		if vanilla[i] == mcgo[i] {
			continue
		}
		lo := i - 3
		if lo < 0 {
			lo = 0
		}
		t.Fatalf("%s: step %d differs\nvanilla: %s\nmc-go:   %s\n--- vanilla around %d ---\n%s\n--- mc-go around %d ---\n%s",
			what, i, vanilla[i], mcgo[i], i,
			strings.Join(vanilla[lo:min(i+3, len(vanilla))], "\n"), i,
			strings.Join(mcgo[lo:min(i+3, len(mcgo))], "\n"))
	}
	if len(vanilla) != len(mcgo) {
		i := 0
		for i < len(vanilla) && i < len(mcgo) && vanilla[i] == mcgo[i] {
			i++
		}
		lo := i - 3
		if lo < 0 {
			lo = 0
		}
		t.Fatalf("%s: %d steps from vanilla, %d from mc-go; equal up to %d, then\nvanilla:\n%s\nmc-go:\n%s",
			what, len(vanilla), len(mcgo), i,
			strings.Join(vanilla[lo:min(i+4, len(vanilla))], "\n"),
			strings.Join(mcgo[lo:min(i+4, len(mcgo))], "\n"))
	}
}
