package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestStewGiveSyntax probes which suspicious_stew_effects SNBT form
// each server accepts via item replace + a SelectedItem check.
func TestStewGiveSyntax(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Eater"),
	}, nil)
	defer targets.cleanup()

	forms := []string{
		`minecraft:suspicious_stew[minecraft:suspicious_stew_effects=[{id:"minecraft:fire_resistance",duration:400}]]`,
		`minecraft:suspicious_stew[minecraft:suspicious_stew_effects=[{id:"fire_resistance",duration:400}]]`,
		`minecraft:suspicious_stew[minecraft:suspicious_stew_effects=[{id:"minecraft:fire_resistance"}]]`,
		`minecraft:suspicious_stew[suspicious_stew_effects=[{id:"fire_resistance",duration:400}]]`,
	}
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "Eater")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			sendCommand(t, pw, "gamemode survival @s")
			consumeDrain(t, c, pr, pw, 500*time.Millisecond)
			for i, f := range forms {
				sendCommand(t, pw, "clear @s")
				sendCommand(t, pw, "item replace entity @s hotbar.0 with "+f)
				sendCommand(t, pw, fmt.Sprintf("execute if entity @s[nbt={SelectedItem:{id:\"minecraft:suspicious_stew\"}}] run say ZEB-ok-%d", i))
				sendCommand(t, pw, fmt.Sprintf("execute unless entity @s[nbt={SelectedItem:{id:\"minecraft:suspicious_stew\"}}] run say ZEB-miss-%d", i))
				deadline := time.Now().Add(10 * time.Second)
				res := "none"
				for time.Now().Before(deadline) && res == "none" {
					for _, e := range consumeDrain(t, c, pr, pw, 300*time.Millisecond) {
						if strings.Contains(e.chat, fmt.Sprintf("ZEB-ok-%d", i)) {
							res = "ok"
						}
						if strings.Contains(e.chat, fmt.Sprintf("ZEB-miss-%d", i)) {
							res = "miss"
						}
					}
				}
				t.Logf("%s form%d -> %s", tc.tag, i, res)
			}
		})
	}
}
