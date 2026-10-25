package blackbox

// TestSlideDownBlockParity stands a honey column and slides the player
// down beside it — vanilla's entityInside fires slide_down_block while
// the box overlaps the inset honey shape, the feet sit below its top
// (+0.9375), and the entity is descending with vy < -0.08. The harness
// streams airborne move packets hugging the block face.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestSlideDownBlockParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Slider"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	move := func(pw *protocol.Writer, x, y, z float64) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		b.Byte(0) // airborne
		_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
	}

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Slider")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "tp @s 5.24 -55 -10.5")
		sendCommand(t, pw, "fill 4 -58 -11 4 -64 -10 minecraft:honey_block")
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		// Slide needs the center past the inset column edge (x<5.2375
		// inside it reads as standing on the honey): x=5.24 clips cell
		// 4 by 0.02 of box edge. Descend 0.15 per packet past the face.
		for y := -56.0; y > -63; y -= 0.1 {
			move(pw, 5.24, y, -10.5)
			advCollect(t, c, pr, pw, 22*time.Millisecond, f)
		}
		advCollect(t, c, pr, pw, 800*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/honey_block_slide"]["honey_block_slide"] {
		t.Fatalf("vanilla: honey_block_slide not obtained: %v", want.prog["minecraft:adventure/honey_block_slide"])
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("slide_down_block parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
