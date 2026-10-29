//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
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

// bucketCase lays blocks around a case origin, holds a bucket stack in
// hotbar slot 0 and aims the use_item at a point (case-relative,
// fractional) from a player standing 2.5 blocks north of the origin.
type bucketCase struct {
	name     string
	setup    []string
	item     string
	count    int
	survival bool
	sneak    bool
	aim      [3]float64
}

var bucketCases = []bucketCase{
	{name: "empty on water source", setup: []string{"0 -1 0 water"}, item: "bucket", aim: [3]float64{0.5, -0.1, 0.5}},
	{name: "empty on flowing water", setup: []string{"0 -1 0 water[level=3]"}, item: "bucket", aim: [3]float64{0.5, -0.5, 0.5}},
	{name: "empty on lava source", setup: []string{"0 -1 0 lava"}, item: "bucket", aim: [3]float64{0.5, -0.1, 0.5}},
	{name: "empty on waterlogged slab", setup: []string{"0 0 0 oak_slab[waterlogged=true]"}, item: "bucket", aim: [3]float64{0.5, 0.25, 0}},
	{name: "empty on waterlogged ladder", setup: []string{"0 0 1 stone", "0 0 0 ladder[facing=north,waterlogged=true]"}, item: "bucket", aim: [3]float64{0.5, 0.5, 0.9}},
	{name: "empty on powder snow", setup: []string{"0 0 0 powder_snow"}, item: "bucket", aim: [3]float64{0.5, 0.5, 0}},
	{name: "empty on stone", setup: []string{"0 0 0 stone"}, item: "bucket", aim: [3]float64{0.5, 0.5, 0}},
	{name: "empty survival", setup: []string{"0 -1 0 water"}, item: "bucket", survival: true, aim: [3]float64{0.5, -0.1, 0.5}},
	{name: "empty survival stack", setup: []string{"0 -1 0 water"}, item: "bucket", count: 3, survival: true, aim: [3]float64{0.5, -0.1, 0.5}},
	{name: "water on stone face", setup: []string{"0 0 0 stone"}, item: "water_bucket", aim: [3]float64{0.5, 0.5, 0}},
	{name: "water on floor", item: "water_bucket", aim: [3]float64{0.5, 0, 0.5}},
	{name: "lava on floor", item: "lava_bucket", aim: [3]float64{0.5, 0, 0.5}},
	{name: "water into slab", setup: []string{"0 0 0 oak_slab"}, item: "water_bucket", aim: [3]float64{0.5, 0.25, 0}},
	{name: "water into double slab", setup: []string{"0 0 0 oak_slab[type=double]"}, item: "water_bucket", aim: [3]float64{0.5, 0.5, 0}},
	{name: "lava at slab", setup: []string{"0 0 0 oak_slab"}, item: "lava_bucket", aim: [3]float64{0.5, 0.25, 0}},
	{name: "sneak water at slab", setup: []string{"0 0 0 oak_slab"}, item: "water_bucket", sneak: true, aim: [3]float64{0.5, 0.25, 0}},
	{name: "water into lit campfire", setup: []string{"0 0 0 campfire[lit=true]"}, item: "water_bucket", aim: [3]float64{0.5, 0.2, 0}},
	{name: "water into lit candle", setup: []string{"0 0 0 candle[lit=true]"}, item: "water_bucket", aim: [3]float64{0.5, 0.2, 0.5}},
	{name: "water at short grass", setup: []string{"0 0 0 short_grass"}, item: "water_bucket", aim: [3]float64{0.5, 0.3, 0.5}},
	{name: "water at torch", setup: []string{"0 0 0 torch"}, item: "water_bucket", aim: [3]float64{0.5, 0.3, 0.5}},
	{name: "water over flower", setup: []string{"0 0 1 stone", "0 0 0 poppy"}, item: "water_bucket", aim: [3]float64{0.5, 0.85, 1}},
	{name: "water onto flowing water", setup: []string{"0 0 1 stone", "0 0 0 water[level=4]"}, item: "water_bucket", aim: [3]float64{0.5, 0.5, 1}},
	{name: "water onto water source", setup: []string{"0 0 1 stone", "0 0 0 water"}, item: "water_bucket", aim: [3]float64{0.5, 0.5, 1}},
	{name: "water survival", item: "water_bucket", survival: true, aim: [3]float64{0.5, 0, 0.5}},
	{name: "water at sky", item: "water_bucket", aim: [3]float64{0.5, 6, 0.5}},
	{name: "water out of reach", setup: []string{"0 0 4 stone"}, item: "water_bucket", aim: [3]float64{0.5, 0.5, 4}},
	{name: "bottle on water", setup: []string{"0 -1 0 water"}, item: "glass_bottle", aim: [3]float64{0.5, -0.1, 0.5}},
	{name: "bottle on waterlogged slab", setup: []string{"0 0 0 oak_slab[waterlogged=true]"}, item: "glass_bottle", aim: [3]float64{0.5, 0.25, 0}},
	{name: "bottle on stone", setup: []string{"0 0 0 stone"}, item: "glass_bottle", aim: [3]float64{0.5, 0.5, 0}},
	{name: "bottle survival stack", setup: []string{"0 -1 0 water"}, item: "glass_bottle", count: 4, survival: true, aim: [3]float64{0.5, -0.1, 0.5}},
	{name: "boat on floor", item: "oak_boat", aim: [3]float64{0.5, 0, 0.5}},
	{name: "boat on water", setup: []string{"-1 -1 0 water", "0 -1 0 water", "1 -1 0 water", "-1 -1 1 water", "0 -1 1 water", "1 -1 1 water"}, item: "spruce_boat", aim: [3]float64{0.5, -0.1, 1.0}},
	{name: "boat over water pocket", setup: []string{"0 -1 0 water"}, item: "spruce_boat", aim: [3]float64{0.3, -0.1, 0.7}},
	{name: "chest boat survival", item: "oak_chest_boat", survival: true, aim: [3]float64{0.5, 0, 0.5}},
	{name: "boat under low ceiling", setup: []string{"0 1 1 stone", "0 1 0 stone"}, item: "birch_boat", aim: [3]float64{0.5, 0, 0.9}},
	{name: "boat at wall", setup: []string{"0 0 1 stone"}, item: "oak_boat", aim: [3]float64{0.5, 0.5, 1}},
	{name: "raft at sky", item: "bamboo_raft", aim: [3]float64{0.5, 6, 0.5}},
	{name: "water at fence side", setup: []string{"0 0 0 oak_fence"}, item: "water_bucket", aim: [3]float64{0.5, 0.5, 0.375}},
}

