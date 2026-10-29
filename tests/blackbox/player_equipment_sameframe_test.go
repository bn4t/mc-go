package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestPlayerEquipmentSameFrameParity(t *testing.T) {
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor("P", "Q")}
	bodies := map[string]string{"pack.mcmeta": `{"pack":{"description":"same frame equipment probe","min_format":107,"max_format":107}}`}
	for _, phase := range []struct{ name, item string }{{"equip", "iron_sword"}, {"air", "air"}, {"axe", "copper_axe"}} {
		bodies["data/equprobe/function/"+phase.name+".mcfunction"] = "execute store result score before equprobe run attribute @s attack_damage get 100\nitem replace entity @s weapon.mainhand with " + phase.item + "\nexecute store result score immediate equprobe run attribute @s attack_damage get 100\nexecute store success score present equprobe run attribute @s attack_damage modifier value get minecraft:base_attack_damage\n"
	}
	for path, body := range bodies {
		relative := "datapacks/equprobe/" + path
		files["world/"+relative] = body
		local := filepath.Join(directory, relative)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, files, func(s *server.Server) {
		s.WorldDir = directory
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
		sendCommand(t, pw, "scoreboard objectives add equprobe dummy")
		barrier("owner")
		for _, phase := range []string{"equip", "air", "axe"} {
			sendCommand(t, pw, "function equprobe:"+phase)
			barrier("owner")
			for _, holder := range []string{"before", "immediate", "present"} {
				sendCommand(t, pw, "scoreboard players get "+holder+" equprobe")
				found := false
				for _, line := range barrier("owner") {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.scoreboard.players.get.success" {
						want := "100"
						if phase == "air" {
							want = "600"
						}
						if holder == "present" {
							want = "0"
							if phase == "air" {
								want = "1"
							}
						}
						if len(args) != 3 {
							t.Fatal("invalid score reply", args)
						}
						value := args[1]
						if compound, ok := value.(map[string]any); ok {
							value = compound[""]
						}
						if fmt.Sprint(value) != want {
							t.Fatalf("%s %s %s got=%v want=%s", leg.name, phase, holder, value, want)
						}
						found = true
					}
				}
				if !found {
					t.Fatal("score receipt missing", leg.name, phase, holder)
				}
			}
			settle(5)
			damage, modifier := query("attribute @s attack_damage get"), query("attribute @s attack_damage modifier value get minecraft:base_attack_damage")
			wantDamage, wantModifier := float64(6), any(float64(5))
			if phase == "air" {
				wantDamage = 1
				wantModifier = "commands.attribute.failed.no_modifier"
			}
			if phase == "axe" {
				wantDamage = 9
				wantModifier = float64(8)
			}
			if damage != wantDamage || modifier != wantModifier {
				t.Fatalf("%s %s settled damage=%v modifier=%v want=%v/%v", leg.name, phase, damage, modifier, wantDamage, wantModifier)
			}
		}
	}
}
