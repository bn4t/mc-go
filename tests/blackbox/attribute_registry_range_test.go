package blackbox

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestAttributeRegistryRangeParity(t *testing.T) {
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
		names := map[int32]string{1: "armor", 18: "gravity", 19: "jump_strength", 20: "knockback_resistance", 23: "max_health", 39: "waypoint_receive_range"}
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
		huge := "1" + strings.Repeat("0", 308)
		for _, spec := range []struct {
			name               string
			initial, low, high float64
			sync               bool
		}{
			{"armor", 0, 0, 30, true}, {"gravity", .08, -1, 1, true}, {"jump_strength", .41999998688697815, 0, 32, true},
			{"knockback_resistance", 0, -2, 1, false}, {"max_health", 20, 1, 1024, true}, {"waypoint_receive_range", 6e7, 0, 6e7, false},
		} {
			attr := spec.name
			prefix := "attribute @s minecraft:" + attr
			if effective, base := query(prefix+" get"), query(prefix+" base get"); effective != spec.initial || base != spec.initial {
				t.Fatal("supplier default", leg.name, attr, effective, base, spec.initial)
			}
			for _, tc := range []struct {
				name, base, amount, op string
				zero                   bool
			}{
				{"low", "-4096", "", "", false},
				{"high", "1000000000", "", "", false},
				{"positive_overflow", "2048", huge, "add_multiplied_total", false},
				{"negative_overflow", "2048", "-" + huge, "add_multiplied_total", false},
				{"nan", "2048", huge, "add_multiplied_base", true},
			} {
				sendCommand(t, pw, prefix+" modifier remove test:range")
				sendCommand(t, pw, prefix+" modifier remove test:zero")
				sendCommand(t, pw, prefix+" base reset")
				barrier("owner")
				settle(5)
				updates = map[string][]string{}
				sendCommand(t, pw, prefix+" base set "+tc.base)
				if tc.amount != "" {
					sendCommand(t, pw, prefix+" modifier add test:range "+tc.amount+" "+tc.op)
				}
				if tc.zero {
					sendCommand(t, pw, prefix+" modifier add test:zero -1 add_multiplied_total")
				}
				barrier("owner")
				settle(5)
				effective, base := query(prefix+" get"), query(prefix+" base get")
				wantBase, _ := strconv.ParseFloat(tc.base, 64)
				if base != wantBase {
					t.Fatal("raw base rejected", leg.name, attr, tc.name, base, wantBase)
				}
				var modifier any
				if tc.amount != "" {
					modifier = query(prefix + " modifier value get test:range")
					want, _ := strconv.ParseFloat(tc.amount, 64)
					if modifier != want {
						t.Fatal("raw modifier rejected", leg.name, attr, tc.name, modifier, want)
					}
				}
				settle(3)
				wantEffective := spec.low
				if tc.name == "high" || tc.name == "positive_overflow" {
					wantEffective = spec.high
				}
				if effective != wantEffective {
					t.Fatalf("%s %s %s effective=%v want=%v", leg.name, attr, tc.name, effective, wantEffective)
				}
				var mods []string
				if tc.amount != "" {
					op := 2
					if tc.op == "add_multiplied_base" {
						op = 1
					}
					amount, _ := strconv.ParseFloat(tc.amount, 64)
					mods = append(mods, fmt.Sprintf("test:range:%g:%d", amount, op))
				}
				if tc.zero {
					mods = append(mods, "test:zero:-1:2")
				}
				wantRow := fmt.Sprintf("base=%g mods=%v", wantBase, mods)
				for _, side := range []string{"owner", "viewer"} {
					rows := updates[side+"/"+attr]
					if !spec.sync {
						if len(rows) != 0 {
							t.Fatal("nonsyncable attribute emitted", leg.name, attr, tc.name, side, rows)
						}
						continue
					}
					if len(rows) == 0 || rows[len(rows)-1] != wantRow {
						t.Fatalf("%s %s %s %s raw rows=%v want=%s", leg.name, attr, tc.name, side, rows, wantRow)
					}
				}
			}
			sendCommand(t, pw, prefix+" modifier remove test:range")
			sendCommand(t, pw, prefix+" modifier remove test:zero")
			sendCommand(t, pw, prefix+" base reset")
			barrier("owner")
			settle(5)
			if effective, base := query(prefix+" get"), query(prefix+" base get"); effective != spec.initial || base != spec.initial {
				t.Fatal("supplier reset", leg.name, attr, effective, base, spec.initial)
			}
		}
	}
}