func bucketOrigin(i int) (int, int, int) { return 100 + 6*(i%12), -60, 340 + 6*(i/12) }

// bucketAim is the yaw/pitch from the player's eye (standing 1.62, or
// sneaking 1.27) to the case-relative aim point.
func bucketAim(c bucketCase) (float32, float32) {
	eye := 1.62
	if c.sneak {
		eye = 1.27
	}
	dx, dy, dz := c.aim[0]-0.5, c.aim[1]-eye, c.aim[2]+2.5
	yaw := math.Atan2(-dx, dz) * 180 / math.Pi
	pitch := -math.Atan2(dy, math.Hypot(dx, dz)) * 180 / math.Pi
	return float32(yaw), float32(pitch)
}

// TestBucketParity uses buckets (BucketItem.use) over the pickup and
// empty paths and diffs the block updates and the held stack each use
// leaves: the POV clip against outline and fluid shapes, pickups from
// sources, waterlogged blocks and powder snow, emptying into the hit
// cell or its face neighbour, liquid containers, sneaking, the
// replaceable-block break, and the survival result stacks.
func TestBucketParity(t *testing.T) {
	packMeta := `{"pack":{"description":"zz","min_format":107,"max_format":107}}`
	files := map[string]string{"ops.json": opsJSONFor("Bucketer"), "world/datapacks/zz/pack.mcmeta": packMeta}
	mcDir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(mcDir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("datapacks/zz/pack.mcmeta", packMeta)
	for i, c := range bucketCases {
		x, y, z := bucketOrigin(i)
		var b strings.Builder
		fmt.Fprintf(&b, "fill %d %d %d %d %d %d air\n", x-2, y, z-2, x+2, y+4, z+4)
		fmt.Fprintf(&b, "fill %d %d %d %d %d %d stone\n", x-2, y-1, z-2, x+2, y-1, z+4)
		for _, s := range c.setup {
			var dx, dy, dz int
			var blk string
			fmt.Sscanf(s, "%d %d %d %s", &dx, &dy, &dz, &blk)
			fmt.Fprintf(&b, "setblock %d %d %d %s\n", x+dx, y+dy, z+dz, blk)
		}
		mode := "creative"
		if c.survival {
			mode = "survival"
		}
		fmt.Fprintf(&b, "gamemode %s @s\n", mode)
		b.WriteString("clear @s\n")
		n := c.count
		if n == 0 {
			n = 1
		}
		fmt.Fprintf(&b, "item replace entity @s hotbar.0 with minecraft:%s %d\n", c.item, n)
		yaw, pitch := bucketAim(c)
		fmt.Fprintf(&b, "tp @s %.1f %d %.1f %v %v\n", float64(x)+0.5, y, float64(z)-2.5, yaw, pitch)
		fmt.Fprintf(&b, "say SETUP%dX\n", i)
		fn := b.String()
		files[fmt.Sprintf("world/datapacks/zz/data/zz/function/b%d.mcfunction", i)] = fn
		write(fmt.Sprintf("datapacks/zz/data/zz/function/b%d.mcfunction", i), fn)
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.WorldDir = mcDir
	})
	defer tg.cleanup()

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "Bucketer")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 16
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sendCommand(t, pw, "gamerule do_tile_drops false")
		// Frozen ticks keep placed fluids from flowing: each case diffs
		// the use's own writes only.
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "tp @s 130 -60 350")
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
		sendCommand(t, pw, "function zz:b0")
		wait("SETUP0X")
		time.Sleep(2 * time.Second)
		sink.take()
		var out []string
		for i, bc := range bucketCases {
			sendCommand(t, pw, fmt.Sprintf("function zz:b%d", i))
			wait(fmt.Sprintf("SETUP%dX", i))
			time.Sleep(time.Second)
			sink.take()
			if bc.sneak {
				sendPlayerInput(pw, 0x20)
				time.Sleep(200 * time.Millisecond)
			}
			yaw, pitch := bucketAim(bc)
			sendUseItemAim(t, pw, 0, int32(2000+i), yaw, pitch)
			time.Sleep(200 * time.Millisecond)
			if bc.sneak {
				sendPlayerInput(pw, 0)
			}
			sendCommand(t, pw, fmt.Sprintf("say DONE%dX", i))
			lines := wait(fmt.Sprintf("DONE%dX", i))
			time.Sleep(time.Second)
			lines = append(lines, sink.take()...)
			updates := map[[3]int32]int32{}
			inv := map[int16]string{}
			full := false
			x, y, z := bucketOrigin(i)
			var spawns []string
			for _, l := range lines {
				collectBlockUpdates(l, updates)
				if strings.HasPrefix(l, "0x1:") {
					if sp, ok := dfDecodeSpawn([]byte(l[4:])); ok {
						name, _ := server.EntityTypeName(sp.typ)
						if name == "item" {
							// A block drop's spawn offset and yaw are random.
							spawns = append(spawns, "item")
							continue
						}
						spawns = append(spawns, fmt.Sprintf("%s@(%.3f,%.3f,%.3f)yaw=%d", name, sp.x-float64(x), sp.y-float64(y), sp.z-float64(z), sp.yaw))
					}
				}
				if collectInventory(l, inv) {
					full = true
				}
			}
			var cells []string
			for p, st := range updates {
				cells = append(cells, fmt.Sprintf("%d,%d,%d=%s", p[0]-int32(x), p[1]-int32(y), p[2]-int32(z), stateLabel(st)))
			}
			sort.Strings(cells)
			var slots []string
			for sl, it := range inv {
				slots = append(slots, fmt.Sprintf("%d:%s", sl, it))
			}
			sort.Strings(slots)
			out = append(out, fmt.Sprintf("%s | full=%v %s | %s", strings.Join(cells, " "), full, strings.Join(slots, " "), strings.Join(spawns, " ")))
		}
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	bad := 0
	for i, c := range bucketCases {
		if want[i] != got[i] {
			bad++
			t.Errorf("%s:\n  vanilla %s\n  mc-go   %s", c.name, want[i], got[i])
		} else {
			t.Logf("%s: %s", c.name, got[i])
		}
	}
	t.Logf("%d cases, %d differ", len(bucketCases), bad)
}

