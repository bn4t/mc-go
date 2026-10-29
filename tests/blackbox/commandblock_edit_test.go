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

func TestCommandBlockEditParity(t *testing.T) {
	var ops []string
	for _, entry := range []struct {
		name  string
		level int
	}{{"P", 4}, {"GameMaster", 2}, {"LowOp", 1}} {
		ops = append(ops, fmt.Sprintf(`{"uuid":"%s","name":"%s","level":%d,"bypassesPlayerLimit":false}`, server.DashUUID(server.OfflineUUID(entry.name)), entry.name, entry.level))
	}
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": "[" + strings.Join(ops, ",") + "]"}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
		s.SetOpLevel("GameMaster", 2)
		s.SetOpLevel("LowOp", 1)
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
			"tp @s 22 -55 30", "tick freeze",
			`setblock 20 -60 30 minecraft:command_block[facing=east]{Command:"say seed",SuccessCount:7,TrackOutput:1b,UpdateLastExecution:1b,LastExecution:12L,CustomName:"Keeper",LastOutput:"old"}`,
			"setblock 24 -60 30 minecraft:chain_command_block[facing=up]",
			"setblock 26 -60 30 minecraft:stone",
			"setblock 28 -60 30 minecraft:command_block[facing=west]",
			`setblock 30 -60 30 minecraft:command_block{TrackOutput:0b,LastOutput:"hidden"}`,
		} {
			sendCommand(t, pw, cmd)
		}
		waitDrained(t, pw, sink)
		index := 0
		check := func(condition string) {
			sink.take()
			marker := fmt.Sprintf("CBE_PASS%d", index)
			end := fmt.Sprintf("CBE_DONE%d", index)
			index++
			sendCommand(t, pw, "execute "+condition+" run say "+marker)
			sendCommand(t, pw, "say "+end)
			if !sawMarker(waitForMarker(t, sink, end, 10*time.Second), marker) {
				t.Errorf("%s: %s", addr, condition)
			}
		}
		edit := func(w *protocol.Writer, chat *chatSink, x int32, command string, mode int32, flags byte, feedback string) {
			chat.take()
			var b bytes.Buffer
			_ = protocol.WritePosition(&b, x, -60, 30)
			_ = protocol.WriteString(&b, command)
			_ = protocol.WriteVarInt(&b, mode)
			_ = b.WriteByte(flags)
			if err := w.WritePacket(server.IDPlaySetCommandBlockSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			if feedback == "" {
				sendCommand(t, w, "say CBE_SILENT_DONE")
				for _, line := range waitForMarker(t, chat, "CBE_SILENT_DONE", 10*time.Second) {
					if strings.Contains(line, "advMode.setCommand.") {
						t.Errorf("unexpected edit feedback: %s", line)
					}
				}
				return
			}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range chat.take() {
					if strings.Contains(line, feedback) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s: missing %s", addr, feedback)
		}
		check(`if data block 20 -60 30 {Command:"say seed",SuccessCount:7,TrackOutput:1b,UpdateLastExecution:1b,LastExecution:12L}`)
		check(`if data block 24 -60 30 {Command:"",SuccessCount:0,TrackOutput:1b,UpdateLastExecution:1b,powered:0b,conditionMet:0b,auto:1b}`)
		check(`unless data block 30 -60 30 LastOutput`)
		edit(pw, sink, 30, "say tracking enabled", 2, 1, "advMode.setCommand.success")
		check(`unless data block 30 -60 30 LastOutput`)
		edit(pw, sink, 20, "say edited", 1, 3, "advMode.setCommand.success")
		check(`if block 20 -60 30 minecraft:repeating_command_block[facing=east,conditional=true]`)
		check(`if data block 20 -60 30 {Command:"say edited",SuccessCount:0,TrackOutput:1b,auto:0b,LastExecution:12L}`)
		check(`if data block 20 -60 30 LastOutput`)
		check(`if data block 20 -60 30 CustomName`)
		edit(pw, sink, 20, "say chain", 0, 0, "advMode.setCommand.success")
		check(`if block 20 -60 30 minecraft:chain_command_block[facing=east,conditional=false]`)
		check(`if data block 20 -60 30 {Command:"say chain",SuccessCount:0,TrackOutput:0b,auto:0b}`)
		check(`unless data block 20 -60 30 LastOutput`)
		edit(pw, sink, 20, "say impulse", 2, 1, "advMode.setCommand.success")
		check(`if block 20 -60 30 minecraft:command_block[facing=east,conditional=false]`)
		sendCommand(t, pw, "gamerule command_blocks_work false")
		waitDrained(t, pw, sink)
		edit(pw, sink, 20, "say disabled", 1, 5, "advMode.setCommand.disabled")
		check(`if data block 20 -60 30 {Command:"say disabled",SuccessCount:0,TrackOutput:1b,auto:1b,conditionMet:1b}`)
		edit(pw, sink, 20, "", 1, 1, "")
		check(`if data block 20 -60 30 {Command:"",SuccessCount:0,auto:0b}`)
		edit(pw, sink, 26, "say wrong target", 2, 1, "")
		check(`if block 26 -60 30 minecraft:stone`)
		for _, name := range []string{"GameMaster", "LowOp"} {
			other, r, w := joinPlayQuiet(t, addr, name)
			defer other.Close()
			_ = other.SetDeadline(time.Time{})
			sendClientInformation(t, w)
			chat := newChatSinkTP(r, w)
			if name == "GameMaster" {
				edit(w, chat, 28, "say level two", 2, 1, "advMode.setCommand.disabled")
			} else {
				edit(w, chat, 28, "say forbidden", 2, 1, "advMode.notAllowed")
			}
		}
		check(`if data block 28 -60 30 {Command:"say level two",SuccessCount:0}`)
		sendCommand(t, pw, "gamemode survival @s")
		waitDrained(t, pw, sink)
		edit(pw, sink, 28, "say survival forbidden", 2, 1, "advMode.notAllowed")
		check(`if data block 28 -60 30 {Command:"say level two",SuccessCount:0}`)
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
