package blackbox

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Fluid behavior is captured from the real server rather than guessed.
//
// The measurement deliberately does NOT read the Block Update stream:
// a scripted client's view of that stream turned out to be an
// unreliable oracle (updates are scoped to what the server thinks the
// player tracks), and a partial stream looks exactly like a partial
// spread. Instead the probe asks the server what is actually there, one
// position at a time:
//
//	/execute if block <x> <y> <z> minecraft:water[level=N] run say <marker>
//
// A marker in the chat reply means the block matched. That answer comes
// from the world itself, not from a broadcast.
//
//	MCGO_BLACKBOX=1 MCGO_CAPTURE=1 go test ./tests/blackbox -run TestCaptureVanillaFluid -v

const fluidProbeName = "FluidProbe"

// capture gates the recording tests: normal runs never write a
// reference file, and never depend on one being fresh.
func capture(t *testing.T) bool {
	t.Helper()
	if os.Getenv("MCGO_CAPTURE") != "1" {
		t.Skip("set MCGO_CAPTURE=1 to record vanilla behavior")
		return false
	}
	return true
}

// chatSink drains a connection in its own goroutine and records System
// Chat payloads. Reading in a goroutine (rather than with per-packet
// read deadlines on the test goroutine) matters: a deadline that fires
// inside a frame desynchronizes the stream, and a desynchronized stream
// yields plausible-looking garbage rather than an error.
type chatSink struct {
	mu   sync.Mutex
	chat []string
	// cap drops packets larger than this so chunk and light spam does
	// not drown the interesting ones; tests expecting oversized replies
	// (a full entity NBT read) raise it.
	cap int
	// ids counts traffic per packet id, and where records which packet
	// ids carried a marker, so a reply that is not a System Chat is
	// found rather than assumed.
	ids   map[int32]int
	total int
	err   error
}

// newChatSink drains the connection and keeps the session alive: a
// scripted client that never answers Keep Alive is disconnected by
// vanilla after a few seconds, which shows up as a broken pipe on the
// next write and silently truncates whatever was being measured.
func newChatSink(pr *protocol.Reader, pw *protocol.Writer) *chatSink {
	return newChatSinkOpts(pr, pw, false)
}

// newChatSinkTP is newChatSink plus real-client teleport handling: it
// confirms the teleport and echoes the position back. Without that a
// vanilla server never moves the entity — the position stays at spawn
// for chunk ticking — and silently drops block actions.
func newChatSinkTP(pr *protocol.Reader, pw *protocol.Writer) *chatSink {
	return newChatSinkOpts(pr, pw, true)
}

func newChatSinkOpts(pr *protocol.Reader, pw *protocol.Writer, confirmTP bool) *chatSink {
	s := &chatSink{ids: map[int32]int{}, cap: 4096}
	var writeMu sync.Mutex
	go func() {
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				s.mu.Lock()
				s.err = err
				s.mu.Unlock()
				return
			}
			s.mu.Lock()
			s.total++
			s.ids[p.ID]++
			// Keep anything small enough to be a message; chunk and
			// light payloads dominate the stream and never carry text.
			if len(p.Data) < s.cap {
				s.chat = append(s.chat, fmt.Sprintf("%#x:%s", p.ID, string(p.Data)))
			}
			s.mu.Unlock()

			if p.ID == server.IDPlayKeepAliveCB {
				writeMu.Lock()
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				writeMu.Unlock()
			}
			// A real client answers every chunk batch; without the ack
			// the streaming throttle holds all later packets — command
			// replies included — behind the batch forever.
			if p.ID == server.IDPlayChunkBatchDone {
				writeMu.Lock()
				_ = ackChunkBatch(pw)
				writeMu.Unlock()
			}
			if confirmTP && p.ID == server.IDPlaySyncPos {
				r := protocol.NewPacketReader(p.Data)
				if id, err := r.VarInt(); err == nil {
					var b protocol.Buffer
					b.VarInt(id)
					writeMu.Lock()
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					if x, err := r.Double(); err == nil {
						var mb protocol.Buffer
						mb.Double(x)
						if y, err := r.Double(); err == nil {
							mb.Double(y)
						}
						if z, err := r.Double(); err == nil {
							mb.Double(z)
						}
						mb.Byte(0)
						_ = pw.WritePacket(server.IDPlayMovePosSB, mb.Bytes())
					}
					writeMu.Unlock()
				}
			}
		}
	}()
	return s
}

