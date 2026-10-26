package blackbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/conformance"
)

type liveWindowTrace struct {
	loaded                   map[[2]int32][]byte
	center                   [2]int32
	open                     bool
	count                    int
	chunks, forgets, batches int
	events                   []string
}

func liveWindowShape(t *testing.T, number int32) conformance.ChunkShape {
	t.Helper()
	raw, err := os.ReadFile("../../conformance/testdata/mixed-profile-facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Profiles map[string]mixedFacts }
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	p, ok := f.Profiles[fmt.Sprint(number)]
	if !ok {
		t.Fatal("missing official profile facts")
	}
	return conformance.ChunkShape{Sections: 24, BlockStates: p.BlockStates, Biomes: p.Biomes}
}
func liveWindowCenter(packets []conformance.Packet) ([2]int32, error) {
	for _, p := range packets {
		if p.ID == 0x5e {
			r := bytes.NewReader(p.Data)
			x, e := mixedReadVI(r)
			if e != nil {
				return [2]int32{}, e
			}
			z, e := mixedReadVI(r)
			if e != nil || r.Len() != 0 {
				return [2]int32{}, fmt.Errorf("cache center framing: %v", e)
			}
			return [2]int32{x, z}, nil
		}
	}
	return [2]int32{}, fmt.Errorf("initial cache center absent")
}
func (w *liveWindowTrace) apply(c *conformance.PlayClient, shape conformance.ChunkShape, p conformance.Packet) error {
	if w.loaded == nil {
		w.loaded = map[[2]int32][]byte{}
	}
	switch p.ID {
	case 0x20:
		return fmt.Errorf("play disconnected: %x", p.Data)
	case 0x48:
		r := bytes.NewReader(p.Data)
		_, err := mixedReadVI(r)
		if err != nil {
			return err
		}
		var xyz [3]float64
		if err := binary.Read(r, binary.BigEndian, &xyz); err != nil {
			return err
		}
		w.events = append(w.events, fmt.Sprintf("sync(%.1f,%.1f,%.1f)", xyz[0], xyz[1], xyz[2]))
		// Vanilla's destination streaming advances after its confirmed teleport
		// receives a matching position, as a real client sends. This is separate
		// from PlayClient's automatic teleport confirmation.
		if err := c.Move(xyz[0], xyz[1], xyz[2], 0, 0, false); err != nil {
			return err
		}
	case 0x5e:
		r := bytes.NewReader(p.Data)
		x, err := mixedReadVI(r)
		if err != nil {
			return err
		}
		z, err := mixedReadVI(r)
		if err != nil || r.Len() != 0 {
			return fmt.Errorf("cache center framing: %v", err)
		}
		w.center = [2]int32{x, z}
		w.events = append(w.events, fmt.Sprintf("center(%d,%d)", x, z))
	case 0x0c:
		if w.open || len(p.Data) != 0 {
			return fmt.Errorf("invalid/nested chunk batch")
		}
		w.open = true
		w.count = 0
		w.events = append(w.events, "start")
	case 0x2d:
		if !w.open {
			return fmt.Errorf("chunk outside batch")
		}
		chunk, err := conformance.DecodeChunk(c.Protocol(), p.Data, shape)
		if err != nil {
			return err
		}
		key := [2]int32{chunk.X, chunk.Z}
		if _, exists := w.loaded[key]; exists {
			return fmt.Errorf("duplicate loaded column %v", key)
		}
		w.loaded[key] = p.Data
		w.chunks++
		w.count++
	case 0x0b:
		r := bytes.NewReader(p.Data)
		count, err := mixedReadVI(r)
		if err != nil || r.Len() != 0 || !w.open || count < 1 || int(count) != w.count {
			return fmt.Errorf("batch count %d, actual %d, open=%v: %v", count, w.count, w.open, err)
		}
		w.open = false
		w.batches++
		w.events = append(w.events, fmt.Sprintf("done(%d)", count))
	case 0x25:
		if len(p.Data) != 8 {
			return fmt.Errorf("forget packet length %d", len(p.Data))
		}
		packed := binary.BigEndian.Uint64(p.Data)
		key := [2]int32{int32(packed), int32(packed >> 32)}
		if _, exists := w.loaded[key]; !exists {
			return fmt.Errorf("forgot unstreamed column %v", key)
		}
		delete(w.loaded, key)
		w.forgets++
		if w.forgets == 1 {
			w.events = append(w.events, "first-forget")
		}
	}
	return nil
}
func (w *liveWindowTrace) hasWindow(center [2]int32) bool {
	if w.open || w.center != center {
		return false
	}
	for dx := int32(-3); dx <= 3; dx++ {
		for dz := int32(-3); dz <= 3; dz++ {
			if w.loaded[[2]int32{center[0] + dx, center[1] + dz}] == nil {
				return false
			}
		}
	}
	return true
}
func readLiveWindow(ctx context.Context, c *conformance.PlayClient, shape conformance.ChunkShape, w *liveWindowTrace, center [2]int32, initial []conformance.Packet, observe func(conformance.Packet)) error {
	for {
		var p conformance.Packet
		if len(initial) > 0 {
			p, initial = initial[0], initial[1:]
		} else {
			var err error
			p, err = c.ReadPacket(ctx)
			if err != nil {
				return err
			}
		}
		if err := w.apply(c, shape, p); err != nil {
			return err
		}
		if observe != nil {
			observe(p)
		}
		if w.hasWindow(center) {
			return nil
		}
	}
}
func liveWindowFence(t *testing.T, ctx context.Context, c *conformance.PlayClient, shape conformance.ChunkShape, w *liveWindowTrace, token string) {
	t.Helper()
	if err := coldJoinToken(ctx, c, token, func(p conformance.Packet) error { return w.apply(c, shape, p) }); err != nil {
		t.Fatal(err)
	}
}

// The same scripted movement and teleport contract runs against both official
// profiles and Go: complete, independently decoded destination windows, matching
// centers, exact batch counts and eventual removal of a distant prior window.
// Vanilla's measured center/forget/batch ordering is recorded as evidence;
// batch segmentation and the existing Go near-window prune margin may differ.
func TestLiveWindowMovementTeleportWire(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) { runLiveWindowMovement(t, requireMcGoOnly(t), number) })
	}
}
func TestLiveWindowMovementTeleportVanilla(t *testing.T) {
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
					t.Skipf("protocol 775 oracle unavailable: %v", err)
				}
			}
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			name := fmt.Sprintf("LiveWindow%d", number)
			addr, stop := startVanillaCfgFiles(t, jar, port, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor(name)})
			defer stop()
			runLiveWindowMovement(t, addr, number)
		})
	}
}
func runLiveWindowMovement(t *testing.T, addr string, number int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: number, Username: fmt.Sprintf("LiveWindow%d", number), Timeout: 90 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	shape := liveWindowShape(t, number)
	initial := c.InitialPackets()
	center, err := liveWindowCenter(initial)
	if err != nil {
		t.Fatal(err)
	}
	w := &liveWindowTrace{}
	if err := readLiveWindow(ctx, c, shape, w, center, initial, nil); err != nil {
		t.Fatal(err)
	}
	liveWindowFence(t, ctx, c, shape, w, "live-initial-fence")
	stages := []struct {
		name, command string
		center        [2]int32
		newColumns    int
		move          bool
	}{
		{"cold teleport", "tp @s 335.5 71 328.5", [2]int32{20, 20}, 49, false},
		{"one-column movement", "", [2]int32{21, 20}, 7, true},
		{"negative teleport", "tp @s -311.5 71 -311.5", [2]int32{-20, -20}, 49, false},
	}
	for _, stage := range stages {
		beforeChunks, beforeForgets := w.chunks, w.forgets
		w.events = nil
		if stage.move {
			err = c.Move(336.5, 71, 328.5, 0, 0, false)
		} else {
			err = c.Command(stage.command)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := readLiveWindow(ctx, c, shape, w, stage.center, nil, nil); err != nil {
			t.Fatalf("%s: %v", stage.name, err)
		}
		liveWindowFence(t, ctx, c, shape, w, "live-"+stage.name+"-fence")
		if got := w.chunks - beforeChunks; got != stage.newColumns {
			t.Fatalf("%s: %d new columns, want %d", stage.name, got, stage.newColumns)
		}
		if !stage.move && len(w.loaded) != 49 {
			t.Fatalf("%s retained %d columns after far prune", stage.name, len(w.loaded))
		}
		t.Logf("protocol %d %s: %d new columns, %d forgets, events=%v", number, stage.name, w.chunks-beforeChunks, w.forgets-beforeForgets, w.events)
	}
}

// A cold live teleport must leave ownership available for an already admitted
// observer even while ticks are frozen. The edit/fence starts after the traveler
// receives its destination sync and finishes before all destination chunks.
// Deterministic scheduler tests establish unit overlap; these observations
// include transport and client scheduling and assert no latency threshold.
func TestLiveWindowObserverProgressAndCanonicalLight(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			addr := requireMcGoOnly(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			open := func(protocol int32, name string) *conformance.PlayClient {
				t.Helper()
				c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: protocol, Username: name, Timeout: 90 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { c.Close() })
				return c
			}
			observer := open(1551-number, "LiveObserver")
			traveler := open(number, "LiveTraveler")
			shape := liveWindowShape(t, number)
			var trace *liveWindowTrace
			for _, c := range []*conformance.PlayClient{observer, traveler} {
				sh := liveWindowShape(t, c.Protocol())
				w := &liveWindowTrace{}
				center, err := liveWindowCenter(c.InitialPackets())
				if err != nil {
					t.Fatal(err)
				}
				if err := readLiveWindow(ctx, c, sh, w, center, c.InitialPackets(), nil); err != nil {
					t.Fatal(err)
				}
				if c == traveler {
					trace = w
				}
			}
			mixedBarrier(t, observer, "tick freeze")
			defer observer.Command("tick unfreeze")
			synced := make(chan struct{}, 1)
			type ready struct {
				at  time.Time
				err error
			}
			finished := make(chan ready, 1)
			go func() {
				err := readLiveWindow(ctx, traveler, shape, trace, [2]int32{20, 20}, nil, func(p conformance.Packet) {
					if p.ID == 0x48 {
						select {
						case synced <- struct{}{}:
						default:
						}
					}
				})
				finished <- ready{time.Now(), err}
			}()
			if err := observer.Command("tp LiveTraveler 328.5 71 328.5"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-synced:
			case out := <-finished:
				t.Fatalf("destination finished before sync: %v", out.err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			editStart := time.Now()
			if err := observer.Command("setblock 328 40 328 minecraft:stone"); err != nil {
				t.Fatal(err)
			}
			if err := coldJoinToken(ctx, observer, "live-window-edit-fence", nil); err != nil {
				t.Fatal(err)
			}
			editEnd := time.Now()
			if err := observer.Command("setworldspawn 328 71 328"); err != nil {
				t.Fatal(err)
			}
			if err := coldJoinToken(ctx, observer, "live-spawn-fence", nil); err != nil {
				t.Fatal(err)
			}
			out := <-finished
			if out.err != nil {
				t.Fatal(out.err)
			}
			if !editEnd.Before(out.at) {
				t.Fatalf("observer edit/fence did not finish before destination readiness (start=%s end=%s ready=%s)", editStart, editEnd, out.at)
			}
			body := trace.loaded[[2]int32{20, 20}]
			chunk, err := conformance.DecodeChunk(number, body, shape)
			if err != nil {
				t.Fatal(err)
			}
			if got := chunk.Blocks[(40+64)*256+8*16+8]; got != 1 {
				t.Fatalf("destination stone wire state=%d, want independently measured state 1", got)
			}
			fresh := open(number, "LiveFresh")
			freshTrace := &liveWindowTrace{}
			if err := readLiveWindow(ctx, fresh, shape, freshTrace, [2]int32{20, 20}, fresh.InitialPackets(), nil); err != nil {
				t.Fatal(err)
			}
			columnPreparationEqual(t, "live destination vs fresh snapshot", body, freshTrace.loaded[[2]int32{20, 20}])
			view := parseChunkView(t, "live destination", body)
			for _, sample := range []struct {
				x, y, z int
				want    byte
			}{{8, 39, 8, 14}, {8, 40, 8, 0}, {8, 41, 8, 15}, {9, 39, 8, 15}} {
				i := (sample.y&15)*256 + sample.z*16 + sample.x
				arr := view.sky[(sample.y+64)/16]
				if len(arr) != 2048 {
					t.Fatal("missing sky layer")
				}
				got := arr[i/2] >> uint(i%2*4) & 15
				if got != sample.want {
					t.Fatalf("sky sample (%d,%d,%d)=%d want%d", sample.x, sample.y, sample.z, got, sample.want)
				}
			}
			t.Logf("protocol %d observer edit/fence completed %s before full 49-column destination; final block, heightmaps, section bytes and all light sections match fresh snapshot", number, out.at.Sub(editEnd))
		})
	}
}

