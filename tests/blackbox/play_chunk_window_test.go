package blackbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/conformance"
)

// These public-runner checks require the whole acknowledged window, including
// the final batch finish, rather than treating Play Login as world readiness.
// Registry bounds are independently pinned official-report facts; dimension
// sections are the measured overworld shape, not server implementation values.
func TestPortablePlayChunkWindowProfiles(t *testing.T) {
	addr := requireMcGoOnly(t)
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) { runPortablePlayChunkWindow(t, addr, number) })
	}
}

// The same portable scenario executes against each official release, with no
// implementation internals in the runner. The factual source/probe hashes and
// observed ordering are recorded in testdata/portable-play-window-observations.json.
func TestPortablePlayChunkWindowVanilla(t *testing.T) {
	for _, number := range []int32{775, 776} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			jar := blackboxJar(t)
			if number == 775 {
				jar = os.Getenv("MCGO_VANILLA_775_JAR")
				if jar == "" {
					jar = filepath.Join("..", "..", "vanilla", "server-26.1.jar")
				}
				if _, err := os.Stat(jar); err != nil {
					t.Skipf("protocol 775 vanilla jar not found at %s: %v", jar, err)
				}
				var err error
				jar, err = filepath.Abs(jar)
				if err != nil {
					t.Fatal(err)
				}
			}
			port, ok := freePort()
			if !ok {
				t.Skip("loopback binds blocked in this environment")
			}
			addr, stop := startVanilla(t, jar, port)
			defer stop()
			runPortablePlayChunkWindow(t, addr, number)
		})
	}
}

func runPortablePlayChunkWindow(t *testing.T, addr string, number int32) {
	t.Helper()
	raw, err := os.ReadFile("../../conformance/testdata/mixed-profile-facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var facts struct {
		Profiles map[string]struct {
			BlockStates int `json:"block_states"`
			Biomes      int `json:"biomes"`
		}
	}
	if err := json.Unmarshal(raw, &facts); err != nil {
		t.Fatal(err)
	}
	f, ok := facts.Profiles[fmt.Sprint(number)]
	if !ok {
		t.Fatalf("missing official profile facts for %d", number)
	}
	report := conformance.Run(context.Background(), conformance.Config{
		Endpoint: addr, Protocol: number, Username: fmt.Sprintf("Window%d", number),
		Capabilities: []string{"play-chunks"}, Timeout: 30 * time.Second,
		Play: &conformance.PlayConfig{Shape: conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}},
	})
	assertPortableDeclaredPasses(t, report, 1)
	var result *conformance.Result
	for i := range report.Results {
		if report.Results[i].Scenario == "play.chunk-window" {
			result = &report.Results[i]
			break
		}
	}
	if result == nil {
		t.Fatal("no play.chunk-window result")
	}
	// Independently inspect scenario evidence: all 49 decoded bodies must have
	// crossed this connection and the last one must precede a completed batch.
	chunks, starts, finishes, acks, loaded, teleports := 0, 0, 0, 0, 0, 0
	lastChunk, lastFinish := -1, -1
	play := false
	for i, event := range result.Transcript {
		if event.Direction == "clientbound" && event.PacketID == 0x31 {
			play = true
		}
		if !play {
			continue
		}
		if event.Direction == "clientbound" {
			switch event.PacketID {
			case 0x2d:
				chunks++
				lastChunk = i
			case 0x0c:
				starts++
			case 0x0b:
				finishes++
				lastFinish = i
			}
		} else if event.Direction == "serverbound" {
			switch event.PacketID {
			case 0x0b:
				acks++
			case 0x2c:
				loaded++
			case 0:
				teleports++
			}
		}
	}
	if chunks != 49 || starts < 1 || starts != finishes || acks != finishes || lastFinish < lastChunk || loaded != 1 || teleports < 1 {
		t.Fatalf("incomplete window evidence: chunks=%d starts=%d finishes=%d acks=%d loaded=%d teleports=%d lastChunk=%d lastFinish=%d", chunks, starts, finishes, acks, loaded, teleports, lastChunk, lastFinish)
	}
	t.Logf("protocol %d: %d complete chunks, %d acknowledged batches, own bootstrap satisfied", number, chunks, finishes)
}
