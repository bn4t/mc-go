//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestScoreboardCriteriaParity pins the criteria feeds: the live
// criteria (health/xp/level/food/air/armor) are event-driven — they
// stay unset until the event that owns them fires — a stat award
// writes the new total into every tracking objective, and an
// untouched tracked criterion stays unset.
func TestScoreboardCriteriaParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{
			"ops.json": opsJSONFor("P"),
		}, nil)
	defer targets.cleanup()

	setup := []string{
		`scoreboard objectives add zh health`,
		`scoreboard objectives add zxp xp`,
		`scoreboard objectives add zlv level`,
		`scoreboard objectives add zfd food`,
		`scoreboard objectives add zar armor`,
		`scoreboard objectives add zdc deathCount`,
		`scoreboard objectives add zair air`,
		`scoreboard objectives add zpt minecraft.custom:minecraft.play_time`,
		`scoreboard objectives add zmn minecraft.mined:minecraft.stone`,
	}

	// legs run in order; each is a command whose `execute if score`
	// (or unless score) resolves after the tick has had time to feed.
	legs := []struct {
		name string
		// cmds run before the probe; probe is the execute.
		cmds  []string
		probe string
	}{
		{"fresh health", nil, `execute if score P zh matches 20 run say ZC-ok-%d`},
		{"fresh food", nil, `execute if score P zfd matches 20 run say ZC-ok-%d`},
		{"fresh armor", nil, `execute if score P zar matches 0 run say ZC-ok-%d`},
		{"fresh air", nil, `execute if score P zair matches 300 run say ZC-ok-%d`},
		{"untracked unset", nil, `execute unless score P zmn matches -2147483648..2147483647 run say ZC-ok-%d`},
		{"xp five", []string{`xp add P 5 points`}, `execute if score P zxp matches 5 run say ZC-ok-%d`},
		{"level still zero", nil, `execute if score P zlv matches 0 run say ZC-ok-%d`},
		{"xp eight", []string{`xp add P 3 points`}, `execute if score P zxp matches 8 run say ZC-ok-%d`},
		{"level one", nil, `execute if score P zlv matches 1 run say ZC-ok-%d`},
		{"armor chest", []string{`item replace entity P armor.chest with minecraft:iron_chestplate`}, `execute if score P zar matches 6 run say ZC-ok-%d`},
		{"damage 3 armored", []string{`damage P 3`}, `execute if score P zh matches 18 run say ZC-ok-%d`},
		{"zh set", nil, `execute if score P zh matches -2147483648..2147483647 run say ZC-ok-%d`},
		{"zh 17", nil, `execute if score P zh matches 17 run say ZC-ok-%d`},
		{"readonly set", []string{`scoreboard players set P zfd 5`}, `execute if score P zfd matches 5 run say ZC-ok-%d`},
		{"play_time counts", nil, `execute if score P zpt matches 1.. run say ZC-ok-%d`},
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		// Give the first live-criteria refresh a moment.
		time.Sleep(1500 * time.Millisecond)

		var out []string
		for i, leg := range legs {
			for _, cmd := range leg.cmds {
				sendCommand(t, pw, cmd)
			}
			time.Sleep(600 * time.Millisecond)
			sendCommand(t, pw, fmt.Sprintf(leg.probe, i))
			sendCommand(t, pw, fmt.Sprintf("say ZCK%d", i))
			marker := fmt.Sprintf("ZCK%d", i)
			hit := "miss"
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				done := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						done = true
					}
					if strings.Contains(line, fmt.Sprintf("ZC-ok-%d", i)) {
						hit = "hit"
					}
				}
				if done {
					break
				}
				time.Sleep(40 * time.Millisecond)
			}
			out = append(out, hit)
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i, leg := range legs {
		t.Logf("%-24s vanilla=%s mcgo=%s", leg.name, vanilla[i], mcgo[i])
		if vanilla[i] != mcgo[i] {
			t.Errorf("leg %q: vanilla %s, mc-go %s", leg.name, vanilla[i], mcgo[i])
		}
	}
}
