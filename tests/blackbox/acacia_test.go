package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestAcaciaParity grows acacia trees on both servers and compares the
// parts that are not luck: a log trunk always roots at the origin, the
// canopy volume holds at least the main skirt's worth of leaves, the
// rooted dirt, and the 5x5 space check that keeps a crowded acacia a
// stage-1 sapling forever.
//
// Measured on 26.2: acacia.json — ForkingTrunkPlacer {base 5, randA 2,
// randB 2} → 5-9 logs; the column leans one horizontal step per level
// for its top leanSteps(1-3) levels starting at
// leanHeight = treeHeight - U(0..3) - 1, plus an optional staircase
// branch (different random direction) climbing 1-3 logs from
// leanHeight - U(0..1) - 1; attachments: main tip radiusOffset 1,
// branch tip 0. AcaciaFoliagePlacer {radius 2, offset 0}: a skirt row
// at attach-1 of radius 2+radiusOffset (3 main / 2 branch) with
// corners skipped and a full 3x3 cap at the attach level. minsize
// two_layers {upper 2} → the 5x5 check from y>=1.
func TestAcaciaParity(t *testing.T) {
	const probe = "AcaciaParity"
	tg := requireBlackboxFiles(t,
		map[string]string{
			"command-spam-threshold-seconds": "0",
			"simulation-distance":            "3",
		},
		map[string]string{"ops.json": opsJSONFor(probe)},
		func(s *server.Server) { s.SimulationDistance = 3 })
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runAcaciaScenarios(t, tg.vanilla, probe)
	got := runAcaciaScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("probe cells: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	for i := 0; i < acaciaCount; i++ {
		// Main column alone writes treeHeight(5-9) logs; a branch can
		// add up to 3 more.
		if want.logs[i] < 5 {
			t.Errorf("acacia %d: vanilla has only %d acacia_log cells", i, want.logs[i])
		}
		if got.logs[i] < 5 {
			t.Errorf("acacia %d: mc-go has only %d acacia_log cells", i, got.logs[i])
		}
		// The main umbrella alone is a 7x7-minus-corners skirt (45)
		// below the crown and a 3x3 cap — twenty-five leaves in the
		// scanned band is a conservative floor.
		if want.leaves[i] < 25 {
			t.Errorf("acacia %d: vanilla has only %d acacia_leaves cells", i, want.leaves[i])
		}
		if got.leaves[i] < 25 {
			t.Errorf("acacia %d: mc-go has only %d acacia_leaves cells", i, got.leaves[i])
		}
	}
}

const acaciaCount = 3

type acaciaRun struct {
	names  map[[3]int]string
	logs   [acaciaCount]int
	leaves [acaciaCount]int
}

func runAcaciaScenarios(t *testing.T, addr, name string) acaciaRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCmd(t, pw, "time set day")
	sendCmd(t, pw, "gamerule random_tick_speed 600")
	sendCmd(t, pw, "tick freeze")
	waitDrained(t, pw, sink)

	// Three acacia 10 apart (the umbrella reaches +-4 around each
	// attachment, and the trunk leans up to 3). A crowded acacia whose
	// (x+2,y+1) stone sits inside its 5x5 stays a stage-1 sapling.
	baseY := -60
	xs := [acaciaCount]int{4, 14, 24}
	for _, x := range xs {
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" -61 -44 minecraft:dirt")
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" "+itoa(int32(baseY))+" -44 minecraft:acacia_sapling")
	}
	sendCmd(t, pw, "setblock 4 -61 -48 minecraft:dirt")
	sendCmd(t, pw, "setblock 4 "+itoa(int32(baseY))+" -48 minecraft:acacia_sapling")
	sendCmd(t, pw, "setblock 6 "+itoa(int32(baseY+1))+" -48 minecraft:stone")
	waitDrained(t, pw, sink)

	sprintTicksLoose(t, pw, sink, 300)

	out := acaciaRun{names: map[[3]int]string{}}

	candidates := []string{
		"minecraft:acacia_sapling", "minecraft:acacia_log", "minecraft:acacia_leaves",
		"minecraft:dirt", "minecraft:stone", "minecraft:air",
	}
	out.names = scanNames(t, pw, sink, candidates, [][3]int{
		{4, baseY, -44},     // the origin log cell of tree 0
		{4, baseY - 1, -44}, // its dirt
		{4, baseY, -48},     // the crowded acacia (stage-1 sapling)
		{6, baseY + 1, -48}, // the blocking stone
	})

	// Count scan: the whole grown volume — the lean and branch keep
	// the trunk within +-4 of the origin and the canopy band sits at
	// treeHeight-1..treeHeight, covered by y+2..y+10.
	for i, x := range xs {
		var box [][3]int
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				for y := baseY; y <= baseY+10; y++ {
					box = append(box, [3]int{x + dx, y, -44 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink,
			[]string{"minecraft:acacia_log", "minecraft:acacia_leaves"}, box)
		for _, v := range names {
			switch v {
			case "minecraft:acacia_log":
				out.logs[i]++
			case "minecraft:acacia_leaves":
				out.leaves[i]++
			}
		}
	}
	return out
}

// scanNames asks which of a set of candidate blocks is at each
// position, like queryNames but unpaced (the probe disables both
// servers' command-spam thresholds, so a command flood is not
// throttled). Each position's batch ends with an always-true marker:
// when the marker answers, every `if` probe in that batch ran, so a
// missing hit is a real miss. Positions whose marker never arrived are
// re-asked until they answer.
func scanNames(t *testing.T, pw *protocol.Writer, sink *chatSink, candidates []string, positions [][3]int) map[[3]int]string {
	t.Helper()
	out := map[[3]int]string{}
	todo := make([][3]int, len(positions))
	copy(todo, positions)

	for pass := 0; pass < 4 && len(todo) > 0; pass++ {
		sink.take()
		const sentinel = "SCANDONE"
		for _, p := range todo {
			tag := levelTag(p)
			for i, name := range candidates {
				sendCommand(t, pw, fmt.Sprintf(
					"execute if block %d %d %d %s run say %sC%dY",
					p[0], p[1], p[2], name, tag, i))
			}
			sendCommand(t, pw, "say "+tag+"Z")
		}
		sendCommand(t, pw, "say "+sentinel)
		lines := waitForMarker(t, sink, sentinel, 120*time.Second)

		var next [][3]int
		for _, p := range todo {
			tag := levelTag(p)
			if !sawMarker(lines, tag+"Z") {
				if pass == 3 {
					t.Fatalf("position %v never answered", p)
				}
				next = append(next, p)
				continue
			}
			var hits []string
			for i, name := range candidates {
				if sawMarker(lines, fmt.Sprintf("%sC%dY", tag, i)) {
					hits = append(hits, name)
				}
			}
			if len(hits) == 0 {
				out[p] = "?"
			} else {
				out[p] = strings.Join(hits, "+")
			}
		}
		todo = next
	}
	return out
}
