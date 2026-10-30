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

func TestPlayerArmorAttributeParity(t *testing.T) {
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
		names := map[int32]string{1: "armor", 2: "armor_toughness", 20: "knockback_resistance"}
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
		for phaseIndex, stage := range []struct {
			name     string
			commands []string
		}{
			{"copper_helmet", []string{"item replace entity @s armor.head with copper_helmet"}},
			{"iron_chest", []string{"item replace entity @s armor.chest with iron_chestplate"}},
			{"diamond_legs", []string{"item replace entity @s armor.legs with diamond_leggings"}},
			{"netherite_boots", []string{"item replace entity @s armor.feet with netherite_boots"}},
			{"offhand", []string{"item replace entity @s weapon.offhand with diamond_chestplate"}},
			{"wrong_head_slot", []string{"item replace entity @s armor.head with diamond_boots"}},
			{"remove_chest", []string{"attribute @s armor modifier remove minecraft:armor.chestplate", "attribute @s armor_toughness modifier remove minecraft:armor.chestplate"}},
			{"identical_chest", []string{"item replace entity @s armor.chest with iron_chestplate"}},
			{"changed_chest", []string{`item replace entity @s armor.chest with iron_chestplate[custom_name='"Changed"']`}},
			{"body_wolf", []string{"item replace entity @s armor.body with wolf_armor"}},
			{"body_air", []string{"item replace entity @s armor.body with air"}},
		} {
			updates = map[string][]string{}
			for _, command := range stage.commands {
				sendCommand(t, pw, command)
				barrier("owner")
				if strings.HasPrefix(command, "item replace") {
					fields := strings.Fields(command)
					item := strings.Split(fields[6], "[")[0]
					if item != "air" {
						receipt := fmt.Sprintf("ARMOR_RECEIPT_%d", serial)
						sendCommand(t, pw, "execute if items entity @s "+fields[4]+" "+item+" run say "+receipt)
						if !structureSaidBB(barrier("owner"), receipt) {
							t.Fatal("item slot preparation failed", leg.name, stage.name, command)
						}
					}
				}
			}
			settle(5)
			expected := []float64{2, 8, 14, 17, 17, 15, 9, 9, 15, 26, 15}[phaseIndex]
			expectedToughness := float64(0)
			if phaseIndex == 2 {
				expectedToughness = 2
			}
			if phaseIndex >= 3 {
				expectedToughness = 5
			}
			resistance := float64(0)
			if phaseIndex >= 3 {
				resistance = .10000000149011612
			}
			values := map[string]float64{"armor": expected, "armor_toughness": expectedToughness, "knockback_resistance": resistance}
			expectedMods := map[string]map[string]float64{"armor": {}, "armor_toughness": {}, "knockback_resistance": {}}
			if phaseIndex <= 4 {
				expectedMods["armor"]["minecraft:armor.helmet"] = 2
				expectedMods["armor_toughness"]["minecraft:armor.helmet"] = 0
			}
			if phaseIndex >= 1 && phaseIndex != 6 && phaseIndex != 7 {
				expectedMods["armor"]["minecraft:armor.chestplate"] = 6
				expectedMods["armor_toughness"]["minecraft:armor.chestplate"] = 0
			}
			if phaseIndex >= 2 {
				expectedMods["armor"]["minecraft:armor.leggings"] = 6
				expectedMods["armor_toughness"]["minecraft:armor.leggings"] = 2
			}
			if phaseIndex >= 3 {
				expectedMods["armor"]["minecraft:armor.boots"] = 3
				expectedMods["armor_toughness"]["minecraft:armor.boots"] = 3
				expectedMods["knockback_resistance"]["minecraft:armor.boots"] = resistance
			}
			if phaseIndex == 9 {
				expectedMods["armor"]["minecraft:armor.body"] = 11
				expectedMods["armor_toughness"]["minecraft:armor.body"] = 0
			}
			for _, attr := range []string{"armor", "armor_toughness", "knockback_resistance"} {
				prefix := "attribute @s " + attr
				if effective, base := query(prefix+" get"), query(prefix+" base get"); effective != values[attr] || base != float64(0) {
					t.Fatal("effective armor", leg.name, stage.name, attr, effective, base, values[attr])
				}
				for _, key := range []string{"minecraft:armor.chestplate", "minecraft:armor.helmet", "minecraft:armor.leggings", "minecraft:armor.boots", "minecraft:armor.body"} {
					got := query(prefix + " modifier value get " + key)
					want, present := expectedMods[attr][key]
					if present && got != want {
						t.Fatal("modifier value", leg.name, stage.name, attr, key, got, want)
					}
					if !present && got != "commands.attribute.failed.no_modifier" {
						t.Fatal("unexpected modifier", leg.name, stage.name, attr, key, got)
					}
				}
			}
			settle(3)
			for _, side := range []string{"owner", "viewer"} {
				if len(updates[side+"/knockback_resistance"]) != 0 {
					t.Fatal("resistance must not sync", leg.name, stage.name, side)
				}
				for _, attr := range []string{"armor", "armor_toughness"} {
					rows := updates[side+"/"+attr]
					if phaseIndex == 4 || phaseIndex == 7 {
						if len(rows) != 0 {
							t.Fatal("unchanged slot dirtied armor", leg.name, stage.name, side, attr, rows)
						}
						continue
					}
					var modifiers []string
					for key, value := range expectedMods[attr] {
						modifiers = append(modifiers, fmt.Sprintf("%s:%g:0", key, value))
					}
					sort.Strings(modifiers)
					want := fmt.Sprintf("base=0 mods=%v", modifiers)
					if len(rows) == 0 || rows[len(rows)-1] != want {
						t.Fatal("raw armor row", leg.name, stage.name, side, attr, rows, want)
					}
				}
			}
			if phaseIndex == 0 {
				sendCommand(t, pw, "gamerule natural_health_regeneration false")
				sendCommand(t, pw, "gamemode survival P")
				barrier("owner")
				settle(65)
				sendCommand(t, pw, "execute if data entity @s {Health:20f} run say ARMOR_HEALTH_READY")
				if !structureSaidBB(barrier("owner"), "ARMOR_HEALTH_READY") {
					t.Fatal("health baseline not20", leg.name)
				}
				sendCommand(t, pw, "damage @s 10 minecraft:mob_attack")
				barrier("owner")
				sendCommand(t, pw, "execute if data entity @s {Health:10.16f} run say COPPER_DAMAGE_READY")
				if !structureSaidBB(barrier("owner"), "COPPER_DAMAGE_READY") {
					sendCommand(t, pw, "data get entity @s Health")
					t.Logf("health diagnostic %v", barrier("owner"))
					t.Fatal("copper armor damage mismatch", leg.name)
				}
			}

		}
		sendCommand(t, pw, "gamerule keep_inventory true")
		sendCommand(t, pw, "attribute @s armor modifier remove minecraft:armor.chestplate")
		sendCommand(t, pw, "attribute @s armor_toughness modifier remove minecraft:armor.chestplate")
		barrier("owner")
		settle(5)
		if got := query("attribute @s armor get"); got != float64(9) {
			t.Fatal("respawn removal preparation", leg.name, got)
		}
		sendCommand(t, pw, "kill P")
		dead := false
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !dead {
			for _, line := range barrier("owner") {
				prefix, body, ok := strings.Cut(line, ":")
				if ok && prefix == fmt.Sprintf("%#x", server.IDPlaySetHealth) {
					health, err := protocol.NewPacketReader([]byte(body)).Float()
					if err == nil && health == 0 {
						dead = true
					}
				}
			}
		}
		if !dead {
			t.Fatal("death health missing", leg.name)
		}
		var request protocol.Buffer
		request.VarInt(0)
		if err := pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes()); err != nil {
			t.Fatal(err)
		}
		respawned := false
		deadline = time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !respawned {
			for _, line := range barrier("owner") {
				prefix, _, ok := strings.Cut(line, ":")
				if ok && prefix == fmt.Sprintf("%#x", server.IDPlayRespawn) {
					respawned = true
				}
			}
		}
		if !respawned {
			t.Fatal("respawn packet missing", leg.name)
		}
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		settle(5)
		sendCommand(t, pw, "execute if items entity @s armor.chest iron_chestplate run say ARMOR_RETAINED")
		if !structureSaidBB(barrier("owner"), "ARMOR_RETAINED") {
			t.Fatal("retained chest missing", leg.name)
		}
		if armor, tough, chest, zero := query("attribute @s armor get"), query("attribute @s armor_toughness get"), query("attribute @s armor modifier value get minecraft:armor.chestplate"), query("attribute @s armor_toughness modifier value get minecraft:armor.chestplate"); armor != float64(15) || tough != float64(5) || chest != float64(6) || zero != float64(0) {
			t.Fatal("respawn armor", leg.name, armor, tough, chest, zero)
		}

	}
}
