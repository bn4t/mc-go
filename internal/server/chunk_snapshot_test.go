package server

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"mc-go/api/registry"
	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func chunkSnapshotFixture(t testing.TB) (*dimension, *chunkSnapshot) {
	t.Helper()
	w := world.New(event.New(), 8)
	t.Cleanup(w.Stop)
	for y := 0; y < 32; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if (x+y+z)%3 == 0 {
					w.SetBlock(world.Pos{X: x, Y: y, Z: z}, world.Stone)
				}
			}
		}
	}
	d := testOverworld(w)
	// Isolate steady-state block capture and encoding. Initial skylight
	// materialization is deliberately a separate owner-side cost.
	d.skylight = false
	mapping, ok := world.BlockMapping(ProtocolVersion)
	if !ok {
		t.Fatal("missing built-in block mapping")
	}
	return d, captureChunkSnapshot(d, 0, 0, nil, mapping)
}

func TestChunkSnapshotDetachedFromWorldAndBlockEntities(t *testing.T) {
	d, snapshot := chunkSnapshotFixture(t)
	mapping := snapshot.mapping
	be := []clientBlockEntity{{pos: world.Pos{X: 1, Y: 0, Z: 2}, spec: blockEntitySpec{id: 7}, nbt: []byte{10, 0}}}
	snapshot = captureChunkSnapshot(d, 0, 0, be, mapping)
	before, err := encodeChunkSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.retainedBytes() > chunkSnapshotUpperBound(d, be) {
		t.Fatalf("snapshot retains %d bytes, reserved %d", snapshot.retainedBytes(), chunkSnapshotUpperBound(d, be))
	}
	be[0].nbt[0] = 99
	be[0].spec.id = 123
	d.w.SetBlock(world.Pos{X: 0, Y: 0, Z: 0}, world.Dirt)
	d.biome = "changed after capture"
	d.height = 16
	after, err := encodeChunkSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("live state changed a captured packet")
	}
}

