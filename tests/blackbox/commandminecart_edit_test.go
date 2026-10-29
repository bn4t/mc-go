package blackbox

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestCommandMinecartEditParity measures the entity edit packet separately
// from activator-rail execution. Its floating cart never receives power.
func TestCommandMinecartEditParity(t *testing.T) {
	var ops []string
	for _, entry := range []struct {
		name  string
		level int
	}{{"P", 4}, {"CartMaster", 2}, {"CartLow", 1}} {
		ops = append(ops, fmt.Sprintf(`{"uuid":"%s","name":"%s","level":%d,"bypassesPlayerLimit":false}`, server.DashUUID(server.OfflineUUID(entry.name)), entry.name, entry.level))
	}
	targets := requireBlackboxFiles(t, map[string]string{"gamemode": "creative", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": "[" + strings.Join(ops, ",") + "]"}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
		s.SetOpLevel("CartMaster", 2)
		s.SetOpLevel("CartLow", 1)
	})
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		time.Sleep(time.Second)
		for _, cmd := range []string{"tp @s 20 -55 20", "tick freeze", `summon minecraft:command_block_minecart 20.5 -55 20.5 {NoGravity:1b,Tags:["cartedit"],Command:"say seed",SuccessCount:7,LastExecution:12L,LastOutput:"old",CustomName:"Keeper"}`} {
			sendCommand(t, pw, cmd)
		}
		var observed []string
		clock := func() int64 {
			sendCommand(t, pw, "time query gametime")
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
				batch := sink.take()
				observed = append(observed, batch...)
				for _, line := range batch {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.time.query.gametime" && len(args) > 0 {
						if v, ok := args[0].(int64); ok {
							return v
						}
					}
				}
			}
			t.Fatal("cart edit clock query missing")
			return 0
		}

		step := func(n int) {
			before := clock()
			sendCommand(t, pw, fmt.Sprintf("tick step %d", n))
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				sendCommand(t, pw, "time query gametime")
				time.Sleep(30 * time.Millisecond)
				batch := sink.take()
				observed = append(observed, batch...)
				for _, line := range batch {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.time.query.gametime" && len(args) > 0 {
						if v, ok := args[0].(int64); ok && v >= before+int64(n) {
							return
						}
					}
				}
			}
			t.Fatal("cart edit step did not complete")
		}
		// Spawn tracking may happen at a world boundary. Read the actual wire id.
		observed = append(observed, sink.take()...)
		step(1)
		entityID := int32(-1)
		prefix := fmt.Sprintf("%#x:", server.IDPlaySpawnEntity)
		cartType, _ := server.EntityTypeID("command_block_minecart")
		for attempt := 0; attempt < 40 && entityID < 0; attempt++ {
			for _, line := range observed {
				if strings.HasPrefix(line, prefix) {
					r := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix)))
					id, e1 := r.VarInt()
					_, e2 := r.Read(16)
					typ, e3 := r.VarInt()
					if e1 == nil && e2 == nil && e3 == nil && typ == cartType {
						entityID = id
					}
				}
			}
			if entityID < 0 {
				time.Sleep(50 * time.Millisecond)
				step(1)
			}
		}

		if entityID < 0 {
			t.Fatalf("%s: no command minecart spawn packet", addr)
		}
		initialMeta := commandMinecartWireMetadata(t, observed, entityID)
		if initialMeta[13] != "say seed" || componentPlain(initialMeta[14]) != "old" {
			t.Errorf("%s: initial command cart metadata=%#v", addr, initialMeta)
		}
		index := 0
		check := func(condition string) {
			sink.take()
			marker, end := fmt.Sprintf("CME_PASS%d", index), fmt.Sprintf("CME_DONE%d", index)
			index++
			sendCommand(t, pw, "execute "+condition+" run say "+marker)
			sendCommand(t, pw, "say "+end)
			if !sawMarker(waitForMarker(t, sink, end, 10*time.Second), marker) {
				t.Errorf("%s: %s", addr, condition)
			}
		}
		selector := `@e[type=minecraft:command_block_minecart,tag=cartedit,limit=1]`
		edit := func(w *protocol.Writer, chat *chatSink, id int32, command string, track bool, feedback string) {
			chat.take()
			var b protocol.Buffer
			b.VarInt(id)
			b.String(command)
			if track {
				b.Byte(1)
			} else {
				b.Byte(0)
			}
			if err := w.WritePacket(server.IDPlaySetCommandMinecartSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			var lines []string
			if w != pw && feedback != "" {
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					batch := chat.take()
					lines = append(lines, batch...)
					found := false
					for _, line := range batch {
						found = found || strings.Contains(line, "advMode.")
					}
					if found {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			sendCommand(t, pw, "say CME_EDIT_END")
			lines = append(lines, waitForMarker(t, chat, "CME_EDIT_END", 10*time.Second)...)

			observed = append(observed, lines...)
			got := false
			for _, line := range lines {
				if strings.Contains(line, "advMode.") {
					got = true
					if feedback == "" || !strings.Contains(line, feedback) {
						t.Errorf("%s: unexpected edit feedback %q", addr, line)
					}
				}
			}
			if feedback != "" && !got {
				t.Errorf("%s: missing edit feedback %s", addr, feedback)
			}
		}
		check(`if data entity ` + selector + ` {Command:"say seed",SuccessCount:0,TrackOutput:1b,UpdateLastExecution:1b,LastExecution:12L,LastOutput:"old",CustomName:"Keeper"}`)
		observed = nil
		edit(pw, sink, entityID, "say next", true, "advMode.setCommand.success")
		step(3)
		if got := commandMinecartWireMetadata(t, observed, entityID); got[13] != "say next" {
			t.Errorf("%s: edited command metadata=%#v", addr, got)
		}
		check(`if data entity ` + selector + ` {Command:"say next",SuccessCount:0,TrackOutput:1b,LastExecution:12L,LastOutput:"old",CustomName:"Keeper"}`)
		observed = nil
		edit(pw, sink, entityID, "say quiet", false, "advMode.setCommand.success")
		step(3)
		if got := commandMinecartWireMetadata(t, observed, entityID); got[13] != "say quiet" || got[14] == nil || componentPlain(got[14]) != "" {
			t.Errorf("%s: cleared output metadata=%#v", addr, got)
		}
		check(`if data entity ` + selector + ` {Command:"say quiet",SuccessCount:0,TrackOutput:0b}`)
		check(`unless data entity ` + selector + ` LastOutput`)
		observed = nil
		edit(pw, sink, entityID, "say visible", true, "advMode.setCommand.success")
		step(3)
		if got := commandMinecartWireMetadata(t, observed, entityID); got[13] != "say visible" {
			t.Errorf("%s: reenabled metadata=%#v", addr, got)
		}
		check(`if data entity ` + selector + ` {LastOutput:""}`)
		sendCommand(t, pw, "gamerule command_blocks_work false")
		waitDrained(t, pw, sink)
		observed = nil
		edit(pw, sink, entityID, "say disabled", true, "advMode.setCommand.disabled")
		step(3)
		if got := commandMinecartWireMetadata(t, observed, entityID); got[13] != nil {
			t.Errorf("%s: disabled edit changed cached metadata=%#v", addr, got)
		}
		check(`if data entity ` + selector + ` {Command:"say disabled",SuccessCount:0}`)
		edit(pw, sink, entityID, "", true, "")
		check(`if data entity ` + selector + ` {Command:"",SuccessCount:0}`)
		edit(pw, sink, -123, "say wrong", true, "")
		check(`if data entity ` + selector + ` {Command:""}`)
		sendCommand(t, pw, "gamerule command_blocks_work true")
		waitDrained(t, pw, sink)
		for _, name := range []string{"CartLow", "CartMaster"} {
			other, opr, opw := joinPlayQuiet(t, addr, name)
			sendClientInformation(t, opw)
			osink := newChatSinkTP(opr, opw)
			time.Sleep(200 * time.Millisecond)
			feedback := "advMode.notAllowed"
			command := "say denied"
			if name == "CartMaster" {
				feedback = "advMode.setCommand.success"
				command = "say master"
			}
			edit(opw, osink, entityID, command, true, feedback)
			other.Close()
		}
		check(`if data entity ` + selector + ` {Command:"say master",SuccessCount:0,CustomName:"Keeper"}`)
		sendCommand(t, pw, "gamemode survival @s")
		waitDrained(t, pw, sink)
		edit(pw, sink, entityID, "say denied", true, "advMode.notAllowed")
		check(`if data entity ` + selector + ` {Command:"say master"}`)

		t.Logf("%s: %d strict minecart edit predicates", addr, index)
	}
	run(targets.vanilla)
	run(targets.mcgo)
}

func commandMinecartWireMetadata(t *testing.T, lines []string, entityID int32) map[byte]any {
	t.Helper()
	out := map[byte]any{}
	prefix := fmt.Sprintf("%#x:", server.IDPlayEntityMetadata)
	for _, line := range lines {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		r := bytes.NewReader([]byte(strings.TrimPrefix(line, prefix)))
		id, err := protocol.ReadVarInt(r)
		if err != nil || id != entityID {
			continue
		}
		for {
			index, err := r.ReadByte()
			if err != nil || index == 255 {
				break
			}
			kind, err := protocol.ReadVarInt(r)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case 0, 8:
				_, err = r.ReadByte()
			case 1, 15, 20:
				_, err = protocol.ReadVarInt(r)
			case 2:
				_, err = protocol.ReadVarLong(r)
			case 3:
				_, err = r.Seek(4, 1)
			case 4:
				out[index], err = protocol.ReadString(r)
			case 5:
				out[index], err = nbt.ReadUnnamed(r)
			case 6:
				var present byte
				present, err = r.ReadByte()
				if err == nil && present != 0 {
					_, err = nbt.ReadUnnamed(r)
				}
			default:
				t.Fatalf("command cart metadata serializer%d", kind)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}
