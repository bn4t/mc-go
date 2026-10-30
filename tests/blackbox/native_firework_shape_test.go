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

func TestNativeFireworkShapeSaveParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ShapeWriter")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.SetOpLevel("ShapeWriter", 4) })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "ShapeWriter", 1)
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
				marker := fmt.Sprintf("SHAPE_WRITER_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tp @s 0.5 -60 0.5 0 0")
			loaded := false
			deadline := time.Now().Add(60 * time.Second)
			for !loaded && time.Now().Before(deadline) {
				loaded = structureSaidBB(command("execute if loaded 1 -60 0 run say SHAPE_RECORD_READY"), "SHAPE_RECORD_READY")
			}
			if !loaded {
				t.Fatal("jukebox target not loaded")
			}
			placed := false
			for _, line := range command("setblock 1 -60 0 minecraft:jukebox") {
				key, _, ok := componentArgs(line)
				if ok && key == "commands.setblock.success" {
					placed = true
				}
			}
			if !placed {
				t.Fatal("jukebox placement did not succeed")
			}
			command(`data merge block 1 -60 0 {RecordItem:{id:"minecraft:music_disc_cat",count:1,components:{"minecraft:firework_explosion":{shape:"small_ball"},"minecraft:fireworks":{flight_duration:2,explosions:[{shape:"star",colors:[1,2],fade_colors:[3],has_trail:1b,has_twinkle:1b},{shape:"small_ball",colors:[4]}]}}}}`)
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
			record, ok := root["RecordItem"].(map[string]any)
			if !ok {
				t.Fatalf("RecordItem%#v", root["RecordItem"])
			}
			if record["id"] != "minecraft:music_disc_cat" || record["count"] != int32(1) {
				t.Fatalf("record identity/count%#v", record)
			}
			components, ok := record["components"].(map[string]any)
			if !ok {
				t.Fatalf("record components%#v", record["components"])
			}
			star := map[string]any{"shape": "star", "colors": nbt.List{Elem: nbt.TagInt, Items: []any{int32(1), int32(2)}}, "fade_colors": nbt.List{Elem: nbt.TagInt, Items: []any{int32(3)}}, "has_trail": int8(1), "has_twinkle": int8(1)}
			if got := components["minecraft:firework_explosion"]; !reflect.DeepEqual(got, map[string]any{"shape": "small_ball"}) {
				t.Errorf("small-ball explosion%#v", got)
			}
			want := map[string]any{"flight_duration": int8(2), "explosions": nbt.List{Elem: nbt.TagCompound, Items: []any{star, map[string]any{"shape": "small_ball", "colors": nbt.List{Elem: nbt.TagInt, Items: []any{int32(4)}}}}}}
			if got := components["minecraft:fireworks"]; !reflect.DeepEqual(got, want) {
				t.Errorf("fireworks%#v want%#v", got, want)
			}
		})
	}
}
