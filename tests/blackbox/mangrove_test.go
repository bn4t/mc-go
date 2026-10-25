// TestMangroveParity pins mangrove + tall_mangrove against vanilla's
// mangrove.json / tall_mangrove.json configured features
// (server-26.2.jar → data/minecraft/worldgen/configured_feature):
// UpwardsBranchingTrunkPlacer stilt trunk + 0.5 branch rolls,
// MangroveRootPlacer's simulated root walk, RandomSpreadFoliagePlacer
// and the {leave_vine .125, attached_to_leaves propagule, beehive}
// decorator set. Counts come from `fill <box> air replace <name>`,
// which reports the removed count — floors, not exact equality, since
// placements roll their own RNG on each server.
package blackbox

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

type mangroveRun struct {
	failures int
	sapGrew  bool
	logs     int
	tallLogs int
	roots    int
	leaves   int
	prop     int
	vine     int
	carpet   int
}

func TestMangroveParity(t *testing.T) {
	const probe = "MangroveParity"
	tg := requireBlackboxFiles(t,
		map[string]string{
			"command-spam-threshold-seconds": "0",
			"random_tick_speed":              "600",
		},
		map[string]string{"ops.json": opsJSONFor(probe)},
		nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runMangroveScenarios(t, tg.vanilla, probe)
	got := runMangroveScenarios(t, tg.mcgo, probe)

	// A placement legitimately fails when a simulated root chain
	// reaches layer 15 — vanilla and mc-go share the mechanic but not
	// the RNG stream, so per-run failure counts drift a little.
	if want.failures > 2 || got.failures > 2 {
		t.Errorf("/place failures: vanilla=%d mc-go=%d (want <=2)", want.failures, got.failures)
	}
	if want.sapGrew != got.sapGrew {
		t.Errorf("propagule grew: vanilla=%v mc-go=%v", want.sapGrew, got.sapGrew)
	}
	floors := map[string]int{
		"logs": 5, "tallLogs": 8, "roots": 3, "leaves": 30,
		"prop": 1, "vine": 1, "carpet": 1,
	}
	for name, w := range map[string][2]int{
		"logs":     {want.logs, got.logs},
		"tallLogs": {want.tallLogs, got.tallLogs},
		"roots":    {want.roots, got.roots},
		"leaves":   {want.leaves, got.leaves},
		"prop":     {want.prop, got.prop},
		"vine":     {want.vine, got.vine},
		"carpet":   {want.carpet, got.carpet},
	} {
		if w[0] < floors[name] || w[1] < floors[name] {
			t.Errorf("%s: vanilla=%d mc-go=%d (want >=%d)", name, w[0], w[1], floors[name])
		}
	}
	t.Logf("mangrove: logs v=%d g=%d, tall v=%d g=%d, roots v=%d g=%d, leaves v=%d g=%d, prop v=%d g=%d, vine v=%d g=%d, carpet v=%d g=%d",
		want.logs, got.logs, want.tallLogs, got.tallLogs, want.roots, got.roots,
		want.leaves, got.leaves, want.prop, got.prop, want.vine, got.vine, want.carpet, got.carpet)
}

// countFill removes every <name> cell in the box via `fill ... air
// replace` and returns the summed "Successfully filled N" count. Each
// sub-fill stays inside the shared 32768-cell cap both servers enforce.
func countFill(t *testing.T, pw *protocol.Writer, sink *chatSink,
	name string, x0, y0, z0, x1, y1, z1 int) int {
	t.Helper()
	total := 0
	for x := x0; x <= x1; x += 42 {
		xe := x + 41
		if xe > x1 {
			xe = x1
		}
		for z := z0; z <= z1; z += 30 {
			ze := z + 29
			if ze > z1 {
				ze = z1
			}
			for y := y0; y <= y1; y += 26 {
				ye := y + 25
				if ye > y1 {
					ye = y1
				}
				sendCommand(t, pw, fmt.Sprintf(
					"fill %d %d %d %d %d %d minecraft:air replace %s",
					x, y, z, xe, ye, ze, name))
			}
		}
	}
	sendCommand(t, pw, "say CNT_"+name)
	lines := waitForMarker(t, sink, "CNT_"+name, 60*time.Second)
	for _, l := range lines {
		key, args, ok := componentArgs(l)
		if !ok || key != "commands.fill.success" || len(args) == 0 {
			continue
		}
		switch v := args[0].(type) {
		case int8:
			total += int(v)
		case int16:
			total += int(v)
		case int32:
			total += int(v)
		case int64:
			total += int(v)
		case float32:
			total += int(v)
		case float64:
			total += int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				total += n
			}
		}
	}
	return total
}