// take returns and clears the chat seen so far.
func (s *chatSink) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.chat
	s.chat = nil
	return out
}

func (s *chatSink) health() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, n := range s.ids {
		ids = append(ids, fmt.Sprintf("%#x:%d", id, n))
	}
	sort.Strings(ids)
	return fmt.Sprintf("packets=%d err=%v ids=[%s]", s.total, s.err, strings.Join(ids, " "))
}

// sawMarker reports whether any captured chat payload contains marker.
func sawMarker(lines []string, marker string) bool {
	for _, l := range lines {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

// sendCmd writes a command. sendCommand emits the bare unsigned form
// both servers accept; the chat-message helper's trailing fields would
// desynchronize vanilla.
func sendCmd(t *testing.T, pw *protocol.Writer, cmd string) {
	t.Helper()
	sendCommand(t, pw, cmd)
	time.Sleep(60 * time.Millisecond)
}

// waitDrained blocks until the server has worked through everything
// already sent, by round-tripping a marker command. A big /tick step can
// take seconds to simulate; without this the next query reads a world
// that has not been stepped yet, which looks exactly like "nothing
// happened".
func waitDrained(t *testing.T, pw *protocol.Writer, sink *chatSink) {
	t.Helper()
	for i := 0; i < 120; i++ {
		tag := fmt.Sprintf("DRAIN%d", i)
		sink.take()
		sendCommand(t, pw, "say "+tag)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			if sawMarker(sink.take(), tag) {
				return
			}
		}
	}
	t.Fatal("server never drained its command queue")
}

// componentArgs decodes a System Chat payload captured by the sink and
// returns its translation key with the arguments, the form the real
// server words command replies in.
func componentArgs(line string) (string, []any, bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", nil, false
	}
	root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
	if err != nil {
		return "", nil, false
	}
	m, ok := root.(map[string]any)
	if !ok {
		return "", nil, false
	}
	key, found := m["translate"].(string)
	if !found {
		// Error replies carry the message one level down, in extra, and
		// the arguments with it.
		if l, ok := m["extra"].(nbt.List); ok && len(l.Items) > 0 {
			sub, ok := l.Items[0].(map[string]any)
			if !ok {
				return "", nil, false
			}
			m = sub
			key, found = m["translate"].(string)
		}
	}
	if !found {
		return "", nil, false
	}
	var args []any
	if l, ok := m["with"].(nbt.List); ok {
		args = l.Items
	}
	return key, args, true
}

// componentTree renders a captured System Chat payload as its whole NBT
// tree, canonicalized: every field with its tag type, compounds sorted
// by name. A command reply carries more than a translation key - an
// error is styled and empty-texted with the message in extra, and it is
// followed by a context message whose click event, marked token and
// cursor marker are all part of the reply - so a comparison that reads
// only the key cannot see a wrong cursor or a missing style flag.
//
// Only System Chat payloads are decoded: the sink keeps every packet
// small enough to be a message, and a Set Time packet's eight bytes
// should not be read as a component.
func componentTree(line string, withValues bool) (string, bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", false
	}
	id, err := strconv.ParseInt(line[:i], 0, 32)
	if err != nil || int32(id) != server.IDPlaySystemChatCB {
		return "", false
	}
	root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
	if err != nil {
		return "", false
	}
	if _, ok := root.(map[string]any); !ok {
		return "", false
	}
	return renderNBT(root, withValues), true
}

