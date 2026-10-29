package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

func TestStructureBlockStrictPlacementParity(t *testing.T) {
	ints := func(x, y, z int32) nbt.List { return nbt.List{Elem: nbt.TagInt, Items: []any{x, y, z}} }
	files := map[string]string{"ops.json": opsJSONFor("StructureStrict")}
	mcDir := t.TempDir()
	for _, asset := range []struct {
		name, block string
		props       map[string]any
		tag         map[string]any
	}{
		{"named_chest", "chest", map[string]any{"facing": "north", "type": "single", "waterlogged": "false"}, map[string]any{"id": "minecraft:chest", "CustomName": "TemplateName", "Items": nbt.List{Elem: nbt.TagEnd}}},
		{"fence", "oak_fence", map[string]any{"north": "false", "east": "false", "south": "false", "west": "false", "waterlogged": "false"}, nil},
		{"slab", "oak_slab", map[string]any{"type": "bottom", "waterlogged": "false"}, nil},
		{"tnt", "tnt", map[string]any{"unstable": "false"}, nil},
		{"stone", "stone", nil, nil},
		{"chest", "chest", map[string]any{"facing": "north", "type": "single", "waterlogged": "false"}, map[string]any{"id": "minecraft:chest", "Items": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Slot": int8(0), "id": "minecraft:iron_ingot", "count": int32(1)}}}}},
	} {
		state := map[string]any{"Name": "minecraft:" + asset.block}
		if asset.props != nil {
			state["Properties"] = asset.props
		}
		block := map[string]any{"pos": ints(0, 0, 0), "state": int32(0)}
		if asset.tag != nil {
			block["nbt"] = asset.tag
		}
		native := map[string]any{"DataVersion": int32(4903), "size": ints(1, 1, 1), "palette": nbt.List{Elem: nbt.TagCompound, Items: []any{state}}, "blocks": nbt.List{Elem: nbt.TagCompound, Items: []any{block}}, "entities": nbt.List{Elem: nbt.TagEnd}}
		var raw bytes.Buffer
		zw := gzip.NewWriter(&raw)
		if e := nbt.WriteNamed(zw, "", native); e != nil {
			t.Fatal(e)
		}
		if e := zw.Close(); e != nil {
			t.Fatal(e)
		}
		files["world/generated/test/structure/"+asset.name+".nbt"] = raw.String()
		path := filepath.Join(mcDir, "generated", "test", "structure", asset.name+".nbt")
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, raw.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "command-spam-threshold-seconds": "0"}, files, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.WorldDir = mcDir })
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "StructureStrict")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			counter := 0
			barrier := func() []string {
				marker := fmt.Sprintf("STRICT_DONE_%d", counter)
				counter++
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			check := func(condition string) {
				marker := fmt.Sprintf("STRICT_PASS_%d", counter)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !sawMarker(barrier(), marker) {
					t.Errorf("failed %s", condition)
				}
			}
			load := func(name string, strict bool) {
				flags := byte(1)
				if strict {
					flags |= 8
				}
				writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, 2, 1, "test:"+name, [3]byte{0, 1, 0}, [3]byte{1, 1, 1}, 0, 0, "", 1, 17, flags)
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range sink.take() {
						if strings.Contains(line, "structure_block.load_success") {
							return
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatalf("missing load success for %s strict%v", name, strict)
			}
			reset := func() {
				sendCommand(t, pw, "fill 2 -59 2 6 -58 6 minecraft:air")
				sendCommand(t, pw, "kill @e[type=minecraft:item]")
				sendCommand(t, pw, "kill @e[type=minecraft:tnt]")
				barrier()
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "setblock 4 -60 4 minecraft:structure_block[mode=load]")
			barrier()
			for _, strict := range []bool{false, true} {
				reset()
				sendCommand(t, pw, "setblock 3 -59 4 minecraft:oak_fence")
				sendCommand(t, pw, "setblock 5 -59 4 minecraft:stone")
				barrier()
				load("fence", strict)
				check(fmt.Sprintf("if block 4 -59 4 minecraft:oak_fence[east=%t,west=%t]", !strict, !strict))
				check(fmt.Sprintf("if block 3 -59 4 minecraft:oak_fence[east=%t]", !strict))
				reset()
				sendCommand(t, pw, "setblock 4 -59 4 minecraft:water")
				barrier()
				load("slab", strict)
				check("if block 4 -59 4 minecraft:oak_slab[waterlogged=true]")
				reset()
				sendCommand(t, pw, "setblock 5 -59 4 minecraft:redstone_block")
				barrier()
				load("tnt", strict)
				if strict {
					check("if block 4 -59 4 minecraft:tnt")
					check("unless entity @e[type=minecraft:tnt]")
				} else {
					check("if block 4 -59 4 minecraft:air")
					check("if entity @e[type=minecraft:tnt]")
				}
				reset()
				sendCommand(t, pw, `setblock 4 -59 4 minecraft:chest{Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`)
				barrier()
				load("stone", strict)
				prefix := "if"
				if strict {
					prefix = "unless"
				}
				check(prefix + ` entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:diamond"}},x=3,y=-60,z=3,dx=3,dy=3,dz=3]`)
				reset()
				sendCommand(t, pw, `setblock 4 -59 4 minecraft:chest{Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`)
				barrier()
				load("chest", strict)
				check(`unless entity @e[type=minecraft:item,nbt={Item:{id:"minecraft:diamond"}},x=3,y=-60,z=3,dx=3,dy=3,dz=3]`)
				check(`if data block 4 -59 4 {Items:[{Slot:0b,id:"minecraft:iron_ingot",count:1}]}`)
				reset()
				sendCommand(t, pw, "setblock 4 -59 4 minecraft:water")
				barrier()
				load("named_chest", strict)
				check("if data block 4 -59 4 CustomName")
				reset()
				sendCommand(t, pw, `setblock 3 -59 4 minecraft:chest[facing=north]{CustomName:"NeighborName"}`)
				barrier()
				load("named_chest", strict)
				check("if data block 4 -59 4 CustomName")
				check("if data block 3 -59 4 CustomName")
				reset()
				sendCommand(t, pw, "setblock 4 -59 4 minecraft:shulker_box")
				sendCommand(t, pw, "item replace block 4 -59 4 container.0 with minecraft:diamond 3")
				barrier()
				check(`if data block 4 -59 4 {Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`)
				load("chest", strict)
				check("unless entity @e[type=minecraft:item]")
				if strict {
					reset()
					sendCommand(t, pw, "setblock 4 -59 4 minecraft:shulker_box")
					sendCommand(t, pw, "item replace block 4 -59 4 container.0 with minecraft:diamond 3")
					barrier()
					load("stone", true)
					check("unless entity @e[type=minecraft:item]")
				}

			}
		})
	}
}
