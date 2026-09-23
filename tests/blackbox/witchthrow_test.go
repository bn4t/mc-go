package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// Witch.performRangedAttack throws a splash_potion every 60 ticks at
// a target inside ten blocks: slowness past eight blocks (no slowness
// yet), poison against a healthy target, a 25% weakness roll inside
// three, harming otherwise. The splash lands the scaled contents on
// everything within four blocks — the survival player reads them
// back through `data get entity @s active_effects` / Health.
func TestWitchThrowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		slowness, poison, harmed bool
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}

		myEffects := func() string {
			sendCommand(t, pw, "data get entity @s active_effects")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
			var sb strings.Builder
			for _, ln := range obs.chats[base:] {
				sb.WriteString(ln)
			}
			return sb.String()
		}
		myHealth := func() float64 {
			sendCommand(t, pw, "data get entity @s Health")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
			for _, ln := range obs.chats[base:] {
				if !strings.Contains(ln, "player") && !strings.Contains(ln, "P") {
					continue
				}
				j := strings.Index(ln, "query")
				if j < 0 {
					continue
				}
				rest := ln[j+5:]
				k := 0
				for k < len(rest) && !(rest[k] >= '0' && rest[k] <= '9' || rest[k] == '-') {
					k++
				}
				end := k
				for end < len(rest) && strings.IndexByte("0123456789.eE+-", rest[end]) >= 0 {
					end++
				}
				if v, err := strconv.ParseFloat(rest[k:end], 64); err == nil {
					return v
				}
			}
			return -1
		}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		sendCommand(t, pw, "effect give P minecraft:resistance 120 3")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)

		// Slowness leg: twelve blocks out — the witch closes to ~10
		// and throws slowness.
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "summon minecraft:witch 112.5 -59 0.5")
		fleePump(t, c, pr, pw, obs, 6000*time.Millisecond)
		r.slowness = strings.Contains(myEffects(), "slowness")

		// Poison leg: full health at six blocks → poison.
		sendCommand(t, pw, "effect clear P")
		sendCommand(t, pw, "effect give P minecraft:resistance 120 3")
		sendCommand(t, pw, "kill @e[type=minecraft:witch]")
		sendCommand(t, pw, "tp P 105.5 -59 0.5")
		sendCommand(t, pw, "summon minecraft:witch 111.5 -59 0.5")
		fleePump(t, c, pr, pw, obs, 6000*time.Millisecond)
		r.poison = strings.Contains(myEffects(), "poison")

		// Harming leg: under eight health → harming. Resistance would
		// eat both the `damage` amount and the splash, so clear it
		// first; 14 real damage leaves 6 hearts and ~6*proximity of
		// magic either lands a sub-6 read or kills outright (the
		// respawn reads back 20 — count the death broadcast too).
		sendCommand(t, pw, "effect clear P")
		sendCommand(t, pw, "damage P 14 minecraft:generic")
		fleePump(t, c, pr, pw, obs, 300*time.Millisecond)
		preHit := myHealth()
		t.Logf("%s post-damage health %.1f", addr, preHit)
		deathBase := len(obs.chats)
		fleePump(t, c, pr, pw, obs, 5500*time.Millisecond)
		diedToMagic := false
		for _, ln := range obs.chats[deathBase:] {
			if strings.Contains(ln, "indirectMagic") || strings.Contains(ln, "magic") {
				diedToMagic = true
			}
		}
		h := myHealth()
		r.harmed = diedToMagic || h < 6.5
		if !r.harmed {
			fleePump(t, c, pr, pw, obs, 2500*time.Millisecond)
			r.harmed = myHealth() < 6.5
		}
		sendCommand(t, pw, "gamemode creative P")
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if !r.slowness {
			t.Errorf("%s witch never threw slowness at a 12-block target", name)
		}
		if !r.poison {
			t.Errorf("%s witch never threw poison at a healthy 6-block target", name)
		}
		if !r.harmed {
			t.Errorf("%s witch never harmed a 6-health target", name)
		}
	}
}
