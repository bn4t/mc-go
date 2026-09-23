package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// Witch.aiStep drinks: underwater → water_breathing (0.15/tick),
// burning → fire_resistance (0.15), hurt → healing (0.05, +4 health),
// far target → swiftness (0.5). Each drink is a 32-tick
// use-duration potion held in the mainhand; the finish clears the
// slot and applies the contents.
func TestWitchDrinkParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		fireRes, healed, speed bool
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}

		effects := func() string {
			sendCommand(t, pw, "execute as @e[type=minecraft:witch] run data get entity @s active_effects")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
			var sb strings.Builder
			for _, ln := range obs.chats[base:] {
				if strings.Contains(ln, "witch") {
					sb.WriteString(ln)
				}
			}
			return sb.String()
		}
		health := func() float64 {
			sendCommand(t, pw, "data get entity @e[type=minecraft:witch,limit=1] Health")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1500*time.Millisecond)
			for _, ln := range obs.chats[base:] {
				if i := strings.Index(ln, "witch"); i >= 0 {
					rest := ln[i+5:]
					// Trim the reply prefix word boundary: the
					// number starts at the first digit/-.
					j := 0
					for j < len(rest) && !(rest[j] >= '0' && rest[j] <= '9' || rest[j] == '-') {
						j++
					}
					end := j
					for end < len(rest) && strings.IndexByte("0123456789.eE+-", rest[end]) >= 0 {
						end++
					}
					if v, err := strconv.ParseFloat(rest[j:end], 64); err == nil {
						return v
					}
				}
			}
			return -1
		}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 800*time.Millisecond)

		// Burning witch → fire_resistance drink.
		sendCommand(t, pw, "summon minecraft:witch 105.5 -59 0.5 {Fire:100s}")
		fleePump(t, c, pr, pw, obs, 4500*time.Millisecond)
		r.fireRes = strings.Contains(effects(), "fire_resistance")
		sendCommand(t, pw, "kill @e[type=minecraft:witch]")
		fleePump(t, c, pr, pw, obs, 500*time.Millisecond)

		// Hurt witch → healing drink (+4 health back).
		sendCommand(t, pw, "summon minecraft:witch 105.5 -59 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)
		sendCommand(t, pw, "damage @e[type=minecraft:witch,limit=1] 10 minecraft:generic")
		fleePump(t, c, pr, pw, obs, 7000*time.Millisecond)
		r.healed = health() > 17
		sendCommand(t, pw, "kill @e[type=minecraft:witch]")
		fleePump(t, c, pr, pw, obs, 500*time.Millisecond)

		// Survival player 13 blocks out → swiftness drink.
		sendCommand(t, pw, "effect give P minecraft:resistance 60 4")
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "tp P 117.5 -59 0.5")
		sendCommand(t, pw, "summon minecraft:witch 130.5 -59 0.5")
		fleePump(t, c, pr, pw, obs, 5000*time.Millisecond)
		r.speed = strings.Contains(effects(), "speed")
		sendCommand(t, pw, "gamemode creative P")
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if !r.fireRes {
			t.Errorf("%s burning witch never gained fire_resistance", name)
		}
		if !r.healed {
			t.Errorf("%s hurt witch never healed above 17", name)
		}
		if !r.speed {
			t.Errorf("%s far-target witch never gained speed", name)
		}
	}
}
