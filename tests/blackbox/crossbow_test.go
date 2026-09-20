package blackbox

// TestShotCrossbowParity fires adventure/ol_betsy on both servers:
// Use Item charges the crossbow for the vanilla 25-tick hold, the
// release arms it, and the second use fires — shot_crossbow grants
// with the crossbow itself as the condition item.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestShotCrossbowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("XBow"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	const adv = "minecraft:adventure/ol_betsy"
	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "XBow")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:crossbow")
		advCollect(t, c, pr, pw, 500*time.Millisecond, f)

		use := func() {
			var ub protocol.Buffer
			ub.VarInt(0) // main hand
			ub.VarInt(0) // sequence
			ub.Float(0)
			ub.Float(0)
			_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		}
		release := func() {
			var ab protocol.Buffer
			ab.VarInt(5) // RELEASE_USE_ITEM
			ab.Position(0, 0, 0)
			ab.Byte(0)
			ab.VarInt(0)
			_ = pw.WritePacket(server.IDPlayPlayerActionSB, ab.Bytes())
		}
		// Charge: hold past 25 ticks, release to arm, use once more to
		// fire — vanilla's three-step crossbow.
		use()
		advCollect(t, c, pr, pw, 1400*time.Millisecond, f)
		release()
		advCollect(t, c, pr, pw, 200*time.Millisecond, f)
		use()
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog[adv]["shot_crossbow"] {
		t.Fatalf("vanilla: shot_crossbow not obtained: %v", want.prog[adv])
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("shot_crossbow parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
