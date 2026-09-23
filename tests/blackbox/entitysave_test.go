package blackbox

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// Entity persistence is checked in both directions for the same
// reason worldsave_test checks the chunk format both ways: a server
// loading its own files proves nothing about the format.
//
//	A. the real server summons two mobs, saves, and stops; mc-go boots
//	   on the directory and a joining client sees both mobs at their
//	   positions.
//	B. mc-go does the same save; the real server boots on the
//	   directory and reports the same spawns.
//
// The probes are NoAI summons at fixed positions so a rejoining client
// sees them exactly where they were left. The zombie sits in a chunk
// east of the origin and the pig in one to the west, so each gets its
// own teleport probe rather than relying on one view covering both.
func TestEntitySaveParity(t *testing.T) {
	jar := blackboxJar(t)
	zombieID, _ := server.EntityTypeID("zombie")
	pigID, _ := server.EntityTypeID("pig")

	summonZ := "summon minecraft:zombie 10 -60 10 {NoAI:1b,PersistenceRequired:1b}"
	summonP := "summon minecraft:pig -5.5 -60 2.5 {NoAI:1b}"

	// seeAt joins addr as name, teleports to (x,z), and returns the
	// add_entity spawn positions by entity type that arrive.
	seeAt := func(t *testing.T, addr, name string, x, z float64) map[int32][][3]float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		var pos [3]float64
		spawns := map[int32][][3]float64{}
		read := func(d time.Duration) {
			deadline := time.Now().Add(d)
			for time.Now().Before(deadline) {
				_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
				p, err := pr.ReadPacket()
				if err != nil {
					break
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					var kb protocol.Buffer
					kb.Raw(p.Data)
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
				case server.IDPlayChunkBatchDone:
					_ = ackChunkBatch(pw)
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					if id, err := r.VarInt(); err == nil {
						x, _ := r.Double()
						y, _ := r.Double()
						z, _ := r.Double()
						pos[0], pos[1], pos[2] = x, y, z
						var b protocol.Buffer
						b.VarInt(id)
						_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					}
				case server.IDPlaySpawnEntity:
					r := protocol.NewPacketReader(p.Data)
					_, e1 := r.VarInt()
					_, e2 := r.Read(16)
					typ, e3 := r.VarInt()
					ex, e4 := r.Double()
					ey, e5 := r.Double()
					ez, e6 := r.Double()
					if e1 == nil && e2 == nil && e3 == nil &&
						e4 == nil && e5 == nil && e6 == nil {
						spawns[typ] = append(spawns[typ], [3]float64{ex, ey, ez})
					}
				}
			}
		}
		read(1200 * time.Millisecond)
		sendCommand(t, pw, fmt.Sprintf("tp @s %d -59 %d", int(x), int(z)))
		read(1500 * time.Millisecond)
		_ = c.Close()
		return spawns
	}
	near := func(list [][3]float64, want [3]float64, tol float64) bool {
		for _, p := range list {
			if math.Abs(p[0]-want[0]) <= tol &&
				math.Abs(p[1]-want[1]) <= tol &&
				math.Abs(p[2]-want[2]) <= tol {
				return true
			}
		}
		return false
	}

	// ---- A: the real server saves; mc-go loads ----
	vDir := t.TempDir()
	vPort, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	_, say, wait := vanillaConsole(t, jar, vPort,
		map[string]string{"level-name": "world", "spawn-protection": "0"},
		map[string]string{"ops.json": opsJSONFor("P")}, vDir)
	say("gamerule minecraft:advance_time false")
	say("forceload add 0 0")
	say("forceload add -1 0")
	say(summonZ)
	say(summonP)
	say("save-all flush")
	say("stop")
	if code := wait(); code != 0 {
		t.Fatalf("vanilla exited %d", code)
	}
	entDir := filepath.Join(vDir, "world", "dimensions", "minecraft", "overworld", "entities")
	ents, err := os.ReadDir(entDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("vanilla wrote no entity regions under %s: %v", entDir, err)
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
	gotZ := seeAt(t, mAddr, "P", 10, 10)
	if !near(gotZ[zombieID], [3]float64{10, -60, 10}, 1.5) {
		t.Fatalf("mc-go reload dropped the zombie: spawns %v", gotZ[zombieID])
	}
	gotP := seeAt(t, mAddr, "P", -5, 2)
	if !near(gotP[pigID], [3]float64{-5.5, -60, 2.5}, 1.5) {
		t.Fatalf("mc-go reload dropped the pig: spawns %v", gotP[pigID])
	}

	// ---- B: mc-go saves; the real server loads ----
	m2Dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(m2Dir, "ops.json"), []byte(opsJSONFor("Q")), 0o644); err != nil {
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
	c2, pr2, pw2 := joinPlayBlackbox(t, m2Addr, "Q")
	// Drain the join burst, then place the two mobs.
	deadline := time.Now().Add(1200 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = c2.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		p, err := pr2.ReadPacket()
		if err != nil {
			break
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			var kb protocol.Buffer
			kb.Raw(p.Data)
			_ = pw2.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw2)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw2.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		}
	}
	sendCommand(t, pw2, summonZ)
	sendCommand(t, pw2, summonP)
	time.Sleep(800 * time.Millisecond)
	if err := m2.SaveWorld(); err != nil {
		t.Fatalf("mc-go save: %v", err)
	}
	_ = c2.Close()
	if fi, err := os.Stat(filepath.Join(m2Dir, "world", "dimensions", "minecraft", "overworld", "entities", "r.0.0.mca")); err != nil || fi.Size() == 0 {
		t.Fatalf("mc-go wrote no entity region: %v", err)
	}

	v2Dir := t.TempDir()
	// vanilla's level-name is the directory under <serverdir>/world —
	// point it at mc-go's world by running the server in m2Dir's
	// parent layout is awkward; simpler: boot vanilla in a fresh dir
	// with a symlinked/copy world. Use a plain copy of the world dir.
	cpWorld(t, filepath.Join(m2Dir, "world"), filepath.Join(v2Dir, "world"))
	v2Port, ok := freePort()
	if !ok {
		t.Skip("loopback binds blocked in this environment")
	}
	v2Addr, stop2 := startVanillaInDir(t, jar, v2Port,
		map[string]string{"level-name": "world", "spawn-protection": "0"},
		map[string]string{"ops.json": opsJSONFor("R")}, v2Dir)
	defer stop2()
	gotZ2 := seeAt(t, v2Addr, "R", 10, 10)
	if !near(gotZ2[zombieID], [3]float64{10, -60, 10}, 1.5) {
		if l, err := os.ReadFile(filepath.Join(v2Dir, "vanilla.log")); err == nil {
			t.Logf("vanilla log:\n%s", l)
		}
		if l, err := world.LoadEntityRegions(filepath.Join(v2Dir, "world", "dimensions", "minecraft", "overworld")); err == nil {
			for _, e := range l {
				t.Logf("file entity id=%v pos=%v", e["id"], e["Pos"])
			}
		}
		t.Fatalf("vanilla reload dropped the zombie: spawns %v", gotZ2[zombieID])
	}
	gotP2 := seeAt(t, v2Addr, "R", -5, 2)
	if !near(gotP2[pigID], [3]float64{-5.5, -60, 2.5}, 1.5) {
		t.Fatalf("vanilla reload dropped the pig: spawns %v", gotP2[pigID])
	}
}

// cpWorld copies a world directory (level.dat, dimensions tree) into
// dst — used to hand a saved world between servers without either
// holding it open.
func cpWorld(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		to := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy world: %v", err)
	}
}
