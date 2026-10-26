package blackbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
)

type liveWindowPhase struct {
	Name    string          `json:"name"`
	StartNS int64           `json:"start_ns"`
	ReadyNS int64           `json:"ready_ns"`
	Probes  []coldJoinProbe `json:"probes"`
}

// TestLiveWindowResponsiveness is the identical baseline/after public TCP
// probe for an already joined player's distant live teleport. The shared flat
// world/operator fixture and minimum client radius give 49 columns. Readiness
// requires all independently decoded unique columns and the final acknowledged
// batch; no sleep, quiet socket or latency threshold establishes correctness.
// MCGO_LIVE_WINDOW_REPORT appends raw relative timestamps as one NDJSON record.
// Observers have one outstanding token each, paced 20 ms after each response.
// Transport, ticks, decoding and client scheduling are included in observations;
// these timings establish no latency bound, exact preparation overlap or capacity.
func TestLiveWindowResponsiveness(t *testing.T) {
	raw, err := os.ReadFile("../../server/testdata/external-world/facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var measured struct{ Profiles map[string]coldJoinFacts }
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	addr := requireMcGoOnly(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	epoch := time.Now()
	runID := fmt.Sprintf("%x", epoch.UnixNano())
	var observers []*conformance.PlayClient
	open := func(protocol int32, suffix string) *conformance.PlayClient {
		t.Helper()
		c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: protocol,
			Username: fmt.Sprintf("LW%x%s", epoch.UnixNano()%1000000000, suffix), Timeout: 90 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		f := measured.Profiles[fmt.Sprint(protocol)]
		columnPreparationWindow(t, ctx, c, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}, "initial ready", 0, c.InitialPackets(), false)
		return c
	}
	for _, protocol := range []int32{775, 776} {
		observers = append(observers, open(protocol, fmt.Sprint(protocol)))
	}
	mover := open(776, "mover")
	f := measured.Profiles["776"]
	shape := conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}
	var phases []liveWindowPhase
	var coldBody []byte
	for _, phaseName := range []string{"cold", "warm"} {
		if phaseName == "warm" {
			columnPreparationWindow(t, ctx, mover, shape, "unmeasured warm return", 0, nil, true)
		}
		phase := liveWindowPhase{Name: phaseName, StartNS: time.Since(epoch).Nanoseconds()}
		stop := make(chan struct{})
		var once sync.Once
		stopProbes := func() { once.Do(func() { close(stop) }) }
		defer stopProbes()
		type observation struct {
			probes []coldJoinProbe
			err    error
		}
		results := make(chan observation, len(observers))
		for index, observer := range observers {
			go func(index int, c *conformance.PlayClient) {
				out := observation{}
				defer func() { results <- out }()
				for sequence := 0; ; sequence++ {
					// Complete at least one request, preserving timestamps even
					// when a fast warm phase ends before this goroutine runs.
					if sequence != 0 {
						select {
						case <-stop:
							return
						default:
						}
					}
					probe := coldJoinProbe{Protocol: c.Protocol(), Kind: "token", StartNS: time.Since(epoch).Nanoseconds()}
					token := fmt.Sprintf("live-window-%s-%s-%d-%d", runID, phaseName, index, sequence)
					if out.err = coldJoinToken(ctx, c, token, nil); out.err != nil {
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
		// Destination 100 (1608.5,71,1608.5) and its halo are untouched
		// by all joins. The second visit is the otherwise identical control.
		body := columnPreparationWindow(t, ctx, mover, shape, phaseName+" live window", 100, nil, true)
		phase.ReadyNS = time.Since(epoch).Nanoseconds()
		stopProbes()
		for range observers {
			out := <-results
			if out.err != nil {
				t.Fatal(out.err)
			}
			phase.Probes = append(phase.Probes, out.probes...)
		}
		if phaseName == "cold" {
			coldBody = body
		} else {
			columnPreparationEqual(t, "warm live window", coldBody, body)
		}
		sort.Slice(phase.Probes, func(i, j int) bool { return phase.Probes[i].StartNS < phase.Probes[j].StartNS })
		for _, protocol := range []int32{775, 776} {
			var durations []int64
			beforeReady := 0
			for _, p := range phase.Probes {
				if p.Protocol != protocol {
					continue
				}
				durations = append(durations, p.EndNS-p.StartNS)
				if p.EndNS <= phase.ReadyNS {
					beforeReady++
				}
			}
			if len(durations) == 0 {
				t.Fatalf("%s protocol %d recorded no response", phaseName, protocol)
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			t.Logf("%s protocol %d: tokens=%d completed-before-ready=%d p50=%v p95=%v max=%v; complete49 readiness=%v", phaseName, protocol, len(durations), beforeReady,
				time.Duration(durations[(len(durations)-1)/2]), time.Duration(durations[(95*len(durations)+99)/100-1]), time.Duration(durations[len(durations)-1]), time.Duration(phase.ReadyNS-phase.StartNS))
		}
		phases = append(phases, phase)
	}
	if path := os.Getenv("MCGO_LIVE_WINDOW_REPORT"); path != "" {
		record := struct {
			RunID       string            `json:"run_id"`
			EpochUnixNS int64             `json:"epoch_unix_ns"`
			Phases      []liveWindowPhase `json:"phases"`
		}{runID, epoch.UnixNano(), phases}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewEncoder(file).Encode(record)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("write load report: %v/%v", err, closeErr)
		}
	}
}
