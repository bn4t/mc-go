//go:build blackbox

package blackbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Block entities of generated blocks: a dungeon's chests and spawner, a
// meadow tree's bee nest, a desert well's suspicious sand, deep dark
// sculk, sulfur cave potent sulfur, a pale garden's creaking hearts and
// an end highlands gateway. Both servers generate a normal world from
// the same seed with every feature stripped except the ones that leave
// block entities (so decoration-order effects stay local), and the
// comparison is the saved block_entities list of each chunk and the
// chunk packet's block entity array.
//
// Decoration order: where neighbouring chunks' features overlap (pale
// oak canopies, sculk spreading) the real server's result depends on
// its schedule. A chunk is compared strictly only when every non-fluid
// block of it agrees; elsewhere the block entities at cells whose block
// agrees are still compared, and the rest are counted, not failed.

const genBESeed = 12345

// genBEPlaced are the placed features kept, by name.
var genBEPlaced = []string{
	"monster_room", "monster_room_deep", "desert_well", "trees_meadow",
	"sculk_patch_deep_dark", "sulfur_pool", "rooted_sulfur_spring",
	"pale_garden_vegetation", "end_gateway_return",
}

func genBEOverrides() map[string]json.RawMessage {
	keep := map[string]bool{}
	for _, p := range genBEPlaced {
		keep[p] = true
	}
	return worldgen.StageOverrides(worldgen.Vanilla(), func(p string) bool { return keep[p] }, true, true)
}

// genBEAreas are the forced areas per dimension (chunk rectangles).
var genBEAreas = map[string][]terrainArea{
	"overworld": {
		{9, -14, 11, -12},        // deep dark: sculk catalysts, sensors, shriekers
		{12, -12, 14, -10},       // deep dark edge
		{28, 36, 30, 38},         // a lone deep dark patch with a shrieker (29,37)
		{29, 40, 31, 42},         // meadow tree bee nest (30,41) over deep dark
		{34, 34, 36, 36},         // two dungeons (35,35) and (36,35)
		{46, 95, 48, 97},         // sulfur caves potent sulfur
		{5, -37, 7, -35},         // pale garden creaking hearts
		{-232, -281, -230, -279}, // desert well (-231,-280)
		{-221, -278, -219, -276}, // dungeon over sulfur caves
	},
	"the_end": {
		{125, 127, 127, 129}, // end_gateway_return at (126,128)
		{91, 129, 93, 131},   // end_gateway_return at (92,130)
	},
}

// mcgoTerrainRegion has mc-go generate every chunk of areas in dim on
// the normal level type (ticking frozen), saves, and returns the
// dimension's region directory.
func mcgoTerrainRegion(t *testing.T, seed int64, ov map[string]json.RawMessage, dim string, areas []terrainArea) string {
	t.Helper()
	dir := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	var srv *server.Server
	startMcGoCfg(t, port, func(s *server.Server) {
		srv = s
		s.LevelType = server.LevelTypeNormal
		s.GenerateStructures = false
		s.WorldSeed = seed
		s.WorldgenOverlay = ov
		s.WorldDir = filepath.Join(dir, "world")
	})
	srv.SetFrozen(true)
	w := srv.DimensionWorld("minecraft:" + dim)
	if w == nil {
		t.Fatalf("mc-go has no dimension %s", dim)
	}
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				w.BaseChunk(int(cx), int(cz))
			}
		}
	}
	if err := srv.SaveWorld(); err != nil {
		t.Fatalf("mc-go save: %v", err)
	}
	return filepath.Join(dir, "world", "dimensions", "minecraft", dim, "region")
}

// savedBlockEntities reads a saved chunk's block_entities by position.
func savedBlockEntities(c map[string]any) map[[3]int]map[string]any {
	out := map[[3]int]map[string]any{}
	l, _ := c["block_entities"].(nbt.List)
	for _, v := range l.Items {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		x, _ := m["x"].(int32)
		y, _ := m["y"].(int32)
		z, _ := m["z"].(int32)
		out[[3]int{int(x), int(y), int(z)}] = m
	}
	return out
}

// normNBT makes two decoded values comparable: an empty list's element
// type is not part of its value (the real server writes TAG_End for
// it).
func normNBT(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = normNBT(x)
		}
		return m
	case nbt.List:
		l := nbt.List{Elem: t.Elem, Items: make([]any, len(t.Items))}
		if len(t.Items) == 0 {
			l.Elem = 0
		}
		for i, x := range t.Items {
			l.Items[i] = normNBT(x)
		}
		return l
	}
	return v
}

