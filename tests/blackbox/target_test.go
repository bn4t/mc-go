package blackbox

// TestTargetHitParity drops a player-owned arrow onto a target
// block's dead center on both servers: vanilla's
// TargetBlock.getRedstoneStrength reads |frac−0.5| over the hit
// face's two axes and bullseye wants signal 15 plus the projectile
// ≥30 horizontal blocks from the shooter. The arrow is summoned
// hovering over the center point so it falls straight down onto the
// top face — the cleanest dead-center hit that runs identically on
// both targets.

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestTargetHitParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Archer"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Archer")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "kill @e[type=!player]")
		// Target block floats at z=-30; the shooter stands 65 blocks
		// south so the projectile predicate's 30-block distance holds.
		sendCommand(t, pw, "tp @s 4 -60 35 0 0")
		sendCommand(t, pw, "setblock 4 -60 -30 minecraft:target")
		u := server.OfflineUUID("Archer")
		i := func(off int) int32 {
			return int32(u[off])<<24 | int32(u[off+1])<<16 | int32(u[off+2])<<8 | int32(u[off+3])
		}
		// The arrow spawns over the top face's exact center
		// (4.5, -59, -29.5) and falls — the hit lands at frac .5/.5
		// → distance 0 → strength 15.
		sendCommand(t, pw, fmt.Sprintf(
			"summon minecraft:arrow 4.5 -55 -29.5 {Motion:[0.0,-0.6,0.0],Owner:[I;%d,%d,%d,%d]}",
			i(0), i(4), i(8), i(12)))
		advCollect(t, c, pr, pw, 2500*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for _, tc := range [][2]string{
		{"minecraft:adventure/bullseye", "bullseye"},
	} {
		if !want.prog[tc[0]][tc[1]] {
			t.Fatalf("vanilla: %s[%s] not obtained: %v", tc[0], tc[1], want.prog[tc[0]])
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("target_hit parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
