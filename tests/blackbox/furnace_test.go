package blackbox

// Furnace-family recipe parity: what each of the three blocks will and
// will not cook, and how long it takes.
//
// The three blocks share one block entity and one smelting tick and
// differ only in which recipe type they cook with - smelting for the
// furnace, blasting for the blast furnace, smoking for the smoker - so
// the interesting behaviour is the edges of those three sets. They are
// real, disjoint, and asymmetric in both directions:
//
//   - a blast furnace takes ores and metal gear but no food, so it
//     smelts an iron pickaxe into nuggets and refuses beef;
//   - a smoker takes food and nothing else, so it cooks beef and refuses
//     both sand and raw iron, which are perfectly good smelting recipes;
//   - a plain furnace takes everything all three sets list.
//
// A hand-written table of thirteen inputs cannot tell those apart: it
// accepts the same thirteen in all three blocks, which is wrong in both
// directions at once - it cooks beef in a blast furnace and refuses an
// iron pickaxe everywhere. The fixture below is built to fail on exactly
// that, and the cook times (200 ticks for smelting, 100 for the other
// two) show up as different item counts after the same number of ticks,
// so the two are measured together in one run.
//
// The world is built once, on the real server, and a copy is handed to
// each of them, so what is under test is how the blocks cook and not how
// either server's commands build a world.
import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
)

// furnaceTicks is how long both servers are given to cook, as the
// argument to /tick sprint. The sprint is what both are given and each
// simulates one tick more than it asks for (see TestTickCountParity), so
// 450 is 451 simulated ticks: two smelts at 200 and four at 100, plus
// fifty-one ticks over in both. That is chosen so the two cook times
// cannot be confused - a server that used the wrong one lands on a
// different item count *and* on a different cook counter - and the fifty
// is the point of the number: it leaves every block that cooked at all
// still mid-cook, which is what pins the counter and not just the
// output. The counters are compared against a world the real server
// produced, so what the count is matters less than that both servers
// were given the same command and took the same ticks over it.
const furnaceTicks = 450

// furnaceInputs is how many of a stackable input every block is given:
// more than either cook time can finish in the run above, so a block
// that cooks is left with some of its input as well as with a part of a
// cook on the counter. An input that does not stack - a tool - is given
// one instead, because that is all the real server will put in a slot:
// "/item replace block ... with minecraft:iron_pickaxe 4" is refused
// with "minecraft:iron_pickaxe can only stack up to 1".
const furnaceInputs = 4

// A fixture is one furnace-family block and what it is given: some of
// one input and one coal. Coal is the fuel throughout - 1600 ticks in a
// furnace and 800 in the other two, which is long enough that none of
// them runs out before the run ends, so the fuel that is left is a
// second reading of the burn rate alongside lit_total_time.
type furnaceFixture struct {
	pos   [3]int
	block string
	input string
	// given is how many of the input the block starts with, and count
	// how many the real server leaves in the result slot. An empty
	// output is a block that refused its input, and is what the reject
	// fixtures are for.
	given  int32
	output string
	count  int32
	// comment says what the fixture is there to pin.
	comment string
}

var furnaceFixtures = []furnaceFixture{
	// The plain furnace cooks all three sets, with the inputs chosen so
	// that one comes from each: a tag-member block, a tool, and food.
	{[3]int{2, -60, 2}, "furnace", "sand", 4, "glass", 2,
		"a smelting-only input (sand is in #smelts_to_glass, a tag)"},
	{[3]int{6, -60, 2}, "furnace", "iron_pickaxe", 1, "iron_nugget", 1,
		"a tool, which blasting also lists"},
	{[3]int{10, -60, 2}, "furnace", "beef", 4, "cooked_beef", 2,
		"food, which smoking also lists"},
	// The blast furnace takes ores and metal gear, at half the time.
	{[3]int{2, -60, 6}, "blast_furnace", "raw_iron", 4, "iron_ingot", 4,
		"an ore, cooked in 100 ticks rather than 200"},
	{[3]int{6, -60, 6}, "blast_furnace", "iron_pickaxe", 1, "iron_nugget", 1,
		"a tool, which blasting lists and smoking does not"},
	{[3]int{10, -60, 6}, "blast_furnace", "beef", 4, "", 0,
		"food, which blasting does not list"},
	// The smoker takes food and nothing else.
	{[3]int{2, -60, 10}, "smoker", "beef", 4, "cooked_beef", 4,
		"food, cooked in 100 ticks rather than 200"},
	{[3]int{6, -60, 10}, "smoker", "sand", 4, "", 0,
		"a smelting-only input the smoker does not list"},
	{[3]int{10, -60, 10}, "smoker", "raw_iron", 4, "", 0,
		"an ore the smoker does not list"},
}