func nbtString(v any) string {
	b, _ := json.Marshal(normNBT(v))
	return string(b)
}

// genBEDimBounds is a dimension's minY and height.
func genBEDimBounds(dim string) (int32, int32) {
	if dim == "overworld" {
		return -64, 384
	}
	return 0, 256
}

// chunkBlocksAgree reports whether two saved chunks hold the same
// blocks, fluids exempt (post-processing flow), and the per-cell
// agreement.
func chunkBlocksAgree(v, m savedChunk) (bool, func(i int) bool) {
	same := func(i int) bool { return v.blocks[i] == m.blocks[i] }
	for i := range v.blocks {
		if !same(i) && !strings.Contains(v.blocks[i], "level=") && !strings.Contains(m.blocks[i], "level=") {
			return false, same
		}
	}
	return true, same
}

// TestGeneratedBlockEntitySaveParity compares the saved block entities
// of every forced chunk, entry for entry and field for field.
func TestGeneratedBlockEntitySaveParity(t *testing.T) {
	ov := genBEOverrides()
	counts := map[string]int{}
	strict, loose, skipped := 0, 0, 0
	for _, dim := range []string{"overworld", "the_end"} {
		areas := genBEAreas[dim]
		if len(areas) == 0 {
			continue
		}
		vRegion := vanillaDimTerrain(t, genBESeed, stageDatapack(ov), dim, areas)
		mRegion := mcgoTerrainRegion(t, genBESeed, ov, dim, areas)
		minY, height := genBEDimBounds(dim)
		for _, a := range areas {
			for cx := a.x0; cx <= a.x1; cx++ {
				for cz := a.z0; cz <= a.z1; cz++ {
					v := readSavedChunk(t, vRegion, cx, cz, minY, height)
					m := readSavedChunk(t, mRegion, cx, cz, minY, height)
					agree, same := chunkBlocksAgree(v, m)
					vb, mb := savedBlockEntities(v.raw), savedBlockEntities(m.raw)
					cellOf := func(p [3]int) int {
						return ((p[1]-int(minY))*16+(p[2]&15))*16 + (p[0] & 15)
					}
					if agree {
						strict++
					} else {
						loose++
						t.Logf("%s chunk %d,%d: blocks differ by decoration order (%d vanilla block entities)", dim, cx, cz, len(vb))
					}
					for p, ve := range vb {
						if !agree && !same(cellOf(p)) {
							skipped++
							continue
						}
						me, ok := mb[p]
						if !ok {
							t.Errorf("%s chunk %d,%d: block entity at %v (%v) missing on mc-go", dim, cx, cz, p, ve["id"])
							continue
						}
						if !reflect.DeepEqual(normNBT(ve), normNBT(me)) {
							t.Errorf("%s chunk %d,%d block entity at %v:\n vanilla %s\n mc-go   %s", dim, cx, cz, p, nbtString(ve), nbtString(me))
							continue
						}
						id, _ := ve["id"].(string)
						counts[id]++
					}
					for p, me := range mb {
						if _, ok := vb[p]; ok {
							continue
						}
						if !agree && !same(cellOf(p)) {
							skipped++
							continue
						}
						t.Errorf("%s chunk %d,%d: block entity at %v (%v) only on mc-go", dim, cx, cz, p, me["id"])
					}
				}
			}
		}
	}
	t.Logf("chunks: %d strict, %d with order-dependent blocks; %d block entities at order-dependent cells skipped; matched %v", strict, loose, skipped, counts)
	for _, id := range []string{"minecraft:mob_spawner", "minecraft:chest", "minecraft:beehive", "minecraft:sculk_sensor",
		"minecraft:sculk_catalyst", "minecraft:sculk_shrieker", "minecraft:potent_sulfur", "minecraft:creaking_heart"} {
		if counts[id] == 0 {
			t.Errorf("no %s compared", id)
		}
	}
}

// genBEWireSpot is one place both clients go to.
type genBEWireSpot struct {
	tp       string
	sections int
	// freeze stops level ticking first. The real server sends no new
	// chunks for an in-dimension teleport while frozen, but a dimension
	// change still loads the destination.
	freeze bool
}

