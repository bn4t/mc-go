package blackbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// A chest's contents and a sign's text live in the chunk's
// block_entities list, and that list is the only place they live: get
// it wrong and the real server hands a player an empty chest, however
// right the blocks around it are.
//
// The check runs the same world state through both servers and compares
// the block entities each one keeps for it, field for field and
// position for position:
//
//	A. the real server saves a world; mc-go loads it and writes it back
//	   out, and the two lists agree;
//	B. the real server loads mc-go's copy, saves it again, and its own
//	   second save still agrees with its first.
//
// B is the one that matters to a player: it is the real server reading
// mc-go's chest and finding what was put in it.
func TestWorldBlockEntityParity(t *testing.T) {
	jar := blackboxJar(t)

	// The world both servers are given. Every block entity mc-go models
	// is here, and each one carries state that only the block entity
	// list can hold. The furnace is deliberately given no fuel: a lit
	// one would tick its burn down between the two saves, and the
	// counters are worth comparing exactly.
	fixtures := []struct {
		pos   [3]int
		block string
		cmds  []string
	}{
		{[3]int{5, -60, 5}, "chest", []string{
			"setblock 5 -60 5 minecraft:chest",
			"item replace block 5 -60 5 container.0 with minecraft:diamond 3",
			"item replace block 5 -60 5 container.26 with minecraft:apple 1",
		}},
		{[3]int{6, -60, 6}, "oak_sign", []string{
			"setblock 6 -60 6 minecraft:oak_sign[rotation=0]",
			`data merge block 6 -60 6 {front_text:{messages:['"line one"','','','']},back_text:{messages:['"behind"','','','']}}`,
		}},
		{[3]int{7, -60, 7}, "furnace", []string{
			"setblock 7 -60 7 minecraft:furnace[facing=north]",
			"item replace block 7 -60 7 container.0 with minecraft:raw_iron 2",
		}},
		{[3]int{8, -60, 8}, "hopper", []string{
			"setblock 8 -60 8 minecraft:hopper[facing=down]",
			"item replace block 8 -60 8 container.3 with minecraft:gold_ingot 12",
		}},
		{[3]int{9, -60, 9}, "barrel", []string{
			"setblock 9 -60 9 minecraft:barrel",
			"item replace block 9 -60 9 container.1 with minecraft:apple 7",
		}},
		{[3]int{10, -60, 10}, "brewing_stand", []string{
			"setblock 10 -60 10 minecraft:brewing_stand",
			"item replace block 10 -60 10 container.3 with minecraft:blaze_powder 2",
		}},
		{[3]int{11, -60, 11}, "blast_furnace", []string{
			"setblock 11 -60 11 minecraft:blast_furnace[facing=north]",
			"item replace block 11 -60 11 container.1 with minecraft:coal 4",
		}},
	}

	// ---- A: the real server saves the world ----
	vanillaDir := t.TempDir()
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort, map[string]string{"level-name": "world"}, nil, vanillaDir)
	// The chunk a console command writes into has to be loaded: 26.2
	// keeps no spawn chunks, so /setblock otherwise answers "That
	// position is not loaded" and the fixtures would silently not exist.
	say("forceload add 0 0")
	say("gamerule minecraft:advance_time false")
	for _, f := range fixtures {
		for _, c := range f.cmds {
			say(c)
		}
	}
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited with code %d, log tail:\n%s", code, vanillaLogTailText(vanillaDir))
	}
	vanillaWorld := filepath.Join(vanillaDir, "world")
	want := chunkBlockEntitiesByPos(t, vanillaWorld)
	if len(want) != len(fixtures) {
		t.Fatalf("the real server saved %d block entities, want %d: %s", len(want), len(fixtures), renderBlockEntities(want))
	}
	for _, f := range fixtures {
		if _, ok := want[f.pos]; !ok {
			t.Errorf("the real server saved no block entity at %v: %s", f.pos, renderBlockEntities(want))
		}
	}
	t.Logf("the real server's block entities: %s", renderBlockEntities(want))

	// mc-go is handed the real server's own file, so what it writes
	// back can be compared with the original directly.
	mcRoot := t.TempDir()
	mcWorld := filepath.Join(mcRoot, "world")
	copyTree(t, vanillaWorld, mcWorld)

	t.Run("mc-go writes the same block entities", func(t *testing.T) {
		mPort, ok := freePort()
		if !ok {
			t.Skip("loopback binds blocked in this environment")
		}
		addr := startMcGoCfg(t, mPort, func(s *server.Server) {
			s.WorldDir = mcWorld
			s.WorldPath = ""
			s.LevelName = "world"
		})
		c, _, pw := joinPlayBlackbox(t, addr, "Saver")
		_ = c.SetDeadline(time.Time{})
		time.Sleep(2 * time.Second)
		sendCmd(t, pw, "save-all")
		time.Sleep(commandPace)
		c.Close()

		checkFixtureBlocks(t, mcWorld, fixtures)
		got := chunkBlockEntitiesByPos(t, mcWorld)
		compareBlockEntities(t, got, want, "mc-go's copy of the real server's world")
	})

	// ---- B: the real server reads mc-go's copy ----
	t.Run("the real server reads mc-go's copy", func(t *testing.T) {
		vPort, ok := freePort()
		if !ok {
			t.Skip("loopback binds blocked in this environment")
		}
		vAddr, vsay, vwait := vanillaConsole(t, jar, vPort, map[string]string{"level-name": "world"}, nil, mcRoot)
		stopped := false
		defer func() {
			if !stopped {
				vsay("stop")
				vwait()
			}
			if t.Failed() {
				t.Logf("the real server's log after loading mc-go's world:\n%s", vanillaLogTailText(mcRoot))
			}
		}()

		// A player is what loads a chunk; without one the real server
		// never reads the file and has nothing to be right or wrong
		// about.
		vc, _, _ := joinPlayBlackbox(t, vAddr, "Loader")
		_ = vc.SetDeadline(time.Time{})
		time.Sleep(3 * time.Second)
		vc.Close()

		vsay("save-all flush")
		vsay("stop")
		stopped = true
		if code := vwait(); code != 0 {
			t.Fatalf("the real server exited with code %d", code)
		}
		got := chunkBlockEntitiesByPos(t, mcWorld)
		compareBlockEntities(t, got, want, "the real server's re-save of mc-go's world")
	})
}

