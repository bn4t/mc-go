package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

// vanillaLogTail reports the tail of the vanilla server's log, so a
// disconnect is explained by the server's own words rather than guessed
// at from a broken pipe.
func vanillaLogTail(t *testing.T) {
	t.Helper()
	if vanillaDirForDebug == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(vanillaDirForDebug, "vanilla.log"))
	if err != nil {
		return
	}
	t.Logf("vanilla log tail:\n%s", tail(b, 4000))
}

// TestFluidParity drives the same fluid scenarios on mc-go and on the
// real server and requires the resulting worlds to be identical.
//
// Both servers are frozen and advanced with /tick step, so the
// comparison is over an exact number of simulated ticks rather than
// however many happened to elapse. The world is read back with
// /execute if block, one predicate per state, which asks the server what
// is there instead of trusting the Block Update broadcast.
func TestFluidParity(t *testing.T) {
	const probe = "FluidParity"
	// The ~130 query commands per scenario ride on the game's frozen
	// clock — vanilla's command-spam throttle (20/packet, decay 1/tick,
	// drops at 200) counts them anyway and silently eats the following
	// /time query; a zero threshold disables it.
	tg := requireBlackboxFiles(t,
		map[string]string{"command-spam-threshold-seconds": "0"},
		map[string]string{
			"ops.json": opsJSONFor(probe),
		}, nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runFluidScenarios(t, tg.vanilla, probe)
	got := runFluidScenarios(t, tg.mcgo, probe)

	for _, name := range scenarioNames() {
		w := want.levels[name]
		g := got.levels[name]
		if diff := diffLevels(w, g, vanillaUnstable[name]); len(diff) > 0 {
			t.Errorf("%s: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
				name, renderDiff(w, g), renderLevels(g))
		}
	}
	for _, name := range namedScenarioNames() {
		w := want.names[name]
		g := got.names[name]
		if !sameNames(w, g) {
			t.Errorf("%s: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
				name, renderNameDiff(w, g), renderNames(g))
		}
	}
}

// namedScenarioNames is the fixed comparison order for the scenarios
// compared by block name rather than fluid level.
func namedScenarioNames() []string {
	return []string{"D: water reaching lava", "E: water falling onto lava"}
}

func sameNames(a, b map[[3]int]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// renderNames lists an observed named world, for failure output.
func renderNames(m map[[3]int]string) string {
	keys := make([][3]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][2] < keys[j][2]
	})
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "  (%4d,%4d,%4d) %s\n", k[0], k[1], k[2], m[k])
	}
	return b.String()
}

// renderNameDiff lists only the positions where the two servers differ.
func renderNameDiff(want, got map[[3]int]string) string {
	keys := map[[3]int]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	var ps [][3]int
	for k := range keys {
		ps = append(ps, k)
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i][0] != ps[j][0] {
			return ps[i][0] < ps[j][0]
		}
		return ps[i][2] < ps[j][2]
	})
	var b strings.Builder
	fmt.Fprintf(&b, "  %-18s %-22s %-22s\n", "position", "vanilla", "mc-go")
	for _, p := range ps {
		if want[p] == got[p] {
			continue
		}
		fmt.Fprintf(&b, "  (%4d,%4d,%4d) %-22s %-22s\n", p[0], p[1], p[2], want[p], got[p])
	}
	return b.String()
}

// vanillaUnstable lists the positions where the real server does not
// reproduce its own answer, keyed by scenario.
//
// Running the identical scenario twice against vanilla produces a
// different world at these positions. They are the last blocks of a
// falling pool's arms, and whether they are reached at all comes down to
// vanilla's randomized spread delay — a fluid that gains a level has its
// next tick multiplied by four three times in four — combined with the
// order the pool happens to fill in. Stepping two thousand ticks does
// not settle it, because it is not a settling problem.
//
// mc-go always commits to the settled answer. Where vanilla is of two
// minds, mc-go must produce one of the two.
var vanillaUnstable = map[string]map[[3]int][]int{
	// The last block of the falling pool's arms: present as a level 7
	// flow, or never reached at all.
	"B: falling column and its landing": {
		{24, -60, 32}: {7, -1},
		{32, -60, 24}: {7, -1},
	},
}

