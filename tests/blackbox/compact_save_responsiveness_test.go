package blackbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
	publicserver "mc-go/server"
)

type compactSaveProbeEntry struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	Z     int    `json:"z"`
	State string `json:"state"`
}

type compactSaveProbeFile struct {
	Version int                     `json:"version"`
	Blocks  []compactSaveProbeEntry `json:"blocks"`
}

type compactSaveProbeArtifact struct {
	Dimension string `json:"dimension"`
	Entries   int    `json:"entries"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
}

// TestCompactSaveResponsiveness is an unchanged baseline/after probe using
// public server.New/SaveWorld and real TCP clients. A fixed canonical-v2 fixture
// contains 262,144 far-away overrides: 131,072 overworld, 65,536 nether and
// 65,536 end. Bootstrap, loading, complete49 observer readiness and token fences
// finish before measurement. Each observer keeps one token outstanding, pacing
// 20 ms after responses. Start/end timestamps retain samples beyond API return.
// These are whole-process observations including transport, scheduling, capture,
// serialization and filesystem effects, not owner-hold or capacity measurements.
// SaveWorld return and file validation establish completed writes, not fsync.
// MCGO_COMPACT_SAVE_REPORT appends one raw NDJSON record per invocation.
func TestCompactSaveResponsiveness(t *testing.T) {
	if os.Getenv(targetProp) != "" {
		t.Skip("public SaveWorld probe needs its own embedded public runtime")
	}
	raw, err := os.ReadFile("../../server/testdata/external-world/facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var measured struct{ Profiles map[string]coldJoinFacts }
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "world.json")
	var seeds []compactSaveProbeArtifact
	for _, dim := range compactSaveProbeDimensions() {
		entries := make([]compactSaveProbeEntry, dim.count)
		for i := range entries {
			// Deliberately reverse the seed order; saved files must use the
			// existing canonical x/y/z order, independently of fixture order.
			entries[i] = compactSaveProbeExpected(dim.count - 1 - i)
		}
		data, err := json.Marshal(compactSaveProbeFile{Version: 2, Blocks: entries})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+dim.suffix, data, 0644); err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, compactSaveProbeArtifact{Dimension: dim.name, Entries: dim.count, Bytes: len(data), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback bind unavailable: %v", err)
	}
	s := publicserver.New(ln.Addr().String())
	s.WorldPath = path
	s.ViewDistance, s.SimulationDistance = 2, 2
	t.Cleanup(func() { _ = ln.Close(); s.Stop() })
	if err := s.InitPlugins(); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ln) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	runID := fmt.Sprintf("%x", time.Now().UnixNano())
	var observers []*conformance.PlayClient
	for _, protocol := range []int32{775, 776} {
		c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: ln.Addr().String(), Protocol: protocol,
			Username: fmt.Sprintf("CS%x%d", time.Now().UnixNano()%1000000000, protocol), Timeout: 90 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		f := measured.Profiles[fmt.Sprint(protocol)]
		columnPreparationWindow(t, ctx, c, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}, "save observer ready", 0, c.InitialPackets(), false)
		if err := coldJoinToken(ctx, c, "compact-save-ready-"+runID+fmt.Sprint(protocol), nil); err != nil {
			t.Fatal(err)
		}
		observers = append(observers, c)
	}
	// Release fixture-construction garbage before the observed operation. The
	// counters describe this whole process (server plus clients), not SaveWorld
	// allocations in isolation, and do not bound retained snapshot memory.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	epoch := time.Now()
	stop := make(chan struct{})
	var once sync.Once
	stopProbes := func() { once.Do(func() { close(stop) }) }
	defer stopProbes()
	type observation struct {
		probes []coldJoinProbe
		err    error
	}
	results := make(chan observation, len(observers))
	startNS := time.Since(epoch).Nanoseconds()
	for index, observer := range observers {
		go func(index int, c *conformance.PlayClient) {
			out := observation{}
			defer func() { results <- out }()
			for sequence := 0; ; sequence++ {
				if sequence != 0 {
					select {
					case <-stop:
						return
					default:
					}
				}
				probe := coldJoinProbe{Protocol: c.Protocol(), Kind: "token", StartNS: time.Since(epoch).Nanoseconds()}
				if out.err = coldJoinToken(ctx, c, fmt.Sprintf("compact-save-%s-%d-%d", runID, index, sequence), nil); out.err != nil {
					return
				}
				probe.EndNS = time.Since(epoch).Nanoseconds()
				out.probes = append(out.probes, probe)
				timer := time.NewTimer(20 * time.Millisecond)
				select {
				case <-stop:
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}(index, observer)
	}
	// This public API call begins outside any simulation callback/transaction.
	callNS := time.Since(epoch).Nanoseconds()
	err = s.SaveWorld()
	completeNS := time.Since(epoch).Nanoseconds()
	stopProbes()
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	var probes []coldJoinProbe
	for range observers {
		out := <-results
		if out.err != nil {
			t.Fatal(out.err)
		}
		probes = append(probes, out.probes...)
	}
	sort.Slice(probes, func(i, j int) bool { return probes[i].StartNS < probes[j].StartNS })
	for _, protocol := range []int32{775, 776} {
		var durations []int64
		completed := 0
		for _, p := range probes {
			if p.Protocol == protocol {
				durations = append(durations, p.EndNS-p.StartNS)
				if p.EndNS <= completeNS {
					completed++
				}
			}
		}
		if len(durations) == 0 {
			t.Fatalf("protocol %d recorded no observer response", protocol)
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		t.Logf("protocol %d: tokens=%d completed-before-return=%d p50=%v p95=%v max=%v; public SaveWorld=%v", protocol, len(durations), completed,
			time.Duration(durations[(len(durations)-1)/2]), time.Duration(durations[(95*len(durations)+99)/100-1]), time.Duration(durations[len(durations)-1]), time.Duration(completeNS-callNS))
	}
	var artifacts []compactSaveProbeArtifact
	for _, dim := range compactSaveProbeDimensions() {
		data, err := os.ReadFile(path + dim.suffix)
		if err != nil {
			t.Fatal(err)
		}
		var saved compactSaveProbeFile
		if err := json.Unmarshal(data, &saved); err != nil || saved.Version != 2 || len(saved.Blocks) != dim.count {
			t.Fatalf("%s save format/count: version=%d entries=%d error=%v", dim.name, saved.Version, len(saved.Blocks), err)
		}
		seen := make([]bool, dim.count)
		for i, entry := range saved.Blocks {
			index := (entry.Z-100000)*512 + entry.X - 100000
			if index < 0 || index >= dim.count || seen[index] || entry != compactSaveProbeExpected(index) {
				t.Fatalf("%s invalid/duplicate entry %d: %+v", dim.name, i, entry)
			}
			seen[index] = true
			if i > 0 {
				prev := saved.Blocks[i-1]
				if entry.X < prev.X || entry.X == prev.X && (entry.Y < prev.Y || entry.Y == prev.Y && entry.Z <= prev.Z) {
					t.Fatalf("%s noncanonical entry order %d", dim.name, i)
				}
			}
		}
		artifacts = append(artifacts, compactSaveProbeArtifact{Dimension: dim.name, Entries: dim.count, Bytes: len(data), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
	}
	if report := os.Getenv("MCGO_COMPACT_SAVE_REPORT"); report != "" {
		record := struct {
			RunID           string                     `json:"run_id"`
			EpochUnixNS     int64                      `json:"epoch_unix_ns"`
			StartNS         int64                      `json:"start_ns"`
			CallNS          int64                      `json:"call_ns"`
			CompleteNS      int64                      `json:"complete_ns"`
			Probes          []coldJoinProbe            `json:"probes"`
			Seeds           []compactSaveProbeArtifact `json:"seeds"`
			Artifacts       []compactSaveProbeArtifact `json:"artifacts"`
			HeapBeforeBytes uint64                     `json:"process_heap_before_bytes"`
			HeapAfterBytes  uint64                     `json:"process_heap_after_bytes"`
			AllocatedBytes  uint64                     `json:"process_allocated_bytes"`
		}{runID, epoch.UnixNano(), startNS, callNS, completeNS, probes, seeds, artifacts, before.HeapAlloc, after.HeapAlloc, after.TotalAlloc - before.TotalAlloc}
		f, err := os.OpenFile(report, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewEncoder(f).Encode(record)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("write save probe report: %v/%v", err, closeErr)
		}
	}
}

func compactSaveProbeExpected(index int) compactSaveProbeEntry {
	states := [...]string{"minecraft:stone", "minecraft:dirt", "minecraft:glass", "minecraft:oak_log[axis=y]"}
	return compactSaveProbeEntry{X: 100000 + index%512, Y: 70, Z: 100000 + index/512, State: states[index%len(states)]}
}

func compactSaveProbeDimensions() []struct {
	name, suffix string
	count        int
} {
	return []struct {
		name, suffix string
		count        int
	}{{"minecraft:overworld", "", 131072}, {"minecraft:the_nether", ".the_nether", 65536}, {"minecraft:the_end", ".the_end", 65536}}
}
