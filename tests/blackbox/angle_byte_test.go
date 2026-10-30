//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEntitySpawnAngleBytesParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("AngleProbe")}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.SetOpLevel("AngleProbe", 4) })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "AngleProbe", 1)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			sendCommand(t, pw, "gamerule minecraft:spawn_mobs false")
			sendCommand(t, pw, "tp @s 0.5 -60 0.5")
			for i, row := range []struct {
				yaw, pitch         float32
				wantYaw, wantPitch byte
			}{
				{-.1, -.1, 255, 255}, {math.Float32frombits(0xbfb40001), math.Float32frombits(0x3fb3ffff), 254, 0}, {-360, 360, 0, 0}, {-180, 90, 128, 64},
			} {
				x := float64(i) + 3.5
				sendCommand(t, pw, fmt.Sprintf("summon minecraft:pig %g -60 0.5 {NoAI:1b,NoGravity:1b,PersistenceRequired:1b,Rotation:[%gf,%gf]}", x, row.yaw, row.pitch))
				found := false
				deadline := time.Now().Add(10 * time.Second)
				for !found && time.Now().Before(deadline) {
					for _, line := range sink.take() {
						colon := strings.IndexByte(line, ':')
						if colon < 0 {
							continue
						}
						id, e := strconv.ParseInt(line[:colon], 0, 32)
						if e != nil || int32(id) != server.LatestVersion().IDs.PlaySpawnEntity {
							continue
						}
						r := protocol.NewPacketReader([]byte(line[colon+1:]))
						r.VarInt()
						r.UUID()
						kind, e := r.VarInt()
						if e != nil || kind != server.LatestVersion().IDs.EntityTypePig {
							continue
						}
						px, _ := r.Double()
						py, _ := r.Double()
						pz, _ := r.Double()
						if px != x || py != -60 || pz != .5 {
							continue
						}
						if _, _, _, e := r.ReadLpVec3(); e != nil {
							t.Fatal(e)
						}
						pitch, e := r.Byte()
						if e != nil {
							t.Fatal(e)
						}
						yaw, e := r.Byte()
						if e != nil {
							t.Fatal(e)
						}
						if yaw != row.wantYaw || pitch != row.wantPitch {
							t.Errorf("case%d raw%g/%g got%d/%d want%d/%d", i, row.yaw, row.pitch, yaw, pitch, row.wantYaw, row.wantPitch)
						}
						t.Logf("case%d spawn yaw%d pitch%d", i, yaw, pitch)
						found = true
					}
					if !found {
						time.Sleep(10 * time.Millisecond)
					}
				}
				if !found {
					t.Fatalf("case%d spawn packet missing", i)
				}
			}
		})
	}
}
