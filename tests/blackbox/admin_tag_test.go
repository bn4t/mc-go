package blackbox

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestAdminTagParity pins /tag and the operator commands /op /deop
// /kick /ban /pardon /ban-ip /pardon-ip against the real server: reply
// components and argument-error cursors, plus the disconnect packet a
// kicked or banned client receives.
func TestAdminTagParity(t *testing.T) { testAdminTagParity(t, false, false) }

// Keep the original direct-kick script intact and separately pin the measured
// executor-changing result/success-store command against both servers.
func TestKickExecuteStoreParity(t *testing.T) { testAdminTagParity(t, true, false) }

func TestKickOperatorTargetParity(t *testing.T) { testAdminTagParity(t, true, true) }

func testAdminTagParity(t *testing.T, storeVariant, operatorTarget bool) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	cmds := []string{
		`tag P add ztag1`,
		`tag P add ztag2`,
		`tag P list`,
		`tag P remove ztag1`,
		`tag P list`,
		`tag P remove ztag1`,
		`tag P add ztag1`,
		`tag P add ztag1`,
		`tag P add`,
		`tag nosuchplayer add x`,
		`tag @e[type=minecraft:pig] add x`,
		`tag`,
		`tag P`,
		`tag P remove`,
		`tag P bogus x`,
		`op Q`,
		`op Q`,
		`deop Q`,
		`deop Q`,
		`ban nosuchplayer2`,
		`ban nosuchplayer2`,
		`pardon nosuchplayer2`,
		`pardon nosuchplayer2`,
		`ban-ip 1.2.3.4`,
		`ban-ip nosuchplayer`,
		`ban-ip 1.2.3.4 again`,
		`pardon-ip 1.2.3.4`,
		`pardon-ip 9.9.9.9`,
		`pardon-ip`,
		`ban`,
		`kick`,
		`op`,
		`deop`,
		`pardon`,
		`kick Q bye reason`,
		`ban Q`,
	}

	if storeVariant {
		cmds = []string{
			`scoreboard objectives add kickStore dummy`,
			`execute as Q store result score kickresult kickStore store success score kicksuccess kickStore run kick Q bye reason`,
			`scoreboard players get kickresult kickStore`,
			`scoreboard players get kicksuccess kickStore`,
		}
	}

	if operatorTarget {
		cmds = []string{`kick Q bye reason`}
	}
	run := func(addr string) [][]string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		c2, pr2, pw2 := joinPlayBlackbox(t, addr, "Q")
		defer c2.Close()
		_ = c2.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		_ = pw2.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		sink2 := newChatSink(pr2, pw2)
		sink2.cap = 1 << 20
		sendCommand(t, pw, `tellraw P "AdminTagReady"`)
		_ = waitForMarker(t, sink, "AdminTagReady", 15*time.Second)
		if operatorTarget {
			sendCommand(t, pw, "op Q")
			sendCommand(t, pw, `tellraw Q "AdminTagOperatorTargetReady"`)
			_ = waitForMarker(t, sink2, "AdminTagOperatorTargetReady", 15*time.Second)
			sendCommand(t, pw, `tellraw P "AdminTagOperatorIssuerReady"`)
			setup := waitForMarker(t, sink, "AdminTagOperatorIssuerReady", 15*time.Second)
			if !strings.Contains(strings.Join(setup, ""), "commands.op.success") {
				t.Fatalf("Q operator admission missing: %v", setup)
			}
		}
		var out [][]string
		for i, cmd := range cmds {
			sink.take()
			sink2.take()
			sendCommand(t, pw, cmd)
			marker := fmt.Sprintf("AdminTagDone%d", i)
			sendCommand(t, pw, `tellraw P "`+marker+`"`)
			lines := waitForMarker(t, sink, marker, 15*time.Second)
			if strings.HasPrefix(cmd, "kick Q ") || strings.Contains(cmd, "run kick Q ") {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					sink2.mu.Lock()
					closed := sink2.err != nil
					sink2.mu.Unlock()
					if closed {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				sink2.mu.Lock()
				closeErr := sink2.err
				closed := closeErr != nil
				sink2.mu.Unlock()
				if closed && !errors.Is(closeErr, io.EOF) {
					t.Fatalf("Q closure: %v", closeErr)
				}
				if !closed {
					t.Fatalf("%s Q connection did not close: %s", cmd, sink2.health())
				}
				// Socket closure and the P leave broadcast are independent
				// receipts. Neither a command marker nor Q's EOF proves the
				// broadcast has reached P; retain its actual position.
				leaveDeadline := time.Now().Add(10 * time.Second)
				sawLeave := func() bool {
					for _, line := range lines {
						if strings.Contains(line, "multiplayer.player.left") {
							return true
						}
					}
					return false
				}
				for !sawLeave() && time.Now().Before(leaveDeadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				if !sawLeave() {
					t.Fatalf("%s missing P leave broadcast: %s", cmd, sink.health())
				}
				closure := fmt.Sprintf("AdminTagClosed%d", i)
				sendCommand(t, pw, `tellraw P "`+closure+`"`)
				lines = append(lines, waitForMarker(t, sink, closure, 15*time.Second)...)
			}
			var evs []string
			for _, line := range lines {
				if strings.Contains(line, marker) || strings.Contains(line, "AdminTagClosed") {
					continue
				}
				if tree, ok := componentTree(line, true); ok {
					evs = append(evs, "chat "+maskUUID(tree))
				}
			}
			// The explicit operator-target control also retains all Q chat
			// through EOF, so a post-disconnect admin echo cannot be hidden.
			// Other controls retain the original disconnect-only Q contract.
			for _, line := range sink2.take() {
				if operatorTarget {
					if tree, ok := componentTree(line, true); ok {
						evs = append(evs, "Qchat "+maskUUID(tree))
					}
				}
				i := strings.Index(line, ":")
				if i < 0 {
					continue
				}
				id, err := strconv.ParseInt(line[:i], 0, 32)
				if err != nil || id != 0x20 {
					continue
				}
				evs = append(evs, "Q:"+maskUUID(line))
			}
			if strings.HasPrefix(cmd, "kick Q ") || strings.Contains(cmd, "run kick Q ") {
				leaves, feedback, disconnects := 0, 0, 0
				for _, event := range evs {
					if strings.HasPrefix(event, "chat ") && strings.Contains(event, "multiplayer.player.left") {
						leaves++
					}
					if strings.HasPrefix(event, "chat ") && strings.Contains(event, "commands.kick.success") {
						feedback++
					}
					if strings.HasPrefix(event, "Q:0x20:") {
						disconnects++
					}
					if operatorTarget && strings.HasPrefix(event, "Qchat ") {
						t.Fatalf("kick target received post-disconnect feedback: %v", evs)
					}
				}
				if leaves != 1 || feedback != 1 || disconnects != 1 || len(evs) != 3 {
					t.Fatalf("kick receipt counts leave%d feedback%d disconnect%d: %v", leaves, feedback, disconnects, evs)
				}
			}
			if storeVariant && strings.HasPrefix(cmd, "scoreboard players get ") {
				if len(evs) != 1 || !strings.Contains(evs[0], `"translate": "commands.scoreboard.players.get.success"`) || !strings.Contains(evs[0], "i(1)") {
					t.Fatalf("%s expected actual stored one for %q: %v", addr, cmd, evs)
				}
			}
			t.Logf("%s cmd=%q receipts=%v", addr, cmd, evs)
			out = append(out, evs)
		}
		if storeVariant {
			return out
		}
		// A self-kick has no surviving issuer for a command marker. Keep
		// P's stream through actual EOF and use an independent observer's
		// leave receipt; compare every delivered feedback/disconnect packet.
		observer, ro, wo := joinPlayBlackbox(t, addr, "R")
		defer observer.Close()
		_ = observer.SetDeadline(time.Time{})
		_ = wo.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		observed := newChatSink(ro, wo)
		observed.cap = 1 << 20
		// Explicitly admit the observer as an operator on both servers;
		// default harness permissions differ and cannot pin admin echoes.
		sendCommand(t, pw, "op R")
		sendCommand(t, pw, `tellraw R "AdminTagSelfReady"`)
		_ = waitForMarker(t, observed, "AdminTagSelfReady", 15*time.Second)
		sendCommand(t, pw, `tellraw P "AdminTagSelfStart"`)
		_ = waitForMarker(t, sink, "AdminTagSelfStart", 15*time.Second)
		sendCommand(t, pw, "kick P self reason")
		deadline := time.Now().Add(10 * time.Second)
		var selfLines, observerLines []string
		var closedErr error
		for time.Now().Before(deadline) {
			selfLines = append(selfLines, sink.take()...)
			observerLines = append(observerLines, observed.take()...)
			sink.mu.Lock()
			closedErr = sink.err
			sink.mu.Unlock()
			if closedErr != nil && strings.Contains(strings.Join(observerLines, ""), "multiplayer.player.left") {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !errors.Is(closedErr, io.EOF) {
			t.Fatalf("self-kick closure = %v", closedErr)
		}
		// The reader may enqueue the last packet immediately before setting
		// EOF, after the loop's take. EOF makes this final drain complete.
		selfLines = append(selfLines, sink.take()...)
		// EOF belongs to P's transport; an independent R command receipt
		// closes the observer stream after the kick's operator feedback.
		sendCommand(t, wo, `tellraw R "AdminTagSelfDone"`)
		observerLines = append(observerLines, waitForMarker(t, observed, "AdminTagSelfDone", 15*time.Second)...)
		var selfEvents []string
		disconnects, leaves, admins := 0, 0, 0
		for _, line := range selfLines {
			if tree, ok := componentTree(line, true); ok {
				selfEvents = append(selfEvents, "chat "+maskUUID(tree))
			}
			if strings.HasPrefix(line, "0x20:") {
				selfEvents = append(selfEvents, "P:"+maskUUID(line))
				disconnects++
			}
		}
		for _, line := range observerLines {
			if strings.Contains(line, "AdminTagSelfDone") {
				continue
			}
			if tree, ok := componentTree(line, true); ok {
				selfEvents = append(selfEvents, "R:"+maskUUID(tree))
				if strings.Contains(tree, "multiplayer.player.left") {
					leaves++
				}
				if strings.Contains(tree, "chat.type.admin") && strings.Contains(tree, "commands.kick.success") {
					admins++
				}
			}
		}
		if disconnects != 1 || leaves != 1 || admins != 1 || len(selfEvents) != 3 {
			t.Fatalf("self-kick receipts disconnects%d leaves%d admins%d: %v", disconnects, leaves, admins, selfEvents)
		}
		t.Logf("%s self-kick actual EOF receipts=%v", addr, selfEvents)
		out = append(out, selfEvents)
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i := range want {
		cmd := "kick P self reason"
		if i < len(cmds) {
			cmd = cmds[i]
		}
		// Native has produced both orders for the self-observer pair and the
		// ordinary Q-kick pair. Preserve raw logs and allow only each pair's
		// exact two payloads; every other event remains ordered.
		if !storeVariant && i == len(cmds) && len(want[i]) == 3 && len(got[i]) == 3 && want[i][0] == got[i][0] {
			samePair := want[i][1] == got[i][1] && want[i][2] == got[i][2]
			reversedPair := want[i][1] == got[i][2] && want[i][2] == got[i][1]
			if samePair || reversedPair {
				continue
			}
		}
		if (strings.HasPrefix(cmd, "kick Q ") || strings.Contains(cmd, "run kick Q ")) && len(want[i]) == 3 && len(got[i]) == 3 && want[i][2] == got[i][2] {
			samePair := want[i][0] == got[i][0] && want[i][1] == got[i][1]
			reversedPair := want[i][0] == got[i][1] && want[i][1] == got[i][0]
			if samePair || reversedPair {
				continue
			}
		}
		if len(want[i]) != len(got[i]) {
			t.Errorf("cmd %q: vanilla %v\n  mc-go %v", cmd, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("cmd %q event %d:\n  vanilla %s\n  mc-go   %s", cmd, j, want[i][j], got[i][j])
			}
		}
	}
}
