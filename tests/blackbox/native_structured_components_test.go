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

func TestNativeStructuredComponentsSaveParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("StructureWriter")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.SetOpLevel("StructureWriter", 4) })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "StructureWriter", 1)
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
				marker := fmt.Sprintf("STRUCTURED_WRITER_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tp @s 0.5 -60 0.5 0 0")
			loaded := false
			deadline := time.Now().Add(60 * time.Second)
			for !loaded && time.Now().Before(deadline) {
				loaded = structureSaidBB(command("execute if loaded 1 -60 0 run say STRUCTURED_CHEST_READY"), "STRUCTURED_CHEST_READY")
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
			command(`item replace block 1 -60 0 container.0 with minecraft:stone[minecraft:suspicious_stew_effects=[{id:"minecraft:night_vision"},{id:"minecraft:blindness",duration:0}]]`)
			command(`item replace block 1 -60 0 container.1 with minecraft:stone[minecraft:suspicious_stew_effects=[{id:"minecraft:night_vision",duration:160},{id:"minecraft:blindness",duration:200}]]`)
			command(`item replace block 1 -60 0 container.2 with minecraft:stone`)
			command(`item replace block 1 -60 0 container.3 with minecraft:stone[minecraft:pot_decorations=["minecraft:brick","minecraft:angler_pottery_sherd","minecraft:archer_pottery_sherd","minecraft:skull_pottery_sherd"]]`)
			command(`item replace block 1 -60 0 container.4 with minecraft:stone`)
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
			if !ok || items.Elem != nbt.TagCompound || len(items.Items) != 5 {
				t.Fatalf("Items%#v", root["Items"])
			}
			wants := []any{nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "minecraft:night_vision"}, map[string]any{"id": "minecraft:blindness", "duration": int32(0)}}}, nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "minecraft:night_vision"}, map[string]any{"id": "minecraft:blindness", "duration": int32(200)}}}, nil, nbt.List{Elem: nbt.TagString, Items: []any{"minecraft:brick", "minecraft:angler_pottery_sherd", "minecraft:archer_pottery_sherd", "minecraft:skull_pottery_sherd"}}, nil}
			ids := []string{"stone", "stone", "stone", "stone", "stone"}
			keys := []string{"suspicious_stew_effects", "suspicious_stew_effects", "suspicious_stew_effects", "pot_decorations", "pot_decorations"}
			found := [5]bool{}
			for _, value := range items.Items {
				entry, ok := value.(map[string]any)
				if !ok {
					t.Fatalf("item%T", value)
				}
				slot, ok := entry["Slot"].(int8)
				if !ok || slot < 0 || slot > 4 || found[slot] {
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
