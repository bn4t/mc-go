package blackbox

import (
	"fmt"
	"os"
	"testing"

	"mc-go/internal/server"
)

// TestCommandsLevelProbe captures the vanilla Commands packet for a
// player whose op level is 0..4, so the command tree's per-node
// permission thresholds can be read off the real server.
func TestCommandsLevelProbe(t *testing.T) {
	for level := 0; level <= 4; level++ {
		level := level
		t.Run(fmt.Sprintf("level%d", level), func(t *testing.T) {
			name := fmt.Sprintf("Lvl%dProbe", level)
			files := map[string]string{}
			if level > 0 {
				id := server.OfflineUUID(name)
				files["ops.json"] = fmt.Sprintf(
					`[{"uuid":"%s","name":"%s","level":%d,"bypassesPlayerLimit":true}]`,
					formatUUID(id), name, level)
			}
			tg := requireBlackboxFiles(t, nil, files, nil)
			defer tg.cleanup()
			c, pr, _ := joinPlayBlackbox(t, tg.vanilla, name)
			defer c.Close()
			for i := 0; i < 40; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Fatalf("read: %v", err)
				}
				if p.ID == server.IDPlayCommands {
					out := fmt.Sprintf("/tmp/vanilla_commands_l%d.bin", level)
					if err := os.WriteFile(out, p.Data, 0o644); err != nil {
						t.Fatal(err)
					}
					t.Logf("level %d commands len=%d -> %s", level, len(p.Data), out)
					return
				}
			}
			t.Fatal("no commands packet")
		})
	}
}

func formatUUID(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
