package blackbox

import (
	"encoding/hex"
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestConfigBurstDiagnostic logs the first config packets of both
// servers with hex bodies. It is a development aid for shape parity:
// run with -v when reconciling a packet's fields.
func TestConfigBurstDiagnostic(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()
	for _, tc := range []struct{ tag, addr string }{
		{"vanilla", tg.vanilla},
		{"mc-go", tg.mcgo},
	} {
		c, pr, pw := loginStart(t, tc.addr, "CfgProbe")
		func() {
			defer c.Close()
			readLoginSuccess(t, pr, pw, tc.tag)
			if err := pw.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
				t.Fatal(err)
			}
			sendKnownPacks(t, pw, "99.9-test")
			for i := 0; i < 6; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Logf("%s: read %d: %v", tc.tag, i, err)
					return
				}
				t.Logf("%s cfg #%d id=%#x len=%d body=%s", tc.tag, i, p.ID, len(p.Data), hex.EncodeToString(p.Data))
				if p.ID == server.IDConfigFinish {
					return
				}
			}
		}()
	}
}

var _ = protocol.NewPacketReader
