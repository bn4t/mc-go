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
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestJigsawGenerateParity(t *testing.T) {
	list := func(v ...any) nbt.List { return nbt.List{Elem: nbt.TagInt, Items: v} }
	native := map[string]any{"DataVersion": int32(4903), "size": list(int32(1), int32(1), int32(1)), "palette": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Name": "minecraft:jigsaw", "Properties": map[string]any{"orientation": "up_north"}}}}, "blocks": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"pos": list(int32(0), int32(0), int32(0)), "state": int32(0), "nbt": map[string]any{"name": "test:anchor", "target": "minecraft:empty", "pool": "minecraft:empty", "final_state": "minecraft:gold_block", "joint": "rollable"}}}}, "entities": nbt.List{Elem: nbt.TagEnd}}

	native["size"] = list(int32(2), int32(1), int32(1))
	palette := native["palette"].(nbt.List)
	palette.Items = append(palette.Items, map[string]any{"Name": "minecraft:jigsaw", "Properties": map[string]any{"orientation": "east_up"}})
	native["palette"] = palette
	blocks := native["blocks"].(nbt.List)
	blocks.Items = append(blocks.Items, map[string]any{"pos": list(int32(1), int32(0), int32(0)), "state": int32(1), "nbt": map[string]any{"name": "test:out", "target": "test:child", "pool": "test:child", "final_state": "minecraft:stone", "joint": "rollable"}})
	native["blocks"] = blocks
	child := map[string]any{"DataVersion": int32(4903), "size": list(int32(1), int32(1), int32(1)), "palette": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Name": "minecraft:jigsaw", "Properties": map[string]any{"orientation": "west_up"}}}}, "blocks": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"pos": list(int32(0), int32(0), int32(0)), "state": int32(0), "nbt": map[string]any{"name": "test:child", "target": "minecraft:empty", "pool": "minecraft:empty", "final_state": "minecraft:diamond_block", "joint": "rollable"}}}}, "entities": nbt.List{Elem: nbt.TagEnd}}
	var childRaw bytes.Buffer
	childZip := gzip.NewWriter(&childRaw)
	if err := nbt.WriteNamed(childZip, "", child); err != nil {
		t.Fatal(err)
	}
	if err := childZip.Close(); err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	if err := nbt.WriteNamed(zw, "", native); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	pool := `{"fallback":"minecraft:empty","elements":[{"weight":1,"element":{"element_type":"minecraft:single_pool_element","location":"test:root","processors":"minecraft:empty","projection":"rigid"}}]}`
	files := map[string]string{"ops.json": opsJSONFor("JigsawGenerate"), "world/generated/test/structure/root.nbt": raw.String(), "world/datapacks/jigsaw/pack.mcmeta": `{"pack":{"description":"jigsaw wire pin","min_format":[107,1],"max_format":[107,1]}}`, "world/datapacks/jigsaw/data/test/worldgen/template_pool/root.json": pool}
	files["world/generated/test/structure/child.nbt"] = childRaw.String()
	files["world/datapacks/jigsaw/data/test/worldgen/template_pool/child.json"] = strings.ReplaceAll(pool, "test:root", "test:child")
	directory := t.TempDir()
	for name, value := range files {
		if strings.HasPrefix(name, "world/") {
			path := filepath.Join(directory, strings.TrimPrefix(name, "world/"))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(value), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, files, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.WorldDir = directory
		s.OpsRequired = true
		s.SetOpLevel("JigsawGenerate", 4)
	})
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "JigsawGenerate")
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("JG_DONE_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			check := func(condition string) {
				serial++
				marker := fmt.Sprintf("JG_PASS_%d", serial)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !sawMarker(barrier(), marker) {
					t.Errorf("failed %s", condition)
				}
			}
			generate := func(depth int32, keep bool) {
				var b protocol.Buffer
				b.Position(0, 70, 0)
				b.VarInt(depth)
				b.Bool(keep)
				if err := pw.WritePacket(server.IDPlayJigsawGenerateSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
				barrier()
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "tp @s 4 72 4")
			sendCommand(t, pw, `setblock 0 70 0 jigsaw[orientation=east_up]{name:"test:controller",target:"test:anchor",pool:"test:root"}`)
			barrier()
			generate(0, false)
			check("if block 1 69 0 minecraft:air")
			generate(1, false)
			check("if block 1 69 0 minecraft:gold_block")
			check("if block 0 69 0 minecraft:stone")
			check("if block -1 69 0 minecraft:diamond_block")
			check("if block 0 70 0 minecraft:jigsaw")
			sendCommand(t, pw, "setblock 1 69 0 air")
			barrier()
			generate(1, true)
			check("if block 1 69 0 minecraft:jigsaw")
			check(`if data block 1 69 0 {name:"test:anchor",final_state:"minecraft:gold_block"}`)
			check(`if data block -1 69 0 {name:"test:child",final_state:"minecraft:diamond_block"}`)
			sendCommand(t, pw, "setblock 1 69 0 air")
			sendCommand(t, pw, "gamemode survival")
			barrier()
			generate(1, false)
			check("if block 1 69 0 minecraft:air")
			sendCommand(t, pw, "gamemode creative")
			sendCommand(t, pw, `data merge block 0 70 0 {target:"test:missing"}`)
			barrier()
			generate(1, false)
			check("if block 1 69 0 minecraft:air")
		})
	}
}
