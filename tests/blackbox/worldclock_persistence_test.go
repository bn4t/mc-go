//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Both save directions retain raw rate/pause/fraction, rather than the
// effective zero rate projected to clients while advance_time is disabled.
func TestWorldClockSaveCopyParity(t *testing.T) {
	const name = "ClockSave"
	jar := blackboxJar(t)
	for _, sourceKind := range []string{"vanilla", "mc-go"} {
		t.Run(sourceKind, func(t *testing.T) {
			source := t.TempDir()
			payload := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{
				"minecraft:overworld": map[string]any{"total_ticks": int64(500), "partial_tick": float32(.25), "rate": float32(.5)},
				"minecraft:the_end":   map[string]any{"total_ticks": int64(42), "partial_tick": float32(.75), "rate": float32(2), "paused": int8(1)},
			}}
			if err := world.WriteLevel(world.LevelData{Dir: filepath.Join(source, "world"), GameRules: map[string]string{"minecraft:advance_time": "false"}}); err != nil {
				t.Fatal(err)
			}
			clockPath := func(dir string) string { return filepath.Join(dir, "world", "data", "minecraft", "world_clocks.dat") }
			if err := os.WriteFile(clockPath(source), []byte(clockFallbackNBTBB(t, payload)), 0644); err != nil {
				t.Fatal(err)
			}
			start := func(kind, dir string) (string, func()) {
				port, ok := freePort()
				if !ok {
					t.Skip("loopback unavailable")
				}
				files := map[string]string{"ops.json": opsJSONFor(name)}
				if kind == "vanilla" {
					return startVanillaInDir(t, jar, port, nil, files, dir)
				}
				if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(files["ops.json"]), 0644); err != nil {
					t.Fatal(err)
				}
				var runtime *server.Server
				addr := startMcGoCfg(t, port, func(s *server.Server) {
					runtime = s
					s.WorldDir = filepath.Join(dir, "world")
					s.ServerDir = dir
					s.SetOpLevel(name, 4)
				})
				return addr, func() { runtime.Stop() }
			}
			type probe struct {
				command  func(string) []string
				snapshot func(string, int) worldClockPacketBB
				age      func() int64
				close    func()
			}
			join := func(kind, addr string) probe {
				c, pr, pw := joinPlayBlackboxView(t, addr, name, 1)
				drainToJoinBlackbox(t, kind, pr)
				awaitPlayReadyBlackbox(t, kind, c, pr, pw)
				sink := newChatSinkTP(pr, pw)
				serial := 0
				command := func(cmd string) []string {
					serial++
					marker := fmt.Sprintf("CLOCK_SAVE_%d", serial)
					sendCommand(t, pw, cmd)
					sendCommand(t, pw, "say "+marker)
					return waitForExactSayBB(t, sink, marker, 10*time.Second)
				}
				snapshot := func(label string, index int) worldClockPacketBB {
					oc, opr, opw := joinPlayBlackboxView(t, addr, fmt.Sprintf("ClockSaved%d", index), 1)
					defer oc.Close()
					drainToJoinBlackbox(t, kind, opr)
					other := newChatSinkTP(opr, opw)
					marker := fmt.Sprintf("CLOCK_SAVE_OBS_%d", index)
					command("say " + marker)
					lines := waitForExactSayBB(t, other, marker, 10*time.Second)
					var packets []worldClockPacketBB
					for _, p := range worldClockPacketsBB(t, lines) {
						if len(p.rows) == 2 {
							packets = append(packets, p)
						}
					}
					if len(packets) != 1 {
						t.Fatalf("%s clock snapshots%#v", label, packets)
					}
					return packets[0]
				}
				command("tick freeze")
				return probe{command, snapshot, func() int64 { return gameTimeOf(t, pw, sink) }, func() { c.Close() }}
			}
			readSaved := func(dir string) map[string]any {
				f, err := os.Open(clockPath(dir))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				v, err := readGzipNBTBlackbox(f)
				if err != nil {
					t.Fatal(err)
				}
				root, ok := v.(map[string]any)
				if !ok {
					t.Fatal("non-compound clock save")
				}
				return root
			}
			addr, stop := start(sourceKind, source)
			p := join(sourceKind, addr)
			initial := p.snapshot("source", 0)
			rows := []worldClockRowBB{{0, 500, .25, 0}, {1, 42, .75, 0}}
			assertWorldClockPacketBB(t, "source", initial, initial.age, rows)
			p.command("save-all flush")
			if got := readSaved(source); !reflect.DeepEqual(got, payload) {
				t.Fatalf("source save%#v want%#v", got, payload)
			}
			// Copy the acknowledged save while the source is still running; shutdown
			// cannot replace the evidence consumed by the second implementation.
			target := t.TempDir()
			copyTree(t, source, target)
			p.close()
			stop()
			targetKind := "vanilla"
			if sourceKind == "vanilla" {
				targetKind = "mc-go"
			}
			addr, stop = start(targetKind, target)
			defer stop()
			p = join(targetKind, addr)
			defer p.close()
			restored := p.snapshot("restored", 1)
			assertWorldClockPacketBB(t, "restored", restored, restored.age, rows)
			lines := p.command("gamerule minecraft:advance_time true")
			var updates []worldClockPacketBB
			for _, packet := range worldClockPacketsBB(t, lines) {
				if len(packet.rows) > 0 {
					updates = append(updates, packet)
				}
			}
			rows[0].rate = .5
			if !reflect.DeepEqual(updates, []worldClockPacketBB{{restored.age, rows}}) {
				t.Fatalf("resume updates%#v", updates)
			}
			before := p.age()
			p.command("tick step 2")
			deadline := time.Now().Add(10 * time.Second)
			for p.age() < before+2 {
				if time.Now().After(deadline) {
					t.Fatal("step did not finish")
				}
			}
			rows[0].total = 501
			assertWorldClockPacketBB(t, "progressed", p.snapshot("progressed", 2), before+2, rows)
			p.command("save-all flush")
			payload["data"].(map[string]any)["minecraft:overworld"].(map[string]any)["total_ticks"] = int64(501)
			if got := readSaved(target); !reflect.DeepEqual(got, payload) {
				t.Fatalf("target save%#v want%#v", got, payload)
			}
		})
	}
}
