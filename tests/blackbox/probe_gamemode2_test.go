package blackbox

// Throwaway probe v2: capture vanilla 26.2's answers to /gamemode
// variants WITHOUT the consuming sentinel. Deleted once encoded.

import (
	"testing"
	"time"
)

func TestProbeGamemode2(t *testing.T) {
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
	time.Sleep(1500 * time.Millisecond)
	sink.take()

	variants := []string{
		"gamemode survival",
		"gamemode creative",
		"gamemode bogus",
		"gamemode",
		"gamemode s",
		"gamemode creative Nobody",
		"gamemode adventure",
		"gamemode spectator",
		"gamemode survival ProbeG extra",
		"gamemode creative @s",
	}
	for _, cmd := range variants {
		sendCommand(t, s.pw, cmd)
		time.Sleep(1200 * time.Millisecond)
		t.Logf("=== %q", cmd)
		seen := map[int32]int{}
		for _, p := range sink.take() {
			seen[p.id]++
			if p.id == 0x79 || p.id == 0x7e || p.id == 0x26 || p.id == 0x3b || p.id == 0x83 {
				t.Logf("  pkt %#x: %q", p.id, p.data)
			}
		}
		t.Logf("  histogram: %v", seen)
	}
}
