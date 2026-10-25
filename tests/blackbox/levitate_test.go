package blackbox

// TestLevitationParity grants the player levitation and streams rising
// move packets — vanilla stamps levitationStartPos when the effect
// lands and fires the trigger every tick until the effect leaves, with
// distance.y measured from that anchor (end/levitate wants ≥50).

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestLevitationParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Floater"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	move := func(pw *protocol.Writer, x, y, z float64) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		b.Byte(0)
		_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
	}

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Floater")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "tp @s 4 -60 2")
		sendCommand(t, pw, "effect give @s minecraft:levitation infinite 0")
		advCollect(t, c, pr, pw, 400*time.Millisecond, f)
		// Levitation's vanilla rise is ~0.9/tick at amplifier 0; a
		// packet-driven rise of 0.9 per step clears 50 in ~56 ticks.
		for y := -60.0; y < 0.0; y += 0.9 {
			move(pw, 4, y, 2)
			advCollect(t, c, pr, pw, 22*time.Millisecond, f)
		}
		advCollect(t, c, pr, pw, 800*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:end/levitate"]["levitated"] {
		t.Fatalf("vanilla: levitate not obtained: %v", want.prog["minecraft:end/levitate"])
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("levitation parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
