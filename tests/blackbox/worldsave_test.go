package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// World saving is checked in both directions, because either one alone
// can pass while the format is wrong: mc-go reading its own files
// proves nothing about whether the real server can, and the real
// server's file is only half the story if mc-go cannot read it.
//
//	testWorldHandoff
//	  A. the real server saves a world; mc-go loads it, and both agree
//	     on the blocks and the time of day.
//	  B. mc-go saves a world; the real server loads it, and both agree
//	     the same way.
//	  C. the chunk mc-go writes for a given world state is compared
//	     field by field with the chunk the real server wrote for the
//	     same state.
//
// C is what pins the format itself: A and B can pass with a chunk that
// is merely loadable, and the fields a client uses - the heightmaps,
// the section indices, the palette - would still be free to drift.
func TestWorldSaveParity(t *testing.T) {
	jar := blackboxJar(t)

	// saveBlocks is the world state both directions write, chosen to
	// cover the parts of a chunk the format has to get right: a block
	// buried under the terrain, one high above it (a heightmap the
	// generator never produces), one whose column holds a non-solid
	// block, and one that replaces a generated block.
	saveBlocks := []struct {
		command string
		pos     [3]int
		name    string
	}{
		{"setblock 5 -50 5 minecraft:stone", [3]int{5, -50, 5}, "minecraft:stone"},
		{"setblock 5 100 5 minecraft:glass", [3]int{5, 100, 5}, "minecraft:glass"},
		{"setblock 9 -60 9 minecraft:oak_leaves", [3]int{9, -60, 9}, "minecraft:oak_leaves"},
		{"setblock 12 -61 12 minecraft:stone_bricks", [3]int{12, -61, 12}, "minecraft:stone_bricks"},
	}
	positions := make([][3]int, 0, len(saveBlocks))
	want := map[[3]int]string{}
	for _, b := range saveBlocks {
		positions = append(positions, b.pos)
		want[b.pos] = b.name
	}
	// The candidates a position is probed against: every block the
	// world is known to hold, so an answer is a name rather than a
	// yes/no against one guess.
	candidates := []string{"minecraft:stone", "minecraft:glass", "minecraft:oak_leaves", "minecraft:stone_bricks", "minecraft:grass_block", "minecraft:air"}

	// dayTime is what both save: a fixed, non-zero, non-default time of
	// day, with the day clock stopped so the reading is exact rather
	// than a moving one.
	const dayTime = 6000

	// ---- A: the real server saves, mc-go reads ----
	vanillaDir := t.TempDir()
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort, map[string]string{"level-name": "world"}, nil, vanillaDir)
	// The chunk has to be loaded, or /setblock refuses every position in
	// it with "That position is not loaded": 26.2 does not keep the
	// spawn chunks loaded the way older versions did, and a console
	// command has no player whose view would load them. The test does
	// not join this server, so it forces the chunk instead - the same
	// command an operator would use, and the world it leaves behind is
	// still an ordinary one.
	say("forceload add 0 0")
	say("gamerule minecraft:advance_time false")
	say(fmt.Sprintf("time set %d", dayTime))
	for _, b := range saveBlocks {
		say(b.command)
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited with code %d, log tail:\n%s", code, vanillaLogTailText(vanillaDir))
	}
	savedDir := filepath.Join(vanillaDir, "world")
	lv, err := world.ReadLevel(savedDir)
	if err != nil {
		t.Fatalf("read the world vanilla saved: %v", err)
	}
	// The level file is the reference for everything below, so its own
	// reading is checked first: if vanilla did not save what this test
	// asked it to, the comparison after it would be measuring nothing.
	if lv.DayTime != dayTime {
		t.Fatalf("vanilla saved day time %d, want %d (the clock should be stopped)", lv.DayTime, dayTime)
	}
	if lv.Spawn != [3]int{0, -60, 0} {
		t.Errorf("vanilla saved spawn %v, want the flat world's [0 -60 0]", lv.Spawn)
	}
	if lv.Seed != 1 {
		t.Errorf("vanilla saved seed %d, want 1", lv.Seed)
	}
	t.Logf("vanilla's level.dat: spawn=%v seed=%d gametime=%d daytime=%d rules=%d",
		lv.Spawn, lv.Seed, lv.GameTime, lv.DayTime, len(lv.GameRules))

	// C: the chunk mc-go writes for this world state.
	t.Run("chunk format", func(t *testing.T) {
		compareChunkFormat(t, savedDir, want)
	})

	t.Run("mc-go reads vanilla's world", func(t *testing.T) {
		mPort, ok := freePort()
		if !ok {
			t.Skip("loopback binds blocked in this environment")
		}
		addr := startMcGoCfg(t, mPort, func(s *server.Server) {
			s.WorldDir = savedDir
			s.WorldPath = ""
			s.LevelName = "world"
		})
		c, pr, pw := joinPlayBlackbox(t, addr, "Reader")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sink := newChatSink(pr, pw)
		time.Sleep(2 * time.Second)
		sink.take()

		got := queryNames(t, pw, sink, candidates, positions)
		for _, pos := range positions {
			if got[pos] != want[pos] {
				t.Errorf("mc-go reports %s at %v, want %s", got[pos], pos, want[pos])
			}
		}
		if day := queryDayClock(t, pw, sink); day != dayTime {
			t.Errorf("mc-go reports day time %d after loading, want %d", day, dayTime)
		}
	})

	// ---- B: mc-go saves, the real server reads ----
	t.Run("vanilla reads mc-go's world", func(t *testing.T) {
		dir := t.TempDir()
		mPort, ok := freePort()
		if !ok {
			t.Skip("loopback binds blocked in this environment")
		}
		addr := startMcGoCfg(t, mPort, func(s *server.Server) {
			s.WorldDir = filepath.Join(dir, "world")
			s.WorldPath = ""
			s.LevelName = "world"
			s.WorldSeed = 1
		})
		c, pr, pw := joinPlayBlackbox(t, addr, "Writer")
		_ = c.SetDeadline(time.Time{})
		sink := newChatSink(pr, pw)
		time.Sleep(2 * time.Second)
		sink.take()

		// The day clock is stopped through the packet a client sends
		// when it runs /gamerule, so the saved time is exact.
		setGameRule(t, pw, "minecraft:advance_time", "false")
		sendCmd(t, pw, fmt.Sprintf("time set %d", dayTime))
		for _, b := range saveBlocks {
			sendCmd(t, pw, b.command)
		}
		sendCmd(t, pw, "save-all")
		waitDrained(t, pw, sink)
		c.Close()

		// mc-go's own file must carry what the commands asked for, or
		// the real server is about to be handed a different world than
		// the one this test means to give it.
		lv, err := world.ReadLevel(filepath.Join(dir, "world"))
		if err != nil {
			t.Fatalf("read the world mc-go saved: %v", err)
		}
		if lv.DayTime != dayTime {
			t.Fatalf("mc-go saved day time %d, want %d", lv.DayTime, dayTime)
		}

		vPort, ok := freePort()
		if !ok {
			t.Skip("loopback binds blocked in this environment")
		}
		vAddr, vsay, vwait := vanillaConsole(t, jar, vPort, map[string]string{"level-name": "world"}, nil, dir)
		logTail := func() {
			if t.Failed() {
				// A failure here can be the real server rejecting the
				// world it was handed, which only its own log says.
				t.Logf("the real server's log after loading mc-go's world:\n%s", vanillaLogTailText(dir))
			}
		}
		// stopped keeps the cleanup from writing to a console whose
		// server has already exited, which is a closed pipe.
		stopped := false
		defer func() {
			if !stopped {
				vsay("stop")
				vwait()
			}
			logTail()
		}()

		// A player is what loads the chunk: nothing else makes the real
		// server read a chunk off disk, and a chunk it never read is one
		// it cannot have got wrong.
		vc, vpr, vpw := joinPlayBlackbox(t, vAddr, "Loader")
		_ = vc.SetDeadline(time.Time{})
		vsink := newChatSink(vpr, vpw)
		time.Sleep(2 * time.Second)
		vsink.take()
		// A player cannot run /time at all until it is an operator, and
		// the real server answers a plain player with an unknown-command
		// error rather than a reading.
		vsay("op Loader")
		time.Sleep(1500 * time.Millisecond)
		vsink.take()
		// The clock: the real server resumed the saved world, so its day
		// time is where mc-go left it plus whatever it has run since.
		// Boot takes seconds and the saved rule stops the clock, so a
		// window is the honest assertion - the exact reading is checked
		// on the file above.
		day := queryDayClock(t, vpw, vsink)
		if day < dayTime || day > dayTime+2000 {
			t.Errorf("vanilla reports day time %d after loading a world saved at %d, want it within [%d,%d]",
				day, dayTime, dayTime, dayTime+2000)
		}
		vc.Close()

		// What the real server made of mc-go's chunks is read back out of
		// the file it writes when it stops: asking it block by block over
		// chat is a flood of commands it answers by kicking the client
		// ("Kicked for spamming"), and its own saved chunk is the same
		// answer without the traffic.
		vsay("save-all flush")
		vsay("stop")
		stopped = true
		if code := vwait(); code != 0 {
			logTail()
			t.Fatalf("the real server exited with code %d", code)
		}
		reload := world.New(nil, 4)
		reload.Start()
		defer reload.Stop()
		if err := reload.LoadDimension(filepath.Join(dir, "world"), "minecraft:overworld", -64, 384); err != nil {
			t.Fatalf("read the world the real server saved: %v", err)
		}
		for _, b := range saveBlocks {
			wantBlock, ok := world.BlockState(strings.TrimPrefix(b.name, "minecraft:"))
			if !ok {
				t.Fatalf("unknown block %s", b.name)
			}
			p := world.Pos{X: b.pos[0], Y: b.pos[1], Z: b.pos[2]}
			if got := reload.GetBlock(p); got != wantBlock {
				name := "unknown"
				if n, ok := world.StateOwner(got); ok {
					name = n
				}
				t.Errorf("the real server's saved world has %s at %v, want %s", name, b.pos, b.name)
			}
		}
		// The rest of the chunk is the flat world the generator makes,
		// which is what tells a world that was read from one that was
		// regenerated around the four blocks above.
		if got := reload.GetBlock(world.Pos{X: 100, Y: -61, Z: 100}); got != world.Grass {
			t.Errorf("untouched column is %d, want the flat world's grass", got)
		}
	})
}

