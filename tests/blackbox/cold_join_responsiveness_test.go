package blackbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
)

type coldJoinFacts struct {
	BlockStates int `json:"block_states"`
	Biomes      int
	States      map[string]int32
	Packets     map[string]int32
}

type coldJoinProbe struct {
	Protocol int32  `json:"protocol"`
	Kind     string `json:"kind"`
	StartNS  int64  `json:"start_ns"`
	EndNS    int64  `json:"end_ns"`
}

type coldJoinPhase struct {
	Name        string          `json:"name"`
	StartNS     int64           `json:"start_ns"`
	ReadyNS     int64           `json:"ready_ns"`
	EditStartNS int64           `json:"edit_start_ns,omitempty"`
	EditEndNS   int64           `json:"edit_end_ns,omitempty"`
	Probes      []coldJoinProbe `json:"probes"`
}

// TestColdJoinResponsiveness is also the unchanged baseline/after load probe.
// It uses the shared flat-overworld/operator test configuration and public TCP
// clients. MCGO_TARGET can select another endpoint with those same prerequisites.
// MCGO_COLD_JOIN_REPORT appends one complete NDJSON record per invocation.
// Timings include transport, command handling, ticks and client scheduling.
// They are observations, not a fixed latency SLO or proof of preparation overlap;
// deterministic scheduler tests establish that separate property.
func TestColdJoinResponsiveness(t *testing.T) {
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
	name := func(suffix string) string { return fmt.Sprintf("CJ%x%s", epoch.UnixNano()%1000000000, suffix) }
	var observers []*conformance.PlayClient
	for _, protocol := range []int32{775, 776} {
		c, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: protocol, Username: name(fmt.Sprint(protocol)), Timeout: 90 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		observers = append(observers, c)
		f := measured.Profiles[fmt.Sprint(protocol)]
		columnPreparationWindow(t, ctx, c, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}, "observer ready", 0, c.InitialPackets(), false)
	}
	// A new player at the old spawn would reuse the observers' warm terrain.
	// This command's semantics are pinned by TestSpawnCommandParity.
	mixedBarrier(t, observers[0], "setworldspawn 328 71 328")

	var phases []coldJoinPhase
	var warmBody []byte
	for _, phaseName := range []string{"cold", "warm"} {
		phase := coldJoinPhase{Name: phaseName, StartNS: time.Since(epoch).Nanoseconds()}
		stop := make(chan struct{})
		var once sync.Once
		stopProbes := func() { once.Do(func() { close(stop) }) }
		defer stopProbes()
		type observation struct {
			probes             []coldJoinProbe
			editStart, editEnd int64
			err                error
		}
		results := make(chan observation, len(observers))
		for index, observer := range observers {
			go func(index int, c *conformance.PlayClient) {
				out := observation{}
				defer func() { results <- out }()
				for sequence := 0; ; sequence++ {
					// Always complete one response, even if a very fast
					// warm join finishes before this goroutine is scheduled.
					// Its raw timestamps reveal whether it overlapped.
					if sequence != 0 {
						select {
						case <-stop:
							return
						default:
						}
					}
					probe := coldJoinProbe{Protocol: c.Protocol(), Kind: "token", StartNS: time.Since(epoch).Nanoseconds()}
					// The edit's ordering relative to server preparation is
					// intentionally unconstrained. Materialize either legal
					// chunk-before-delta or edit-before-snapshot ordering.
					if phaseName == "cold" && index == 0 && sequence == 0 {
						probe.Kind = "edit_and_token"
						out.editStart = time.Since(epoch).Nanoseconds()
						if out.err = c.Command("setblock 328 40 328 minecraft:stone"); out.err != nil {
							return
						}
					}
					token := fmt.Sprintf("cold-join-%s-%s-%d-%d", runID, phaseName, index, sequence)
					if out.err = coldJoinToken(ctx, c, token, nil); out.err != nil {
						return
					}
					probe.EndNS = time.Since(epoch).Nanoseconds()
					out.probes = append(out.probes, probe)
					if phaseName == "cold" && index == 0 && sequence == 0 {
						out.editEnd = probe.EndNS
					}
					// Pacing controls the offered probe load; no assertion
					// depends on this delay or a quiet socket.
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
		joined, err := conformance.OpenPlay(ctx, conformance.Config{Endpoint: addr, Protocol: 776, Username: name(phaseName), Timeout: 90 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer joined.Close()
		f := measured.Profiles["776"]
		cell := &coldJoinCell{facts: f}
		body := coldJoinWindow(t, ctx, joined, cell)
		phase.ReadyNS = time.Since(epoch).Nanoseconds()
		stopProbes()
		for range observers {
			out := <-results
			if out.err != nil {
				t.Fatal(out.err)
			}
			phase.Probes = append(phase.Probes, out.probes...)
			if out.editEnd != 0 {
				phase.EditStartNS, phase.EditEndNS = out.editStart, out.editEnd
			}
		}
		// Explicitly flush deferred block/light deltas before the final
		// FIFO marker, using the measured tick commands rather than sleep.
		for _, command := range []string{"tick freeze", "tick step 1", "tick unfreeze"} {
			if err := joined.Command(command); err != nil {
				t.Fatal(err)
			}
		}
		if err := coldJoinToken(ctx, joined, "cold-join-final-"+runID+phaseName, cell.apply); err != nil {
			t.Fatal(err)
		}
		if !cell.seen || cell.state != f.States["minecraft:stone"] {
			t.Fatalf("%s joined viewer final state=%d seen=%v, want measured stone=%d", phaseName, cell.state, cell.seen, f.States["minecraft:stone"])
		}
		if phaseName == "cold" && phase.EditEndNS == 0 {
			t.Fatal("cold phase never completed the edit/owner fence")
		}
		if phaseName == "warm" {
			warmBody = body
			coldJoinSky(t, body)
		}
		joined.Close()
		sort.Slice(phase.Probes, func(i, j int) bool { return phase.Probes[i].StartNS < phase.Probes[j].StartNS })
		for _, protocol := range []int32{775, 776} {
			var durations []int64
			beforeReady, responses := 0, 0
			for _, probe := range phase.Probes {
				if probe.Protocol == protocol {
					responses++
					if probe.Kind == "token" {
						durations = append(durations, probe.EndNS-probe.StartNS)
					}
					if probe.EndNS <= phase.ReadyNS {
						beforeReady++
					}
				}
			}
			if responses == 0 {
				t.Fatalf("%s protocol %d recorded no owner response", phaseName, protocol)
			}
			if len(durations) == 0 {
				t.Logf("%s protocol %d: responses=%d completed-before-ready=%d; no token-only sample; complete49 readiness=%v", phaseName, protocol, responses, beforeReady, time.Duration(phase.ReadyNS-phase.StartNS))
				continue
			}
			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			t.Logf("%s protocol %d: responses=%d token-only=%d completed-before-ready=%d token-p50=%v token-p95=%v token-max=%v; complete49 readiness=%v", phaseName, protocol, responses, len(durations), beforeReady, time.Duration(durations[(len(durations)-1)/2]), time.Duration(durations[(95*len(durations)+99)/100-1]), time.Duration(durations[len(durations)-1]), time.Duration(phase.ReadyNS-phase.StartNS))
		}
		phases = append(phases, phase)
	}
	// Once measurement ends, both existing viewers revisit the prepared
	// terrain and validate its canonical edited light and block contents.
	// This does not assert live-teleport fairness.
	for _, observer := range observers {
		f := measured.Profiles[fmt.Sprint(observer.Protocol())]
		body := columnPreparationWindow(t, ctx, observer, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}, "existing viewer final terrain", 20, nil, true)
		coldJoinSky(t, body)
		if observer.Protocol() == 776 {
			columnPreparationEqual(t, "existing viewer final terrain", warmBody, body)
		}
		chunk, err := conformance.DecodeChunk(observer.Protocol(), body, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes})
		if err != nil || chunk.Blocks[(40+64)*256+8*16+8] != f.States["minecraft:stone"] {
			t.Fatalf("existing protocol %d viewer lost edited stone: %v", observer.Protocol(), err)
		}
	}
	if path := os.Getenv("MCGO_COLD_JOIN_REPORT"); path != "" {
		record := struct {
			RunID  string          `json:"run_id"`
			Phases []coldJoinPhase `json:"phases"`
		}{runID, phases}
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

func coldJoinToken(ctx context.Context, c *conformance.PlayClient, token string, apply func(conformance.Packet) error) error {
	if err := c.Command("tellraw @s \"" + token + "\""); err != nil {
		return err
	}
	var applyErr error
	_, err := c.WaitPacket(ctx, func(p conformance.Packet) bool {
		if apply != nil {
			if e := apply(p); e != nil {
				applyErr = e
				return true
			}
		}
		return p.ID == 0x79 && bytes.Contains(p.Data, []byte(token))
	})
	if applyErr != nil {
		return applyErr
	}
	return err
}

type coldJoinCell struct {
	facts coldJoinFacts
	state int32
	seen  bool
}

func (c *coldJoinCell) apply(p conformance.Packet) error {
	r := bytes.NewReader(p.Data)
	switch p.ID {
	case c.facts.Packets["minecraft:level_chunk_with_light"]:
		chunk, err := conformance.DecodeChunk(776, p.Data, conformance.ChunkShape{Sections: 24, BlockStates: c.facts.BlockStates, Biomes: c.facts.Biomes})
		if err != nil {
			return err
		}
		if chunk.X == 20 && chunk.Z == 20 {
			c.state, c.seen = chunk.Blocks[(40+64)*256+8*16+8], true
		}
	case c.facts.Packets["minecraft:block_update"]:
		var position uint64
		if err := binary.Read(r, binary.BigEndian, &position); err != nil {
			return err
		}
		state, err := mixedReadVI(r)
		if err != nil || r.Len() != 0 {
			return fmt.Errorf("block update framing: %v", err)
		}
		if position == mixedPos(328, 40, 328) {
			c.state, c.seen = state, true
		}
	case c.facts.Packets["minecraft:section_blocks_update"]:
		var section uint64
		if err := binary.Read(r, binary.BigEndian, &section); err != nil {
			return err
		}
		n, err := mixedReadVI(r)
		if err != nil || n < 0 || n > 4096 {
			return fmt.Errorf("section update count: %d/%v", n, err)
		}
		for range n {
			var value uint64
			for shift := 0; ; shift += 7 {
				b, err := r.ReadByte()
				if err != nil || shift > 63 || (shift == 63 && b > 1) {
					return fmt.Errorf("section update VarLong: %v", err)
				}
				value |= uint64(b&127) << shift
				if b < 128 {
					break
				}
			}
			if section == uint64(20)<<42|uint64(20)<<20|2 && value&4095 == 8<<8|8<<4|8 {
				c.state, c.seen = int32(value>>12), true
			}
		}
		if r.Len() != 0 {
			return fmt.Errorf("trailing section update bytes")
		}
	}
	return nil
}

func coldJoinWindow(t *testing.T, ctx context.Context, c *conformance.PlayClient, cell *coldJoinCell) []byte {
	t.Helper()
	initial := c.InitialPackets()
	columns := map[[2]int32]bool{}
	openingDone, attributes := false, false
	var body []byte
	for {
		var p conformance.Packet
		if len(initial) != 0 {
			p, initial = initial[0], initial[1:]
		} else {
			var err error
			p, err = c.ReadPacket(ctx)
			if err != nil {
				t.Fatalf("cold join readiness: %d/49 columns: %v", len(columns), err)
			}
		}
		if err := cell.apply(p); err != nil {
			t.Fatal(err)
		}
		switch p.ID {
		case 0x2d:
			key := [2]int32{int32(binary.BigEndian.Uint32(p.Data)), int32(binary.BigEndian.Uint32(p.Data[4:]))}
			if key[0] < 17 || key[0] > 23 || key[1] < 17 || key[1] > 23 || columns[key] {
				t.Fatalf("unexpected or duplicate cold join column %v", key)
			}
			columns[key] = true
			if key == [2]int32{20, 20} {
				body = p.Data
			}
		case 0x83:
			id, err := mixedReadVI(bytes.NewReader(p.Data))
			if err == nil && id == c.EntityID {
				if !openingDone {
					t.Fatal("player attributes overtook opening chunk batch")
				}
				attributes = true
			}
		case 0x0b:
			if !openingDone {
				count, err := mixedReadVI(bytes.NewReader(p.Data))
				if err != nil || count != 9 || len(columns) != 9 {
					t.Fatalf("opening batch count=%d columns=%d: %v", count, len(columns), err)
				}
				openingDone = true
			}
			if len(columns) == 49 && attributes && body != nil {
				return body
			}
		}
	}
}

func coldJoinSky(t *testing.T, body []byte) {
	t.Helper()
	v := parseChunkView(t, "cold join final terrain", body)
	// Existing official single-opaque-block observations, as exercised by
	// TestSkyPropParity/TestColumnPreparationColdWarmWire.
	for _, sample := range []struct {
		y    int
		want byte
	}{{39, 14}, {40, 0}, {41, 15}} {
		index := (sample.y&15)*256 + 8*16 + 8
		got := v.sky[(sample.y+64)/16][index/2] >> uint(index%2*4) & 15
		if got != sample.want {
			t.Fatalf("final edited sky at (328,%d,328)=%d, want measured %d", sample.y, got, sample.want)
		}
	}
}
