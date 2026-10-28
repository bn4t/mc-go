package blackbox

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
	publicserver "mc-go/server"
)

func nativeSavePublicServer(t *testing.T, dir string) (*publicserver.Server, string, func()) {
	t.Helper()
	if os.Getenv(targetProp) != "" {
		t.Skip("native save completion/restart requires embedded public runtime")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback unavailable: %v", err)
	}
	s := publicserver.New(ln.Addr().String())
	s.WorldDir = dir
	s.ViewDistance = 2
	s.SimulationDistance = 2
	if err := s.InitPlugins(); err != nil {
		ln.Close()
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	var once sync.Once
	stop := func() { once.Do(func() { _ = ln.Close(); s.Stop() }) }
	t.Cleanup(stop)
	return s, ln.Addr().String(), stop
}

// Copy immediately after public completion, before disconnect/shutdown can write
// again. The restarted runtime therefore consumes the exact completed artifacts.
// Frozen blocked-writer unit tests establish responsiveness independently; these
// TCP tokens exercise real 775/776 streams without claiming physical I/O overlap.
func TestNativeSaveWorldCompletionRestartWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	source := filepath.Join(t.TempDir(), "source")
	s, addr, stop := nativeSavePublicServer(t, source)
	clients := map[int32]*conformance.PlayClient{}
	for _, number := range []int32{775, 776} {
		c := compactSaveOpen(t, ctx, addr, number, fmt.Sprintf("Native%d", number))
		clients[number] = c
		center, err := liveWindowCenter(c.InitialPackets())
		if err != nil {
			t.Fatal(err)
		}
		if err := readLiveWindow(ctx, c, liveWindowShape(t, number), &liveWindowTrace{}, center, c.InitialPackets(), nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []string{
		"tick freeze",
		"setblock 8 40 8 minecraft:oak_log[axis=x]",
		"execute in minecraft:the_nether run setblock 8 40 8 minecraft:oak_log[axis=z]",
		"execute in minecraft:the_end run setblock 8 40 8 minecraft:stone",
	} {
		mixedBarrier(t, clients[775], command)
	}
	for _, c := range clients {
		compactSavePredicate(t, ctx, c, "minecraft:overworld", 8, 40, 8, "minecraft:oak_log[axis=x]")
	}
	complete := make(chan error, 1)
	go func() { complete <- s.SaveWorld() }()
	for number, c := range clients {
		if err := coldJoinToken(ctx, c, fmt.Sprintf("native-save-live-%d", number), nil); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-complete:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	copied := filepath.Join(t.TempDir(), "copy")
	files := 0
	if err := filepath.WalkDir(source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(copied, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0700)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		return os.WriteFile(dest, raw, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("successful native receipt produced no artifacts")
	}
	if _, err := os.Stat(filepath.Join(copied, "level.dat")); err != nil {
		t.Fatal(err)
	}
	for _, c := range clients {
		c.Close()
	}
	stop()
	_, restartAddr, _ := nativeSavePublicServer(t, copied)
	for _, number := range []int32{775, 776} {
		c := compactSaveOpen(t, ctx, restartAddr, number, fmt.Sprintf("Native%d", number))
		compactSavePredicate(t, ctx, c, "minecraft:overworld", 8, 40, 8, "minecraft:oak_log[axis=x]")
		compactSavePredicate(t, ctx, c, "minecraft:the_nether", 8, 40, 8, "minecraft:oak_log[axis=z]")
		compactSavePredicate(t, ctx, c, "minecraft:the_end", 8, 40, 8, "minecraft:stone")
	}
}
