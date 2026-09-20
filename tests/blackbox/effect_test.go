package blackbox

// TestEffectAdvParity drives nether/all_potions ("A Furious
// Cocktail") through /effect give on both servers: the 17 potion
// effects must sit active simultaneously for the effects_changed
// criterion to grant. /effect give/clear replies, the update and
// remove mob_effect packets, and the grant all join the flush diff.

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/server"
)

var potionEffects = []string{
	"speed", "slowness", "strength", "jump_boost", "regeneration",
	"fire_resistance", "water_breathing", "invisibility", "night_vision",
	"weakness", "poison", "resistance", "slow_falling", "wind_charged",
	"weaving", "oozing", "infested",
}

func TestEffectAdvParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("EffV", "EffM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	const adv = "minecraft:nether/all_potions"
	run := func(addr, name string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}, mobFx: map[int32]string{}}
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		for _, eff := range potionEffects {
			sendCommand(t, pw, fmt.Sprintf("effect give @s minecraft:%s 300", eff))
			advCollect(t, c, pr, pw, 150*time.Millisecond, f)
		}
		// Clear one to prove specific-clear parity, then re-give so the
		// set stays complete on both sides.
		sendCommand(t, pw, "effect clear @s minecraft:speed")
		advCollect(t, c, pr, pw, 150*time.Millisecond, f)
		sendCommand(t, pw, "effect give @s minecraft:speed 300")
		advCollect(t, c, pr, pw, 800*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla, "EffV")
	got := run(targets.mcgo, "EffM")
	if !want.prog[adv]["all_effects"] {
		t.Fatalf("vanilla: all_potions not obtained: prog=%v fx=%v", want.prog, want.mobFx)
	}
	for eff, spec := range want.mobFx {
		if got.mobFx[eff] != spec {
			t.Fatalf("mob effect %d: vanilla=%q mc-go=%q", eff, spec, got.mobFx[eff])
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("effect parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}

// allEffects is the 34-effect set nether/all_effects ("How Did We
// Get Here?") requires simultaneously — the 17 potion effects plus
// the non-potion remainder, every one grantable via /effect.
var allEffects = append(append([]string{}, potionEffects...),
	"absorption", "bad_omen", "blindness", "breath_of_the_nautilus",
	"conduit_power", "darkness", "dolphins_grace", "glowing", "haste",
	"hero_of_the_village", "hunger", "levitation", "mining_fatigue",
	"nausea", "raid_omen", "trial_omen", "wither")

// TestAllEffectsParity grants all 34 at once on both servers: the
// all_potions subset finishes first, then all_effects — the hidden
// challenge def surfaces only after its parent, so the flush diff
// covers both the deferred visibility and the grant itself.
func TestAllEffectsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("AllFxV", "AllFxM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr, name string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}, mobFx: map[int32]string{}}
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		for _, eff := range allEffects {
			sendCommand(t, pw, fmt.Sprintf("effect give @s minecraft:%s infinite", eff))
			advCollect(t, c, pr, pw, 150*time.Millisecond, f)
		}
		advCollect(t, c, pr, pw, 800*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla, "AllFxV")
	got := run(targets.mcgo, "AllFxM")
	for _, adv := range [][2]string{
		{"minecraft:nether/all_potions", "all_effects"},
		{"minecraft:nether/all_effects", "all_effects"},
	} {
		if !want.prog[adv[0]][adv[1]] {
			t.Fatalf("vanilla: %s not obtained: prog=%v", adv[0], want.prog)
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("all_effects parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
