package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestGolemBuildParity pins the vanilla golem-construction patterns:
// CarvedPumpkinBlock.onPlace runs on every block write (so /setblock
// completing a snow/iron/copper pattern spawns the golem and airs
// the cells, copper instead turning its block into a copper chest),
// while WitherSkullBlock.checkSpawn rides setPlacedBy — an
// item-place-only hook — so a setblock'd skull T spawns nothing
// until the last skull is placed by an actual item use.
func TestGolemBuildParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)

	// awaitKey polls the chat sink until a reply containing key
	// arrives — commands echo their translate key on both servers.
	awaitKey := func(sink *chatSink, key string) bool {
		end := time.Now().Add(4 * time.Second)
		for time.Now().Before(end) {
			for _, line := range sink.take() {
				if strings.Contains(line, key) {
					return true
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		return false
	}

	run := func(addr string) map[string]bool {
		cv, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer cv.Close()
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "tp P 30 -60 6 0 0")

		setb := func(x, y, z int, blk string) {
			for range 30 {
				sendCommand(t, pw,
					fmt.Sprintf("setblock %d %d %d minecraft:%s", x, y, z, blk))
				if awaitKey(sink, "setblock.success") {
					return
				}
				// argument.pos.unloaded while the chunk stream is
				// still catching the teleport — keep retrying.
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("setblock %s never landed", blk)
		}
		// A pattern cell is consumed when it no longer holds the
		// original block — air is too strict since the spawned
		// snow golem's own trail re-fills its column instantly.
		gone := func(x, y, z int, blk string) bool {
			sendCommand(t, pw, fmt.Sprintf(
				"execute unless block %d %d %d minecraft:%s run say GONE", x, y, z, blk))
			return awaitKey(sink, "GONE")
		}
		exists := func(mob string) bool {
			return nbtFloatOf(t, sink, pw,
				"@e[type=minecraft:"+mob+",limit=1]", "Health") > 0
		}

		setb(-2, -62, -2, "stone") // sanity: writes work at all
		// The build site sits inside generated terrain — clear the
		// region outright (the `~` cells must be real air) then lay
		// the stone floor the golems stand on. Fills fail with
		// argument.pos.unloaded while chunks stream in, so retry
		// until the success key actually lands.
		fillb := func(cmd string) {
			for range 30 {
				sendCommand(t, pw, cmd)
				// "fill." catches commands.fill.success and
				// commands.fill.failed ("No blocks filled" = the
				// region already held the requested state).
				if awaitKey(sink, "fill.") {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("fill never landed: %s", cmd)
		}
		fillb("fill -2 -62 -2 64 -55 2 minecraft:air")
		fillb("fill -2 -62 -2 64 -62 2 minecraft:stone")

		// Snow golem — "^","#","#": pumpkin over two snow blocks.
		// Build it inside a sealed stone cell — the golem's own
		// snow trail otherwise wanders into the iron/wither `~`
		// cells faster than the assertions can kill it.
		fillb("fill -1 -61 -1 1 -56 1 minecraft:stone hollow")
		setb(0, -60, 0, "snow_block")
		setb(0, -59, 0, "snow_block")
		setb(0, -58, 0, "carved_pumpkin")
		// Iron golem — "~^~","###","~#~" on the X crossbar.
		setb(9, -60, 0, "iron_block")
		setb(10, -60, 0, "iron_block")
		setb(11, -60, 0, "iron_block")
		setb(10, -61, 0, "iron_block")
		setb(10, -59, 0, "carved_pumpkin")
		// Copper golem — "^","#" with a #minecraft:copper cell.
		setb(20, -60, 0, "copper_block")
		setb(20, -59, 0, "carved_pumpkin")
		time.Sleep(500 * time.Millisecond)

		out := map[string]bool{
			"snow_spawned": exists("snow_golem"),
		}
		// SnowGolem.aiStep's trail: a live golem leaves snow_layer
		// in whatever air cell it stands in (fires even mid-fall —
		// vanilla has no onGround gate on it).
		sendCommand(t, pw,
			"execute at @e[type=minecraft:snow_golem,limit=1] if block ~ ~ ~ minecraft:snow_layer run say TRAIL")
		out["snow_trail"] = awaitKey(sink, "TRAIL")
		// Kill the snow golem the instant it is confirmed — its
		// trail otherwise snow-layers the iron/wither `~` cells.
		sendCommand(t, pw, "kill @e[type=minecraft:snow_golem]")
		awaitKey(sink, "kill")
		out["snow_cleared"] = gone(0, -60, 0, "snow_block") &&
			gone(0, -59, 0, "snow_block") && gone(0, -58, 0, "carved_pumpkin")
		out["iron_spawned"] = exists("iron_golem")
		if !out["iron_spawned"] {
			// Diagnostic: which `~`/`#` cell failed to match?
			for i, c := range [][3]int{
				{9, -61, 0}, {11, -61, 0}, {9, -59, 0}, {11, -59, 0},
			} {
				for _, blk := range []string{
					"air", "snow_layer", "dirt", "grass_block",
					"stone", "iron_block", "carved_pumpkin",
				} {
					sendCommand(t, pw, fmt.Sprintf(
						"execute if block %d %d %d minecraft:%s run say CELL%d%s",
						c[0], c[1], c[2], blk, i, blk))
					time.Sleep(60 * time.Millisecond)
				}
			}
			for _, l := range sink.take() {
				if strings.Contains(l, "CELL") {
					t.Logf("%s %s", addr, l)
				}
			}
		}
		out["iron_cleared"] = gone(10, -61, 0, "iron_block") &&
			gone(10, -60, 0, "iron_block") && gone(10, -59, 0, "carved_pumpkin")
		out["copper_spawned"] = exists("copper_golem")
		out["copper_pumpkin_gone"] = gone(20, -59, 0, "carved_pumpkin")
		sendCommand(t, pw, fmt.Sprintf(
			"execute if block 20 -60 0 minecraft:copper_chest run say CHESTSEEN"))
		out["chest_present"] = awaitKey(sink, "CHESTSEEN")

		// Wither — "^*^","###","~#~" (wither skull row over a soul T).
		// setblock writes spawn NOTHING: checkSpawn is setPlacedBy.
		setb(29, -60, 0, "soul_sand")
		setb(30, -60, 0, "soul_sand")
		setb(31, -60, 0, "soul_sand")
		setb(30, -61, 0, "soul_sand")
		setb(29, -59, 0, "wither_skeleton_skull")
		setb(30, -59, 0, "wither_skeleton_skull")
		setb(31, -59, 0, "wither_skeleton_skull")
		time.Sleep(500 * time.Millisecond)
		out["wither_setblock_silent"] = !exists("wither")

		// Rebuild the last skull slot by hand: remove it and place
		// the skull item, which is what actually trips checkSpawn.
		// The tp moves P inside the interaction reach — SyncPos needs
		// the confirm+echo the chat sink already performs.
		setb(31, -59, 0, "air")
		sendCommand(t, pw, "tp P 31.5 -59 3 0 90")
		sendCommand(t, pw,
			"item replace entity P weapon.mainhand with minecraft:wither_skeleton_skull")
		awaitKey(sink, "item")
		var ub protocol.Buffer
		ub.VarInt(0)            // main hand
		ub.Position(31, -60, 0) // click the soul block's top face
		ub.VarInt(1)            // face up -> skull lands at 31,-59,0
		ub.Float(0.5)
		ub.Float(1.0)
		ub.Float(0.5)
		ub.Bool(false)
		ub.Bool(false)
		ub.VarInt(0)
		_ = pw.WritePacket(server.IDPlayUseItemOnSB, ub.Bytes())
		time.Sleep(800 * time.Millisecond)
		out["wither_spawned"] = exists("wither")
		out["wither_cleared"] = gone(30, -60, 0, "soul_sand") &&
			gone(30, -59, 0, "wither_skeleton_skull")
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	t.Logf("vanilla %v", van)
	t.Logf("mc-go   %v", mc)
	for k, v := range van {
		if !v {
			t.Errorf("vanilla: %s", k)
		}
	}
	for k, v := range mc {
		if !v {
			t.Errorf("mc-go: %s", k)
		}
	}
}
