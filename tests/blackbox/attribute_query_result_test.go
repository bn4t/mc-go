package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestAttributeQueryResultParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, pr, pw := joinPlayBlackbox(t, leg.addr, "P")
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("ATTR_QUERY_BARRIER%d", serial)
			sendCommand(t, pw, "say "+marker)
			return waitForExactSayBB(t, sink, marker, 15*time.Second)
		}
		sendCommand(t, pw, "scoreboard objectives add attr_query dummy")
		barrier()
		sendCommand(t, pw, `summon pig 12 -60 12 {Tags:["attr_query_pig"],NoAI:1b}`)
		sendCommand(t, pw, "execute if entity @e[tag=attr_query_pig,limit=1] run say ATTR_QUERY_PIG_READY")
		if !structureSaidBB(barrier(), "ATTR_QUERY_PIG_READY") {
			t.Fatal("missing prepared pig")
		}

		cases := []struct {
			name, base, query string
			modifier          bool
		}{
			{"value", "2.5", "get", false},
			{"value scaled", "2.5", "get 2", false},
			{"base scaled", "2.5", "base get 2", false},
			{"modifier scaled", "2.5", "modifier value get test:result 2", true},
			{"negative value", "-2.5", "get 0.5", false},
			{"negative base", "-2.5", "base get 0.5", false},
			{"successful zero", "0", "get 2", false},
			{"zero scale", "2.5", "get 0", false},
			{"negative scale", "2.5", "get -0.5", false},
			{"missing modifier", "2.5", "modifier value get test:missing", false},
			{"positive saturation", "2.5", "get 100000000000000000000", false},
			{"negative saturation", "2.5", "get -100000000000000000000", false},
			{"base setter", "2.5", "base set 3", false},
			{"base reset", "2.5", "base reset", false},
			{"modifier add", "2.5", "modifier add test:added 0.25 add_value", false},
			{"duplicate modifier", "2.5", "modifier add test:result 1 add_value", true},
			{"modifier remove", "2.5", "modifier remove test:result", true},
			{"missing remove", "2.5", "modifier remove test:missing", false},
			{"missing target", "2.5", "attribute @e[tag=attr_query_absent,limit=1] minecraft:luck get", false},
			{"missing attribute", "2.5", "attribute @e[tag=attr_query_pig,limit=1] minecraft:luck get", false},
		}
		type expectation struct {
			result, success int64
			display         any
		}
		expected := map[string]expectation{
			"value": {2, 1, 2.5}, "value scaled": {5, 1, 2.5}, "base scaled": {5, 1, 2.5}, "modifier scaled": {-1, 1, -.75},
			"negative value": {-1, 1, -2.5}, "negative base": {-1, 1, -2.5}, "successful zero": {0, 1, float64(0)}, "zero scale": {0, 1, 2.5}, "negative scale": {-1, 1, 2.5},
			"missing modifier": {0, 0, nil}, "positive saturation": {2147483647, 1, 2.5}, "negative saturation": {-2147483648, 1, 2.5},
			"base setter": {1, 1, nil}, "base reset": {1, 1, nil}, "modifier add": {1, 1, nil}, "duplicate modifier": {0, 0, nil}, "modifier remove": {1, 1, nil}, "missing remove": {0, 0, nil}, "missing target": {0, 0, nil}, "missing attribute": {0, 0, nil},
		}
		number := func(v any) (float64, bool) {
			switch n := v.(type) {
			case int64:
				return float64(n), true
			case int32:
				return float64(n), true
			case int:
				return float64(n), true
			case float64:
				return n, true
			case float32:
				return float64(n), true
			default:
				return 0, false
			}
		}
		unwrap := func(v any) any {
			if m, ok := v.(map[string]any); ok {
				return m[""]
			}
			return v
		}
		for _, tc := range cases {
			sendCommand(t, pw, "attribute @s minecraft:luck modifier remove test:result")
			sendCommand(t, pw, "attribute @s minecraft:luck modifier remove test:added")
			sendCommand(t, pw, "attribute @s minecraft:luck base set "+tc.base)
			if tc.modifier {
				sendCommand(t, pw, "attribute @s minecraft:luck modifier add test:result -0.75 add_value")
			}
			sendCommand(t, pw, "scoreboard players set out attr_query 999")
			sendCommand(t, pw, "scoreboard players set ok attr_query 999")
			barrier()
			query := "attribute @s minecraft:luck " + tc.query
			if strings.HasPrefix(tc.query, "attribute ") {
				query = tc.query
			}
			sendCommand(t, pw, "execute store result score out attr_query store success score ok attr_query run "+query)
			sendCommand(t, pw, "scoreboard players get out attr_query")
			sendCommand(t, pw, "scoreboard players get ok attr_query")
			var result, success, display any
			for _, line := range barrier() {
				key, args, ok := componentArgs(line)
				if !ok {
					continue
				}
				if key == "commands.scoreboard.players.get.success" && len(args) > 1 {
					switch fmt.Sprint(unwrap(args[0])) {
					case "out":
						result = unwrap(args[1])
					case "ok":
						success = unwrap(args[1])
					}
				}
				if strings.HasPrefix(key, "commands.attribute.") && strings.HasSuffix(key, "get.success") && len(args) > 0 {
					display = unwrap(args[len(args)-1])
				}
			}
			if result == nil || success == nil {
				t.Fatalf("%s %s missing score results", leg.name, tc.name)
			}
			want := expected[tc.name]
			actualResult, resultOK := number(result)
			actualSuccess, successOK := number(success)
			if !resultOK || !successOK || actualResult != float64(want.result) || actualSuccess != float64(want.success) {
				t.Errorf("%s %s result/success %v/%v, want %d/%d", leg.name, tc.name, result, success, want.result, want.success)
			}
			if want.display == nil {
				if display != nil {
					t.Errorf("%s %s unexpected getter display %v", leg.name, tc.name, display)
				}
			} else {
				actual, ok := number(display)
				if !ok || actual != want.display.(float64) {
					t.Errorf("%s %s display %v, want unscaled %v", leg.name, tc.name, display, want.display)
				}
			}
			t.Logf("%s %s result=%v success=%v display=%v", leg.name, tc.name, result, success, display)
		}
		c.Close()
	}
}
