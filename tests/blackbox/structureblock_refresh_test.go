package blackbox

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type structureBEUpdate struct {
	pos  [3]int32
	kind int32
	tag  map[string]any
}

func structureBEUpdatesBB(t *testing.T, lines []string) []structureBEUpdate {
	t.Helper()
	var out []structureBEUpdate
	for _, line := range lines {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		id, e := strconv.ParseInt(line[:colon], 0, 32)
		if e != nil || int32(id) != server.IDPlayBlockEntityData {
			continue
		}
		r := bytes.NewReader([]byte(line[colon+1:]))
		x, y, z, e := protocol.ReadPosition(r)
		if e != nil {
			t.Fatal(e)
		}
		kind, e := protocol.ReadVarInt(r)
		if e != nil {
			t.Fatal(e)
		}
		first, e := r.ReadByte()
		if e != nil {
			t.Fatal(e)
		}
		var tag map[string]any
		if first != nbt.TagEnd {
			_ = r.UnreadByte()
			value, e := nbt.ReadUnnamed(r)
			if e != nil {
				t.Fatal(e)
			}
			tag, _ = value.(map[string]any)
		}
		out = append(out, structureBEUpdate{[3]int32{x, y, z}, kind, tag})
	}
	return out
}

func TestStructureTemplateBlockEntityRefreshParity(t *testing.T) {
	ints := func(x, y, z int32) nbt.List { return nbt.List{Elem: nbt.TagInt, Items: []any{x, y, z}} }
	messages := func(first string) nbt.List { return nbt.List{Elem: nbt.TagString, Items: []any{first, "", "", ""}} }
	palette := nbt.List{Elem: nbt.TagCompound}
	blocks := nbt.List{Elem: nbt.TagCompound}
	for i, asset := range []struct {
		name       string
		props, tag map[string]any
	}{
		{"oak_sign", map[string]any{"rotation": "0", "waterlogged": "false"}, map[string]any{"id": "minecraft:sign", "front_text": map[string]any{"messages": messages("WireSign"), "color": "blue", "has_glowing_text": int8(1)}, "back_text": map[string]any{"messages": messages(""), "color": "black", "has_glowing_text": int8(0)}, "is_waxed": int8(1)}},
		{"structure_block", map[string]any{"mode": "data"}, map[string]any{"id": "minecraft:structure_block", "name": "test:placed_structure", "author": "PacketAuthor", "metadata": "wire_metadata", "mode": "DATA", "posX": int32(-3)}},
		{"jigsaw", map[string]any{"orientation": "north_up"}, map[string]any{"id": "minecraft:jigsaw", "name": "test:placed_jigsaw", "target": "test:next", "pool": "minecraft:empty", "final_state": "minecraft:oak_planks", "joint": "aligned", "selection_priority": int32(3), "placement_priority": int32(7)}},
		{"command_block", map[string]any{"facing": "up", "conditional": "false"}, map[string]any{"id": "minecraft:command_block", "Command": "say native_template", "TrackOutput": int8(1), "auto": int8(0), "SuccessCount": int32(2)}},
		{"chest", map[string]any{"facing": "north", "type": "single", "waterlogged": "false"}, map[string]any{"id": "minecraft:chest", "Items": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Slot": int8(0), "id": "minecraft:diamond", "count": int32(2)}}}}},
		{"furnace", map[string]any{"facing": "north", "lit": "false"}, map[string]any{"id": "minecraft:furnace", "Items": nbt.List{Elem: nbt.TagEnd}}},
		{"suspicious_sand", map[string]any{"dusted": "0"}, map[string]any{"id": "minecraft:brushable_block"}},
		{"spawner", nil, map[string]any{"id": "minecraft:mob_spawner", "Delay": int16(20), "SpawnData": map[string]any{"entity": map[string]any{"id": "minecraft:pig"}}, "SpawnPotentials": nbt.List{Elem: nbt.TagEnd}}},
	} {
		palette.Items = append(palette.Items, map[string]any{"Name": "minecraft:" + asset.name, "Properties": asset.props})
		blocks.Items = append(blocks.Items, map[string]any{"pos": ints(int32(i), 0, 0), "state": int32(i), "nbt": asset.tag})
	}
	native := map[string]any{"DataVersion": int32(4903), "size": ints(8, 1, 1), "palette": palette, "blocks": blocks, "entities": nbt.List{Elem: nbt.TagEnd}}
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	if e := nbt.WriteNamed(zw, "", native); e != nil {
		t.Fatal(e)
	}
	if e := zw.Close(); e != nil {
		t.Fatal(e)
	}
	mcDir := t.TempDir()
	path := filepath.Join(mcDir, "generated", "test", "structure", "be_refresh.nbt")
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, raw.Bytes(), 0644); e != nil {
		t.Fatal(e)
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("StructureWire"), "world/generated/test/structure/be_refresh.nbt": raw.String()}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.WorldDir = mcDir })
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "StructureWire")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "setblock 4 -60 4 minecraft:structure_block[mode=load]")
			sendCommand(t, pw, "say BE_REFRESH_READY")
			_ = waitForMarker(t, sink, "BE_REFRESH_READY", 10*time.Second)
			time.Sleep(250 * time.Millisecond)
			sink.take()
			writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, 2, 1, "test:be_refresh", [3]byte{0, 1, 0}, [3]byte{8, 1, 1}, 0, 0, "", 1, 17, 1)
			var lines []string
			seen := false
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				batch := sink.take()
				lines = append(lines, batch...)
				for _, line := range batch {
					seen = seen || strings.Contains(line, "structure_block.load_success")
				}
				if seen {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !seen {
				t.Fatal("missing native load feedback")
			}
			time.Sleep(500 * time.Millisecond)
			lines = append(lines, sink.take()...)
			updates := structureBEUpdatesBB(t, lines)
			byPos := map[[3]int32][]structureBEUpdate{}
			for _, u := range updates {
				byPos[u.pos] = append(byPos[u.pos], u)
			}
			for x := int32(4); x <= 11; x++ {
				count := len(byPos[[3]int32{x, -59, 4}])
				want := 0
				if x <= 6 || x >= 10 {
					want = 1
				}
				if count != want {
					t.Errorf("target x%d updates%d want%d", x, count, want)
				}
			}
			if count := len(byPos[[3]int32{4, -60, 4}]); count != 1 {
				t.Errorf("controller edit updates%d want1", count)
			}
			first := func(x int32) map[string]any {
				values := byPos[[3]int32{x, -59, 4}]
				if len(values) == 0 {
					return nil
				}
				return values[len(values)-1].tag
			}
			if tag := first(4); tag != nil {
				front, _ := tag["front_text"].(map[string]any)
				messages, _ := front["messages"].(nbt.List)
				if tag["is_waxed"] != int8(1) || front["color"] != "blue" || len(messages.Items) != 4 || messages.Items[0] != "WireSign" {
					t.Errorf("stale sign payload%+v", tag)
				}
			}
			if tag := first(5); tag != nil {
				if tag["name"] != "test:placed_structure" || tag["author"] != "PacketAuthor" || tag["metadata"] != "wire_metadata" || tag["mode"] != "DATA" {
					t.Errorf("stale structure payload%+v", tag)
				}
			}
			if tag := first(6); tag != nil {
				if tag["name"] != "test:placed_jigsaw" || tag["target"] != "test:next" || tag["selection_priority"] != int32(3) || tag["placement_priority"] != int32(7) {
					t.Errorf("stale jigsaw payload%+v", tag)
				}
			}
			if tag := first(11); tag != nil {
				sd, _ := tag["SpawnData"].(map[string]any)
				ent, _ := sd["entity"].(map[string]any)
				if ent["id"] != "minecraft:pig" || tag["SpawnPotentials"] != nil {
					t.Errorf("stale spawner update%+v", tag)
				}
			}
			for _, x := range []int32{4, 5, 6, 10, 11} {
				if first(x) == nil {
					t.Errorf("missing compound tag at x%d", x)
				}
				if tag := first(x); tag != nil {
					for _, key := range []string{"id", "x", "y", "z", "keepPacked"} {
						if tag[key] != nil {
							t.Errorf("update x%d leaked save-frame field%s", x, key)
						}
					}
				}
			}
			counts := []int{}
			for x := int32(4); x <= 11; x++ {
				counts = append(counts, len(byPos[[3]int32{x, -59, 4}]))
			}
			t.Logf("native BE updates sign/structure/jigsaw/command/chest/furnace/brushable/spawner%v", counts)
		})
	}
}
