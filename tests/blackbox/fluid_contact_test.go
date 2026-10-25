package blackbox

import (
	"testing"
	"time"
)

// TestFluidContactParity walks every lava/water meeting geometry that
// fits near spawn, runs each one on the real server and on mc-go, and
// compares both the state before the water arrives and the state after
// the two fluids have settled.
//
// The geometries are chosen so that between them they cover every side
// water can reach lava from - beside a source, beside a flow, above
// either, under either, and from a distance - and the state before the
// water arrives is compared too, so a difference in how far the lava
// spread is not mistaken for a difference in what the two fluids make
// when they meet.
func TestFluidContactParity(t *testing.T) {
	const probe = "FluidContact"
	tg := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor(probe),
	}, nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runContactScenarios(t, tg.vanilla, probe)
	got := runContactScenarios(t, tg.mcgo, probe)

	for _, sc := range contactScenarios {
		w := want[sc.name]
		g := got[sc.name]
		if !sameNames(w.before, g.before) {
			t.Errorf("%s: the lava alone differs\n%s", sc.name, renderNameDiff(w.before, g.before))
		}
		if !sameNames(w.after, g.after) {
			t.Errorf("%s: the settled world differs\n%s", sc.name, renderNameDiff(w.after, g.after))
		}
	}
}

// contactScenario is one meeting of the two fluids. The lava is placed
// first and given 40 ticks to spread on its own, which is what turns a
// source into a source with flows around it; then the water is placed
// and the world is given the full settle budget.
type contactScenario struct {
	name    string
	lava    string
	water   string
	queries [][3]int
}

// contactScenarios are laid out far enough apart that no two can reach
// each other, and inside the eight chunks the server keeps loaded, which
// is why the far corner is reused at y=-60 for two of them.
var contactScenarios = []contactScenario{
	{
		name:    "water two blocks from a lava source",
		lava:    "setblock 0 -60 0 minecraft:lava[level=0]",
		water:   "setblock 2 -60 0 minecraft:water[level=0]",
		queries: [][3]int{{0, -60, 0}, {1, -60, 0}, {2, -60, 0}, {3, -60, 0}, {0, -60, -1}, {0, -60, 1}},
	},
	{
		name:    "water beside a lava source",
		lava:    "setblock 32 -60 32 minecraft:lava[level=0]",
		water:   "setblock 33 -60 32 minecraft:water[level=0]",
		queries: [][3]int{{32, -60, 32}, {33, -60, 32}, {32, -60, 31}, {32, -60, 33}, {34, -60, 32}},
	},
	{
		name:    "water above a lava source",
		lava:    "setblock 64 -60 64 minecraft:lava[level=0]",
		water:   "setblock 64 -59 64 minecraft:water[level=0]",
		queries: [][3]int{{64, -60, 64}, {64, -59, 64}, {63, -60, 64}, {65, -60, 64}, {64, -60, 63}},
	},
	{
		name:    "water three blocks from a lava source",
		lava:    "setblock 96 -60 96 minecraft:lava[level=0]",
		water:   "setblock 99 -60 96 minecraft:water[level=0]",
		queries: [][3]int{{96, -60, 96}, {97, -60, 96}, {98, -60, 96}, {99, -60, 96}, {96, -60, 95}, {96, -60, 97}},
	},
	{
		name:    "water above a lava flow",
		lava:    "setblock 128 -60 128 minecraft:lava[level=0]",
		water:   "setblock 129 -59 128 minecraft:water[level=0]",
		queries: [][3]int{{128, -60, 128}, {129, -60, 128}, {129, -59, 128}, {130, -60, 128}, {128, -59, 128}},
	},
	{
		name:    "water under a lava flow",
		lava:    "setblock 0 -60 128 minecraft:lava[level=0]",
		water:   "setblock 1 -61 128 minecraft:water[level=0]",
		queries: [][3]int{{0, -60, 128}, {1, -60, 128}, {1, -61, 128}, {2, -60, 128}, {3, -60, 128}, {0, -60, 127}},
	},
	{
		name:    "water under a lava source",
		lava:    "setblock 128 -60 0 minecraft:lava[level=0]",
		water:   "setblock 128 -61 0 minecraft:water[level=0]",
		queries: [][3]int{{128, -60, 0}, {128, -61, 0}, {129, -60, 0}, {127, -60, 0}, {128, -60, 1}},
	},
	{
		name:    "lava source directly over water",
		lava:    "setblock 32 -59 128 minecraft:lava[level=0]",
		water:   "setblock 32 -60 128 minecraft:water[level=0]",
		queries: [][3]int{{32, -60, 128}, {32, -59, 128}, {33, -60, 128}, {31, -60, 128}, {32, -60, 129}},
	},
	{
		name:    "lava source falling beside water",
		lava:    "setblock 64 -59 128 minecraft:lava[level=0]",
		water:   "setblock 63 -60 128 minecraft:water[level=0]",
		queries: [][3]int{{63, -60, 128}, {64, -60, 128}, {64, -59, 128}, {63, -59, 128}, {65, -60, 128}},
	},
}

// contactRun is one scenario's world before and after the water lands.
type contactRun struct {
	before map[[3]int]string
	after  map[[3]int]string
}

// runContactScenarios replays every meeting on one server.
func runContactScenarios(t *testing.T, addr, name string) map[string]contactRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCommand(t, pw, "tick freeze")
	waitDrained(t, pw, sink)

	out := map[string]contactRun{}
	for _, sc := range contactScenarios {
		placeAndStep(t, pw, sink, sc.lava, 40)
		before := queryNames(t, pw, sink, fluidContactCandidates, sc.queries)
		placeAndStep(t, pw, sink, sc.water, settleTicks)
		out[sc.name] = contactRun{
			before: before,
			after:  queryNames(t, pw, sink, fluidContactCandidates, sc.queries),
		}
	}
	return out
}
