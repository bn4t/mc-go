//go:build blackbox

package blackbox

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestTpParity drives the measured /tp grammar — entity and coordinate
// forms, multi-target victims, relative/local/absolute coordinates,
// rotations, facing — and diffs every rendered event between vanilla
// and mc-go, including the unknown.argument cursor positions and the
// parse-vs-exec failure split (notfound.entity vs underlined errors).
func TestTpParity(t *testing.T) {
	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)
	cases := []string{
		"tp", "tp Q", "tp 0 65 0", "tp Q 0 65 0", "tp @s Q",
		"tp Q ~ ~1 ~", "tp NoSuch", "tp 0", "tp 0 65",
		"tp x y z", "tp 0 65 0 extra",
		"tp @e[type=pig,limit=1] Q", "tp @e[limit=2] Q",
		"tp @s 0 65 0 90 45", "tp @s 0 65 0 90", "tp @s 99999999 65 0",
		"tp Q @s", "tp Q Q", "tp @a Q", "tp @e Q",
		"tp 0 65 0 0 0 0", "tp ~ ~ ~",
		"tp 0 65 0 90", "tp P 0 65 0 90 45", "tp @s @s",
		"tp NoSuch 0 0 0", "tp Q 99999999 0 0",
		"tp @s 0 65 0 -400 -200", "tp @s ^ ^ ^",
		"tp Q x y z", "tp 0 65 0 90 45", "tp Q 0 65 0 90 45",
		"tp @e[limit=2] @s", "tp @a 0 65 0", "tp @e[limit=2]",
		"tp 14075747-e146-3809-a161-83abe512191e",
		"tp @s 0 65 0 0 0 facing entity Q",
		"tp @s 0 65 0 0 0 facing entity Q eyes",
		"tp @s 0 65 0 0 0 facing 1 2 3",
		"tp @s 0 65 0 0 0 facing entity Q badanchor",
		"tp @s facing entity Q",
	}

	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": `[{"uuid":"14075747-e146-3809-a161-83abe512191e","name":"P","level":2,"bypassesPlayerLimit":false}]`},
		func(s *server.Server) {
			s.OpsRequired = true
			s.SetOpLevel("P", 2)
		})
	defer targets.cleanup()

	run := func(addr string) [][]string {
		var c net.Conn
		var pw *protocol.Writer
		var sink *chatSink
		rejoin := func() {
			if c != nil {
				c.Close()
			}
			var pr *protocol.Reader
			c, pr, pw = joinPlayQuiet(t, addr, "P")
			_ = c.SetDeadline(time.Time{})
			sink = newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(2 * time.Second)
		}
		rejoin()
		defer c.Close()

		cQ, _, _ := joinPlayQuiet(t, addr, "Q")
		defer cQ.Close()
		time.Sleep(time.Second)

		var out [][]string
		for i, cmd := range cases {
			marker := fmt.Sprintf("ZZ%d", i)
			var evs []string
			for attempt := 0; attempt < 4; attempt++ {
				sink.take()
				var b protocol.Buffer
				b.String(cmd)
				if err := pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
					rejoin()
					continue
				}
				time.Sleep(150 * time.Millisecond)
				b = protocol.Buffer{}
				b.String("me " + marker)
				if err := pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes()); err != nil {
					rejoin()
					continue
				}
				evs = evs[:0]
				deadline := time.Now().Add(5 * time.Second)
				got := false
				quietUntil := time.Now()
				for {
					before := len(evs)
					for _, line := range sink.take() {
						if strings.Contains(line, marker) {
							got = true
							continue
						}
						var ev string
						if tree, ok := componentTree(line, true); ok {
							ev = tree
						} else if strings.HasPrefix(line, "0x79:") {
							i2 := strings.Index(line, ":")
							if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
								ev = renderNBT(root, true)
							}
						}
						if ev != "" {
							evs = append(evs, numRe.ReplaceAllString(maskUUID(ev), "N"))
						}
					}
					if len(evs) != before {
						quietUntil = time.Now().Add(150 * time.Millisecond)
					}
					if time.Now().After(deadline) || (got && time.Now().After(quietUntil)) {
						break
					}
					time.Sleep(30 * time.Millisecond)
				}
				if got {
					break
				}
				rejoin()
			}
			out = append(out, evs)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, cmd := range cases {
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

// TestTpVoidDeathParity sends the issuer below the world so void
// damage kills it, then diffs command replies from the dead session —
// vanilla keeps executing a dead player's commands but resolves @s
// differently and applies its isAlive selector filters.
func TestTpVoidDeathParity(t *testing.T) {
	numRe := regexp.MustCompile(`d\(-?[0-9.e+]+\)|f\(-?[0-9.e+]+\)|i\(-?[0-9]+\)|b\([0-9]+\)|l\(-?[0-9]+\)|"-?[0-9]+(?:\.[0-9]+)?"`)

	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": `[{"uuid":"14075747-e146-3809-a161-83abe512191e","name":"P","level":2,"bypassesPlayerLimit":false}]`},
		func(s *server.Server) {
			s.OpsRequired = true
			s.SetOpLevel("P", 2)
		})
	defer targets.cleanup()

	send := func(pw *protocol.Writer, cmd string) bool {
		var b protocol.Buffer
		b.String(cmd)
		return pw.WritePacket(server.IDPlayChatCommandSB, b.Bytes()) == nil
	}
	collect := func(sink *chatSink, marker string, deadline time.Duration) []string {
		var evs []string
		end := time.Now().Add(deadline)
		quietUntil := time.Now()
		got := false
		for time.Now().Before(end) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					got = true
					continue
				}
				var ev string
				if tree, ok := componentTree(line, true); ok {
					ev = tree
				} else if strings.HasPrefix(line, "0x79:") {
					i2 := strings.Index(line, ":")
					if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
						ev = renderNBT(root, true)
					}
				}
				if ev != "" {
					evs = append(evs, numRe.ReplaceAllString(maskUUID(ev), "N"))
					quietUntil = time.Now().Add(150 * time.Millisecond)
				}
			}
			if got && time.Now().After(quietUntil) {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		return evs
	}

	run := func(addr string) [][]string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		cQ, _, _ := joinPlayQuiet(t, addr, "Q")
		defer cQ.Close()
		time.Sleep(1500 * time.Millisecond)

		var out [][]string
		// The kill teleport's reply plus the death broadcast when the
		// void ticks down (~2s of 4dmg hits).
		sink.take()
		send(pw, "tp @s 0 -3000 0")
		var death []string
		end := time.Now().Add(12 * time.Second)
		for time.Now().Before(end) {
			for _, line := range sink.take() {
				var ev string
				if tree, ok := componentTree(line, true); ok {
					ev = tree
				} else if strings.HasPrefix(line, "0x79:") {
					i2 := strings.Index(line, ":")
					if root, err := nbt.ReadUnnamed(strings.NewReader(line[i2+1:])); err == nil {
						ev = renderNBT(root, true)
					}
				}
				if ev != "" {
					death = append(death, numRe.ReplaceAllString(maskUUID(ev), "N"))
				}
			}
			joined := strings.Join(death, "|")
			if strings.Contains(joined, "outOfWorld") {
				time.Sleep(300 * time.Millisecond)
				for _, line := range sink.take() {
					if tree, ok := componentTree(line, true); ok {
						death = append(death, numRe.ReplaceAllString(maskUUID(tree), "N"))
					}
				}
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		out = append(out, death)
		postDeath := []string{
			"tp @e Q", "tp @s @s", "tp ~ ~ ~", "tp @a Q",
			"tp P Q", "tp Q @s", "tp @e[limit=2] Q", "tp Q",
		}
		for i, cmd := range postDeath {
			marker := fmt.Sprintf("ZZD%d", i)
			sink.take()
			if !send(pw, cmd) {
				out = append(out, []string{"<write-failed>"})
				continue
			}
			time.Sleep(150 * time.Millisecond)
			send(pw, "me "+marker)
			out = append(out, collect(sink, marker, 5*time.Second))
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if len(want) != len(got) {
		t.Fatalf("event groups differ: %d vs %d", len(want), len(got))
	}
	for i := range want {
		if len(want[i]) != len(got[i]) {
			t.Errorf("group %d: vanilla %v\n  mc-go %v", i, want[i], got[i])
			continue
		}
		for j := range want[i] {
			if want[i][j] != got[i][j] {
				t.Errorf("group %d event %d:\n  vanilla %s\n  mc-go   %s", i, j, want[i][j], got[i][j])
			}
		}
	}
}
