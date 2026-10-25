package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestNeighborFoldParity covers the second updateShape fold family
// measured on the 26.2 jar: SnowyBlock's snowy prop flips when the
// cell above becomes/loses a #snow block; ChorusPlantBlock refolds
// only the changed direction's prop (chorus/flower, plus
// #supports_chorus_plant for down); ScaffoldingBlock schedules its
// 1-tick distance/bottom recompute (distance 7 drops the block, a
// persistent 7 falls); CoralBlock schedules its 60+rng(40) die tick
// when no water touches any face; ConcretePowderBlock solidifies on
// any water-fluid neighbour; TripWireBlock refolds the changed side
// toward wires and back-facing hooks; TripWireHookBlock pops when its
// anchor side breaks and calculateState attaches a contiguous run to
// an opposite-facing hook.
func TestNeighborFoldParity(t *testing.T) {
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

	// probeRetry is probe for folds whose landing time jitters —
	// a miss retries once after a beat so a slow tick doesn't flake
	// the leg.
	probeRetry := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		if got := probe(pw, sink, spec, i); got == "hit" {
			return got
		}
		time.Sleep(1200 * time.Millisecond)
		return probe(pw, sink, spec, i+900)
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, "fill 90 -59 70 120 -55 145 air")
		time.Sleep(400 * time.Millisecond)

		var results []string
		mark := func(name, got string) { results = append(results, name+"="+got) }

		// Snowy fold: snow above a grass_block sets snowy; breaking
		// it clears; placing under snow arrives snowy.
		sendCommand(t, pw, "setblock 96 -59 120 grass_block")
		sendCommand(t, pw, "setblock 96 -58 120 snow")
		time.Sleep(300 * time.Millisecond)
		mark("snowy_on", probeRetry(pw, sink, "block 96 -59 120 minecraft:grass_block[snowy=true]", 10))
		sendCommand(t, pw, "setblock 96 -58 120 air")
		time.Sleep(300 * time.Millisecond)
		mark("snowy_off", probeRetry(pw, sink, "block 96 -59 120 minecraft:grass_block[snowy=false]", 11))
		sendCommand(t, pw, "setblock 97 -58 120 snow_block")
		sendCommand(t, pw, "setblock 97 -59 120 podzol")
		time.Sleep(300 * time.Millisecond)
		mark("snowy_place_fold", probe(pw, sink, "block 97 -59 120 minecraft:podzol[snowy=true]", 12))

		// Chorus per-direction fold: a plant on end stone has
		// down=true; a flower planted above sets up=true, and the
		// flower's break clears it.
		sendCommand(t, pw, "setblock 96 -60 122 end_stone")
		sendCommand(t, pw, "setblock 96 -59 122 chorus_plant")
		time.Sleep(400 * time.Millisecond)
		mark("chorus_down", probe(pw, sink, "block 96 -59 122 minecraft:chorus_plant[down=true]", 13))
		mark("chorus_up_empty", probe(pw, sink, "block 96 -59 122 minecraft:chorus_plant[up=false]", 14))
		sendCommand(t, pw, "setblock 96 -58 122 chorus_flower")
		time.Sleep(400 * time.Millisecond)
		mark("chorus_up_flower", probe(pw, sink, "block 96 -59 122 minecraft:chorus_plant[up=true]", 15))
		sendCommand(t, pw, "setblock 96 -58 122 air")
		time.Sleep(400 * time.Millisecond)
		mark("chorus_up_clear", probe(pw, sink, "block 96 -59 122 minecraft:chorus_plant[up=false]", 16))

		// Scaffolding distance fold: the base sits on the flatworld
		// grass at y=-61 (first air cell is -60); tower cells share
		// the below-scaffold distance, horizontal cells hang at
		// distance+1, and the seventh out (distance 7) falls and
		// re-lands one row below.
		sendCommand(t, pw, "setblock 96 -60 124 scaffolding")
		time.Sleep(400 * time.Millisecond)
		mark("scaff_ground", probe(pw, sink, "block 96 -60 124 minecraft:scaffolding[distance=0]", 17))
		sendCommand(t, pw, "setblock 96 -59 124 scaffolding")
		time.Sleep(400 * time.Millisecond)
		mark("scaff_tower", probe(pw, sink, "block 96 -59 124 minecraft:scaffolding[distance=0,bottom=false]", 18))
		sendCommand(t, pw, "setblock 97 -59 124 scaffolding")
		sendCommand(t, pw, "setblock 98 -59 124 scaffolding")
		time.Sleep(500 * time.Millisecond)
		mark("scaff_hang1", probe(pw, sink, "block 97 -59 124 minecraft:scaffolding[distance=1,bottom=true]", 19))
		mark("scaff_hang2", probe(pw, sink, "block 98 -59 124 minecraft:scaffolding[distance=2]", 20))
		for x := 99; x <= 102; x++ {
			sendCommand(t, pw, fmt.Sprintf("setblock %d -59 124 scaffolding", x))
		}
		time.Sleep(500 * time.Millisecond)
		mark("scaff_hang6", probe(pw, sink, "block 102 -59 124 minecraft:scaffolding[distance=6]", 21))
		sendCommand(t, pw, "setblock 103 -59 124 scaffolding")
		time.Sleep(800 * time.Millisecond)
		mark("scaff_fall", probe(pw, sink, "block 103 -59 124 minecraft:air", 22))
		mark("scaff_landed", probe(pw, sink, "block 103 -60 124 minecraft:scaffolding", 23))

		// Coral die tick: dry coral blocks schedule 60+rng(40) and
		// become the dead_ variant; scanForWater counts a waterlogged
		// neighbour's fluid state, and a waterlogged slab can't flow
		// toward the dry cell.
		sendCommand(t, pw, "setblock 96 -59 126 tube_coral_block")
		sendCommand(t, pw, "setblock 97 -59 136 brain_coral_block")
		sendCommand(t, pw, "setblock 98 -59 136 stone_slab[waterlogged=true]")
		time.Sleep(8 * time.Second)
		mark("coral_die", probe(pw, sink, "block 96 -59 126 minecraft:dead_tube_coral_block", 24))
		mark("coral_live", probe(pw, sink, "block 97 -59 136 minecraft:brain_coral_block", 25))

		// Concrete powder solidifies when a water-fluid neighbour
		// arrives on a side or above — touchesLiquid skips the cell
		// below unless the powder itself holds water, so water at
		// y-1 leaves it powder.
		sendCommand(t, pw, "setblock 96 -59 128 red_concrete_powder")
		sendCommand(t, pw, "setblock 97 -59 128 water")
		time.Sleep(400 * time.Millisecond)
		mark("powder_solid", probe(pw, sink, "block 96 -59 128 minecraft:red_concrete", 26))
		sendCommand(t, pw, "setblock 96 -60 130 red_concrete_powder")
		sendCommand(t, pw, "setblock 97 -60 130 stone")
		sendCommand(t, pw, "setblock 96 -61 130 water")
		time.Sleep(400 * time.Millisecond)
		mark("powder_below", probe(pw, sink, "block 96 -60 130 minecraft:red_concrete_powder", 36))

		// Tripwire fold: a wire connects to a wire and to a hook
		// facing back down the run; a sideways hook does not connect.
		sendCommand(t, pw, "setblock 95 -59 130 stone")
		sendCommand(t, pw, "setblock 99 -59 130 stone")
		sendCommand(t, pw, "setblock 96 -59 130 tripwire_hook[facing=east]")
		sendCommand(t, pw, "setblock 98 -59 130 tripwire_hook[facing=west]")
		sendCommand(t, pw, "setblock 97 -59 130 tripwire")
		time.Sleep(400 * time.Millisecond)
		mark("wire_east", probe(pw, sink, "block 97 -59 130 minecraft:tripwire[east=true]", 27))
		mark("wire_west", probe(pw, sink, "block 97 -59 130 minecraft:tripwire[west=true]", 28))
		mark("wire_north", probe(pw, sink, "block 97 -59 130 minecraft:tripwire[north=false]", 29))
		mark("hook_a_attached", probe(pw, sink, "block 96 -59 130 minecraft:tripwire_hook[attached=true]", 30))
		mark("hook_b_attached", probe(pw, sink, "block 98 -59 130 minecraft:tripwire_hook[attached=true]", 31))
		// The lone wire at (97,-59,132) next to a hook facing away
		// stays unconnected.
		sendCommand(t, pw, "setblock 97 -59 132 tripwire")
		sendCommand(t, pw, "setblock 98 -59 132 tripwire_hook[facing=south]")
		time.Sleep(400 * time.Millisecond)
		mark("wire_hook_away", probe(pw, sink, "block 97 -59 132 minecraft:tripwire[east=false]", 32))
		mark("hook_away_detached", probe(pw, sink, "block 98 -59 132 minecraft:tripwire_hook[attached=false]", 33))
		// Hook pops (with the 2001 break effect + drop) when the
		// anchor block on its back side breaks.
		sendCommand(t, pw, "setblock 95 -59 130 air")
		time.Sleep(400 * time.Millisecond)
		mark("hook_pop", probe(pw, sink, "block 96 -59 130 minecraft:air", 34))
		mark("wire_detach", probe(pw, sink, "block 97 -59 130 minecraft:tripwire[west=false]", 35))

		// MultifaceBlock fold: a setblock'd vein face detaches when
		// its attach cell's face stops being full; dropping the last
		// face pops the block (updateOrDestroy), while a setblock'd
		// floating vein keeps its requested state — BlockInput.place
		// discards the air fold.
		sendCommand(t, pw, "setblock 96 -60 140 stone")
		sendCommand(t, pw, "setblock 96 -59 140 glow_lichen[down=true]")
		time.Sleep(400 * time.Millisecond)
		mark("vein_attached", probe(pw, sink, "block 96 -59 140 minecraft:glow_lichen[down=true]", 37))
		sendCommand(t, pw, "setblock 97 -59 140 stone")
		sendCommand(t, pw, "setblock 96 -59 140 glow_lichen[down=true,east=true]")
		time.Sleep(400 * time.Millisecond)
		mark("vein_two_face", probe(pw, sink, "block 96 -59 140 minecraft:glow_lichen[down=true,east=true]", 38))
		sendCommand(t, pw, "setblock 97 -59 140 air")
		time.Sleep(400 * time.Millisecond)
		mark("vein_one_face", probe(pw, sink, "block 96 -59 140 minecraft:glow_lichen[down=true,east=false]", 39))
		sendCommand(t, pw, "setblock 96 -60 140 air")
		time.Sleep(400 * time.Millisecond)
		mark("vein_pop", probe(pw, sink, "block 96 -59 140 minecraft:air", 40))
		sendCommand(t, pw, "setblock 98 -59 140 sculk_vein[up=true]")
		time.Sleep(400 * time.Millisecond)
		mark("vein_float", probe(pw, sink, "block 98 -59 140 minecraft:sculk_vein[up=true]", 41))

		return results
	}

	va := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla legs: %v", va)
	t.Logf("mcgo legs:    %v", mc)
	if len(va) != len(mc) {
		t.Fatalf("leg counts differ: vanilla=%v mcgo=%v", va, mc)
	}
	bad := 0
	for i := range va {
		if va[i] != mc[i] {
			t.Logf("leg %d diverged: vanilla=%s mcgo=%s", i, va[i], mc[i])
			bad++
		}
		if strings.HasSuffix(va[i], "=none") {
			t.Fatalf("probe with no answer (server down mid-run?): %v", va)
		}
	}
	if bad > 0 {
		t.Fatalf("%d legs diverged", bad)
	}
}
