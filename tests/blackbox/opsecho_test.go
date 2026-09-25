package blackbox

// TestOpsEchoParity: the broadcastToOps reply surface as the wire sees
// it — a success flagged sendSuccess(_, true) reaches every OTHER op
// wrapped as chat.type.admin [<displayName>: <message>] in gray italic,
// while the caller sees the bare component and non-ops see nothing.
// Queries (sendSuccess(_, false)) echo nowhere. Multi-line replies
// carry a per-line flag (save-all's "saving" stays private). The
// gamerule minecraft:send_command_feedback gates the whole fan-out.
//
// Session A is the op issuing commands; B is the op who should see the
// wrapped echoes; C is a non-op who must see neither bare replies nor
// echoes — the ops-list membership test, not the permission level, is
// what broadcastToAdmins sends to.

import (
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/server"
)

// adminArg0 pulls the sender display-name component off the
// chat.type.admin line wrapping the given message key — the form
// getDisplayName takes on the wire (team dress included).
func adminArg0(lines []string, key string) map[string]any {
	for _, l := range lines {
		k, args, ok := componentArgs(l)
		if !ok || k != "chat.type.admin" || len(args) < 2 {
			continue
		}
		m, ok := args[1].(map[string]any)
		if !ok || m["translate"] != key {
			continue
		}
		if a0, ok := args[0].(map[string]any); ok {
			return a0
		}
	}
	return nil
}

// settleAbsent drains sink after a short settle window and returns the
// lines — the negative side of an echo assertion: "nothing landed".
func settleAbsent(sink *chatSink, within time.Duration) []string {
	time.Sleep(within)
	return sink.take()
}

func TestOpsEchoParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("EchoA", "EchoB"),
	}, func(s *server.Server) {
		// ops.json only reaches vanilla's working dir; mc-go gets the
		// same operator set through the programmatic table.
		s.SetOpLevel("EchoA", 4)
		s.SetOpLevel("EchoB", 4)
		// Vanilla always has a world dir, so /save-all succeeds — mc-go
		// needs a save path of its own or it honestly reports failed.
		s.WorldPath = filepath.Join(t.TempDir(), "world.json")
	})
	defer targets.cleanup()

	run := func(addr, label string) {
		t.Run(label, func(t *testing.T) {
			connA, prA, pwA := joinPlayBlackbox(t, addr, "EchoA")
			defer connA.Close()
			connB, prB, pwB := joinPlayBlackbox(t, addr, "EchoB")
			defer connB.Close()
			connC, prC, pwC := joinPlayBlackbox(t, addr, "EchoC")
			defer connC.Close()
			sa := newChatSink(prA, pwA)
			sb := newChatSink(prB, pwB)
			sc := newChatSink(prC, pwC)

			var accC []string

			// Mutator: A's bare reply, B's wrapped echo, C nothing.
			sendCommand(t, pwA, "time set 1000")
			accA := collectCmd(t, pwA, sa, "M1")
			if !sawKey(accA, "commands.time.set.absolute") {
				t.Fatalf("caller never saw bare commands.time.set: %v", keySeq(accA))
			}
			accB := waitForKey(t, sb, nil, "commands.time.set.absolute", 10*time.Second)
			if got := adminSeq(accB); len(got) != 1 || got[0] != "commands.time.set.absolute" {
				t.Fatalf("other op echo seq = %v, want [commands.time.set]", got)
			}
			if a0 := adminArg0(accB, "commands.time.set.absolute"); a0 == nil {
				t.Fatalf("echo missing display-name arg: %v", accB)
			}
			accC = append(accC, settleAbsent(sc, 400*time.Millisecond)...)

			// Query: caller sees the value line, nobody else hears a thing.
			sendCommand(t, pwA, "time query day")
			accA = collectCmd(t, pwA, sa, "M2")
			if !sawKey(accA, "commands.time.query.timeline") {
				t.Fatalf("caller never saw commands.time.query.day: %v", keySeq(accA))
			}
			if lines := settleAbsent(sb, 400*time.Millisecond); len(adminSeq(lines)) != 0 {
				t.Fatalf("query echoed to ops: %v", adminSeq(lines))
			}
			accC = append(accC, settleAbsent(sc, 200*time.Millisecond)...)

			// save-all: two reply lines, only the success line echoes.
			sendCommand(t, pwA, "save-all")
			accA = collectCmd(t, pwA, sa, "M3")
			waitForKey(t, sa, accA, "commands.save.success", 10*time.Second)
			accB = waitForKey(t, sb, nil, "commands.save.success", 10*time.Second)
			for _, key := range adminSeq(accB) {
				if key != "commands.save.success" {
					t.Fatalf("save-all echoed %q — only the success line is broadcastToOps", key)
				}
			}

			// Team dress: once A joins a team, echoes name them in the
			// team's prefix/suffix — getDisplayName resolves at send time.
			sendCommand(t, pwA, "team add red")
			collectCmd(t, pwA, sa, "M4")
			waitForKey(t, sb, nil, "commands.team.add.success", 10*time.Second)
			sendCommand(t, pwA, "team join red")
			accA = collectCmd(t, pwA, sa, "M5")
			waitForKey(t, sa, accA, "commands.team.join.success.single", 10*time.Second)
			accB = waitForKey(t, sb, nil, "commands.team.join.success.single", 10*time.Second)
			if a0 := adminArg0(accB, "commands.team.join.success.single"); a0 == nil {
				t.Fatalf("join echo missing sender arg: %v", accB)
			}
			sendCommand(t, pwA, "time set 2500")
			collectCmd(t, pwA, sa, "M6")
			accB = waitForKey(t, sb, nil, "commands.time.set.absolute", 10*time.Second)
			a0 := adminArg0(accB, "commands.time.set.absolute")
			if a0 == nil {
				t.Fatalf("post-join echo missing sender arg: %v", accB)
			}
			if _, dressed := a0["extra"]; !dressed {
				t.Fatalf("post-join echo sender lacks team dress: %v", a0)
			}

			// send_command_feedback gates the caller's own reply too —
			// acceptsSuccess on a player source. Turning it off eats the
			// toggle's own message: the rule is already off when
			// sendSuccess runs, so the caller hears nothing either.
			sendCommand(t, pwA, "gamerule send_command_feedback false")
			accA = collectCmd(t, pwA, sa, "M7")
			if sawKey(accA, "commands.gamerule.set") {
				t.Fatalf("feedback-off toggle still reached the caller: %v", keySeq(accA))
			}
			sendCommand(t, pwA, "time set 3000")
			accA = collectCmd(t, pwA, sa, "M8")
			if sawKey(accA, "commands.time.set.absolute") {
				t.Fatalf("feedback-off mutator reply reached the caller")
			}
			if lines := settleAbsent(sb, 400*time.Millisecond); len(adminSeq(lines)) != 0 {
				t.Fatalf("echo leaked past send_command_feedback=false: %v", adminSeq(lines))
			}

			// Back on, the gate opens again for both ends.
			sendCommand(t, pwA, "gamerule send_command_feedback true")
			accA = collectCmd(t, pwA, sa, "M9")
			if !sawKey(accA, "commands.gamerule.set") {
				t.Fatalf("feedback-on toggle reply never reached the caller: %v", keySeq(accA))
			}
			_ = waitForKey(t, sb, nil, "commands.gamerule.set", 10*time.Second)

			// Non-op C's whole stream: announcements only, never an
			// admin line.
			accC = append(accC, sc.take()...)
			if sawKey(accC, "chat.type.admin") {
				t.Fatalf("non-op saw an ops echo")
			}
		})
	}
	run(targets.vanilla, "vanilla")
	run(targets.mcgo, "mc-go")
}
