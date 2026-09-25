package blackbox

// TestSheepEatGrassParity: a sheared sheep with AI on, standing on
// grass_block, runs EatBlockGoal — the 1/1000-per-tick canUse roll,
// a 40-tick grazing animation (entity event 10), then the eat:
// grass_block below → dirt, Mob.ate → Sheared=false. A control sheep
// on stone stays sheared. `tick sprint` compresses the roll to a
// deterministic tick count on both servers.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSheepEatGrassParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("SheepE"),
	}, nil)
	defer targets.cleanup()

	probes := []struct{ name, spec string }{
		{"grass sheep un-sheared", `entity @e[type=minecraft:sheep,nbt={Sheared:0b},x=30,y=-60,z=30,distance=..4]`},
		{"a grass cell ate to dirt", `!blocks 29 -61 30 31 -61 32 41 -61 30 all`},
		{"stone sheep still sheared", `entity @e[type=minecraft:sheep,nbt={Sheared:1b},x=36,y=-60,z=30,distance=..4]`},
		{"stone floor untouched", `block 36 -61 30 minecraft:stone`},
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, "SheepE")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		time.Sleep(200 * time.Millisecond)
		sendCommand(t, pw, "tp @s 33 -59 24 0 0")
		time.Sleep(1500 * time.Millisecond)
		// Pens: 3x3 grass pen (stone walls above the floor) at x 29-31
		// and a stone-floored pen at x 35-37 — the sheep roam inside.
		for _, cmd := range []string{
			`fill 28 -62 29 32 -59 33 minecraft:stone`,
			`fill 29 -61 30 31 -61 32 minecraft:grass_block`,
			`fill 29 -60 30 31 -59 32 minecraft:air`,
			`fill 34 -62 29 38 -59 33 minecraft:stone`,
			`fill 35 -60 30 37 -59 32 minecraft:air`,
			// Pristine reference pen — untouched grass for the
			// blocks-compare probe.
			`fill 41 -62 30 43 -61 32 minecraft:grass_block`,
			`fill 41 -60 30 43 -59 32 minecraft:air`,
			`summon minecraft:sheep 36.5 -60 31.5 {Sheared:1b}`,
		} {
			sendCommand(t, pw, cmd)
			time.Sleep(60 * time.Millisecond)
		}
		// Six sheep in the grass pen: every one rolls the 1/1000
		// canUse each tick, so a 3000-tick sprint leaves P(no eat at
		// all) ~1e-8 — deterministic enough without a huge sprint.
		for i := 0; i < 6; i++ {
			sendCommand(t, pw, "summon minecraft:sheep 30.5 -60 31.5 {Sheared:1b}")
			time.Sleep(40 * time.Millisecond)
		}
		// The sprint runs on the command thread: every command sent
		// after it sees post-sprint state, and both servers announce
		// completion with the commands.tick.sprint.report line — wait
		// for it instead of a wall-clock guess.
		sendCommand(t, pw, "tick sprint 3000")
		sprintDeadline := time.Now().Add(120 * time.Second)
		for time.Now().Before(sprintDeadline) {
			done := false
			for _, line := range sink.take() {
				if strings.Contains(line, "Sprint completed") {
					done = true
				}
			}
			if done {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		sink.take()
		var out []string
		for i, p := range probes {
			spec := p.spec
			if strings.HasPrefix(spec, "!") {
				spec = spec[1:]
				sendCommand(t, pw, fmt.Sprintf("execute unless %s run say EG-ok-%d", spec, i))
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say EG-miss-%d", spec, i))
			} else {
				sendCommand(t, pw, fmt.Sprintf("execute if %s run say EG-ok-%d", spec, i))
				sendCommand(t, pw, fmt.Sprintf("execute unless %s run say EG-miss-%d", spec, i))
			}
			sendCommand(t, pw, fmt.Sprintf("say EGK%d", i))
			marker := fmt.Sprintf("EGK%d", i)
			hit := "none"
			deadline := time.Now().Add(4 * time.Second)
			for time.Now().Before(deadline) {
				done := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						done = true
						break
					}
					if strings.Contains(line, fmt.Sprintf("EG-ok-%d", i)) {
						hit = "hit"
					}
					if strings.Contains(line, fmt.Sprintf("EG-miss-%d", i)) {
						hit = "miss"
					}
				}
				if done {
					break
				}
			}
			out = append(out, hit)
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i, p := range probes {
		t.Logf("%-26s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}
