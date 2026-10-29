package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestFireIgniteParity covers the igniter-item slice measured on the
// 26.2 jar: FlintAndSteelItem/FireChargeItem.useOn light campfires,
// candles and candle cakes (lit=true, tag-gated), and otherwise write
// BaseFireBlock.getState at clicked+face — soul_fire over
// #soul_fire_base_blocks, direction-folded fire otherwise — gated by
// canBePlacedAt (air + canSurvive-or-portal). A fresh fire inside an
// obsidian frame runs PortalShape.findEmptyPortalShape/createPortalBlocks,
// and tnt_explodes=false falls through to useOn so flint still lays
// fire next to the TNT.
func TestFireIgniteParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
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
		sink := newChatSinkTP(pr, pw)
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}

		var results []string
		mark := func(name, got string) { results = append(results, name+"="+got) }

		sendCommand(t, pw, "gamemode creative @s")
		// Vanilla's /fill refuses to write unloaded chunks: move the
		// player into the test area first so the pads actually exist.
		sendCommand(t, pw, "tp @s 96.5 -59 81.5")
		sendCommand(t, pw, "forceload add 92 78 126 96")
		loaded := false
		deadline := time.Now().Add(10 * time.Second)
		for attempt := 0; time.Now().Before(deadline) && !loaded; attempt++ {
			ready := fmt.Sprintf("FIRECHUNKSREADY%dX", attempt)
			end := fmt.Sprintf("FIRECHUNKSEND%dX", attempt)
			condition := "execute"
			for cx := 5; cx <= 7; cx++ {
				for cz := 4; cz <= 6; cz++ {
					condition += fmt.Sprintf(" if loaded %d -60 %d", cx*16, cz*16)
				}
			}
			sendCommand(t, pw, condition+" run say "+ready)
			sendCommand(t, pw, "say "+end)
			done := false
			for time.Now().Before(deadline) && !done {
				for _, line := range sink.take() {
					if structureSaidBB([]string{line}, ready) {
						loaded = true
					}
					if structureSaidBB([]string{line}, end) {
						done = true
					}
					if strings.HasPrefix(line, "0x79:") {
						t.Logf("%s fire setup: %q", addr, line)
					}
				}
				if !done {
					time.Sleep(10 * time.Millisecond)
				}
			}
		}
		if !loaded {
			t.Fatalf("%s fire setup chunks did not load", addr)
		}
		// Flat pads: z=80..84 sturdy stone; the torch cells break the
		// floor's sturdiness for the face-prop and fail legs.
		sendCommand(t, pw, "fill 92 -60 78 126 -60 96 stone")
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:flint_and_steel 1")
		time.Sleep(400 * time.Millisecond)
		pad := probe(pw, sink, "block 96 -60 80 minecraft:stone", 30)
		mark("pad_placed", pad)
		if pad != "hit" {
			t.Fatalf("%s fire setup pad missing after loaded barrier: %s", addr, pad)
		}

		seq := int32(1)
		click := func(x, y, z, face int32) {
			sendUseItemOn(t, pw, x, y, z, face, seq)
			seq++
		}

		// Leg A: plain fire on a sturdy floor — click the pad's top
		// face so the target is the air cell above it.
		sendCommand(t, pw, "tp @s 96.5 -59 81.5")
		time.Sleep(800 * time.Millisecond)
		click(96, -60, 80, 1)
		time.Sleep(300 * time.Millisecond)
		mark("plain_fire", probe(pw, sink, "block 96 -59 80 minecraft:fire", 0))

		// Leg B: soul base picks soul_fire.
		sendCommand(t, pw, "setblock 98 -60 80 soul_sand")
		sendCommand(t, pw, "tp @s 98.5 -59 81.5")
		time.Sleep(250 * time.Millisecond)
		click(98, -60, 80, 1)
		time.Sleep(300 * time.Millisecond)
		mark("soul_fire", probe(pw, sink, "block 98 -59 80 minecraft:soul_fire", 1))

		// Leg C: flammable neighbor, non-sturdy floor — the placement
		// state folds the neighbor's direction prop (south=+z).
		sendCommand(t, pw, "setblock 100 -60 82 torch")
		sendCommand(t, pw, "setblock 100 -59 83 oak_planks")
		time.Sleep(250 * time.Millisecond)
		click(100, -60, 82, 1)
		time.Sleep(300 * time.Millisecond)
		mark("face_prop", probe(pw, sink, "block 100 -59 82 minecraft:fire[south=true]", 2))

		// Leg D: nothing sturdy below, nothing burnable adjacent —
		// canBePlacedAt fails, the cell stays air.
		sendCommand(t, pw, "setblock 98 -60 84 torch")
		sendCommand(t, pw, "tp @s 98.5 -59 85.5")
		time.Sleep(250 * time.Millisecond)
		click(98, -60, 84, 1)
		time.Sleep(300 * time.Millisecond)
		mark("fail_no_fire", probe(pw, sink, "block 98 -59 84 minecraft:air", 3))

		// Leg E/F: light branch — unlit campfire and candle go lit,
		// clicked block itself.
		sendCommand(t, pw, "setblock 102 -59 80 campfire[lit=false]")
		sendCommand(t, pw, "setblock 104 -59 80 candle[lit=false]")
		sendCommand(t, pw, "tp @s 103.5 -59 81.5")
		time.Sleep(250 * time.Millisecond)
		click(102, -59, 80, 1)
		click(104, -59, 80, 1)
		time.Sleep(400 * time.Millisecond)
		mark("campfire_lit", probe(pw, sink, "block 102 -59 80 minecraft:campfire[lit=true]", 4))
		mark("candle_lit", probe(pw, sink, "block 104 -59 80 minecraft:candle[lit=true]", 5))

		// Leg G: fire charge places fire and shrinks even-1 stack to
		// empty (survival). shrink is unconditional on vanilla — this
		// leg only needs the block probe.
		sendCommand(t, pw, "tp @s 106.5 -59 81.5")
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:fire_charge 1")
		time.Sleep(250 * time.Millisecond)
		click(106, -60, 80, 1)
		time.Sleep(300 * time.Millisecond)
		mark("charge_fire", probe(pw, sink, "block 106 -59 80 minecraft:fire", 6))
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:flint_and_steel 1")

		// Leg H: nether portal lighting, X axis. 4x5 obsidian wall in
		// the x direction, 2x3 air interior.
		sendCommand(t, pw, "fill 110 -60 90 113 -56 90 obsidian")
		sendCommand(t, pw, "fill 111 -59 90 112 -57 90 air")
		sendCommand(t, pw, "tp @s 111.5 -59 88.5")
		time.Sleep(300 * time.Millisecond)
		click(111, -60, 90, 1)
		time.Sleep(400 * time.Millisecond)
		mark("portal_x_low", probe(pw, sink, "block 111 -59 90 minecraft:nether_portal[axis=x]", 7))
		mark("portal_x_high", probe(pw, sink, "block 112 -57 90 minecraft:nether_portal[axis=x]", 8))
		mark("portal_frame", probe(pw, sink, "block 110 -59 90 minecraft:obsidian", 9))

		// Leg I: same frame in the z plane -> axis=z.
		sendCommand(t, pw, "fill 120 -60 92 120 -56 95 obsidian")
		sendCommand(t, pw, "fill 120 -59 93 120 -57 94 air")
		sendCommand(t, pw, "tp @s 119.5 -59 93.5")
		time.Sleep(300 * time.Millisecond)
		click(120, -60, 93, 1)
		time.Sleep(400 * time.Millisecond)
		mark("portal_z_low", probe(pw, sink, "block 120 -59 93 minecraft:nether_portal[axis=z]", 10))
		mark("portal_z_high", probe(pw, sink, "block 120 -57 94 minecraft:nether_portal[axis=z]", 11))

		// Leg J: tnt_explodes=false — the TNT use returns PASS, so
		// flint's useOn still lays fire above the block.
		sendCommand(t, pw, "gamerule tnt_explodes false")
		sendCommand(t, pw, "setblock 96 -59 86 tnt")
		sendCommand(t, pw, "tp @s 96.5 -59 87.5")
		time.Sleep(300 * time.Millisecond)
		click(96, -59, 86, 1)
		time.Sleep(400 * time.Millisecond)
		mark("tnt_off_fire", probe(pw, sink, "block 96 -58 86 minecraft:fire", 12))
		mark("tnt_off_stays", probe(pw, sink, "block 96 -59 86 minecraft:tnt", 13))
		sendCommand(t, pw, "gamerule tnt_explodes true")
		// Clean the fire so its block tick can't matter later.
		sendCommand(t, pw, "setblock 96 -58 86 air")
		sendCommand(t, pw, "setblock 96 -59 86 air")

		return results
	}

	va := run(targets.vanilla)
	mc := run(targets.mcgo)
	if len(va) != len(mc) {
		t.Fatalf("leg counts differ: vanilla=%v mcgo=%v", va, mc)
	}
	bad := 0
	for i := range va {
		if va[i] != mc[i] {
			t.Logf("leg %d diverged: vanilla=%s mcgo=%s", i, va[i], mc[i])
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d legs diverged", bad)
	}
}
