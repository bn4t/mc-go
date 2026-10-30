//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeOminousBannerSaveParity(t *testing.T) {
	mcDir := t.TempDir()
	fn := "summon minecraft:pillager 6.5 -60 2.5 " + captainNBT + "\n" + "item replace block 1 -60 0 container.0 from entity @e[type=minecraft:pillager,limit=1,sort=nearest] armor.head\n" + "item replace block 1 -60 0 container.1 with minecraft:white_banner\n" + "say BANNER_CAPTURED\n"
	pack := map[string]string{"datapacks/zz_native_banner/pack.mcmeta": `{"pack":{"description":"native banner storage","min_format":107,"max_format":107}}`, "datapacks/zz_native_banner/data/nbt_banner/function/capture.mcfunction": fn}
	files := map[string]string{"ops.json": opsJSONFor("BannerWriter")}
	for name, body := range pack {
		p := filepath.Join(mcDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		files["world/"+name] = body
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, files, func(s *server.Server) {
		s.WorldDir = mcDir
		s.GameMode = server.GameModeCreative
		s.SetOpLevel("BannerWriter", 4)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "BannerWriter", 1)
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
				marker := fmt.Sprintf("BANNER_WRITER_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tp @s 0.5 -60 0.5 0 0")
			loaded := false
			deadline := time.Now().Add(60 * time.Second)
			for !loaded && time.Now().Before(deadline) {
				loaded = structureSaidBB(command("execute if loaded 1 -60 0 run say BANNER_CHEST_READY"), "BANNER_CHEST_READY")
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
			sendCommand(t, pw, "function nbt_banner:capture")
			waitForExactSayBB(t, sink, "BANNER_CAPTURED", 30*time.Second)
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
			want := map[string]any{
				"minecraft:banner_patterns": nbt.List{Elem: nbt.TagCompound, Items: []any{
					map[string]any{"pattern": "minecraft:rhombus", "color": "cyan"}, map[string]any{"pattern": "minecraft:stripe_bottom", "color": "light_gray"}, map[string]any{"pattern": "minecraft:stripe_center", "color": "gray"}, map[string]any{"pattern": "minecraft:border", "color": "light_gray"}, map[string]any{"pattern": "minecraft:stripe_middle", "color": "black"}, map[string]any{"pattern": "minecraft:half_horizontal", "color": "light_gray"}, map[string]any{"pattern": "minecraft:circle", "color": "light_gray"}, map[string]any{"pattern": "minecraft:border", "color": "black"}}},
				"minecraft:tooltip_display": map[string]any{"hidden_components": nbt.List{Elem: nbt.TagString, Items: []any{"minecraft:banner_patterns"}}},
				"minecraft:item_name":       map[string]any{"translate": "block.minecraft.ominous_banner"}, "minecraft:rarity": "uncommon",
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
				if entry["id"] != "minecraft:white_banner" || entry["count"] != int32(1) {
					t.Errorf("slot%d identity/count%#v", slot, entry)
				}
				components := map[string]any{}
				if value, present := entry["components"]; present {
					var ok bool
					components, ok = value.(map[string]any)
					if !ok {
						t.Fatalf("slot%d components%#v", slot, value)
					}
				}
				if slot == 0 {
					if !reflect.DeepEqual(components, want) {
						t.Errorf("captain components%#v want%#v", components, want)
					}
				} else if len(components) != 0 {
					t.Errorf("plain banner gained components%#v", components)
				}
			}
			if !found[0] || !found[1] {
				t.Fatal("banner item missing")
			}
		})
	}
}
