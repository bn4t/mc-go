package blackbox

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/conformance"
	publicserver "mc-go/server"
)

// These independent JSON shapes describe only the fields seeded/checked by
// this fixture. Version3 retains its existing numeric item-handle format;
// handles are resolved through the public catalog before fixture construction.
type dataSaveProbeStack struct {
	ID, Count  int32
	Damage     int32                   `json:"damage,omitempty"`
	RepairCost int32                   `json:"repair_cost,omitempty"`
	Name       string                  `json:"name,omitempty"`
	Contents   *[27]dataSaveProbeStack `json:"contents,omitempty"`
}

type dataSaveProbeContainer struct {
	Dim       string `json:"dim,omitempty"`
	X, Y, Z   int
	Block     string               `json:"block,omitempty"`
	Items     []dataSaveProbeStack `json:"items"`
	LootTable string               `json:"loot_table,omitempty"`
	LootSeed  int64                `json:"loot_seed,omitempty"`
	BrewTime  int32                `json:"brew_time,omitempty"`
	Fuel      int32                `json:"fuel,omitempty"`
	Disabled  uint16               `json:"disabled,omitempty"`
}

type dataSaveProbeSign struct {
	Dim          string `json:"dim,omitempty"`
	X, Y, Z      int
	Front        [4]string `json:"front"`
	Back         [4]string `json:"back"`
	FrontColor   string    `json:"frontColor,omitempty"`
	BackColor    string    `json:"backColor,omitempty"`
	FrontGlowing bool      `json:"frontGlowing,omitempty"`
	BackGlowing  bool      `json:"backGlowing,omitempty"`
	Waxed        bool      `json:"waxed,omitempty"`
}

type dataSaveProbeFile struct {
	Version    int                        `json:"version"`
	Players    map[string]json.RawMessage `json:"players"`
	Signs      []dataSaveProbeSign        `json:"signs"`
	Chests     []dataSaveProbeContainer   `json:"chests"`
	Containers []dataSaveProbeContainer   `json:"containers"`
}

const dataSaveProbeCount = 2048

