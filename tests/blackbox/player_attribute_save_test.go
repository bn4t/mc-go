package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func playerAttributeSaveReceiptBB(t *testing.T, addr string, setup bool, walkSpeed float32) {
	t.Helper()
	conn, pr, pw := joinPlayBlackbox(t, addr, "AttrSave")
	defer conn.Close()
	awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
	sink := newChatSinkTP(pr, pw)
	collect := func(marker string) []string {
		var lines []string
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !structureSaidBB(lines, marker) {
			lines = append(lines, sink.take()...)
			time.Sleep(10 * time.Millisecond)
		}
		if !structureSaidBB(lines, marker) {
			t.Fatalf("%s missing %s", addr, marker)
		}
		return lines
	}
	setupPlayerTickBarrierBB(t, pw)
	if setup {
		for _, command := range []string{
			"attribute @s minecraft:scale base set 2",
			"attribute @s minecraft:scale modifier add test:permanent_scale 0.25 add_value",
			"attribute @s minecraft:attack_damage base set 7",
			"attribute @s minecraft:attack_damage modifier add test:permanent_damage 2 add_value",
			"attribute @s minecraft:movement_speed base set 0.2",
			"attribute @s minecraft:movement_speed modifier add test:permanent_speed 0.5 add_multiplied_base",
			"attribute @s minecraft:luck base set 2048",
			"attribute @s minecraft:luck modifier add test:permanent_luck -0.5 add_multiplied_total",
			"item replace entity @s weapon.mainhand with diamond_sword",
			"effect give @s speed 20 0 true",
			"attribute @s minecraft:movement_speed modifier remove minecraft:effect.speed",
			"attribute @s minecraft:movement_speed modifier add minecraft:effect.speed 0.75 add_multiplied_total",
			"attribute @s minecraft:luck modifier add minecraft:effect.luck 7 add_value",
		} {
			sendCommand(t, pw, command)
		}
	}
	waitPlayerTicksBB(t, pw, collect, "ATTRSAVEGEAR", 2)
	if setup {
		sendCommand(t, pw, "attribute @s minecraft:attack_damage modifier remove minecraft:base_attack_damage")
	}
	sendCommand(t, pw, "scoreboard objectives add AttrSaveProof dummy")
	movementWant := int(float64(walkSpeed) * 100)
	movementBitsWant := int(float64(walkSpeed) * 1e9)
	if setup {
		movementWant = 20
		movementBitsWant = 200000000
	}
	sendCommand(t, pw, "gamemode creative @s")
	sendCommand(t, pw, "gamemode adventure @s")
	queries := []struct {
		query  string
		result int
	}{
		{"scale base get 100", 200}, {"scale modifier value get test:permanent_scale 100", 25},
		{"attack_damage base get 100", 700}, {"attack_damage modifier value get test:permanent_damage 100", 200},
		{"movement_speed base get 100", movementWant},
		{"movement_speed base get 1000000000", movementBitsWant}, {"movement_speed modifier value get test:permanent_speed 100", 50},
		{"luck base get", 2048},
		{"luck modifier value get test:permanent_luck 100", -50},
		{"movement_speed modifier value get minecraft:effect.speed 1000", 750},
		{"luck modifier value get minecraft:effect.luck 100", 700},
	}
	for i, q := range queries {
		sendCommand(t, pw, "execute store result score @s AttrSaveProof run attribute @s minecraft:"+q.query)
		sendCommand(t, pw, fmt.Sprintf("execute if score @s AttrSaveProof matches %d run say ATTRSAVECHECK%dX", q.result, i))
	}
	gearWant := 600
	gearStore := "result"
	if setup {
		gearWant = 0
		gearStore = "success"
	}
	sendCommand(t, pw, "execute store "+gearStore+" score @s AttrSaveProof run attribute @s minecraft:attack_damage modifier value get minecraft:base_attack_damage 100")
	sendCommand(t, pw, fmt.Sprintf("execute if score @s AttrSaveProof matches %d run say ATTRSAVEGEARRESTORED", gearWant))
	sendCommand(t, pw, `execute if data entity @s {active_effects:[{id:"minecraft:speed"}]} run say ATTRSAVEEFFECTACTIVE`)
	sendCommand(t, pw, "say ATTRSAVECHECKEND")
	var lines []string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !structureSaidBB(lines, "ATTRSAVECHECKEND") {
		lines = append(lines, sink.take()...)
		time.Sleep(10 * time.Millisecond)
	}
	abilitiesOK := false
	prefix := fmt.Sprintf("%#x:", server.IDPlayAbilitiesCB)
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			rd := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix)))
			_, _ = rd.Byte()
			_, _ = rd.Float()
			got, err := rd.Float()
			if err == nil && got == walkSpeed {
				abilitiesOK = true
			}
		}
	}
	if !abilitiesOK {
		t.Errorf("%s ability walking speed receipt missing: want%v", addr, walkSpeed)
	}
	if !structureSaidBB(lines, "ATTRSAVEEFFECTACTIVE") {
		t.Errorf("%s active effect lifetime not restored", addr)
	}
	if !structureSaidBB(lines, "ATTRSAVEGEARRESTORED") {
		t.Errorf("%s held gear removal/reload modifier receipt missing (setup=%v)", addr, setup)
	}
	for i, q := range queries {
		if !structureSaidBB(lines, fmt.Sprintf("ATTRSAVECHECK%dX", i)) {
			t.Errorf("%s raw saved query %q did not return%d", addr, q.query, q.result)
		}
	}
	if !structureSaidBB(lines, "ATTRSAVECHECKEND") {
		t.Fatal("attribute save queries not completed")
	}
	if !setup {
		// Each bounded wait is under the helper's15s deadline. No action is retried.
		for i := 0; i < 3; i++ {
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("ATTRSAVEEXPIRE%d", i), 140)
		}
		sendCommand(t, pw, `execute unless data entity @s {active_effects:[{id:"minecraft:speed"}]} run say ATTRSAVEEFFECTEXPIRED`)
		sendCommand(t, pw, "execute store success score @s AttrSaveProof run attribute @s minecraft:movement_speed modifier value get minecraft:effect.speed")
		sendCommand(t, pw, "execute if score @s AttrSaveProof matches 0 run say ATTRSAVEEXPIREDMODREMOVED")
		sendCommand(t, pw, "execute store result score @s AttrSaveProof run attribute @s minecraft:luck modifier value get minecraft:effect.luck 100")
		sendCommand(t, pw, "execute if score @s AttrSaveProof matches 700 run say ATTRSAVECOMMANDIDRETAINED")
		sendCommand(t, pw, "say ATTRSAVEEXPIREEND")
		lines = collect("ATTRSAVEEXPIREEND")
		for _, marker := range []string{"ATTRSAVEEFFECTEXPIRED", "ATTRSAVEEXPIREDMODREMOVED", "ATTRSAVECOMMANDIDRETAINED"} {
			if !structureSaidBB(lines, marker) {
				t.Errorf("%s missing lifetime/collision receipt%s", addr, marker)
			}
		}
	}
}