// teleportMoved runs a teleport command, confirms the position sync it
// draws and answers with a position packet at the destination, as a
// real client does: the real server re-centres an in-dimension
// player's chunk window only once the client has moved there.
func teleportMoved(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, cmd string) {
	t.Helper()
	sendCommand(t, pw, cmd)
	deadline := time.Now().Add(20 * time.Second)
	defer c.SetReadDeadline(time.Time{})
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(deadline)
		p, err := pr.ReadPacket()
		if err != nil {
			t.Fatalf("teleport %q: %v", cmd, err)
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.VarInt()
			x, _ := r.Double()
			y, _ := r.Double()
			z, _ := r.Double()
			var b protocol.Buffer
			b.VarInt(id)
			_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			var m protocol.Buffer
			m.Double(x)
			m.Double(y)
			m.Double(z)
			m.Byte(0)
			_ = pw.WritePacket(server.IDPlayMovePosSB, m.Bytes())
			return
		}
	}
	t.Fatalf("teleport %q: no position sync", cmd)
}

// TestGeneratedBlockEntityWireParity joins both servers, teleports
// over the generated block entities (high above the dungeons, out of
// their spawners' range, so Delay holds; ticking is frozen before the
// end trip, since a gateway's Age runs with the level) and compares each chunk
// packet's block entity array: positions, types and the update tag (a
// spawner's SpawnData and delays, a gateway's Age and exit, nothing for
// the rest).
func TestGeneratedBlockEntityWireParity(t *testing.T) {
	ov := genBEOverrides()
	props := map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          fmt.Sprint(genBESeed),
		"generate-structures": "false",
		"view-distance":       "4",
	}
	files := stageDatapack(ov)
	files["ops.json"] = opsJSONFor("BEProbe")
	tg := requireBlackboxFiles(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.GenerateStructures = false
		s.WorldSeed = genBESeed
		s.WorldgenOverlay = ov
		s.ViewDistance = 4
	})
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	spots := []genBEWireSpot{
		{"tp @s 566 100 566", 24, false},                                  // dungeon spawners and chests
		{"tp @s 482 140 671", 24, false},                                  // meadow bee nest
		{"tp @s 160 0 -210", 24, false},                                   // deep dark sculk
		{"tp @s -3682 100 -4473", 24, false},                              // desert well sand
		{"execute in minecraft:the_end run tp @s 2028 90 2060", 16, true}, // end gateway
	}
	fetch := func(tag, addr string) []map[[2]int32][]byte {
		c, pr, pw := joinPlayBlackbox(t, addr, "BEProbe")
		defer c.Close()
		collectJoin(t, tag, c, pr, pw)
		var out []map[[2]int32][]byte
		for _, sp := range spots {
			if sp.freeze {
				sendCommand(t, pw, "tick freeze")
			}
			if strings.HasPrefix(sp.tp, "execute in") {
				// A dimension change loads its own window.
				sendCommand(t, pw, sp.tp)
			} else {
				teleportMoved(t, c, pr, pw, sp.tp)
			}
			ch, _ := collectJoin(t, tag+" "+sp.tp, c, pr, pw)
			out = append(out, ch)
		}
		return out
	}
	vAll := fetch("vanilla", tg.vanilla)
	mAll := fetch("mc-go", tg.mcgo)
	mapping, _ := world.BlockMapping(server.ProtocolVersion)
	fluid := func(wire int32) bool {
		b, ok := mapping.FromWire(wire)
		return ok && worldgen.HasFluid(b)
	}
	counts := map[int32]int{}
	withTag := 0
	for i, sp := range spots {
		for k, vb := range vAll[i] {
			mb, ok := mAll[i][k]
			if !ok {
				continue
			}
			v := parseWireChunk(t, "vanilla", vb, sp.sections)
			m := parseWireChunk(t, "mc-go", mb, sp.sections)
			agree := true
			for s := range v.blocks {
				for j := range v.blocks[s] {
					if a, b := v.blocks[s][j], m.blocks[s][j]; a != b && !fluid(a) && !fluid(b) {
						agree = false
					}
				}
			}
			_, _, vbe, err := parseChunkBlockEntities(vb)
			if err != nil {
				t.Fatalf("vanilla chunk %v block entities: %v", k, err)
			}
			_, _, mbe, err := parseChunkBlockEntities(mb)
			if err != nil {
				t.Fatalf("mc-go chunk %v block entities: %v", k, err)
			}
			if !agree {
				continue
			}
			var keys [][3]int
			for p := range vbe {
				keys = append(keys, p)
			}
			sort.Slice(keys, func(a, b int) bool { return fmt.Sprint(keys[a]) < fmt.Sprint(keys[b]) })
			for _, p := range keys {
				e := vbe[p]
				me, ok := mbe[p]
				if !ok {
					t.Errorf("chunk %v block entity at %v (type %d) missing on mc-go", k, p, e.typ)
					continue
				}
				if me.typ != e.typ || !reflect.DeepEqual(normNBT(e.nbt), normNBT(me.nbt)) {
					t.Errorf("chunk %v block entity at %v: vanilla type %d %s, mc-go type %d %s", k, p, e.typ, nbtString(e.nbt), me.typ, nbtString(me.nbt))
					continue
				}
				counts[e.typ]++
				if _, empty := e.nbt["__empty"]; !empty {
					withTag++
				}
			}
			for p, me := range mbe {
				if _, ok := vbe[p]; !ok {
					t.Errorf("chunk %v block entity at %v (type %d) only on mc-go", k, p, me.typ)
				}
			}
		}
	}
	t.Logf("matched block entities by type id: %v (%d with an update tag)", counts, withTag)
	if withTag == 0 {
		t.Error("no block entity with an update tag compared")
	}
}