// TestServerDataSaveResponsiveness uses the identical baseline/after public
// Server.DataPath/LoadServerData/SaveServerData API and real TCP clients. The
// fixed schema3 fixture has 2048 chests, 2048 generic containers and 2048 signs
// across three dimensions, far from observers. Nested Contents have depth2.
// There are no remembered offline players, avoiding an unrelated baseline bug.
// Bootstrap, loading and complete49 observer readiness precede measurement;
// the runtime is frozen so player and container state remains stable.
// One token per observer is outstanding, paced20ms after each response. Raw
// timestamps retain responses after API return. No timing threshold asserts
// correctness. All saved entries, nested/scalar values and active players are
// checked after successful API completion, without relying on map/list order.
// Measurements include capture, transport, scheduling, serialization and the
// filesystem. They do not isolate owner hold, establish capacity or imply fsync.
// MCGO_DATA_SAVE_REPORT appends one raw NDJSON record per invocation.
func TestServerDataSaveResponsiveness(t *testing.T) {
	if os.Getenv(targetProp) != "" {
		t.Skip("public SaveServerData probe needs its own embedded public runtime")
	}
	raw, err := os.ReadFile("../../server/testdata/external-world/facts.json")
	if err != nil {
		t.Fatal(err)
	}
	var measured struct{ Profiles map[string]coldJoinFacts }
	if err := json.Unmarshal(raw, &measured); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback bind unavailable: %v", err)
	}
	s := publicserver.New(ln.Addr().String())
	s.DataPath = filepath.Join(t.TempDir(), "server-data.json")
	s.ViewDistance, s.SimulationDistance, s.StartFrozen = 2, 2, true
	t.Cleanup(func() { _ = ln.Close(); s.Stop() })
	ids := make(map[string]int32)
	for _, name := range []string{"shulker_box", "diamond"} {
		id, ok := s.ItemCatalog().Lookup("minecraft:" + name)
		if !ok {
			t.Fatalf("public item catalog lacks %s", name)
		}
		ids[name] = int32(id)
	}
	seed := dataSaveProbeFile{Version: 3, Players: map[string]json.RawMessage{}}
	for index := 0; index < dataSaveProbeCount; index++ {
		seed.Chests = append(seed.Chests, dataSaveProbeExpectedContainer(index, false, ids))
		seed.Containers = append(seed.Containers, dataSaveProbeExpectedContainer(index, true, ids))
		seed.Signs = append(seed.Signs, dataSaveProbeExpectedSign(index))
	}
	seedRaw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	seedBytes, seedSHA := len(seedRaw), fmt.Sprintf("%x", sha256.Sum256(seedRaw))
	if err := os.WriteFile(s.DataPath, seedRaw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.LoadServerData(); err != nil {
		t.Fatal(err)
	}
	seed, seedRaw = dataSaveProbeFile{}, nil
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
			Username: fmt.Sprintf("DS%x%d", time.Now().UnixNano()%1000000000, protocol), Timeout: 90 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		f := measured.Profiles[fmt.Sprint(protocol)]
		columnPreparationWindow(t, ctx, c, conformance.ChunkShape{Sections: 24, BlockStates: f.BlockStates, Biomes: f.Biomes}, "data-save observer ready", 0, c.InitialPackets(), false)
		if err := coldJoinToken(ctx, c, "data-save-ready-"+runID+fmt.Sprint(protocol), nil); err != nil {
			t.Fatal(err)
		}
		observers = append(observers, c)
	}
	// Whole-process counters include server and TCP clients and are not isolated
	// save allocations or a bound on the detached graph's retained memory.
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
				if out.err = coldJoinToken(ctx, c, fmt.Sprintf("data-save-%s-%d-%d", runID, index, sequence), nil); out.err != nil {
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
	callNS := time.Since(epoch).Nanoseconds()
	err = s.SaveServerData() // Public API, outside simulation callbacks.
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
		t.Logf("protocol %d: tokens=%d completed-before-return=%d p50=%v p95=%v max=%v; public SaveServerData=%v", protocol, len(durations), completed,
			time.Duration(durations[(len(durations)-1)/2]), time.Duration(durations[(95*len(durations)+99)/100-1]), time.Duration(durations[len(durations)-1]), time.Duration(completeNS-callNS))
	}
	savedRaw, err := os.ReadFile(s.DataPath)
	if err != nil {
		t.Fatal(err)
	}
	dataSaveProbeValidate(t, savedRaw, ids, observers)
	if report := os.Getenv("MCGO_DATA_SAVE_REPORT"); report != "" {
		record := struct {
			RunID           string           `json:"run_id"`
			EpochUnixNS     int64            `json:"epoch_unix_ns"`
			StartNS         int64            `json:"start_ns"`
			CallNS          int64            `json:"call_ns"`
			CompleteNS      int64            `json:"complete_ns"`
			Probes          []coldJoinProbe  `json:"probes"`
			SeedBytes       int              `json:"seed_bytes"`
			SeedSHA256      string           `json:"seed_sha256"`
			SavedBytes      int              `json:"saved_bytes"`
			RecordsPerKind  int              `json:"records_per_kind"`
			ItemIDs         map[string]int32 `json:"item_ids"`
			HeapBeforeBytes uint64           `json:"process_heap_before_bytes"`
			HeapAfterBytes  uint64           `json:"process_heap_after_bytes"`
			AllocatedBytes  uint64           `json:"process_allocated_bytes"`
		}{runID, epoch.UnixNano(), startNS, callNS, completeNS, probes, seedBytes, seedSHA, len(savedRaw), dataSaveProbeCount, ids, before.HeapAlloc, after.HeapAlloc, after.TotalAlloc - before.TotalAlloc}
		f, err := os.OpenFile(report, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewEncoder(f).Encode(record)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("write data-save probe report: %v/%v", err, closeErr)
		}
	}
}

func dataSaveProbeExpectedContainer(index int, generic bool, ids map[string]int32) dataSaveProbeContainer {
	dims := [...]string{"minecraft:overworld", "minecraft:the_nether", "minecraft:the_end"}
	entry := dataSaveProbeContainer{Dim: dims[index%3], X: 100000 + index%128, Y: 70, Z: 100000 + index/128,
		LootTable: "minecraft:chests/simple_dungeon", LootSeed: int64(index + 8675309)}
	slots := 27
	if generic {
		entry.Z += 10000
		entry.LootTable, entry.LootSeed = "", 0
		switch index % 3 {
		case 0:
			entry.Block = "barrel"
		case 1:
			entry.Block, slots, entry.BrewTime, entry.Fuel = "brewing_stand", 5, 7, 9
		case 2:
			entry.Block, slots, entry.Disabled = "crafter", 9, 257
		}
	}
	entry.Items = make([]dataSaveProbeStack, slots)
	inner, outer := new([27]dataSaveProbeStack), new([27]dataSaveProbeStack)
	inner[5] = dataSaveProbeStack{ID: ids["diamond"], Count: int32(index%32 + 1), Damage: 3, RepairCost: 2,
		Name: fmt.Sprintf("deep-%d-%s", index, strings.Repeat("n", 512))}
	outer[2] = dataSaveProbeStack{ID: ids["shulker_box"], Count: 1, Name: "middle", Contents: inner}
	entry.Items[3] = dataSaveProbeStack{ID: ids["shulker_box"], Count: 1, Name: "outer", Contents: outer}
	entry.Items[slots-1] = dataSaveProbeStack{ID: ids["diamond"], Count: 13, Damage: 4, RepairCost: 6, Name: "scalar-" + strings.Repeat("s", 128)}
	return entry
}

