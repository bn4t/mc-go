package blackbox

import (
	"testing"
	"time"
)

// TestPlayerDataParity: `data get entity @s <key>` presence parity —
// the key set a fresh survival player reports. Measured on the 26.2
// jar: Entity.saveWithoutId (Pos/Motion/Rotation/fall_distance/Fire/
// Air/OnGround/Invulnerable/PortalCooldown/UUID/CustomName(cond)/
// CustomNameVisible/Silent/NoGravity/Glowing/TicksFrozen/
// HasVisualFire/Tags/Passengers(cond)), LivingEntity (absorption,
// attributes, CanPickUpLoot, DeathTime, FallFlying, Health, HurtTime,
// LeftHanded, current_impulse_context_reset_grace_time),
// Player/Avatar (Inventory, SelectedItemSlot, SleepTimer, Xp set,
// Score, abilities, EnderItems, food quad), ServerPlayer
// (warden_spawn_tracker, seenCredits, recipeBook, Dimension,
// playerGameType).
func TestPlayerDataParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("PD")}, nil)
	defer targets.cleanup()

	// The pinned vector: every key probed on both legs; presence
	// must match. Cond keys (CustomName, sleeping_pos, Passengers,
	// respawn, entered_nether_pos, ShoulderEntity*, active_effects,
	// RootVehicle) are expected ABSENT on a fresh player — their
	// absence is part of the pin.
	keys := []string{
		"AbsorptionAmount", "Air", "CanPickUpLoot", "CustomName",
		"CustomNameVisible", "DataVersion", "DeathTime", "Dimension",
		"EnderItems", "FallFlying", "Fire", "Glowing", "HasVisualFire",
		"Health", "HurtTime", "Inventory", "Invulnerable", "LeftHanded",
		"Motion", "NoGravity", "OnGround", "Passengers", "PortalCooldown",
		"Pos", "Rotation", "Score", "SelectedItemSlot", "Silent",
		"SleepTimer", "Tags", "TicksFrozen", "UUID", "XpLevel", "XpP",
		"XpSeed", "XpTotal", "abilities", "active_effects",
		"attributes", "current_impulse_context_reset_grace_time",
		"entered_nether_pos", "fall_distance", "foodExhaustionLevel",
		"foodLevel", "foodSaturationLevel", "foodTickTimer",
		"playerGameType", "recipeBook", "respawn", "seenCredits",
		"sleeping_pos", "warden_spawn_tracker",
	}

	present := map[string]map[string]bool{}
	for _, s := range []struct{ label, addr string }{
		{"vanilla", targets.vanilla}, {"mcgo", targets.mcgo},
	} {
		t.Run(s.label, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, s.addr, "PD")
			defer c.Close()
			sendClientInformation(t, pw)
			sink := newChatSink(pr, pw)
			sink.cap = 1 << 20
			time.Sleep(400 * time.Millisecond)
			sendCommand(t, pw, "gamemode survival @s")
			time.Sleep(400 * time.Millisecond)
			hits := map[string]bool{}
			for _, k := range keys {
				line := dataGetLine(t, sink, pw, "@s", k)
				hits[k] = line != ""
			}
			present[s.label] = hits
		})
	}
	if !present["vanilla"]["Health"] {
		t.Fatal("vacuous run — vanilla reported nothing (Health absent)")
	}
	for _, k := range keys {
		if present["vanilla"][k] != present["mcgo"][k] {
			t.Errorf("%s: vanilla=%v mcgo=%v", k,
				present["vanilla"][k], present["mcgo"][k])
		}
	}
}
