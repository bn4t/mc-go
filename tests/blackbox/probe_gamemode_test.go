package blackbox

// Throwaway probe: capture vanilla 26.2's answers to /gamemode
// variants (replies plus packets). Deleted once encoded.

import (
	"fmt"
	"testing"
	"time"
)

func TestProbeGamemode(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ProbeG"),
	}, nil)
	defer tg.cleanup()

	s := joinPlayOp(t, tg.vanilla, "ProbeG")
	defer s.conn.Close()
	drainToJoinBlackbox(t, "vanilla", s.pr)
	_ = s.conn.SetDeadline(time.Now().Add(60 * time.Second))
	sink := newUseClient(s.pr, s.pw, true)
	sendCommand(t, s.pw, "tp @s 6 -60 2 0 0")
	if !digSentinel(t, s.pw, sink, "READY", 30*time.Second) {
		t.Fatal("never ready")
	}
	sink.take()

	variants := []string{
		"gamemode survival",
		"gamemode creative",
		"gamemode survival @s",
		"gamemode creative ProbeG",
		"gamemode bogus",
		"gamemode",
		"gamemode s",
		"gamemode creative Nobody",
		"gamemode adventure",
		"gamemode spectator",
		"gamemode survival ProbeG extra",
	}
	for i, cmd := range variants {
		sendCommand(t, s.pw, cmd)
		mark := fmt.Sprintf("GM%d", i)
		if !digSentinel(t, s.pw, sink, mark, 10*time.Second) {
			t.Fatalf("no ack for %q", cmd)
		}
		time.Sleep(300 * time.Millisecond)
		t.Logf("=== %q", cmd)
		for _, p := range sink.take() {
			switch p.id {
			case 0x79:
				t.Logf("  chat: %q", p.data)
			case 0x26:
				t.Logf("  gameevent: %x", p.data)
			case 0x3b, 0x3c:
				t.Logf("  pkt %#x: %x", p.id, p.data)
			default:
				t.Logf("  pkt %#x len=%d", p.id, len(p.data))
			}
		}
	}
}
