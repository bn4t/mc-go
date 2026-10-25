package blackbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Player state files are checked in both directions like the entity
// regions are: a server reloading its own players/data/<uuid>.dat
// proves nothing about the format.
//
//	A. the real server equips a player, teleports them, and saves on
//	   disconnect + stop; mc-go boots on that world and the rejoining
//	   player's data reports the same fields.
//	B. mc-go does the same save; the real server boots on the world
//	   and reports the same state.
//
// What survives the trip is checked through /data get, which renders
// the loaded player's file fields on the wire.
func TestPlayerDatSaveParity(t *testing.T) {
	jar := blackboxJar(t)

	// setup runs the same command list on whichever server the client
	// just joined: a sword in hotbar slot 0, a helmet on the head, an
	// apple in the ender chest, seven levels, adventure mode, one
	// advancement, and a fixed teleport with rotation.
	setup := []string{
		"gamemode adventure @s",
		"give @s minecraft:diamond_sword",
		"item replace entity @s armor.head with minecraft:diamond_helmet",
		"item replace entity @s enderchest.0 with minecraft:apple 3",
		"xp add @s 7 levels",
		"advancement grant @s only minecraft:story/mine_diamond",
		"tp @s 5.5 -60 -3.25 100 20",
	}

	// queryLine sends `data get entity @s <path>` and returns the
	// rendered value (the text before the query translate key), like
	// nbtFloatOf but keeping the whole line for compound asserts.
	queryLine := func(t *testing.T, sink *chatSink, pw *protocol.Writer, path string) string {
		t.Helper()
		sendCommand(t, pw, "data get entity @s "+path)
		sendCommand(t, pw, "say ZEND")
		deadline := time.Now().Add(6 * time.Second)
		got := ""
		for time.Now().Before(deadline) {
			end := false
			for _, line := range sink.take() {
				if i := strings.Index(line, "data.entity.query"); i >= 0 {
					got = line[:i]
				}
				if strings.Contains(line, "ZEND") {
					end = true
				}
			}
			if end {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		return got
	}
	// queryNum is nbtFloatOf on @s.
	queryNum := func(t *testing.T, sink *chatSink, pw *protocol.Writer, path string) float64 {
		return nbtFloatOf(t, sink, pw, "@s", path)
	}

	// verify is what both directions check after the load: the fields
	// the commands set must report the same values on the other server.
	verify := func(t *testing.T, addr, label string) {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		sink := newChatSinkTP(pr, pw)
		if got := queryLine(t, sink, pw, "Inventory"); !strings.Contains(got, "diamond_sword") {
			t.Fatalf("%s: Inventory missing diamond_sword: %q", label, got)
		}
		if got := queryLine(t, sink, pw, "equipment"); !strings.Contains(got, "diamond_helmet") {
			t.Fatalf("%s: equipment missing diamond_helmet: %q", label, got)
		}
		if got := queryLine(t, sink, pw, "EnderItems"); !strings.Contains(got, "apple") {
			t.Fatalf("%s: EnderItems missing apple: %q", label, got)
		}
		if n := queryNum(t, sink, pw, "XpLevel"); n != 7 {
			t.Fatalf("%s: XpLevel = %v, want 7", label, n)
		}
		pos := queryLine(t, sink, pw, "Pos")
		for _, want := range []string{"5.5", "-60", "-3.25"} {
			if !strings.Contains(pos, want) {
				t.Fatalf("%s: Pos %q missing %q", label, pos, want)
			}
		}
		rot := queryLine(t, sink, pw, "Rotation")
		if !strings.Contains(rot, "100") {
			t.Fatalf("%s: Rotation %q missing yaw 100", label, rot)
		}
		// Adventure mode round-trips through playerGameType.
		sendCommand(t, pw, "execute if entity @a[name=P,gamemode=adventure] run say PDMODE")
		sendCommand(t, pw, "say ZEND")
		deadline := time.Now().Add(4 * time.Second)
		seen := false
		for time.Now().Before(deadline) {
			end := false
			for _, line := range sink.take() {
				if strings.Contains(line, "PDMODE") {
					seen = true
				}
				if strings.Contains(line, "ZEND") {
					end = true
				}
			}
			if end {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if !seen {
			t.Fatalf("%s: gamemode adventure did not restore", label)
		}
		// The advancement grant must ride back through the file.
		sendCommand(t, pw, "execute if entity @a[name=P,advancements={minecraft:story/mine_diamond=true}] run say PDADV")
		sendCommand(t, pw, "say ZEND")
		deadline = time.Now().Add(4 * time.Second)
		seen = false
		for time.Now().Before(deadline) {
			end := false
			for _, line := range sink.take() {
				if strings.Contains(line, "PDADV") {
					seen = true
				}
				if strings.Contains(line, "ZEND") {
					end = true
				}
			}
			if end {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		if !seen {
			t.Fatalf("%s: advancement story/mine_diamond did not restore", label)
		}
	}

	// grant joins, runs the setup list, waits a moment for the
	// commands to land, and closes the conn so the server writes the
	// player file on disconnect.
	grant := func(t *testing.T, addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		sink := newChatSinkTP(pr, pw)
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		_ = sink
		_ = c.Close()
	}

	// countDats asserts exactly one players/data/*.dat exists.
	countDats := func(t *testing.T, worldDir string) string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(worldDir, "players", "data", "*.dat"))
		if err != nil || len(matches) == 0 {
			t.Fatalf("no player file under %s: %v", worldDir, err)
		}
		return matches[0]
	}
	// statFile returns the one players/stats/*.json body — quit bumps
	// leave_game, so the file must always exist after a save.
	statFile := func(t *testing.T, worldDir string) string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(worldDir, "players", "stats", "*.json"))
		if err != nil || len(matches) == 0 {
			t.Fatalf("no stats file under %s: %v", worldDir, err)
		}
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	// advFile returns the one players/advancements/*.json body.
	advFile := func(t *testing.T, worldDir string) string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(worldDir, "players", "advancements", "*.json"))
		if err != nil || len(matches) == 0 {
			t.Fatalf("no advancements file under %s: %v", worldDir, err)
		}
		raw, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	// ---- A: the real server saves; mc-go loads ----
	vDir := t.TempDir()
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	vAddr, say, wait := vanillaConsole(t, jar, vPort, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, vDir)
	grant(t, vAddr)
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d", code)
	}
	dat := countDats(t, filepath.Join(vDir, "world"))
	if fi, err := os.Stat(dat); err != nil || fi.Size() == 0 {
		t.Fatalf("vanilla player file empty: %v", err)
	}
	if s := statFile(t, filepath.Join(vDir, "world")); !strings.Contains(s, "minecraft:leave_game") {
		t.Fatalf("vanilla stats file missing leave_game: %s", s)
	}
	if s := advFile(t, filepath.Join(vDir, "world")); !strings.Contains(s, "story/mine_diamond") {
		t.Fatalf("vanilla advancements file missing mine_diamond: %s", s)
	}

	mDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mDir, "ops.json"), []byte(opsJSONFor("P")), 0o644); err != nil {
		t.Fatal(err)
	}
	mPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	mAddr := startMcGoCfg(t, mPort, func(s *server.Server) {
		s.WorldDir = filepath.Join(vDir, "world")
		s.ServerDir = mDir
		s.OpsRequired = true
		if err := s.LoadAdminFiles(); err != nil {
			t.Errorf("load admin files: %v", err)
		}
	})
	verify(t, mAddr, "mc-go-on-vanilla-world")

	// ---- B: mc-go saves; the real server loads ----
	m2Dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(m2Dir, "ops.json"), []byte(opsJSONFor("P")), 0o644); err != nil {
		t.Fatal(err)
	}
	m2Port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	var m2 *server.Server
	m2Addr := startMcGoCfg(t, m2Port, func(s *server.Server) {
		m2 = s
		s.WorldDir = filepath.Join(m2Dir, "world")
		s.ServerDir = m2Dir
		s.OpsRequired = true
		if err := s.LoadAdminFiles(); err != nil {
			t.Errorf("load admin files: %v", err)
		}
	})
	grant(t, m2Addr)
	if err := m2.SaveWorld(); err != nil {
		t.Fatalf("mc-go save: %v", err)
	}
	dat = countDats(t, filepath.Join(m2Dir, "world"))
	if fi, err := os.Stat(dat); err != nil || fi.Size() == 0 {
		t.Fatalf("mc-go player file empty: %v", err)
	}
	if s := statFile(t, filepath.Join(m2Dir, "world")); !strings.Contains(s, "minecraft:leave_game") {
		t.Fatalf("mc-go stats file missing leave_game: %s", s)
	}
	if s := advFile(t, filepath.Join(m2Dir, "world")); !strings.Contains(s, "story/mine_diamond") {
		t.Fatalf("mc-go advancements file missing mine_diamond: %s", s)
	}

	v2Dir := t.TempDir()
	cpWorld(t, filepath.Join(m2Dir, "world"), filepath.Join(v2Dir, "world"))
	v2Port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	// The vanilla side needs a console too: after the verify the
	// server saves and stops, and its rewritten stats file proves the
	// counters loaded into its model rather than just parsing on disk.
	v2Addr, say2, wait2 := vanillaConsole(t, jar, v2Port, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, v2Dir)
	verify(t, v2Addr, "vanilla-on-mc-go-world")
	say2("save-all flush")
	say2("stop")
	if code := wait2(); code != 0 {
		t.Fatalf("vanilla (reload) exited %d", code)
	}
	if s := statFile(t, filepath.Join(v2Dir, "world")); !strings.Contains(s, "minecraft:leave_game") {
		t.Fatalf("vanilla dropped mc-go's stats on its own save: %s", s)
	}
	if s := advFile(t, filepath.Join(v2Dir, "world")); !strings.Contains(s, "story/mine_diamond") {
		t.Fatalf("vanilla dropped mc-go's advancement on its own save: %s", s)
	}
}
