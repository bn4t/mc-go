package blackbox

// TestTickingStateParity: the /tick surface as the wire sees it —
// ClientboundTickingStatePacket on every setFrozen/setTickRate call
// (no-op calls included), ClientboundTickingStepPacket when a step is
// requested or stopped, the sprint's unfreeze/refreeze around the run,
// the interrupt/stop reply texts, and the sprint report broadcast.
//
// Session A drives the commands and its stream is where ticking-state
// and ticking-step packets are collected — both packets are broadcast
// to every play-stage client, so B's commands land on A's wire too.
// Session B exists to race a sprint and a step mid-run.

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// tickStateLine decodes the raw ticking_state payloads a chatSink keeps
// as "0x7f:<5 bytes>" — float32 tick rate + a frozen byte.
type tickStateLine struct {
	rate   float32
	frozen bool
}

func tickStateSeq(lines []string) []tickStateLine {
	var out []tickStateLine
	for _, l := range lines {
		if !strings.HasPrefix(l, fmt.Sprintf("%#x:", server.IDPlayTickingState)) {
			continue
		}
		raw := l[strings.IndexByte(l, ':')+1:]
		if len(raw) != 5 {
			continue
		}
		out = append(out, tickStateLine{
			rate:   math.Float32frombits(binary.BigEndian.Uint32([]byte(raw[:4]))),
			frozen: raw[4] != 0,
		})
	}
	return out
}

// tickStepSeq decodes the ticking_step payloads: a VarInt owed count.
func tickStepSeq(lines []string) []int64 {
	var out []int64
	for _, l := range lines {
		if !strings.HasPrefix(l, fmt.Sprintf("%#x:", server.IDPlayTickingStep)) {
			continue
		}
		raw := l[strings.IndexByte(l, ':')+1:]
		r := protocol.NewPacketReader([]byte(raw))
		if v, err := r.VarInt(); err == nil {
			out = append(out, int64(v))
		}
	}
	return out
}

// collectCmd sends a marker say on pw and returns every line captured
// on sink through it — the bound for "the server processed what was
// sent so far".
func collectCmd(t *testing.T, pw *protocol.Writer, sink *chatSink, tag string) []string {
	t.Helper()
	var out []string
	sendCommand(t, pw, "say "+tag)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		out = append(out, sink.take()...)
		if sawMarker(out, tag) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("marker %s never arrived (%s)", tag, sink.health())
	return out
}

