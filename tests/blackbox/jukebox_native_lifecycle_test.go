package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestJukeboxNativeLifecycleParity(t *testing.T) {
	tagged := `{components:{"minecraft:custom_name":"Box"},RecordItem:{id:"minecraft:music_disc_cat",count:1,components:{"minecraft:custom_name":"Old","minecraft:enchantments":{"minecraft:unbreaking":2},"minecraft:repair_cost":3}},ticks_since_song_started:7L}`
	type phase struct {
		name             string
		commands, checks []string
		stops            int
	}
	phases := []phase{
		{name: "tagged_record", commands: []string{"setblock 0 159 0 jukebox[has_record=true]" + tagged}, checks: []string{`execute if data block 0 159 0 {components:{"minecraft:custom_name":"Box"},RecordItem:{components:{"minecraft:custom_name":"Old","minecraft:enchantments":{"minecraft:unbreaking":2},"minecraft:repair_cost":3}},ticks_since_song_started:7L}`}},
		{name: "same_components_count_only", commands: []string{"data modify block 0 159 0 RecordItem.count set value 2"}, checks: []string{`execute if data block 0 159 0 {RecordItem:{count:2,components:{"minecraft:custom_name":"Old"}},ticks_since_song_started:7L}`}},
		{name: "omit_clock_same_record", commands: []string{"data remove block 0 159 0 ticks_since_song_started"}, checks: []string{`execute if data block 0 159 0 {RecordItem:{count:2},ticks_since_song_started:7L}`}},
		{name: "change_components", commands: []string{`data modify block 0 159 0 RecordItem.components."minecraft:custom_name" set value "New"`}, checks: []string{`execute if data block 0 159 0 {components:{"minecraft:custom_name":"Box"},RecordItem:{count:2,components:{"minecraft:custom_name":"New","minecraft:enchantments":{"minecraft:unbreaking":2},"minecraft:repair_cost":3}},ticks_since_song_started:7L}`}, stops: 1},
		{name: "remove_record", commands: []string{"data remove block 0 159 0 RecordItem"}, checks: []string{"execute unless data block 0 159 0 RecordItem", "execute unless data block 0 159 0 ticks_since_song_started", `execute if data block 0 159 0 {components:{"minecraft:custom_name":"Box"}}`}, stops: 1},
		{name: "replace_block", commands: []string{"data merge block 0 159 0 " + tagged, "kill @e[type=item]", "setblock 0 159 0 stone", "setblock 0 159 0 jukebox"}, checks: []string{"execute unless data block 0 159 0 RecordItem", "execute unless data block 0 159 0 ticks_since_song_started", `execute unless data block 0 159 0 {components:{"minecraft:custom_name":"Box"}}`, `execute unless entity @e[type=item,distance=..8,nbt={Item:{id:"minecraft:music_disc_cat",components:{"minecraft:custom_name":"Old"}}}]`}, stops: 1},
	}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("JukeNative")}, nil)
	defer targets.cleanup()
	type result struct {
		checks bool
		stops  int
	}
	run := func(addr string) []result {
		conn, pr, pw := joinPlayBlackbox(t, addr, "JukeNative")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		collect := func(marker string) []string {
			var out []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				out = append(out, sink.take()...)
				if structureSaidBB(out, marker) {
					return out
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing %s", addr, marker)
			return nil
		}
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 0.5 160.0 -2.5")
		sendCommand(t, pw, "forceload add 0 0")
		sendCommand(t, pw, "say JUKENATIVEREADY")
		collect("JUKENATIVEREADY")
		loaded := false
		deadline := time.Now().Add(10 * time.Second)
		for n := 0; time.Now().Before(deadline) && !loaded; n++ {
			ready, end := fmt.Sprintf("JUKENATIVELOADED%dX", n), fmt.Sprintf("JUKENATIVELOADEND%dX", n)
			sendCommand(t, pw, "execute if loaded 0 159 0 run say "+ready)
			sendCommand(t, pw, "say "+end)
			loaded = structureSaidBB(collect(end), ready)
		}
		if !loaded {
			t.Fatalf("%s native jukebox chunk not ready", addr)
		}
		var out []result
		for i, phase := range phases {
			sink.take()
			for _, command := range phase.commands {
				sendCommand(t, pw, command)
			}
			markers := make([]string, len(phase.checks))
			for j, condition := range phase.checks {
				markers[j] = fmt.Sprintf("JUKENATIVE%d_%dX", i, j)
				sendCommand(t, pw, condition+" run say "+markers[j])
			}
			end := fmt.Sprintf("JUKENATIVEDONE%dX", i)
			sendCommand(t, pw, "say "+end)
			lines := collect(end)
			got := result{checks: true}
			for _, marker := range markers {
				present := structureSaidBB(lines, marker)
				t.Logf("%s %s condition %s=%v", addr, phase.name, marker, present)
				got.checks = got.checks && present
			}
			prefix := fmt.Sprintf("%#x:", server.IDPlayLevelEvent)
			for _, line := range lines {
				if strings.HasPrefix(line, prefix) {
					id, err := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix))).Int()
					if err == nil && id == 1011 {
						got.stops++
					}
				}
			}
			out = append(out, got)
			t.Logf("%s %s %+v", addr, phase.name, got)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i, phase := range phases {
		want := result{checks: true, stops: phase.stops}
		if vanilla[i] != want || mcgo[i] != vanilla[i] {
			t.Errorf("%s vanilla%+v mcgo%+v want%+v", phase.name, vanilla[i], mcgo[i], want)
		}
	}
}
