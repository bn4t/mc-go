package blackbox

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

// hopperTicks is how long both servers are given to move their items.
// It is well past the slowest arrangement: eight items at one per
// transfer, and a transfer every eight ticks, is 64 ticks of work.
const hopperTicks = 240

// hopperFixtures is what the world holds before either server runs, one
// arrangement per line. Each is chosen to settle: once the last item has
// moved, nothing further happens however long the server runs on, so the
// comparison is over the state reached and not over the exact tick a
// save landed on.
//
// Every facing a hopper can have in 26.2 appears, together with every
// container it works with. "Up" is not among them: the real server
// refuses hopper[facing=up] outright ("does not accept 'up' for facing
// property"), so a hopper only ever faces sideways or down.
var hopperFixtures = []string{
	// A: facing east, with a chest to the east and another below. The
	// eastern one is what fills - a hopper pushes toward its face.
	"setblock 2 -60 2 minecraft:hopper[facing=east]",
	"setblock 3 -60 2 minecraft:chest",
	"setblock 2 -61 2 minecraft:chest",
	"item replace block 2 -60 2 container.0 with minecraft:apple 1",
	// B: a barrel above and a barrel below. The hopper drains the one
	// above into the one below, one item per transfer, so the transfer
	// rate shows up in how the eight apples are spread.
	"setblock 5 -60 5 minecraft:hopper[facing=down]",
	"setblock 5 -59 5 minecraft:barrel",
	"setblock 5 -61 5 minecraft:barrel",
	"item replace block 5 -59 5 container.0 with minecraft:apple 8",
	// C: facing north, to pin the second horizontal facing.
	"setblock 7 -60 7 minecraft:hopper[facing=north]",
	"setblock 7 -60 6 minecraft:chest",
	"item replace block 7 -60 7 container.0 with minecraft:apple 1",
	// D: pushing down into a furnace, which fills its input slot with
	// whatever it is given - coal included, which is not smeltable.
	"setblock 9 -60 9 minecraft:hopper[facing=down]",
	"setblock 9 -61 9 minecraft:furnace[facing=north]",
	"item replace block 9 -60 9 container.0 with minecraft:coal 1",
	// E: pushing sideways into a furnace, which fills its fuel slot and
	// nothing else.
	"setblock 11 -60 11 minecraft:hopper[facing=east]",
	"setblock 12 -60 11 minecraft:furnace[facing=north]",
	"item replace block 11 -60 11 container.0 with minecraft:coal 1",
	// F: a hopper under a furnace drains the furnace's result and
	// touches neither the input nor the fuel.
	"setblock 13 -60 13 minecraft:hopper[facing=down]",
	"setblock 13 -59 13 minecraft:furnace[facing=north]",
	"item replace block 13 -59 13 container.0 with minecraft:raw_iron 2",
	"item replace block 13 -59 13 container.2 with minecraft:iron_ingot 3",
	// G: a hopper with nothing to move. It keeps its item, and it keeps
	// no cooldown either - an idle hopper saves as TransferCooldown 0
	// rather than as a number counting down from eight.
	"setblock 15 -60 15 minecraft:hopper[facing=down]",
	"item replace block 15 -60 15 container.0 with minecraft:apple 1",
}

// hopperFixtureItems is how many items the world holds before either
// server runs: the sum of the item replace commands above. A command the
// real server refused would leave a world with fewer, and the comparison
// would quietly be over a smaller world than this test describes.
const hopperFixtureItems = 1 + 8 + 1 + 1 + 1 + 2 + 3 + 1

// TestHopperTransferParity drives both servers through the same hopper
// arrangements and requires the two resulting worlds to be identical,
// block entity for block entity.
//
// The world is built once, on the real server, and a copy is handed to
// each of them: what is under test is how a hopper moves items, not how
// either server's commands build one.
func TestHopperTransferParity(t *testing.T) {
	jar := blackboxJar(t)

	built := buildHopperWorld(t, jar)

	// ---- the real server, from the same starting world ----
	vDir := t.TempDir()
	vWorld := filepath.Join(vDir, "world")
	copyTree(t, built, vWorld)
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort, map[string]string{"level-name": "world"}, nil, vDir)
	say("forceload add 0 0")
	say("tick freeze")
	say(fmt.Sprintf("tick sprint %d", hopperTicks))
	waitVanillaTicks(t, say, vDir, hopperTicks)
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited with code %d, log tail:\n%s", code, vanillaLogTailText(vDir))
	}
	want := chunkBlockEntitiesByPos(t, vWorld)
	checkHoppersMoved(t, want, "the real server")
	t.Logf("the real server after %d ticks: %s", hopperTicks, renderBlockEntities(want))

	// ---- mc-go, from an identical copy ----
	mDir := t.TempDir()
	mWorld := filepath.Join(mDir, "world")
	copyTree(t, built, mWorld)
	got := runHopperMcGo(t, mWorld)
	compareBlockEntities(t, got, want, "mc-go's hoppers")
}

