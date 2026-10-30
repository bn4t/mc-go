//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"math"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlayerFiniteRotationParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("RotDriver", "RotObserver")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.SetOpLevel("RotDriver", 4)
		s.SetOpLevel("RotObserver", 4)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, "RotDriver", 1)
			defer c.Close()
			var eid int32
			for eid == 0 {
				p, e := pr.ReadPacket()
				if e != nil {
					t.Fatal(e)
				}
				if p.ID == server.IDPlayJoinGame {
					eid, e = protocol.NewPacketReader(p.Data).Int()
					if e != nil {
						t.Fatal(e)
					}
				}
			}
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			driver := newChatSinkTP(pr, pw)
			driver.mu.Lock()
			driver.cap = 1 << 20
			driver.mu.Unlock()
			oc, opr, opw := joinPlayBlackboxView(t, target.addr, "RotObserver", 1)
			defer oc.Close()
			drainToJoinBlackbox(t, target.name, opr)
			awaitPlayReadyBlackbox(t, target.name, oc, opr, opw)
			observer := newChatSinkTP(opr, opw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("ROTATION_MARK_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, driver, marker, 10*time.Second)
			}
			command("tp RotDriver 0.5 -60 0.5 0 0")
			command("tp RotObserver 2.5 -60 0.5 180 0")
			receipt := func() {
				serial++
				tx := int32(serial)
				var b protocol.Buffer
				b.VarInt(tx)
				b.VarInt(eid)
				if e := pw.WritePacket(server.IDPlayEntityTagQuerySB, b.Bytes()); e != nil {
					t.Fatal(e)
				}
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					for _, line := range driver.take() {
						prefix := fmt.Sprintf("%#x:", server.IDPlayTagQueryCB)
						if !strings.HasPrefix(line, prefix) {
							continue
						}
						r := bytes.NewReader([]byte(strings.TrimPrefix(line, prefix)))
						got, e := protocol.ReadVarInt(r)
						if e == nil && got == tx {
							return
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("entity-query movement receipt missing")
			}
			receipt()
			for i, rotation := range [][2]float32{{450, 100}, {-450, -100}, {180, 180}, {-180, -180}, {540, 270}, {-540, -270}, {360, 360}, {-360, -360}, {720.5, 91}, {-720.5, -91}, {.1, .1}, {-.1, -.1}, {math.MaxFloat32, -math.MaxFloat32}, {float32(math.Copysign(0, -1)), float32(math.Copysign(0, -1))}, {16777216, -16777216}} {
				before := gameTimeOf(t, pw, driver)
				var b protocol.Buffer
				packet := int32(server.IDPlayMoveRotSB)
				if i%2 != 0 {
					packet = server.IDPlayMovePosRotSB
					b.Double(.5)
					b.Double(-60)
					b.Double(.5)
				}
				b.Float(rotation[0])
				b.Float(rotation[1])
				b.Byte(1)
				if e := pw.WritePacket(packet, b.Bytes()); e != nil {
					t.Fatal(e)
				}
				receipt()
				var pose string
				for _, line := range command("data get entity RotDriver Rotation") {
					key, args, ok := componentArgs(line)
					if ok && key == "commands.data.entity.query" && len(args) == 2 {
						pose = playerRotationTextBB(args[1])
					}
				}
				deadline := time.Now().Add(10 * time.Second)
				for gameTimeOf(t, pw, driver) < before+4 {
					if time.Now().After(deadline) {
						t.Fatal("game ticks did not advance")
					}
				}
				serial++
				marker := fmt.Sprintf("ROTATION_OBSERVER_%d", serial)
				sendCommand(t, pw, "say "+marker)
				waitForExactSayBB(t, driver, marker, 10*time.Second)
				waitForExactSayBB(t, observer, marker, 10*time.Second)
				want := []string{"[90.0f, 90.0f]", "[-90.0f, -90.0f]", "[-180.0f, -90.0f]", "[-180.0f, -90.0f]", "[-180.0f, -90.0f]", "[-180.0f, 90.0f]", "[0.0f, 0.0f]", "[0.0f, 0.0f]", "[0.5f, 90.0f]", "[-0.5f, -90.0f]", "[0.1f, 0.1f]", "[-0.1f, -0.1f]", "[0.0f, 0.0f]", "[0.0f, 0.0f]", "[136.0f, -90.0f]"}[i]
				if playerRotationPairBB(pose) != playerRotationPairBB(want) {
					t.Errorf("case%d raw%v server%q want%q", i, rotation, pose, want)
				}
				t.Logf("case%d raw[%g,%g] server%s", i, rotation[0], rotation[1], pose)
			}
		})
	}
}
func playerRotationTextBB(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		text, _ := v["text"].(string)
		if compact, ok := v[""].(string); ok {
			text = compact
		}
		return text + playerRotationTextBB(v["extra"])
	case nbt.List:
		var b strings.Builder
		for _, part := range v.Items {
			b.WriteString(playerRotationTextBB(part))
		}
		return b.String()
	}
	return ""
}

func playerRotationPairBB(text string) [2]float32 {
	out := [2]float32{float32(math.NaN()), float32(math.NaN())}
	parts := strings.Split(strings.Trim(text, "[]"), ",")
	if len(parts) != 2 {
		return out
	}
	for i, part := range parts {
		value, e := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(part), "f"), 32)
		if e != nil {
			return out
		}
		out[i] = float32(value)
	}
	return out
}
