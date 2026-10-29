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

func TestWorldClockCommandsActiveParity(t *testing.T) { worldClockCommandsBB(t, false) }
func TestWorldClockCommandsPausedParity(t *testing.T) { worldClockCommandsBB(t, true) }
func worldClockCommandsBB(t *testing.T, paused bool) {
	const name = "ClockCommands"
	ow := map[string]any{"total_ticks": int64(500), "partial_tick": float32(.25), "rate": float32(.5)}
	if paused {
		ow["paused"] = int8(1)
	}
	payload := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"minecraft:overworld": ow, "minecraft:the_end": map[string]any{"total_ticks": int64(42), "partial_tick": float32(.75), "rate": float32(2), "paused": int8(1)}}}
	files := map[string]string{"ops.json": opsJSONFor(name), "world/data/minecraft/world_clocks.dat": clockFallbackNBTBB(t, payload)}
	directory := worldClockDirectoryBB(t, files)
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) {
		s.WorldDir = directory
		s.GameMode = server.GameModeCreative
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
			phase := "initial"
			seen := map[string][]worldClockPacketBB{}
			record := func(lines []string) {
				for _, packet := range worldClockPacketsBB(t, lines) {
					if len(packet.rows) > 0 {
						seen[phase] = append(seen[phase], packet)
					}
				}
			}
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("WCC_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				lines := waitForExactSayBB(t, sink, marker, 10*time.Second)
				record(lines)
				return lines
			}
			clock := func() int64 {
				sendCommand(t, pw, "time query gametime")
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					lines := sink.take()
					record(lines)
					for _, line := range lines {
						key, args, ok := componentArgs(line)
						if ok && key == "commands.time.query.gametime" && len(args) == 1 {
							if value, ok := args[0].(int64); ok {
								return value
							}
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("clock receipt missing")
				return 0
			}
			snapshot := func(label string, index int) worldClockPacketBB {
				phase = label
				oc, opr, opw := joinPlayBlackboxView(t, tc.addr, fmt.Sprintf("CmdObserve%d", index), 1)
				defer oc.Close()
				drainToJoinBlackbox(t, tc.name, opr)
				other := newChatSinkTP(opr, opw)
				marker := fmt.Sprintf("WCC_OBS_%d", index)
				command("say " + marker)
				lines := waitForExactSayBB(t, other, marker, 10*time.Second)
				record(lines)
				var snapshots []worldClockPacketBB
				for _, p := range worldClockPacketsBB(t, lines) {
					if len(p.rows) == 2 {
						snapshots = append(snapshots, p)
					}
				}
				if len(snapshots) != 1 {
					t.Fatalf("%s snapshots %#v", label, snapshots)
				}
				return snapshots[0]
			}
			command("tick freeze")
			baseline := snapshot("baseline", 0)
			rate := float32(.5)
			total := int64(500) + baseline.age/2
			fraction := float32(.25)
			if baseline.age%2 != 0 {
				fraction = .75
			}
			if paused {
				rate = 0
				total = 500
				fraction = .25
			}
			expected := []worldClockRowBB{{0, total, fraction, rate}, {1, 42, .75, 0}}
			assertWorldClockPacketBB(t, "baseline", baseline, baseline.age, expected)
			check := func(label string, index int) {
				assertWorldClockPacketBB(t, label, snapshot(label, index), baseline.age, expected)
			}
			update := func(label, cmd string, id int32, total int64, fraction float32) {
				phase = label
				command(cmd)
				expected[id].total = total
				expected[id].fraction = fraction
			}
			update("ow_add", "time add 1", 0, total+1, fraction)
			check("ow_added", 1)
			update("ow_set", "time set 1234", 0, 1234, 0)
			check("ow_set_result", 2)
			update("end_add", "execute in minecraft:the_end run time add 2", 1, 44, .75)
			check("end_added", 3)
			phase = "end_marker"
			markerLines := command("execute in minecraft:the_end run time set noon")
			markerFailed := false
			for _, line := range markerLines {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.no_time_marker_found" && reflect.DeepEqual(args, []any{"ResourceKey[minecraft:clock_time_marker / minecraft:noon]", "minecraft:the_end"}) {
					markerFailed = true
				}
			}
			if !markerFailed {
				t.Fatal("End noon marker should be absent")
			}
			check("end_marker_result", 10)
			update("end_set", "execute in minecraft:the_end run time set 55000", 1, 55000, 0)
			check("end_set_result", 4)
			phase = "nether_set"
			lines := command("execute in minecraft:the_nether run time set 123")
			found := false
			for _, line := range lines {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.time.no_default_clock" && reflect.DeepEqual(args, []any{"minecraft:the_nether"}) {
					found = true
				}
			}
			if !found {
				t.Fatal("Nether missing-default-clock error absent")
			}
			check("nether_result", 5)
			update("negative_add", "time add -1", 0, 1233, 0)
			check("negative_add_result", 6)
			phase = "negative_set"
			command("time set -1")
			check("negative_set_result", 7)
			update("clamp_add", "time add -2000", 0, 0, 0)
			check("clamp_add_result", 8)
			phase = "step"
			before := clock()
			command("tick step 2")
			deadline := time.Now().Add(10 * time.Second)
			for clock() < before+2 {
				if time.Now().After(deadline) {
					t.Fatal("step receipt missing")
				}
			}
			if !paused {
				expected[0].total = 1
			}
			assertWorldClockPacketBB(t, "after_step", snapshot("after_step", 9), baseline.age+2, expected)
			updates := map[string][]worldClockPacketBB{
				"ow_add":       {{baseline.age, []worldClockRowBB{{0, total + 1, fraction, rate}}}},
				"ow_set":       {{baseline.age, []worldClockRowBB{{0, 1234, 0, rate}}}},
				"end_marker":   {{baseline.age, []worldClockRowBB{{1, 44, .75, 0}}}},
				"end_add":      {{baseline.age, []worldClockRowBB{{1, 44, .75, 0}}}},
				"end_set":      {{baseline.age, []worldClockRowBB{{1, 55000, 0, 0}}}},
				"negative_add": {{baseline.age, []worldClockRowBB{{0, 1233, 0, rate}}}},
				"negative_set": {{baseline.age, []worldClockRowBB{{0, 1233, 0, rate}}}},
				"clamp_add":    {{baseline.age, []worldClockRowBB{{0, 0, 0, rate}}}},
			}
			for label, want := range updates {
				if !reflect.DeepEqual(seen[label], want) {
					t.Errorf("%s updates%#v want%#v", label, seen[label], want)
				}
			}
			if len(seen["nether_set"]) != 0 {
				t.Fatal("Nether error changed clocks")
			}
			command("save-all flush")
			root := directory
			if tc.name == "vanilla" {
				root = filepath.Join(vanillaDirForDebug, "world")
			}
			f, err := os.Open(filepath.Join(root, "data", "minecraft", "world_clocks.dat"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			saved, err := readGzipNBTBlackbox(f)
			if err != nil {
				t.Fatal(err)
			}
			owSaved := map[string]any{"total_ticks": expected[0].total, "rate": float32(.5)}
			if paused {
				owSaved["paused"] = int8(1)
			}
			want := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"minecraft:overworld": owSaved, "minecraft:the_end": map[string]any{"total_ticks": int64(55000), "rate": float32(2), "paused": int8(1)}}}
			if !reflect.DeepEqual(saved, want) {
				t.Fatalf("saved%#v want%#v", saved, want)
			}
		})
	}
}