// buildHopperWorld builds the fixture world on the real server and
// returns the world directory, checking on the way that every command
// took: a fixture the real server refused would leave this test
// comparing a world with one arrangement silently missing.
func buildHopperWorld(t *testing.T, jar string) string {
	t.Helper()
	dir := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, port, map[string]string{"level-name": "world"}, nil, dir)
	// The chunks have to be loaded for the commands to reach them: 26.2
	// keeps no spawn chunks, and a console command has no player whose
	// view would load them.
	say("forceload add 0 0")
	say("gamerule minecraft:advance_time false")
	// The clock stops before the fixtures exist, so the world is handed
	// over exactly as built rather than after a few seconds of hoppers
	// already working.
	say("tick freeze")
	for _, c := range hopperFixtures {
		say(c)
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("the real server exited with code %d while building the fixtures, log tail:\n%s",
			code, vanillaLogTailText(dir))
	}
	worldDir := filepath.Join(dir, "world")
	be := chunkBlockEntitiesByPos(t, worldDir)
	if n := countItems(be); n != hopperFixtureItems {
		t.Fatalf("the fixture world holds %d items, want %d: %s", n, hopperFixtureItems, renderBlockEntities(be))
	}
	// Before either server runs, every hopper holds what it was given
	// and every container it faces is empty: a fixture command the real
	// server took but placed elsewhere would leave a hopper with nothing
	// to move, and the run below would then be a comparison of two
	// worlds where nothing happened.
	for _, h := range hopperSources {
		slot, ok := singleItem(be[h.pos])
		if !ok || slot.Name != h.item || slot.Count != h.count {
			t.Errorf("the fixture world's hopper at %v holds %s, want %d %s (%s)",
				h.pos, renderNBT(be[h.pos], true), h.count, h.item, h.comment)
		}
	}
	for _, pos := range hopperDestinations {
		if list, ok := be[pos]["Items"].(nbt.List); ok && len(list.Items) != 0 {
			t.Errorf("the fixture world's %v already holds %s, want it empty to start",
				pos, renderNBT(be[pos], true))
		}
	}
	// Every fixture block is a block entity, and a hopper that never got
	// its item would still be one - so the item count above is the check
	// that matters, and this one names the positions.
	for _, pos := range [][3]int{
		{2, -60, 2}, {3, -60, 2}, {2, -61, 2},
		{5, -60, 5}, {5, -59, 5}, {5, -61, 5},
		{7, -60, 7}, {7, -60, 6},
		{9, -60, 9}, {9, -61, 9},
		{11, -60, 11}, {12, -60, 11},
		{13, -60, 13}, {13, -59, 13},
		{15, -60, 15},
	} {
		if _, ok := be[pos]; !ok {
			t.Errorf("the fixture world has no block entity at %v: %s", pos, renderBlockEntities(be))
		}
	}
	return worldDir
}

// runHopperMcGo loads the given world into mc-go, advances it by exactly
// hopperTicks simulated ticks, saves it, and returns the block entities
// of the chunk under test.
func runHopperMcGo(t *testing.T, worldDir string) map[[3]int]map[string]any {
	t.Helper()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		s.WorldDir = worldDir
		s.WorldPath = ""
		s.LevelName = "world"
	})
	// A player is what loads the chunk: without one mc-go never reads
	// the fixtures, and its hoppers never tick.
	c, pr, pw := joinPlayBlackbox(t, addr, "Hopper")
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()
	sendCommand(t, pw, "tick freeze")
	waitDrained(t, pw, sink)
	sprintTicks(t, pw, sink, hopperTicks)
	sendCommand(t, pw, "save-all")
	waitDrained(t, pw, sink)
	c.Close()
	return chunkBlockEntitiesByPos(t, worldDir)
}

// hopperSources is what each fixture hopper starts holding. Every one
// of them has somewhere to put it, except the last, which is what pins
// the idle case.
var hopperSources = []struct {
	pos     [3]int
	item    string
	count   int32
	comment string
}{
	{[3]int{2, -60, 2}, "minecraft:apple", 1, "the east-facing hopper"},
	{[3]int{7, -60, 7}, "minecraft:apple", 1, "the north-facing hopper"},
	{[3]int{9, -60, 9}, "minecraft:coal", 1, "the hopper over a furnace"},
	{[3]int{11, -60, 11}, "minecraft:coal", 1, "the hopper beside a furnace"},
	{[3]int{15, -60, 15}, "minecraft:apple", 1, "the hopper with nothing to move"},
}

// hopperDestinations is every container a fixture hopper can reach,
// empty before either server runs.
var hopperDestinations = [][3]int{
	{3, -60, 2}, {2, -61, 2}, {5, -61, 5}, {7, -60, 6}, {9, -61, 9}, {12, -60, 11},
}