// furnaceFixtureItems is how many items the fixture world holds before
// either server runs: every input and one coal per block. A command the
// real server refused would leave a world with fewer, and the comparison
// would quietly be over fewer arrangements than this test describes.
func furnaceFixtureItems() int {
	n := len(furnaceFixtures) // one coal each
	for _, f := range furnaceFixtures {
		n += int(f.given)
	}
	return n
}

// TestFurnaceRecipeParity drives both servers through the same
// furnace-family arrangements and requires the two resulting worlds to
// be identical, block entity for block entity.
func TestFurnaceRecipeParity(t *testing.T) {
	jar := blackboxJar(t)
	built := buildFurnaceWorld(t, jar)

	// ---- the real server, from the same starting world ----
	vDir := t.TempDir()
	vWorld := filepath.Join(vDir, "world")
	copyTree(t, built, vWorld)
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort, map[string]string{"level-name": "world"}, nil, vDir)
	// Frozen *before* the chunk is loaded, and the world carries no
	// forceload ticket for the chunk (the fixture was saved without
	// one), so nothing at all has ticked when the sprint starts. A
	// furnace's saved cook and burn counters are the number of ticks
	// since the world was loaded, so a single tick of slack at startup
	// would show up as a wrong count in every one of the fixtures.
	say("tick freeze")
	say("forceload add 0 0")
	sprintVanilla(t, say, vDir, furnaceTicks)
	// The world is read after the flush and *before* the stop, because
	// the shutdown save that follows a flush can write the older of the
	// two copies of a chunk back out (see buildFurnaceWorld). Reading
	// first makes the comparison the flushed world whichever the
	// shutdown writes over it.
	say("save-all flush")
	want := chunkBlockEntitiesByPos(t, vWorld)
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited with code %d, log tail:\n%s", code, vanillaLogTailText(vDir))
	}
	checkFurnacesRan(t, want, "the real server")
	t.Logf("the real server after a sprint of %d: %s", furnaceTicks, renderBlockEntities(want))

	// ---- mc-go, from an identical copy ----
	mDir := t.TempDir()
	mWorld := filepath.Join(mDir, "world")
	copyTree(t, built, mWorld)
	got := runFurnaceMcGo(t, mWorld)
	compareBlockEntities(t, got, want, "mc-go's furnaces")
}

