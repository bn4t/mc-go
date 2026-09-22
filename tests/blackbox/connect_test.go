//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestConnectParity covers the CrossCollisionBlock connection slice
// measured on the 26.2 jar: FenceBlock.connectsTo and
// IronBarsBlock.attachsTo rewrite the prop facing a changed horizontal
// neighbour (the updateShape half of every block write), with the
// rewrite cascading down a connected run. FenceBlock joins a same-
// family fence (matching #wooden_fences membership), a perpendicular
// fence gate, or a sturdy side that isn't in Block's exception list;
// IronBarsBlock joins panes/bars, walls, or a sturdy non-exception
// side. Player placement folds the props via getStateForPlacement;
// command writes place the exact state and only neighbours update.
func TestConnectParity(t *testing.T) {
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
		// Vanilla's /fill refuses unloaded chunks: land the player in
		// the region before writing pads.
		sendCommand(t, pw, "tp @s 96.5 -59 88.5")
		time.Sleep(500 * time.Millisecond)
		sendCommand(t, pw, "fill 92 -60 78 126 -60 96 stone")
		sendCommand(t, pw, "fill 92 -59 78 126 -59 96 air")
		time.Sleep(400 * time.Millisecond)

		// Leg A: two adjacent setblock fences connect both ways — the
		// second write flips the first fence's prop via updateShape,
		// and the cascade back-fills the second's own prop.
		sendCommand(t, pw, "setblock 96 -59 80 oak_fence")
		sendCommand(t, pw, "setblock 97 -59 80 oak_fence")
		time.Sleep(300 * time.Millisecond)
		mark("fence_a_east", probe(pw, sink, "block 96 -59 80 minecraft:oak_fence[east=true]", 0))
		mark("fence_b_west", probe(pw, sink, "block 97 -59 80 minecraft:oak_fence[west=true]", 1))

		// Leg B: a sturdy non-exception side connects (stone).
		sendCommand(t, pw, "setblock 99 -59 80 oak_fence")
		sendCommand(t, pw, "setblock 100 -59 80 stone")
		time.Sleep(300 * time.Millisecond)
		mark("fence_sturdy", probe(pw, sink, "block 99 -59 80 minecraft:oak_fence[east=true]", 2))

		// Leg C: a torch face isn't sturdy — no connection.
		sendCommand(t, pw, "setblock 102 -59 80 oak_fence")
		sendCommand(t, pw, "setblock 103 -59 80 torch")
		time.Sleep(300 * time.Millisecond)
		mark("fence_thin_no", probe(pw, sink, "block 102 -59 80 minecraft:oak_fence[east=false]", 3))

		// Leg D: leaves are in the connection-exception list — full
		// cube but never attached.
		sendCommand(t, pw, "setblock 105 -59 80 oak_fence")
		sendCommand(t, pw, "setblock 106 -59 80 oak_leaves")
		time.Sleep(300 * time.Millisecond)
		mark("fence_leaves_no", probe(pw, sink, "block 105 -59 80 minecraft:oak_fence[east=false]", 4))

		// Leg E: material gate — oak and nether_brick fences share
		// #fences but not #wooden_fences; no connection either way.
		sendCommand(t, pw, "setblock 108 -59 80 oak_fence")
		sendCommand(t, pw, "setblock 109 -59 80 nether_brick_fence")
		time.Sleep(300 * time.Millisecond)
		mark("mat_oak_east", probe(pw, sink, "block 108 -59 80 minecraft:oak_fence[east=false]", 5))
		mark("mat_nether_west", probe(pw, sink, "block 109 -59 80 minecraft:nether_brick_fence[west=false]", 6))

		// Leg F/G: a fence gate connects only when its facing is
		// perpendicular to the fence->gate direction. Gate east of
		// the fence: facing south (axis Z) connects, facing east
		// (axis X) does not.
		sendCommand(t, pw, "setblock 96 -59 84 oak_fence")
		sendCommand(t, pw, "setblock 97 -59 84 oak_fence_gate[facing=south]")
		sendCommand(t, pw, "setblock 99 -59 84 oak_fence")
		sendCommand(t, pw, "setblock 100 -59 84 oak_fence_gate[facing=east]")
		time.Sleep(300 * time.Millisecond)
		mark("gate_perp", probe(pw, sink, "block 96 -59 84 minecraft:oak_fence[east=true]", 7))
		mark("gate_par", probe(pw, sink, "block 99 -59 84 minecraft:oak_fence[east=false]", 8))

		// Leg H: pane pair connects.
		sendCommand(t, pw, "setblock 96 -59 88 glass_pane")
		sendCommand(t, pw, "setblock 97 -59 88 glass_pane")
		time.Sleep(300 * time.Millisecond)
		mark("pane_pair_a", probe(pw, sink, "block 96 -59 88 minecraft:glass_pane[east=true]", 9))
		mark("pane_pair_b", probe(pw, sink, "block 97 -59 88 minecraft:glass_pane[west=true]", 10))

		// Leg I: pane next to fence — neither family reaches the
		// other (fence isn't sturdy, isn't a pane, isn't a wall).
		sendCommand(t, pw, "setblock 99 -59 88 glass_pane")
		sendCommand(t, pw, "setblock 100 -59 88 oak_fence")
		time.Sleep(300 * time.Millisecond)
		mark("pane_fence_no", probe(pw, sink, "block 99 -59 88 minecraft:glass_pane[east=false]", 11))
		mark("fence_pane_no", probe(pw, sink, "block 100 -59 88 minecraft:oak_fence[west=false]", 12))

		// Leg J: iron bars attach to walls.
		sendCommand(t, pw, "setblock 103 -59 88 iron_bars")
		sendCommand(t, pw, "setblock 104 -59 88 cobblestone_wall")
		time.Sleep(300 * time.Millisecond)
		mark("bars_wall", probe(pw, sink, "block 103 -59 88 minecraft:iron_bars[east=true]", 13))

		// Leg K: breaking the middle of a fence run drops the two
		// outer connection props through the cascade.
		sendCommand(t, pw, "setblock 112 -59 80 oak_fence")
		sendCommand(t, pw, "setblock 113 -59 80 oak_fence")
		sendCommand(t, pw, "setblock 114 -59 80 oak_fence")
		time.Sleep(250 * time.Millisecond)
		sendCommand(t, pw, "setblock 113 -59 80 air")
		time.Sleep(400 * time.Millisecond)
		mark("break_a", probe(pw, sink, "block 112 -59 80 minecraft:oak_fence[east=false]", 14))
		mark("break_c", probe(pw, sink, "block 114 -59 80 minecraft:oak_fence[west=false]", 15))

		// Leg M: wall pair — endpoints keep a raised post, sides are
		// low under open sky.
		sendCommand(t, pw, "setblock 96 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 97 -59 92 cobblestone_wall")
		time.Sleep(300 * time.Millisecond)
		mark("wall_a_east", probe(pw, sink, "block 96 -59 92 minecraft:cobblestone_wall[east=low]", 20))
		mark("wall_a_up", probe(pw, sink, "block 96 -59 92 minecraft:cobblestone_wall[up=true]", 21))

		// Leg N: a three-wall run flattens the middle post; the ends
		// keep theirs.
		sendCommand(t, pw, "setblock 100 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 101 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 102 -59 92 cobblestone_wall")
		time.Sleep(300 * time.Millisecond)
		mark("wall_mid_up", probe(pw, sink, "block 101 -59 92 minecraft:cobblestone_wall[up=false]", 22))
		mark("wall_mid_e", probe(pw, sink, "block 101 -59 92 minecraft:cobblestone_wall[east=low]", 23))
		mark("wall_end_up", probe(pw, sink, "block 100 -59 92 minecraft:cobblestone_wall[up=true]", 24))

		// Leg O: a solid block above raises sides to tall and keeps
		// the post.
		sendCommand(t, pw, "setblock 105 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 106 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 105 -58 92 stone")
		time.Sleep(400 * time.Millisecond)
		mark("wall_tall_e", probe(pw, sink, "block 105 -59 92 minecraft:cobblestone_wall[east=tall]", 25))
		mark("wall_tall_up", probe(pw, sink, "block 105 -59 92 minecraft:cobblestone_wall[up=true]", 26))

		// Leg P: a torch on top overrides the post on a straight run.
		sendCommand(t, pw, "setblock 109 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 110 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 111 -59 92 cobblestone_wall")
		time.Sleep(250 * time.Millisecond)
		sendCommand(t, pw, "setblock 110 -58 92 torch")
		time.Sleep(400 * time.Millisecond)
		mark("wall_torch_up", probe(pw, sink, "block 110 -59 92 minecraft:cobblestone_wall[up=true]", 27))

		// Leg Q: walls attach to panes and gates — wall side becomes
		// low toward a pane; the pane connects back via #walls.
		sendCommand(t, pw, "setblock 114 -59 92 cobblestone_wall")
		sendCommand(t, pw, "setblock 115 -59 92 glass_pane")
		time.Sleep(300 * time.Millisecond)
		mark("wall_pane", probe(pw, sink, "block 114 -59 92 minecraft:cobblestone_wall[east=low]", 28))
		mark("pane_wall", probe(pw, sink, "block 115 -59 92 minecraft:glass_pane[west=true]", 29))

		// Leg R: a player-placed chest faces the player (opposite of
		// horizontal facing) and starts single.
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:chest 2")
		sendCommand(t, pw, "tp @s 96.5 -59 98.5 180 0") // faces north
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 96, -60, 96, 1, 101)
		time.Sleep(400 * time.Millisecond)
		mark("chest_facing", probe(pw, sink, "block 96 -59 96 minecraft:chest[facing=south,type=single]", 30))

		// Leg S: a second chest placed adjacent pairs — the new chest
		// is the left half (its partner lies on its clockwise side)
		// and the existing single flips to right via updateShape.
		sendUseItemOn(t, pw, 97, -60, 96, 1, 102)
		time.Sleep(400 * time.Millisecond)
		mark("chest_new_left", probe(pw, sink, "block 97 -59 96 minecraft:chest[type=left]", 31))
		mark("chest_old_right", probe(pw, sink, "block 96 -59 96 minecraft:chest[type=right]", 32))

		// Leg T: breaking the partner returns a chest to single.
		sendCommand(t, pw, "setblock 97 -59 96 air")
		time.Sleep(400 * time.Millisecond)
		mark("chest_unmerge", probe(pw, sink, "block 96 -59 96 minecraft:chest[type=single]", 33))

		// Leg U: setblock writes the exact state — two adjacent
		// default chests never merge.
		sendCommand(t, pw, "setblock 104 -59 96 chest[facing=south]")
		sendCommand(t, pw, "setblock 105 -59 96 chest[facing=south]")
		time.Sleep(300 * time.Millisecond)
		mark("sb_chest_a", probe(pw, sink, "block 104 -59 96 minecraft:chest[type=single]", 34))
		mark("sb_chest_b", probe(pw, sink, "block 105 -59 96 minecraft:chest[type=single]", 35))

		// Leg V: a trapped chest is a different block — no merge.
		sendCommand(t, pw, "setblock 107 -59 96 trapped_chest[facing=south]")
		sendCommand(t, pw, "tp @s 108.5 -59 98.5 180 0")
		time.Sleep(400 * time.Millisecond)
		sendUseItemOn(t, pw, 108, -60, 96, 1, 103)
		time.Sleep(400 * time.Millisecond)
		mark("trapped_no", probe(pw, sink, "block 108 -59 96 minecraft:chest[type=single]", 36))
		mark("trapped_kept", probe(pw, sink, "block 107 -59 96 minecraft:trapped_chest[type=single]", 37))

		// Leg W: sneaking and clicking a single chest's east face
		// merges with it — the new chest takes the partner's facing
		// and the side-appropriate type.
		sendCommand(t, pw, "setblock 110 -59 96 chest[facing=south]")
		sendCommand(t, pw, "tp @s 111.5 -59 98.5 0 0")
		time.Sleep(400 * time.Millisecond)
		if err := pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0x20}); err != nil { // sneak
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		sendUseItemOn(t, pw, 110, -59, 96, 5, 104) // east face of the chest
		time.Sleep(400 * time.Millisecond)
		mark("sneak_new", probe(pw, sink, "block 111 -59 96 minecraft:chest[facing=south,type=left]", 38))
		mark("sneak_old", probe(pw, sink, "block 110 -59 96 minecraft:chest[type=right]", 39))
		if err := pw.WritePacket(server.IDPlayPlayerInputSB, []byte{0x00}); err != nil { // sneak off
			t.Fatal(err)
		}

		// Leg L: player placement folds getStateForPlacement — a
		// fence placed next to a fence writes its own prop on entry.
		sendCommand(t, pw, "setblock 121 -59 80 oak_fence")
		sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:oak_fence 1")
		sendCommand(t, pw, "tp @s 119.5 -59 81.5")
		time.Sleep(400 * time.Millisecond)
		var seq int32 = 100
		sendUseItemOn(t, pw, 120, -60, 80, 1, seq)
		time.Sleep(400 * time.Millisecond)
		mark("place_fold", probe(pw, sink, "block 120 -59 80 minecraft:oak_fence[east=true]", 16))
		mark("place_fold_nb", probe(pw, sink, "block 121 -59 80 minecraft:oak_fence[west=true]", 17))

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