// checkHoppersMoved requires the real server's items to have ended up
// where its own rules say, so that the comparison against this world is
// a comparison against hoppers that worked rather than against two
// worlds where nothing happened.
func checkHoppersMoved(t *testing.T, be map[[3]int]map[string]any, who string) {
	t.Helper()
	// Where the items are once everything has settled.
	want := []struct {
		pos     [3]int
		item    string
		count   int32
		comment string
	}{
		{[3]int{3, -60, 2}, "minecraft:apple", 1, "the chest east of the east-facing hopper"},
		{[3]int{5, -61, 5}, "minecraft:apple", 8, "the barrel below the down-facing hopper"},
		{[3]int{7, -60, 6}, "minecraft:apple", 1, "the chest north of the north-facing hopper"},
		{[3]int{9, -61, 9}, "minecraft:coal", 1, "the input slot of the furnace under a hopper"},
		{[3]int{12, -60, 11}, "minecraft:coal", 1, "the fuel slot of the furnace beside a hopper"},
		{[3]int{13, -60, 13}, "minecraft:iron_ingot", 3, "the hopper under a furnace"},
		{[3]int{13, -59, 13}, "minecraft:raw_iron", 2, "the furnace the hopper drains nothing but the result of"},
		{[3]int{15, -60, 15}, "minecraft:apple", 1, "the hopper with nothing to move"},
	}
	for _, w := range want {
		entry, ok := be[w.pos]
		if !ok {
			t.Errorf("%s has no block entity at %v: %s", who, w.pos, renderBlockEntities(be))
			continue
		}
		slot, ok := singleItem(entry)
		if !ok {
			t.Errorf("%s at %v holds %s, want one stack of %d %s (%s)",
				who, w.pos, renderNBT(entry, true), w.count, w.item, w.comment)
			continue
		}
		if slot.Name != w.item || slot.Count != w.count {
			t.Errorf("%s at %v holds %s x%d, want %s x%d (%s)",
				who, w.pos, slot.Name, slot.Count, w.item, w.count, w.comment)
		}
	}
	// What the transfers emptied: the hoppers that had something to send
	// on, the barrel they drained, and the chest a sideways hopper never
	// fills.
	for _, pos := range [][3]int{
		{2, -60, 2}, {5, -60, 5}, {7, -60, 7}, {9, -60, 9}, {11, -60, 11},
		{5, -59, 5}, {2, -61, 2},
	} {
		entry, ok := be[pos]
		if !ok {
			t.Errorf("%s has no block entity at %v: %s", who, pos, renderBlockEntities(be))
			continue
		}
		if list, ok := entry["Items"].(nbt.List); ok && len(list.Items) != 0 {
			t.Errorf("%s at %v holds %s, want it emptied by now", who, pos, renderNBT(entry, true))
		}
	}
}

// singleItem reads the one stack a container holds, or reports that it
// holds none or several.
func singleItem(entry map[string]any) (itemNBTStack, bool) {
	list, ok := entry["Items"].(nbt.List)
	if !ok || len(list.Items) != 1 {
		return itemNBTStack{}, false
	}
	item, ok := list.Items[0].(map[string]any)
	if !ok {
		return itemNBTStack{}, false
	}
	name, _ := item["id"].(string)
	count, _ := item["count"].(int32)
	return itemNBTStack{Name: name, Count: count}, true
}

// itemNBTStack is one item read back out of a saved block entity, named
// as the file names it so it can be compared with the fixture's own
// command line.
type itemNBTStack struct {
	Name  string
	Count int32
}

// countItems totals the items a chunk's block entities hold.
func countItems(be map[[3]int]map[string]any) int64 {
	var total int64
	for _, entry := range be {
		list, ok := entry["Items"].(nbt.List)
		if !ok {
			continue
		}
		for _, anyItem := range list.Items {
			item, ok := anyItem.(map[string]any)
			if !ok {
				continue
			}
			if n, ok := item["count"].(int32); ok {
				total += int64(n)
			}
		}
	}
	return total
}

// waitVanillaTicks waits until the real server's own clock says n ticks
// have been simulated, which is what makes a comparison against it a
// comparison over the same amount of game time.
func waitVanillaTicks(t *testing.T, say func(string), dir string, n int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		say("time query gametime")
		time.Sleep(300 * time.Millisecond)
		if ticks, ok := lastGameTime(vanillaLogTailText(dir)); ok && ticks >= int64(n) {
			return
		}
	}
	t.Fatalf("the real server never reached %d game ticks; log tail:\n%s", n, vanillaLogTailText(dir))
}

// lastGameTime reads the newest game time the real server reported.
func lastGameTime(log string) (int64, bool) {
	var out int64
	found := false
	for _, line := range strings.Split(log, "\n") {
		i := strings.Index(line, "The game time is ")
		if i < 0 {
			continue
		}
		var v int64
		if _, err := fmt.Sscanf(line[i:], "The game time is %d", &v); err != nil {
			continue
		}
		out, found = v, true
	}
	return out, found
}