// buildFurnaceWorld builds the fixture world on the real server and
// returns the world directory, checking on the way that every command
// took: a fixture the real server refused would leave this test
// comparing a world with one arrangement silently missing.
func buildFurnaceWorld(t *testing.T, jar string) string {
	t.Helper()
	dir := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr, say, _ := vanillaConsole(t, jar, port, map[string]string{"level-name": "world"}, nil, dir)
	// The chunks have to be loaded for the commands to reach them: 26.2
	// keeps no spawn chunks. A player loads them, and a player's ticket
	// is transient - unlike `/forceload`, which is saved into the
	// dimension's chunk_tickets.dat, so a world handed over with one
	// has its chunk loaded and ticking from the moment the next server
	// boots, however quickly that server freezes. Measured: five ticks
	// of it, in every fixture, which is why the fixture world is loaded
	// here by a client rather than by a command.
	c, _, _ := joinPlayBlackbox(t, addr, "FurnaceBuilder")
	defer c.Close()
	say("gamerule minecraft:advance_time false")
	// The clock stops before the fixtures exist, so the world is handed
	// over exactly as built rather than after a few seconds of furnaces
	// already cooking.
	say("tick freeze")
	// No `/forceload` anywhere in this builder: the player's own
	// connection is what keeps the chunk loaded while the commands run,
	// and a ticket would be written into the world and loaded again by
	// whoever reads it next. The runner needs the chunk loaded and
	// nothing else - a ticket in the file makes the chunk tick from the
	// moment that server boots, which is however many ticks before its
	// own freeze lands. Measured: seven ticks of cooking in every
	// fixture, on a world whose only difference was the ticket.
	for _, f := range furnaceFixtures {
		say(fmt.Sprintf("setblock %d %d %d minecraft:%s[facing=north]",
			f.pos[0], f.pos[1], f.pos[2], f.block))
		say(fmt.Sprintf("item replace block %d %d %d container.0 with minecraft:%s %d",
			f.pos[0], f.pos[1], f.pos[2], f.input, f.given))
		say(fmt.Sprintf("item replace block %d %d %d container.1 with minecraft:coal 1",
			f.pos[0], f.pos[1], f.pos[2]))
	}
	// The world is flushed and the server is then left running for the
	// test's own cleanup to kill, rather than stopped. Measured: a
	// `/stop` after the flush rewrites the chunk on the way out, and
	// that second write is the *older* of the two about half the time -
	// the last fixture's coal is in the block entity the server reports
	// through `/data get block` and absent from the chunk on disk. It is
	// always the last-written chunk, which is why it is worth a comment
	// rather than a shrug: a fixture that is silently one item short
	// would be compared as though it had been built.
	say("save-all flush")
	worldDir := filepath.Join(dir, "world")
	be := chunkBlockEntitiesByPos(t, worldDir)
	if n, want := countItems(be), int64(furnaceFixtureItems()); n != want {
		t.Fatalf("the fixture world holds %d items, want %d: %s",
			n, want, renderBlockEntities(be))
	}
	// Every input and its coal, at rest and unburnt: a fixture command
	// the real server took but placed elsewhere would leave a block with
	// nothing to cook, and the run below would then be a comparison of
	// two worlds where nothing happened.
	for _, f := range furnaceFixtures {
		entry, ok := be[f.pos]
		if !ok {
			t.Errorf("the fixture world has no block entity at %v (%s)", f.pos, f.comment)
			continue
		}
		slots := furnaceStacks(entry)
		if s, ok := slots[0]; !ok || s.Name != "minecraft:"+f.input || s.Count != f.given {
			t.Errorf("the fixture world's %s at %v holds %s, want %d %s (%s)",
				f.block, f.pos, renderNBT(entry, true), f.given, f.input, f.comment)
		}
		if s, ok := slots[1]; !ok || s.Name != "minecraft:coal" || s.Count != 1 {
			t.Errorf("the fixture world's %s at %v holds %s, want one coal (%s)",
				f.block, f.pos, renderNBT(entry, true), f.comment)
		}
		if s, ok := slots[2]; ok {
			t.Errorf("the fixture world's %s at %v already holds %s x%d in its result slot, want it empty",
				f.block, f.pos, s.Name, s.Count)
		}
		if burn := furnaceShort(t, entry, "lit_time_remaining", f.pos); burn != 0 {
			t.Errorf("the fixture world's %s at %v reads lit_time_remaining %d, want it unburnt",
				f.block, f.pos, burn)
		}
		if lit := furnaceShort(t, entry, "lit_total_time", f.pos); lit != 0 {
			t.Errorf("the fixture world's %s at %v reads lit_total_time %d, want it never lit",
				f.block, f.pos, lit)
		}
		if cook := furnaceShort(t, entry, "cooking_time_spent", f.pos); cook != 0 {
			t.Errorf("the fixture world's %s at %v reads cooking_time_spent %d, want it never ticked",
				f.block, f.pos, cook)
		}
	}
	return worldDir
}

