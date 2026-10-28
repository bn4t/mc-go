package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A late old transport cleanup runs after the successor's public save receipt.
// Compare all native player artifacts before shutdown can conceal stale writes.
func TestNativeDisconnectReplacedIncarnationPreservesSuccessorFiles(t *testing.T) {
	nativeDisconnectSuccessor(t, false, false)
}
func TestNativeDisconnectKickedIncarnationPreservesSuccessorFiles(t *testing.T) {
	nativeDisconnectSuccessor(t, true, false)
}
func TestNativeDisconnectKickedIncarnationAfterSuccessorDisconnect(t *testing.T) {
	nativeDisconnectSuccessor(t, true, true)
}
func nativeDisconnectSuccessor(t *testing.T, kick, successorLeft bool) {
	t.Helper()
	s := nativeSaveTestServer(t)
	old, _ := enforceSession(t, s, "NativeReplace")
	s.simulationAction(func() {
		old.inv[slotHotbarStart] = nestedDataSaveStack(1)
		old.addCustomStat("jump", 1)
		old.advCrits = map[string]map[string]int64{"minecraft:story/root": {"crafting_table": 1000}}
		old.advDone = map[string]bool{"minecraft:story/root": true}
	})
	if kick && !s.KickPlayer("NativeReplace", "test retirement") {
		t.Fatal("kick did not retire old identity")
	}
	current, _ := enforceSession(t, s, "NativeReplace")
	s.simulationAction(func() {
		current.inv[slotHotbarStart] = nestedDataSaveStack(7)
		current.addCustomStat("jump", 7)
		current.advCrits = map[string]map[string]int64{"minecraft:story/root": {"crafting_table": 7000}}
		current.advDone = map[string]bool{"minecraft:story/root": true}
	})
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	if successorLeft {
		s.simulationAction(func() { s.untrack(current) })
	}
	uuid := DashUUID(offlineUUID("NativeReplace"))
	jumpID, _ := CustomStatID("jump")
	jumpKey := statPersistKey(statKey{statCustom, jumpID})
	before := map[string][]byte{}
	for _, suffix := range []string{"data/" + uuid + ".dat", "stats/" + uuid + ".json", "advancements/" + uuid + ".json"} {
		path := filepath.Join(s.WorldDir, "players", suffix)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = raw
	}
	s.simulationAction(func() { s.untrack(old) })
	if successorLeft {
		if err := s.SaveWorld(); err != nil {
			t.Fatal(err)
		}
	}
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("late replaced disconnect overwrote successor native artifact %s", filepath.Base(filepath.Dir(path)))
		}
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	p := restarted.savedPlayers[uuid]
	if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 || p.Stats[jumpKey] != 7 || p.Advancements["minecraft:story/root"].Criteria["crafting_table"] != 7000 {
		t.Fatalf("successor native state lost on restart: count=%d jump=%d advancement=%+v", dataSaveInnerCount(p.Inv[slotHotbarStart]), p.Stats[jumpKey], p.Advancements["minecraft:story/root"])
	}
	if successorLeft {
		return
	}
	// Ordinary successor disconnect still persists its latest unsaved state.
	s.simulationAction(func() {
		current.inv[slotHotbarStart].Contents[1].Contents[2].Count = 9
		current.addCustomStat("jump", 2)
		current.advCrits["minecraft:story/root"]["crafting_table"] = 9000
		s.untrack(current)
	})
	latest := nativeSaveTestServer(t)
	latest.WorldDir = s.WorldDir
	if err := latest.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	p = latest.savedPlayers[uuid]
	if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 9 || p.Stats[jumpKey] != 9 || p.Advancements["minecraft:story/root"].Criteria["crafting_table"] != 9000 {
		t.Fatal("ordinary disconnect failed to persist current native state")
	}
}

func TestNativeDisconnectUncommittedSuccessorPreservesOrdinarySave(t *testing.T) {
	for _, stage := range []string{"configuring", "rejected"} {
		t.Run(stage, func(t *testing.T) {
			s := nativeSaveTestServer(t)
			old, _ := enforceSession(t, s, "NativePending")
			s.simulationAction(func() { old.inv[slotHotbarStart] = nestedDataSaveStack(7) })
			if !s.KickPlayer("NativePending", "retire before pending successor") {
				t.Fatal("kick failed")
			}
			if stage == "configuring" {
				joinPrepTestSession(t, s, "NativePending")
			} else {
				s.simulationAction(func() { s.MaxPlayers = 0 })
				// Admission failure at capacity must not transfer native write authority.
				pending := &Session{}
				pending.setIdentity("NativePending", offlineUUID("NativePending"))
				pending.playerRecord.java = pending
				s.simulationAction(func() {
					if err := s.reservePlayerOwned(context.Background(), &pending.playerRecord); err == nil {
						t.Fatal("full server admitted successor")
					}
				})
			}
			s.simulationAction(func() { s.untrack(old) })
			restored := nativeSaveTestServer(t)
			restored.WorldDir = s.WorldDir
			if err := restored.LoadWorld(); err != nil {
				t.Fatal(err)
			}
			p := restored.savedPlayers[DashUUID(offlineUUID("NativePending"))]
			if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 {
				t.Fatal("uncommitted successor suppressed ordinary native save")
			}
		})
	}
}
