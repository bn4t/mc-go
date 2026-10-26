package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/player"
	"mc-go/api/worldview"
	"mc-go/internal/world"
)

// These two regressions also run unchanged against the prior committed tree.
// They establish the remembered-player omissions independently of the writer.
func TestDataSaveRememberedOfflinePlayersSurviveRestart(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	s.DataPath = filepath.Join(t.TempDir(), "remembered.json")
	p := playerPersist{UUID: offlineUUID("Offline"), Health: 17, Food: 12}
	p.Inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["diamond"], Count: 7}
	s.simulationAction(func() {
		s.savedPlayersMu.Lock()
		s.savedPlayers = map[string]playerPersist{"Offline": p, DashUUID(p.UUID): p}
		s.savedPlayersMu.Unlock()
	})
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	restarted := simulationTestServer(t)
	t.Cleanup(restarted.Stop)
	restarted.DataPath = s.DataPath
	if err := restarted.LoadServerData(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Offline", DashUUID(p.UUID)} {
		got, ok := restarted.savedPlayers[key]
		if !ok || got.Health != 17 || got.Food != 12 || got.Inv[slotHotbarStart].Count != 7 {
			t.Fatalf("remembered offline player %q lost across save/restart: present=%v health=%v food=%d count=%d", key, ok, got.Health, got.Food, got.Inv[slotHotbarStart].Count)
		}
	}
}

func TestDataSaveLateReplacedUntrackPreservesSuccessor(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	old, _ := enforceSession(t, s, "ReplacedSave")
	s.simulationAction(func() { old.inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["diamond"], Count: 1} })
	current, _ := enforceSession(t, s, "ReplacedSave")
	s.simulationAction(func() {
		current.inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["diamond"], Count: 7}
		s.untrack(current)
		s.untrack(old)
	})
	got := s.savedPlayers["ReplacedSave"]
	if got.Inv[slotHotbarStart].Count != 7 {
		t.Fatalf("late replaced disconnect overwrote successor cache: count=%d want 7", got.Inv[slotHotbarStart].Count)
	}
}

func dataSaveTestServer(t *testing.T) *Server {
	t.Helper()
	s := joinPrepTestServer(t, func(world.Pos) world.Block { return world.Air })
	s.dims[dimOverworld].skylight = false
	s.DataPath = filepath.Join(t.TempDir(), "data.json")
	return s
}

func readSavedData(t *testing.T, path string) serverDataFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out serverDataFile
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func nestedDataSaveStack(count int32) ItemStack {
	inner := new([shulkerSlots]ItemStack)
	inner[2] = ItemStack{ID: world.ItemIDs["diamond"], Count: count, Name: "captured"}
	outer := new([shulkerSlots]ItemStack)
	outer[1] = ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: inner}
	return ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: outer}
}

func dataSaveInnerCount(stack ItemStack) int32 {
	if stack.Contents == nil || stack.Contents[1].Contents == nil {
		return -1
	}
	return stack.Contents[1].Contents[2].Count
}