// renderNBT renders one NBT value with its tag type. Bytes keep their
// value even when numbers are masked, because the only bytes a command
// reply carries are style flags: "italic: 1" is a different reply from
// "italic: 0", while a game time is a moving reading.
func renderNBT(v any, withValues bool) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case int8:
		return fmt.Sprintf("b(%d)", x)
	case int16:
		return nbtNum("s", float64(x), withValues)
	case int32:
		return nbtNum("i", float64(x), withValues)
	case int64:
		return nbtNum("l", float64(x), withValues)
	case float32:
		return nbtNum("f", float64(x), withValues)
	case float64:
		return nbtNum("d", x, withValues)
	case nbt.List:
		parts := make([]string, 0, len(x.Items))
		for _, it := range x.Items {
			parts = append(parts, renderNBT(it, withValues))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []byte:
		return fmt.Sprintf("ba(%d)", len(x))
	case []int32:
		return fmt.Sprintf("ia(%d)", len(x))
	case []int64:
		return fmt.Sprintf("la(%d)", len(x))
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %s", k, renderNBT(x[k], withValues))
		}
		b.WriteString("}")
		return b.String()
	case nil:
		return "nil"
	}
	return fmt.Sprintf("%T", v)
}

// nbtNum renders a numeric NBT value with its tag letter.
func nbtNum(tag string, v float64, withValues bool) string {
	if !withValues {
		return tag
	}
	return fmt.Sprintf("%s(%v)", tag, v)
}

// gameTimeOf asks a server for its game clock. The clock stands still
// while the game is frozen and moves exactly one tick per tick
// simulated, which makes it the one honest completion signal for a
// stepped or sprinted run: /tick step and /tick sprint both answer
// before the ticks have been simulated.
func gameTimeOf(t *testing.T, pw *protocol.Writer, sink *chatSink) int64 {
	t.Helper()
	sink.take()
	sendCommand(t, pw, "time query gametime")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range sink.take() {
			key, args, ok := componentArgs(line)
			if !ok || key != "commands.time.query.gametime" || len(args) == 0 {
				continue
			}
			if v, ok := args[0].(int64); ok {
				return v
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	var tail []string
	for _, l := range sink.take() {
		if len(l) > 160 {
			l = l[:160]
		}
		tail = append(tail, l)
	}
	t.Fatalf("no game time reply from the server (%s) chat=%q", sink.health(), tail)
	return 0
}

// sprintTicks advances a server by exactly n ticks and returns once
// they have all been simulated. It sprint rather than steps because the
// real server runs a step at 20 ticks per second no matter how fast the
// machine is, while a sprint runs flat out - and a sprint report comes
// back only after the ticks are done.
func sprintTicks(t *testing.T, pw *protocol.Writer, sink *chatSink, n int) {
	t.Helper()
	before := gameTimeOf(t, pw, sink)
	sendCommand(t, pw, fmt.Sprintf("tick sprint %d", n))
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if gameTimeOf(t, pw, sink) >= before+int64(n) {
			return
		}
	}
	t.Fatalf("the server never simulated %d ticks", n)
}

// blockQuery asks the server what block is at a position by testing the
// fluid's levels. It returns the level 0..8 that matched, or -1 for "not
// this fluid", or -2 when nothing matched at all (air or another block).
func blockQuery(t *testing.T, pw *protocol.Writer, sink *chatSink, fluid string, x, y, z int) int {
	t.Helper()
	sink.take()
	tag := fmt.Sprintf("Q%d_%d_%d_", x, y, z)
	for lvl := 0; lvl <= 8; lvl++ {
		sendCommand(t, pw, fmt.Sprintf(
			"execute if block %d %d %d minecraft:%s[level=%d] run say %sL%d",
			x, y, z, fluid, lvl, tag, lvl))
	}
	time.Sleep(700 * time.Millisecond)
	lines := sink.take()
	// The fluid block exists but at a level outside 0..8 only if the
	// world holds a state the game never places; report it as -2.
	any := false
	for lvl := 0; lvl <= 8; lvl++ {
		if sawMarker(lines, fmt.Sprintf("%sL%d", tag, lvl)) {
			any = true
			// Keep the highest matching level: a marker string is a
			// prefix of longer ones only for the same tag, so exact
			// matching is by construction here.
			if !sawMarker(lines, fmt.Sprintf("%sL%d", tag, lvl)) {
				continue
			}
			best := lvl
			for l2 := lvl + 1; l2 <= 8; l2++ {
				if sawMarker(lines, fmt.Sprintf("%sL%d", tag, l2)) {
					best = l2
				}
			}
			return best
		}
	}
	if any {
		return -2
	}
	// Distinguish "air" from "some other block" for the report.
	sink.take()
	sendCommand(t, pw, fmt.Sprintf(
		"execute if block %d %d %d minecraft:air run say %sAIR", x, y, z, tag))
	time.Sleep(200 * time.Millisecond)
	if sawMarker(sink.take(), tag+"AIR") {
		return -2
	}
	return -3
}

// fluidGrid probes a horizontal slab and returns absolute position ->
// level, for the positions that hold the fluid.
func fluidGrid(t *testing.T, pw *protocol.Writer, sink *chatSink, fluid string, y, x0, x1, z0, z1 int) map[[3]int]int {
	t.Helper()
	out := map[[3]int]int{}
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			if lvl := blockQuery(t, pw, sink, fluid, x, y, z); lvl >= 0 {
				out[[3]int{x, y, z}] = lvl
			}
		}
	}
	return out
}