func runMangroveScenarios(t *testing.T, addr, name string) mangroveRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCmd(t, pw, "time set day")
	waitDrained(t, pw, sink)

	baseY := -60
	out := mangroveRun{}

	// Dirt pad under every site: the root walk terminates on the
	// floor instead of failing at layer 15 in open air. Roots wander
	// up to ~8 cells from each trunk, so the pad overhangs the sites.
	sendCmd(t, pw, "fill -50 -61 -70 95 -61 -4 minecraft:dirt")
	sink.take()

	// Four mangrove + four tall_mangrove placements, spaced for the
	// canopy spread; each /place answers success|failed in chat.
	for _, x := range []int{-40, -20, 0, 20} {
		sendCmd(t, pw, "place feature minecraft:mangrove "+
			itoa(int32(x))+" "+itoa(int32(baseY))+" -44")
	}
	for _, x := range []int{40, 55, 70, 85} {
		sendCmd(t, pw, "place feature minecraft:tall_mangrove "+
			itoa(int32(x))+" "+itoa(int32(baseY))+" -56")
	}
	sendCommand(t, pw, "say PLACESDONE")
	lines := waitForMarker(t, sink, "PLACESDONE", 60*time.Second)
	for _, l := range lines {
		if strings.Contains(l, "place.feature.failed") ||
			strings.Contains(l, "Failed to place feature") {
			out.failures++
		}
	}
	waitDrained(t, pw, sink)

	// Planted propagules on an isolated row (z=-16, ten cells clear
	// of the placed trees' reach): random ticks advance stage 0→1
	// then grow (the 0.85 secondary roll inside makes the grown tree
	// almost always the tall spec).
	for _, x := range []int{-40, -20, 0, 20} {
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" "+itoa(int32(baseY))+" -16 minecraft:mangrove_propagule")
	}
	waitDrained(t, pw, sink)
	sendCommand(t, pw, "say MANGWAIT")
	_ = waitForMarker(t, sink, "MANGWAIT", 75*time.Second)

	// Counting mutates AND pops dependents, so decorations go first:
	// propagules pop when the leaf above is removed, vines pop when
	// their attach cell goes, and moss carpets pop when the cell
	// below goes. trunks, roots and leaves outlast everything else.
	out.vine = countFill(t, pw, sink, "minecraft:vine",
		-45, baseY-2, -64, 95, baseY+22, -40)
	out.prop = countFill(t, pw, sink, "minecraft:mangrove_propagule",
		-45, baseY-2, -64, 95, baseY+22, -40)
	out.carpet = countFill(t, pw, sink, "minecraft:moss_carpet",
		-45, baseY-2, -64, 95, baseY+22, -40)
	out.leaves = countFill(t, pw, sink, "minecraft:mangrove_leaves",
		-45, baseY-2, -64, 95, baseY+22, -40)
	out.roots = countFill(t, pw, sink, "minecraft:mangrove_roots",
		-45, baseY-2, -64, 95, baseY+22, -40) +
		countFill(t, pw, sink, "minecraft:muddy_mangrove_roots",
			-45, baseY-2, -64, 95, baseY+22, -40)
	// The tall zone's logs are counted and removed first so the big
	// region's remainder is the mangrove-side count.
	out.tallLogs = countFill(t, pw, sink, "minecraft:mangrove_log",
		35, baseY-2, -64, 95, baseY+22, -48)
	out.logs = countFill(t, pw, sink, "minecraft:mangrove_log",
		-45, baseY-2, -64, 95, baseY+22, -40)
	out.sapGrew = countFill(t, pw, sink, "minecraft:mangrove_log",
		-45, baseY-1, -30, 25, baseY+18, -2) > 0
	return out
}