// Both directions use the actual player file, not an mc-go-private sidecar.
func TestPlayerAttributeSaveParity(t *testing.T) {
	jar := blackboxJar(t)
	source := t.TempDir()
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	addr, console, wait := vanillaConsole(t, jar, port, nil, map[string]string{"ops.json": opsJSONFor("AttrSave")}, source)
	playerAttributeSaveReceiptBB(t, addr, true, .1)
	console("save-all flush")
	console("stop")
	if code := wait(); code != 0 {
		t.Fatalf("native exit%d", code)
	}

	bootGo := func(t *testing.T, worldDir string) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(opsJSONFor("AttrSave")), 0600); err != nil {
			t.Fatal(err)
		}
		port, ok := freePort()
		if !ok {
			t.Skip("loopback unavailable")
		}
		return startMcGoCfg(t, port, func(s *server.Server) {
			s.WorldDir = worldDir
			s.ServerDir = dir
			s.OpsRequired = true
			if err := s.LoadAdminFiles(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("native-to-go", func(t *testing.T) {
		dir := t.TempDir()
		cpWorld(t, filepath.Join(source, "world"), filepath.Join(dir, "world"))
		playerAttributeSaveReceiptBB(t, bootGo(t, filepath.Join(dir, "world")), false, .1)
	})
	for _, backend := range []string{"native", "go"} {
		t.Run("custom-walk-to-"+backend, func(t *testing.T) {
			dir := t.TempDir()
			cpWorld(t, filepath.Join(source, "world"), filepath.Join(dir, "world"))
			files, err := filepath.Glob(filepath.Join(dir, "world", "players", "data", "*.dat"))
			if err != nil || len(files) != 1 {
				t.Fatalf("player files%v %v", files, err)
			}
			raw, err := os.ReadFile(files[0])
			if err != nil {
				t.Fatal(err)
			}
			zr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			_, value, err := nbt.ReadNamed(zr)
			_ = zr.Close()
			if err != nil {
				t.Fatal(err)
			}
			root := value.(map[string]any)
			abilities := root["abilities"].(map[string]any)
			abilities["walkSpeed"] = float32(.3)
			// Only the ability changes. Raw saved movement base remains the command's.2.
			attributes := root["attributes"].(nbt.List)
			rawBaseOK := false
			for _, record := range attributes.Items {
				m := record.(map[string]any)
				if m["id"] == "minecraft:movement_speed" {
					rawBaseOK = m["base"] == float64(.2)
				}
			}
			if !rawBaseOK {
				t.Fatal("source raw movement base not.2")
			}
			var encoded bytes.Buffer
			zw := gzip.NewWriter(&encoded)
			if err = nbt.WriteNamed(zw, "", root); err != nil {
				t.Fatal(err)
			}
			if err = zw.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(files[0], encoded.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if backend == "go" {
				playerAttributeSaveReceiptBB(t, bootGo(t, filepath.Join(dir, "world")), false, .3)
				return
			}
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			addr, console, wait := vanillaConsole(t, jar, port, nil, map[string]string{"ops.json": opsJSONFor("AttrSave")}, dir)
			playerAttributeSaveReceiptBB(t, addr, false, .3)
			console("stop")
			if code := wait(); code != 0 {
				t.Fatalf("native exit%d", code)
			}
		})
	}
	t.Run("go-to-native", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(opsJSONFor("AttrSave")), 0600); err != nil {
			t.Fatal(err)
		}
		port, ok := freePort()
		if !ok {
			t.Skip("loopback unavailable")
		}
		var core *server.Server
		addr := startMcGoCfg(t, port, func(s *server.Server) {
			core = s
			s.WorldDir = filepath.Join(dir, "world")
			s.ServerDir = dir
			s.OpsRequired = true
			if err := s.LoadAdminFiles(); err != nil {
				t.Fatal(err)
			}
		})
		playerAttributeSaveReceiptBB(t, addr, true, .1)
		deadline := time.Now().Add(10 * time.Second)
		for core.OnlineCount() != 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if core.OnlineCount() != 0 {
			t.Fatal("Go writer did not retire before save")
		}
		if err := core.SaveWorld(); err != nil {
			t.Fatal(err)
		}
		nativeDir := t.TempDir()
		cpWorld(t, filepath.Join(dir, "world"), filepath.Join(nativeDir, "world"))
		port, ok = freePort()
		if !ok {
			t.Skip("loopback unavailable")
		}
		addr, console, wait := vanillaConsole(t, jar, port, nil, map[string]string{"ops.json": opsJSONFor("AttrSave")}, nativeDir)
		playerAttributeSaveReceiptBB(t, addr, false, .1)
		console("stop")
		if code := wait(); code != 0 {
			t.Fatalf("native exit%d", code)
		}
	})
}
