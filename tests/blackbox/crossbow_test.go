package blackbox

// TestShotCrossbowParity fires adventure/ol_betsy on both servers:
// Use Item charges the crossbow for the vanilla 25-tick hold, the
// release arms it, and the second use fires — shot_crossbow grants
// with the crossbow itself as the condition item. A summoned
// pillager then dies to crossbow bolts for whos_the_pillager_now
// (killed_by_arrow with fired_from_weapon=crossbow).

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

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "XBow")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "kill @e[type=!player]")
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:crossbow")
		sendCommand(t, pw, "summon minecraft:pillager 4 -60 14 {NoAI:1b}")
		advCollect(t, c, pr, pw, 700*time.Millisecond, f)

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
		// Each shot: charge ≥25t, release to arm, use to fire. Seven
		// arrows at ~7 damage cover the pillager's 24 health with
		// margin for aim jitter on both targets.
		for i := 0; i < 7; i++ {
			use()
			advCollect(t, c, pr, pw, 1350*time.Millisecond, f)
			release()
			advCollect(t, c, pr, pw, 150*time.Millisecond, f)
			use()
			advCollect(t, c, pr, pw, 400*time.Millisecond, f)
		}
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for _, tc := range [][2]string{
		{"minecraft:adventure/ol_betsy", "shot_crossbow"},
		{"minecraft:adventure/whos_the_pillager_now", "kill_pillager"},
	} {
		if !want.prog[tc[0]][tc[1]] {
			t.Fatalf("vanilla: %s[%s] not obtained: %v", tc[0], tc[1], want.prog[tc[0]])
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("shot_crossbow parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
