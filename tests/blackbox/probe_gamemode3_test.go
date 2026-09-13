package blackbox

// Throwaway probe v3: /gamemode at another online player. Deleted
// once encoded.

import (
	"testing"
	"time"
)

func TestProbeGamemode3(t *testing.T) {
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("ProbeG", "ProbeH"),
	}, nil)
	defer tg.cleanup()

	g := joinPlayOp(t, tg.vanilla, "ProbeG")
	defer g.conn.Close()
	drainToJoinBlackbox(t, "vanilla", g.pr)
	h := joinPlayOp(t, tg.vanilla, "ProbeH")
	defer h.conn.Close()
	drainToJoinBlackbox(t, "vanilla", h.pr)
	_ = g.conn.SetDeadline(time.Now().Add(60 * time.Second))
	_ = h.conn.SetDeadline(time.Now().Add(60 * time.Second))
	gsink := newUseClient(g.pr, g.pw, true)
	hsink := newUseClient(h.pr, h.pw, true)
	time.Sleep(1500 * time.Millisecond)
	gsink.take()
	hsink.take()

	for _, cmd := range []string{"gamemode survival ProbeH", "gamemode creative @p", "gamemode adventure ProbeG"} {
		sendCommand(t, g.pw, cmd)
		time.Sleep(1200 * time.Millisecond)
		t.Logf("=== %q sender:", cmd)
		for _, p := range gsink.take() {
			if p.id == 0x79 || p.id == 0x26 || p.id == 0x83 {
				t.Logf("  pkt %#x: %q", p.id, p.data)
			}
		}
		t.Logf("=== target:")
		for _, p := range hsink.take() {
			if p.id == 0x79 || p.id == 0x26 || p.id == 0x83 {
				t.Logf("  pkt %#x: %q", p.id, p.data)
			}
		}
	}
}
