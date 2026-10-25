package blackbox

// TestTotemParity pops a totem of undying on both servers: a survival
// player holding a mainhand totem takes a lethal zombie hit and lives.
// Vanilla's checkTotemDeathProtection shrinks the stack by one, fires
// used_totem (adventure/totem_of_undying), resets health to 1, runs
// the death_protection component's effects (clear-all, regen II 900t,
// absorption II 100t, fire resistance I 800t), and broadcasts Entity
// Event 35 — all of which the flush records.

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestTotemParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("TotV", "TotM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	const adv = "minecraft:adventure/totem_of_undying"
	run := func(addr, name string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}, spawnTypes: map[int32][]int32{}, soundIDs: map[int32]bool{}, mobFx: map[int32]string{}}
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		for _, cmd := range []string{
			"gamerule doMobSpawning false",
			"kill @e[type=!player]",
			"time set midnight",
			"gamemode survival @s",
			"tp @s 6 -60 2 0 0",
			"item replace entity @s weapon.mainhand with minecraft:totem_of_undying",
		} {
			sendCommand(t, pw, cmd)
			advCollect(t, c, pr, pw, 400*time.Millisecond, f)
		}
		sendCommand(t, pw, "summon minecraft:zombie 8 -60 2")
		// Zombie approach + melee to 20hp at ~2s per landed hit:
		// poll for the pop up to 30s.
		for i := 0; i < 60 && f.ev35 == 0; i++ {
			advCollect(t, c, pr, pw, 500*time.Millisecond, f)
		}
		// Freeze before the follow-up hit can really kill.
		sendCommand(t, pw, "kill @e[type=!player]")
		advCollect(t, c, pr, pw, 1500*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla, "TotV")
	got := run(targets.mcgo, "TotM")
	if !want.prog[adv]["used_totem"] {
		t.Fatalf("vanilla: used_totem not obtained: prog=%v ev35=%d fx=%v chats=%v",
			want.prog, want.ev35, want.mobFx, want.chats)
	}
	if got.ev35 != want.ev35 {
		t.Fatalf("totem pop event: vanilla=%d mc-go=%d", want.ev35, got.ev35)
	}
	for eff, spec := range want.mobFx {
		if got.mobFx[eff] != spec {
			t.Fatalf("mob effect %d: vanilla=%q mc-go=%q", eff, spec, got.mobFx[eff])
		}
	}
	for eff := range got.mobFx {
		if _, ok := want.mobFx[eff]; !ok {
			t.Fatalf("mc-go sent extra mob effect %d", eff)
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("totem parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
