//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuiltinClockControlsParity(t *testing.T) {
	const name = "ClockControls"
	payload := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{
		"minecraft:overworld": map[string]any{"total_ticks": int64(500), "partial_tick": float32(.25), "rate": float32(.5)},
		"minecraft:the_end":   map[string]any{"total_ticks": int64(42), "partial_tick": float32(.75), "rate": float32(2), "paused": int8(1)},
	}}
	files := map[string]string{"ops.json": opsJSONFor(name), "world/data/minecraft/world_clocks.dat": clockFallbackNBTBB(t, payload)}
	directory := worldClockDirectoryBB(t, files)
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, files, func(s *server.Server) {
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
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			phase := "initial"
			type controlReply struct {
				key  string
				args []any
			}
			seen := map[string][]worldClockPacketBB{}
			replies := map[string][]controlReply{}
			record := func(lines []string) {
				for _, packet := range worldClockPacketsBB(t, lines) {
					if len(packet.rows) > 0 {
						seen[phase] = append(seen[phase], packet)
					}
				}
				for _, line := range lines {
					key, args, ok := componentArgs(line)
					if ok && (strings.HasPrefix(key, "commands.time.") || strings.HasPrefix(key, "argument.") || strings.HasPrefix(key, "parsing.") || key == "commands.scoreboard.players.get.success") {
						replies[phase] = append(replies[phase], controlReply{key, args})
					}
				}
			}
			command := func(label, cmd string) []string {
				phase = label
				serial++
				marker := fmt.Sprintf("CLOCK_CTL_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				lines := waitForExactSayBB(t, sink, marker, 10*time.Second)
				record(lines)
				return lines
			}
			age := func() int64 { return gameTimeOf(t, pw, sink) }
			step := func(label string, n int) {
				phase = label
				before := age()
				command(label, fmt.Sprintf("tick step %d", n))
				deadline := time.Now().Add(10 * time.Second)
				for age() < before+int64(n) {
					if time.Now().After(deadline) {
						t.Fatal("step did not complete")
					}
				}
				record(sink.take())
			}
			snapshot := func(label string, index int) {
				phase = label
				oc, opr, opw := joinPlayBlackboxView(t, tc.addr, fmt.Sprintf("ClockCtlObs%d", index), 1)
				defer oc.Close()
				drainToJoinBlackbox(t, tc.name, opr)
				other := newChatSinkTP(opr, opw)
				marker := fmt.Sprintf("CLOCK_CTL_OBS_%d", index)
				command(label, "say "+marker)
				record(waitForExactSayBB(t, other, marker, 10*time.Second))
			}
			command("freeze", "tick freeze")
			snapshot("baseline", 0)
			if len(seen["baseline"]) != 1 || len(seen["baseline"][0].rows) != 2 {
				t.Fatalf("baseline clocks%#v", seen["baseline"])
			}
			startAge := seen["baseline"][0].age
			startTotal := int64(500) + startAge/2
			startFraction := float32(.25)
			if startAge%2 != 0 {
				startFraction = .75
			}
			for _, x := range []struct{ label, cmd string }{
				{"ow_pause", "time of overworld pause"},
				{"ow_rate_paused", "time of overworld rate 1.25"},
				{"ow_resume", "time of overworld resume"},
				{"end_rate_paused", "time of the_end rate 2.5"},
				{"end_resume", "time of the_end resume"},
				{"end_resume_repeat", "time of the_end resume"},
				{"nether_explicit_end", "execute in minecraft:the_nether run time of the_end pause"},
				{"nether_default", "execute in minecraft:the_nether run time pause"},
				{"absolute_ow", "time of overworld query time"},
				{"absolute_end", "time of the_end query time"},
				{"day_ow", "time of overworld query day"},
				{"repetition_ow", "time of overworld query day repetition"},
				{"moon_ow", "time of overworld query moon"},
				{"early_ow", "time of overworld query early_game"},
				{"villager_ow", "time of overworld query villager_schedule"},
				{"wrong_timeline", "time of the_end query day"},
				{"explicit_gametime", "time of overworld query gametime"},
				{"unknown_clock", "time of missing pause"},
				{"rate_zero", "time rate 0"},
				{"rate_high", "time rate 1001"},
				{"rate_below", "time rate .000001"},
				{"rate_bad", "time rate abc"},
				{"ow_default_pause", "time pause"},
				{"ow_default_resume", "time resume"},
				{"max_rate", "time rate 1000"},
				{"ow_normalize", "time of overworld set 500"},
				{"end_normalize", "time of the_end set 42"},
				{"min_rate", "time rate .00001"},
			} {
				command(x.label, x.cmd)
			}
			snapshot("before_min_step", 1)
			step("min_step", 2)
			snapshot("after_min_step", 2)
			command("set_rate", "time rate 1.25")
			command("end_run", "time of the_end resume")
			step("rate_step", 2)
			snapshot("after_rate_step", 3)
			command("pause_end_final", "time of the_end pause")
			command("objective", "scoreboard objectives add clockret dummy")
			command("result_rate", "execute store result score #rate clockret run time of overworld rate 1.25")
			command("query_result_rate", "scoreboard players get #rate clockret")
			command("result_pause", "execute store result score #pause clockret run time of the_end pause")
			command("query_result_pause", "scoreboard players get #pause clockret")
			command("save", "save-all flush")
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
			expectedSaved := map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{
				"minecraft:overworld": map[string]any{"total_ticks": int64(502), "partial_tick": float32(.50002), "rate": float32(1.25)},
				"minecraft:the_end":   map[string]any{"total_ticks": int64(47), "rate": float32(2.5), "paused": int8(1)},
			}}
			if !reflect.DeepEqual(saved, expectedSaved) {
				t.Fatalf("saved%#v want%#v", saved, expectedSaved)
			}
			one := func(age int64, id int32, total int64, fraction, rate float32) []worldClockPacketBB {
				return []worldClockPacketBB{{age, []worldClockRowBB{{id, total, fraction, rate}}}}
			}
			pair := func(age int64, ow int64, fraction, rate float32, end int64, endFraction, endRate float32) []worldClockPacketBB {
				return []worldClockPacketBB{{age, []worldClockRowBB{{0, ow, fraction, rate}, {1, end, endFraction, endRate}}}}
			}
			expectedClocks := map[string][]worldClockPacketBB{
				"baseline": pair(startAge, startTotal, startFraction, .5, 42, .75, 0),
				"ow_pause": one(startAge, 0, startTotal, startFraction, 0), "ow_rate_paused": one(startAge, 0, startTotal, startFraction, 0), "ow_resume": one(startAge, 0, startTotal, startFraction, 1.25),
				"end_rate_paused": one(startAge, 1, 42, .75, 0), "end_resume": one(startAge, 1, 42, .75, 2.5), "end_resume_repeat": one(startAge, 1, 42, .75, 2.5), "nether_explicit_end": one(startAge, 1, 42, .75, 0),
				"ow_default_pause": one(startAge, 0, startTotal, startFraction, 0), "ow_default_resume": one(startAge, 0, startTotal, startFraction, 1.25), "max_rate": one(startAge, 0, startTotal, startFraction, 1000),
				"ow_normalize": one(startAge, 0, 500, 0, 1000), "end_normalize": one(startAge, 1, 42, 0, 0), "min_rate": one(startAge, 0, 500, 0, .00001),
				"before_min_step": pair(startAge, 500, 0, .00001, 42, 0, 0), "after_min_step": pair(startAge+2, 500, .00002, .00001, 42, 0, 0),
				"set_rate": one(startAge+2, 0, 500, .00002, 1.25), "end_run": one(startAge+2, 1, 42, 0, 2.5), "after_rate_step": pair(startAge+4, 502, .50002, 1.25, 47, 0, 2.5),
				"pause_end_final": one(startAge+4, 1, 47, 0, 0), "result_rate": one(startAge+4, 0, 502, .50002, 1.25), "result_pause": one(startAge+4, 1, 47, 0, 0),
			}
			for label, want := range expectedClocks {
				if !reflect.DeepEqual(seen[label], want) {
					t.Errorf("%s clocks%#v want%#v", label, seen[label], want)
				}
			}
			for label, got := range seen {
				if label != "initial" && label != "freeze" {
					if _, ok := expectedClocks[label]; !ok && len(got) > 0 {
						t.Errorf("%s unexpected clock mutation%#v", label, got)
					}
				}
			}
			value := func(v any) map[string]any { return map[string]any{"": v} }
			rateReply := func(clock string, rate float32) []controlReply {
				return []controlReply{{"commands.time.rate", []any{value(clock), value(rate)}}}
			}
			pauseReply := func(key, clock string) []controlReply { return []controlReply{{"commands.time." + key, []any{clock}}} }
			expectedReplies := map[string][]controlReply{
				"ow_pause": pauseReply("pause", "minecraft:overworld"), "ow_rate_paused": rateReply("minecraft:overworld", 1.25), "ow_resume": pauseReply("resume", "minecraft:overworld"),
				"end_rate_paused": rateReply("minecraft:the_end", 2.5), "end_resume": pauseReply("resume", "minecraft:the_end"), "end_resume_repeat": pauseReply("resume", "minecraft:the_end"),
				"nether_explicit_end": pauseReply("pause", "minecraft:the_end"), "nether_default": {{"commands.time.no_default_clock", []any{"minecraft:the_nether"}}},
				"absolute_ow": {{"commands.time.query.absolute", []any{value("minecraft:overworld"), value(startTotal)}}}, "absolute_end": {{"commands.time.query.absolute", []any{value("minecraft:the_end"), value(int64(42))}}},
				"day_ow": {{"commands.time.query.timeline", []any{value("minecraft:day"), value(startTotal % 24000)}}}, "repetition_ow": {{"commands.time.query.timeline.repetitions", []any{value("minecraft:day"), value(startTotal / 24000)}}},
				"moon_ow": {{"commands.time.query.timeline", []any{value("minecraft:moon"), value(startTotal % 192000)}}}, "early_ow": {{"commands.time.query.timeline", []any{value("minecraft:early_game"), value(startTotal)}}}, "villager_ow": {{"commands.time.query.timeline", []any{value("minecraft:villager_schedule"), value(startTotal % 24000)}}},
				"wrong_timeline": {{"commands.time.wrong_timeline_for_clock", []any{"minecraft:day", "minecraft:the_end"}}}, "explicit_gametime": {{"argument.resource.not_found", []any{"minecraft:gametime", "minecraft:timeline"}}}, "unknown_clock": {{"argument.resource.not_found", []any{"minecraft:missing", "minecraft:world_clock"}}},
				"rate_zero": {{"argument.float.low", []any{float32(.00001), float32(0)}}}, "rate_below": {{"argument.float.low", []any{float32(.00001), float32(.000001)}}}, "rate_high": {{"argument.float.big", []any{float32(1000), float32(1001)}}}, "rate_bad": {{"parsing.float.expected", nil}},
				"ow_default_pause": pauseReply("pause", "minecraft:overworld"), "ow_default_resume": pauseReply("resume", "minecraft:overworld"), "max_rate": rateReply("minecraft:overworld", 1000), "min_rate": rateReply("minecraft:overworld", .00001), "set_rate": rateReply("minecraft:overworld", 1.25), "end_run": pauseReply("resume", "minecraft:the_end"), "pause_end_final": pauseReply("pause", "minecraft:the_end"), "result_rate": rateReply("minecraft:overworld", 1.25), "result_pause": pauseReply("pause", "minecraft:the_end"),
			}
			for label, want := range expectedReplies {
				if !reflect.DeepEqual(replies[label], want) {
					t.Errorf("%s replies%#v want%#v", label, replies[label], want)
				}
			}
			for label, score := range map[string]string{"query_result_rate": "#rate", "query_result_pause": "#pause"} {
				got := replies[label]
				if len(got) != 1 || got[0].key != "commands.scoreboard.players.get.success" || len(got[0].args) != 3 || !reflect.DeepEqual(got[0].args[0], value(score)) || !reflect.DeepEqual(got[0].args[1], value(int32(1))) {
					t.Errorf("%s numeric command result%#v", label, got)
				}
			}
			t.Logf("clock controls age%d→%d raw saved state%#v", startAge, startAge+4, saved)
		})
	}
}