// queryDayClock runs /time query day and returns the reading, which the
// reply carries as a nested component with the value under an empty
// key - the same shape the real server uses.
func queryDayClock(t *testing.T, pw *protocol.Writer, sink *chatSink) int64 {
	t.Helper()
	sink.take()
	sendCommand(t, pw, "time query day")
	for _, line := range waitForReply(t, sink, "time query day") {
		key, args, ok := componentArgs(line)
		if !ok || key != "commands.time.query.timeline" || len(args) < 2 {
			continue
		}
		if v, ok := nestedNumber(args[len(args)-1]); ok {
			return v
		}
	}
	t.Fatalf("/time query day gave no reading; sink health: %s", sink.health())
	return 0
}

// nestedNumber reads the value a nested component carries under its
// empty key.
func nestedNumber(v any) (int64, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	switch n := m[""].(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	default:
		return 0, false
	}
}

// waitForReply collects System Chat lines for a few seconds after a
// command, giving a reply time to arrive without waiting for a quiet
// stream (a query is not the last thing on the wire).
func waitForReply(t *testing.T, sink *chatSink, what string) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var out []string
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		out = append(out, sink.take()...)
		if len(out) > 0 {
			break
		}
	}
	return out
}

// setGameRule sends the packet a real client sends for /gamerule, which
// is the only way a game rule moves in either server.
func setGameRule(t *testing.T, pw *protocol.Writer, name, value string) {
	t.Helper()
	var b protocol.Buffer
	b.String(name)
	b.String(value)
	if err := pw.WritePacket(server.IDPlaySetGameRuleSB, b.Bytes()); err != nil {
		t.Fatalf("set game rule %s: %v", name, err)
	}
	time.Sleep(commandPace)
}

