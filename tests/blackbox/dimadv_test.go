package blackbox

// TestChangedDimensionParity stands the player inside a setblock'd
// nether_portal and waits out the survival 80-tick charge: vanilla's
// changed_dimension trigger lights nether/root's entered_nether and
// story/enter_the_nether on the hop, and the flush diff covers the
// grant plus the nether tree's def fan-out.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestChangedDimensionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("DimHop"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "DimHop")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "setblock 4 -60 2 minecraft:nether_portal")
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		// 80-tick survival portal charge + travel + flush.
		advCollect(t, c, pr, pw, 8*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:nether/root"]["entered_nether"] {
		t.Fatalf("vanilla: nether/root entered_nether not obtained: %v", want.prog["minecraft:nether/root"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("changed_dimension parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}

// TestEnterEndGatewayParity drops an end_gateway block into the flat
// world and walks the player inside on both servers: enter_block fires
// the game-tree criterion (end/enter_end_gateway) the same tick.
func TestEnterEndGatewayParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Gater"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Gater")
		defer c.Close()
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "setblock 4 -60 8 minecraft:end_gateway")
		sendCommand(t, pw, "tp @s 4 -60 2")
		advCollect(t, c, pr, pw, 400*time.Millisecond, f)
		// Step inside the gateway block.
		sendCommand(t, pw, "tp @s 4 -60 8")
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for _, tc := range [][2]string{
		{"minecraft:end/enter_end_gateway", "entered_end_gateway"},
	} {
		if !want.prog[tc[0]][tc[1]] {
			t.Fatalf("vanilla: %s[%s] not obtained: %v", tc[0], tc[1], want.prog[tc[0]])
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("enter_block parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