// diffLevels reports the positions where two observed worlds disagree,
// ignoring positions declared unstable when mc-go produces one of the
// answers vanilla is known to give.
func diffLevels(want, got map[[3]int]int, unstable map[[3]int][]int) map[[3]int]int {
	out := map[[3]int]int{}
	keys := map[[3]int]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	for k := range keys {
		w, okw := want[k]
		g, okg := got[k]
		if okw == okg && w == g {
			continue
		}
		if allowed, ok := unstable[k]; ok {
			// A level of -1 means "the fluid is not there".
			wv, gv := -1, -1
			if okw {
				wv = w
			}
			if okg {
				gv = g
			}
			if slices.Contains(allowed, wv) && slices.Contains(allowed, gv) {
				continue
			}
		}
		if okg {
			out[k] = g
		} else {
			out[k] = -1
		}
	}
	return out
}

// scenarioNames is the fixed comparison order.
func scenarioNames() []string {
	return []string{
		"A: water source on flat ground",
		"B: falling column and its landing",
		"C: lava source on flat ground",
	}
}

// fluidWorld is one scenario's observed positions and fluid levels.
type fluidWorld map[string]map[[3]int]int

// fluidRun is everything one server produced: the fluid levels observed
// for the flow scenarios, and the block names observed for the
// interaction scenario.
type fluidRun struct {
	levels fluidWorld
	names  map[string]map[[3]int]string
}

// runFluidScenarios joins a server, freezes it, runs every scenario with
// an exact tick budget, and reads each resulting world back.
func runFluidScenarios(t *testing.T, addr, name string) fluidRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	// The chat sink owns all reads from here; clearing the deadline
	// keeps a timeout from desynchronizing the stream mid-frame.
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCommand(t, pw, "tick freeze")
	waitDrained(t, pw, sink)

	out := fluidRun{levels: fluidWorld{}, names: map[string]map[[3]int]string{}}

	// A: a source resting on flat ground. It cannot fall, so its shape
	// is the pure horizontal spread.
	placeAndStep(t, pw, sink, "setblock 0 -60 0 minecraft:water[level=0]", settleTicks)
	out.levels["A: water source on flat ground"] = queryLevels(t, pw, sink, "water", crossPositions(0, -60, 0, 9))

	// B: a source held in the air, so the water falls and then fans out
	// where it lands. Placed far from A so the two cannot interact.
	placeAndStep(t, pw, sink, "setblock 32 -55 32 minecraft:water[level=0]", settleTicks)
	pos := crossPositions(32, -60, 32, 9)
	for y := -59; y <= -54; y++ {
		pos = append(pos, [3]int{32, y, 32})
	}
	pos = append(pos, [3]int{31, -59, 32}, [3]int{33, -59, 32}, [3]int{32, -59, 31}, [3]int{32, -59, 33})
	out.levels["B: falling column and its landing"] = queryLevels(t, pw, sink, "water", pos)

	// C: lava, which spends its level twice as fast as water and so
	// reaches three blocks instead of seven.
	placeAndStep(t, pw, sink, "setblock 64 -60 64 minecraft:lava[level=0]", settleTicks*2)
	out.levels["C: lava source on flat ground"] = queryLevels(t, pw, sink, "lava", crossPositions(64, -60, 64, 6))

	// D: water reaching lava. The lava is the block that gives way, and
	// what it becomes depends on whether it was a source or a flow, and
	// on where the water arrives from.
	lavaAt := [3]int{96, -60, 96}
	waterAt := [3]int{97, -60, 96}
	placeAndStep(t, pw, sink, "setblock 96 -60 96 minecraft:lava[level=0]", 40)
	placeAndStep(t, pw, sink, "setblock 97 -60 96 minecraft:water[level=0]", settleTicks)
	around := [][3]int{lavaAt, waterAt,
		{95, -60, 96}, {96, -60, 97}, {96, -60, 95}, {98, -60, 96}, {98, -60, 97}}
	out.names["D: water reaching lava"] = queryNames(t, pw, sink, fluidContactCandidates, around)

	// E: water falling onto a lava flow, which is where cobblestone
	// comes from. The lava is given its first spread first, and the
	// water lands on the flow one block from the source.
	genSrc := [3]int{128, -60, 128}
	genFlow := [3]int{129, -60, 128}
	genWater := [3]int{129, -59, 128}
	placeAndStep(t, pw, sink, "setblock 128 -60 128 minecraft:lava[level=0]", 40)
	placeAndStep(t, pw, sink, "setblock 129 -59 128 minecraft:water[level=0]", settleTicks)
	out.names["E: water falling onto lava"] = queryNames(t, pw, sink, fluidContactCandidates,
		[][3]int{genSrc, genFlow, genWater, {130, -60, 128}, {129, -58, 128}, {128, -59, 128}})

	return out
}

// fluidContactCandidates is the block set a lava/water meeting can
// produce, plus what the two fluids look like on their own.
var fluidContactCandidates = []string{
	"minecraft:lava", "minecraft:water", "minecraft:obsidian", "minecraft:cobblestone", "minecraft:air", "minecraft:stone",
}

