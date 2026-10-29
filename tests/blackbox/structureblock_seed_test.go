package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
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

type structureSeedObservation struct {
	blocks   string
	lootSeed int64
	hasLoot  bool
}

func TestStructureBlockSeededPlacementParity(t *testing.T) {
	intList := func(x, y, z int32) nbt.List { return nbt.List{Elem: nbt.TagInt, Items: []any{x, y, z}} }
	palettes := nbt.List{Elem: nbt.TagList}
	for _, name := range []string{"stone", "gold_block"} {
		palettes.Items = append(palettes.Items, nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Name": "minecraft:" + name}, map[string]any{"Name": "minecraft:chest", "Properties": map[string]any{"facing": "north", "type": "single", "waterlogged": "false"}}}})
	}
	blocks := nbt.List{Elem: nbt.TagCompound}
	for x := int32(0); x < 8; x++ {
		blocks.Items = append(blocks.Items, map[string]any{"pos": intList(x, 0, 0), "state": int32(0)})
	}
	blocks.Items = append(blocks.Items, map[string]any{"pos": intList(8, 0, 0), "state": int32(1), "nbt": map[string]any{"id": "minecraft:chest", "LootTable": "minecraft:chests/simple_dungeon", "LootTableSeed": int64(12345)}})
	native := map[string]any{"DataVersion": int32(4903), "size": intList(9, 1, 1), "palettes": palettes, "blocks": blocks, "entities": nbt.List{Elem: nbt.TagEnd}}
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	if e := nbt.WriteNamed(zw, "", native); e != nil {
		t.Fatal(e)
	}
	if e := zw.Close(); e != nil {
		t.Fatal(e)
	}
	mcDir := t.TempDir()
	path := filepath.Join(mcDir, "generated", "test", "structure", "seeded.nbt")
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, raw.Bytes(), 0644); e != nil {
		t.Fatal(e)
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("StructureSeed"), "world/generated/test/structure/seeded.nbt": raw.String()}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.WorldDir = mcDir })
	defer tg.cleanup()
	samples := []struct {
		seed      int64
		integrity float32
		want      structureSeedObservation
	}{
		{17, 1, structureSeedObservation{"ggggggggc", -4937981208836185383, true}},
		{17, .5, structureSeedObservation{"aaaggaaga", 0, false}},
		{42, .75, structureSeedObservation{"ggggagggc", -5025562857975149833, true}},
		{-42, .5, structureSeedObservation{"ssasassaa", 0, false}},
		{17, 0, structureSeedObservation{"aaaaaaaaa", 0, false}},
		{1000, 1, structureSeedObservation{"ggggggggc", -5346144739450824145, true}},
		{-9223372036854775808, 1, structureSeedObservation{"ggggggggc", -4962768465676381896, true}},
	}
	for _, tc := range []struct{ tag, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "StructureSeed")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			counter := 0
			barrier := func() []string {
				marker := fmt.Sprintf("STRUCT_SEED_DONE_%d", counter)
				counter++
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
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
				t.Fatalf("missing%s", key)
			}
			tile := func() map[string]any {
				counter++
				tx := int32(counter)
				var b protocol.Buffer
				b.VarInt(tx)
				b.Position(12, -59, 4)
				if e := pw.WritePacket(server.IDPlayBlockEntityTagQuerySB, b.Bytes()); e != nil {
					t.Fatal(e)
				}
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range sink.take() {
						colon := strings.IndexByte(line, ':')
						if colon < 0 {
							continue
						}
						id, e := strconv.ParseInt(line[:colon], 0, 32)
						if e != nil || int32(id) != server.IDPlayTagQueryCB {
							continue
						}
						r := bytes.NewReader([]byte(line[colon+1:]))
						got, e := protocol.ReadVarInt(r)
						if e != nil || got != tx {
							continue
						}
						first, e := r.ReadByte()
						if e != nil {
							t.Fatal(e)
						}
						if first == nbt.TagEnd {
							return nil
						}
						_ = r.UnreadByte()
						value, e := nbt.ReadUnnamed(r)
						if e != nil {
							t.Fatal(e)
						}
						tag, _ := value.(map[string]any)
						return tag
					}
					time.Sleep(20 * time.Millisecond)
				}
				t.Fatal("missing native tag query")
				return nil
			}
			sendCommand(t, pw, "tick freeze")
			sendCommand(t, pw, "setblock 4 -60 4 minecraft:structure_block[mode=load]")
			barrier()
			for i, sample := range samples {
				sendCommand(t, pw, "fill 4 -59 4 12 -59 4 minecraft:air")
				barrier()
				writeStructureEditBB(t, pw, [3]int32{4, -60, 4}, 2, 1, "test:seeded", [3]byte{0, 1, 0}, [3]byte{9, 1, 1}, 0, 0, "", sample.integrity, sample.seed, 1)
				feedback("structure_block.load_success")
				for x := 0; x < 9; x++ {
					for _, kind := range []struct{ name, token string }{{"stone", "s"}, {"gold_block", "g"}, {"chest", "c"}, {"air", "a"}} {
						sendCommand(t, pw, fmt.Sprintf("execute if block %d -59 4 minecraft:%s run say STRUCT_SEED_%d_%s", 4+x, kind.name, x, kind.token))
					}
				}
				lines := barrier()
				var observed structureSeedObservation
				for x := 0; x < 9; x++ {
					found := "?"
					matches := 0
					for _, token := range []string{"s", "g", "c", "a"} {
						if sawMarker(lines, fmt.Sprintf("STRUCT_SEED_%d_%s", x, token)) {
							found = token
							matches++
						}
					}
					if matches != 1 {
						t.Fatalf("block %d matched %d state probes", x, matches)
					}
					observed.blocks += found
				}
				if strings.Contains(observed.blocks, "?") {
					t.Fatalf("missing block-state observation %q", observed.blocks)
				}
				if tag := tile(); tag != nil {
					observed.lootSeed, observed.hasLoot = tag["LootTableSeed"].(int64)
				}
				t.Logf("seed%d integrity%g: %+v", sample.seed, sample.integrity, observed)
				if observed != sample.want {
					t.Errorf("sample%d got%+v want%+v", i, observed, sample.want)
				}
			}
		})
	}
}