func TestDataSaveDetachedNestedAliasesAllowPublicMoveAndEdit(t *testing.T) {
	s := dataSaveTestServer(t)
	java, _ := enforceSession(t, s, "NestedSave")
	// Retire this manual Java fixture through ownership before its legacy
	// cleanup runs; the public avatar's context may retire concurrently.
	t.Cleanup(func() { s.simulationAction(func() { s.untrack(java) }) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	core := pluginAvatarCore(s)
	avatar, err := core.Admit(ctx, player.Profile{Name: "DataObserver", UUID: offlineUUID("DataObserver")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if u, err := avatar.Next(ctx); err != nil || u.Kind != player.Snapshot {
		t.Fatalf("avatar baseline=%+v error=%v", u, err)
	}
	view, _ := watchTestVolume(t, s, worldview.Request{Dimension: dimOverworld,
		Bounds: worldview.Box{Min: worldview.Pos{X: 1, Y: 0, Z: 1}, Max: worldview.Pos{X: 2, Y: 1, Z: 2}}})
	d := s.dims[dimOverworld]
	shared := nestedDataSaveStack(7)
	offline := playerPersist{Inv: [slotCount]ItemStack{shared}, Stats: map[string]int32{"custom:jump": 7},
		StatsOrder: []string{"custom:jump"}, Advancements: map[string]savedAdv{"example:test": {Done: true, Criteria: map[string]int64{"first": 7}}},
		UnlockedRecipes: []string{"example:before"}, SeenRecipes: []int32{7}}
	container := &containerData{block: "barrel", slots: []ItemStack{shared}, brewTime: 7, fuel: 8, disabled: 9}
	chest := &chestData{lootTable: "example:before", lootSeed: 7}
	chest.slots[2] = shared
	sign := &signText{Front: [4]string{"before"}, frontColor: "blue", frontGlowing: true, waxed: true}
	s.simulationAction(func() {
		java.inv[slotHotbarStart], java.carried, java.ender[2], java.bodyArmor = shared, shared, shared, shared
		s.savedPlayersMu.Lock()
		s.savedPlayers = map[string]playerPersist{"OfflineNested": offline}
		s.savedPlayersMu.Unlock()
		d.chests.chests = map[world.Pos]*chestData{{X: 3}: chest}
		d.containers.containers = map[world.Pos]*containerData{{X: 4}: container}
		d.signs.signs = map[world.Pos]*signText{{X: 5}: sign}
	})
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	s.dataSaveWrite = func(path string, snapshot *serverDataSnapshot) error {
		once.Do(func() { close(started); <-release })
		return snapshot.write(path)
	}
	saved := make(chan error, 1)
	go func() { saved <- s.SaveServerData() }()
	joinPrepTestWait(t, started, "detached data writer")
	if applied, err := avatar.Move(ctx, player.Movement{X: 5, Y: 1, Z: 5}); err != nil || !applied {
		t.Fatalf("public move during data write=%v,%v", applied, err)
	}
	if update, err := avatar.Next(ctx); err != nil || update.Kind != player.Moved || update.Player.Pose.X != 5 {
		t.Fatalf("public move was not committed while saving: %+v,%v", update, err)
	}
	if result, err := core.EditBlock(ctx, worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{X: 1, Y: 0, Z: 1}, State: blockEditKey(t, "stone")}); err != nil || !result.Applied {
		t.Fatalf("public edit during data write=%+v,%v", result, err)
	}
	if update := nextTestWorldUpdate(t, view); update.Kind != worldview.Delta || len(update.Changes) != 1 || update.Changes[0].State != world.Stone {
		t.Fatalf("public edit was not published while saving: %+v", update)
	}
	s.simulationAction(func() {
		shared.Contents[1].Contents[2].Count = 99
		shared.Contents[1].Contents[2].Name = "after"
		container.slots[0] = ItemStack{ID: world.ItemIDs["stone"], Count: 99}
		container.brewTime, container.fuel, container.disabled = 99, 99, 99
		chest.lootTable, chest.lootSeed = "example:after", 99
		sign.Front[0], sign.frontColor, sign.frontGlowing, sign.waxed = "after", "red", false, false
		offline.Stats["custom:jump"] = 99
		offline.StatsOrder[0] = "after"
		offline.Advancements["example:test"].Criteria["first"] = 99
		offline.UnlockedRecipes[0], offline.SeenRecipes[0] = "after", 99
	})
	select {
	case err := <-saved:
		t.Fatalf("public save returned before writing: %v", err)
	default:
	}
	if s.TickCount() != 0 || !s.clock.isFrozen() {
		t.Fatal("data save progress required advancing the game clock")
	}
	open()
	if err := compactSaveWait(t, saved); err != nil {
		t.Fatal(err)
	}
	file := readSavedData(t, s.DataPath)
	p := file.Players["NestedSave"]
	for name, stack := range map[string]ItemStack{"inventory": p.Inv[slotHotbarStart], "carried": p.Carried, "ender": p.Ender[2], "body": p.Body,
		"offline": file.Players["OfflineNested"].Inv[0], "chest": file.Chests[0].Items[2], "container": file.Containers[0].Items[0]} {
		if dataSaveInnerCount(stack) != 7 || stack.Contents[1].Contents[2].Name != "captured" {
			t.Fatalf("post-capture nested mutation leaked into %s: %+v", name, stack)
		}
	}
	remembered := file.Players["OfflineNested"]
	if remembered.Stats["custom:jump"] != 7 || remembered.StatsOrder[0] != "custom:jump" || remembered.Advancements["example:test"].Criteria["first"] != 7 || remembered.UnlockedRecipes[0] != "example:before" || remembered.SeenRecipes[0] != 7 {
		t.Fatalf("remembered player maps/slices were not detached: %+v", remembered)
	}
	if file.Containers[0].BrewTime != 7 || file.Containers[0].Fuel != 8 || file.Containers[0].Disabled != 9 || file.Chests[0].LootTable != "example:before" || file.Chests[0].LootSeed != 7 || file.Signs[0].Front[0] != "before" || file.Signs[0].FrontColor != "blue" || !file.Signs[0].FrontGlowing || !file.Signs[0].Waxed {
		t.Fatal("post-capture block-entity mutation leaked into the detached file")
	}
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	latest := readSavedData(t, s.DataPath)
	if dataSaveInnerCount(latest.Players["NestedSave"].Inv[slotHotbarStart]) != 99 || latest.Containers[0].Items[0].Count != 99 || latest.Signs[0].Front[0] != "after" {
		t.Fatal("later data save failed to capture the newer committed state")
	}
}

func TestDataSaveContentsSharingAndCyclesAreDetached(t *testing.T) {
	s := dataSaveTestServer(t)
	java, _ := enforceSession(t, s, "GraphSave")
	shared := nestedDataSaveStack(7)
	var snapshot *serverDataSnapshot
	s.simulationAction(func() {
		java.inv[0], java.inv[1] = shared, shared
		snapshot = s.captureServerDataOwned()
	})
	copy := snapshot.file.Players["GraphSave"]
	if copy.Inv[0].Contents == shared.Contents || copy.Inv[0].Contents != copy.Inv[1].Contents || !reflect.DeepEqual(copy.Inv[0], shared) {
		t.Fatal("capture did not detach the shared contents graph while preserving its values and sharing")
	}
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.DataPath)
	cycle := new([shulkerSlots]ItemStack)
	cycle[0] = ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: cycle}
	s.simulationAction(func() { java.inv[0] = cycle[0] })
	err := s.SaveServerData()
	var unsupported *json.UnsupportedValueError
	if !errors.As(err, &unsupported) {
		t.Fatalf("cycle did not retain JSON's normal error: %v", err)
	}
	after, _ := os.ReadFile(s.DataPath)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cyclic contents damaged the previous complete data file")
	}
	s.simulationAction(func() { java.inv[0] = nestedDataSaveStack(9) })
	if err := s.SaveServerData(); err != nil {
		t.Fatalf("cycle error retained the job slot or global writer fence: %v", err)
	}
}

