//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeZeroDyedColorParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ColorCmd", "ColorSlot")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.SetOpLevel("ColorCmd", 4)
		s.SetOpLevel("ColorSlot", 4)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		for _, mode := range []string{"commands", "creative"} {
			t.Run(target.name+"/"+mode, func(t *testing.T) {
				user := "ColorCmd"
				chestX := int32(1)
				if mode == "creative" {
					user = "ColorSlot"
					chestX = 3
				}
				c, pr, pw := joinPlayBlackboxView(t, target.addr, user, 1)
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
					marker := fmt.Sprintf("COLOR_%s_%d", user, serial)
					sendCommand(t, pw, cmd)
					sendCommand(t, pw, "say "+marker)
					return waitForExactSayBB(t, sink, marker, 10*time.Second)
				}
				command("tp @s 0.5 -60 0.5 0 0")
				loaded := false
				deadline := time.Now().Add(60 * time.Second)
				for !loaded && time.Now().Before(deadline) {
					loaded = structureSaidBB(command(fmt.Sprintf("execute if loaded %d -60 0 run say COLOR_CHEST_READY", chestX)), "COLOR_CHEST_READY")
				}
				if !loaded {
					t.Fatal("chest target not loaded")
				}
				placed := false
				for _, line := range command(fmt.Sprintf("setblock %d -60 0 minecraft:chest", chestX)) {
					key, _, ok := componentArgs(line)
					if ok && key == "commands.setblock.success" {
						placed = true
					}
				}
				if !placed {
					t.Fatal("chest placement did not succeed")
				}
				if mode == "commands" {
					command(fmt.Sprintf(`item replace block %d -60 0 container.0 with minecraft:leather_helmet[minecraft:dyed_color=0]`, chestX))
					command(fmt.Sprintf(`item replace block %d -60 0 container.1 with minecraft:leather_helmet[minecraft:dyed_color=1193046]`, chestX))
					command(fmt.Sprintf(`item replace block %d -60 0 container.2 with minecraft:leather_helmet`, chestX))
				} else {
					command(`item replace entity @s weapon.mainhand with minecraft:leather_helmet[minecraft:dyed_color=1193046]`)
					creative := func(present bool, rgb int32) {
						var body protocol.Buffer
						body.Short(36)
						body.VarInt(1)
						body.VarInt(int32(world.ItemIDs["leather_helmet"]))
						if present {
							body.VarInt(1)
						} else {
							body.VarInt(0)
						}
						body.VarInt(0)
						if present {
							body.VarInt(44)
							body.VarInt(4)
							body.Int(rgb)
						}
						if err := pw.WritePacket(server.IDPlayCreativeSlotSB, body.Bytes()); err != nil {
							t.Fatal(err)
						}
						var stats protocol.Buffer
						stats.VarInt(1)
						if err := pw.WritePacket(server.IDPlayClientStatusSB, stats.Bytes()); err != nil {
							t.Fatal(err)
						}
						done := false
						deadline := time.Now().Add(10 * time.Second)
						for !done && time.Now().Before(deadline) {
							for _, line := range sink.take() {
								if strings.HasPrefix(line, fmt.Sprintf("%#x:", server.LatestVersion().IDs.PlayAwardStats)) {
									done = true
								}
							}
							if !done {
								time.Sleep(10 * time.Millisecond)
							}
						}
						if !done {
							t.Fatal("creative statistics owner receipt missing")
						}
					}
					creative(true, 0)
					command(fmt.Sprintf(`item replace block %d -60 0 container.0 from entity @s weapon.mainhand`, chestX))
					creative(true, 1)
					command(fmt.Sprintf(`item replace block %d -60 0 container.1 from entity @s weapon.mainhand`, chestX))
					creative(false, 0)
					command(fmt.Sprintf(`item replace block %d -60 0 container.2 from entity @s weapon.mainhand`, chestX))
				}
				var query protocol.Buffer
				query.VarInt(91)
				query.Position(chestX, -60, 0)
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
				if !ok || items.Elem != nbt.TagCompound || len(items.Items) != 3 {
					t.Fatalf("Items%#v", root["Items"])
				}
				wants := []any{int32(0), int32(1193046), nil}
				if mode == "creative" {
					wants[1] = int32(1)
				}
				ids := []string{"leather_helmet", "leather_helmet", "leather_helmet"}
				keys := []string{"dyed_color", "dyed_color", "dyed_color"}
				found := [3]bool{}
				for _, value := range items.Items {
					entry, ok := value.(map[string]any)
					if !ok {
						t.Fatalf("item%T", value)
					}
					slot, ok := entry["Slot"].(int8)
					if !ok || slot < 0 || slot > 2 || found[slot] {
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
}
