//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestEntityCrammingParity pins LivingEntity.pushEntities'
// max_entity_cramming gate: each living tick a mob counts the
// pushable entities whose bounding boxes intersect its own (itself
// and riding passengers excluded); above max_entity_cramming-1
// neighbors, a 1-in-4 roll deals cramming 6 to that mob — repeated
// every tick, a pile thins to at most the cap, while a cap at the
// pile size, or 0 (the rule disabled), kills nobody.
func TestEntityCrammingParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)

	awaitKey := func(sink *chatSink, key string) bool {
		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			for _, line := range sink.take() {
				if strings.Contains(line, key) {
					return true
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		return false
	}

	countOf := func(t *testing.T, sink *chatSink, pw *protocol.Writer, tag string) int {
		t.Helper()
		sendCommand(t, pw, fmt.Sprintf(
			"execute store result storage z:cram n int 1 run execute if entity @e[tag=%s]", tag))
		sendCommand(t, pw, "data get storage z:cram n")
		sendCommand(t, pw, "say ZEND")
		deadline := time.Now().Add(6 * time.Second)
		got := ""
		for time.Now().Before(deadline) {
			end := false
			for _, line := range sink.take() {
				if i := strings.Index(line, "data.storage.query"); i >= 0 {
					got = line[:i]
				}
				if strings.Contains(line, "ZEND") {
					end = true
				}
			}
			if end {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if got == "" {
			return -1
		}
		return int(lastNumber(got))
	}

	run := func(addr string) map[string]int {
		cv, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "tp P 14 -59 14 0 0")
		sendCommand(t, pw, "time set 18000")
		sendCommand(t, pw, "weather clear")
		sendCommand(t, pw, "kill @e[type=chicken]")

		fillb := func(cmd string) {
			for range 30 {
				sendCommand(t, pw, cmd)
				if awaitKey(sink, "fill.") {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("fill never landed: %s", cmd)
		}
		// Three 1×1 cells so the pushed-apart chickens stay stacked:
		// vanilla's doPush would otherwise separate a pile before
		// cramming finishes thinning it.
		fillb("fill 9 -61 9 11 -58 11 minecraft:stone hollow")
		fillb("fill 13 -61 9 15 -58 11 minecraft:stone hollow")
		fillb("fill 17 -61 9 19 -58 11 minecraft:stone hollow")

		pile := func(tag string, x float64) {
			for range 8 {
				sendCommand(t, pw, fmt.Sprintf(
					"summon minecraft:chicken %v -59 10.5 {NoAI:1b,Tags:[\"%s\"]}", x, tag))
				awaitKey(sink, "summon")
			}
		}

		out := map[string]int{}
		// Cap 4 with 8 stacked: every mob sees 7 > 3 neighbors —
		// the pile thins to at most 4 survivors.
		sendCommand(t, pw, "gamerule max_entity_cramming 4")
		pile("c4", 10.5)
		time.Sleep(6 * time.Second)
		out["cap4"] = countOf(t, sink, pw, "c4")
		// Cap 8 with 8 stacked: each sees 7, the gate is > 7 — the
		// boundary pile stays whole.
		sendCommand(t, pw, "gamerule max_entity_cramming 8")
		pile("c8", 14.5)
		time.Sleep(6 * time.Second)
		out["cap8"] = countOf(t, sink, pw, "c8")
		// Cap 0 disables the check outright.
		sendCommand(t, pw, "gamerule max_entity_cramming 0")
		pile("c0", 18.5)
		time.Sleep(6 * time.Second)
		out["cap0"] = countOf(t, sink, pw, "c0")
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for name, got := range map[string]int{"vanilla": van["cap4"], "mc-go": mc["cap4"]} {
		if got > 4 {
			t.Errorf("%s cap4: %d survivors, want <= 4", name, got)
		}
		if got < 0 {
			t.Errorf("%s cap4: count read failed (%d)", name, got)
		}
	}
	for name, m := range map[string]map[string]int{"vanilla": van, "mc-go": mc} {
		if m["cap8"] != 8 {
			t.Errorf("%s cap8: %d survivors, want 8 (7 neighbors does not cross > cap-1)", name, m["cap8"])
		}
		if m["cap0"] != 8 {
			t.Errorf("%s cap0: %d survivors, want 8 (rule disabled)", name, m["cap0"])
		}
	}
}
