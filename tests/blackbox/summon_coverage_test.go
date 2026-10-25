package blackbox

// TestSummonCoverageParity pins the non-living /summon surface: all 24
// non-living EntityTypes vanilla's EntityType.create accepts must be
// summonable (EvokerFangs ctor defaults, OminousItemSpawner's
// spawnItemAfterTicks roll, ItemEntity's empty-stack discard — all
// measured against the 26.2 bytecode), and their measured per-tick
// fates match after a 140-tick sprint.
//
//   - acceptance: every type prints commands.summon.success.
//   - presence: persistent types answer an @e[type=] selector probe
//     (short-lived types skip the probe — the tick-1 discards race).
//   - fates: bobber/fangs/eye/ominous_spawner/empty item are gone by
//     140 ticks; mid-air hanging entities pop at the checkInterval-100
//     pass; a fence leash_knot survives; falling_block lands as its
//     BlockState (sand default) and the entity is gone; spectral
//     arrows and tridents stick like arrows; the rest of the thrown
//     set breaks on landing.

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// summonTypes is the measured 26.2 non-living accept set plus the
// arrow control — presence probes run only for the entries whose
// lifetime clears a single tick boundary.
var summonTypes = []struct {
	name    string
	probe   bool // safe to assert presence right after summon
	gone140 bool // asserted absent after the 140-tick sprint
}{
	{"snowball", false, true},
	{"egg", false, true},
	{"ender_pearl", false, true},
	{"experience_bottle", false, true},
	{"splash_potion", false, true},
	{"lingering_potion", false, true},
	{"llama_spit", false, true},
	{"spectral_arrow", false, false}, // sticks in the ground like an arrow
	{"trident", false, false},
	{"item", false, true}, // air stack → ItemEntity.tick discards
	{"block_display", true, false},
	{"item_display", true, false},
	{"text_display", true, false},
	{"interaction", true, false},
	{"end_crystal", true, false},
	{"painting", false, true}, // mid-air → pops at checkInterval 100
	{"item_frame", false, true},
	{"glow_item_frame", false, true},
	{"eye_of_ender", true, true},          // life > 80 → discard
	{"evoker_fangs", true, true},          // warmup 0, life 22 → discard ~23t
	{"ominous_item_spawner", false, true}, // spawnItemAfterTicks=0 → kill tick 1
	{"falling_block", false, true},        // lands as sand
	{"leash_knot", false, true},           // mid-air knot → pops at 100
	{"arrow", false, false},               // control: pre-existing accept
}

func TestSummonCoverageParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSink(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(1400 * time.Millisecond)
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		time.Sleep(800 * time.Millisecond)
		sink.take()

		var out []string
		collect := func() {
			for _, line := range sink.take() {
				for _, marker := range []string{"HAVE-", "GONE-", "PRESENT-", "KNOT-", "SAND@", "summon.success", "entity.not_summonable"} {
					if strings.Contains(line, marker) {
						out = append(out, line)
						break
					}
				}
			}
		}
		// A stone pad gives the falling_block a deterministic landing
		// cell regardless of what the flat world leaves under it.
		sendCommand(t, pw, "setblock 52 -60 20 minecraft:stone")
		// Acceptance + presence: summon each type spread 2 apart at
		// (x, -58.5, 20.5), then probe the whole-type selector for the
		// types that clear a tick boundary.
		x := 8.0
		for _, st := range summonTypes {
			sink.take()
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s %v -58.5 20.5", st.name, x))
			time.Sleep(300 * time.Millisecond)
			if st.probe {
				sendCommand(t, pw, fmt.Sprintf("execute if entity @e[type=minecraft:%s,limit=1] run say HAVE-%s", st.name, st.name))
				time.Sleep(300 * time.Millisecond)
			}
			collect()
			x += 2
		}
		// A fence-post leash_knot survives the checkInterval pass —
		// kept off the summon row (z=26) so it can't prop up a frame.
		sink.take()
		sendCommand(t, pw, "setblock 40 -60 26 minecraft:oak_fence")
		sendCommand(t, pw, "summon minecraft:leash_knot 40.5 -59.4 26.5")
		time.Sleep(300 * time.Millisecond)
		// The NBT-fed summons: a long-fused tnt and a valued xp orb
		// persist past the sprint; a bare area_effect_cloud sits
		// forever (duration -1); a marker is invisible but @e still
		// selects it; a bare mannequin spawns profileless.
		extra := [][2]string{
			{"summon minecraft:tnt 60 -59 26.5 {fuse:30000}", "tnt"},
			{"summon minecraft:experience_orb 62 -59 26.5 {Value:7}", "experience_orb"},
			{"summon minecraft:area_effect_cloud 64 -59 26.5", "area_effect_cloud"},
			{"summon minecraft:marker 66 -59 26.5", "marker"},
			{"summon minecraft:mannequin 68 -59 26.5", "mannequin"},
		}
		for _, ex := range extra {
			sendCommand(t, pw, ex[0])
			time.Sleep(300 * time.Millisecond)
			sendCommand(t, pw, fmt.Sprintf("execute if entity @e[type=minecraft:%s,limit=1] run say HAVE-%s", ex[1], ex[1]))
			time.Sleep(300 * time.Millisecond)
		}
		// EntityType.Builder.noSummon — the only two registered
		// types vanilla refuses, each answered entity.not_summonable.
		sendCommand(t, pw, "summon minecraft:fishing_bobber 70 -59 26.5")
		sendCommand(t, pw, "summon minecraft:player 72 -59 26.5")
		time.Sleep(400 * time.Millisecond)
		collect()

		sprintTicksLoose(t, pw, sink, 140)
		sink.take()

		for _, st := range summonTypes {
			if st.gone140 {
				sendCommand(t, pw, fmt.Sprintf("execute unless entity @e[type=minecraft:%s] run say GONE-%s", st.name, st.name))
			} else {
				sendCommand(t, pw, fmt.Sprintf("execute if entity @e[type=minecraft:%s] run say PRESENT-%s", st.name, st.name))
			}
		}
		for _, n := range []string{"tnt", "experience_orb", "area_effect_cloud", "marker", "mannequin"} {
			sendCommand(t, pw, fmt.Sprintf("execute if entity @e[type=minecraft:%s] run say PRESENT-%s", n, n))
		}
		// Only the fence knot remains — its persistence is survives().
		sendCommand(t, pw, "execute if entity @e[type=minecraft:leash_knot] run say KNOT-FENCE")
		// The falling_block (summon slot x=52) rests on the stone pad
		// placed before the loop: whichever -61..-58 cell reads sand,
		// say it so the legs compare the same marker set.
		for _, y := range []int{-61, -60, -59, -58} {
			sendCommand(t, pw, fmt.Sprintf("execute if block 52 %d 20 minecraft:sand run say SAND@%d", y, y))
		}
		time.Sleep(1500 * time.Millisecond)
		collect()
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	key := func(lines []string) []string {
		var ks []string
		for _, l := range lines {
			for _, marker := range []string{"HAVE-", "GONE-", "PRESENT-", "KNOT-", "SAND@", "summon.success", "entity.not_summonable"} {
				if i := strings.Index(l, marker); i >= 0 {
					end := i + len(marker)
					for end < len(l) && l[end] != '"' && l[end] != ' ' && l[end] != ']' && l[end] != ',' {
						end++
					}
					ks = append(ks, l[i:end])
					break
				}
			}
		}
		return ks
	}
	wk, gk := key(want), key(got)
	// Chat lines reach the two legs in different orders — the parity
	// claim is the marker multiset, not the emission order.
	sort.Strings(wk)
	sort.Strings(gk)
	if fmt.Sprintf("%v", wk) != fmt.Sprintf("%v", gk) {
		t.Errorf("summon coverage diverged:\n  vanilla %v\n  mc-go   %v", wk, gk)
	}
}
