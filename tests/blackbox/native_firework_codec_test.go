//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestNativeFireworkCodecAdmissionParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("CodecWriter")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.SetOpLevel("CodecWriter", 4) })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "CodecWriter", 1)
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
				marker := fmt.Sprintf("CODEC_WRITER_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tp @s 0.5 -60 0.5 0 0")
			loaded := false
			deadline := time.Now().Add(60 * time.Second)
			for !loaded && time.Now().Before(deadline) {
				loaded = structureSaidBB(command("execute if loaded 1 -60 0 run say CODEC_CHEST_READY"), "CODEC_CHEST_READY")
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
			queryRoot := func(wantTx int32) map[string]any {
				var query protocol.Buffer
				query.VarInt(wantTx)
				query.Position(1, -60, 0)
				if e := pw.WritePacket(server.IDPlayBlockEntityTagQuerySB, query.Bytes()); e != nil {
					t.Fatal(e)
				}
				var root map[string]any
				deadline := time.Now().Add(10 * time.Second)
				for root == nil && time.Now().Before(deadline) {
					for _, line := range sink.take() {
						prefix := fmt.Sprintf("%#x:", server.IDPlayTagQueryCB)
						if !strings.HasPrefix(line, prefix) {
							continue
						}
						r := bytes.NewReader([]byte(strings.TrimPrefix(line, prefix)))
						tx, e := protocol.ReadVarInt(r)
						if e != nil || tx != wantTx {
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
				return root
			}
			assertSlot := func(root map[string]any, id string, count int32, label string) {
				t.Helper()
				items, ok := root["Items"].(nbt.List)
				if !ok || items.Elem != nbt.TagCompound || len(items.Items) != 1 {
					t.Fatalf("%s Items%#v", label, root["Items"])
				}
				entry, ok := items.Items[0].(map[string]any)
				if !ok {
					t.Fatalf("%s item%T", label, items.Items[0])
				}
				if entry["Slot"] != int8(0) || entry["id"] != "minecraft:"+id || entry["count"] != count {
					t.Errorf("%s slot%#v want%s/%d", label, entry, id, count)
				}
			}
			cases := []struct {
				name, item string
				count      int
				accepted   bool
			}{
				{"missing_shape", `stone[minecraft:firework_explosion={}]`, 2, false},
				{"empty_rocket", `stone[minecraft:fireworks={}]`, 3, true},
				{"flight_only", `stone[minecraft:fireworks={flight_duration:2}]`, 4, true},
				{"explosions_only", `stone[minecraft:fireworks={explosions:[{shape:"small_ball"}]}]`, 5, true},
				{"child_missing_shape", `stone[minecraft:fireworks={flight_duration:2,explosions:[{}]}]`, 6, false},
				{"explicit_shape", `stone[minecraft:firework_explosion={shape:"small_ball"}]`, 8, true},
				{"absent_component", `stone`, 9, true},
			}
			for i, tc := range cases {
				command(`item replace block 1 -60 0 container.0 with minecraft:diamond 7`)
				assertSlot(queryRoot(int32(100+2*i)), "diamond", 7, tc.name+" sentinel")
				command(fmt.Sprintf("item replace block 1 -60 0 container.0 with minecraft:%s %d", tc.item, tc.count))
				id, count := "diamond", int32(7)
				if tc.accepted {
					id, count = "stone", int32(tc.count)
				}
				assertSlot(queryRoot(int32(101+2*i)), id, count, tc.name)
			}
		})
	}
}
