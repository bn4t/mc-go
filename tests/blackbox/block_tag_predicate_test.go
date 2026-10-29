package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestBlockTagPredicateParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("BlockTagProbe")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	cases := []struct {
		name, pos, spec string
		want            bool
	}{
		{"member chest", "4 -60 4", `#minecraft:guarded_by_piglins`, true},
		{"default namespace", "4 -60 4", `#guarded_by_piglins`, true},
		{"member gold", "8 -60 4", `#minecraft:guarded_by_piglins`, true},
		{"nonmember", "12 -60 4", `#minecraft:guarded_by_piglins`, false},
		{"empty properties", "4 -60 4", `#guarded_by_piglins[]`, true},
		{"empty spaced properties", "4 -60 4", `#guarded_by_piglins[ ]`, true},
		{"property", "4 -60 4", `#guarded_by_piglins[facing=north]`, true},
		{"wrong property value", "4 -60 4", `#guarded_by_piglins[facing=east]`, false},
		{"unrecognized property value", "4 -60 4", `#guarded_by_piglins[facing=sideways]`, false},
		{"unrecognized property", "4 -60 4", `#guarded_by_piglins[not_a_property=x]`, false},
		{"property key case", "4 -60 4", `#guarded_by_piglins[Facing=north]`, false},
		{"property value case", "4 -60 4", `#guarded_by_piglins[facing=North]`, false},
		{"quoted properties", "4 -60 4", `#guarded_by_piglins["facing"="north"]`, true},
		{"quoted comma/brace name", "4 -60 4", `#guarded_by_piglins["unknown,{"="x"]`, false},
		{"quoted comma value", "4 -60 4", `#guarded_by_piglins[facing="north,east"]`, false},
		{"property whitespace", "4 -60 4", `#guarded_by_piglins[ facing = north , waterlogged = false ]`, true},
		{"trailing comma", "4 -60 4", `#guarded_by_piglins[facing=north,]`, true},
		{"empty value", "4 -60 4", `#guarded_by_piglins[facing=]`, false},
		{"empty name", "4 -60 4", `#guarded_by_piglins[=north]`, false},
		{"NBT subset", "4 -60 4", `#guarded_by_piglins{Items:[{Slot:0b,id:"minecraft:stone",count:3}]}`, true},
		{"wrong NBT", "4 -60 4", `#guarded_by_piglins{Items:[{Slot:0b,count:4}]}`, false},
		{"property and NBT", "4 -60 4", `#guarded_by_piglins[facing=north]{Items:[{Slot:0b}]}`, true},
		{"wrong property and NBT", "4 -60 4", `#guarded_by_piglins[facing=east]{Items:[{Slot:0b}]}`, false},
		{"non-BE empty compound", "8 -60 4", `#guarded_by_piglins{}`, false},
		{"sign tag", "16 -60 4", `#minecraft:signs{is_waxed:1b}`, true},
		{"property absent on this member", "16 -60 4", `#minecraft:signs[facing=north]`, false},
		{"property on this member", "16 -60 4", `#minecraft:signs[rotation=0]{is_waxed:1b}`, true},
	}
	errors := []struct{ name, spec, key string }{
		{"unknown tag", `#minecraft:no_such_block_tag`, "arguments.block.tag.unknown"},
		{"duplicate property", `#guarded_by_piglins[facing=north,facing=south]`, "argument.block.property.duplicate"},
		{"missing equals", `#guarded_by_piglins[facing]`, "argument.block.property.novalue"},
		{"space after trailing comma", `#guarded_by_piglins[facing=north, ]`, "argument.block.property.novalue"},
		{"unclosed properties", `#guarded_by_piglins[facing=north`, "argument.block.property.unclosed"},
		{"invalid quoted escape", `#guarded_by_piglins[facing="nor\uth"]`, "parsing.quote.escape"},
		{"invalid identifier", `#mine/craft:guarded_by_piglins`, "argument.id.invalid"},
	}
	for _, tc := range []struct{ tag, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.tag, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "BlockTagProbe")
			defer c.Close()
			drainToJoinBlackbox(t, tc.tag, pr)
			awaitPlayReadyBlackbox(t, tc.tag, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("BLOCK_TAG_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				var lines []string
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					lines = append(lines, sink.take()...)
					if structureSaidBB(lines, marker) {
						return lines
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("typed tag barrier missing")
				return nil
			}
			for _, cmd := range []string{"tick freeze", "setblock 4 -60 4 chest[facing=north]", "setblock 8 -60 4 gold_block", "setblock 12 -60 4 stone", "setblock 16 -60 4 oak_sign[rotation=0]", `data merge block 4 -60 4 {Items:[{Slot:0b,id:"minecraft:stone",count:3}]}`, `data merge block 16 -60 4 {is_waxed:1b}`} {
				sendCommand(t, pw, cmd)
				barrier()
			}
			for i, check := range cases {
				for _, kind := range []string{"if", "unless"} {
					marker := fmt.Sprintf("BLOCK_TAG_RESULT_%d_%s", i, kind)
					sendCommand(t, pw, "execute "+kind+" block "+check.pos+" "+check.spec+" run say "+marker)
					lines := barrier()
					want := check.want
					if kind == "unless" {
						want = !want
					}
					if got := structureSaidBB(lines, marker); got != want {
						t.Errorf("%s %s got%v want%v", kind, check.name, got, want)
					}
					for _, line := range lines {
						if strings.Contains(line, "command.context.here") {
							t.Errorf("%s %s had syntax error", kind, check.name)
							break
						}
					}
				}
			}
			for i, check := range errors {
				marker := fmt.Sprintf("BLOCK_TAG_ERROR_RESULT_%d", i)
				sendCommand(t, pw, "execute if block 4 -60 4 "+check.spec+" run say "+marker)
				lines := barrier()
				if structureSaidBB(lines, marker) {
					t.Errorf("%s executed despite parser error", check.name)
				}
				if !sawMarker(lines, check.key) {
					t.Errorf("%s missing error key%s", check.name, check.key)
				}
			}
			// Client chat rejects LF before command parsing. Command-block
			// edits carry it unchanged into the real command dispatcher.
			sendCommand(t, pw, "gamerule command_block_output false")
			barrier()
			for i, check := range []struct {
				spec string
				want bool
			}{
				{"#guarded_by_piglins[\n]", true},
				{"#guarded_by_piglins[facing=north\n]", true},
				{"#guarded_by_piglins[facing=north,\n]", false},
			} {
				x := 20 + i*4
				sendCommand(t, pw, fmt.Sprintf("setblock %d -60 4 command_block", x))
				ready := fmt.Sprintf("BLOCK_TAG_COMMAND_CREATED_%d", i)
				sendCommand(t, pw, fmt.Sprintf("execute if block %d -60 4 command_block run say %s", x, ready))
				if !structureSaidBB(barrier(), ready) {
					t.Fatal("newline command block was not created")
				}
				marker := fmt.Sprintf("BLOCK_TAG_NEWLINE_RESULT_%d", i)
				var edit protocol.Buffer
				edit.Position(int32(x), -60, 4)
				edit.String("execute if block 4 -60 4 " + check.spec + " run say " + marker)
				edit.VarInt(2)
				edit.Byte(1) // Impulse, track output, needs signal.
				if err := pw.WritePacket(server.IDPlaySetCommandBlockSB, edit.Bytes()); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(10 * time.Second)
				edited := false
				for time.Now().Before(deadline) && !edited {
					edited = sawMarker(sink.take(), "advMode.setCommand.success")
					if !edited {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if !edited {
					t.Fatal("no newline command edit receipt")
				}
				before := gameTimeOf(t, pw, sink)
				sendCommand(t, pw, fmt.Sprintf("setblock %d -60 4 redstone_block", x+1))
				sendCommand(t, pw, "tick step 1")
				var lines []string
				completed := false
				deadline = time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) && !completed {
					sendCommand(t, pw, "time query gametime")
					time.Sleep(30 * time.Millisecond)
					batch := sink.take()
					lines = append(lines, batch...)
					for _, line := range batch {
						key, args, ok := componentArgs(line)
						if ok && key == "commands.time.query.gametime" && len(args) > 0 {
							if now, ok := args[0].(int64); ok && now == before+1 {
								completed = true
							}
						}
					}
				}
				if !completed {
					t.Fatal("newline command exact step did not complete")
				}
				lines = append(lines, barrier()...)
				if got := structureSaidBB(lines, marker); got != check.want {
					t.Errorf("newline case%d ran%v want%v", i, got, check.want)
				}
				t.Logf("newline case%d executed%v", i, structureSaidBB(lines, marker))
			}
			t.Logf("checked%d tag conditions +%d parser failures", len(cases)*2, len(errors))
		})
	}
}
