//go:build blackbox

package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func scheduledEventsFileBB(t *testing.T, events []any) []byte {
	t.Helper()
	var raw, out bytes.Buffer
	if err := nbt.WriteNamed(&raw, "", map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"events": nbt.List{Elem: 10, Items: events}}}); err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func readScheduledEventsFileBB(path string) ([]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	_, value, err := nbt.ReadNamed(zr)
	if err != nil {
		return nil, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("noncompound root")
	}
	data, ok := root["data"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing data")
	}
	events, ok := data["events"].(nbt.List)
	if !ok {
		return nil, fmt.Errorf("missing events")
	}
	return events.Items, nil
}

func TestCustomFunctionSchedulePersistenceParity(t *testing.T) {
	const name = "ScheduleSave"
	jar := blackboxJar(t)
	event := func(id string, age int64) any {
		return map[string]any{"id": id, "trigger_time": age, "callback": map[string]any{"type": "minecraft:function", "id": "persistpin:a"}}
	}
	common := map[string]string{
		"ops.json":                               opsJSONFor(name),
		"world/datapacks/persistpin/pack.mcmeta": `{"pack":{"description":"schedule persistence","min_format":107,"max_format":107}}`,
		"world/datapacks/persistpin/data/persistpin/function/a.mcfunction": "setblock 8 -60 8 minecraft:diamond_block\n",
		"world/datapacks/persistpin/data/persistpin/function/b.mcfunction": "setblock 8 -60 8 minecraft:emerald_block\n",
	}
	for _, kind := range []string{"vanilla", "mc-go"} {
		t.Run(kind, func(t *testing.T) {
			source := t.TempDir()
			saved := filepath.Join(source, "world", "data", "minecraft", "scheduled_events.dat")
			if err := os.MkdirAll(filepath.Dir(saved), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(saved, scheduledEventsFileBB(t, []any{event("persistpin:seed", 200000), event("persistpin:seed", 200001)}), 0644); err != nil {
				t.Fatal(err)
			}
			start := func(dir string) (string, func()) {
				port, ok := freePort()
				if !ok {
					t.Skip("loopback unavailable")
				}
				if kind == "vanilla" {
					return startVanillaInDir(t, jar, port, nil, common, dir)
				}
				for path, body := range common {
					p := filepath.Join(dir, path)
					if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, []byte(body), 0644); err != nil {
						t.Fatal(err)
					}
				}
				var runtime *server.Server
				addr := startMcGoCfg(t, port, func(s *server.Server) { runtime = s; s.WorldDir = filepath.Join(dir, "world"); s.ServerDir = dir })
				return addr, func() { runtime.Stop() }
			}
			addr, stop := start(source)
			type probe struct {
				command func(string) []string
				age     func() int64
				step    func(int)
				close   func()
			}
			join := func(addr string) probe {
				c, pr, pw := joinPlayBlackbox(t, addr, name)
				drainToJoinBlackbox(t, kind, pr)
				awaitPlayReadyBlackbox(t, kind, c, pr, pw)
				sink := newChatSinkTP(pr, pw)
				serial := 0
				command := func(cmd string) []string {
					sendCommand(t, pw, cmd)
					serial++
					marker := fmt.Sprintf("SCHEDULE_SAVE_END_%d", serial)
					sendCommand(t, pw, "say "+marker)
					return waitForExactSayBB(t, sink, marker, 10*time.Second)
				}
				age := func() int64 { return gameTimeOf(t, pw, sink) }
				step := func(n int) {
					before := age()
					command(fmt.Sprintf("tick step %d", n))
					deadline := time.Now().Add(20 * time.Second)
					for time.Now().Before(deadline) {
						if age() == before+int64(n) {
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					t.Fatalf("step%d did not complete", n)
				}
				command("tick freeze")
				return probe{command, age, step, func() { c.Close() }}
			}
			p := join(addr)
			expect := func(p probe, condition, label string) {
				marker := "SCHEDULE_SAVE_RESULT_" + label
				if !structureSaidBB(p.command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("%s: %s", label, condition)
				}
			}
			p.command("scoreboard objectives add schedsave dummy")
			p.command("execute store result score cleared schedsave run schedule clear persistpin:seed")
			expect(p, "if score cleared schedsave matches 2", "native_seed_loaded")
			p.command("setblock 8 -60 8 air")
			before := p.age()
			p.command("schedule function persistpin:a 1000t append")
			p.command("schedule function persistpin:b 1001t append")
			p.command("save-all flush")
			deadline := time.Now().Add(10 * time.Second)
			var events []any
			for time.Now().Before(deadline) {
				var err error
				events, err = readScheduledEventsFileBB(saved)
				if err == nil && len(events) == 2 {
					good := true
					for i, id := range []string{"persistpin:a", "persistpin:b"} {
						m, ok := events[i].(map[string]any)
						if !ok || m["id"] != id || m["trigger_time"] != before+1000+int64(i) {
							good = false
						}
					}
					if good {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			if len(events) != 2 {
				t.Fatalf("saved events unavailable: %v", events)
			}
			for i, id := range []string{"persistpin:a", "persistpin:b"} {
				m, ok := events[i].(map[string]any)
				if !ok || m["id"] != id || m["trigger_time"] != before+1000+int64(i) {
					t.Fatalf("saved event%d mismatch: %v", i, events[i])
				}
				callback, ok := m["callback"].(map[string]any)
				if !ok || callback["type"] != "minecraft:function" || callback["id"] != id {
					t.Fatalf("saved callback%d mismatch: %v", i, m)
				}
			}
			// Copy only the completed metadata and pack, before disconnect can save.
			restart := t.TempDir()
			for _, relative := range []string{"level.dat", "data/minecraft/world_gen_settings.dat", "data/minecraft/world_clocks.dat", "data/minecraft/game_rules.dat", "data/minecraft/scheduled_events.dat"} {
				raw, err := os.ReadFile(filepath.Join(source, "world", relative))
				if err != nil {
					t.Fatal(err)
				}
				dest := filepath.Join(restart, "world", relative)
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dest, raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
			p.close()
			stop()
			addr, stop = start(restart)
			defer stop()
			p = join(addr)
			defer p.close()
			current := p.age()
			remaining := before + 1000 - current
			if remaining <= 1 {
				t.Fatalf("restart consumed scheduled boundary before probe: now%d deadline%d", current, before+1000)
			}
			p.command("tick rate 1000")
			p.step(int(remaining - 1))
			expect(p, "if block 8 -60 8 minecraft:air", "restart_before_deadline")
			p.step(1)
			expect(p, "if block 8 -60 8 minecraft:diamond_block", "restart_first_deadline")
			p.step(1)
			expect(p, "if block 8 -60 8 minecraft:emerald_block", "restart_second_deadline")
		})
	}
}