func dataSaveProbeExpectedSign(index int) dataSaveProbeSign {
	dims := [...]string{"minecraft:overworld", "minecraft:the_nether", "minecraft:the_end"}
	entry := dataSaveProbeSign{Dim: dims[index%3], X: 100000 + index%128, Y: 70, Z: 120000 + index/128,
		FrontColor: "blue", BackColor: "red", FrontGlowing: true, BackGlowing: true, Waxed: true}
	for line := range 4 {
		entry.Front[line] = fmt.Sprintf("front-%d-%d-%s", index, line, strings.Repeat("f", 128))
		entry.Back[line] = fmt.Sprintf("back-%d-%d-%s", index, line, strings.Repeat("b", 128))
	}
	return entry
}

func dataSaveProbeValidate(t *testing.T, raw []byte, ids map[string]int32, observers []*conformance.PlayClient) {
	t.Helper()
	var saved dataSaveProbeFile
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Version != 3 || len(saved.Signs) != dataSaveProbeCount || len(saved.Chests) != dataSaveProbeCount || len(saved.Containers) != dataSaveProbeCount || len(saved.Players) != 2 {
		t.Fatalf("unexpected data format/counts: version=%d signs=%d chests=%d containers=%d players=%d", saved.Version, len(saved.Signs), len(saved.Chests), len(saved.Containers), len(saved.Players))
	}
	for _, generic := range []bool{false, true} {
		entries, zBase := saved.Chests, 100000
		if generic {
			entries, zBase = saved.Containers, 110000
		}
		seen := make([]bool, dataSaveProbeCount)
		for _, entry := range entries {
			index := (entry.Z-zBase)*128 + entry.X - 100000
			if index < 0 || index >= dataSaveProbeCount || seen[index] || !reflect.DeepEqual(entry, dataSaveProbeExpectedContainer(index, generic, ids)) {
				t.Fatalf("generic=%v invalid/duplicate container at %s/%d,%d,%d", generic, entry.Dim, entry.X, entry.Y, entry.Z)
			}
			seen[index] = true
		}
	}
	seen := make([]bool, dataSaveProbeCount)
	for _, entry := range saved.Signs {
		index := (entry.Z-120000)*128 + entry.X - 100000
		if index < 0 || index >= dataSaveProbeCount || seen[index] || entry != dataSaveProbeExpectedSign(index) {
			t.Fatalf("invalid/duplicate sign at %s/%d,%d,%d", entry.Dim, entry.X, entry.Y, entry.Z)
		}
		seen[index] = true
	}
	for _, c := range observers {
		var p struct {
			Inv      []dataSaveProbeStack `json:"inv"`
			Health   float32              `json:"health"`
			Food     int32                `json:"food"`
			Dim      string               `json:"dim"`
			X, Y, Z  float64
			GameMode int32 `json:"game_mode"`
		}
		if err := json.Unmarshal(saved.Players[c.Username()], &p); err != nil {
			t.Fatalf("saved active player %s: %v", c.Username(), err)
		}
		if len(p.Inv) != 46 || p.Health != 20 || p.Food != 20 || p.Dim != "minecraft:overworld" || p.X != .5 || p.Y != -60 || p.Z != .5 || p.GameMode != 1 {
			t.Fatalf("saved active player %s has unexpected stable state: inventory=%d health=%v food=%d dimension=%s position=%v,%v,%v mode=%d", c.Username(), len(p.Inv), p.Health, p.Food, p.Dim, p.X, p.Y, p.Z, p.GameMode)
		}
	}
}