// runFurnaceMcGo loads the given world into mc-go, advances it by
// exactly furnaceTicks simulated ticks, saves it, and returns the block
// entities of the chunk under test.
func runFurnaceMcGo(t *testing.T, worldDir string) map[[3]int]map[string]any {
	t.Helper()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		s.WorldDir = worldDir
		s.WorldPath = ""
		s.LevelName = "world"
		// The world is handed over before it has ticked, which is what
		// the real server is given above by freezing before the chunk
		// is loaded. Without it the player that loads the chunk ticks
		// the world for the seconds it takes the freeze command to
		// land, and every furnace in the fixture is that many ticks
		// further along than the world it is compared with.
		s.StartFrozen = true
	})
	// A player is what loads the chunk: without one mc-go never reads
	// the fixtures, and its furnaces never tick.
	c, pr, pw := joinPlayBlackbox(t, addr, "Furnace")
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()
	sprintTicks(t, pw, sink, furnaceTicks)
	sendCommand(t, pw, "save-all")
	waitDrained(t, pw, sink)
	c.Close()
	return chunkBlockEntitiesByPos(t, worldDir)
}

// checkFurnacesRan requires the real server's own blocks to have cooked
// what its rules say they should, so that the comparison against this
// world is a comparison against furnaces that worked rather than against
// two worlds where nothing happened.
//
// Its real job is the reject fixtures: a block given an input its recipe
// type does not list has to come out of the run untouched - the same
// input, the same coal, no result, and no burn at all, because the real
// server never lights a furnace it has nothing to cook.
func checkFurnacesRan(t *testing.T, be map[[3]int]map[string]any, who string) {
	t.Helper()
	for _, f := range furnaceFixtures {
		entry, ok := be[f.pos]
		if !ok {
			t.Errorf("%s has no block entity at %v (%s)", who, f.pos, f.comment)
			continue
		}
		slots := furnaceStacks(entry)
		cook := furnaceShort(t, entry, "cooking_time_spent", f.pos)
		total := furnaceShort(t, entry, "cooking_total_time", f.pos)
		burn := furnaceShort(t, entry, "lit_time_remaining", f.pos)
		if f.output == "" {
			// Refused: nothing cooked, nothing burnt, and the input and
			// its coal still exactly as they were handed over.
			if cook != 0 || burn != 0 {
				t.Errorf("%s's %s at %v reads CookTime %d and BurnTime %d, want it never to have lit (%s)",
					who, f.block, f.pos, cook, burn, f.comment)
			}
			if s, ok := slots[0]; !ok || s.Name != "minecraft:"+f.input || s.Count != f.given {
				t.Errorf("%s's %s at %v holds %s, want its %d %s untouched (%s)",
					who, f.block, f.pos, renderNBT(entry, true), f.given, f.input, f.comment)
			}
			if s, ok := slots[1]; !ok || s.Name != "minecraft:coal" || s.Count != 1 {
				t.Errorf("%s's %s at %v holds %s, want its one coal untouched (%s)",
					who, f.block, f.pos, renderNBT(entry, true), f.comment)
			}
			if s, ok := slots[2]; ok {
				t.Errorf("%s's %s at %v produced %s x%d, want it to refuse %s (%s)",
					who, f.block, f.pos, s.Name, s.Count, f.input, f.comment)
			}
			continue
		}
		// Cooked: the result is what the recipe says, in the count the
		// cook time allows, and the input has been eaten down by it.
		s, ok := slots[2]
		if !ok || s.Name != "minecraft:"+f.output || s.Count != f.count {
			t.Errorf("%s's %s at %v holds %s in its result slot, want %d %s (%s)",
				who, f.block, f.pos, renderNBT(entry, true), f.count, f.output, f.comment)
		}
		if in, ok := slots[0]; ok && in.Count != f.given-f.count {
			t.Errorf("%s's %s at %v holds %d %s, want %d left after %d cooked (%s)",
				who, f.block, f.pos, in.Count, f.input, f.given-f.count, f.count, f.comment)
		}
		if _, ok := slots[0]; !ok && f.given-f.count != 0 {
			t.Errorf("%s's %s at %v has no input left, want %d (%s)",
				who, f.block, f.pos, f.given-f.count, f.comment)
		}
		// The cook total is the recipe's own time, which is what the
		// block's recipe type bakes in: 200 for smelting and 100 for the
		// other two. A block that refused its input reads 200 whatever it
		// is, which is the fallback the rejects above pin by reading
		// their totals below.
		wantTotal := int16(200)
		if f.block != "furnace" {
			wantTotal = 100
		}
		if total != wantTotal {
			t.Errorf("%s's %s at %v reads CookTimeTotal %d, want %d (%s)",
				who, f.block, f.pos, total, wantTotal, f.comment)
		}
	}
	// The rejects read the fallback rather than their own type's time:
	// a smoker given sand reads 200, not 100. Checking it here rather
	// than in the loop above keeps the reason next to the reason it
	// matters - it is the one field where a block's cook total is not
	// its own recipe type's.
	for _, pos := range [][3]int{{10, -60, 6}, {6, -60, 10}, {10, -60, 10}} {
		if total := furnaceShort(t, be[pos], "cooking_total_time", pos); total != 200 {
			t.Errorf("%s at %v reads cooking_total_time %d for an input it has no recipe for, want 200",
				who, pos, total)
		}
	}
}

