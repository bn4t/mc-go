package blackbox

// TestNetherTravelParity does the fast_travel loop: portal in at the
// overworld origin, then tp 1000 nether-blocks away inside a second
// portal — the return trip lands ~8000 overworld blocks out, past
// nether/fast_travel's 7000-block distance check.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestNetherTravelParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trav"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Trav")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "setblock 4 -60 2 minecraft:nether_portal")
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		advCollect(t, c, pr, pw, 8*time.Second, f) // 80-tick charge + hop
		// Vanilla's post-travel portalCooldown (300t) only ticks down
		// OUTSIDE a portal — arrive, step out, wait it off, step back in.
		sendCommand(t, pw, "tp @s 4 60 4 0 0")
		// Vanilla refuses setblock into an unloaded chunk — tp near the
		// target first so the chunk loads, then build and enter.
		sendCommand(t, pw, "tp @s 1000 60 4 0 0")
		advCollect(t, c, pr, pw, 2*time.Second, f)
		sendCommand(t, pw, "setblock 1004 60 2 minecraft:nether_portal")
		advCollect(t, c, pr, pw, 14*time.Second, f) // finish cooldown out
		sendCommand(t, pw, "tp @s 1004 60 2 0 0")
		advCollect(t, c, pr, pw, 8*time.Second, f) // 80-tick charge + hop
		advCollect(t, c, pr, pw, 2*time.Second, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	// explore_nether's per-biome criteria are location triggers: which
	// nether biome the exit portal lands in is vanilla-terrain
	// dependent, not behavior mc-go models — compare without it.
	delete(want.prog, "minecraft:nether/explore_nether")
	delete(got.prog, "minecraft:nether/explore_nether")
	if !want.prog["minecraft:nether/fast_travel"]["travelled"] {
		t.Fatalf("vanilla: fast_travel/travelled not obtained: %v", want.prog["minecraft:nether/fast_travel"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("nether_travel parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
