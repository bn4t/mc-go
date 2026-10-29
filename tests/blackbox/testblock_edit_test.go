package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTestBlockEditorParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("TestBlockEditor")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("TestBlockEditor", 4)
	})
	defer tg.cleanup()
	type observation struct {
		Counts []int
		First  []string
		Checks []bool
	}
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "TestBlockEditor")
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			out := observation{}
			markerSeen := func(lines []string, marker string) bool {
				for _, line := range lines {
					if strings.HasPrefix(line, "0x21:") && strings.Contains(line, marker) {
						return true
					}
				}
				return false
			}
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("TB_DONE_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			command := func(cmd string) { sendCommand(t, pw, cmd); barrier() }
			check := func(condition string) {
				serial++
				marker := fmt.Sprintf("TB_PASS_%d", serial)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				ok := markerSeen(barrier(), marker)
				out.Checks = append(out.Checks, ok)
				if !ok {
					t.Errorf("failed %s", condition)
				}
			}
			pos := [3]int32{4, 70, 4}
			edit := func(at [3]int32, mode int32, message string, expect bool) map[string]any {
				time.Sleep(120 * time.Millisecond)
				sink.take()
				var b protocol.Buffer
				b.Position(at[0], at[1], at[2])
				b.VarInt(mode)
				b.String(message)
				if err := pw.WritePacket(server.IDPlaySetTestBlockSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
				count := 0
				var tag map[string]any
				collect := func(lines []string) {
					for _, u := range structureBEUpdatesBB(t, lines) {
						if u.pos == at {
							count++
							tag = u.tag
						}
					}
				}
				collect(barrier())
				if expect {
					deadline := time.Now().Add(2 * time.Second)
					for tag == nil && time.Now().Before(deadline) {
						time.Sleep(10 * time.Millisecond)
						collect(sink.take())
					}
				}
				time.Sleep(120 * time.Millisecond)
				collect(sink.take())
				out.Counts = append(out.Counts, count)
				want := 0
				if expect {
					want = 1
				}
				if count != want {
					t.Errorf("mode%d at%v updateCount%d want%d tag%#v", mode, at, count, want, tag)
				}
				return tag
			}
			command("tick freeze")
			command("setblock 0 69 0 stone")
			command("tp @s 0.5 70 0.5")
			command("setblock 4 70 4 minecraft:test_block[mode=log]")
			check(`if data block 4 70 4 {mode:"log",message:"",powered:0b}`)
			first := edit(pos, 2, "  λ raw:[]  ", true)
			for _, key := range []string{"mode", "message", "powered"} {
				out.First = append(out.First, fmt.Sprintf("%T:%v", first[key], first[key]))
			}
			wantFirst := []string{"string:fail", "string:  λ raw:[]  ", "int8:0"}
			if !reflect.DeepEqual(out.First, wantFirst) {
				t.Errorf("first update%v want%v", out.First, wantFirst)
			}
			check(`if block 4 70 4 minecraft:test_block[mode=fail]`)
			check(`if data block 4 70 4 {mode:"fail",message:"  λ raw:[]  ",powered:0b}`)
			edit(pos, 2, "  λ raw:[]  ", true)
			for _, entry := range []struct {
				id   int32
				mode string
			}{{0, "start"}, {1, "log"}, {3, "accept"}, {-1, "start"}, {2147483647, "start"}} {
				edit(pos, entry.id, "next", true)
				check("if block 4 70 4 minecraft:test_block[mode=" + entry.mode + "]")
				check(`if data block 4 70 4 {mode:"` + entry.mode + `",message:"next",powered:0b}`)
			}
			command(`data merge block 4 70 4 {mode:"accept",message:"native",powered:1b,unknown:1}`)
			check(`if block 4 70 4 minecraft:test_block[mode=start]`)
			check(`if data block 4 70 4 {mode:"accept",message:"native",powered:1b}`)
			check("unless data block 4 70 4 unknown")
			command(`setblock 4 70 4 minecraft:test_block[mode=accept]{message:"raw native",powered:1b,unknown:1}`)
			check(`if block 4 70 4 minecraft:test_block[mode=accept]`)
			check(`if data block 4 70 4 {mode:"fail",message:"raw native",powered:1b}`)
			check("unless data block 4 70 4 unknown")
			edit(pos, 1, "after native", true)
			check(`if data block 4 70 4 {mode:"log",message:"after native",powered:1b}`)
			command("gamemode survival")
			edit(pos, 3, "denied", false)
			check(`if data block 4 70 4 {mode:"log",message:"after native",powered:1b}`)
			command("gamemode creative")
			command("setblock 5 70 4 stone")
			edit([3]int32{5, 70, 4}, 0, "wrong", false)
			check("if block 5 70 4 minecraft:stone")
			t.Logf("typed first%v counts%v checks%v", out.First, out.Counts, out.Checks)
		})
	}
}