// sprintVanilla advances the real server by exactly n ticks and returns
// once they are all simulated. The sprint is synchronous on the server
// thread - the console does not read another line until it is done - so
// the game time is read before it starts and the wait is for that many
// ticks past where the world was, rather than for an absolute count the
// saved world happens to have left off at.
func sprintVanilla(t *testing.T, say func(string), dir string, n int) {
	t.Helper()
	say("time query gametime")
	before, ok := lastGameTime(vanillaLogTailText(dir))
	if !ok {
		t.Fatalf("the real server never reported a game time; log tail:\n%s", vanillaLogTailText(dir))
	}
	say(fmt.Sprintf("tick sprint %d", n))
	waitVanillaTicks(t, say, dir, int(before)+n)
}

// furnaceShort reads one of the numeric fields a saved furnace-family
// block entity carries, under the name the real server saves it as, and
// fails when the field is not there.
//
// The failure is the point. Reading a missing field with a type
// assertion that is allowed to fail gives zero, and zero is what half of
// these checks are looking for - a furnace that never lit, an input that
// has just been emptied - so a check that spells a field name wrong
// turns into a check that passes whatever the world says. Measured: this
// test read "CookTimeTotal" off a compound whose field is
// "cooking_total_time" and reported every cooked fixture as never having
// cooked its total, which is the same shape of bug as the fixtures it
// was written to catch.
func furnaceShort(t *testing.T, entry map[string]any, key string, pos [3]int) int16 {
	t.Helper()
	v, ok := entry[key].(int16)
	if !ok {
		t.Errorf("the block entity at %v carries no %s: %s",
			pos, key, renderNBT(entry, true))
	}
	return v
}

// furnaceStacks reads a furnace-family block entity's items by slot
// number, which is how the input, the fuel and the result are told
// apart: the saved list is ordered by nothing in particular.
func furnaceStacks(entry map[string]any) map[int32]itemNBTStack {
	out := map[int32]itemNBTStack{}
	list, ok := entry["Items"].(nbt.List)
	if !ok {
		return out
	}
	for _, anyItem := range list.Items {
		item, ok := anyItem.(map[string]any)
		if !ok {
			continue
		}
		slot, ok := item["Slot"].(int8)
		if !ok {
			continue
		}
		name, _ := item["id"].(string)
		count, _ := item["count"].(int32)
		out[int32(slot)] = itemNBTStack{Name: name, Count: count}
	}
	return out
}
