package blackbox

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestSpectatorTeleportEntityParity measures UUID-targeted spectator travel,
// including mob targets, target orientation, and the non-spectator gate.
// Oracle: official 26.2 server SHA-1 823e2250d24b3ddac457a60c92a6a941943fcd6a;
// wire observation plus JDK 25 javap measurement of
// ServerGamePacketListenerImpl.handleTeleportToEntityPacket.
func TestSpectatorTeleportEntityParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	run := func(addr string) [][]float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		command := func(cmd string) {
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say SPECTATOR_BARRIER")
			deadline := time.Now().Add(45 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, "SPECTATOR_BARRIER") {
						return
					}
				}
				time.Sleep(30 * time.Millisecond)
			}
			t.Fatalf("command did not complete: %s", cmd)
		}
		command("tick freeze")
		command("tp @s 0.5 70.5 0.5 0 0")
		command("summon minecraft:pig 12.5 70.5 15.5 {NoAI:1b,NoGravity:1b,UUID:[I;0,0,0,12345],Rotation:[75f,20f]}")
		teleport := func(id byte) {
			var b protocol.Buffer
			var uuid [16]byte
			uuid[14], uuid[15] = 48, id
			b.UUID(uuid)
			_ = pw.WritePacket(server.IDPlayTeleportToEntitySB, b.Bytes())
			time.Sleep(350 * time.Millisecond)
		}
		sample := func() []float64 {
			pos := dataGetFloats(t, sink, pw, "@s", "Pos")
			_, yaw := dataGetSel(t, sink, pw, "@s", "Rotation[0]")
			_, pitch := dataGetSel(t, sink, pw, "@s", "Rotation[1]")
			return append(pos, yaw, pitch)
		}
		teleport(57)
		out := [][]float64{sample()}
		command("gamemode spectator")
		teleport(57)
		out = append(out, sample())
		teleport(58)
		out = append(out, sample())
		// A player target also verifies UUID resolution across dimensions.
		q, qpr, qpw := joinPlayBlackbox(t, addr, "Q")
		defer q.Close()
		_ = q.SetDeadline(time.Time{})
		_ = qpw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		newChatSinkTP(qpr, qpw)
		command("execute in minecraft:the_nether run tp Q 8.5 70.5 8.5")
		command("rotate Q 110 35")
		sink.take()
		var target protocol.Buffer
		target.UUID(offlineUUIDBlackbox("Q"))
		_ = pw.WritePacket(server.IDPlayTeleportToEntitySB, target.Bytes())
		respawn := false
		deadline := time.Now().Add(30 * time.Second)
		for !respawn && time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.HasPrefix(line, fmt.Sprintf("%#x:", server.IDPlayRespawn)) {
					respawn = true
				}
			}
			if !respawn {
				time.Sleep(30 * time.Millisecond)
			}
		}
		if !respawn {
			t.Fatalf("%s cross-dimension spectator teleport omitted Respawn", addr)
		}
		out = append(out, sample())
		return out
	}
	want := run(targets.vanilla)
	t.Logf("vanilla: %v", want)
	got := run(targets.mcgo)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vanilla %v; mc-go %v", want, got)
	}
	if !reflect.DeepEqual(want[3], []float64{8.5, 70.5, 8.5, 110, 35}) {
		t.Fatalf("oracle cross-dimension teleport failed: %v", want)
	}
	if !reflect.DeepEqual(want[1], []float64{12.5, 70.5, 15.5, 75, 20}) {
		t.Fatalf("oracle did not teleport: %v", want)
	}
}