// A new dimension invalidates an unfinished live destination. Already admitted
// FIFO packets may precede Respawn; every chunk after Respawn must use the new
// dimension shape and destination. The return transition also resets the same
// warm overworld coordinates without reusing prior client tracking state.
func TestLiveWindowRetargetAcrossDimensionWire(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			addr := requireMcGoOnly(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: number, Username: "LiveDimension", Timeout: 90 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			shape := liveWindowShape(t, number)
			w := &liveWindowTrace{}
			center, err := liveWindowCenter(c.InitialPackets())
			if err != nil {
				t.Fatal(err)
			}
			if err := readLiveWindow(ctx, c, shape, w, center, c.InitialPackets(), nil); err != nil {
				t.Fatal(err)
			}
			liveWindowFence(t, ctx, c, shape, w, "dimension-initial-fence")
			if err := c.Command("tp @s 328.5 71 328.5"); err != nil {
				t.Fatal(err)
			}
			for {
				p, err := c.ReadPacket(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := w.apply(c, shape, p); err != nil {
					t.Fatal(err)
				}
				if p.ID == 0x48 {
					break
				}
			}
			// This second action is submitted as soon as the first destination sync is
			// observed; the eventual window must follow the dimension reset.
			for _, stage := range []struct {
				command  string
				center   [2]int32
				sections int
			}{
				{"execute in minecraft:the_nether run tp @s -631.5 71 -631.5", [2]int32{-40, -40}, 16},
				{"execute in minecraft:overworld run tp @s 8.5 71 8.5", [2]int32{0, 0}, 24},
			} {
				if err := c.Command(stage.command); err != nil {
					t.Fatal(err)
				}
				for {
					p, err := c.ReadPacket(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if p.ID == 0x52 {
						if w.open {
							t.Fatal("Respawn split a chunk batch")
						}
						break
					}
					if err := w.apply(c, shape, p); err != nil {
						t.Fatal(err)
					}
				}
				shape.Sections = stage.sections
				w = &liveWindowTrace{}
				if err := readLiveWindow(ctx, c, shape, w, stage.center, nil, nil); err != nil {
					t.Fatal(err)
				}
				liveWindowFence(t, ctx, c, shape, w, fmt.Sprintf("dimension-%d-fence", stage.sections))
				if len(w.loaded) != 49 {
					t.Fatalf("dimension sections %d: %d loaded columns, want49", stage.sections, len(w.loaded))
				}
				for key := range w.loaded {
					if key[0] < stage.center[0]-3 || key[0] > stage.center[0]+3 || key[1] < stage.center[1]-3 || key[1] > stage.center[1]+3 {
						t.Fatalf("stale generation column %v after Respawn at %v", key, stage.center)
					}
				}
			}
			t.Logf("protocol %d: no stale dimension/target chunk after either Respawn; all 49 nether and overworld chunks independently decoded", number)
		})
	}
}
