// Turtle egg random-tick parity: TurtleEggBlock.randomTick gates on
// the day timeline's turtle_egg_hatch_chance attribute — constant
// keyframes 21062->1.0 and 21905->0.002, i.e. a guaranteed progress
// roll inside the early-night window and a 1/500 trickle outside —
// and requires a #sand block below. hatch<2 bumps the stage with a
// crack sound; hatch==2 removes the egg, plays the hatch sound, and
// per egg runs a 2001 break effect then spawns a baby turtle at
// x+0.3+i*0.2 / z+0.3, age -24000, home the egg position, zero
// yaw/pitch.
package blackbox

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// nbtWithTailInt decodes an NBT text component payload and returns the
// trailing number of its rendered text — for a
// commands.data.entity.get reply that last argument is the fetched
// value.
func nbtWithTailInt(data string) (int, bool) {
	root, err := nbt.ReadUnnamed(bytes.NewReader([]byte(data)))
	if err != nil {
		return 0, false
	}
	ms := dataNumLooseRe.FindAllStringSubmatch(componentPlain(root), -1)
	if len(ms) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(ms[len(ms)-1][1], 64)
	if err != nil {
		return 0, false
	}
	return int(f), true
}

// TestTurtleEggHatchParity pins the hatch mechanics end to end: a
// sand egg at hatch=2 hatches immediately inside the window with
// babies at the measured offsets, a fresh egg climbs and hatches,
// an egg off sand stays, and the babies carry Age -24000 plus
// home_pos at the egg site.
func TestTurtleEggHatchParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`gamerule random_tick_speed 0`,
		`time set 1000`,
		// Sand pad for the hatch legs; a stone pad isolates the
		// off-sand egg — onSand reads the block directly below.
		`fill 100 -61 100 104 -61 104 minecraft:sand`,
		`fill 106 -61 106 108 -61 108 minecraft:stone`,
		// Egg A: four mature eggs — the first passing roll hatches
		// all four babies at once.
		`setblock 101 -60 101 minecraft:turtle_egg[eggs=4,hatch=2]`,
		// Egg B: a fresh egg climbs hatch 0 -> 2 then hatches.
		`setblock 103 -60 103 minecraft:turtle_egg[eggs=2,hatch=0]`,
		// Egg C: mature but on stone — onSand fails forever.
		`setblock 107 -60 107 minecraft:turtle_egg[eggs=1,hatch=2]`,
		`tp @s 102.5 -59 102.5`,
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZT-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZT-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZTK%d", i))
		marker := fmt.Sprintf("ZTK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZT-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZT-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	// entProbe is the @e selector analogue — execute if entity.
	entProbe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if entity %s run say ZE-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless entity %s run say ZE-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZEK%d", i))
		marker := fmt.Sprintf("ZEK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZE-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZE-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	// ageProbe data-gets a baby's Age: vanilla hatches at -24000 and
	// counts up, so the assert is the open interval (-24000, 0).
	ageProbe := func(pw *protocol.Writer, sink *chatSink, sel string, i int) int {
		sendCommand(t, pw, fmt.Sprintf("data get entity %s Age", sel))
		sendCommand(t, pw, fmt.Sprintf("say ZAK%d", i))
		marker := fmt.Sprintf("ZAK%d", i)
		age := 0
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return age
				}
				// The reply ships as a commands.data.entity.query
				// translatable over an NBT component; the Age is the
				// last `with` argument.
				if strings.HasPrefix(line, "0x79:") && strings.Contains(line, "commands.data.entity.query") {
					if v, ok := nbtWithTailInt(strings.TrimPrefix(line, "0x79:")); ok {
						age = v
					}
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return age
	}

	// countTurtles says one marker per matching turtle and counts the
	// replies — the eggs=N headcount around a hatched site.
	countTurtles := func(pw *protocol.Writer, sink *chatSink, sel string, i int) int {
		sendCommand(t, pw, fmt.Sprintf("execute as %s run say ZTC-%d", sel, i))
		sendCommand(t, pw, fmt.Sprintf("say ZCK%d", i))
		marker := fmt.Sprintf("ZCK%d", i)
		n := -1
		cnt := 0
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					n = cnt
					goto done
				}
				if strings.Contains(line, fmt.Sprintf("ZTC-%d", i)) {
					cnt++
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
	done:
		return n
	}

	run := func(addr string) ([]string, []int, int) {
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
		time.Sleep(1500 * time.Millisecond)

		var out []string
		var counts []int
		out = append(out, probe(pw, sink, "101 -60 101 minecraft:turtle_egg[hatch=2]", 0))
		out = append(out, probe(pw, sink, "103 -60 103 minecraft:turtle_egg[hatch=0]", 1))
		out = append(out, probe(pw, sink, "107 -60 107 minecraft:turtle_egg[hatch=2]", 2))
		// Step the clock into the 1.0-chance window and let the fast
		// tick run: every cell rolls ~once per tick, so the two sand
		// eggs are guaranteed progress each tick.
		sendCommand(t, pw, `time set 21100`)
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(5 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		out = append(out, probe(pw, sink, "101 -60 101 minecraft:air", 3))
		out = append(out, probe(pw, sink, "103 -60 103 minecraft:air", 4))
		out = append(out, probe(pw, sink, "107 -60 107 minecraft:turtle_egg[hatch=2]", 5))
		out = append(out, entProbe(pw, sink,
			`@e[type=minecraft:turtle,x=103.5,y=-59.5,z=103.5,distance=..10,nbt={home_pos:[I;103,-60,103]}]`, 6))
		out = append(out, entProbe(pw, sink,
			`@e[type=minecraft:turtle,x=101.5,y=-59.5,z=101.5,distance=..10,nbt={has_egg:0b}]`, 7))
		counts = append(counts, countTurtles(pw, sink,
			`@e[type=minecraft:turtle,x=101.5,y=-59.5,z=101.5,distance=..10]`, 8))
		counts = append(counts, countTurtles(pw, sink,
			`@e[type=minecraft:turtle,x=103.5,y=-59.5,z=103.5,distance=..10]`, 9))
		ageA := ageProbe(pw, sink,
			`@e[type=minecraft:turtle,sort=nearest,limit=1,x=101.5,y=-59.5,z=101.5,distance=..10]`, 10)
		return out, counts, ageA
	}

	van, vanC, vanAge := run(targets.vanilla)
	mcg, mcgC, mcgAge := run(targets.mcgo)
	names := []string{
		"fixture A egg present", "fixture B egg present", "fixture C egg present",
		"egg A hatched to air", "egg B hatched to air", "off-sand egg stays",
		"site B baby home_pos", "site A baby has_egg",
	}
	for i, name := range names {
		t.Logf("%-28s vanilla=%s mcgo=%s", name, van[i], mcg[i])
		if van[i] == "hit" && mcg[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", name, mcg[i])
		}
		if mcg[i] == "hit" && van[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", name, van[i])
		}
	}
	for i, site := range []string{"site A", "site B"} {
		t.Logf("%-28s vanilla=%d mcgo=%d", site+" turtle count", vanC[i], mcgC[i])
		if vanC[i] != mcgC[i] {
			t.Errorf("%s turtle count: vanilla %d, mc-go %d", site, vanC[i], mcgC[i])
		}
	}
	t.Logf("%-28s vanilla=%d mcgo=%d", "site A baby age", vanAge, mcgAge)
	// Vanilla hatches at -24000 and ages up; both legs must sit in
	// (-24000, 0) — grown-adult (0) or a stuck -24000 both fail.
	for _, pair := range [][2]int{{vanAge, 1}, {mcgAge, 2}} {
		if pair[0] <= -24000 || pair[0] >= 0 {
			which := "vanilla"
			if pair[1] == 2 {
				which = "mc-go"
			}
			t.Errorf("%s baby age %d outside (-24000, 0)", which, pair[0])
		}
	}
}
