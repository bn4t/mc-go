package blackbox

// TestSniperDuelParity kills a NoAI skeleton with player-owned arrows
// while the player stands ≥50 horizontal blocks away.
// adventure/sniper_duel's killed_skeleton criterion wants entity_type
// skeleton, distance.horizontal ≥ 50 (victim vs the player's position
// when the kill resolves), and an is_projectile killing blow — a
// summoned arrow carrying Owner=[I;player-uuid] credits exactly that.
// The arrow is spawned 2 blocks short of the skeleton on a flat +z
// trajectory; three hits at ~8 damage each drop 20 health.

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/server"
)

// uuidIntArray renders a 16-byte uuid as the SNBT int-array tag
// ([I;a,b,c,d]) vanilla stores entity references as.
func uuidIntArray(u [16]byte) string {
	var v [4]int32
	for i := 0; i < 4; i++ {
		v[i] = int32(u[i*4])<<24 | int32(u[i*4+1])<<16 | int32(u[i*4+2])<<8 | int32(u[i*4+3])
	}
	return fmt.Sprintf("[I;%d,%d,%d,%d]", v[0], v[1], v[2], v[3])
}

func TestSniperDuelParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("SnipT"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()
	ownerTag := uuidIntArray(server.OfflineUUID("SnipT"))

	run := func(addr, who string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}, spawnTypes: map[int32][]int32{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "SnipT")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		for _, cmd := range []string{
			"gamerule doMobSpawning false", // flat-world slimes eat arrows mid-flight
			"kill @e[type=!player]",
			"time set midnight",
			"tp @s 30.5 -60 30.5 0 0",
			"summon minecraft:skeleton 30.5 -60 90.5 {NoAI:1b}",
		} {
			sendCommand(t, pw, cmd)
			advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		}
		// Owned arrows, one per round until the skeleton dies.
		for round := 0; round < 4 && !f.prog["minecraft:adventure/sniper_duel"]["killed_skeleton"]; round++ {
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:arrow 30.5 -59.5 88.0 {Motion:[0.0d,0.0d,4.0d],Owner:%s}", ownerTag))
			advCollect(t, c, pr, pw, 2500*time.Millisecond, f)
		}
		return f
	}

	want := run(targets.vanilla, "vanilla")
	got := run(targets.mcgo, "mc-go")
	if !want.prog["minecraft:adventure/sniper_duel"]["killed_skeleton"] {
		t.Fatalf("vanilla: killed_skeleton not obtained: prog=%v chats=%v", want.prog["minecraft:adventure/sniper_duel"], want.chats)
	}
	// Command echoes and join chatter are harness input, not gameplay.
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("sniper_duel parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