// waitKey drains sink until a System Chat component carrying the
// translate key appears — command replies are translatable components
// on both servers, so the key is the wire-level thing to match, never
// the English the client happens to render. Fails the test on timeout.
func waitKey(t *testing.T, sink *chatSink, key string, within time.Duration) []string {
	t.Helper()
	var out []string
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		out = append(out, sink.take()...)
		if sawKey(out, key) {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	var keys []string
	for _, l := range out {
		if k, _, ok := componentArgs(l); ok {
			keys = append(keys, k)
		}
	}
	t.Fatalf("never saw %q; keys=%v lines=%v (%s)", key, keys, out, sink.health())
	return out
}

// waitForKey is waitKey that also accepts lines already drained: a
// vanilla sprint's report can beat the say marker it was queued behind,
// landing inside a collect the caller just did.
func waitForKey(t *testing.T, sink *chatSink, acc []string, key string, within time.Duration) []string {
	t.Helper()
	if sawKey(acc, key) {
		return nil
	}
	var keys []string
	for _, l := range acc {
		if k, _, ok := componentArgs(l); ok {
			keys = append(keys, k)
		}
	}
	t.Logf("waitForKey %q: acc keys=%v", key, keys)
	return waitKey(t, sink, key, within)
}

func sawKey(lines []string, key string) bool {
	for _, l := range lines {
		k, args, ok := componentArgs(l)
		if !ok {
			continue
		}
		if k == key {
			return true
		}
		// A broadcastToOps success reaches the other ops wrapped in the
		// admin line — the message's own key sits one level down.
		if k == "chat.type.admin" && len(args) >= 2 {
			if m, ok := args[1].(map[string]any); ok && m["translate"] == key {
				return true
			}
		}
	}
	return false
}

// keySeq maps captured System Chat lines to their top-level translate
// keys — the bare replies a command's caller sees.
func keySeq(lines []string) []string {
	var out []string
	for _, l := range lines {
		if k, _, ok := componentArgs(l); ok {
			out = append(out, k)
		}
	}
	return out
}

// adminSeq maps chat.type.admin lines to the wrapped message's
// translate key — the form a broadcastToOps success takes for every op
// but the caller.
func adminSeq(lines []string) []string {
	var out []string
	for _, l := range lines {
		k, args, ok := componentArgs(l)
		if !ok || k != "chat.type.admin" || len(args) < 2 {
			continue
		}
		if m, ok := args[1].(map[string]any); ok {
			if key, ok := m["translate"].(string); ok {
				out = append(out, key)
			}
		}
	}
	return out
}

func TestTickingStateParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("TickA", "TickB"),
	}, func(s *server.Server) {
		// The file only reaches vanilla's working dir; mc-go gets the
		// same operator set through the programmatic table.
		s.SetOpLevel("TickA", 4)
		s.SetOpLevel("TickB", 4)
	})
	defer targets.cleanup()

	type result struct {
		states []tickStateLine
		steps  []int64
		// keys* are the ordered translate keys System Chat carried on
		// each wire; adm* the inner keys of the admin echoes — the two
		// forms are wire-distinct, so they are tallied apart.
		keysA, keysB []string
		admA, admB   []string
		stepped      int64
	}

	run := func(addr string) result {
		label := "vanilla"
		if addr == targets.mcgo {
			label = "mc-go"
		}
		t.Logf("run against %s", label)
		ca, pra, pwa := joinPlayBlackbox(t, addr, "TickA")
		defer ca.Close()
		sa := newChatSinkTP(pra, pwa)
		cb, prb, pwb := joinPlayBlackbox(t, addr, "TickB")
		defer cb.Close()
		sb := newChatSinkTP(prb, pwb)
		// No pre-drain on A: the join burst's ticking_state/ticking_step
		// pair (vanilla's updateJoiningPlayer) is the first entries.
		time.Sleep(400 * time.Millisecond)

		var res result
		var allA, allB []string
		// drainA sweeps A's stream into the shared packet sequences and
		// the per-wire chat pool; drainB keeps B's the same way (its
		// packets are A's own, only the admin echoes differ).
		drainA := func(lines []string) {
			res.states = append(res.states, tickStateSeq(lines)...)
			res.steps = append(res.steps, tickStepSeq(lines)...)
			allA = append(allA, lines...)
		}
		drainB := func(lines []string) { allB = append(allB, lines...) }
		// phase runs one of A's commands and drains both wires of its
		// consequences — B's wire holds the admin echo of A's reply.
		phase := func(cmd, tag string) []string {
			if cmd != "" {
				sendCommand(t, pwa, cmd)
			}
			lines := collectCmd(t, pwa, sa, tag)
			drainA(lines)
			time.Sleep(100 * time.Millisecond)
			drainB(sb.take())
			return lines
		}
		// bphase is the same drain on B's stream (its broadcasts also
		// land on A, which a settle-take picks up).
		bphase := func(cmd, tag string) []string {
			if cmd != "" {
				sendCommand(t, pwb, cmd)
			}
			lines := collectCmd(t, pwb, sb, tag)
			drainB(lines)
			time.Sleep(200 * time.Millisecond)
			drainA(sa.take())
			return lines
		}

		drainA(collectCmd(t, pwa, sa, "TICK-BASE"))
		drainB(sb.take())

		phase("tick rate 8", "R1")
		phase("tick rate 8", "R2") // unconditional broadcast, same value
		phase("tick freeze", "F1")
		phase("tick freeze", "F2") // no-op call still emits
		lines := phase("tick sprint 30", "S1")
		// Vanilla's sprint runs on the server loop: commands interleave
		// with its ticks, so the finish's report — not the say marker —
		// is the boundary after which the game is provably re-frozen. It
		// can even beat the marker, so check the lines just drained too.
		drainA(waitForKey(t, sa, lines, "commands.tick.sprint.report", 30*time.Second))
		phase("tick step 3", "ST1")
		// Vanilla's steps drain at server-loop pace; let them finish so
		// the stop below is a clean "nothing in progress" on both.
		time.Sleep(500 * time.Millisecond)
		phase("tick step stop", "STS1")
		phase("tick sprint stop", "SPS1")
		phase("tick unfreeze", "UF1")
		// Frozen sprint: the request unfreezes ({8,false}), the finish
		// re-freezes ({8,true}) — and step works again after, which is
		// the proof the frozen flag was restored rather than left off.
		phase("tick freeze", "F3")
		lines = phase("tick sprint 20", "S2")
		drainA(waitForKey(t, sa, lines, "commands.tick.sprint.report", 30*time.Second))
		phase("tick step 2", "ST2")
		phase("tick unfreeze", "UF2")

		// A sprints long; B's sprint interrupts it — one report only,
		// for the new leg, and B gets "Interrupted" before "sprinting".
		sendCommand(t, pwa, "tick sprint 6000")
		drainA(waitKey(t, sa, "commands.tick.status.sprinting", 15*time.Second))
		sendCommand(t, pwb, "tick sprint 25")
		blines := collectCmd(t, pwb, sb, "B-INT")
		drainB(blines)
		drainB(waitForKey(t, sb, blines, "commands.tick.sprint.report", 60*time.Second))
		// The report is a broadcast — it lands on A's wire as the same
		// admin line, which also sweeps the interrupt's state packets.
		drainA(waitForKey(t, sa, nil, "commands.tick.sprint.report", 30*time.Second))

		// A sprints again; B's step fails mid-sprint (the game is not
		// frozen while it runs), and B's stop cuts the sprint short.
		sendCommand(t, pwa, "tick sprint 6000")
		drainA(waitKey(t, sa, "commands.tick.status.sprinting", 15*time.Second))
		bphase("tick step 5", "B-STEPFAIL")
		// /tick query mid-sprint: the status line says sprinting and
		// the rate line drops its target — three lines with the
		// percentile report trailing.
		bphase("tick query", "B-QUERY")
		sendCommand(t, pwb, "tick sprint stop")
		drainB(waitKey(t, sb, "commands.tick.sprint.stop.success", 30*time.Second))
		// The sprint-finish report broadcasts to everyone — wait for
		// it on A's wire, which also sweeps the restore broadcast.
		drainA(waitForKey(t, sa, nil, "commands.tick.sprint.report", 30*time.Second))
		drainB(sb.take())

		// Frozen step interrupted cross-session: A steps 4000 while
		// frozen, B's step stop drains the rest.
		phase("tick freeze", "F4")
		before := gameTimeOf(t, pwa, sa)
		sendCommand(t, pwa, "tick step 4000")
		drainA(waitKey(t, sa, "commands.tick.step.success", 15*time.Second))
		bphase("tick step stop", "B-STEPSTOP")
		res.stepped = gameTimeOf(t, pwa, sa) - before
		phase("tick step stop", "STS2")
		phase("tick unfreeze", "UF3")
		phase("tick query", "Q1")

		// Settle-take both wires so a straggler echo does not drop out
		// of the tail of one run's sequence.
		time.Sleep(300 * time.Millisecond)
		drainA(sa.take())
		drainB(sb.take())
		res.keysA, res.admA = keySeq(allA), adminSeq(allA)
		res.keysB, res.admB = keySeq(allB), adminSeq(allB)
		return res
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	if fmt.Sprint(want.states) != fmt.Sprint(got.states) {
		t.Errorf("ticking_state sequence differs\nvanilla: %v\nmc-go:   %v", want.states, got.states)
	}
	if fmt.Sprint(want.steps) != fmt.Sprint(got.steps) {
		t.Errorf("ticking_step sequence differs\nvanilla: %v\nmc-go:   %v", want.steps, got.steps)
	}
	cmpSeq := func(name string, w, g []string) {
		if fmt.Sprint(w) != fmt.Sprint(g) {
			t.Errorf("%s differ\nvanilla: %v\nmc-go:   %v", name, w, g)
		}
	}
	cmpSeq("A replies", want.keysA, got.keysA)
	cmpSeq("B replies", want.keysB, got.keysB)
	cmpSeq("A admin echoes", want.admA, got.admA)
	cmpSeq("B admin echoes", want.admB, got.admB)
	t.Logf("stepped ticks before stop: vanilla=%d mc-go=%d", want.stepped, got.stepped)
	if want.stepped >= 4000 || got.stepped >= 4000 {
		t.Errorf("step stop did not interrupt: vanilla stepped %d, mc-go %d", want.stepped, got.stepped)
	}
}
