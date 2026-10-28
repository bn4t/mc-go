package server

import (
	"path/filepath"
	"testing"
)

// The deterministic failure checks native login's canonical UUID lookup, rather
// than waiting for randomized duplicate-map iteration to choose an older alias.
func TestNativeOfflineRememberedAliasesFollowLatestDisconnect(t *testing.T) {
	seed := nativeSaveTestServer(t)
	old, _ := enforceSession(t, seed, "NativeOffline")
	seed.simulationAction(func() {
		old.inv[slotHotbarStart] = nestedDataSaveStack(1)
		old.addCustomStat("jump", 1)
		old.advCrits = map[string]map[string]int64{"minecraft:story/root": {"crafting_table": 1000}}
		old.advDone = map[string]bool{"minecraft:story/root": true}
	})
	if err := seed.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	boot := nativeSaveTestServer(t)
	boot.WorldDir = seed.WorldDir
	if err := boot.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	boot.savedPlayersMu.Lock()
	boot.savedPlayers["FormerName"] = boot.savedPlayers[DashUUID(offlineUUID("NativeOffline"))]
	foreign := playerPersist{UUID: offlineUUID("Unrelated"), Health: 13}
	boot.savedPlayers["Unrelated"] = foreign
	boot.savedPlayersMu.Unlock()
	current, _ := enforceSession(t, boot, "NativeOffline")
	boot.simulationAction(func() {
		boot.applySavedPlayer(current)
		current.inv[slotHotbarStart] = nestedDataSaveStack(7)
		current.addCustomStat("jump", 6)
		current.advCrits = map[string]map[string]int64{"minecraft:story/root": {"crafting_table": 7000}}
		current.advDone = map[string]bool{"minecraft:story/root": true}
		boot.untrack(current)
	})
	uuid := DashUUID(offlineUUID("NativeOffline"))
	// Native boot records use UUID keys. If a player later returns under another
	// name, savedPlayerForSession falls back to this UUID record. It must reflect
	// the latest completed disconnect, independently of Go's map iteration order.
	jumpID, _ := CustomStatID("jump")
	jumpKey := statPersistKey(statKey{statCustom, jumpID})
	boot.savedPlayersMu.Lock()
	matching := 0
	for alias, canonical := range boot.savedPlayers {
		if canonical.UUID != offlineUUID("NativeOffline") {
			continue
		}
		matching++
		if dataSaveInnerCount(canonical.Inv[slotHotbarStart]) != 7 || canonical.Stats[jumpKey] != 7 || canonical.Advancements["minecraft:story/root"].Criteria["crafting_table"] != 7000 {
			t.Errorf("remembered alias %q stale after disconnect: count=%d jump=%d advancement=%+v", alias, dataSaveInnerCount(canonical.Inv[slotHotbarStart]), canonical.Stats[jumpKey], canonical.Advancements["minecraft:story/root"])
		}
	}
	boot.savedPlayersMu.Unlock()
	if matching == 0 {
		t.Fatal("disconnect forgot native player identity")
	}
	if boot.savedPlayers["Unrelated"].Health != 13 || boot.savedPlayers["Unrelated"].UUID != foreign.UUID {
		t.Fatal("remembering one identity modified unrelated UUID")
	}
	for _, mode := range []string{"public", "owned"} {
		t.Run(mode, func(t *testing.T) {
			var err error
			if mode == "public" {
				err = boot.SaveWorld()
			} else {
				boot.simulationAction(func() { err = boot.saveWorldOwned() })
			}
			if err != nil {
				t.Fatal(err)
			}
			restarted := nativeSaveTestServer(t)
			restarted.WorldDir = boot.WorldDir
			if err := restarted.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			p := restarted.savedPlayers[uuid]
			if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 || p.Stats[jumpKey] != 7 || p.Advancements["minecraft:story/root"].Criteria["crafting_table"] != 7000 {
				t.Fatalf("offline %s native save restored stale state: %+v", mode, p)
			}
		})
	}
	boot.DataPath = filepath.Join(t.TempDir(), "data.json")
	if err := boot.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	for alias, p := range readSavedData(t, boot.DataPath).Players {
		if p.UUID == offlineUUID("NativeOffline") && (dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 || p.Stats[jumpKey] != 7 || p.Advancements["minecraft:story/root"].Criteria["crafting_table"] != 7000) {
			t.Fatalf("JSON alias %q stale after ordinary disconnect", alias)
		}
	}
}

