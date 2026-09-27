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
// ADDS its delta into every tracking objective (stat objectives
// count forward from creation, never backfill, and add on top of a
// manual /scoreboard players set), a stat reset writes score 0 into
// tracking objectives, and an untouched tracked criterion stays unset.
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
		`scoreboard objectives add zkz minecraft.killed:minecraft.zombie`,
		`scoreboard objectives add zt minecraft.custom:minecraft.time_since_death`,
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
		// The stat feed ADDS the awarded delta on top of whatever the
		// score currently is — a manual players set stays in the sum.
		{"zombie stat feed", []string{
			`summon minecraft:zombie ~ ~-2 ~ {NoAI:1b,Tags:["zka"]}`,
			`damage @e[type=minecraft:zombie,tag=zka,limit=1] 100 minecraft:mob_attack by P`,
		}, `execute if score P zkz matches 1 run say ZC-ok-%d`},
		{"stat set then award", []string{
			`scoreboard players set P zkz 7`,
			`summon minecraft:zombie ~ ~-2 ~ {NoAI:1b,Tags:["zkb"]}`,
			`damage @e[type=minecraft:zombie,tag=zkb,limit=1] 100 minecraft:mob_attack by P`,
		}, `execute if score P zkz matches 8 run say ZC-ok-%d`},
		// An objective created after the kills sees none of them —
		// stat criteria never backfill.
		{"no backfill", []string{
			`scoreboard objectives add zkz2 minecraft.killed:minecraft.zombie`,
		}, `execute unless score P zkz2 matches -2147483648..2147483647 run say ZC-ok-%d`},
		{"late set then award", []string{
			`scoreboard players set P zkz2 3`,
			`summon minecraft:zombie ~ ~-2 ~ {NoAI:1b,Tags:["zkc"]}`,
			`damage @e[type=minecraft:zombie,tag=zkc,limit=1] 100 minecraft:mob_attack by P`,
		}, `execute if score P zkz2 matches 4 run say ZC-ok-%d`},
		// Submerged, the air meter drops and feeds its criterion.
		{"air drop", []string{
			`execute at P run fill ~-1 ~-1 ~-1 ~1 ~1 ~1 minecraft:water`,
		}, `execute unless score P zair matches 300 run say ZC-ok-%d`},
		// resetStat writes score 0 into tracking objectives — even one
		// the player never scored on (getOrCreate + set(0) broadcasts).
		{"death reset writes zero", []string{
			`kill P`,
		}, `execute if score P zt matches ..40 run say ZC-ok-%d`},
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