// placeAndStep issues a placement and advances the frozen clock by
// exactly the requested number of ticks, waiting for the game clock to
// say the ticks really happened.
func placeAndStep(t *testing.T, pw *protocol.Writer, sink *chatSink, place string, ticks int) {
	t.Helper()
	sendCommand(t, pw, place)
	waitDrained(t, pw, sink)
	sprintTicks(t, pw, sink, ticks)
}

// crossPositions is the two axes plus both diagonals through a centre,
// which is enough to pin a fluid's shape without probing a whole slab.
func crossPositions(cx, y, cz, r int) [][3]int {
	var out [][3]int
	for d := -r; d <= r; d++ {
		out = append(out, [3]int{cx + d, y, cz})
		if d != 0 {
			out = append(out, [3]int{cx, y, cz + d})
		}
	}
	for _, d := range []int{-4, -1, 1, 4} {
		out = append(out, [3]int{cx + d, y, cz + d})
		out = append(out, [3]int{cx + d, y, cz - d})
	}
	return out
}

// queryLevels asks the server for the level of one fluid at every
// position.
//
// Every predicate is paired with its negation, so each command produces
// a reply whether or not it matched. That pairing is the whole point: a
// bare `if` says nothing when the condition fails, which makes "the
// fluid is not there" and "the server dropped my command" the same
// observation. With positive and negative replies counted per position,
// a short answer is detectable and the position is simply asked again,
// more slowly.
func queryLevels(t *testing.T, pw *protocol.Writer, sink *chatSink, fluid string, positions [][3]int) map[[3]int]int {
	t.Helper()
	type pending struct {
		pos   [3]int
		level int
		has   bool
		seen  int
	}
	todo := make([]*pending, 0, len(positions))
	for _, p := range positions {
		todo = append(todo, &pending{pos: p, level: -1})
	}
	out := map[[3]int]int{}

	for pass := 0; pass < 4 && len(todo) > 0; pass++ {
		// Later passes slow down: the first commands of a burst are what
		// vanilla's throttle swallows.
		pace := commandPace * time.Duration(pass+1)
		sink.take()
		const sentinel = "QUERYDONE"
		for _, it := range todo {
			tag := levelTag(it.pos)
			for lvl := 0; lvl <= 8; lvl++ {
				sendCommand(t, pw, fmt.Sprintf(
					"execute if block %d %d %d minecraft:%s[level=%d] run say %sL%d",
					it.pos[0], it.pos[1], it.pos[2], fluid, lvl, tag, lvl))
				time.Sleep(pace)
				sendCommand(t, pw, fmt.Sprintf(
					"execute unless block %d %d %d minecraft:%s[level=%d] run say %sN%d",
					it.pos[0], it.pos[1], it.pos[2], fluid, lvl, tag, lvl))
				time.Sleep(pace)
			}
		}
		sendCommand(t, pw, "say "+sentinel)
		lines := waitForMarker(t, sink, sentinel, 90*time.Second)

		var next []*pending
		for _, it := range todo {
			tag := levelTag(it.pos)
			for lvl := 0; lvl <= 8; lvl++ {
				neg := strconv.Itoa(lvl)
				if sawMarker(lines, tag+"L"+neg) {
					it.level, it.has, it.seen = lvl, true, it.seen+1
				} else if sawMarker(lines, tag+"N"+neg) {
					it.seen++
				}
			}
			switch {
			case it.seen == 9:
				if it.has {
					out[it.pos] = it.level
				}
			case pass == 3:
				t.Fatalf("position %v never got a full answer (%d/9 replies)", it.pos, it.seen)
			default:
				it.seen, it.has, it.level = 0, false, -1
				next = append(next, it)
			}
		}
		todo = next
	}
	return out
}