// fluidColumn probes a vertical line.
func fluidColumn(t *testing.T, pw *protocol.Writer, sink *chatSink, fluid string, x, z, y0, y1 int) map[[3]int]int {
	t.Helper()
	out := map[[3]int]int{}
	for y := y0; y <= y1; y++ {
		if lvl := blockQuery(t, pw, sink, fluid, x, y, z); lvl >= 0 {
			out[[3]int{x, y, z}] = lvl
		}
	}
	return out
}

// renderGrid draws a slab as a top-down map, so a symmetry problem is
// visible at a glance rather than inferred from a list.
func renderGrid(m map[[3]int]int, y, x0, x1, z0, z1 int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "y=%d, x %d..%d (rows), z %d..%d (cols); '.' = no fluid\n", y, x0, x1, z0, z1)
	b.WriteString("      ")
	for z := z0; z <= z1; z++ {
		fmt.Fprintf(&b, "%3d", z)
	}
	b.WriteString("\n")
	for x := x0; x <= x1; x++ {
		fmt.Fprintf(&b, "%4d  ", x)
		for z := z0; z <= z1; z++ {
			lvl, ok := m[[3]int{x, y, z}]
			switch {
			case !ok:
				b.WriteString("  .")
			case lvl == 0:
				b.WriteString("  S") // source
			case lvl == 8:
				b.WriteString("  F") // falling
			default:
				fmt.Fprintf(&b, "%3d", lvl)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestCaptureVanillaFluid records how vanilla spreads water and lava.
func TestCaptureVanillaFluid(t *testing.T) {
	if !capture(t) {
		return
	}
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(fluidProbeName),
	}, nil)
	defer tg.cleanup()

	c, pr, pw := joinPlayBlackbox(t, tg.vanilla, fluidProbeName)
	defer c.Close()
	// From here on the sink goroutine is the only reader.
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(4 * time.Second)
	sink.take()

	sendCmd(t, pw, "tick freeze")
	// A known floor with clear air above it.
	sendCmd(t, pw, "fill -24 -61 -24 24 -61 24 minecraft:stone")
	sendCmd(t, pw, "fill -24 -60 -24 24 -58 24 minecraft:air")
	time.Sleep(500 * time.Millisecond)

	// Sanity: place one distinctive block and read it back, so a broken
	// query shows up before any fluid data is trusted.
	sendCmd(t, pw, "setblock 5 -60 7 minecraft:gold_block")
	time.Sleep(300 * time.Millisecond)
	sink.take()
	sendCommand(t, pw, "execute if block 5 -60 7 minecraft:gold_block run say SANITYOK")
	sendCommand(t, pw, "execute if block 5 -60 7 minecraft:water run say SANITYBAD")
	time.Sleep(400 * time.Millisecond)
	lines := sink.take()
	if !sawMarker(lines, "SANITYOK") || sawMarker(lines, "SANITYBAD") {
		t.Fatalf("query path is broken: gold=%v water=%v\n%s\ncaptured=%v",
			sawMarker(lines, "SANITYOK"), sawMarker(lines, "SANITYBAD"), sink.health(), lines)
	}
	t.Log("sanity: the /execute-if-block query path reads the world correctly")
	sendCmd(t, pw, "setblock 5 -60 7 minecraft:air")

	// Scenario A: a source resting on flat ground.
	sendCmd(t, pw, "setblock 0 -60 0 minecraft:water[level=0]")
	sendCmd(t, pw, "tick step 120")
	waitDrained(t, pw, sink)
	gridA := fluidGrid(t, pw, sink, "water", -60, -3, 3, -3, 3)
	t.Logf("\n=== A: water source on flat ground, after 120 ticks ===\n%s", renderGrid(gridA, -60, -3, 3, -3, 3))
	t.Logf("  A health: %s", sink.health())

	// A far probe along each axis, to find the true reach.
	axis := fluidGrid(t, pw, sink, "water", -60, -9, 9, 0, 0)
	t.Logf("\n=== A: reach along the x axis at z=0 ===\n%s", renderGrid(axis, -60, -9, 9, 0, 0))

	// Scenario B: a source in the air, so the water falls first.
	sendCmd(t, pw, "fill -24 -60 -24 24 -58 24 minecraft:air")
	time.Sleep(400 * time.Millisecond)
	sendCmd(t, pw, "setblock 0 -55 0 minecraft:water[level=0]")
	sendCmd(t, pw, "tick step 120")
	waitDrained(t, pw, sink)
	colB := fluidColumn(t, pw, sink, "water", 0, 0, -60, -50)
	t.Logf("\n=== B: falling column at x=0,z=0 (y from -60 up to -50) ===")
	keys := make([][3]int, 0, len(colB))
	for k := range colB {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][1] > keys[j][1] })
	for _, k := range keys {
		label := fmt.Sprintf("level=%d", colB[k])
		if colB[k] == 8 {
			label = "falling"
		}
		t.Logf("  y=%d  %s", k[1], label)
	}
	gridB := fluidGrid(t, pw, sink, "water", -60, -3, 3, -3, 3)
	t.Logf("\n=== B: where the falling water landed ===\n%s", renderGrid(gridB, -60, -3, 3, -3, 3))
	// The full column shape, one layer at a time: this is what decides
	// whether a falling fluid spreads sideways, and whether a flowing
	// fluid that can still fall does.
	for y := -55; y >= -59; y-- {
		layer := fluidGrid(t, pw, sink, "water", y, -3, 3, -3, 3)
		t.Logf("\n=== B: layer y=%d ===\n%s", y, renderGrid(layer, y, -3, 3, -3, 3))
	}
	// How far the landing pool reaches along an axis.
	reach := fluidGrid(t, pw, sink, "water", -60, -10, 10, 0, 0)
	t.Logf("\n=== B: reach along the x axis at z=0, y=-60 ===\n%s", renderGrid(reach, -60, -10, 10, 0, 0))

	// Scenario C: lava's reach.
	sendCmd(t, pw, "fill -24 -60 -24 24 -58 24 minecraft:air")
	time.Sleep(600 * time.Millisecond)
	sendCmd(t, pw, "setblock 0 -60 0 minecraft:lava[level=0]")
	time.Sleep(400 * time.Millisecond)
	// Confirm the placement landed before stepping, so an empty grid is
	// read as "lava did not spread" rather than "lava was never placed".
	sink.take()
	sendCommand(t, pw, "execute if block 0 -60 0 minecraft:lava run say LAVAPLACED")
	time.Sleep(300 * time.Millisecond)
	t.Logf("  C: lava present right after setblock = %v", sawMarker(sink.take(), "LAVAPLACED"))
	sendCmd(t, pw, "tick step 400")
	waitDrained(t, pw, sink)
	gridC := fluidGrid(t, pw, sink, "lava", -60, -6, 6, 0, 0)
	t.Logf("\n=== C: lava reach along the x axis at z=0 ===\n%s", renderGrid(gridC, -60, -6, 6, 0, 0))

	fmt.Println("fluid capture complete")
}