// collectInventory folds a player-inventory container_set_content
// (reported true; only non-empty slots kept) or set_slot line into the
// slot -> item map.
func collectInventory(line string, into map[int16]string) bool {
	i := strings.Index(line, ":")
	if i < 0 {
		return false
	}
	var id int32
	if _, err := fmt.Sscanf(line[:i], "%v", &id); err != nil {
		return false
	}
	r := protocol.NewPacketReader([]byte(line[i+1:]))
	label := func(it server.ItemStack) string {
		if it.Empty() {
			return "empty"
		}
		name, _ := world.ItemName(it.ID)
		return fmt.Sprintf("%sx%d", strings.TrimPrefix(name, "minecraft:"), it.Count)
	}
	switch id {
	case server.IDPlayContainerContent:
		if w, err := r.VarInt(); err != nil || w != 0 {
			return false
		}
		if _, err := r.VarInt(); err != nil {
			return false
		}
		n, err := r.VarInt()
		if err != nil {
			return false
		}
		for k := range into {
			delete(into, k)
		}
		for sl := int16(0); sl < int16(n); sl++ {
			it, err := readSlotBB(r)
			if err != nil {
				return true
			}
			if !it.Empty() {
				into[sl] = label(it)
			}
		}
		return true
	case server.IDPlayContainerSlot:
		if w, err := r.VarInt(); err != nil || w != 0 {
			return false
		}
		if _, err := r.VarInt(); err != nil {
			return false
		}
		sl, err := r.Short()
		if err != nil {
			return false
		}
		it, err := readSlotBB(r)
		if err != nil {
			return false
		}
		into[sl] = label(it)
	}
	return false
}
