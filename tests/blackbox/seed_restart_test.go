//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSeedSavedWorldParity(t *testing.T) {
	jar := blackboxJar(t)
	for _, initial := range []int64{0, 1} {
		t.Run(fmt.Sprintf("initial%d", initial), func(t *testing.T) {
			dir := t.TempDir()
			p, ok := freePort()
			if !ok {
				t.Fatal("no port")
			}
			other := int64(1) - initial
			props := func(seed int64) map[string]string {
				return map[string]string{"level-type": "minecraft:flat", "level-seed": fmt.Sprint(seed), "view-distance": "2", "simulation-distance": "2", "generate-structures": "false"}
			}
			files := map[string]string{"ops.json": opsJSONFor("SeedProbe")}
			addr, exited := startSeedNative(t, jar, p, props(initial), files, dir)
			before := seedJoinOnly(t, addr, "native-initial")
			c, pr, pw := joinPlayBlackbox(t, addr, "SeedProbe")
			awaitPlayReadyBlackbox(t, "save", c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			sendCommand(t, pw, "save-all flush")
			sendCommand(t, pw, "say SAVED_SEED_END")
			lines := waitForExactSayBB(t, sink, "SAVED_SEED_END", 60*time.Second)
			saved := false
			for _, line := range lines {
				key, _, ok := componentArgs(line)
				if ok && key == "commands.save.success" {
					saved = true
				}
			}
			if !saved {
				t.Fatal("missing save success")
			}
			sendCommand(t, pw, "stop")
			waitSeedShutdown(t, exited)
			c.Close()
			wdir := filepath.Join(dir, "world")
			lv, err := world.ReadLevel(wdir)
			if err != nil {
				t.Fatal(err)
			}
			if !lv.HasSeed || lv.Seed != initial {
				t.Fatalf("native saved seed=%d present=%t", lv.Seed, lv.HasSeed)
			}
			p, ok = freePort()
			if !ok {
				t.Fatal("no restart port")
			}
			addr, exited = startSeedNative(t, jar, p, props(other), files, dir)
			native := seedJoinOnly(t, addr, "native-restart")
			c, pr, pw = joinPlayBlackbox(t, addr, "SeedProbe")
			awaitPlayReadyBlackbox(t, "shutdown", c, pr, pw)
			sendCommand(t, pw, "stop")
			waitSeedShutdown(t, exited)
			c.Close()
			if !reflect.DeepEqual(before, native) {
				t.Errorf("native initial %+v restart %+v", before, native)
			}
			p, ok = freePort()
			if !ok {
				t.Fatal("no go port")
			}
			maddr := startMcGoCfg(t, p, func(s *server.Server) {
				s.WorldDir = wdir
				s.WorldSeed = other
				s.ViewDistance = 2
				s.SimulationDistance = 2
				s.GenerateStructures = false
				if err := s.LoadWorld(); err != nil {
					t.Fatalf("load native saved world: %v", err)
				}
			})
			actual := seedJoinOnly(t, maddr, "go-restart")
			if !reflect.DeepEqual(native, actual) {
				t.Errorf("native restart %+v go restart %+v", native, actual)
			}
		})
	}
}

func seedJoinOnly(t *testing.T, addr, tag string) seedProbeObservation {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, "SeedProbe")
	defer c.Close()
	p, err := pr.ReadPacket()
	if err != nil || p.ID != server.IDPlayJoinGame {
		t.Fatalf("%s join=%x err=%v", tag, p.ID, err)
	}
	r := protocol.NewPacketReader(p.Data)
	r.Int()
	r.Bool()
	n, _ := r.VarInt()
	for i := int32(0); i < n; i++ {
		r.String()
	}
	r.VarInt()
	r.VarInt()
	r.VarInt()
	r.Bool()
	r.Bool()
	r.Bool()
	r.VarInt()
	r.String()
	hash, err := r.Long()
	if err != nil {
		t.Fatal(err)
	}
	awaitPlayReadyBlackbox(t, tag, c, pr, pw)
	sink := newChatSinkResolvedTP(pr, pw)
	sendCommand(t, pw, "seed")
	sendCommand(t, pw, "say SEED_RESTART_END")
	lines := waitForExactSayBB(t, sink, "SEED_RESTART_END", 30*time.Second)
	var reply []any
	for _, line := range lines {
		key, args, ok := componentArgs(line)
		if ok && key == "commands.seed.success" {
			reply = args
			break
		}
	}
	if reply == nil {
		t.Fatalf("%s missing seed reply", tag)
	}
	t.Logf("%s join=%d seed=%v", tag, hash, reply)
	return seedProbeObservation{join: hash, seedReply: reply}
}

func startSeedNative(t *testing.T, jar string, port int, props, files map[string]string, dir string) (string, <-chan error) {
	t.Helper()
	writeVanillaServerDir(t, dir, port, props, files)
	log, err := os.Create(filepath.Join(dir, "vanilla.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("java", "-jar", jar, "--nogui")
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	log.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(90 * time.Second)
	for !probeStatus(addr) {
		select {
		case err := <-done:
			t.Fatalf("native exited before ready: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("native readiness timeout")
		}
		time.Sleep(500 * time.Millisecond)
	}
	return addr, done
}
func waitSeedShutdown(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("native clean stop failed: %v", err)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("native did not exit after stop")
	}
}
