package blackbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mc-go/api/registry"
	"mc-go/conformance"
	"mc-go/internal/nbt"
	publicserver "mc-go/server"
)

func compactSavePublicServer(t *testing.T, path string) (*publicserver.Server, string, func()) {
	t.Helper()
	if os.Getenv(targetProp) != "" {
		t.Skipf("%s mode: test requires public local SaveWorld completion and restart", targetProp)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback unavailable: %v", err)
	}
	s := publicserver.New(ln.Addr().String())
	s.WorldPath = path
	s.WorldDir = ""
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
func compactSaveOpen(t *testing.T, ctx context.Context, addr string, number int32, name string) *conformance.PlayClient {
	t.Helper()
	c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: number, Username: name, Timeout: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func compactSavePredicate(t *testing.T, ctx context.Context, c *conformance.PlayClient, dimension string, x, y, z int, state string) {
	t.Helper()
	token := fmt.Sprintf("compact-state-%d", time.Now().UnixNano())
	seen := false
	command := fmt.Sprintf("execute in %s if block %d %d %d %s run tellraw @s \"%s\"", dimension, x, y, z, state, token)
	if err := c.Command(command); err != nil {
		t.Fatal(err)
	}
	if err := coldJoinToken(ctx, c, fmt.Sprintf("compact-fence-%d", time.Now().UnixNano()), func(p conformance.Packet) error {
		if p.ID == 0x79 && bytes.Contains(p.Data, []byte(token)) {
			seen = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatalf("protocol %d restarted block differs: %s", c.Protocol(), command)
	}
}

// SaveWorld completion means the compact override artifacts have been written.
// Copy them before closing any session or stopping the source runtime so a
// disconnect/shutdown save cannot conceal an early receipt. Only WorldPath's
// canonical block overrides are promised here; player/container stores and
// native WorldDir persistence use separate existing paths.
func TestCompactSaveWorldCompletionRestartWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sourcePath := filepath.Join(t.TempDir(), "source.json")
	s, addr, stop := compactSavePublicServer(t, sourcePath)
	clients := map[int32]*conformance.PlayClient{}
	for _, number := range []int32{775, 776} {
		c := compactSaveOpen(t, ctx, addr, number, fmt.Sprintf("Compact%d", number))
		clients[number] = c
		shape := liveWindowShape(t, number)
		w := &liveWindowTrace{}
		center, err := liveWindowCenter(c.InitialPackets())
		if err != nil {
			t.Fatal(err)
		}
		if err := readLiveWindow(ctx, c, shape, w, center, c.InitialPackets(), nil); err != nil {
			t.Fatal(err)
		}
	}
	editor := clients[775]
	for _, command := range []string{
		"setblock 8 -61 8 minecraft:air",
		"setblock 8 40 8 minecraft:oak_log[axis=x]",
		"execute in minecraft:the_nether run setblock 8 40 8 minecraft:oak_log[axis=z]",
		"execute in minecraft:the_end run setblock 8 40 8 minecraft:stone",
	} {
		mixedBarrier(t, editor, command)
	}
	for _, c := range clients {
		compactSavePredicate(t, ctx, c, "minecraft:overworld", 8, -61, 8, "minecraft:air")
		compactSavePredicate(t, ctx, c, "minecraft:overworld", 8, 40, 8, "minecraft:oak_log[axis=x]")
		compactSavePredicate(t, ctx, c, "minecraft:the_nether", 8, 40, 8, "minecraft:oak_log[axis=z]")
		compactSavePredicate(t, ctx, c, "minecraft:the_end", 8, 40, 8, "minecraft:stone")
	}
	// Invoke the public persistence API from outside simulation ownership. Its
	// actual completion, rather than a Java tellraw fence, precedes artifact copy.
	complete := make(chan error, 1)
	go func() { complete <- s.SaveWorld() }()
	// Both already admitted profiles continue using their public command streams.
	// No assertion claims these short requests necessarily overlap physical I/O;
	// deterministic blocked-writer tests establish that scheduling property.
	for number, c := range clients {
		if err := coldJoinToken(ctx, c, fmt.Sprintf("compact-save-live-%d", number), nil); err != nil {
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
	copiedPath := filepath.Join(t.TempDir(), "copied.json")
	firstArtifacts := map[string][]byte{}
	for _, suffix := range []string{"", ".the_nether", ".the_end"} {
		raw, err := os.ReadFile(sourcePath + suffix)
		if err != nil {
			t.Fatalf("successful save missing %q: %v", suffix, err)
		}
		firstArtifacts[suffix] = raw
		if err := os.WriteFile(copiedPath+suffix, raw, 0600); err != nil {
			t.Fatal(err)
		}
		var saved struct {
			Version int
			Blocks  []struct {
				X, Y, Z int
				State   registry.StateKey
			}
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Version != 2 {
			t.Fatalf("save version %d, want canonical version2", saved.Version)
		}
		want := map[[3]int]string{}
		switch suffix {
		case "":
			want[[3]int{8, -61, 8}] = "minecraft:air"
			want[[3]int{8, 40, 8}] = "minecraft:oak_log[axis=x]"
		case ".the_nether":
			want[[3]int{8, 40, 8}] = "minecraft:oak_log[axis=z]"
		case ".the_end":
			want[[3]int{8, 40, 8}] = "minecraft:stone"
		}
		for _, row := range saved.Blocks {
			pos := [3]int{row.X, row.Y, row.Z}
			if expected, ok := want[pos]; ok {
				if row.State.String() != expected {
					t.Fatalf("artifact %q state at %v=%s want%s", suffix, pos, row.State, expected)
				}
				delete(want, pos)
			}
		}
		if len(want) != 0 {
			t.Fatalf("artifact %q lacks canonical rows: %v", suffix, want)
		}
	}
	// Save a later, different generation to the source. The restart below must
	// still consume the first completion's copied artifact, including explicit air.
	for _, command := range []string{"setblock 8 -61 8 minecraft:stone", "setblock 8 40 8 minecraft:dirt", "execute in minecraft:the_nether run setblock 8 40 8 minecraft:stone", "execute in minecraft:the_end run setblock 8 40 8 minecraft:air"} {
		mixedBarrier(t, editor, command)
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	for suffix, raw := range firstArtifacts {
		copy, err := os.ReadFile(copiedPath + suffix)
		if err != nil || !bytes.Equal(copy, raw) {
			t.Fatalf("first completion copy changed for %q: %v", suffix, err)
		}
	}
	for _, c := range clients {
		_ = c.Close()
	}
	stop()
	_, restarted, _ := compactSavePublicServer(t, copiedPath)
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			c := compactSaveOpen(t, ctx, restarted, number, fmt.Sprintf("Reload%d", number))
			shape := liveWindowShape(t, number)
			w := &liveWindowTrace{}
			center, err := liveWindowCenter(c.InitialPackets())
			if err != nil {
				t.Fatal(err)
			}
			if err := readLiveWindow(ctx, c, shape, w, center, c.InitialPackets(), nil); err != nil {
				t.Fatal(err)
			}
			body := w.loaded[[2]int32{0, 0}]
			chunk, err := conformance.DecodeChunk(number, body, shape)
			if err != nil {
				t.Fatal(err)
			}
			if got := chunk.Blocks[3*256+8*16+8]; got != 0 {
				t.Fatalf("restored explicit air became wire state %d, want independently measured air0", got)
			}
			compactSavePredicate(t, ctx, c, "minecraft:overworld", 8, -61, 8, "minecraft:air")
			compactSavePredicate(t, ctx, c, "minecraft:overworld", 8, 40, 8, "minecraft:oak_log[axis=x]")
			compactSavePredicate(t, ctx, c, "minecraft:the_nether", 8, 40, 8, "minecraft:oak_log[axis=z]")
			compactSavePredicate(t, ctx, c, "minecraft:the_end", 8, 40, 8, "minecraft:stone")
			t.Logf("protocol %d: completion-copied canonical overrides survive fresh runtime restart; explicit air independently decoded and all dimension descriptors verified over TCP", number)
		})
	}
}

func compactSaveComponent(p conformance.Packet) (map[string]any, error) {
	if p.ID != 0x79 {
		return nil, nil
	}
	r := bytes.NewReader(p.Data)
	root, err := nbt.ReadUnnamed(r)
	if err != nil {
		return nil, err
	}
	overlay, err := r.ReadByte()
	if err != nil || overlay != 0 || r.Len() != 0 {
		return nil, fmt.Errorf("system chat framing: %v", err)
	}
	value, _ := root.(map[string]any)
	return value, nil
}
func compactSaveContains(root map[string]any, key string) bool {
	if root == nil {
		return false
	}
	if root["translate"] == key {
		return true
	}
	for _, field := range []string{"with", "extra"} {
		if list, ok := root[field].(nbt.List); ok {
			for _, entry := range list.Items {
				if m, ok := entry.(map[string]any); ok && compactSaveContains(m, key) {
					return true
				}
			}
		}
	}
	return false
}

// This focused 775/776 oracle probe reuses the existing save command surface:
// Saving is caller-local, Success follows it and only Success echoes to ops.
// No claim about compact SaveWorld, fsync or restart semantics is derived from
// a vanilla save-all command, whose native persistence path remains separate.
func TestCompactSaveCommandOracleProfiles(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			jar := blackboxJar(t)
			if number == 775 {
				jar = os.Getenv("MCGO_VANILLA_775_JAR")
				if jar == "" {
					jar = filepath.Join("..", "..", "vanilla", "server-26.1.jar")
				}
				var err error
				jar, err = filepath.Abs(jar)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = os.Stat(jar); err != nil {
					t.Skipf("775 oracle unavailable: %v", err)
				}
			}
			sourceName, peerName := fmt.Sprintf("SaveSource%d", number), fmt.Sprintf("SavePeer%d", number)
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			addr, stop := startVanillaCfgFiles(t, jar, port, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor(sourceName, peerName)})
			defer stop()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			source := compactSaveOpen(t, ctx, addr, number, sourceName)
			peer := compactSaveOpen(t, ctx, addr, number, peerName)
			for index, command := range []string{"save-all", "save-all flush"} {
				if err := source.Command(command); err != nil {
					t.Fatal(err)
				}
				keys := []string{}
				for len(keys) < 2 {
					p, err := source.ReadPacket(ctx)
					if err != nil {
						t.Fatal(err)
					}
					root, err := compactSaveComponent(p)
					if err != nil {
						t.Fatal(err)
					}
					if root != nil {
						if key, ok := root["translate"].(string); ok && (key == "commands.save.saving" || key == "commands.save.success" || key == "commands.save.failed") {
							keys = append(keys, key)
						}
					}
				}
				if keys[0] != "commands.save.saving" || keys[1] != "commands.save.success" {
					t.Fatalf("%s caller keys=%v", command, keys)
				}
				echoes := []string{}
				token := fmt.Sprintf("save-echo-%d-%d", number, index)
				if err := coldJoinToken(ctx, peer, token, func(p conformance.Packet) error {
					root, err := compactSaveComponent(p)
					if err != nil {
						return err
					}
					if root != nil && root["translate"] == "chat.type.admin" {
						for _, key := range []string{"commands.save.saving", "commands.save.success", "commands.save.failed"} {
							if compactSaveContains(root, key) {
								echoes = append(echoes, key)
							}
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if len(echoes) != 1 || echoes[0] != "commands.save.success" {
					t.Fatalf("%s operator save echoes=%v, want only success", command, echoes)
				}
				t.Logf("protocol %d %s: caller Saving→Success; secondary operator only wrapped Success", number, command)
			}
		})
	}
}
