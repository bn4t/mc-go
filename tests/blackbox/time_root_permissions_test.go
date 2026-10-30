//go:build blackbox

package blackbox

import (
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"testing"
)

func TestTimeRootPermissionsParity(t *testing.T) {
	const op = "RootPermOp"
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor(op)}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel(op, 4)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			for _, actor := range []string{op, "RootPermNoOp"} {
				s := joinPlayOp(t, target.addr, actor)
				defer s.conn.Close()
				// The last row verifies the actor's effective argument gate;
				// root-name suggestions follow a distinct native boundary.
				cases := []struct {
					text          string
					start, length int32
					values        []string
				}{
					{"/gi", 1, 2, []string{"give"}},
					{"/time", 0, 0, nil},
					{"/ti", 1, 2, []string{"tick", "time", "title"}},
					{"/time set n", 10, 1, []string{"minecraft:night", "minecraft:noon"}},
				}
				if actor != op {
					cases[3].start = 0
					cases[3].length = 0
					cases[3].values = nil
				}
				for i, want := range cases {
					text := want.text
					var b protocol.Buffer
					b.VarInt(int32(i))
					b.String(text)
					if err := s.pw.WritePacket(server.IDPlayCommandSuggestionSB, b.Bytes()); err != nil {
						t.Fatal(err)
					}
					p := readUntil(t, target.name, s, func(p protocol.Packet) bool { return p.ID == server.IDPlayCommandSuggestionsCB })
					r := protocol.NewPacketReader(p.Data)
					tx, e := r.VarInt()
					if e != nil || tx != int32(i) {
						t.Fatalf("tx%d %v", tx, e)
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
						t.Errorf("actor%s %q range(%d,%d) values%q want(%d,%d)%q", actor, text, start, length, matches, want.start, want.length, want.values)
					}
				}
				s.conn.Close()
			}
		})
	}
}