// queryNames asks which of a set of candidate blocks is at each
// position. Like queryLevels it pairs every predicate with its negation
// so each command answers, and re-asks any position it did not get a
// full set of answers for.
func queryNames(t *testing.T, pw *protocol.Writer, sink *chatSink, candidates []string, positions [][3]int) map[[3]int]string {
	t.Helper()
	type pending struct {
		pos  [3]int
		hits []string
		seen int
	}
	todo := make([]*pending, 0, len(positions))
	for _, p := range positions {
		todo = append(todo, &pending{pos: p})
	}
	out := map[[3]int]string{}

	for pass := 0; pass < 4 && len(todo) > 0; pass++ {
		pace := commandPace * time.Duration(pass+1)
		sink.take()
		const sentinel = "NAMEDONE"
		for _, it := range todo {
			tag := levelTag(it.pos)
			for i, name := range candidates {
				sendCommand(t, pw, fmt.Sprintf(
					"execute if block %d %d %d %s run say %sB%dY",
					it.pos[0], it.pos[1], it.pos[2], name, tag, i))
				time.Sleep(pace)
				sendCommand(t, pw, fmt.Sprintf(
					"execute unless block %d %d %d %s run say %sB%dN",
					it.pos[0], it.pos[1], it.pos[2], name, tag, i))
				time.Sleep(pace)
			}
		}
		sendCommand(t, pw, "say "+sentinel)
		lines := waitForMarker(t, sink, sentinel, 90*time.Second)

		var next []*pending
		for _, it := range todo {
			tag := levelTag(it.pos)
			it.hits = it.hits[:0]
			it.seen = 0
			for i, name := range candidates {
				switch {
				case sawMarker(lines, fmt.Sprintf("%sB%dY", tag, i)):
					it.hits = append(it.hits, name)
					it.seen++
				case sawMarker(lines, fmt.Sprintf("%sB%dN", tag, i)):
					it.seen++
				}
			}
			switch {
			case it.seen == len(candidates):
				if len(it.hits) == 0 {
					// Nothing in the candidate set is there; report it
					// rather than silently dropping the position.
					out[it.pos] = "?"
				} else {
					out[it.pos] = strings.Join(it.hits, "+")
				}
			case pass == 3:
				t.Fatalf("position %v never got a full answer (%d/%d replies)", it.pos, it.seen, len(candidates))
			default:
				next = append(next, it)
			}
		}
		todo = next
	}
	return out
}

// settleTicks is the tick budget each scenario is stepped by. It is
// comfortably past the point where the fluid stops changing: vanilla
// multiplies a spreading fluid's delay by four at random, so the shape
// converges long before the furthest block arrives. Comparing mid-flow
// would compare nothing but the two servers' luck.
const settleTicks = 400

// commandPace is the gap between probe commands. Vanilla's command
// throttle drops packets above roughly one per tick, so the probe paces
// itself and retries anything it did not get a full answer for.
const commandPace = 55 * time.Millisecond

// levelTag is the per-position marker prefix the queries carry back.
func levelTag(p [3]int) string {
	return fmt.Sprintf("Q%d_%d_%d_", p[0], p[1], p[2])
}

// waitForMarker blocks until the sentinel appears in chat.
func waitForMarker(t *testing.T, sink *chatSink, sentinel string, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var all []string
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		all = append(all, sink.take()...)
		if sawMarker(all, sentinel) {
			return all
		}
	}
	t.Fatalf("sentinel %s never arrived; sink health: %s", sentinel, sink.health())
	return all
}

func sameLevels(a, b map[[3]int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// renderLevels draws an observed world grouped by y, for failure output.
func renderLevels(m map[[3]int]int) string {
	var b strings.Builder
	byY := map[int][][3]int{}
	for p := range m {
		byY[p[1]] = append(byY[p[1]], p)
	}
	ys := make([]int, 0, len(byY))
	for y := range byY {
		ys = append(ys, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ys)))
	for _, y := range ys {
		ps := byY[y]
		sort.Slice(ps, func(i, j int) bool {
			if ps[i][0] != ps[j][0] {
				return ps[i][0] < ps[j][0]
			}
			return ps[i][2] < ps[j][2]
		})
		fmt.Fprintf(&b, "  y=%d:", y)
		for _, p := range ps {
			fmt.Fprintf(&b, " (%d,%d)=%d", p[0], p[2], m[p])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderDiff lists only the positions where the two servers differ.
func renderDiff(want, got map[[3]int]int) string {
	var b strings.Builder
	keys := map[[3]int]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	var ps [][3]int
	for k := range keys {
		ps = append(ps, k)
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i][1] != ps[j][1] {
			return ps[i][1] > ps[j][1]
		}
		if ps[i][0] != ps[j][0] {
			return ps[i][0] < ps[j][0]
		}
		return ps[i][2] < ps[j][2]
	})
	fmt.Fprintf(&b, "  %-16s %-8s %-8s\n", "position", "vanilla", "mc-go")
	for _, p := range ps {
		w, okw := want[p]
		g, okg := got[p]
		if okw == okg && w == g {
			continue
		}
		fmt.Fprintf(&b, "  (%4d,%4d,%4d) %-8s %-8s\n", p[0], p[1], p[2], levelName(w, okw), levelName(g, okg))
	}
	return b.String()
}

func levelName(lvl int, ok bool) string {
	if !ok {
		return "-"
	}
	return strconv.Itoa(lvl)
}