func TestDataSaveCurrentIncarnationWinsRememberedAndRetiredRecords(t *testing.T) {
	s := dataSaveTestServer(t)
	old, _ := enforceSession(t, s, "OverlaySave")
	s.simulationAction(func() { old.inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["diamond"], Count: 1} })
	current, _ := enforceSession(t, s, "OverlaySave")
	s.simulationAction(func() {
		current.inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["diamond"], Count: 7}
		s.savedPlayersMu.Lock()
		s.savedPlayers = map[string]playerPersist{"OverlaySave": {Health: 1}}
		s.savedPlayersMu.Unlock()
	})
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	if got := readSavedData(t, s.DataPath).Players["OverlaySave"].Inv[slotHotbarStart].Count; got != 7 {
		t.Fatalf("inactive or remembered record replaced the current player: count=%d", got)
	}
}

func TestDataSaveDisconnectFenceOrdersAfterPublicWriter(t *testing.T) {
	s := dataSaveTestServer(t)
	java, _ := enforceSession(t, s, "DisconnectSave")
	s.simulationAction(func() { java.inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["diamond"], Count: 1} })
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	s.dataSaveWrite = func(path string, snapshot *serverDataSnapshot) error {
		once.Do(func() { close(started); <-release })
		return snapshot.write(path)
	}
	first := make(chan error, 1)
	go func() { first <- s.SaveServerData() }()
	joinPrepTestWait(t, started, "public data write before disconnect")
	s.simulationAction(func() { java.inv[slotHotbarStart].Count = 7 })
	entered, left := make(chan struct{}), make(chan struct{})
	go func() {
		s.simulationAction(func() {
			close(entered)
			s.untrack(java)
		})
		close(left)
	}()
	joinPrepTestWait(t, entered, "owned disconnect data fence")
	open()
	if err := compactSaveWait(t, first); err != nil {
		t.Fatal(err)
	}
	joinPrepTestWait(t, left, "disconnect after public save")
	if got := readSavedData(t, s.DataPath).Players["DisconnectSave"].Inv[slotHotbarStart].Count; got != 7 {
		t.Fatalf("older public snapshot overwrote disconnect data: count=%d", got)
	}
	current, _ := enforceSession(t, s, "DisconnectSave")
	s.simulationAction(func() { s.applySavedPlayer(current) })
	if got := current.inv[slotHotbarStart].Count; got != 7 {
		t.Fatalf("reconnect did not restore the fenced disconnect state: count=%d", got)
	}
}

func TestDataSaveWriteFailureAndDisabledPersistence(t *testing.T) {
	s := dataSaveTestServer(t)
	want := errors.New("controlled data writer error")
	var calls atomic.Int32
	s.dataSaveWrite = func(path string, snapshot *serverDataSnapshot) error {
		if calls.Add(1) == 1 {
			return want
		}
		return snapshot.write(path)
	}
	if err := s.SaveServerData(); !errors.Is(err, want) {
		t.Fatalf("wrong write-completion receipt: %v", err)
	}
	if err := s.SaveServerData(); err != nil {
		t.Fatalf("write error retained admission or global lock: %v", err)
	}
	s.DataPath, s.WorldPath = "", ""
	if err := s.SaveServerData(); err == nil {
		t.Fatal("disabled data persistence reported success")
	}
}