func TestChunkSnapshotCanEncodeConcurrentlyWithWorldMutation(t *testing.T) {
	d, snapshot := chunkSnapshotFixture(t)
	want, err := encodeChunkSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 4)
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 8; i++ {
				got, err := encodeChunkSnapshot(snapshot)
				if err != nil {
					failures <- err
					return
				}
				if !bytes.Equal(got, want) {
					failures <- errSnapshotChanged
					return
				}
			}
		}()
	}
	for i := 0; i < 64; i++ {
		d.w.SetBlock(world.Pos{X: i % 16, Y: 1, Z: (i / 16) % 16}, world.Dirt)
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

var errSnapshotChanged = errors.New("immutable snapshot produced different bytes")

var benchmarkCapturedChunk *chunkSnapshot

func BenchmarkChunkSnapshot(b *testing.B) {
	d, snapshot := chunkSnapshotFixture(b)
	mapping := snapshot.mapping
	body, err := encodeChunkSnapshot(snapshot)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("Capture", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			benchmarkCapturedChunk = captureChunkSnapshot(d, 0, 0, nil, mapping)
		}
	})
	b.Run("Encode", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(body)), "packet-bytes")
		for i := 0; i < b.N; i++ {
			var err error
			benchmarkChunkBytes, err = encodeChunkSnapshot(snapshot)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("CaptureAndSubmit", func(b *testing.B) {
		pool := newChunkEncodingPool(1, 1, chunkSnapshotUpperBound(d, nil))
		defer pool.Stop()
		b.ReportAllocs()
		b.StopTimer()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Completion and worker retirement are excluded from the owner
			// admission timing; total CPU/client latency are separate costs.
			for {
				pool.mu.Lock()
				empty := pool.jobs == 0
				pool.mu.Unlock()
				if empty {
					break
				}
				runtime.Gosched()
			}
			b.StartTimer()
			reservation, ok := pool.tryReserve(1, chunkSnapshotUpperBound(d, nil))
			if !ok {
				b.Fatal("idle encoder refused reservation")
			}
			captured := captureChunkSnapshot(d, 0, 0, nil, mapping)
			future := reservation.Submit(captured.retainedBytes(), func() ([]byte, error) { return encodeChunkSnapshot(captured) })
			reservation.Close()
			b.StopTimer()
			if _, err := future.wait(nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Synchronous", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(len(body)), "packet-bytes")
		for i := 0; i < b.N; i++ {
			var err error
			benchmarkChunkBytes, err = encodeChunkSnapshot(captureChunkSnapshot(d, 0, 0, nil, mapping))
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestChunkSnapshotReservationCoversDenseLightAndLargeNBT(t *testing.T) {
	d, snapshot := chunkSnapshotFixture(t)
	d.skylight = true
	key := [2]int{0, 0}
	d.skyGen = map[[2]int]bool{key: true}
	d.skyLayers = map[[2]int]map[int]*[4096]byte{key: {}}
	d.blockLight = map[world.Pos]uint8{}
	for section := 0; section < d.sections(); section++ {
		layer := &[4096]byte{}
		for i := range layer {
			layer[i] = 15
		}
		d.skyLayers[key][section] = layer
		d.blockLight[world.Pos{X: 0, Y: d.minY + section*16, Z: 0}] = 15
	}
	entities := []clientBlockEntity{{spec: blockEntitySpec{id: 7}, nbt: make([]byte, 100001)}}
	snapshot = captureChunkSnapshot(d, 0, 0, entities, snapshot.mapping)
	if len(snapshot.light) < d.sections()*4096 {
		t.Fatal("fixture did not materialize every sky/block light array")
	}
	if actual, reserved := snapshot.retainedBytes(), chunkSnapshotUpperBound(d, entities); actual > reserved {
		t.Fatalf("dense snapshot retains %d bytes, reserved %d", actual, reserved)
	}
}

func TestChunkSnapshotUsesTargetMappingAndRejectsMissingStates(t *testing.T) {
	_, snapshot := chunkSnapshotFixture(t)
	mapping, ok := world.BlockMapping(775)
	if !ok {
		t.Fatal("missing measured protocol775 block table")
	}
	var canonical world.Block
	var wire int32
	for state := world.Block(0); int(state) < world.BlockCatalog().Len(); state++ {
		if target, exists := mapping.Lookup(state); exists && target != int32(state) {
			canonical, wire = state, target
			break
		}
	}
	if canonical == 0 {
		t.Fatal("fixture found no renumbered measured state")
	}
	snapshot.mapping, snapshot.height = mapping, 16
	snapshot.states = snapshot.states[:4096]
	for i := range snapshot.states {
		snapshot.states[i] = canonical
	}
	body, err := encodeChunkSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.NewPacketReader(body)
	_, _ = r.Int()
	_, _ = r.Int()
	maps, _ := r.VarInt()
	for i := int32(0); i < maps; i++ {
		_, _ = r.VarInt()
		longs, _ := r.VarInt()
		if _, err := r.Read(int(longs) * 8); err != nil {
			t.Fatal(err)
		}
	}
	length, _ := r.VarInt()
	data, err := r.Read(int(length))
	if err != nil {
		t.Fatal(err)
	}
	section := protocol.NewPacketReader(data)
	_, _ = section.Short()
	_, _ = section.Short()
	if values := decodeContainer(t, section, 4096, 4); values[0] != wire {
		t.Fatalf("chunk used canonical handle %d rather than target wire ID %d: %d", canonical, wire, values[0])
	}
	snapshot.states[0] = mapping.Missing()[0]
	if _, err := encodeChunkSnapshot(snapshot); !errors.Is(err, registry.ErrMissingBlockState) {
		t.Fatalf("unrepresentable state error = %v", err)
	}
}

func TestChunkSnapshotRejectsForeignCatalogMapping(t *testing.T) {
	_, snapshot := chunkSnapshotFixture(t)
	state, _ := registry.NewStateKey("example:foreign", nil)
	catalog, err := registry.NewBlockCatalog([]registry.BlockDefinition{{State: state, Default: true}})
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := registry.NewBlockMapping(999, catalog, []registry.BlockWireEntry{{State: state, WireID: 7}}, registry.MissingBlockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.mapping = mapping
	if _, err := encodeChunkSnapshot(snapshot); err == nil {
		t.Fatal("foreign catalog handles were interpreted as world states")
	}
}

// Captured from the immutable pre-pipeline encoder at repository commit
// 6e28441, using this exact no-skylight fixture and sign entry. This guard is
// independent of the new wrapper/capture implementation; actual vanilla
// differential tests remain the authority for protocol behavior.
func TestChunkSnapshotHistorical776BodyGolden(t *testing.T) {
	d, snapshot := chunkSnapshotFixture(t)
	entities := []clientBlockEntity{{pos: world.Pos{X: 1, Y: 0, Z: 2}, spec: blockEntitySpec{id: 7}, nbt: []byte{10, 0}}}
	body, err := encodeChunkSnapshot(captureChunkSnapshot(d, 0, 0, entities, snapshot.mapping))
	if err != nil {
		t.Fatal(err)
	}
	const want = "0849485fb89d42cc92a5fa5835d9f5dc0db43de8f5b0cb9fb401fd48c6bdcd55"
	if got := fmt.Sprintf("%x", sha256.Sum256(body)); len(body) != 7278 || got != want {
		t.Fatalf("historical packet changed: bytes=%d SHA256=%s", len(body), got)
	}
}
