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

func TestNativeStoredEnchantmentsSaveParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("StoredWriter")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.SetOpLevel("StoredWriter", 4) })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "StoredWriter", 1)
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
				marker := fmt.Sprintf("STORED_WRITER_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tp @s 0.5 -60 0.5 0 0")
			loaded := false
			deadline := time.Now().Add(60 * time.Second)
			for !loaded && time.Now().Before(deadline) {
				loaded = structureSaidBB(command("execute if loaded 1 -60 0 run say STORED_CHEST_READY"), "STORED_CHEST_READY")
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
			command(`item replace block 1 -60 0 container.0 with minecraft:enchanted_book[minecraft:stored_enchantments={"minecraft:mending":1,"minecraft:unbreaking":2}]`)
			command(`item replace block 1 -60 0 container.1 with minecraft:leather_helmet[minecraft:enchantments={"minecraft:protection":1},minecraft:stored_enchantments={"minecraft:mending":2}]`)
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
			if !ok || items.Elem != nbt.TagCompound || len(items.Items) != 2 {
				t.Fatalf("Items%#v", root["Items"])
			}
			expected := []struct {
				id             string
				normal, stored map[string]any
			}{
				{"minecraft:enchanted_book", nil, map[string]any{"minecraft:mending": int32(1), "minecraft:unbreaking": int32(2)}},
				{"minecraft:leather_helmet", map[string]any{"minecraft:protection": int32(1)}, map[string]any{"minecraft:mending": int32(2)}},
			}
			found := [2]bool{}
			for _, value := range items.Items {
				entry, ok := value.(map[string]any)
				if !ok {
					t.Fatalf("item%T", value)
				}
				slot, ok := entry["Slot"].(int8)
				if !ok || slot < 0 || slot > 1 || found[slot] {
					t.Fatalf("slot%#v", entry["Slot"])
				}
				found[slot] = true
				want := expected[slot]
				if entry["id"] != want.id || entry["count"] != int32(1) {
					t.Errorf("slot%d identity/count%#v", slot, entry)
				}
				components, ok := entry["components"].(map[string]any)
				if !ok {
					t.Fatalf("slot%d components%#v", slot, entry["components"])
				}
				if !reflect.DeepEqual(components["minecraft:stored_enchantments"], want.stored) {
					t.Errorf("slot%d stored%#v want%#v", slot, components["minecraft:stored_enchantments"], want.stored)
				}
				normal, present := components["minecraft:enchantments"]
				if want.normal == nil {
					if present {
						t.Errorf("book gained ordinary enchantments%#v", normal)
					}
				} else if !reflect.DeepEqual(normal, want.normal) {
					t.Errorf("slot%d ordinary%#v want%#v", slot, normal, want.normal)
				}
			}
			if !found[0] || !found[1] {
				t.Fatal("stored item missing")
			}
		})
	}
}
