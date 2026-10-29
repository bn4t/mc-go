//go:build blackbox

package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func clockFallbackNBTBB(t *testing.T, value any) string {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	if err := nbt.WriteNamed(gz, "", value); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.String()
}
func TestWorldClockFallbackMissing(t *testing.T) { worldClockFallbackBB(t, "missing") }
func TestWorldClockFallbackEmpty(t *testing.T)   { worldClockFallbackBB(t, "empty") }
func TestWorldClockFallbackPartial(t *testing.T) { worldClockFallbackBB(t, "partial") }
func TestWorldClockFallbackUnknown(t *testing.T) { worldClockFallbackBB(t, "unknown") }
func TestWorldClockFallbackInvalid(t *testing.T) { worldClockFallbackBB(t, "invalid") }
func worldClockFallbackBB(t *testing.T, variant string) {
	const name = "ClockFallback"
	seed := t.TempDir()
	if err := world.WriteLevel(world.LevelData{Dir: seed, LevelName: "clock fallback", GameType: 1, Difficulty: "normal", Seed: 1, GameTime: 4000, DayTime: 8000, Spawn: [3]int{0, 70, 0}, GameRules: map[string]string{"minecraft:advance_time": "false"}}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"ops.json": opsJSONFor(name)}
	if err := filepath.WalkDir(seed, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(seed, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files["world/"+filepath.ToSlash(relative)] = string(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	root, e := readGzipNBTBlackbox(bytes.NewReader([]byte(files["world/level.dat"])))
	if e != nil {
		t.Fatal(e)
	}
	root.(map[string]any)["Data"].(map[string]any)["DayTime"] = int64(8000)
	files["world/level.dat"] = clockFallbackNBTBB(t, root)
	clock := func(value int64) any { return map[string]any{"total_ticks": value} }
	payload := map[string]any{}
	switch variant {
	case "missing":
		delete(files, "world/data/minecraft/world_clocks.dat")
	case "empty":
	case "partial":
		payload["minecraft:overworld"] = clock(100)
	case "unknown":
		payload["minecraft:overworld"] = clock(100)
		payload["minecraft:the_end"] = clock(200)
		payload["testpin:absent"] = clock(300)
	case "invalid":
		payload["minecraft:overworld"] = clock(100)
		payload["minecraft:the_end"] = map[string]any{}
	}
	if variant != "missing" {
		files["world/data/minecraft/world_clocks.dat"] = clockFallbackNBTBB(t, map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": payload})
	}
	directory := worldClockDirectoryBB(t, files)
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, files, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.WorldDir = directory
		s.SetOpLevel(name, 4)
	})
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, tc.addr, name, 1)
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("WCF_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 10*time.Second)
			}
			lines := command("tick freeze")
			deadline := time.Now().Add(5 * time.Second)
			var initial *worldClockPacketBB
			for initial == nil {
				for _, packet := range worldClockPacketsBB(t, lines) {
					if len(packet.rows) == 2 {
						copy := packet
						initial = &copy
						break
					}
				}
				if initial == nil {
					if time.Now().After(deadline) {
						t.Fatal("initial clock snapshot missing")
					}
					time.Sleep(10 * time.Millisecond)
					lines = append(lines, sink.take()...)
				}
			}
			day, end := int64(0), int64(0)
			switch variant {
			case "partial", "invalid":
				day = 100
			case "unknown":
				day = 100
				end = 200
			}
			if initial.age < 4000 {
				t.Fatal("seeded world age lost", initial.age)
			}
			assertWorldClockPacketBB(t, variant, *initial, initial.age, []worldClockRowBB{{0, day, 0, 0}, {1, end, 0, 0}})
			command(fmt.Sprintf("time set %d", day))
			command("save-all flush")
			savedDir := directory
			if tc.name == "vanilla" {
				savedDir = filepath.Join(vanillaDirForDebug, "world")
			}
			file, err := os.Open(filepath.Join(savedDir, "data", "minecraft", "world_clocks.dat"))
			if err != nil {
				t.Fatal(err)
			}
			saved, err := readGzipNBTBlackbox(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"minecraft:overworld": map[string]any{"total_ticks": day}, "minecraft:the_end": map[string]any{"total_ticks": end}}}
			if !reflect.DeepEqual(saved, want) {
				t.Fatalf("%s saved clocks %#v want%#v", variant, saved, want)
			}

		})
	}
}
