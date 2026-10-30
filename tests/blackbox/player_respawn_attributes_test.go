package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// A controlled native-layout file separates the saved raw movement base from
// the walking ability, then exercises one real death and one respawn request.
func TestPlayerRespawnAttributeLifecycleParity(t *testing.T) {
	jar := blackboxJar(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "ops.json"), []byte(opsJSONFor("BodyAttrs")), 0600); err != nil {
		t.Fatal(err)
	}
	port, ok := freePort()
	if !ok {
		t.Skip("loopback unavailable")
	}
	var writer *server.Server
	addr := startMcGoCfg(t, port, func(s *server.Server) {
		writer = s
		s.WorldDir = filepath.Join(source, "world")
		s.ServerDir = source
		s.ViewDistance = 2
		s.OpsRequired = true
		if err := s.LoadAdminFiles(); err != nil {
			t.Fatal(err)
		}
	})
	conn, pr, pw := joinPlayBlackbox(t, addr, "BodyAttrs")
	awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
	sink := newChatSinkTP(pr, pw)
	sendCommand(t, pw, "say BODYSEEDREADY")
	deadline := time.Now().Add(12 * time.Second)
	seeded := false
	for !seeded && time.Now().Before(deadline) {
		seeded = structureSaidBB(sink.take(), "BODYSEEDREADY")
		time.Sleep(10 * time.Millisecond)
	}
	if !seeded {
		t.Fatal("source player not admitted")
	}
	conn.Close()
	deadline = time.Now().Add(10 * time.Second)
	for writer.OnlineCount() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if writer.OnlineCount() != 0 {
		t.Fatal("source retirement incomplete")
	}
	if err := writer.SaveWorld(); err != nil {
		t.Fatal(err)
	}

	for _, backend := range []string{"native", "go"} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			cpWorld(t, filepath.Join(source, "world"), filepath.Join(dir, "world"))
			files, err := filepath.Glob(filepath.Join(dir, "world", "players", "data", "*.dat"))
			if err != nil || len(files) != 1 {
				t.Fatalf("source player files%v %v", files, err)
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
			zr.Close()
			if err != nil {
				t.Fatal(err)
			}
			root := value.(map[string]any)
			root["abilities"].(map[string]any)["walkSpeed"] = float32(.3)
			attrs := root["attributes"].(nbt.List)
			found := false
			for _, value := range attrs.Items {
				attribute := value.(map[string]any)
				if attribute["id"] == "minecraft:movement_speed" {
					attribute["base"] = float64(.7)
					found = true
				}
			}
			if !found {
				t.Fatal("source movement attribute absent")
			}
			attrs.Items = append(attrs.Items, map[string]any{"id": "minecraft:max_health", "base": float64(30)}, map[string]any{"id": "minecraft:luck", "base": float64(10), "modifiers": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"id": "test:body_permanent", "amount": .25, "operation": "add_value"}}}})
			root["attributes"] = attrs
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
			port, ok := freePort()
			if !ok {
				t.Skip("loopback unavailable")
			}
			var stop func()
			if backend == "native" {
				var console func(string)
				var wait func() int
				addr, console, wait = vanillaConsole(t, jar, port, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("BodyAttrs")}, dir)
				stop = func() {
					console("stop")
					if code := wait(); code != 0 {
						t.Fatalf("native exit%d", code)
					}
				}
			} else {
				if err = os.WriteFile(filepath.Join(dir, "ops.json"), []byte(opsJSONFor("BodyAttrs")), 0600); err != nil {
					t.Fatal(err)
				}
				addr = startMcGoCfg(t, port, func(s *server.Server) {
					s.WorldDir = filepath.Join(dir, "world")
					s.ServerDir = dir
					s.ViewDistance = 2
					s.OpsRequired = true
					if err := s.LoadAdminFiles(); err != nil {
						t.Fatal(err)
					}
				})
				stop = func() {}
			}
			defer stop()
			conn, pr, pw := joinPlayBlackbox(t, addr, "BodyAttrs")
			defer conn.Close()
			awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
			sink := newChatSinkTP(pr, pw)
			dead, respawned := false, false
			ability := float32(math.NaN())
			serial := 0
			consume := func(lines []string) {
				for _, line := range lines {
					prefix, body, ok := strings.Cut(line, ":")
					if !ok {
						continue
					}
					rd := protocol.NewPacketReader([]byte(body))
					switch prefix {
					case fmt.Sprintf("%#x", server.IDPlayAbilitiesCB):
						_, _ = rd.Byte()
						_, _ = rd.Float()
						if speed, err := rd.Float(); err == nil {
							ability = speed
						}
					case fmt.Sprintf("%#x", server.IDPlaySetHealth):
						if health, err := rd.Float(); err == nil && health == 0 {
							dead = true
						}
					case fmt.Sprintf("%#x", server.IDPlayRespawn):
						respawned = true
					}
				}
			}
			collect := func(marker string) []string {
				var lines []string
				deadline := time.Now().Add(12 * time.Second)
				for !structureSaidBB(lines, marker) && time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					time.Sleep(10 * time.Millisecond)
				}
				consume(lines)
				if !structureSaidBB(lines, marker) {
					t.Fatalf("%s missing%s", backend, marker)
				}
				return lines
			}
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("BODYBARRIER%dX", serial)
				sendCommand(t, pw, "say "+marker)
				return collect(marker)
			}
			setupPlayerTickBarrierBB(t, pw)
			for _, command := range []string{"scoreboard objectives add BodyProof dummy", "difficulty peaceful", "gamerule minecraft:keep_inventory true", "gamerule minecraft:natural_health_regeneration false", "gamemode creative @s", "gamemode survival @s", "effect give @s glowing 60 0 true", "effect give @s absorption 60 0 true", "effect give @s levitation 60 0 true"} {
				sendCommand(t, pw, command)
			}
			sendCommand(t, pw, "execute store result score @s BodyProof run attribute @s movement_speed base get 1000000000")
			sendCommand(t, pw, "execute if score @s BodyProof matches 300000011 run say BODYBASEBEFORE")
			sendCommand(t, pw, "execute store result score @s BodyProof run attribute @s luck modifier value get test:body_permanent 100")
			sendCommand(t, pw, "execute if score @s BodyProof matches 25 run say BODYPERMBEFORE")
			sendCommand(t, pw, `execute if data entity @s {abilities:{walkSpeed:0.3f},AbsorptionAmount:4.0f,active_effects:[{id:"minecraft:glowing"},{id:"minecraft:absorption"},{id:"minecraft:levitation"}]} run say BODYSTATEBEFORE`)
			lines := barrier()
			for _, marker := range []string{"BODYBASEBEFORE", "BODYPERMBEFORE", "BODYSTATEBEFORE"} {
				if !structureSaidBB(lines, marker) {
					t.Fatalf("%s invalid pre-death state: missing%s", backend, marker)
				}
			}
			if ability != float32(.3) {
				t.Fatalf("%s custom walking packet not established: %v", backend, ability)
			}
			sendCommand(t, pw, "kill @s")
			deadline := time.Now().Add(15 * time.Second)
			for !dead && time.Now().Before(deadline) {
				barrier()
			}
			if !dead {
				t.Fatal("applied death health receipt absent")
			}
			ability = float32(math.NaN())
			var request protocol.Buffer
			request.VarInt(0)
			if err = pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes()); err != nil {
				t.Fatal(err)
			}
			deadline = time.Now().Add(15 * time.Second)
			for !respawned && time.Now().Before(deadline) {
				barrier()
			}
			if !respawned {
				t.Fatal("respawn receipt absent")
			}
			if err = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}
			waitPlayerTicksBB(t, pw, collect, "BODYAFTER", 5)
			t.Logf("%s initial respawn ability packet: %v", backend, ability)
			if ability != float32(.1) {
				t.Errorf("%s respawn ability %v want constructor .1", backend, ability)
			}
			// A forced mode transition projects the settled ability independently of
			// whether this backend emits an initial respawn ability packet.
			ability = float32(math.NaN())
			sendCommand(t, pw, "gamemode creative @s")
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "execute store result score @s BodyProof run attribute @s movement_speed base get 1000000000")
			sendCommand(t, pw, "execute if score @s BodyProof matches 300000011 run say BODYBASEAFTER")
			sendCommand(t, pw, "execute store success score @s BodyProof run attribute @s luck modifier value get test:body_permanent")
			sendCommand(t, pw, "execute if score @s BodyProof matches 0 run say BODYPERMDROPPED")
			sendCommand(t, pw, `execute if data entity @s {abilities:{walkSpeed:0.1f}} run say BODYWALKRESET`)
			sendCommand(t, pw, `execute if data entity @s {Health:30.0f} run say BODYMAXHEALTH`)
			sendCommand(t, pw, `execute unless data entity @s {active_effects:[{id:"minecraft:glowing"}]} unless data entity @s {active_effects:[{id:"minecraft:absorption"}]} unless data entity @s {active_effects:[{id:"minecraft:levitation"}]} run say BODYEFFECTCLEARED`)
			sendCommand(t, pw, `execute if data entity @s {AbsorptionAmount:0.0f} run say BODYABSORBRESET`)
			lines = barrier()
			for _, marker := range []string{"BODYBASEAFTER", "BODYPERMDROPPED", "BODYWALKRESET", "BODYMAXHEALTH", "BODYEFFECTCLEARED", "BODYABSORBRESET"} {
				present := structureSaidBB(lines, marker)
				t.Logf("%s %s=%v", backend, marker, present)
				if !present {
					t.Errorf("%s missing fresh-body result%s", backend, marker)
				}
			}
			if ability != float32(.1) {
				t.Errorf("%s settled new-body ability%v want%v", backend, ability, float32(.1))
			}
		})
	}
}
