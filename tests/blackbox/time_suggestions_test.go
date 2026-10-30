//go:build blackbox

package blackbox

import (
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"testing"
)

func TestTimeSuggestionsParity(t *testing.T) {
	const name = "TimeSuggest"
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) {
		s.OpsRequired = true
		s.GameMode = server.GameModeCreative
		s.SetOpLevel(name, 4)
	})
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			s := joinPlayOp(t, tc.addr, name)
			defer s.conn.Close()
			cases := []struct {
				text          string
				start, length int32
				values        []string
			}{
				{"/gi", 1, 2, []string{"give"}},
				{"/GI", 1, 2, []string{"give"}},
				{"/time", 0, 0, nil},
				{"/zz_no_command", 0, 0, nil},
				{"/time ", 6, 0, []string{"add", "of", "pause", "query", "rate", "resume", "set"}},
				{"/time s", 6, 1, []string{"set"}},
				{"/time set ", 10, 0, []string{"minecraft:day", "minecraft:midnight", "minecraft:night", "minecraft:noon"}},
				{"/time set n", 10, 1, []string{"minecraft:night", "minecraft:noon"}},
				{"/time set 1", 11, 0, []string{"d", "s", "t"}},
				{"/time set 1s", 0, 0, nil},
				{"/time set 1q", 0, 0, nil},
				{"/time add 1.2", 13, 0, []string{"d", "s", "t"}},
				{"/time of ", 9, 0, []string{"minecraft:overworld", "minecraft:the_end"}},
				{"/time of overworld ", 19, 0, []string{"add", "pause", "query", "rate", "resume", "set"}},
				{"/time of the_end set ", 0, 0, nil},
				{"/time query ", 12, 0, []string{"gametime", "minecraft:day", "minecraft:early_game", "minecraft:moon", "minecraft:villager_schedule", "time"}},
				{"/time query day ", 16, 0, []string{"repetition"}},
				{"/time of overworld query ", 25, 0, []string{"minecraft:day", "minecraft:early_game", "minecraft:moon", "minecraft:villager_schedule", "time"}},
				{"/time of the_end query ", 23, 0, []string{"time"}},
				{"/time query schedule", 12, 8, []string{"minecraft:villager_schedule"}},
				{"/time of end", 9, 3, []string{"minecraft:the_end"}},
				{"/time query game", 12, 4, []string{"gametime", "minecraft:early_game"}},
				{"/time set -1", 12, 0, []string{"d", "s", "t"}},
				{"/time set 1S", 11, 1, []string{"s"}},
				{"/time S", 6, 1, []string{"set"}},
				{"/time of the_end query day ", 27, 0, []string{"repetition"}},
				{"/time of the_end set 1", 22, 0, []string{"d", "s", "t"}},
				{"/time query minecraft:day", 0, 0, nil},
				{"/time set minecraft:roll", 0, 0, nil},
				{"/time set minecraft:noon", 0, 0, nil},
			}
			for _, actor := range []string{name, "TimeNoSuggest"} {
				if actor != name {
					s.conn.Close()
					s = joinPlayOp(t, tc.addr, actor)
					defer s.conn.Close()
					cases = []struct {
						text          string
						start, length int32
						values        []string
					}{{"/time ", 0, 0, nil}, {"/time set n", 0, 0, nil}}
				}
				for i, want := range cases {
					text := want.text
					var b protocol.Buffer
					b.VarInt(int32(i))
					b.String(text)
					if err := s.pw.WritePacket(server.IDPlayCommandSuggestionSB, b.Bytes()); err != nil {
						t.Fatal(err)
					}
					p := readUntil(t, tc.name, s, func(p protocol.Packet) bool { return p.ID == server.IDPlayCommandSuggestionsCB })
					r := protocol.NewPacketReader(p.Data)
					tx, e := r.VarInt()
					if e != nil || tx != int32(i) {
						t.Fatalf("transaction%d %v", tx, e)
					}
					start, e := r.VarInt()
					if e != nil {
						t.Fatal(e)
					}
					length, e := r.VarInt()
					if e != nil {
						t.Fatal(e)
					}
					n, e := r.VarInt()
					if e != nil {
						t.Fatal(e)
					}
					var matches []string
					for j := int32(0); j < n; j++ {
						m, e := r.String()
						if e != nil {
							t.Fatal(e)
						}
						tip, e := r.Bool()
						if e != nil || tip {
							t.Fatalf("tooltip%v %v", tip, e)
						}
						matches = append(matches, m)
					}
					if start != want.start || length != want.length || !reflect.DeepEqual(matches, want.values) {
						t.Errorf("%q range(%d,%d) values%q want(%d,%d)%q", text, start, length, matches, want.start, want.length, want.values)
					}
				}
			}
		})
	}
}