func TestNativeIngestRefreshesExistingIdentityAliases(t *testing.T) {
	seed := nativeSaveTestServer(t)
	current, _ := enforceSession(t, seed, "NativeIngest")
	seed.simulationAction(func() {
		current.inv[slotHotbarStart] = nestedDataSaveStack(7)
		current.addCustomStat("jump", 7)
		current.advCrits = map[string]map[string]int64{"minecraft:story/root": {"crafting_table": 7000}}
		current.advDone = map[string]bool{"minecraft:story/root": true}
	})
	if err := seed.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	boot := nativeSaveTestServer(t)
	boot.WorldDir = seed.WorldDir
	stale := playerPersist{UUID: offlineUUID("NativeIngest")}
	stale.Inv[slotHotbarStart] = nestedDataSaveStack(1)
	legacy := stale
	legacy.UUID = [16]byte{}
	boot.savedPlayers = map[string]playerPersist{"FormerIngest": stale, "NativeIngest": legacy, "LegacyOther": {Health: 11}, "Unrelated": {UUID: offlineUUID("Unrelated"), Health: 13}}
	if err := boot.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	jumpID, _ := CustomStatID("jump")
	jumpKey := statPersistKey(statKey{statCustom, jumpID})
	for _, alias := range []string{"FormerIngest", "NativeIngest", DashUUID(stale.UUID)} {
		p := boot.savedPlayers[alias]
		if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 || p.Stats[jumpKey] != 7 || p.Advancements["minecraft:story/root"].Criteria["crafting_table"] != 7000 {
			t.Fatalf("native ingest left remembered alias %q stale: %+v", alias, p)
		}
	}
	if boot.savedPlayers["Unrelated"].Health != 13 || boot.savedPlayers["LegacyOther"].Health != 11 {
		t.Fatal("native ingest changed unrelated identity")
	}
}

func TestNativeRememberedAliasesFollowActiveJSONSnapshot(t *testing.T) {
	seed := nativeSaveTestServer(t)
	old, _ := enforceSession(t, seed, "ActiveAlias")
	seed.simulationAction(func() {
		old.inv[slotHotbarStart] = nestedDataSaveStack(7)
		old.addCustomStat("jump", 7)
		old.advCrits = map[string]map[string]int64{"minecraft:story/root": {"crafting_table": 7000}}
		old.advDone = map[string]bool{"minecraft:story/root": true}
	})
	if err := seed.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	s := nativeSaveTestServer(t)
	s.WorldDir = seed.WorldDir
	if err := s.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	s.DataPath = filepath.Join(t.TempDir(), "data.json")
	current, _ := enforceSession(t, s, "ActiveAlias")
	s.simulationAction(func() {
		s.applySavedPlayer(current)
		current.inv[slotHotbarStart] = nestedDataSaveStack(9)
		current.addCustomStat("jump", 2)
		current.advCrits = map[string]map[string]int64{"minecraft:story/root": {"crafting_table": 9000}}
		current.advDone = map[string]bool{"minecraft:story/root": true}
	})
	if err := s.SaveServerData(); err != nil {
		t.Fatal(err)
	}
	uuid := offlineUUID("ActiveAlias")
	jumpID, _ := CustomStatID("jump")
	jumpKey := statPersistKey(statKey{statCustom, jumpID})
	assertLatest := func(t *testing.T, p playerPersist) {
		t.Helper()
		if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 9 || p.Stats[jumpKey] != 9 || p.Advancements["minecraft:story/root"].Criteria["crafting_table"] != 9000 {
			t.Fatalf("active JSON snapshot retained stale identity alias: count=%d jump=%d advancement=%+v", dataSaveInnerCount(p.Inv[slotHotbarStart]), p.Stats[jumpKey], p.Advancements["minecraft:story/root"])
		}
	}
	s.savedPlayersMu.Lock()
	for alias, p := range s.savedPlayers {
		if p.UUID == uuid && dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 {
			t.Errorf("private JSON capture changed live remembered alias %q", alias)
		}
	}
	s.savedPlayersMu.Unlock()
	file := readSavedData(t, s.DataPath)
	for alias, p := range file.Players {
		if p.UUID == uuid {
			t.Run(alias, func(t *testing.T) { assertLatest(t, p) })
		}
	}
	restored := nativeSaveTestServer(t)
	restored.DataPath = s.DataPath
	if err := restored.LoadServerData(); err != nil {
		t.Fatal(err)
	}
	// The newly produced JSON artifact is authoritative input for an offline
	// native save; no live Java incarnation can conceal stale duplicate aliases.
	if err := restored.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	native := nativeSaveTestServer(t)
	native.WorldDir = restored.WorldDir
	if err := native.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	assertLatest(t, native.savedPlayers[DashUUID(uuid)])
}
