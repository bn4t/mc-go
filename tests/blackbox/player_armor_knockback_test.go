package blackbox

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestPlayerArmorKnockbackParity(t *testing.T) {
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
		armedMotion := false
		var motions [][3]float64
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
				if prefix == fmt.Sprintf("%#x", server.IDPlayEntityMotion) && side == "owner" && armedMotion {
					id, err := r.VarInt()
					if err != nil {
						t.Fatal(err)
					}
					x, y, z, err := r.ReadLpVec3()
					if err != nil {
						t.Fatal(err)
					}
					if id == owner {
						motions = append(motions, [3]float64{x, y, z})
					}
					continue
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
		sendCommand(t, pw, "gamerule spawn_mobs false")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "execute unless entity @e[type=!minecraft:player] run say KB_EMPTY")
		if !structureSaidBB(barrier("owner"), "KB_EMPTY") {
			t.Fatal("nonplayer fixture entities remain", leg.name)
		}
		sendCommand(t, pw, "gamerule keep_inventory true")
		sendCommand(t, pw, "gamerule natural_health_regeneration false")
		barrier("owner")
		for index, tc := range []struct {
			name, base, amount string
			boots              bool
		}{
			{"unarmored", "0", "", false},
			{"netherite", "0", "", true},
			{"base_half", "0.5", "", true},
			{"negative_bound", "-4", "", true},
			{"upper_bound", "2", "", true},
			{"negative_modifier", "0", "-1", true},
			{"removed_unchanged", "0", "", true},
		} {
			if index > 0 {
				sendCommand(t, pw, "kill P")
				dead := false
				deadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(deadline) && !dead {
					for _, line := range barrier("owner") {
						prefix, body, ok := strings.Cut(line, ":")
						if ok && prefix == fmt.Sprintf("%#x", server.IDPlaySetHealth) {
							h, e := protocol.NewPacketReader([]byte(body)).Float()
							if e == nil && h == 0 {
								dead = true
							}
						}
					}
				}
				if !dead {
					t.Fatal("death not applied", leg.name, tc.name)
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
					t.Fatal("respawn not applied", leg.name, tc.name)
				}
				if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
					t.Fatal(err)
				}
			}
			sendCommand(t, pw, "gamemode survival P")
			for _, slot := range []string{"armor.head", "armor.chest", "armor.legs", "armor.feet", "armor.body", "weapon.mainhand", "weapon.offhand"} {
				sendCommand(t, pw, "item replace entity @s "+slot+" with air")
			}
			sendCommand(t, pw, "attribute @s knockback_resistance modifier remove test:kb")
			sendCommand(t, pw, "attribute @s knockback_resistance base set "+tc.base)
			if tc.amount != "" {
				sendCommand(t, pw, "attribute @s knockback_resistance modifier add test:kb "+tc.amount+" add_value")
			}
			if tc.boots {
				sendCommand(t, pw, "item replace entity @s armor.feet with netherite_boots")
			}
			sendCommand(t, pw, "tp P 8 -60 6")
			sendCommand(t, pw, "tp Q 8 -60 10")
			barrier("owner")
			settle(65)
			if tc.boots {
				sendCommand(t, pw, "execute if items entity @s armor.feet netherite_boots run say KB_BOOTS_READY")
				if !structureSaidBB(barrier("owner"), "KB_BOOTS_READY") {
					t.Fatal("boots missing", leg.name, tc.name)
				}
			}
			sendCommand(t, pw, "execute if data entity @s {Health:20f} run say KB_HEALTH_READY")
			if !structureSaidBB(barrier("owner"), "KB_HEALTH_READY") {
				t.Fatal("health baseline not20", leg.name, tc.name)
			}
			if tc.name == "removed_unchanged" {
				sendCommand(t, pw, "attribute @s knockback_resistance modifier remove minecraft:armor.boots")
				sendCommand(t, pw, "item replace entity @s armor.feet with netherite_boots")
				barrier("owner")
				settle(5)
				if got := query("attribute @s knockback_resistance modifier value get minecraft:armor.boots"); got != "commands.attribute.failed.no_modifier" {
					t.Fatal("identical boots reinserted removed resistance", leg.name, got)
				}
			}
			var move protocol.Buffer
			move.Double(8)
			move.Double(-60)
			move.Double(6)
			move.Byte(1)
			if err := pw.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
				t.Fatal(err)
			}
			barrier("owner")
			effective := query("attribute @s knockback_resistance get")
			wantEffective := []float64{0, .10000000149011612, .6000000014901161, -2, 1, -.8999999985098839, 0}[index]
			gotEffective, err := strconv.ParseFloat(fmt.Sprint(effective), 64)
			if err != nil || gotEffective != wantEffective {
				t.Fatalf("%s %s effective=%v want=%g", leg.name, tc.name, effective, wantEffective)
			}
			motions = nil
			armedMotion = true
			sendCommand(t, pw, "damage @s 10 minecraft:mob_attack by Q")
			barrier("owner")
			settle(3)
			sendCommand(t, pw, "execute unless data entity @s {Health:20f} run say KB_DAMAGE_READY")
			if !structureSaidBB(barrier("owner"), "KB_DAMAGE_READY") {
				t.Fatal("damage not applied", leg.name, tc.name)
			}
			armedMotion = false
			if len(motions) == 0 {
				t.Fatal("motion packet missing", leg.name, tc.name)
			}
			// Compare magnitude: teleport reconciliation may change direction and vertical
			// motion. The velocity codec quantizes each horizontal component.
			magnitude := float64(0)
			for _, v := range motions {
				magnitude = math.Max(magnitude, math.Hypot(v[0], v[2]))
			}
			wantMagnitude := []float64{.4, .36, .16, 1.2, 0, .76, .4}[index]
			if math.Abs(magnitude-wantMagnitude) > 1.5e-4 {
				t.Fatalf("%s %s horizontal=%g want=%g motions=%v", leg.name, tc.name, magnitude, wantMagnitude, motions)
			}
			t.Logf("%s %s effective=%g horizontal=%g", leg.name, tc.name, gotEffective, magnitude)
		}
	}
}
