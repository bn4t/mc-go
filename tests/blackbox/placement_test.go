//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// placeCase is one block-item use: a setup (blocks around the case
// origin, relative), the held item, the player's rotation and sneak
// state, and the click (relative hit block, face, cursor).
type placeCase struct {
	name       string
	setup      []string // "dx dy dz block" setblocks relative to the origin
	item       string
	yaw, pitch float32
	sneak      bool
	hit        [3]int
	face       int32
	cursor     [3]float32
}

// placeCases covers the block classes' getStateForPlacement families.
// The origin sits on a stone floor (dy -1 is stone); the player stands
// at origin + (0.5, 0, -2.5).
var placeCases = []placeCase{
	// RotatedPillarBlock: axis from the clicked face.
	{name: "log top", item: "oak_log", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "log side", setup: []string{"0 0 1 stone"}, item: "oak_log", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "log east", setup: []string{"1 0 0 stone"}, item: "oak_log", hit: [3]int{1, 0, 0}, face: 4, cursor: [3]float32{0, 0.5, 0.5}},
	// StairBlock: facing from the player, half from face and cursor.
	{name: "stairs bottom", item: "oak_stairs", yaw: 30, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "stairs side top", setup: []string{"0 0 1 stone"}, item: "stone_stairs", yaw: -100, hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.8, 0}},
	// SlabBlock: half from cursor; a second slab merges to double.
	{name: "slab low", setup: []string{"0 0 1 stone"}, item: "oak_slab", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.3, 0}},
	{name: "slab high", setup: []string{"0 0 1 stone"}, item: "oak_slab", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.7, 0}},
	{name: "slab merge", setup: []string{"0 0 0 oak_slab[type=bottom]"}, item: "oak_slab", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.5, 0.5}},
	{name: "slab merge side", setup: []string{"0 0 0 oak_slab[type=bottom]"}, item: "oak_slab", hit: [3]int{0, 0, 0}, face: 2, cursor: [3]float32{0.5, 0.7, 0}},
	{name: "slab other type", setup: []string{"0 0 0 stone_slab[type=bottom]"}, item: "oak_slab", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.5, 0.5}},
	// DoorBlock + DoubleHighBlockItem: both halves, hinge.
	{name: "door", item: "oak_door", yaw: 10, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.3, 1, 0.6}},
	{name: "door hinge wall", setup: []string{"-1 0 0 stone", "-1 1 0 stone"}, item: "iron_door", yaw: 0, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.7, 1, 0.5}},
	{name: "door no room", setup: []string{"0 1 0 stone"}, item: "oak_door", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	// DoublePlantBlock.
	{name: "sunflower", setup: []string{"0 -1 0 grass_block"}, item: "sunflower", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "flower no soil", item: "poppy", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	// BedBlock + BedItem.
	{name: "bed", item: "red_bed", yaw: 90, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "bed blocked", setup: []string{"-1 0 0 stone"}, item: "red_bed", yaw: 90, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	// StandingAndWallBlockItem: torches, signs, banners, heads.
	{name: "torch floor", item: "torch", pitch: 60, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "torch wall", setup: []string{"0 0 1 stone"}, item: "torch", pitch: 10, hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "torch wall look down", setup: []string{"0 0 1 stone"}, item: "torch", pitch: 80, hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "redstone torch wall", setup: []string{"0 0 1 stone"}, item: "redstone_torch", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "sign floor", item: "oak_sign", yaw: 37, pitch: 40, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "sign wall", setup: []string{"0 0 1 stone"}, item: "oak_sign", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "hanging sign", setup: []string{"0 2 0 stone"}, item: "oak_hanging_sign", yaw: 200, pitch: -40, hit: [3]int{0, 2, 0}, face: 0, cursor: [3]float32{0.5, 0, 0.5}},
	{name: "banner floor", item: "white_banner", yaw: -63, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "head floor", item: "skeleton_skull", yaw: 123, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "head wall", setup: []string{"0 0 1 stone"}, item: "zombie_head", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	// FaceAttachedHorizontalDirectionalBlock.
	{name: "button floor", item: "stone_button", yaw: 45, pitch: 50, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "button wall", setup: []string{"0 0 1 stone"}, item: "oak_button", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "button ceiling", setup: []string{"0 2 0 stone"}, item: "oak_button", pitch: -60, hit: [3]int{0, 2, 0}, face: 0, cursor: [3]float32{0.5, 0, 0.5}},
	{name: "lever wall", setup: []string{"1 0 0 stone"}, item: "lever", yaw: -90, hit: [3]int{1, 0, 0}, face: 4, cursor: [3]float32{0, 0.5, 0.5}},
	{name: "grindstone", item: "grindstone", yaw: 170, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	// Wall attachments over the looking directions.
	{name: "ladder", setup: []string{"0 0 1 stone"}, item: "ladder", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "ladder floor click", setup: []string{"0 0 1 stone"}, item: "ladder", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "tripwire hook", setup: []string{"0 0 1 stone"}, item: "tripwire_hook", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "cocoa", setup: []string{"0 0 1 jungle_log"}, item: "cocoa_beans", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	// TrapDoorBlock.
	{name: "trapdoor top click", item: "oak_trapdoor", yaw: 75, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "trapdoor side high", setup: []string{"0 0 1 stone"}, item: "oak_trapdoor", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.8, 0}},
	// Horizontal facings.
	{name: "furnace", item: "furnace", yaw: 100, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "anvil", item: "anvil", yaw: 10, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "chest", item: "chest", yaw: -170, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "fence gate", item: "oak_fence_gate", yaw: 80, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "campfire", item: "campfire", yaw: 260, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "repeater", item: "repeater", yaw: 15, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "lectern", item: "lectern", yaw: -45, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "bell floor", item: "bell", yaw: 60, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "bell wall", setup: []string{"0 0 1 stone"}, item: "bell", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	// Nearest-looking facings.
	{name: "piston down look", item: "piston", pitch: 70, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "observer", item: "observer", yaw: 20, pitch: 10, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "dispenser", item: "dispenser", yaw: -80, pitch: -20, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "barrel", item: "barrel", pitch: 30, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	// Clicked-face facings.
	{name: "end rod", setup: []string{"0 0 1 stone"}, item: "end_rod", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "end rod on rod", setup: []string{"0 0 0 end_rod[facing=up]"}, item: "end_rod", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.6, 0.5}},
	{name: "hopper side", setup: []string{"0 0 1 stone"}, item: "hopper", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "shulker box", setup: []string{"1 0 0 stone"}, item: "shulker_box", hit: [3]int{1, 0, 0}, face: 4, cursor: [3]float32{0, 0.5, 0.5}},
	{name: "lightning rod", setup: []string{"0 2 0 stone"}, item: "lightning_rod", pitch: -50, hit: [3]int{0, 2, 0}, face: 0, cursor: [3]float32{0.5, 0, 0.5}},
	{name: "chain side", setup: []string{"0 0 1 stone"}, item: "iron_chain", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	// Lanterns and hanging.
	{name: "lantern floor", item: "lantern", pitch: 40, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "lantern ceiling", setup: []string{"0 2 0 stone"}, item: "lantern", pitch: -40, hit: [3]int{0, 2, 0}, face: 0, cursor: [3]float32{0.5, 0, 0.5}},
	// Replacing the clicked block.
	{name: "replace grass", setup: []string{"0 0 0 short_grass"}, item: "stone", hit: [3]int{0, 0, 0}, face: 2, cursor: [3]float32{0.5, 0.3, 0}},
	{name: "replace water", setup: []string{"0 0 0 water", "0 0 1 stone", "-1 0 0 stone", "1 0 0 stone", "0 0 -1 stone"}, item: "oak_planks", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "waterlogged slab", setup: []string{"0 0 0 water", "0 0 1 stone", "-1 0 0 stone", "1 0 0 stone", "0 0 -1 stone"}, item: "oak_slab", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.3, 0}},
	{name: "waterlogged stairs", setup: []string{"0 0 0 water", "0 0 1 stone", "-1 0 0 stone", "1 0 0 stone", "0 0 -1 stone"}, item: "oak_stairs", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.3, 0}},
	{name: "snow stack", setup: []string{"0 0 0 snow[layers=2]"}, item: "snow", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.25, 0.5}},
	{name: "snow on layer1 side", setup: []string{"0 0 0 snow[layers=1]", "0 0 1 stone"}, item: "stone", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "sea pickle stack", setup: []string{"0 0 0 sea_pickle[pickles=1,waterlogged=false]"}, item: "sea_pickle", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.3, 0.5}},
	{name: "candle stack", setup: []string{"0 0 0 candle[candles=2]"}, item: "candle", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.3, 0.5}},
	{name: "carpet", item: "white_carpet", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "rail", item: "rail", yaw: 90, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "redstone wire", item: "redstone", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "vine", setup: []string{"0 0 1 stone"}, item: "vine", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "huge mushroom", setup: []string{"1 0 0 red_mushroom_block"}, item: "red_mushroom_block", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "leaves", item: "oak_leaves", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "fence", setup: []string{"1 0 0 stone"}, item: "oak_fence", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "note block", setup: []string{"0 -1 0 oak_planks"}, item: "note_block", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "pointed dripstone", item: "pointed_dripstone", pitch: 50, hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "sneak chest pair", setup: []string{"0 0 1 chest[facing=north]"}, item: "chest", sneak: true, yaw: 90, hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "concrete powder in water", setup: []string{"0 0 0 water", "0 0 1 stone", "-1 0 0 stone", "1 0 0 stone", "0 0 -1 stone"}, item: "white_concrete_powder", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.5, 0}},
	{name: "farmland under block", setup: []string{"0 1 0 stone"}, item: "farmland", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "bamboo on dirt", setup: []string{"0 -1 0 dirt"}, item: "bamboo", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
	{name: "wall", setup: []string{"1 0 0 cobblestone_wall"}, item: "cobblestone_wall", hit: [3]int{0, -1, 0}, face: 1, cursor: [3]float32{0.5, 1, 0.5}},
}

const placeOriginX, placeOriginZ = 100, 200

func placeOrigin(i int) (int, int, int) { return placeOriginX + 6*(i%12), -60, placeOriginZ + 6*(i/12) }

// placeFunction is the setup function for case i: clear the case box,
// lay its blocks, hold the item and stand facing the requested way.
func placeFunction(i int, c placeCase) string {
	x, y, z := placeOrigin(i)
	var b strings.Builder
	fmt.Fprintf(&b, "fill %d %d %d %d %d %d air\n", x-2, y, z-2, x+2, y+4, z+2)
	fmt.Fprintf(&b, "fill %d %d %d %d %d %d stone\n", x-2, y-1, z-2, x+2, y-1, z+2)
	for _, s := range c.setup {
		var dx, dy, dz int
		var blk string
		fmt.Sscanf(s, "%d %d %d %s", &dx, &dy, &dz, &blk)
		fmt.Fprintf(&b, "setblock %d %d %d %s\n", x+dx, y+dy, z+dz, blk)
	}
	fmt.Fprintf(&b, "item replace entity @s weapon.mainhand with minecraft:%s\n", c.item)
	fmt.Fprintf(&b, "tp @s %.1f %d %.1f %v %v\n", float64(x)+0.5, y, float64(z)-2.5, c.yaw, c.pitch)
	// The marker comes from the function itself, after its writes.
	fmt.Fprintf(&b, "say SETUP%dX\n", i)
	return b.String()
}

// TestPlacementParity places block items over the placement families
// and diffs every block update the click produces: target selection
// (replacing the clicked block or its face neighbour), the class's
// getStateForPlacement (facings from yaw, looking directions and
// clicked face, halves from cursor, waterlogging, stacking, merging),
// the survival and obstruction checks and the second cell double-high
// blocks and beds write.
func TestPlacementParity(t *testing.T) {
	packMeta := `{"pack":{"description":"zz","min_format":107,"max_format":107}}`
	files := map[string]string{"ops.json": opsJSONFor("Placer"), "world/datapacks/zz/pack.mcmeta": packMeta}
	mcDir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(mcDir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("datapacks/zz/pack.mcmeta", packMeta)
	for i, c := range placeCases {
		fn := placeFunction(i, c)
		files[fmt.Sprintf("world/datapacks/zz/data/zz/function/p%d.mcfunction", i)] = fn
		write(fmt.Sprintf("datapacks/zz/data/zz/function/p%d.mcfunction", i), fn)
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.WorldDir = mcDir
	})
	defer tg.cleanup()

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "Placer")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 16
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sendCommand(t, pw, fmt.Sprintf("tp @s %d -60 %d", placeOriginX, placeOriginZ))
		time.Sleep(2 * time.Second)
		wait := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				for _, l := range sink.take() {
					if strings.Contains(l, marker) {
						return lines
					}
					lines = append(lines, l)
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s: marker %s never arrived", addr, marker)
			return nil
		}
		// Warm up: the first setup also streams the case area's
		// chunks; run it once and drop everything it sent.
		sendCommand(t, pw, "function zz:p0")
		wait("SETUP0X")
		time.Sleep(2 * time.Second)
		sink.take()
		var out []string
		for i, pc := range placeCases {
			sendCommand(t, pw, fmt.Sprintf("function zz:p%d", i))
			wait(fmt.Sprintf("SETUP%dX", i))
			time.Sleep(time.Second)
			sink.take()
			if pc.sneak {
				sendPlayerInput(pw, 0x20)
			}
			x, y, z := placeOrigin(i)
			var b protocol.Buffer
			b.VarInt(0)
			b.Position(int32(x+pc.hit[0]), int32(y+pc.hit[1]), int32(z+pc.hit[2]))
			b.VarInt(pc.face)
			b.Float(pc.cursor[0])
			b.Float(pc.cursor[1])
			b.Float(pc.cursor[2])
			b.Bool(false)
			b.VarInt(int32(1000 + i))
			if err := pw.WritePacket(server.IDPlayUseItemOnSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			if pc.sneak {
				sendPlayerInput(pw, 0)
			}
			sendCommand(t, pw, fmt.Sprintf("say DONE%dX", i))
			updates := map[[3]int32]int32{}
			for _, l := range wait(fmt.Sprintf("DONE%dX", i)) {
				collectBlockUpdates(l, updates)
			}
			// Block updates flush at the end of the tick that made
			// them, after a chat marker from the same tick: give a
			// loaded server a moment, then take the stragglers.
			time.Sleep(time.Second)
			for _, l := range sink.take() {
				collectBlockUpdates(l, updates)
			}
			var cells []string
			for p, st := range updates {
				cells = append(cells, fmt.Sprintf("%d,%d,%d=%s", p[0]-int32(x), p[1]-int32(y), p[2]-int32(z), stateLabel(st)))
			}
			sort.Strings(cells)
			out = append(out, strings.Join(cells, " "))
		}
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	bad := 0
	for i, c := range placeCases {
		if want[i] != got[i] {
			bad++
			t.Errorf("%s:\n  vanilla %s\n  mc-go   %s", c.name, want[i], got[i])
		} else {
			t.Logf("%s: %s", c.name, got[i])
		}
	}
	t.Logf("%d cases, %d differ", len(placeCases), bad)
}

// collectBlockUpdates folds one sink line's block_update or
// section_blocks_update into the position -> state map.
func collectBlockUpdates(line string, into map[[3]int32]int32) {
	i := strings.Index(line, ":")
	if i < 0 {
		return
	}
	var id int32
	if _, err := fmt.Sscanf(line[:i], "%v", &id); err != nil {
		return
	}
	r := protocol.NewPacketReader([]byte(line[i+1:]))
	switch id {
	case server.IDPlayBlockUpdateCB:
		x, y, z, err := r.Position()
		if err != nil {
			return
		}
		st, err := r.VarInt()
		if err != nil {
			return
		}
		into[[3]int32{x, y, z}] = st
	case 0x54: // section_blocks_update
		sec, err := r.Long()
		if err != nil {
			return
		}
		sx, sy, sz := int32(sec>>42), int32(sec<<44>>44), int32(sec<<22>>42)
		n, err := r.VarInt()
		if err != nil {
			return
		}
		for k := int32(0); k < n; k++ {
			v, err := r.VarLong()
			if err != nil {
				return
			}
			st := int32(v >> 12)
			lx, lz, ly := int32(v>>8&15), int32(v>>4&15), int32(v&15)
			into[[3]int32{sx*16 + lx, sy*16 + ly, sz*16 + lz}] = st
		}
	}
}

// stateLabel names a protocol state id: block[props].
func stateLabel(id int32) string {
	mapping, _ := world.BlockMapping(server.ProtocolVersion)
	b, ok := mapping.FromWire(id)
	if !ok {
		return fmt.Sprint(id)
	}
	name, ok := world.StateOwner(b)
	if !ok {
		return fmt.Sprint(id)
	}
	props := world.BlockProperties(b)
	if len(props) == 0 {
		return name
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var kv []string
	for _, k := range keys {
		kv = append(kv, k+"="+props[k])
	}
	return name + "[" + strings.Join(kv, ",") + "]"
}