// checkFixtureBlocks reads the saved world back with mc-go's own
// reader and requires every fixture block to still be there. A block
// entity is only written for a position whose block is the one that
// owns it, so without this the block entity comparison below could
// pass by finding nothing at all.
func checkFixtureBlocks(t *testing.T, worldDir string, fixtures []struct {
	pos   [3]int
	block string
	cmds  []string
}) {
	t.Helper()
	w := world.New(nil, 4)
	w.Start()
	defer w.Stop()
	if err := w.LoadDimension(worldDir, "minecraft:overworld", -64, 384); err != nil {
		t.Fatalf("read mc-go's world: %v", err)
	}
	for _, f := range fixtures {
		pos := world.Pos{X: f.pos[0], Y: f.pos[1], Z: f.pos[2]}
		got, ok := world.StateOwner(w.GetBlock(pos))
		if !ok || got != f.block {
			t.Errorf("mc-go's saved world has %q at %v, want %q", got, f.pos, f.block)
		}
	}
}

// chunkBlockEntitiesByPos reads chunk (0,0) of a world's overworld and
func chunkBlockEntitiesByPos(t *testing.T, worldDir string) map[[3]int]map[string]any {
	t.Helper()
	chunk := readChunkAt(t, worldDir, 0, 0)
	out := map[[3]int]map[string]any{}
	list, ok := chunk["block_entities"].(nbt.List)
	if !ok {
		return out
	}
	for _, anyEntry := range list.Items {
		entry, ok := anyEntry.(map[string]any)
		if !ok {
			continue
		}
		x, okx := entry["x"].(int32)
		y, oky := entry["y"].(int32)
		z, okz := entry["z"].(int32)
		if !okx || !oky || !okz {
			t.Errorf("block entity without a position: %s", renderNBT(entry, true))
			continue
		}
		out[[3]int{int(x), int(y), int(z)}] = entry
	}
	return out
}

// compareBlockEntities checks two block entity lists agree, entry by
// entry and field by field. A compound mc-go writes has to be the real
// server's compound: a field it leaves out is a field the real server
// reads as a default, which is how a chest's contents quietly become
// someone else's.
func compareBlockEntities(t *testing.T, got, want map[[3]int]map[string]any, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s holds %d block entities, want %d:\n got %s\nwant %s",
			what, len(got), len(want), renderBlockEntities(got), renderBlockEntities(want))
	}
	for pos, wantEntry := range want {
		gotEntry, ok := got[pos]
		if !ok {
			t.Errorf("%s has no block entity at %v (want %s)", what, pos, renderNBT(wantEntry, true))
			continue
		}
		if a, b := renderNBT(gotEntry, true), renderNBT(wantEntry, true); a != b {
			t.Errorf("%s at %v:\n got %s\nwant %s", what, pos, a, b)
		}
	}
}

// renderBlockEntities renders a whole block entity map for a log line.
func renderBlockEntities(m map[[3]int]map[string]any) string {
	pos := make([][3]int, 0, len(m))
	for p := range m {
		pos = append(pos, p)
	}
	sort.Slice(pos, func(i, j int) bool {
		if pos[i][0] != pos[j][0] {
			return pos[i][0] < pos[j][0]
		}
		if pos[i][1] != pos[j][1] {
			return pos[i][1] < pos[j][1]
		}
		return pos[i][2] < pos[j][2]
	})
	var b strings.Builder
	for _, p := range pos {
		fmt.Fprintf(&b, "\n  %v %s", p, renderNBT(m[p], true))
	}
	return b.String()
}

// copyTree copies a directory tree, which is how mc-go gets a world it
// can save over without destroying the reference the test compares to.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(to)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", src, dst, err)
	}
}
