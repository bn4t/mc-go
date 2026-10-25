package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// TestTNTParity covers the TNT loop measured on the 26.2 jar:
// TntBlock.useItemOn primes on flint_and_steel/fire_charge (block
// clears, a PrimedTnt entity spawns), the fuse counts 80 real-time
// ticks, and the explosion runs the 16³ shell-ray sampler with the
// real per-block blast resistance — DESTROY_WITH_DECAY drops at
// 1/radius, cells write air with flag 3, and TNT cells chain-prime
// with a 10..29 tick fuse. tnt_explodes=false leaves the block
// standing.
func TestTNTParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P", "Q"),
	}, nil)
	defer targets.cleanup()

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZB-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZB-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZBK%d", i))
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZBK%d", i)) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZB-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZB-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		cq, _, _ := joinPlayQuiet(t, addr, "Q")
		defer cq.Close()
		_ = cq.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)

		var results []string
		mark := func(name string, got string) { results = append(results, name+"="+got) }

		sendCommand(t, pw, "gamemode creative @s")
		// Ground pad + blast layout along x.
		sendCommand(t, pw, "fill 96 -60 58 106 -60 62 stone")
		sendCommand(t, pw, "fill 96 -60 68 106 -60 72 stone")
		sendCommand(t, pw, "fill 96 -60 74 106 -60 78 stone")
		sendCommand(t, pw, "tp @s 100.5 -59 61.5")
		sendCommand(t, pw, "tp Q 105.5 -59 60.5")
		sendCommand(t, pw, "gamemode survival Q")
		time.Sleep(400 * time.Millisecond)

		// Leg A: prime by flint_and_steel.
		sendCommand(t, pw, "setblock 98 -59 60 dirt")
		sendCommand(t, pw, "setblock 99 -59 60 dirt")
		sendCommand(t, pw, "setblock 100 -59 60 tnt")
		sendCommand(t, pw, "setblock 104 -59 60 obsidian")
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:flint_and_steel 1")
		time.Sleep(300 * time.Millisecond)
		sendUseItemOn(t, pw, 100, -59, 60, 1, 1)
		time.Sleep(400 * time.Millisecond)
		mark("primed_cell_air", probe(pw, sink, "block 100 -59 60 minecraft:air", 0))
		mark("primed_entity", probe(pw, sink, "entity @e[type=tnt,x=100,y=-59,z=60,distance=..3]", 1))

		// Fuse is 80 ticks ≈ 4s; wait for the blast.
		time.Sleep(4400 * time.Millisecond)
		mark("blast_cell_air", probe(pw, sink, "block 100 -59 60 minecraft:air", 3))
		mark("dirt_a_gone", probe(pw, sink, "block 98 -59 60 minecraft:air", 4))
		mark("dirt_b_gone", probe(pw, sink, "block 99 -59 60 minecraft:air", 5))
		mark("obsidian_holds", probe(pw, sink, "block 104 -59 60 minecraft:obsidian", 6))
		mark("drops_spawned", probe(pw, sink, "entity @e[type=item,x=99,y=-59,z=60,distance=..8]", 7))
		mark("tnt_gone", probe(pw, sink, "entity @e[type=tnt,x=100,y=-59,z=60,distance=..8]", 8))
		// Q stood in survival 5 blocks from the centre: impact
		// 1-5/8=0.375 → damage ((i²+i)/2)·7·4+1 ≈ 8.2 → health ≈ 11.8,
		// stored as ×100. Both bounds pin the damage formula.
		sendCommand(t, pw, "scoreboard objectives add bbh dummy")
		sendCommand(t, pw, "execute store result score Q bbh run data get entity Q Health 100")
		time.Sleep(300 * time.Millisecond)
		mark("health_in_band", probe(pw, sink, "score Q bbh matches 1100..1250", 9))
		mark("health_not_low", probe(pw, sink, "score Q bbh matches ..1099", 10))
		mark("health_not_high", probe(pw, sink, "score Q bbh matches 1251..", 11))

		// Leg B: chain prime — two adjacent TNTs, light the first. P
		// must move in reach: vanilla's use gate is eye->block-AABB
		// under 5.5.
		sendCommand(t, pw, "tp @s 101.5 -59 71.5")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw, "setblock 100 -59 70 tnt")
		sendCommand(t, pw, "setblock 102 -59 70 tnt")
		time.Sleep(200 * time.Millisecond)
		sendUseItemOn(t, pw, 100, -59, 70, 1, 2)
		time.Sleep(400 * time.Millisecond)
		mark("chain_prime_cell", probe(pw, sink, "block 100 -59 70 minecraft:air", 12))
		// 4s fuse + up to ~1.5s short fuse + margin.
		time.Sleep(6200 * time.Millisecond)
		mark("chain_first_air", probe(pw, sink, "block 100 -59 70 minecraft:air", 13))
		mark("chain_second_air", probe(pw, sink, "block 102 -59 70 minecraft:air", 14))
		mark("chain_no_tnt", probe(pw, sink, "entity @e[type=tnt,x=101,y=-59,z=70,distance=..10]", 15))

		// Leg C: tnt_explodes=false — the block stays, no entity.
		sendCommand(t, pw, "tp @s 101.5 -59 77.5")
		time.Sleep(300 * time.Millisecond)
		sendCommand(t, pw, "gamerule tnt_explodes false")
		time.Sleep(200 * time.Millisecond)
		sendCommand(t, pw, "setblock 100 -59 76 tnt")
		sendUseItemOn(t, pw, 100, -59, 76, 1, 3)
		time.Sleep(500 * time.Millisecond)
		mark("off_block_stays", probe(pw, sink, "block 100 -59 76 minecraft:tnt", 16))
		mark("off_no_entity", probe(pw, sink, "entity @e[type=tnt,x=100,y=-59,z=76,distance=..3]", 17))
		sendCommand(t, pw, "gamerule tnt_explodes true")

		return results
	}

	va := run(targets.vanilla)
	mc := run(targets.mcgo)
	if len(va) != len(mc) {
		t.Fatalf("leg counts differ: vanilla=%v mcgo=%v", va, mc)
	}
	for i := range va {
		if va[i] != mc[i] {
			t.Errorf("leg %d differs: vanilla %q vs mc-go %q", i, va[i], mc[i])
		}
	}
}