// compareChunkFormat rebuilds the same world state in mc-go and
// compares the chunk it writes with the one the real server wrote for
// it, field by field.
//
// The generator is the shared ground: both servers produce the flat
// preset, and the test's block edits are applied on top, so the two
// chunks describe the same 16x16x384 blocks. Everything except the
// light arrays has to match: the real server stores light and mc-go
// does not, which is why the chunk says isLightOn=0 and the real server
// lights the chunk itself.
func compareChunkFormat(t *testing.T, savedDir string, blocks map[[3]int]string) {
	t.Helper()
	vanillaChunk := readChunkAt(t, savedDir, 0, 0)

	// The same world, built in process: the flat generator plus the
	// test's edits, saved into a scratch directory.
	w := world.New(nil, 8)
	for pos, name := range blocks {
		b, ok := world.BlockState(strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			t.Fatalf("unknown block %s", name)
		}
		w.SetBlock(world.Pos{X: pos[0], Y: pos[1], Z: pos[2]}, b)
	}
	outDir := t.TempDir()
	// Through the same entry point a real save uses, so the write lands
	// at the vanilla path (dimensions/<namespace>/<path>/region) that
	// the read below expects.
	if err := w.SaveDimension(outDir, "minecraft:overworld", -64, 384, nil); err != nil {
		t.Fatalf("save mc-go's chunk: %v", err)
	}
	w.Stop()
	mcgoChunk := readChunkAt(t, outDir, 0, 0)

	for _, field := range []string{"DataVersion", "xPos", "yPos", "zPos", "Status"} {
		if av, bv := renderField(vanillaChunk[field]), renderField(mcgoChunk[field]); av != bv {
			t.Errorf("chunk %s: vanilla %s, mc-go %s", field, av, bv)
		}
	}
	// The heightmaps are the part a client trusts rather than
	// recomputes, and the part most easily wrong: computed from the
	// wrong blocks they move the terrain and the sky.
	vh, _ := vanillaChunk["Heightmaps"].(map[string]any)
	mh, _ := mcgoChunk["Heightmaps"].(map[string]any)
	if vh == nil || mh == nil {
		t.Fatalf("heightmaps missing: vanilla %v, mc-go %v", vh != nil, mh != nil)
	}
	for _, kind := range []string{"WORLD_SURFACE", "OCEAN_FLOOR", "MOTION_BLOCKING", "MOTION_BLOCKING_NO_LEAVES"} {
		vv, okv := vh[kind].([]int64)
		mv, okm := mh[kind].([]int64)
		if !okv || !okm {
			t.Fatalf("heightmap %s missing or not a long array (vanilla %v, mc-go %v)", kind, okv, okm)
		}
		if len(vv) != len(mv) {
			t.Fatalf("heightmap %s: vanilla %d longs, mc-go %d", kind, len(vv), len(mv))
		}
		for i := range vv {
			if vv[i] != mv[i] {
				t.Errorf("heightmap %s long %d: vanilla %#x, mc-go %#x", kind, i, vv[i], mv[i])
				break
			}
		}
	}

	// Sections: the real server stores light in some of them, so only
	// the block data and the biome palette are compared.
	vs, vok := vanillaChunk["sections"].(nbt.List)
	ms, mok := mcgoChunk["sections"].(nbt.List)
	if !vok || !mok {
		t.Fatalf("sections missing or not a list: vanilla %v, mc-go %v", vok, mok)
	}
	if len(vs.Items) != len(ms.Items) {
		t.Fatalf("sections: vanilla %d, mc-go %d", len(vs.Items), len(ms.Items))
	}
	if len(vs.Items) == 0 {
		t.Fatal("neither chunk has a section, so the comparison below measures nothing")
	}
	litSections := 0
	for i := range vs.Items {
		vsec, _ := vs.Items[i].(map[string]any)
		msec, _ := ms.Items[i].(map[string]any)
		if vsec == nil || msec == nil {
			t.Fatalf("section %d is not a compound", i)
		}
		for _, field := range []string{"Y", "block_states", "biomes"} {
			if av, bv := renderField(vsec[field]), renderField(msec[field]); av != bv {
				t.Errorf("section %d %s: vanilla %s, mc-go %s", i, field, av, bv)
			}
		}
		// The light arrays are what the block data comparison above must
		// skip, and they are only in some sections: the real server
		// leaves the arrays out of a section whose light is uniform, and
		// writes them where it lit something. A chunk with no light at
		// all would mean light was skipped for the wrong reason, and
		// mc-go writing light would mean the two chunks were being
		// compared field by field while differing in a field this test
		// never reads.
		if _, ok := vsec["SkyLight"]; ok {
			litSections++
		}
		if _, ok := msec["SkyLight"]; ok {
			t.Errorf("section %d: mc-go writes light, which the comparison above skips", i)
		}
	}
	if litSections == 0 {
		t.Error("no section of the real server's chunk carries light, so skipping the light comparison measures nothing")
	}
	t.Logf("the real server lit %d of %d sections; mc-go writes no light at all", litSections, len(vs.Items))
	// isLightOn is the documented divergence: mc-go writes no light, so
	// it says so and the real server lights the chunk when it loads it.
	if v, ok := vanillaChunk["isLightOn"].(int8); !ok || v != 1 {
		t.Errorf("vanilla chunk isLightOn = %v, want 1", vanillaChunk["isLightOn"])
	}
	if v, ok := mcgoChunk["isLightOn"].(int8); !ok || v != 0 {
		t.Errorf("mc-go chunk isLightOn = %v, want 0 (mc-go stores no light)", mcgoChunk["isLightOn"])
	}
	// The lists that describe a chunk's contents must be present and
	// empty rather than missing: the real server reads them
	// unconditionally.
	for _, field := range []string{"block_entities", "block_ticks", "fluid_ticks", "PostProcessing", "structures"} {
		if _, ok := mcgoChunk[field]; !ok {
			t.Errorf("mc-go chunk has no %s field", field)
		}
	}
	// A 26.2 chunk carries no entities key at all: a dimension's mobs
	// live in its own entities/ region files. The real server's own
	// chunk is the evidence for that, and mc-go copying a chunk with an
	// entities list would be storing mobs where the real server looks
	// for none.
	if _, ok := vanillaChunk["entities"]; ok {
		t.Error("the real server's chunk has an entities key, so this test's premise is wrong")
	}
	if _, ok := mcgoChunk["entities"]; ok {
		t.Error("mc-go's chunk carries an entities key, want none")
	}
}

// renderField renders one chunk field canonically, so a comparison
// reports what differs rather than that something does.
func renderField(v any) string {
	return renderNBT(v, true)
}

// readChunkAt parses one chunk out of a world directory's overworld
// region files.
func readChunkAt(t *testing.T, worldDir string, cx, cz int32) map[string]any {
	t.Helper()
	dir := filepath.Join(world.DimensionDir(worldDir, "minecraft:overworld"), "region")
	rx, rz := cx>>5, cz>>5
	path := filepath.Join(dir, fmt.Sprintf("r.%d.%d.mca", rx, rz))
	chunks, err := world.ReadRegionFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	c, ok := chunks[[2]int32{cx & 31, cz & 31}]
	if !ok {
		t.Fatalf("chunk (%d,%d) not in %s", cx, cz, path)
	}
	return c
}

// vanillaLogTailText is vanillaLogTail for a directory the test chose
// rather than the shared debug one.
func vanillaLogTailText(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "vanilla.log"))
	if err != nil {
		return ""
	}
	if len(b) > 3000 {
		b = b[len(b)-3000:]
	}
	return string(b)
}
