package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestAttributeModifierPassParity(t *testing.T) {
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
			marker := fmt.Sprintf("ATTR_PASS_BARRIER%d", serial)
			sendCommand(t, pw, "say "+marker)
			return waitForExactSayBB(t, sink, marker, 15*time.Second)
		}
		cases := [][]string{{}, {"add 2 add_value"}, {"add 2 add_value", "base 0.5 add_multiplied_base"}, {"add 2 add_value", "base 0.5 add_multiplied_base", "total 1 add_multiplied_total"}, {"add 2 add_value", "base 0.25 add_multiplied_base", "base_two 0.25 add_multiplied_base", "total 0.5 add_multiplied_total", "total_two 1 add_multiplied_total"}}
		for _, attr := range []string{"attack_damage", "luck"} {
			for index, mods := range cases {
				prefix := "attribute @s minecraft:" + attr
				for _, id := range []string{"add", "base", "base_two", "total", "total_two"} {
					sendCommand(t, pw, prefix+" modifier remove test:"+id)
				}
				sendCommand(t, pw, prefix+" base set 10")
				for _, mod := range mods {
					sendCommand(t, pw, prefix+" modifier add test:"+mod)
				}
				barrier()
				sendCommand(t, pw, prefix+" get")
				found := false
				for _, line := range barrier() {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.attribute.value.get.success" && len(args) == 3 {
						value := args[2]
						if wrapped, ok := value.(map[string]any); ok {
							value = wrapped[""]
						}
						actual := 0.0
						valid := true
						switch number := value.(type) {
						case float64:
							actual = number
						case float32:
							actual = float64(number)
						case int64:
							actual = float64(number)
						case int32:
							actual = float64(number)
						case int:
							actual = float64(number)
						default:
							valid = false
						}
						expected := []float64{10, 12, 18, 36, 54}[index]
						if !valid || actual != expected {
							t.Errorf("%s %s case%d value %v, want %v", leg.name, attr, index, args[2], expected)
						}
						t.Logf("%s %s case%d value=%v", leg.name, attr, index, args[2])
						found = true
					}
				}
				if !found {
					t.Fatalf("%s %s case%d has no query result", leg.name, attr, index)
				}
			}
		}
		c.Close()
	}
}
