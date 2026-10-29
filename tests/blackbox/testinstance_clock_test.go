//go:build blackbox

package blackbox

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type worldClockRowBB struct {
	id             int32
	total          int64
	fraction, rate float32
}
type worldClockPacketBB struct {
	age  int64
	rows []worldClockRowBB
}

func worldClockPacketsBB(t *testing.T, lines []string) []worldClockPacketBB {
	t.Helper()
	var packets []worldClockPacketBB
	for _, line := range lines {
		id, body, _ := strings.Cut(line, ":")
		if id != "0x71" {
			continue
		}
		r := protocol.NewPacketReader([]byte(body))
		age, err := r.Long()
		if err != nil {
			t.Fatal(err)
		}
		count, err := r.VarInt()
		if err != nil || count < 0 || count > 16 {
			t.Fatalf("clock count%d err%v", count, err)
		}
		packet := worldClockPacketBB{age: age}
		for i := int32(0); i < count; i++ {
			id, err := r.VarInt()
			if err != nil {
				t.Fatal(err)
			}
			total, err := r.VarLong()
			if err != nil {
				t.Fatal(err)
			}
			fraction, err := r.Float()
			if err != nil {
				t.Fatal(err)
			}
			rate, err := r.Float()
			if err != nil {
				t.Fatal(err)
			}
			packet.rows = append(packet.rows, worldClockRowBB{id, total, fraction, rate})
		}
		if r.Remaining() != 0 {
			t.Fatal("clock trailing bytes", r.Remaining())
		}
		// Native stores the clock entries in a map; row order varies per boot.
		sort.Slice(packet.rows, func(i, j int) bool { return packet.rows[i].id < packet.rows[j].id })
		packets = append(packets, packet)
	}
	return packets
}
func worldClockDirectoryBB(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, body := range files {
		if !strings.HasPrefix(path, "world/") {
			continue
		}
		local := filepath.Join(dir, strings.TrimPrefix(path, "world/"))
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
func assertWorldClockPacketBB(t *testing.T, label string, got worldClockPacketBB, age int64, rows []worldClockRowBB) {
	t.Helper()
	if got.age != age || !reflect.DeepEqual(got.rows, rows) {
		t.Fatalf("%s clock %#v want age%d rows%#v", label, got, age, rows)
	}
}

func TestTestInstanceClockEnvironmentParity(t *testing.T) { testInstanceClockEnvironmentBB(t, false) }
func TestTestInstanceClockSavedStateParity(t *testing.T)  { testInstanceClockEnvironmentBB(t, true) }
func testInstanceClockEnvironmentBB(t *testing.T, preset bool) {
	const name = "TestInstClock"
	files := map[string]string{"ops.json": opsJSONFor(name), "world/datapacks/clock/pack.mcmeta": `{"pack":{"description":"clock environment controls","min_format":107,"max_format":107}}`}
	for key, body := range map[string]string{"ow": `{"type":"minecraft:clock_time","clock":"minecraft:overworld","time":13000}`, "end": `{"type":"minecraft:clock_time","clock":"minecraft:the_end","time":55000}`, "paused": `{"type":"minecraft:clock_time","clock":"minecraft:overworld","time":7}`} {
		files["world/datapacks/clock/data/testpin/test_environment/"+key+".json"] = body
		files["world/datapacks/clock/data/testpin/test_instance/"+key+".json"] = fmt.Sprintf(`{"type":"minecraft:function","environment":"testpin:%s","structure":"minecraft:empty","function":"minecraft:always_pass","max_ticks":1,"setup_ticks":2}`, key)
	}
	if preset {
		var raw bytes.Buffer
		gz := gzip.NewWriter(&raw)
		payload := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"minecraft:overworld": map[string]any{"total_ticks": int64(500), "partial_tick": float32(.25), "rate": float32(.5)}, "minecraft:the_end": map[string]any{"total_ticks": int64(42), "partial_tick": float32(.75), "rate": float32(2), "paused": int8(1)}}}
		if err := nbt.WriteNamed(gz, "", payload); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		files["world/data/minecraft/world_clocks.dat"] = raw.String()
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
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			phase := "startup"
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
				marker := fmt.Sprintf("TIC_END_%d", serial)
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
			advance := func(n int) {
				before := clock()
				command(fmt.Sprintf("tick step %d", n))
				deadline := time.Now().Add(10 * time.Second)
				for {
					after := clock()
					if after >= before+int64(n) {
						if after != before+int64(n) {
							t.Fatalf("step advanced%d want%d", after-before, n)
						}
						return
					}
					if time.Now().After(deadline) {
						t.Fatal("step receipt missing")
					}
				}
			}
			observe := func(label string, day, age int64) {
				phase = label
				count := 0
				for _, line := range command("time query day") {
					key, args, ok := componentArgs(line)
					if !ok || key != "commands.time.query.timeline" {
						continue
					}
					count++
					want := []any{map[string]any{"": "minecraft:day"}, map[string]any{"": day % 24000}}
					if !reflect.DeepEqual(args, want) {
						t.Fatalf("%s timeline %#v want%#v", label, args, want)
					}
				}
				if count != 1 {
					t.Fatalf("%s timeline receipts%d", label, count)
				}
				if got := clock(); got != age {
					t.Fatalf("%s worldage%d want%d", label, got, age)
				}
			}
			observer := func(label string, index int) worldClockPacketBB {
				phase = label
				oc, opr, opw := joinPlayBlackboxView(t, tc.addr, fmt.Sprintf("ClockObserve%d", index), 1)
				defer oc.Close()
				drainToJoinBlackbox(t, tc.name, opr)
				other := newChatSinkTP(opr, opw)
				marker := fmt.Sprintf("TIC_OBSERVE_%d", index)
				command("say " + marker)
				lines := waitForExactSayBB(t, other, marker, 10*time.Second)
				record(lines)
				var snapshots []worldClockPacketBB
				for _, packet := range worldClockPacketsBB(t, lines) {
					if len(packet.rows) == 2 {
						snapshots = append(snapshots, packet)
					}
				}
				if len(snapshots) != 1 {
					t.Fatalf("%s initial clock snapshots%#v", label, snapshots)
				}
				return snapshots[0]
			}
			command("tick freeze")
			command("setblock 0 69 0 stone")
			command("tp @s 0.5 70 0.5")
			if !preset {
				command("time set 1234")
			}
			pos := [3]int32{4, 70, 4}
			lines := command("setblock 4 70 4 test_instance_block")
			deadline := time.Now().Add(3 * time.Second)
			constructed := func() bool {
				for _, u := range structureBEUpdatesBB(t, lines) {
					if u.pos == pos {
						return true
					}
				}
				return false
			}
			for !constructed() {
				if time.Now().After(deadline) {
					t.Fatal("constructor receipt missing")
				}
				time.Sleep(10 * time.Millisecond)
				lines = append(lines, sink.take()...)
			}
			run := func(test string) {
				phase = "run_" + test
				sink.take()
				request := testInstanceRequestBB{test: "testpin:" + test, size: [3]int32{1, 1, 1}}
				writeTestInstanceActionBB(t, pw, pos, 6, request)
				lines := command("say TIC_RUN")
				deadline := time.Now().Add(5 * time.Second)
				for {
					started, updated := false, false
					for _, line := range lines {
						key, args, ok := componentArgs(line)
						if ok && key == "test_instance_block.starting" && len(args) == 1 && args[0] == request.test {
							started = true
						}
					}
					for _, u := range structureBEUpdatesBB(t, lines) {
						data, _ := u.tag["data"].(map[string]any)
						if u.pos == pos && data["test"] == request.test && data["status"] == "cleared" && data["ignore_entities"] == int8(0) {
							updated = true
						}
					}
					if started && updated {
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("RUN applied receipt missing started%v updated%v", started, updated)
					}
					time.Sleep(10 * time.Millisecond)
					more := sink.take()
					record(more)
					lines = append(lines, more...)
				}
			}
			baseline := observer("baseline", 0)
			if len(baseline.rows) != 2 || baseline.rows[0].id != 0 || baseline.rows[1].id != 1 {
				t.Fatalf("builtin clock ids %#v", baseline)
			}
			owRate, endRate := float32(1), float32(1)
			owTotal, endTotal, endFraction := int64(1234), baseline.age, float32(0)
			owFraction := float32(0)
			if preset {
				owRate = .5
				endRate = 0
				owTotal = 500 + baseline.age/2
				owFraction = .25
				if baseline.age%2 != 0 {
					owFraction = .75
				}
				endTotal = 42
				endFraction = .75
			}
			rows := func(day int64, dayFraction, dayRate float32, end int64, endFraction, endRate float32) []worldClockRowBB {
				return []worldClockRowBB{{0, day, dayFraction, dayRate}, {1, end, endFraction, endRate}}
			}
			assertWorldClockPacketBB(t, "baseline", baseline, baseline.age, rows(owTotal, owFraction, owRate, endTotal, endFraction, endRate))
			run("ow")
			observe("ow_immediate", 13000, baseline.age)
			assertWorldClockPacketBB(t, "ow_mid", observer("ow_mid", 1), baseline.age, rows(13000, 0, owRate, endTotal, endFraction, endRate))
			phase = "ow_step2"
			advance(2)
			owStep := int64(2 * owRate)
			endStep := int64(2 * endRate)
			observe("ow_tick2", 13000+owStep, baseline.age+2)
			assertWorldClockPacketBB(t, "ow_tick2_join", observer("ow_tick2_join", 2), baseline.age+2, rows(13000+owStep, 0, owRate, endTotal+endStep, endFraction, endRate))
			phase = "ow_restore"
			advance(2)
			endBefore := endTotal + int64(4*endRate)
			observe("ow_finished", owTotal, baseline.age+4)
			assertWorldClockPacketBB(t, "ow_after", observer("ow_after", 3), baseline.age+4, rows(owTotal, 0, owRate, endBefore, endFraction, endRate))
			run("end")
			observe("end_immediate", owTotal, baseline.age+4)
			assertWorldClockPacketBB(t, "end_mid", observer("end_mid", 4), baseline.age+4, rows(owTotal, 0, owRate, 55000, 0, endRate))
			phase = "end_step2"
			advance(2)
			assertWorldClockPacketBB(t, "end_tick2", observer("end_tick2", 5), baseline.age+6, rows(owTotal+owStep, 0, owRate, 55000+endStep, 0, endRate))
			phase = "end_restore"
			advance(2)
			dayAfter := owTotal + int64(4*owRate)
			observe("end_finished", dayAfter, baseline.age+8)
			assertWorldClockPacketBB(t, "end_after", observer("end_after", 6), baseline.age+8, rows(dayAfter, 0, owRate, endBefore, 0, endRate))
			phase = "pause_rule"
			command("gamerule minecraft:advance_time false")
			assertWorldClockPacketBB(t, "paused_before", observer("paused_before", 7), baseline.age+8, rows(dayAfter, 0, 0, endBefore, 0, 0))
			run("paused")
			observe("paused_immediate", 7, baseline.age+8)
			assertWorldClockPacketBB(t, "paused_mid", observer("paused_mid", 8), baseline.age+8, rows(7, 0, 0, endBefore, 0, 0))
			phase = "paused_step2"
			advance(2)
			observe("paused_tick2", 7, baseline.age+10)
			assertWorldClockPacketBB(t, "paused_tick2_join", observer("paused_tick2_join", 9), baseline.age+10, rows(7, 0, 0, endBefore, 0, 0))
			phase = "paused_restore"
			advance(2)
			observe("paused_finished", dayAfter, baseline.age+12)
			assertWorldClockPacketBB(t, "paused_after", observer("paused_after", 10), baseline.age+12, rows(dayAfter, 0, 0, endBefore, 0, 0))
			phase = "resume_rule"
			command("gamerule minecraft:advance_time true")
			assertWorldClockPacketBB(t, "resumed", observer("resumed", 11), baseline.age+12, rows(dayAfter, 0, owRate, endBefore, 0, endRate))
			updates := map[string][]worldClockPacketBB{
				"run_ow": {{baseline.age, []worldClockRowBB{{0, 13000, 0, owRate}}}}, "ow_restore": {{baseline.age + 4, []worldClockRowBB{{0, owTotal, 0, owRate}}}},
				"run_end": {{baseline.age + 4, []worldClockRowBB{{1, 55000, 0, endRate}}}}, "end_restore": {{baseline.age + 8, []worldClockRowBB{{1, endBefore, 0, endRate}}}},
				"pause_rule": {{baseline.age + 8, rows(dayAfter, 0, 0, endBefore, 0, 0)}}, "resume_rule": {{baseline.age + 12, rows(dayAfter, 0, owRate, endBefore, 0, endRate)}},
				"run_paused": {{baseline.age + 8, []worldClockRowBB{{0, 7, 0, 0}}}}, "paused_restore": {{baseline.age + 12, []worldClockRowBB{{0, dayAfter, 0, 0}}}},
			}
			for label, want := range updates {
				if !reflect.DeepEqual(seen[label], want) {
					t.Errorf("%s updates %#v want%#v", label, seen[label], want)
				}
			}
			for _, label := range []string{"ow_step2", "end_step2", "paused_step2"} {
				if len(seen[label]) != 0 {
					t.Errorf("%s unexpected updates %#v", label, seen[label])
				}
			}
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
			owSaved := map[string]any{"total_ticks": dayAfter}
			endSaved := map[string]any{"total_ticks": endBefore}
			if preset {
				owSaved["rate"] = float32(.5)
				endSaved["rate"] = float32(2)
				endSaved["paused"] = int8(1)
			}
			wantSaved := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"minecraft:overworld": owSaved, "minecraft:the_end": endSaved}}
			if !reflect.DeepEqual(saved, wantSaved) {
				t.Fatalf("saved clock state %#v want%#v", saved, wantSaved)
			}

		})
	}
}
