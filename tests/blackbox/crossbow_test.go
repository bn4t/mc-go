package blackbox

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// RangedCrossbowAttackGoal / PiglinAi's CrossbowAttack: a crossbow-
// armed pillager or adult piglin closes to its 8-block radius,
// charges 25 ticks, holds 20-40, then fires a speed-1.6 arrow at the
// target. Pinned: a survival player inside range takes arrow damage
// on both servers. Piglins only hunt players not wearing gold.
func TestCrossbowShootParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	type result struct {
		pillagerHit, piglinHit bool
	}
	run := func(addr string) result {
		var r result
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(120 * time.Second))
		obs := &fleeObs{}

		myHealth := func() float64 {
			sendCommand(t, pw, "data get entity @s Health")
			base := len(obs.chats)
			fleePump(t, c, pr, pw, obs, 1200*time.Millisecond)
			for _, ln := range obs.chats[base:] {
				if !strings.Contains(ln, "P") {
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
		waitForHit := func() bool {
			// Two volleys is enough: charge+delay ≈ 45-65t each.
			for i := 0; i < 12; i++ {
				fleePump(t, c, pr, pw, obs, 700*time.Millisecond)
				if h := myHealth(); h >= 0 && h < 20 {
					return true
				}
			}
			return false
		}

		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		fleePump(t, c, pr, pw, obs, 600*time.Millisecond)

		// Pillager leg — spawns crossbow-armed, hostile at any range
		// inside follow_range 32.
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "summon minecraft:pillager 108.5 -59 0.5")
		r.pillagerHit = waitForHit()
		t.Logf("%s pillager health-read %v", addr, myHealth())

		// Piglin leg — crossbow-armed adult, player still bare. Keep
		// the leg under 15s: overworld zombification starts at 300t.
		sendCommand(t, pw, "gamemode creative P")
		sendCommand(t, pw, "effect clear P")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "tp P 100.5 -60 0.5")
		sendCommand(t, pw, "summon minecraft:piglin 107.5 -59 0.5 {IsImmuneToZombification:1b,equipment:{mainhand:{id:\"minecraft:crossbow\",count:1}}}")
		fleePump(t, c, pr, pw, obs, 400*time.Millisecond)
		sendCommand(t, pw, "gamemode survival P")
		r.piglinHit = waitForHit()
		sendCommand(t, pw, "gamemode creative P")
		return r
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("van %+v", van)
	t.Logf("mc  %+v", mc)
	for name, r := range map[string]result{"vanilla": van, "mc-go": mc} {
		if !r.pillagerHit {
			t.Errorf("%s crossbow pillager never hit an 8-block survival target", name)
		}
		if !r.piglinHit {
			t.Errorf("%s crossbow piglin never shot a goldless survival target", name)
		}
	}
}

// TestShotCrossbowParity fires adventure/ol_betsy on both servers:
// Use Item charges the crossbow for the vanilla 25-tick hold, the
// release arms it, and the second use fires — shot_crossbow grants
// with the crossbow itself as the condition item. A summoned
// pillager then dies to crossbow bolts for whos_the_pillager_now
// (killed_by_arrow with fired_from_weapon=crossbow).
func TestShotCrossbowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("XBow"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "XBow")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		sendCommand(t, pw, "gamerule doMobSpawning false")
		sendCommand(t, pw, "kill @e[type=!player]")
		sendCommand(t, pw, "tp @s 4 -60 2 0 0")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:crossbow")
		sendCommand(t, pw, "summon minecraft:pillager 4 -60 14 {NoAI:1b}")
		advCollect(t, c, pr, pw, 700*time.Millisecond, f)

		use := func() {
			var ub protocol.Buffer
			ub.VarInt(0) // main hand
			ub.VarInt(0) // sequence
			ub.Float(0)
			ub.Float(0)
			_ = pw.WritePacket(server.IDPlayUseItemSB, ub.Bytes())
		}
		release := func() {
			var ab protocol.Buffer
			ab.VarInt(5) // RELEASE_USE_ITEM
			ab.Position(0, 0, 0)
			ab.Byte(0)
			ab.VarInt(0)
			_ = pw.WritePacket(server.IDPlayPlayerActionSB, ab.Bytes())
		}
		// Each shot: charge ≥25t, release to arm, use to fire. Seven
		// arrows at ~7 damage cover the pillager's 24 health with
		// margin for aim jitter on both targets.
		for i := 0; i < 7; i++ {
			use()
			advCollect(t, c, pr, pw, 1350*time.Millisecond, f)
			release()
			advCollect(t, c, pr, pw, 150*time.Millisecond, f)
			use()
			advCollect(t, c, pr, pw, 400*time.Millisecond, f)
		}
		advCollect(t, c, pr, pw, 1200*time.Millisecond, f)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for _, tc := range [][2]string{
		{"minecraft:adventure/ol_betsy", "shot_crossbow"},
		{"minecraft:adventure/whos_the_pillager_now", "kill_pillager"},
	} {
		if !want.prog[tc[0]][tc[1]] {
			t.Fatalf("vanilla: %s[%s] not obtained: %v", tc[0], tc[1], want.prog[tc[0]])
		}
	}
	want.chats, got.chats = nil, nil
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("shot_crossbow parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
