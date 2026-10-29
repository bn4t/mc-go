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

func TestStructureBlockSaveLoadParity(t *testing.T) {
	// A native file with an author field independently proves the current file
	// loader ignores that legacy field and uses the repository's default '?'.
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	list := func(v ...any) nbt.List { return nbt.List{Elem: nbt.TagInt, Items: v} }
	native := map[string]any{"DataVersion": int32(4903), "author": "ignored", "size": list(int32(1), int32(1), int32(1)), "palette": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Name": "minecraft:stone"}}}, "blocks": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"pos": list(int32(0), int32(0), int32(0)), "state": int32(0)}}}, "entities": nbt.List{Elem: nbt.TagEnd}}
	if e := nbt.WriteNamed(zw, "", native); e != nil {
		t.Fatal(e)
	}
	if e := zw.Close(); e != nil {
		t.Fatal(e)
	}
	mcDir := t.TempDir()
	path := filepath.Join(mcDir, "generated", "test", "structure", "native.nbt")
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, raw.Bytes(), 0644); e != nil {
		t.Fatal(e)
	}
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("StructureFlow"), "world/generated/test/structure/native.nbt": raw.String()}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.WorldDir = mcDir })
	defer targets.cleanup()
	for _, tc := range []struct{ tag, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "StructureFlow")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			counter := 0
			barrier := func() []string {
				marker := fmt.Sprintf("STRUCTURE_FLOW_DONE_%d", counter)
				counter++
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			check := func(condition string) {
				marker := fmt.Sprintf("STRUCTURE_FLOW_PASS_%d", counter)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !sawMarker(barrier(), marker) {
					t.Errorf("failed%s", condition)
				}
			}
			feedback := func(key string) {
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range sink.take() {
						if strings.Contains(line, key) {
							return
						}
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Errorf("missing%s", key)
			}
			sendCommand(t, pw, "tick freeze")
			for _, cmd := range []string{
				"setblock 4 -60 4 minecraft:structure_block[mode=save]",
				"setblock 5 -59 4 minecraft:stone",
				`setblock 6 -59 4 minecraft:chest[facing=north]{Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`,
				"setblock 7 -59 4 minecraft:structure_void",
				`summon minecraft:villager 5.5 -59 4.5 {NoAI:1b,NoGravity:1b,Tags:[SavedByStructure]}`,
				"setblock 12 -60 4 minecraft:structure_block[mode=load]",
				"setblock 12 -59 6 minecraft:gold_block",
			} {
				sendCommand(t, pw, cmd)
			}
			barrier()
			writeStructureEditBB(t, pw, [3]int32{12, -60, 4}, 2, 1, "missing_piece", [3]byte{}, [3]byte{}, 0, 0, "", 1, 17, 1)
			feedback("structure_block.load_not_found")
			writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, 1, 0, "test:piece", [3]byte{1, 1, 0}, [3]byte{3, 1, 1}, 0, 0, "", 1, 17, 0)
			feedback("structure_block.save_success")
			writeStructureEditBB(t, pw, [3]int32{12, -60, 4}, 2, 1, "test:piece", [3]byte{0, 1, 0}, [3]byte{}, 0, 1, "", 1, 17, 0)
			feedback("structure_block.load_prepare")
			check(`if data block 12 -60 4 {sizeX:3,sizeY:1,sizeZ:1}`)
			check(`if block 12 -59 4 minecraft:air`)
			writeStructureEditBB(t, pw, [3]int32{12, -60, 4}, 2, 1, "test:piece", [3]byte{0, 1, 0}, [3]byte{3, 1, 1}, 0, 1, "", 1, 17, 0)
			feedback("structure_block.load_success")
			check(`if block 12 -59 4 minecraft:stone`)
			check(`if block 12 -59 5 minecraft:chest[facing=east]`)
			check(`if data block 12 -59 5 {Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`)
			check(`if block 12 -59 6 minecraft:gold_block`)
			check(`if entity @e[tag=SavedByStructure,x=12,y=-59,z=4,dx=2,dy=2,dz=2]`)
			sendCommand(t, pw, "setblock 20 -60 4 minecraft:structure_block[mode=load]")
			barrier()
			writeStructureEditBB(t, pw, [3]int32{20, -60, 4}, 2, 1, "test:native", [3]byte{0, 1, 0}, [3]byte{}, 0, 0, "", 1, 17, 1)
			feedback("structure_block.load_prepare")
			check(`if data block 20 -60 4 {sizeX:1,sizeY:1,sizeZ:1,author:"?"}`)
			writeStructureEditBB(t, pw, [3]int32{20, -60, 4}, 2, 1, "test:native", [3]byte{0, 1, 0}, [3]byte{1, 1, 1}, 0, 0, "", 1, 17, 1)
			feedback("structure_block.load_success")
			check(`if block 20 -59 4 minecraft:stone`)
			for _, sample := range []struct{ name, key string }{
				{"example:nested/part", "structure_block.save_success"}, {"..:escape", "structure_block.invalid_structure_name"}, {"UPPER:piece", "structure_block.invalid_structure_name"},
				{"example:../escape", "structure_block.save_failure"}, {"example:/absolute", "structure_block.save_failure"}, {"example:a//b", "structure_block.save_failure"}, {"example:a/./b", "structure_block.save_failure"}, {"example:a/../b", "structure_block.save_failure"},
				{"example:.", "structure_block.save_success"}, {"example:..", "structure_block.save_success"}, {"example:a/..", "structure_block.save_success"}, {"example:a/", "structure_block.save_success"}, {"example:", "structure_block.save_success"}, {".:dot_namespace", "structure_block.save_success"},
				{"example:con", "structure_block.save_failure"}, {"example:com1", "structure_block.save_failure"}, {"example:lpt9/data", "structure_block.save_failure"}, {"example:com10", "structure_block.save_success"}, {"example:a./data", "structure_block.save_failure"},
			} {
				writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, 1, 0, sample.name, [3]byte{1, 1, 0}, [3]byte{1, 1, 1}, 0, 0, "", 1, 17, 1)
				feedback(sample.key)
			}

		})
	}
}
