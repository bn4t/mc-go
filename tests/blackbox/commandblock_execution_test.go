package blackbox

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestCommandBlockExecutionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(2 * time.Second)
		for _, cmd := range []string{
			"tp @s 40.5 -55 40.5", "tick freeze", "gamerule random_tick_speed 0",
			"gamerule command_block_output false", "gamerule max_command_sequence_length 64",
			"fill 18 -60 28 70 -57 36 minecraft:air",
			"scoreboard objectives add cb dummy",
			"execute in minecraft:the_nether run forceload add 0 0",
		} {
			sendCommand(t, pw, cmd)
		}
		for _, holder := range []string{"turn", "repeat", "powered", "mode", "inactive", "afterinactive", "cycle", "zero", "zeroPassed", "limit"} {
			sendCommand(t, pw, "scoreboard players set "+holder+" cb 0")
		}
		sendCommand(t, pw, "scoreboard players set zresult cb 99")
		sendCommand(t, pw, "scoreboard players set zsuccess cb 99")
		waitDrained(t, pw, sink)
		// A successful world write is a loaded-chunk barrier. The old
		// `if loaded` fixture helper models player tracking, not forceload.
		loaded := false
		deadline := time.Now().Add(time.Minute)
		for !loaded && time.Now().Before(deadline) {
			sink.take()
			sendCommand(t, pw, "execute in minecraft:the_nether run setblock 9 80 9 minecraft:stone")
			sendCommand(t, pw, "execute in minecraft:the_nether if block 9 80 9 minecraft:stone run say CB_NETHER_READY")
			sendCommand(t, pw, "say CB_NETHER_BARRIER")
			loaded = sawMarker(waitForMarker(t, sink, "CB_NETHER_BARRIER", 10*time.Second), "CB_NETHER_READY")
		}
		if !loaded {
			t.Fatal("nether command arena never loaded")
		}
		// Loading a chunk is insufficient: prove its scheduled tick queue is
		// active using a repeater before installing the measured engine.
		for _, cmd := range []string{
			"execute in minecraft:the_nether run fill 9 79 8 9 79 10 minecraft:stone",
			"execute in minecraft:the_nether run setblock 9 80 9 minecraft:repeater[facing=north,delay=1]",
			"execute in minecraft:the_nether run setblock 9 80 8 minecraft:redstone_block",
		} {
			sendCommand(t, pw, cmd)
		}
		ready := false
		for attempt := 0; attempt < 100 && !ready; attempt++ {
			warm := gameTimeOf(t, pw, sink)
			sendCommand(t, pw, "tick step 1")
			end := time.Now().Add(10 * time.Second)
			for gameTimeOf(t, pw, sink) < warm+1 && time.Now().Before(end) {
				time.Sleep(20 * time.Millisecond)
			}
			sink.take()
			sendCommand(t, pw, "execute in minecraft:the_nether if block 9 80 9 minecraft:repeater[powered=true] run say CB_TICK_READY")
			sendCommand(t, pw, "say CB_TICK_BARRIER")
			ready = sawMarker(waitForMarker(t, sink, "CB_TICK_BARRIER", 10*time.Second), "CB_TICK_READY")
		}
		if !ready {
			t.Fatal("forced Nether scheduled-tick queue did not activate")
		}
		place := func(x, z int, kind, facing, command string, conditional, automatic, track bool, extra string) {
			bit := func(v bool) int {
				if v {
					return 1
				}
				return 0
			}
			sendCommand(t, pw, fmt.Sprintf("setblock %d -60 %d minecraft:%s[facing=%s,conditional=%t]{Command:%q,auto:%db,TrackOutput:%db%s}", x, z, kind, facing, conditional, command, bit(automatic), bit(track), extra))
		}
		place(20, 30, "command_block", "east", "execute positioned ~0.5 ~0.5 ~0.5 run setblock ~ ~1 ~ minecraft:gold_block", false, false, true, "")
		place(21, 30, "chain_command_block", "east", "setblock ~ ~1 ~ minecraft:diamond_block", true, true, true, "")
		place(22, 30, "chain_command_block", "north", "scoreboard players add turn cb 1", false, true, false, "")
		place(22, 29, "chain_command_block", "up", "scoreboard players add turn cb 1", false, true, false, "")
		sendCommand(t, pw, `setblock 22 -59 29 minecraft:chain_command_block[facing=north]{Command:"scoreboard players add turn cb 1",auto:1b,TrackOutput:0b}`)
		place(30, 30, "command_block", "east", "execute as @e[type=minecraft:armor_stand,tag=cbfork] run data get entity @s Pos[0]", false, false, true, "")
		place(34, 30, "command_block", "east", "execute if entity @s run setblock ~ ~1 ~ minecraft:redstone_block", false, false, true, "")
		place(38, 30, "command_block", "east", "tick query", false, false, true, "")
		place(42, 30, "repeating_command_block", "east", "scoreboard players add repeat cb 1", false, true, false, "")
		place(46, 30, "repeating_command_block", "east", "scoreboard players add powered cb 1", false, false, false, "")
		place(50, 30, "command_block", "east", "/say routed", false, true, false, `,CustomName:"Router"`)
		place(51, 30, "chain_command_block", "east", "scoreboard players add inactive cb 1", false, false, false, ",SuccessCount:7")
		place(52, 30, "chain_command_block", "east", "scoreboard players add afterinactive cb 1", true, true, false, "")
		place(56, 30, "command_block", "east", "SeArGe", false, false, false, "")
		place(60, 32, "command_block", "north", "scoreboard players add cycle cb 1", false, false, false, "")
		for _, p := range []struct {
			x, z   int
			facing string
		}{{60, 31, "east"}, {61, 31, "north"}, {61, 30, "west"}, {60, 30, "south"}} {
			place(p.x, p.z, "chain_command_block", p.facing, "scoreboard players add cycle cb 1", false, true, false, "")
		}
		place(34, 34, "command_block", "east", "execute store success score zsuccess cb run scoreboard players get zero cb", false, false, false, "")
		place(35, 34, "chain_command_block", "east", "scoreboard players add zeroPassed cb 1", true, true, false, "")
		place(38, 34, "command_block", "east", "execute store result score zresult cb run scoreboard players get zero cb", false, false, false, "")
		for _, cmd := range []string{
			`summon minecraft:armor_stand 30.5 -60 36.5 {NoGravity:1b,Tags:["cbfork"]}`,
			`summon minecraft:armor_stand 31.5 -60 36.5 {NoGravity:1b,Tags:["cbfork"]}`,
			"setblock 20 -60 31 minecraft:comparator[facing=north]", "setblock 20 -60 32 minecraft:redstone_wire",
			"setblock 30 -60 31 minecraft:comparator[facing=north]", "setblock 30 -60 32 minecraft:redstone_wire",
			"execute in minecraft:the_nether run fill 4 79 4 6 82 6 minecraft:air",
			`execute in minecraft:the_nether run setblock 5 80 5 minecraft:command_block{Command:"setblock ~ ~1 ~ minecraft:emerald_block"}`,
			"execute in minecraft:the_nether run setblock 6 80 5 minecraft:redstone_block",
		} {
			sendCommand(t, pw, cmd)
		}
		for _, p := range [][2]int{{19, 30}, {29, 30}, {33, 30}, {37, 30}, {45, 30}, {55, 30}, {59, 32}, {33, 34}, {37, 34}} {
			sendCommand(t, pw, fmt.Sprintf("setblock %d -60 %d minecraft:redstone_block", p[0], p[1]))
		}
		waitDrained(t, pw, sink)
		baseline := gameTimeOf(t, pw, sink)
		step := func(n int) []string {
			before := gameTimeOf(t, pw, sink)
			sink.take()
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			var lines []string
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				sendCommand(t, pw, "time query gametime")
				time.Sleep(50 * time.Millisecond)
				batch := sink.take()
				lines = append(lines, batch...)
				for _, line := range batch {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.time.query.gametime" && len(args) != 0 {
						if value, ok := args[0].(int64); ok && value >= before+int64(n) {
							return lines
						}
					}
				}
			}
			t.Fatal("command-block step never completed")
			return nil
		}
		index := 0
		check := func(condition string) {
			sink.take()
			marker, end := fmt.Sprintf("CBX_PASS%d", index), fmt.Sprintf("CBX_DONE%d", index)
			index++
			sendCommand(t, pw, "execute "+condition+" run say "+marker)
			sendCommand(t, pw, "say "+end)
			if !sawMarker(waitForMarker(t, sink, end, 10*time.Second), marker) {
				t.Errorf("%s: %s", addr, condition)
				if strings.Contains(condition, "the_nether") {
					for _, cmd := range []string{"execute in minecraft:the_nether run data get block 5 80 5", "execute in minecraft:the_nether run time query gametime", "execute in minecraft:the_nether if block 6 80 5 minecraft:redstone_block run say CB_POWER_PRESENT", "say CB_DIAGNOSTIC_END"} {
						sendCommand(t, pw, cmd)
					}
					t.Logf("Nether diagnostics: %v", waitForMarker(t, sink, "CB_DIAGNOSTIC_END", 10*time.Second))
				}
			}
		}
		edit := func(x int32, command string, mode int32, flags byte, feedback string) {
			sink.take()
			var b bytes.Buffer
			_ = protocol.WritePosition(&b, x, -60, 30)
			_ = protocol.WriteString(&b, command)
			_ = protocol.WriteVarInt(&b, mode)
			_ = b.WriteByte(flags)
			if err := pw.WritePacket(server.IDPlaySetCommandBlockSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, feedback) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("missing command edit feedback %s", feedback)
		}
		first := step(1)
		routed := false
		for _, line := range first {
			routed = routed || strings.HasPrefix(line, "0x21:") && strings.Contains(line, "routed") && strings.Contains(line, "Router")
		}
		if !routed {
			t.Errorf("%s: quiet say did not use its custom block source name", addr)
		}
		for _, condition := range []string{
			"if block 21 -58 31 minecraft:gold_block", "if block 20 -59 30 minecraft:air", "if block 21 -59 30 minecraft:diamond_block",
			"if score turn cb matches 3", "if score repeat cb matches 1", "if score powered cb matches 1",
			`if data block 20 -60 30 {SuccessCount:1}`, `if data block 21 -60 30 {SuccessCount:1}`,
			`if data block 30 -60 30 {SuccessCount:2}`, `if data block 34 -60 30 {SuccessCount:0}`, "if block 34 -59 30 minecraft:air",
			`unless data block 34 -60 30 LastOutput`, `if data block 38 -60 30 {SuccessCount:0}`,
			`if data block 50 -60 30 {SuccessCount:1}`, `unless data block 50 -60 30 LastOutput`,
			`if data block 51 -60 30 {SuccessCount:7,auto:0b,powered:0b}`, "if score inactive cb matches 0", "if score afterinactive cb matches 1",
			"if score cycle cb matches 5", "if score zsuccess cb matches 1", "if score zresult cb matches 0", "if score zeroPassed cb matches 1",
			`if data block 34 -60 34 {SuccessCount:1}`, `if data block 38 -60 34 {SuccessCount:1}`,
			`if data block 56 -60 30 {SuccessCount:1}`, `unless data block 56 -60 30 LastExecution`, `unless data block 56 -60 30 LastOutput`,
			"in minecraft:the_nether if block 5 81 5 minecraft:emerald_block", "if block 5 81 5 minecraft:air",
		} {
			check(condition)
		}
		check(fmt.Sprintf("if data block 20 -60 30 {LastExecution:%dL}", baseline+1))
		step(2)
		check("if score repeat cb matches 3")
		check("if score powered cb matches 3")
		check("if block 20 -60 32 minecraft:redstone_wire[power=1]")
		check("if block 30 -60 32 minecraft:redstone_wire[power=2]")
		edit(42, "scoreboard players add mode cb 1", 2, 1, "advMode.setCommand.success")
		for _, cmd := range []string{"setblock 45 -60 30 minecraft:air", "setblock 19 -60 30 minecraft:air", "setblock 19 -60 30 minecraft:redstone_block"} {
			sendCommand(t, pw, cmd)
		}
		waitDrained(t, pw, sink)
		step(1)
		for _, condition := range []string{"if score mode cb matches 0", "if score repeat cb matches 3", "if score powered cb matches 4", "if score turn cb matches 6", `if data block 20 -60 30 {SuccessCount:0}`, `if data block 21 -60 30 {SuccessCount:0}`} {
			check(condition)
		}
		check(fmt.Sprintf("if data block 21 -60 30 {LastExecution:%dL}", baseline+1))
		sendCommand(t, pw, "setblock 41 -60 30 minecraft:redstone_block")
		waitDrained(t, pw, sink)
		step(1)
		check("if score mode cb matches 1")
		check("if score powered cb matches 4")
		step(1)
		check("if block 20 -60 32 minecraft:redstone_wire[power=0]")
		sendCommand(t, pw, "gamerule command_blocks_work false")
		waitDrained(t, pw, sink)
		edit(56, "SeArGe", 2, 1, "advMode.setCommand.disabled")
		check(`if data block 56 -60 30 {LastOutput:"#itzlipofutzli"}`)
		for _, x := range []int{29, 55} {
			sendCommand(t, pw, fmt.Sprintf("setblock %d -60 30 minecraft:air", x))
			sendCommand(t, pw, fmt.Sprintf("setblock %d -60 30 minecraft:redstone_block", x))
		}
		waitDrained(t, pw, sink)
		step(1)
		check(`if data block 30 -60 30 {SuccessCount:0}`)
		check(`if data block 30 -60 30 LastOutput`)
		check(`if data block 56 -60 30 {SuccessCount:1,LastOutput:"#itzlipofutzli"}`)
		check(`unless data block 56 -60 30 LastExecution`)
		for _, cmd := range []string{"gamerule command_blocks_work true", "gamerule max_command_sequence_length 2"} {
			sendCommand(t, pw, cmd)
		}
		place(64, 32, "command_block", "north", "scoreboard players add limit cb 1", false, true, false, "")
		for _, z := range []int{31, 30, 29} {
			place(64, z, "chain_command_block", "north", "scoreboard players add limit cb 1", false, true, false, "")
		}
		waitDrained(t, pw, sink)
		step(1)
		check("if score limit cb matches 3")
		check(`if data block 64 -60 29 {SuccessCount:0}`)
		check(`unless data block 64 -60 29 LastExecution`)
		t.Logf("%s: %d strict command engine predicates", addr, index)
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
