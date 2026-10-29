//go:build blackbox

package blackbox

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// entityItemCase is one entity-item use_item_on: setup blocks relative
// to the case origin (stone floor at dy -1), the held item (count 5),
// the player's yaw, and the click.
type entityItemCase struct {
	name   string
	setup  []string // "dx dy dz block" setblocks relative to the origin
	pre    []string // extra commands before the marker (difficulty, gamemode)
	item   string
	entity string // the entity type the use may spawn
	yaw    float32
	hit    [3]int
	face   int32
	cursor [3]float32
	// probe reads the placed entity's Pos/Rotation with /data get
	// (stationary types only — pigs wander, carts roll).
	probe bool
	// sneak holds shift through the click: the clicked block's own
	// use is skipped and the item's useOn runs.
	sneak bool
	// twice repeats the click: the second use meets the first's
	// entity (HangingEntity.canCoexist, the armor stand's box).
	twice bool
}

var eiTop = [3]float32{0.5, 1, 0.5}
var eiNorth = [3]float32{0.5, 0.5, 0}
var eiBottom = [3]float32{0.5, 0, 0.5}

var entityItemCases = []entityItemCase{
	// SpawnEggItem.useOn: clicked cell when its collision is empty,
	// else the face neighbour; getYOffset drops onto what's below.
	{name: "egg floor", item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	{name: "egg wall side", setup: []string{"0 0 1 stone"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth},
	{name: "egg high side", setup: []string{"0 1 1 stone"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, 1, 1}, face: 2, cursor: eiNorth},
	{name: "egg slab top", setup: []string{"0 0 0 stone_slab[type=bottom]"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.5, 0.5}},
	{name: "egg fence top", setup: []string{"0 0 0 oak_fence"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, 0, 0}, face: 1, cursor: eiTop},
	{name: "egg short grass", setup: []string{"0 0 0 short_grass"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.5, 0.5}},
	{name: "egg side slab cell", setup: []string{"0 0 1 stone", "0 0 0 stone_slab[type=bottom]"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth},
	{name: "egg named", item: `pig_spawn_egg[custom_name="Bob"]`, entity: "pig", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	{name: "egg spawner", setup: []string{"0 0 1 spawner"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth},
	{name: "egg peaceful zombie", pre: []string{"difficulty peaceful"}, item: "zombie_spawn_egg", entity: "zombie", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	{name: "egg creative", pre: []string{"gamemode creative @s"}, item: "pig_spawn_egg", entity: "pig", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	// MinecartItem.useOn: rails tag; +0.0625, +0.5 on a slope.
	{name: "cart flat rail", setup: []string{"0 0 0 rail"}, item: "minecart", entity: "minecart", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.125, 0.5}},
	{name: "chest cart slope", setup: []string{"0 0 0 rail[shape=ascending_south]", "0 0 1 stone"}, item: "chest_minecart", entity: "chest_minecart", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.5, 0.5}},
	{name: "hopper cart powered rail", setup: []string{"0 0 0 powered_rail"}, item: "hopper_minecart", entity: "hopper_minecart", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.125, 0.5}},
	{name: "tnt cart side of rail", setup: []string{"0 0 0 rail"}, item: "tnt_minecart", entity: "tnt_minecart", hit: [3]int{0, 0, 0}, face: 2, cursor: [3]float32{0.5, 0.06, 0}},
	{name: "cart on stone", item: "furnace_minecart", entity: "furnace_minecart", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	// ArmorStandItem.useOn: yaw snap, drop, obstruction, DOWN face.
	{name: "stand yaw 0", item: "armor_stand", entity: "armor_stand", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop, probe: true},
	{name: "stand yaw 100", item: "armor_stand", entity: "armor_stand", yaw: 100, hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop, probe: true},
	{name: "stand yaw -30", item: "armor_stand", entity: "armor_stand", yaw: -30, hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop, probe: true},
	{name: "stand yaw 202", item: "armor_stand", entity: "armor_stand", yaw: 202, hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop, probe: true},
	{name: "stand high side", setup: []string{"0 1 1 stone"}, item: "armor_stand", entity: "armor_stand", yaw: 10, hit: [3]int{0, 1, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "stand on slab", setup: []string{"0 0 0 stone_slab[type=bottom]"}, item: "armor_stand", entity: "armor_stand", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.5, 0.5}},
	{name: "stand replaces grass", setup: []string{"0 0 0 short_grass"}, item: "armor_stand", entity: "armor_stand", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.5, 0.5}, probe: true},
	{name: "stand down face", setup: []string{"0 2 0 stone"}, item: "armor_stand", entity: "armor_stand", hit: [3]int{0, 2, 0}, face: 0, cursor: eiBottom},
	{name: "stand blocked by cart", setup: []string{"summon 0 0 0 minecart {}"}, item: "armor_stand", entity: "armor_stand", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	{name: "stand blocked above", setup: []string{"0 1 0 stone"}, item: "armor_stand", entity: "armor_stand", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	// HangingEntityItem.useOn.
	{name: "frame wall", setup: []string{"0 0 1 stone"}, item: "item_frame", entity: "item_frame", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "frame floor", item: "item_frame", entity: "item_frame", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop, probe: true},
	{name: "glow frame ceiling", setup: []string{"0 2 0 stone"}, item: "glow_item_frame", entity: "glow_item_frame", hit: [3]int{0, 2, 0}, face: 0, cursor: eiBottom, probe: true},
	{name: "frame on glass", setup: []string{"0 0 1 glass"}, item: "item_frame", entity: "item_frame", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "frame on fence", setup: []string{"0 0 1 oak_fence"}, item: "item_frame", entity: "item_frame", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth},
	{name: "frame on repeater", setup: []string{"0 0 1 repeater"}, item: "item_frame", entity: "item_frame", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.06, 0}},
	{name: "frame on repeater sneaking", setup: []string{"0 0 1 repeater"}, item: "item_frame", entity: "item_frame", hit: [3]int{0, 0, 1}, face: 2, cursor: [3]float32{0.5, 0.06, 0}, sneak: true, probe: true},
	{name: "frame atop repeater", setup: []string{"0 0 0 repeater"}, item: "item_frame", entity: "item_frame", hit: [3]int{0, 0, 0}, face: 1, cursor: [3]float32{0.5, 0.125, 0.5}, sneak: true},
	{name: "frame twice", setup: []string{"0 0 1 stone"}, item: "item_frame", entity: "item_frame", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth, twice: true},
	{name: "painting twice", setup: []string{"0 0 1 stone"}, item: "painting", entity: "painting", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth, twice: true},
	{name: "stand twice", item: "armor_stand", entity: "armor_stand", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop, twice: true},
	{name: "painting floor", item: "painting", entity: "painting", hit: [3]int{0, -1, 0}, face: 1, cursor: eiTop},
	{name: "painting 1x1", setup: []string{"0 0 1 stone"}, item: "painting", entity: "painting", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "painting 2x1", setup: []string{"0 0 1 stone", "-1 0 1 stone"}, item: "painting", entity: "painting", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "painting 1x2", setup: []string{"0 0 1 stone", "0 1 1 stone"}, item: "painting", entity: "painting", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "painting 4x4", setup: []string{"fill -2 0 1 2 3 1 stone"}, item: "painting", entity: "painting", hit: [3]int{0, 1, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "painting 3x3 low ceiling", setup: []string{"fill -2 0 1 2 2 1 stone", "fill -2 3 0 2 3 0 stone"}, item: "painting", entity: "painting", hit: [3]int{0, 1, 1}, face: 2, cursor: eiNorth, probe: true},
	{name: "painting no wall", setup: []string{"0 0 1 short_grass"}, item: "painting", entity: "painting", hit: [3]int{0, 0, 1}, face: 2, cursor: eiNorth},
}

const entityItemOriginX, entityItemOriginZ = 300, 300

func entityItemOrigin(i int) (int, int, int) {
	return entityItemOriginX + 8*(i%10), -60, entityItemOriginZ + 8*(i/10)
}

// entityItemFunction is case i's setup: clear the box and every
// non-player entity, lay the blocks, hold the item and stand 2.5 north
// of the origin at the requested yaw.
func entityItemFunction(i int, c entityItemCase) string {
	x, y, z := entityItemOrigin(i)
	var b strings.Builder
	b.WriteString("kill @e[type=!player]\n")
	b.WriteString("kill @e[type=item]\n")
	b.WriteString("difficulty normal\n")
	b.WriteString("gamemode survival @s\n")
	fmt.Fprintf(&b, "fill %d %d %d %d %d %d air\n", x-3, y, z-3, x+3, y+4, z+3)
	fmt.Fprintf(&b, "fill %d %d %d %d %d %d stone\n", x-3, y-1, z-3, x+3, y-1, z+3)
	for _, s := range c.setup {
		if strings.HasPrefix(s, "fill ") {
			var x0, y0, z0, x1, y1, z1 int
			var blk string
			fmt.Sscanf(s, "fill %d %d %d %d %d %d %s", &x0, &y0, &z0, &x1, &y1, &z1, &blk)
			fmt.Fprintf(&b, "fill %d %d %d %d %d %d %s\n", x+x0, y+y0, z+z0, x+x1, y+y1, z+z1, blk)
			continue
		}
		if strings.HasPrefix(s, "summon ") {
			var dx, dy, dz int
			var ent, nbtArg string
			fmt.Sscanf(s, "summon %d %d %d %s %s", &dx, &dy, &dz, &ent, &nbtArg)
			fmt.Fprintf(&b, "summon %s %.1f %d %.1f %s\n", ent, float64(x+dx)+0.5, y+dy, float64(z+dz)+0.5, nbtArg)
			continue
		}
		var dx, dy, dz int
		var blk string
		fmt.Sscanf(s, "%d %d %d %s", &dx, &dy, &dz, &blk)
		fmt.Fprintf(&b, "setblock %d %d %d %s\n", x+dx, y+dy, z+dz, blk)
	}
	for _, p := range c.pre {
		b.WriteString(p + "\n")
	}
	count := 5
	if strings.HasSuffix(c.entity, "minecart") {
		count = 1 // minecarts stack to 1
	}
	fmt.Fprintf(&b, "item replace entity @s weapon.mainhand with minecraft:%s %d\n", c.item, count)
	fmt.Fprintf(&b, "tp @s %.1f %d %.1f %v 30\n", float64(x)+0.5, y, float64(z)-2.5, c.yaw)
	fmt.Fprintf(&b, "say SETUP%dX\n", i)
	return b.String()
}

// paintingSizes maps the captured painting_variant registry's wire
// ids to "WxH" — the variant pick is random, its size class is not.
func paintingSizes(t *testing.T) map[int32]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "internal", "server", "testdata", "vanilla-26.2", "config", "registries", "016-minecraft_painting_variant.bin"))
	if err != nil {
		t.Fatal(err)
	}
	br := bytes.NewReader(body)
	_, _ = protocol.ReadString(br)
	n, _ := protocol.ReadVarInt(br)
	out := map[int32]string{}
	for i := int32(0); i < n; i++ {
		_, _ = protocol.ReadString(br)
		present, _ := protocol.ReadBool(br)
		if !present {
			continue
		}
		v, err := nbt.ReadUnnamed(br)
		if err != nil {
			t.Fatal(err)
		}
		m, _ := v.(map[string]any)
		out[i] = fmt.Sprintf("%dx%d", m["width"], m["height"])
	}
	return out
}

// TestEntityItemUseOnParity pins the entity-item useOn paths — spawn
// eggs, minecarts, armor stands, item frames and paintings — through
// use_item_on in survival: whether the entity spawns, its add_entity
// (position to 3 decimals, rotation bytes, data), its pairing data
// (paintings by variant size class), the placement sounds, /data get
// Pos/Rotation where the entity stays put, and the held count left.
func TestEntityItemUseOnParity(t *testing.T) {
	packMeta := `{"pack":{"description":"zz","min_format":107,"max_format":107}}`
	files := map[string]string{"ops.json": opsJSONFor("Hanger"), "world/datapacks/zz/pack.mcmeta": packMeta}
	mcDir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(mcDir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("datapacks/zz/pack.mcmeta", packMeta)
	for i, c := range entityItemCases {
		fn := entityItemFunction(i, c)
		files[fmt.Sprintf("world/datapacks/zz/data/zz/function/e%d.mcfunction", i)] = fn
		write(fmt.Sprintf("datapacks/zz/data/zz/function/e%d.mcfunction", i), fn)
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) {
		s.WorldDir = mcDir
	})
	defer tg.cleanup()
	sizes := paintingSizes(t)
	pigAmbient := map[int32]bool{}
	for _, n := range []string{"entity.pig.ambient", "entity.pig_mini.ambient", "entity.pig_big.ambient"} {
		if id, ok := server.SoundEventID(n); ok {
			pigAmbient[id] = true
		}
	}
	// Only the sounds the uses themselves make are pinned: the
	// placement sounds and the spawned mob's ambient. A fresh pig's
	// steps and later voices are random.
	placeSnd := map[int32]bool{}
	for _, n := range []string{"entity.armor_stand.place", "entity.item_frame.place", "entity.glow_item_frame.place", "entity.painting.place"} {
		if id, ok := server.SoundEventID(n); ok {
			placeSnd[id] = true
		}
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "Hanger")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 16
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		sendCommand(t, pw, "gamerule random_tick_speed 0")
		sendCommand(t, pw, fmt.Sprintf("tp @s %d -60 %d", entityItemOriginX, entityItemOriginZ))
		time.Sleep(2 * time.Second)
		wait := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				for _, l := range sink.take() {
					lines = append(lines, l)
					if strings.Contains(l, marker) {
						return lines
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s: marker %s never arrived", addr, marker)
			return nil
		}
		// Warm up: case 0's setup streams the case area's chunks.
		sendCommand(t, pw, "function zz:e0")
		wait("SETUP0X")
		time.Sleep(2 * time.Second)
		sink.take()
		var out []string
		for i, ec := range entityItemCases {
			sendCommand(t, pw, fmt.Sprintf("function zz:e%d", i))
			wait(fmt.Sprintf("SETUP%dX", i))
			time.Sleep(time.Second)
			sink.take()
			ox, oy, oz := entityItemOrigin(i)
			if ec.sneak {
				sendPlayerInput(pw, 0x20)
				time.Sleep(100 * time.Millisecond)
			}
			sendUseItemOnCursor(t, pw, int32(ox+ec.hit[0]), int32(oy+ec.hit[1]), int32(oz+ec.hit[2]), ec.face,
				ec.cursor[0], ec.cursor[1], ec.cursor[2], int32(1000+i))
			if ec.twice {
				time.Sleep(300 * time.Millisecond)
				sendUseItemOnCursor(t, pw, int32(ox+ec.hit[0]), int32(oy+ec.hit[1]), int32(oz+ec.hit[2]), ec.face,
					ec.cursor[0], ec.cursor[1], ec.cursor[2], int32(2000+i))
			}
			time.Sleep(300 * time.Millisecond)
			if ec.sneak {
				sendPlayerInput(pw, 0)
			}
			sendCommand(t, pw, fmt.Sprintf("say DONE%dX", i))
			lines := wait(fmt.Sprintf("DONE%dX", i))
			time.Sleep(time.Second)
			lines = append(lines, sink.take()...)
			want, _ := server.EntityTypeID(ec.entity)
			roles := map[int32]bool{}
			sounds := map[string]bool{}
			var ev []string
			for _, l := range lines {
				k := strings.Index(l, ":")
				if k < 0 {
					continue
				}
				var id int32
				if _, err := fmt.Sscanf(l[:k], "%v", &id); err != nil {
					continue
				}
				data := []byte(l[k+1:])
				r := protocol.NewPacketReader(data)
				switch id {
				case server.IDPlaySpawnEntity:
					eid, _ := r.VarInt()
					_, _ = r.Read(16)
					typ, _ := r.VarInt()
					if typ != want {
						continue
					}
					if ec.name == "egg spawner" {
						// The spawner, now holding the egg's type, spawns
						// on its own random schedule and cells; its own pin
						// is TestSpawnerParity.
						continue
					}
					px, _ := r.Double()
					py, _ := r.Double()
					pz, _ := r.Double()
					decodeLpVec3(r)
					pitch, _ := r.Byte()
					yaw, _ := r.Byte()
					head, _ := r.Byte()
					dv, _ := r.VarInt()
					roles[eid] = true
					rot := fmt.Sprintf("pitch=%d yaw=%d head=%d", pitch, yaw, head)
					switch {
					case strings.HasSuffix(ec.item, "spawn_egg") || strings.Contains(ec.item, "spawn_egg["):
						// create's snapTo yaw is random; Mob copies it
						// to the head.
						rot = fmt.Sprintf("pitch=%d yaw=rand head=yaw:%v", pitch, head == yaw)
					case ec.entity == "armor_stand":
						// LivingEntity's ctor head yaw: nextFloat*6.28°.
						rot = fmt.Sprintf("pitch=%d yaw=%d head<5:%v", pitch, yaw, head < 5)
					}
					ev = append(ev, fmt.Sprintf("spawn pos=(%.3f,%.3f,%.3f) %s data=%d",
						px-float64(ox), py-float64(oy), pz-float64(oz), rot, dv))
				case server.IDPlayEntityMetadata:
					eid, _ := r.VarInt()
					if !roles[eid] || strings.Contains(ec.item, "spawn_egg") {
						continue
					}
					rest, _ := r.Read(r.Remaining())
					if ec.entity == "painting" {
						ev = append(ev, "data "+paintingMetaLabel(rest, sizes))
						continue
					}
					ev = append(ev, "data "+hex.EncodeToString(rest))
				case server.IDPlayRemoveEntity:
					n, _ := r.VarInt()
					for j := int32(0); j < n; j++ {
						eid, _ := r.VarInt()
						if roles[eid] {
							ev = append(ev, "remove")
						}
					}
				case 0x75:
					snd, _ := r.VarInt()
					src, _ := r.VarInt()
					_, _ = r.Int()
					_, _ = r.Int()
					_, _ = r.Int()
					vol, _ := r.Float()
					if !placeSnd[snd-1] && !pigAmbient[snd-1] {
						continue
					}
					label := fmt.Sprint(snd - 1)
					if pigAmbient[snd-1] {
						// The pig's sound_variant (classic/mini/big)
						// rolls at finalizeSpawn; its ambient follows.
						label = "pig-ambient"
					}
					key := fmt.Sprintf("sound %s src %d vol %.2f", label, src, vol)
					if !sounds[key] {
						sounds[key] = true
						ev = append(ev, key)
					}
				}
			}
			if ec.probe {
				sendCommand(t, pw, fmt.Sprintf("data get entity @e[type=%s,limit=1,sort=nearest] Pos", ec.entity))
				sendCommand(t, pw, fmt.Sprintf("data get entity @e[type=%s,limit=1,sort=nearest] Rotation", ec.entity))
			}
			base := ec.item
			if j := strings.Index(base, "["); j >= 0 {
				base = base[:j]
			}
			sendCommand(t, pw, fmt.Sprintf("clear @s minecraft:%s 0", base))
			sendCommand(t, pw, fmt.Sprintf("say PROBE%dX", i))
			for _, l := range wait(fmt.Sprintf("PROBE%dX", i)) {
				k := strings.Index(l, ":")
				if k < 0 {
					continue
				}
				var id int32
				if _, err := fmt.Sscanf(l[:k], "%v", &id); err != nil || id != server.IDPlaySystemChatCB {
					continue
				}
				root, err := nbt.ReadUnnamed(bytes.NewReader([]byte(l[k+1:])))
				if err != nil {
					t.Fatalf("chat nbt: %v", err)
				}
				txt := eiPlain(root)
				if strings.Contains(txt, "PROBE") {
					continue
				}
				ev = append(ev, "chat "+txt)
			}
			out = append(out, strings.Join(ev, " | "))
		}
		return out
	}
	want := run(tg.vanilla)
	got := run(tg.mcgo)
	bad := 0
	for i, c := range entityItemCases {
		if want[i] != got[i] {
			bad++
			t.Errorf("%s:\n  vanilla %s\n  mc-go   %s", c.name, want[i], got[i])
		} else {
			t.Logf("%s: %s", c.name, got[i])
		}
	}
	t.Logf("%d cases, %d differ", len(entityItemCases), bad)
}

// paintingMetaLabel renders a painting's pairing data with the
// variant holder replaced by its size class.
func paintingMetaLabel(body []byte, sizes map[int32]string) string {
	r := protocol.NewPacketReader(body)
	var parts []string
	variant := false
	for r.Remaining() > 0 {
		idx, err := r.Byte()
		if err != nil || idx == 0xFF {
			break
		}
		ser, _ := r.VarInt()
		v, _ := r.VarInt()
		switch {
		case idx == 9 && ser == 34:
			parts = append(parts, "variant="+sizes[v-1])
			variant = true
		default:
			parts = append(parts, fmt.Sprintf("%d/%d=%d", idx, ser, v))
		}
	}
	if !variant {
		// The pick landed on the registry's first entry — the
		// define default, so the entry stays out of the pairing data.
		parts = append(parts, "variant="+sizes[0])
	}
	return strings.Join(parts, ",")
}

// eiPlain flattens a chat component like componentPlain, also reading
// the {"": value} wrappers a mixed-type `with` list uses (a count next
// to a component).
func eiPlain(v any) string {
	switch c := v.(type) {
	case map[string]any:
		if w, ok := c[""]; ok && len(c) == 1 {
			return eiPlain(w)
		}
		var sb strings.Builder
		if t, ok := c["text"].(string); ok {
			sb.WriteString(t)
		} else if tr, ok := c["translate"].(string); ok {
			sb.WriteString(tr)
		}
		for _, key := range []string{"with", "extra"} {
			if c[key] != nil {
				sb.WriteString("[" + eiPlain(c[key]) + "]")
			}
		}
		return sb.String()
	case nbt.List:
		var parts []string
		for _, item := range c.Items {
			parts = append(parts, eiPlain(item))
		}
		return strings.Join(parts, ",")
	case []any:
		var parts []string
		for _, item := range c {
			parts = append(parts, eiPlain(item))
		}
		return strings.Join(parts, ",")
	case nil:
		return ""
	default:
		return fmt.Sprint(c)
	}
}
