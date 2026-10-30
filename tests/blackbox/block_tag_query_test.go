//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBlockTagQueryParity(t *testing.T) {
	var ops []string
	for _, v := range []struct {
		name  string
		level int
	}{{"BlockQueryAdmin", 4}, {"BlockQueryGM", 2}, {"BlockQueryLow", 1}} {
		ops = append(ops, fmt.Sprintf(`{"uuid":"%s","name":"%s","level":%d,"bypassesPlayerLimit":false}`, server.DashUUID(server.OfflineUUID(v.name)), v.name, v.level))
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": "[" + strings.Join(ops, ",") + "]"}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("BlockQueryAdmin", 4)
		s.SetOpLevel("BlockQueryGM", 2)
		s.SetOpLevel("BlockQueryLow", 1)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			ids := server.LatestVersion().IDs
			type actor struct {
				pw   *protocol.Writer
				sink *chatSink
			}
			join := func(name string) actor {
				c, pr, pw := joinPlayBlackboxView(t, target.addr, name, 1)
				t.Cleanup(func() { c.Close() })
				drainToJoinBlackbox(t, target.name, pr)
				awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
				sink := newChatSinkTP(pr, pw)
				sink.mu.Lock()
				sink.cap = 1 << 20
				sink.mu.Unlock()
				return actor{pw, sink}
			}
			admin := join("BlockQueryAdmin")
			serial := 0
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("BLOCK_QUERY_COMMAND_%d", serial)
				sendCommand(t, admin.pw, cmd)
				sendCommand(t, admin.pw, "say "+marker)
				return waitForExactSayBB(t, admin.sink, marker, 10*time.Second)
			}
			command("tick freeze")
			command("tp BlockQueryAdmin 0.5 -60 0.5 0 0")
			for _, cmd := range []string{
				`setblock 1 -60 0 minecraft:chest`,
				`setblock 4 -60 0 minecraft:chest{Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`,
				`setblock 7 -60 0 minecraft:furnace{lit_time_remaining:9s,lit_total_time:20s,cooking_time_spent:4s,cooking_total_time:200s,RecipesUsed:{"minecraft:iron_ingot_from_smelting_raw_iron":2},Items:[{Slot:0b,id:"minecraft:raw_iron",count:2}]}`,
				`setblock 10 -60 0 minecraft:chest{LootTable:"minecraft:chests/simple_dungeon"}`,
				`setblock 13 -60 0 minecraft:structure_block{mode:"DATA",name:"minecraft:query_template",posX:2,posY:3,posZ:4,sizeX:5,sizeY:6,sizeZ:7}`,
				`setblock 15 -60 0 minecraft:stone`,
			} {
				command(cmd)
			}
			command("item replace block 4 -60 0 container.26 with minecraft:leather_helmet[minecraft:damage=2]")
			command("data merge block 10 -60 0 {LootTableSeed:42L}")
			writeQuery := func(a actor, pos [3]int32, tx int32) {
				var b protocol.Buffer
				b.VarInt(tx)
				b.Position(pos[0], pos[1], pos[2])
				if e := a.pw.WritePacket(server.IDPlayBlockEntityTagQuerySB, b.Bytes()); e != nil {
					t.Fatal(e)
				}
			}
			readTag := func(line string) (int32, any, bool) {
				prefix := fmt.Sprintf("%#x:", ids.PlayTagQueryCB)
				if !strings.HasPrefix(line, prefix) {
					return 0, nil, false
				}
				r := bytes.NewReader([]byte(strings.TrimPrefix(line, prefix)))
				tx, e := protocol.ReadVarInt(r)
				if e != nil {
					t.Fatal(e)
				}
				rootTag, e := r.ReadByte()
				if e != nil {
					t.Fatal(e)
				}
				if rootTag == nbt.TagEnd {
					if r.Len() != 0 {
						t.Fatalf("null tag trailing%d bytes", r.Len())
					}
					return tx, nil, true
				}
				if e := r.UnreadByte(); e != nil {
					t.Fatal(e)
				}
				root, e := nbt.ReadUnnamed(r)
				if e != nil || r.Len() != 0 {
					t.Fatalf("query frame error%v remaining%d", e, r.Len())
				}
				return tx, root, true
			}
			query := func(a actor, pos [3]int32, tx int32) any {
				writeQuery(a, pos, tx)
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range a.sink.take() {
						got, root, ok := readTag(line)
						if !ok {
							continue
						}
						if got != tx {
							t.Fatalf("transaction%d want%d", got, tx)
						}
						return root
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("missing query%d", tx)
				return nil
			}
			compound := func(root any) map[string]any {
				m, ok := root.(map[string]any)
				if !ok {
					t.Fatalf("expected compound got%T", root)
				}
				for _, key := range []string{"id", "x", "y", "z", "keepPacked"} {
					if v, ok := m[key]; ok {
						t.Errorf("saveWithoutMetadata contains%s=%#v", key, v)
					}
				}
				return m
			}
			item := func(m map[string]any, slot int8, id string, count int32) map[string]any {
				items, ok := m["Items"].(nbt.List)
				if !ok {
					t.Fatalf("Items%#v", m["Items"])
				}
				for _, value := range items.Items {
					entry, ok := value.(map[string]any)
					if ok && entry["Slot"] == slot && entry["id"] == id && entry["count"] == count {
						return entry
					}
				}
				t.Fatalf("missing item%s slot%d count%d: %#v", id, slot, count, items)
				return nil
			}
			empty := compound(query(admin, [3]int32{1, -60, 0}, math.MinInt32))
			if list, ok := empty["Items"].(nbt.List); !ok || len(list.Items) != 0 {
				t.Errorf("virgin chest Items%#v", empty["Items"])
			}
			filled := compound(query(admin, [3]int32{4, -60, 0}, math.MaxInt32))
			item(filled, 0, "minecraft:diamond", 3)
			helmet := item(filled, 26, "minecraft:leather_helmet", 1)
			components, ok := helmet["components"].(map[string]any)
			if !ok || components["minecraft:damage"] != int32(2) {
				t.Errorf("helmet components%#v", helmet["components"])
			}
			furnace := compound(query(admin, [3]int32{7, -60, 0}, 0))
			item(furnace, 0, "minecraft:raw_iron", 2)
			for key, want := range map[string]int16{"lit_time_remaining": 9, "lit_total_time": 20, "cooking_time_spent": 4, "cooking_total_time": 200} {
				if furnace[key] != want {
					t.Errorf("furnace%s=%#v want%#v", key, furnace[key], want)
				}
			}
			if !reflect.DeepEqual(furnace["RecipesUsed"], map[string]any{"minecraft:iron_ingot_from_smelting_raw_iron": int32(2)}) {
				t.Errorf("RecipesUsed%#v", furnace["RecipesUsed"])
			}
			loot := compound(query(admin, [3]int32{10, -60, 0}, 0))
			if loot["LootTable"] != "minecraft:chests/simple_dungeon" || loot["LootTableSeed"] != int64(42) {
				t.Errorf("loot fields%#v", loot)
			}
			if _, ok := loot["Items"]; ok {
				t.Errorf("query unpacked loot%#v", loot)
			}
			structure := compound(query(admin, [3]int32{13, -60, 0}, -7))
			for key, want := range map[string]any{"mode": "DATA", "name": "minecraft:query_template", "posX": int32(2), "posY": int32(3), "posZ": int32(4), "sizeX": int32(5), "sizeY": int32(6), "sizeZ": int32(7)} {
				if structure[key] != want {
					t.Errorf("structure%s=%#v want%#v", key, structure[key], want)
				}
			}
			for i, pos := range [][3]int32{{15, -60, 0}, {-2, -60, 0}, {0, 1000, 0}} {
				if root := query(admin, pos, int32(40+i)); root != nil {
					t.Errorf("nonBE%v returned%#v", pos, root)
				}
			}
			gm := join("BlockQueryGM")
			command("gamemode survival BlockQueryGM")
			item(compound(query(gm, [3]int32{4, -60, 0}, -20)), 0, "minecraft:diamond", 3)
			for _, name := range []string{"BlockQueryLow", "BlockQueryNone"} {
				a := join(name)
				a.sink.take()
				writeQuery(a, [3]int32{4, -60, 0}, -99)
				var stats protocol.Buffer
				stats.VarInt(1)
				if e := a.pw.WritePacket(server.IDPlayClientStatusSB, stats.Bytes()); e != nil {
					t.Fatal(e)
				}
				done := false
				deadline := time.Now().Add(10 * time.Second)
				for !done && time.Now().Before(deadline) {
					for _, line := range a.sink.take() {
						if _, _, ok := readTag(line); ok {
							t.Fatalf("%s got denied query reply", name)
						}
						if strings.HasPrefix(line, fmt.Sprintf("%#x:", ids.PlayAwardStats)) {
							done = true
						}
					}
					if !done {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if !done {
					t.Fatalf("%s owner stats barrier missing", name)
				}
				t.Logf("%s denied, live owner barrier", name)
			}
			command("execute in minecraft:the_nether run tp BlockQueryAdmin 0.5 200 0.5 0 0")
			if e := admin.pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); e != nil {
				t.Fatal(e)
			}
			loaded := false
			loadDeadline := time.Now().Add(60 * time.Second)
			for !loaded && time.Now().Before(loadDeadline) {
				lines := command("execute in minecraft:the_nether if loaded 4 200 0 run say BLOCK_QUERY_NETHER_LOADED")
				loaded = structureSaidBB(lines, "BLOCK_QUERY_NETHER_LOADED")
			}
			if !loaded {
				t.Fatal("Nether target never became command-visible loaded")
			}
			placed := false
			for _, line := range command(`execute in minecraft:the_nether run setblock 4 200 0 minecraft:barrel{Items:[{Slot:0b,id:"minecraft:apple",count:5}]}`) {
				key, _, ok := componentArgs(line)
				if ok && key == "commands.setblock.success" {
					placed = true
				}
			}
			if !placed {
				t.Fatal("Nether barrel setup did not succeed")
			}
			item(compound(query(admin, [3]int32{4, 200, 0}, -25)), 0, "minecraft:apple", 5)
			if root := query(admin, [3]int32{4, -60, 0}, -26); root != nil {
				t.Errorf("old dimension target resolved%#v", root)
			}
			t.Logf("native-compatible query cases: empty,inventory,components,furnace,loot,structure,null,permission,dimension")
		})
	}
}