func TestDataSaveGlobalWriteFenceDoesNotOwnAnotherRuntime(t *testing.T) {
	firstServer, secondServer := dataSaveTestServer(t), dataSaveTestServer(t)
	secondServer.DataPath = firstServer.DataPath // preserve cross-runtime write exclusion
	firstServer.savedPlayers = map[string]playerPersist{"FirstRuntime": {Health: 1}}
	secondServer.savedPlayers = map[string]playerPersist{"SecondRuntime": {Health: 2}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	core := pluginAvatarCore(secondServer)
	avatar, err := core.Admit(ctx, player.Profile{Name: "OtherOwner", UUID: offlineUUID("OtherOwner")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := avatar.Next(ctx); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	firstServer.dataSaveWrite = func(path string, snapshot *serverDataSnapshot) error {
		close(started)
		<-release
		return snapshot.write(path)
	}
	secondWriting := make(chan struct{})
	secondServer.dataSaveWrite = func(path string, snapshot *serverDataSnapshot) error {
		close(secondWriting)
		return snapshot.write(path)
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- firstServer.SaveServerData() }()
	joinPrepTestWait(t, started, "first runtime holding global write fence")
	captured := make(chan struct{})
	if !secondServer.queueSimulationAction(func() { close(captured) }) {
		t.Fatal("could not observe second runtime capture")
	}
	go func() { second <- secondServer.SaveServerData() }()
	joinPrepTestWait(t, captured, "second runtime capture behind global writer")
	if applied, err := avatar.Move(ctx, player.Movement{X: 5, Y: 1, Z: 5}); err != nil || !applied {
		t.Fatalf("global writer fence retained the other owner: move=%v,%v", applied, err)
	}
	select {
	case <-secondWriting:
		t.Fatal("cross-runtime data writes entered their filesystem fence concurrently")
	default:
	}
	open()
	for _, saved := range []<-chan error{first, second} {
		if err := compactSaveWait(t, saved); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := readSavedData(t, secondServer.DataPath).Players["SecondRuntime"]; !ok {
		t.Fatal("later serialized runtime write did not complete")
	}
}

func TestDataSaveShutdownRejectsPublicButAllowsTerminalDisconnect(t *testing.T) {
	s := dataSaveTestServer(t)
	java, _ := enforceSession(t, s, "StopDataSave")
	s.simulationAction(func() { java.inv[slotHotbarStart] = ItemStack{ID: world.ItemIDs["diamond"], Count: 1} })
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	var writes atomic.Int32
	s.dataSaveWrite = func(path string, snapshot *serverDataSnapshot) error {
		writes.Add(1)
		once.Do(func() { close(started); <-release })
		return snapshot.write(path)
	}
	first := make(chan error, 1)
	go func() { first <- s.SaveServerData() }()
	joinPrepTestWait(t, started, "accepted data save before shutdown")
	s.simulationAction(func() { java.inv[slotHotbarStart].Count = 7 })
	// A real connection contributes to this same shutdown fence until its
	// deferred owned untrack completes. This manual transport fixture models
	// that lifecycle without starting clocks or a network bootstrap.
	s.connections.Add(1)
	var connectionDone sync.Once
	finishConnection := func() { connectionDone.Do(s.connections.Done) }
	t.Cleanup(finishConnection)
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	deadline := time.Now().Add(5 * time.Second)
	for !s.ShuttingDown() {
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.SaveServerData(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("public data save admitted during shutdown: %v", err)
	}
	entered, left := make(chan struct{}), make(chan struct{})
	go func() {
		defer finishConnection()
		s.simulationAction(func() { close(entered); s.untrack(java) })
		close(left)
	}()
	joinPrepTestWait(t, entered, "terminal disconnect during shutdown")
	select {
	case <-stopped:
		t.Fatal("Stop ignored its accepted data writer or terminal disconnect")
	default:
	}
	open()
	if err := compactSaveWait(t, first); err != nil {
		t.Fatalf("shutdown canceled an accepted data write: %v", err)
	}
	joinPrepTestWait(t, left, "shutdown disconnect persistence")
	joinPrepTestWait(t, stopped, "shutdown data fence")
	if writes.Load() != 2 || readSavedData(t, s.DataPath).Players["StopDataSave"].Inv[slotHotbarStart].Count != 7 {
		t.Fatal("terminal shutdown save was dropped or a rejected public save wrote data")
	}
}
