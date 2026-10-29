package blackbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestAttributeEffectiveRangeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, pr, pw := joinPlayBlackbox(t, leg.addr, "P")
		defer c.Close()
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("ATTR_RANGE_%d", serial)
			sendCommand(t, pw, "say "+marker)
			return waitForExactSayBB(t, sink, marker, 15*time.Second)
		}
		number := func(v any) (float64, bool) {
			if m, ok := v.(map[string]any); ok {
				v = m[""]
			}
			switch n := v.(type) {
			case float64:
				return n, true
			case int64:
				return float64(n), true
			case int32:
				return float64(n), true
			case int:
				return float64(n), true
			default:
				return 0, false
			}
		}
		query := func(command string) float64 {
			sendCommand(t, pw, command)
			for _, line := range barrier() {
				key, args, ok := componentArgs(line)
				if ok && strings.HasPrefix(key, "commands.attribute.") && strings.HasSuffix(key, "get.success") && len(args) > 0 {
					if v, ok := number(args[len(args)-1]); ok {
						return v
					}
				}
			}
			t.Fatalf("%s query response absent: %s", leg.name, command)
			return 0
		}
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with air")
		sendCommand(t, pw, "scoreboard objectives add attr_range dummy")
		barrier()
		huge := "1" + strings.Repeat("0", 308)
		attrs := []struct {
			name, initial, lowBase, highBase string
			low, high                        float64
		}{{"attack_damage", "1", "-10", "4096", 0, 2048}, {"attack_speed", "4", "-10", "2048", 0, 1024}, {"scale", "1", "-0.25", "32", .0625, 16}, {"luck", "0", "-2048", "2048", -1024, 1024}}
		for _, attr := range attrs {
			for _, tc := range []struct {
				name, base, amount, op string
				zero                   bool
				want                   float64
			}{{"low base", attr.lowBase, "", "", false, attr.low}, {"high base", attr.highBase, "", "", false, attr.high}, {"negative add", attr.initial, "-3000", "add_value", false, attr.low}, {"positive overflow", "2048", huge, "add_multiplied_total", false, attr.high}, {"negative overflow", "2048", "-" + huge, "add_multiplied_total", false, attr.low}, {"nan effective", "2048", huge, "add_multiplied_base", true, attr.low}} {
				prefix := "attribute @s minecraft:" + attr.name
				sendCommand(t, pw, prefix+" modifier remove test:range")
				sendCommand(t, pw, prefix+" modifier remove test:zero")
				sendCommand(t, pw, prefix+" base set "+tc.base)
				if tc.amount != "" {
					sendCommand(t, pw, prefix+" modifier add test:range "+tc.amount+" "+tc.op)
				}
				if tc.zero {
					sendCommand(t, pw, prefix+" modifier add test:zero -1 add_multiplied_total")
				}
				barrier()
				effective, base := query(prefix+" get"), query(prefix+" base get")
				wantBase, _ := strconv.ParseFloat(tc.base, 64)
				if effective != tc.want || base != wantBase {
					t.Errorf("%s %s %s effective/base %v/%v, want %v/%v", leg.name, attr.name, tc.name, effective, base, tc.want, wantBase)
				}
				if tc.amount != "" {
					actual := query(prefix + " modifier value get test:range")
					want, _ := strconv.ParseFloat(tc.amount, 64)
					if actual != want {
						t.Errorf("%s %s %s modifier %v, want raw %v", leg.name, attr.name, tc.name, actual, want)
					}
				}
				if tc.name == "high base" {
					sendCommand(t, pw, "execute store result score bounded attr_range run "+prefix+" get 2")
					sendCommand(t, pw, "scoreboard players get bounded attr_range")
					found := false
					for _, line := range barrier() {
						key, args, ok := componentArgs(line)
						if ok && key == "commands.scoreboard.players.get.success" && len(args) > 1 {
							v, ok := number(args[1])
							if !ok || v != attr.high*2 {
								t.Errorf("%s %s scaled result %v, want %v", leg.name, attr.name, v, attr.high*2)
							}
							found = true
						}
					}
					if !found {
						t.Fatal("scaled result absent")
					}
				}
			}
			sendCommand(t, pw, "attribute @s minecraft:"+attr.name+" modifier remove test:range")
			sendCommand(t, pw, "attribute @s minecraft:"+attr.name+" modifier remove test:zero")
			sendCommand(t, pw, "attribute @s minecraft:"+attr.name+" base set "+attr.initial)
			barrier()
		}
	}
}
