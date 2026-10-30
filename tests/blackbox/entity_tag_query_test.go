//go:build blackbox

package blackbox

import (
	"bytes"
	"encoding/binary"
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

type tagQueryActorBB struct {
	id   int32
	pw   *protocol.Writer
	sink *chatSink
}

func TestEntityTagQueryParity(t *testing.T) {
	var entries []string
	for _, row := range []struct {
		name  string
		level int
	}{{"QueryAdmin", 4}, {"QueryGM", 2}, {"QueryLow", 1}} {
		entries = append(entries, fmt.Sprintf(`{"uuid":"%s","name":"%s","level":%d,"bypassesPlayerLimit":false}`, server.DashUUID(server.OfflineUUID(row.name)), row.name, row.level))
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": "[" + strings.Join(entries, ",") + "]"}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("QueryAdmin", 4)
		s.SetOpLevel("QueryGM", 2)
		s.SetOpLevel("QueryLow", 1)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			ids := server.LatestVersion().IDs
			join := func(name string) tagQueryActorBB {
				c, pr, pw := joinPlayBlackboxView(t, target.addr, name, 1)
				t.Cleanup(func() { c.Close() })
				var id int32
				for id == 0 {
					p, e := pr.ReadPacket()
					if e != nil {
						t.Fatal(e)
					}
					if p.ID == server.IDPlayJoinGame {
						id, e = protocol.NewPacketReader(p.Data).Int()
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
				sink := newChatSinkTP(pr, pw)
				sink.mu.Lock()
				sink.cap = 1 << 20
				sink.mu.Unlock()
				return tagQueryActorBB{id, pw, sink}
			}
			admin := join("QueryAdmin")
			serial := 0
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("ENTITY_QUERY_CMD_%d", serial)
				sendCommand(t, admin.pw, cmd)
				sendCommand(t, admin.pw, "say "+marker)
				return waitForExactSayBB(t, admin.sink, marker, 10*time.Second)
			}
			command("tick freeze")
			command("tp QueryAdmin 0.5 -60 0.5 0 0")
			command("item replace entity QueryAdmin hotbar.0 with minecraft:diamond 3")
			command("item replace entity QueryAdmin armor.head with minecraft:leather_helmet[minecraft:damage=2]")
			command("tag QueryAdmin add PlayerQueryTag")
			spawns := command(`summon minecraft:pig 4.5 -59 0.5 {UUID:[I;1,2,3,4],Health:7.5f,NoAI:1b,NoGravity:1b,Silent:1b,Invulnerable:1b,Tags:["EntityQueryTag"],Rotation:[12.5f,-20.25f]}`)
			fixedUUID := [16]byte{3: 1, 7: 2, 11: 3, 15: 4}
			var npc int32
			deadline := time.Now().Add(10 * time.Second)
			for npc == 0 && time.Now().Before(deadline) {
				for _, line := range spawns {
					prefix := fmt.Sprintf("%#x:", ids.PlaySpawnEntity)
					if !strings.HasPrefix(line, prefix) {
						continue
					}
					r := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix)))
					id, e := r.VarInt()
					if e != nil {
						t.Fatal(e)
					}
					u, e := r.UUID()
					if e != nil {
						t.Fatal(e)
					}
					if u != fixedUUID {
						continue
					}
					kind, e := r.VarInt()
					if e != nil || kind != ids.EntityTypePig {
						t.Fatalf("target spawn type%d error%v", kind, e)
					}
					npc = id
				}
				if npc == 0 {
					time.Sleep(10 * time.Millisecond)
					spawns = admin.sink.take()
				}
			}
			if npc == 0 {
				t.Fatal("NPC spawn receipt missing")
			}
			writeQuery := func(actor tagQueryActorBB, id, tx int32) {
				var b protocol.Buffer
				b.VarInt(tx)
				b.VarInt(id)
				if e := actor.pw.WritePacket(server.IDPlayEntityTagQuerySB, b.Bytes()); e != nil {
					t.Fatal(e)
				}
			}
			readTag := func(line string) (int32, map[string]any, bool) {
				prefix := fmt.Sprintf("%#x:", ids.PlayTagQueryCB)
				if !strings.HasPrefix(line, prefix) {
					return 0, nil, false
				}
				r := bytes.NewReader([]byte(strings.TrimPrefix(line, prefix)))
				tx, e := protocol.ReadVarInt(r)
				if e != nil {
					t.Fatal(e)
				}
				value, e := nbt.ReadUnnamed(r)
				if e != nil || r.Len() != 0 {
					t.Fatalf("tag query frame error%v remaining%d", e, r.Len())
				}
				m, ok := value.(map[string]any)
				if !ok {
					t.Fatalf("transaction%d has noncompound %T", tx, value)
				}
				return tx, m, true
			}
			query := func(actor tagQueryActorBB, id, tx int32) map[string]any {
				writeQuery(actor, id, tx)
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range actor.sink.take() {
						got, m, ok := readTag(line)
						if !ok {
							continue
						}
						if got != tx {
							t.Fatalf("unexpected query response transaction%d, waiting%d", got, tx)
						}
						return m
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("query transaction%d missing", tx)
				return nil
			}
			vector := func(m map[string]any, key string, elem byte, want []any) {
				v, ok := m[key].(nbt.List)
				if !ok || v.Elem != elem || !reflect.DeepEqual(v.Items, want) {
					t.Errorf("%s got%#v want elem%d items%#v", key, m[key], elem, want)
				}
			}
			uuid := func(m map[string]any, want []int32) {
				got, ok := m["UUID"].([]int32)
				if !ok || !reflect.DeepEqual(got, want) {
					t.Errorf("UUID got%#v want%v", m["UUID"], want)
				}
			}
			noID := func(m map[string]any) {
				if value, ok := m["id"]; ok {
					t.Errorf("saveWithoutId unexpectedly includes id%#v", value)
				}
			}
			npcGolden := func(m map[string]any) {
				noID(m)
				uuid(m, []int32{1, 2, 3, 4})
				vector(m, "Pos", nbt.TagDouble, []any{float64(4.5), float64(-59), float64(.5)})
				vector(m, "Rotation", nbt.TagFloat, []any{float32(12.5), float32(-20.25)})
				vector(m, "Tags", nbt.TagString, []any{"EntityQueryTag"})
				if got, ok := m["Health"].(float32); !ok || got != 7.5 {
					t.Errorf("Health %#v", m["Health"])
				}
				for _, key := range []string{"NoAI", "NoGravity", "Silent", "Invulnerable"} {
					if got, ok := m[key].(int8); !ok || got != 1 {
						t.Errorf("%s %#v", key, m[key])
					}
				}
				t.Logf("NPC keys%d UUID%T Health%T", len(m), m["UUID"], m["Health"])
			}
			playerGolden := func(m map[string]any, dim string) {
				noID(m)
				u := server.OfflineUUID("QueryAdmin")
				words := make([]int32, 4)
				for i := range words {
					words[i] = int32(binary.BigEndian.Uint32(u[i*4:]))
				}
				uuid(m, words)
				vector(m, "Tags", nbt.TagString, []any{"PlayerQueryTag"})
				if m["Dimension"] != dim {
					t.Errorf("Dimension%#v want%s", m["Dimension"], dim)
				}
				if value, ok := m["Health"].(float32); !ok || value != 20 {
					t.Errorf("player Health%#v", m["Health"])
				}
				inv, ok := m["Inventory"].(nbt.List)
				if !ok || inv.Elem != nbt.TagCompound {
					t.Fatalf("Inventory%#v", m["Inventory"])
				}
				diamond := false
				for _, entry := range inv.Items {
					it, ok := entry.(map[string]any)
					if ok && it["id"] == "minecraft:diamond" && it["count"] == int32(3) && it["Slot"] == int8(0) {
						diamond = true
					}
				}
				if !diamond {
					t.Errorf("diamond inventory slot missing%#v", inv)
				}
				equipment, ok := m["equipment"].(map[string]any)
				if !ok {
					t.Fatalf("equipment%#v", m["equipment"])
				}
				head, ok := equipment["head"].(map[string]any)
				if !ok || head["id"] != "minecraft:leather_helmet" {
					t.Fatalf("head%#v", equipment["head"])
				}
				components, ok := head["components"].(map[string]any)
				if !ok || components["minecraft:damage"] != int32(2) {
					t.Errorf("head components%#v", head["components"])
				}
				t.Logf("player keys%d UUID%T Inventory%d", len(m), m["UUID"], len(inv.Items))
			}
			npcGolden(query(admin, npc, math.MinInt32))
			playerGolden(query(admin, admin.id, math.MaxInt32), "minecraft:overworld")
			npcGolden(query(admin, npc, 0))
			playerGolden(query(admin, admin.id, 0), "minecraft:overworld")
			// A valid owner response after the missing-id query establishes completion;
			// any unexpected response is rejected by query rather than drained away.
			writeQuery(admin, -1, -17)
			playerGolden(query(admin, admin.id, 101), "minecraft:overworld")
			writeQuery(admin, math.MaxInt32, -18)
			playerGolden(query(admin, admin.id, 102), "minecraft:overworld")
			gm := join("QueryGM")
			command("gamemode survival QueryGM")
			npcGolden(query(gm, npc, -123))
			playerGolden(query(gm, admin.id, -124), "minecraft:overworld")
			for _, name := range []string{"QueryLow", "QueryNone"} {
				actor := join(name)
				actor.sink.take()
				writeQuery(actor, actor.id, -999)
				var stats protocol.Buffer
				stats.VarInt(1)
				if e := actor.pw.WritePacket(server.IDPlayClientStatusSB, stats.Bytes()); e != nil {
					t.Fatal(e)
				}
				done := false
				deadline := time.Now().Add(10 * time.Second)
				for !done && time.Now().Before(deadline) {
					for _, line := range actor.sink.take() {
						if _, _, ok := readTag(line); ok {
							t.Fatalf("%s received forbidden query response", name)
						}
						prefix := fmt.Sprintf("%#x:", ids.PlayAwardStats)
						if strings.HasPrefix(line, prefix) {
							done = true
						}
					}
					if !done {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if !done {
					t.Fatalf("%s statistics owner barrier missing", name)
				}
				t.Logf("%s denied without response; statistics barrier received", name)
			}
			command("execute in minecraft:the_nether run tp QueryAdmin 0.5 200 0.5 0 0")
			writeQuery(admin, npc, -19)
			playerGolden(query(admin, admin.id, 103), "minecraft:the_nether")
		})
	}
}
