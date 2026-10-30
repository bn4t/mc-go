//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeScalarComponentsSaveParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("MapWriter")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.SetOpLevel("MapWriter", 4) })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "MapWriter", 1)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			sink.mu.Lock()
			sink.cap = 1 << 20
			sink.mu.Unlock()
			serial := 0
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("MAP_WRITER_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tp @s 0.5 -60 0.5 0 0")
			loaded := false
			deadline := time.Now().Add(60 * time.Second)
			for !loaded && time.Now().Before(deadline) {
				loaded = structureSaidBB(command("execute if loaded 1 -60 0 run say MAP_CHEST_READY"), "MAP_CHEST_READY")
			}
			if !loaded {
				t.Fatal("chest target not loaded")
			}
			placed := false
			for _, line := range command("setblock 1 -60 0 minecraft:chest") {
				key, _, ok := componentArgs(line)
				if ok && key == "commands.setblock.success" {
					placed = true
				}
			}
			if !placed {
				t.Fatal("chest placement did not succeed")
			}
			command(`item replace block 1 -60 0 container.0 with minecraft:filled_map[minecraft:map_id=4]`)
			command(`item replace block 1 -60 0 container.1 with minecraft:filled_map[minecraft:map_id=0]`)
			command(`item replace block 1 -60 0 container.2 with minecraft:filled_map`)
			command(`item replace block 1 -60 0 container.3 with minecraft:stone[minecraft:ominous_bottle_amplifier=4]`)
			command(`item replace block 1 -60 0 container.4 with minecraft:stone[minecraft:ominous_bottle_amplifier=0]`)
			command(`item replace block 1 -60 0 container.5 with minecraft:stone`)
			command(`item replace block 1 -60 0 container.6 with minecraft:leather_helmet[minecraft:dyed_color=1193046]`)
			command(`item replace block 1 -60 0 container.7 with minecraft:leather_helmet`)
			var query protocol.Buffer
			query.VarInt(91)
			query.Position(1, -60, 0)
			if e := pw.WritePacket(server.IDPlayBlockEntityTagQuerySB, query.Bytes()); e != nil {
				t.Fatal(e)
			}
			var root map[string]any
			deadline = time.Now().Add(10 * time.Second)
			for root == nil && time.Now().Before(deadline) {
				for _, line := range sink.take() {
					prefix := fmt.Sprintf("%#x:", server.IDPlayTagQueryCB)
					if !strings.HasPrefix(line, prefix) {
						continue
					}
					r := bytes.NewReader([]byte(strings.TrimPrefix(line, prefix)))
					tx, e := protocol.ReadVarInt(r)
					if e != nil || tx != 91 {
						t.Fatalf("query transaction%d error%v", tx, e)
					}
					value, e := nbt.ReadUnnamed(r)
					if e != nil || r.Len() != 0 {
						t.Fatalf("query frame error%v remaining%d", e, r.Len())
					}
					var ok bool
					root, ok = value.(map[string]any)
					if !ok {
						t.Fatalf("query root%T", value)
					}
				}
				if root == nil {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if root == nil {
				t.Fatal("query receipt missing")
			}
			items, ok := root["Items"].(nbt.List)
			if !ok || items.Elem != nbt.TagCompound || len(items.Items) != 8 {
				t.Fatalf("Items%#v", root["Items"])
			}
			wants := []any{int32(4), int32(0), nil, int32(4), int32(0), nil, int32(1193046), nil}
			ids := []string{"filled_map", "filled_map", "filled_map", "stone", "stone", "stone", "leather_helmet", "leather_helmet"}
			keys := []string{"map_id", "map_id", "map_id", "ominous_bottle_amplifier", "ominous_bottle_amplifier", "ominous_bottle_amplifier", "dyed_color", "dyed_color"}
			found := [8]bool{}
			for _, value := range items.Items {
				entry, ok := value.(map[string]any)
				if !ok {
					t.Fatalf("item%T", value)
				}
				slot, ok := entry["Slot"].(int8)
				if !ok || slot < 0 || slot > 7 || found[slot] {
					t.Fatalf("slot%#v", entry["Slot"])
				}
				found[slot] = true
				if entry["id"] != "minecraft:"+ids[slot] || entry["count"] != int32(1) {
					t.Errorf("slot%d identity/count%#v", slot, entry)
				}
				components, _ := entry["components"].(map[string]any)
				got, present := components["minecraft:"+keys[slot]]
				if wants[slot] == nil {
					if present {
						t.Errorf("absent component gained%#v", got)
					}
				} else if !present || !reflect.DeepEqual(got, wants[slot]) {
					t.Errorf("slot%d component%#v present%v want%#v", slot, got, present, wants[slot])
				}
			}
			for slot, seen := range found {
				if !seen {
					t.Fatalf("item slot%d missing", slot)
				}
			}
		})
	}
}
