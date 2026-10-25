// TestPortalAgeParity pins the per-tick carries of Entity.baseTick
// and AgeableMob.aiStep: portalCooldown decrements one per tick on
// every ticking entity (mobs and non-living alike) and never goes
// negative; a baby's Age ticks up toward 0 only while canAgeUp
// (AgeLocked freezes it); ForcedAge is a reservoir that does not
// tick at all.
package blackbox

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

type portalAgeLeg struct {
	zombieStart, zombieAfter float64
	cartStart, cartAfter     float64
	lockedAfter              float64
	unlockedAfter            float64
	forcedBefore, forcedEnd  float64
	mergeAfter               float64
}

func TestPortalAgeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) portalAgeLeg {
		var res portalAgeLeg
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSink(pr, pw)
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "gamerule do_mob_spawning false")
		sendCommand(t, pw, "tp P 40.5 -60 40.5")
		sendCommand(t, pw, "fill 30 -61 30 60 -61 60 minecraft:stone")

		// /tick sprint is async on vanilla — later commands
		// interleave mid-sprint, so wait for the completion
		// report before reading the counters.
		sprint := func(n int) {
			t.Helper()
			sendCommand(t, pw, "tick sprint "+strconv.Itoa(n))
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, "tick.sprint.report") {
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
			t.Fatalf("sprint %d report never arrived", n)
		}
		get := func(sel, path string) float64 {
			t.Helper()
			// dataGetSel parses via the unsigned lastNumber —
			// Age needs the sign, so read the line raw.
			line := dataGetLine(t, sink, pw, sel, path)
			return lastNumberSigned(line)
		}
		kill := func() {
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			time.Sleep(250 * time.Millisecond)
		}

		// A summoned zombie's PortalCooldown parses and counts down.
		kill()
		sendCommand(t, pw,
			"summon minecraft:zombie 45.5 -60 45.5 {NoAI:1b,PortalCooldown:400}")
		time.Sleep(300 * time.Millisecond)
		zsel := "@e[type=minecraft:zombie,limit=1]"
		res.zombieStart = get(zsel, "PortalCooldown")
		sprint(50)
		res.zombieAfter = get(zsel, "PortalCooldown")

		// A merged value counts down too, and clamps at 0.
		sendCommand(t, pw,
			"data merge entity "+zsel+" {PortalCooldown:10}")
		sprint(30)
		res.mergeAfter = get(zsel, "PortalCooldown")
		kill()

		// Non-living entities tick the same counter (a chest
		// minecart is not a LivingEntity).
		sendCommand(t, pw,
			"summon minecraft:chest_minecart 45.5 -60 45.5 {PortalCooldown:123}")
		time.Sleep(300 * time.Millisecond)
		csel := "@e[type=minecraft:chest_minecart,limit=1]"
		res.cartStart = get(csel, "PortalCooldown")
		sprint(20)
		res.cartAfter = get(csel, "PortalCooldown")
		kill()

		// AgeLocked babies never grow; unlocked babies count up.
		sendCommand(t, pw,
			"summon minecraft:pig 45.5 -60 45.5 {NoAI:1b,Age:-400,AgeLocked:1b}")
		time.Sleep(300 * time.Millisecond)
		sprint(100)
		res.lockedAfter = get("@e[type=minecraft:pig,limit=1]", "Age")
		kill()
		sendCommand(t, pw,
			"summon minecraft:pig 45.5 -60 45.5 {NoAI:1b,Age:-400}")
		time.Sleep(300 * time.Millisecond)
		sprint(100)
		res.unlockedAfter = get("@e[type=minecraft:pig,limit=1]", "Age")
		kill()

		// ForcedAge is a reservoir — it never ticks on its own.
		sendCommand(t, pw,
			"summon minecraft:pig 45.5 -60 45.5 {NoAI:1b,ForcedAge:700}")
		time.Sleep(300 * time.Millisecond)
		psel := "@e[type=minecraft:pig,limit=1]"
		res.forcedBefore = get(psel, "ForcedAge")
		sprint(50)
		res.forcedEnd = get(psel, "ForcedAge")
		kill()
		return res
	}

	van := run(targets.vanilla)
	t.Logf("vanilla: %+v", van)
	mc := run(targets.mcgo)
	t.Logf("mc-go:   %+v", mc)

	// Command-queue latency shifts each read by a few ticks per
	// server, so compare legs within a tolerance rather than
	// equality — the tick rates themselves are exact.
	close := func(a, b float64) bool { return math.Abs(a-b) <= 10 }
	if !close(van.zombieStart, mc.zombieStart) ||
		!close(van.zombieAfter, mc.zombieAfter) ||
		van.mergeAfter != mc.mergeAfter ||
		!close(van.cartStart, mc.cartStart) ||
		!close(van.cartAfter, mc.cartAfter) ||
		van.lockedAfter != mc.lockedAfter ||
		!close(van.unlockedAfter, mc.unlockedAfter) ||
		van.forcedBefore != mc.forcedBefore ||
		van.forcedEnd != mc.forcedEnd {
		t.Errorf("leg mismatch: vanilla %+v vs mc-go %+v", van, mc)
	}
	// Invariants each leg must satisfy regardless of queue slop.
	if van.zombieStart < 380 || van.zombieStart > 400 {
		t.Errorf("vanilla zombie start %v outside [380,400]", van.zombieStart)
	}
	if d := van.zombieStart - van.zombieAfter; d < 45 || d > 75 {
		t.Errorf("vanilla zombie decrement %v outside [45,75]", d)
	}
	if van.mergeAfter != 0 {
		t.Errorf("vanilla merge clamp %v != 0", van.mergeAfter)
	}
	if van.cartStart < 105 || van.cartStart > 123 {
		t.Errorf("vanilla cart start %v outside [105,123]", van.cartStart)
	}
	if d := van.cartStart - van.cartAfter; d < 15 || d > 45 {
		t.Errorf("vanilla cart decrement %v outside [15,45]", d)
	}
	if van.lockedAfter != -400 {
		t.Errorf("vanilla locked pig %v != -400", van.lockedAfter)
	}
	if van.unlockedAfter < -320 || van.unlockedAfter > -278 {
		t.Errorf("vanilla unlocked pig %v outside [-320,-278]",
			van.unlockedAfter)
	}
	if van.forcedBefore != 700 || van.forcedEnd != 700 {
		t.Errorf("vanilla ForcedAge moved: %v -> %v",
			van.forcedBefore, van.forcedEnd)
	}
}
