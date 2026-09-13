package blackbox

import (
	"os"
	"testing"

	"mc-go/internal/server"
)

// TestCommandsProbeDiagnostic dumps the vanilla Commands packet body to
// a file so the tree shape can be diffed against the official
// commands.json report. Development aid; run with -v.
func TestCommandsProbeDiagnostic(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr, out string }{
		{"vanilla", tg.vanilla, "/tmp/vanilla_commands.bin"},
		{"mc-go", tg.mcgo, "/tmp/mcgo_commands.bin"},
	} {
		c, pr, _ := joinPlayBlackbox(t, tc.addr, "CmdProbe")
		func() {
			defer c.Close()
			for i := 0; i < 30; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Logf("%s: read %d: %v", tc.tag, i, err)
					return
				}
				if p.ID == server.IDPlayCommands {
					if err := os.WriteFile(tc.out, p.Data, 0o644); err != nil {
						t.Fatal(err)
					}
					t.Logf("%s commands packet len=%d -> %s", tc.tag, len(p.Data), tc.out)
					return
				}
			}
			t.Logf("%s: no commands packet", tc.tag)
		}()
	}
}
