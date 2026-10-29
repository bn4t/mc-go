package blackbox

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestPlayerEquipmentAttributeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P", "Q")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
		s.SetOpLevel("Q", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		pc, pr, pw := joinPlayBlackbox(t, leg.addr, "P")
		defer pc.Close()
		sendClientInformation(t, pw)
		ps := newChatSinkTP(pr, pw)
		qc, qr, qw := joinPlayBlackbox(t, leg.addr, "Q")
		defer qc.Close()
		sendClientInformation(t, qw)
		qs := newChatSinkTP(qr, qw)
		owner := int32(-1)
		tracked := false
		serial := 0
		play := int32(0)
		havePlay := false
		statsPackets := 0
		names := map[int32]string{3: "attack_damage", 5: "attack_speed", 21: "luck", 30: "scale"}
		updates := map[string][]string{}
		consume := func(side string, lines []string) {
			for _, line := range lines {
				prefix, body, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				r := protocol.NewPacketReader([]byte(body))
				if prefix == fmt.Sprintf("%#x", server.IDPlayJoinGame) && side == "owner" {
					owner, _ = r.Int()
				}
				if prefix == fmt.Sprintf("%#x", server.IDPlaySpawnEntity) && side == "viewer" {
					id, _ := r.VarInt()
					uuid, _ := r.UUID()
					if uuid == offlineUUIDBlackbox("P") {
						owner = id
						tracked = true
					}
				}
				if prefix == "0x3" && side == "owner" {
					statsPackets++
					n, _ := r.VarInt()
					for i := int32(0); i < n; i++ {
						cat, _ := r.VarInt()
						id, _ := r.VarInt()
						v, e := r.VarInt()
						if e == nil && cat == 8 && id == server.CustomStatIDs["play_time"] {
							play = v
							havePlay = true
						}
					}
				}
				if prefix != fmt.Sprintf("%#x", server.IDPlayUpdateAttributes) {
					continue
				}
				id, e := r.VarInt()
				if e != nil {
					t.Fatal(e)
				}
				count, e := r.VarInt()
				if e != nil || count < 0 || count > 256 {
					t.Fatal("invalid attribute count", count, e)
				}
				for n := int32(0); n < count; n++ {
					attr, e1 := r.VarInt()
					base, e2 := r.Double()
					amount, e3 := r.VarInt()
					if e1 != nil || e2 != nil || e3 != nil || amount < 0 || amount > 128 {
						t.Fatal("invalid attribute row")
					}
					var mods []string
					for m := int32(0); m < amount; m++ {
						key, e1 := r.String()
						v, e2 := r.Double()
						op, e3 := r.Byte()
						if e1 != nil || e2 != nil || e3 != nil {
							t.Fatal("invalid modifier")
						}
						mods = append(mods, fmt.Sprintf("%s:%g:%d", key, v, op))
					}
					sort.Strings(mods)
					if name, ok := names[attr]; ok && id == owner {
						updates[side+"/"+name] = append(updates[side+"/"+name], fmt.Sprintf("base=%g mods=%v", base, mods))
					}
				}
				if r.Remaining() != 0 {
					t.Fatal("attribute trailing bytes", r.Remaining())
				}
			}
		}
		barrier := func(side string) []string {
			serial++
			marker := fmt.Sprintf("ATTR_EFFECTIVE_%d", serial)
			writer, sink := pw, ps
			if side == "viewer" {
				writer, sink = qw, qs
			}
			sendCommand(t, writer, "say "+marker)
			lines := waitForExactSayBB(t, sink, marker, 15*time.Second)
			consume(side, lines)
			return lines
		}
		playerTime := func() int32 {
			before := statsPackets
			var request protocol.Buffer
			request.VarInt(1)
			pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes())
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				barrier("owner")
				consume("viewer", qs.take())
				if statsPackets > before {
					if havePlay {
						return play
					}
					before = statsPackets
					pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes())
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("no player time")
			return 0
		}
		settle := func(n int32) {
			before := playerTime()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if playerTime()-before >= n {
					barrier("viewer")
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("player time did not advance")
		}
		query := func(command string) any {
			sendCommand(t, pw, command)
			for _, line := range barrier("owner") {
				key, args, ok := componentArgs(line)
				if !ok || !strings.HasPrefix(key, "commands.attribute.") {
					continue
				}
				if strings.HasSuffix(key, "get.success") && len(args) > 0 {
					v := args[len(args)-1]
					if m, ok := v.(map[string]any); ok {
						return m[""]
					}
					return v
				}
				if strings.Contains(key, "failed") {
					return key
				}
			}
			t.Fatal("query reply absent", command)
			return nil
		}
		sendCommand(t, pw, "tp P 8 -60 6")
		sendCommand(t, pw, "tp Q 8 -60 10")
		barrier("owner")
		barrier("viewer")
		settle(5)
		if owner < 0 || !tracked {
			t.Fatal("viewer did not track owner")
		}
		for _, stage := range []struct {
			name     string
			commands []string
		}{
			{"equip", []string{"item replace entity @s weapon.mainhand with iron_sword"}},
			{"remove", []string{"attribute @s attack_damage modifier remove minecraft:base_attack_damage", "attribute @s attack_speed modifier remove minecraft:base_attack_speed"}},
			{"same_stack", []string{"item replace entity @s weapon.mainhand with iron_sword"}},
			{"component_change", []string{`item replace entity @s weapon.mainhand with iron_sword[custom_name='"Renamed"']`}},
			{"air", []string{"item replace entity @s weapon.mainhand with air"}},
			{"reequip", []string{"item replace entity @s weapon.mainhand with iron_sword"}},
			{"duplicate_add", []string{"attribute @s attack_damage modifier add minecraft:base_attack_damage 99 add_value", "attribute @s attack_speed modifier add minecraft:base_attack_speed 99 add_value"}},
		} {
			updates = map[string][]string{}
			for _, command := range stage.commands {
				sendCommand(t, pw, command)
				barrier("owner")
			}
			settle(5)
			for _, attr := range []string{"attack_damage", "attack_speed"} {
				prefix := "attribute @s minecraft:" + attr
				effective, modifier := query(prefix+" get"), query(prefix+" modifier value get minecraft:base_"+attr)
				present := stage.name == "equip" || stage.name == "component_change" || stage.name == "reequip" || stage.name == "duplicate_add"
				want, wantMod := float64(1), float64(5)
				if attr == "attack_speed" {
					want = 4
					wantMod = -2.4000000953674316
				}
				if present {
					want += wantMod
				}
				if effective != want {
					t.Fatalf("%s %s %s effective=%v want=%v", leg.name, stage.name, attr, effective, want)
				}
				if present && modifier != wantMod {
					t.Fatalf("%s %s %s modifier=%v want=%v", leg.name, stage.name, attr, modifier, wantMod)
				}
				if !present && modifier != "commands.attribute.failed.no_modifier" {
					t.Fatalf("%s %s %s unexpected modifier=%v", leg.name, stage.name, attr, modifier)
				}
			}
			settle(3)
			for _, side := range []string{"owner", "viewer"} {
				if len(updates[side+"/attack_damage"]) != 0 {
					t.Fatal("attack damage must not sync", leg.name, stage.name, side)
				}
				rows := updates[side+"/attack_speed"]
				unchanged := stage.name == "same_stack" || stage.name == "duplicate_add"
				if unchanged {
					if len(rows) != 0 {
						t.Fatal("unchanged equipment dirtied attributes", leg.name, stage.name, side, rows)
					}
					continue
				}
				want := "base=4 mods=[minecraft:base_attack_speed:-2.4000000953674316:0]"
				if stage.name == "remove" || stage.name == "air" {
					want = "base=4 mods=[]"
				}
				if len(rows) == 0 || rows[len(rows)-1] != want {
					t.Fatalf("%s %s %s speed rows=%v want=%s", leg.name, stage.name, side, rows, want)
				}
			}
		}
	}
}
