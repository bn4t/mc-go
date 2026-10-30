//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
)

// Fluid activation is witnessed by exact saved-delay decrement after3ticks.
// Clone is deliberately absent: native clone copies block ticks, not fluids.
// Unload persistence is observed without save-all during the cold interval.
func TestNativeScheduledFluidAutomaticUnloadMeasure(t *testing.T) {
	scheduledAutomaticFluidUnloadBB(t, false)
}
func TestGoScheduledFluidAutomaticUnloadMeasure(t *testing.T) {
	scheduledAutomaticFluidUnloadBB(t, true)
}
func scheduledAutomaticFluidUnloadBB(t *testing.T, goTarget bool) {
	jar := blackboxJar(t)
	source := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	if err := os.WriteFile(filepath.Join(source, "ops.json"), []byte(opsJSONFor("ScheduledSave")), 0644); err != nil {
		t.Fatal(err)
	}
	var runtime *server.Server
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		runtime = s
		s.GameMode = server.GameModeCreative
		s.WorldDir = filepath.Join(source, "world")
		s.ServerDir = source
	})
	p, closeSource := scheduledSaveJoinBB(t, "source", addr, "ScheduledSave")
	armScheduledSaveBB(p)
	awaitSavedTickTargetsBB(t, savedTickRegionBB(source))
	dir := t.TempDir()
	copyScheduledSaveBB(t, source, dir)
	closeSource()
	runtime.Stop()
	chunks, err := world.ReadRegionFile(savedTickRegionBB(dir))
	if err != nil {
		t.Fatal(err)
	}
	far := chunks[[2]int32{}]
	far["xPos"], far["zPos"] = int32(250), int32(0)
	far["block_ticks"] = nbt.List{Elem: nbt.TagCompound}
	far["fluid_ticks"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"i": "minecraft:water", "x": int32(4004), "y": int32(-60), "z": int32(12), "t": int32(5000), "p": int32(0)}}}
	path := filepath.Join(filepath.Dir(savedTickRegionBB(dir)), "r.7.0.mca")
	writeLoaderRegionBB(t, path, map[[2]int32]map[string]any{{26, 0}: far})
	port, ok = freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	var stop func()
	var loaded *server.Server
	if goTarget {
		if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(opsJSONFor("ScheduledRead")), 0644); err != nil {
			t.Fatal(err)
		}
		addr = startMcGoCfg(t, port, func(s *server.Server) {
			loaded = s
			s.GameMode = server.GameModeCreative
			s.ViewDistance = 2
			s.SimulationDistance = 2
			s.WorldDir = filepath.Join(dir, "world")
			s.ServerDir = dir
		})
		stop = loaded.Stop
	} else {
		addr, stop = startVanillaInDir(t, jar, port, map[string]string{"gamemode": "creative", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("ScheduledRead")}, dir)
	}
	defer stop()
	p, closeRead := scheduledSaveJoinBB(t, "native", addr, "ScheduledRead")
	defer closeRead()
	loadFar := func(label string) {
		p.command("tp @s 4008.5 -60 5.5 180 56")
		p.moveReady(4008.6, -60, 5.5)
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				p.await(fmt.Sprintf("if loaded %d -60 %d", 4008+16*dx, 4+16*dz), fmt.Sprintf("%s_%d_%d", label, dx, dz))
			}
		}
	}
	tryDelayAt := func(x int32) (int32, int64, bool, bool, error) {
		data, err := world.ReadRegionFile(path)
		if err != nil {
			return 0, 0, false, false, err
		}
		chunk := data[[2]int32{26, 0}]
		marker := false
		sections, _ := chunk["sections"].(nbt.List)
		for _, value := range sections.Items {
			section, _ := value.(map[string]any)
			states, _ := section["block_states"].(map[string]any)
			palette, _ := states["palette"].(nbt.List)
			for _, entry := range palette.Items {
				state, _ := entry.(map[string]any)
				if state["Name"] == "minecraft:gold_block" {
					marker = true
				}
			}
		}
		rows, _ := chunk["fluid_ticks"].(nbt.List)
		for _, value := range rows.Items {
			row, _ := value.(map[string]any)
			if row["i"] == "minecraft:water" && row["x"] == x {
				delay, ok := row["t"].(int32)
				last, lastOK := chunk["LastUpdate"].(int64)
				return delay, last, ok && lastOK, marker, nil
			}
		}
		return 0, 0, false, marker, nil
	}
	delayAt := func(x int32) (int32, int64) {
		p.command("save-all flush")
		if loaded != nil {
			if err := loaded.SaveWorld(); err != nil {
				t.Fatal(err)
			}
		}
		data, err := world.ReadRegionFile(path)
		if err != nil {
			t.Fatal(err)
		}
		chunk := data[[2]int32{26, 0}]
		rows, _ := chunk["fluid_ticks"].(nbt.List)
		for _, value := range rows.Items {
			row, _ := value.(map[string]any)
			if row["i"] == "minecraft:water" && row["x"] == x {
				delay, ok := row["t"].(int32)
				if !ok {
					t.Fatalf("delay type %#v", row)
				}
				last, _ := chunk["LastUpdate"].(int64)
				return delay, last
			}
		}
		t.Fatalf("missing owner%d rows%#v", x, rows)
		return 0, 0
	}
	loadFar("initial")
	initialAge := gameTimeOf(t, p.pw, p.sink)
	initial, last := delayAt(4004)
	if initial != 5000 {
		t.Fatalf("initial fluid saved delay%d want5000", initial)
	}
	t.Logf("ACTIVE age=%d delay=%d LastUpdate=%d absolute=%d", initialAge, initial, last, initialAge+int64(initial))
	p.step(3)
	clean, cleanLast := delayAt(4004)
	cleanAge := gameTimeOf(t, p.pw, p.sink)
	t.Logf("CLEAN_READ age=%d delay=%d LastUpdate=%d", cleanAge, clean, cleanLast)
	// A harmless remote edit makes this save a fresh chunk snapshot. It does
	// not touch the fluid, its neighbours, or its existing queue deadline.
	p.command("setblock 4002 -59 1 diamond_block strict")
	active, activeLast := delayAt(4004)
	activeAge := gameTimeOf(t, p.pw, p.sink)
	t.Logf("ACTIVATED age=%d delay=%d LastUpdate=%d absolute=%d", activeAge, active, activeLast, activeAge+int64(active))
	if activeAge != initialAge+3 || active != initial-3 || (!goTarget && activeLast != activeAge) {
		t.Fatalf("fresh fluid activation witness age%d want%d delay%d want%d LastUpdate%d", activeAge, initialAge+3, active, initial-3, activeLast)
	}

	p.expect("if block 4004 -60 12 water", "active_source")
	_, _, _, markerBefore, markerErr := tryDelayAt(4004)
	if markerErr != nil || markerBefore {
		t.Fatalf("unload marker must be absent before departure: present%v err%v", markerBefore, markerErr)
	}
	p.command("setblock 4001 -59 1 gold_block strict") // require a fresh unload write
	p.command("tp @s 8.5 -60 5.5 180 56")
	p.moveReady(8.6, -60, 5.5)
	p.await("if loaded 8 -60 4", "home_ready")
	leftAge := gameTimeOf(t, p.pw, p.sink)
	if leftAge != initialAge+3 {
		t.Fatalf("leave advanced age%d want%d", leftAge, initialAge+3)
	}
	// Frozen residency varies with temporary tickets and observation timing.
	// Neither a retained nor an absent FULL holder is assumed here.
	time.Sleep(time.Second)
	full := p.command("execute if block 4004 -60 12 water run say UNLOAD_FULL_PRESENT")
	unloaded := !structureSaidBB(full, "UNLOAD_FULL_PRESENT")
	t.Logf("FROZEN_FULL age=%d present=%v", gameTimeOf(t, p.pw, p.sink), !unloaded)
	if age := gameTimeOf(t, p.pw, p.sink); age != leftAge {
		t.Fatalf("frozen wait advanced age%d→%d", leftAge, age)
	}
	p.command("tick rate 1000")
	for round := 0; round < 20 && !unloaded; round++ {
		p.step(20)
		lines := p.command("execute if block 4004 -60 12 water run say UNLOAD_FULL_PRESENT")
		t.Logf("FULL_CHECK age=%d present=%v", gameTimeOf(t, p.pw, p.sink), structureSaidBB(lines, "UNLOAD_FULL_PRESENT"))
		if !structureSaidBB(lines, "UNLOAD_FULL_PRESENT") {
			for _, line := range lines {
				if tree, ok := componentTree(line, true); ok {
					t.Logf("UNLOAD_DIAGNOSTIC %s", tree)
				}
			}
			unloaded = true
			break
		}
	}
	if !unloaded {
		t.Fatal("no FULL-unavailable receipt during400 exact worldticks")
	}
	unloadedAge := gameTimeOf(t, p.pw, p.sink)
	var packed int32
	var packedLast int64
	var lastReadErr error
	var packedMarker bool
	fresh := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var valid bool
		packed, packedLast, valid, packedMarker, lastReadErr = tryDelayAt(4004)
		if valid && packedMarker && (goTarget && packed == initial-int32(unloadedAge-initialAge) || !goTarget && packedLast >= leftAge && packedLast <= unloadedAge && int64(packed)+packedLast == initialAge+int64(initial)) {
			fresh = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !fresh {
		t.Fatalf("automatic unload save missing without save-all: delay%d LastUpdate%d left%d now%d marker%v err%v", packed, packedLast, leftAge, unloadedAge, packedMarker, lastReadErr)
	}
	if age := gameTimeOf(t, p.pw, p.sink); age != unloadedAge {
		t.Fatalf("read-only automatic-save wait advanced age%d→%d", unloadedAge, age)
	}
	t.Logf("UNLOADED age=%d packedDelay=%d LastUpdate=%d", unloadedAge, packed, packedLast)
	if !goTarget && (packedLast < leftAge || packedLast > unloadedAge || int64(packed)+packedLast != initialAge+int64(initial)) {
		t.Fatalf("unload freshness/deadline mismatch packed%d LastUpdate%d left%d now%d original%d", packed, packedLast, leftAge, unloadedAge, initialAge+int64(initial))
	}
	for i := 0; i < 5; i++ {
		p.step(20)
	}
	cold, coldLast, coldOK, coldMarker, coldErr := tryDelayAt(4004)
	if !coldOK || !coldMarker || coldErr != nil {
		t.Fatalf("cold read err%v present%v marker%v", coldErr, coldOK, coldMarker)
	}
	coldAge := gameTimeOf(t, p.pw, p.sink)
	t.Logf("COLD age=%d packedDelay=%d LastUpdate=%d", coldAge, cold, coldLast)
	if coldAge != unloadedAge+100 || cold != packed || coldLast != packedLast {
		t.Fatalf("cold interval changed saved delay/header: age%d delay%d LastUpdate%d", coldAge, cold, coldLast)
	}
	loadFar("return")
	p.expect("if block 4004 -60 12 water", "return_source")
	returned, returnLast := delayAt(4004)
	returnAge := gameTimeOf(t, p.pw, p.sink)
	t.Logf("RETURN age=%d fluidDelay=%d LastUpdate=%d absolute=%d oldAbsolute=%d", returnAge, returned, returnLast, returnAge+int64(returned), initialAge+int64(initial))
	if returnAge != coldAge || returned != packed {
		t.Fatalf("reload failed to resume packed delay: age%d want%d delay%d want%d", returnAge, coldAge, returned, packed)
	}
	p.step(3)
	p.command("setblock 4003 -59 1 emerald_block strict")
	resumed, resumedLast := delayAt(4004)
	resumedAge := gameTimeOf(t, p.pw, p.sink)
	t.Logf("RESUMED age=%d delay=%d LastUpdate=%d absolute=%d", resumedAge, resumed, resumedLast, resumedAge+int64(resumed))
	if resumedAge != returnAge+3 || resumed != packed-3 || (!goTarget && resumedLast != resumedAge) {
		t.Fatalf("fluid failed to resume active absolute deadline age%d want%d delay%d want%d", resumedAge, returnAge+3, resumed, packed-3)
	}

}
