package blackbox

// TestFallFromHeightParity flies the player to y=400 then streams
// airborne move packets down to the flat ground on both servers —
// neither server simulates player gravity, so the fall is reported
// by the client. fall_from_height fires on the landing tick with
// distance.y = start−end, and adventure/fall_from_world_height wants
// start y≥319, end y≤−59, drop ≥379.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestFallFromHeightParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Faller"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	move := func(pw *protocol.Writer, x, y, z float64, onGround bool) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		var f byte
		if onGround {
			f = 0x01
		}
		b.Byte(f)
		_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
	}

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Faller")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "tp @s 4 400 2")
		advCollect(t, c, pr, pw, 700*time.Millisecond, f)
		// ~3.1 blocks per packet, one per tick — inside the vanilla
		// fall-speed envelope and past the distance bound quickly.
		for y := 398.0; y > -60; y -= 3.1 {
			move(pw, 4, y, 2, false)
			advCollect(t, c, pr, pw, 22*time.Millisecond, f)
		}
		move(pw, 4, -60, 2, true)
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for _, tc := range [][2]string{
		{"minecraft:adventure/fall_from_world_height", "fall_from_world_height"},
	} {
		if !want.prog[tc[0]][tc[1]] {
			t.Fatalf("vanilla: %s[%s] not obtained: %v", tc[0], tc[1], want.prog[tc[0]])
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("fall_from_height parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