// TestGeneratedChestLootParity opens two generated dungeon chests on
// both servers and compares the opened window's slots byte for byte:
// the saved LootTableSeed reproduces the fill (LootTable.fill on a
// LegacyRandomSource of the seed: pool rolls, weighted entries,
// set_count, enchant_randomly, then the shuffled slot spread).
func TestGeneratedChestLootParity(t *testing.T) {
	ov := genBEOverrides()
	props := map[string]string{
		"level-type":          "minecraft:normal",
		"generator-settings":  "{}",
		"level-seed":          fmt.Sprint(genBESeed),
		"generate-structures": "false",
		"view-distance":       "3",
	}
	files := stageDatapack(ov)
	files["ops.json"] = opsJSONFor("LootProbe")
	tg := requireBlackboxFiles(t, props, files, func(s *server.Server) {
		s.LevelType = server.LevelTypeNormal
		s.GenerateStructures = false
		s.WorldSeed = genBESeed
		s.WorldgenOverlay = ov
		s.ViewDistance = 3
	})
	defer tg.cleanup()
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	// The (35,35) dungeon's two chests, opened from inside the room.
	chests := []struct {
		tp      string
		x, y, z int32
	}{
		{"tp @s 567.5 30 567.5", 566, 30, 566},
		{"tp @s 567.5 30 567.5", 567, 30, 566},
	}
	open := func(tag, addr string) [][]byte {
		c, pr, pw := joinPlayBlackbox(t, addr, "LootProbe")
		defer c.Close()
		collectJoin(t, tag, c, pr, pw)
		var out [][]byte
		prev := ""
		for i, ch := range chests {
			if ch.tp != prev {
				teleportMoved(t, c, pr, pw, ch.tp)
				collectJoin(t, tag+" "+ch.tp, c, pr, pw)
				prev = ch.tp
			}
			sendUseItemOn(t, pw, ch.x, ch.y, ch.z, 1, int32(10+i))
			var slots []byte
			win := int32(0)
			deadline := time.Now().Add(20 * time.Second)
			for slots == nil && time.Now().Before(deadline) {
				_ = c.SetReadDeadline(deadline)
				p, err := pr.ReadPacket()
				if err != nil {
					t.Fatalf("%s chest %d: %v", tag, i, err)
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pw)
				case server.IDPlayContainerContent:
					r := protocol.NewPacketReader(p.Data)
					wid, _ := r.VarInt()
					r.VarInt() // state id
					if wid != 0 {
						win = wid
						slots = append([]byte(nil), p.Data[len(p.Data)-r.Remaining():]...)
					}
				}
			}
			_ = c.SetReadDeadline(time.Time{})
			if slots == nil {
				t.Fatalf("%s chest %d at %d,%d,%d never opened", tag, i, ch.x, ch.y, ch.z)
			}
			out = append(out, slots)
			var b protocol.Buffer
			b.VarInt(win)
			_ = pw.WritePacket(server.IDPlayCloseContainerSB, b.Bytes())
		}
		return out
	}
	want := open("vanilla", tg.vanilla)
	got := open("mc-go", tg.mcgo)
	for i := range want {
		if !bytes.Equal(want[i], got[i]) {
			t.Errorf("chest %d slots differ:\n vanilla % x\n mc-go   % x", i, want[i], got[i])
		} else {
			t.Logf("chest %d: %d slot bytes agree", i, len(want[i]))
		}
	}
}
