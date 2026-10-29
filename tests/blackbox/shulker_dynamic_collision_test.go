package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestShulkerDynamicCollisionParity(t *testing.T) {
	runShulkerDynamicCollisionParity(t, "east", 2, 1.34, false)
}

func TestShulkerNegativeCollisionParity(t *testing.T) {
	runShulkerDynamicCollisionParity(t, "west", -1, -.34, false)
}

func TestShulkerSpectatorCollisionParity(t *testing.T) {
	runShulkerDynamicCollisionParity(t, "east", 2, 1.34, true)
}

func runShulkerDynamicCollisionParity(t *testing.T, facing string, start, target float64, spectator bool) {
	const actor, observer = "LidOpener", "LidWalker"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor(actor, observer)}, nil)
	defer targets.cleanup()
	run := func(addr string) []float64 {
		d := newClickDriverOn(t, addr, actor, "minecraft:shulker_box", 20, nil, nil)
		d.close()
		off := 0
		for _, c := range []byte(actor) {
			off = (31*off + int(c)) & 15
		}
		x := float64(8 + 32*off)
		conn, pr, pw := joinPlayBlackbox(t, addr, observer)
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, fmt.Sprintf("fill %d 159 -2 %d 159 2 stone", int(x)-2, int(x)+4))
		sendCommand(t, d.pw, fmt.Sprintf("tp @s %.1f 160 2", x-2))
		sendCommand(t, pw, fmt.Sprintf("setblock %d 160 0 shulker_box[facing=%s]", int(x), facing))
		shulkerStepTicks(t, pw, sink, 12)
		move := func(tag string) float64 {
			sendCommand(t, pw, fmt.Sprintf("tp @s %.2f 160 0.5", x+start))
			// Readback confirms the teleport was consumed before the attempted move.
			sx, _ := dataGetVec2(t, sink, pw, "@s", "Pos")
			if math.Abs(sx-(x+start)) > 1e-6 {
				t.Fatalf("%s %s setupx=%v", addr, tag, sx)
			}
			var b protocol.Buffer
			b.Double(x + target)
			b.Double(160)
			b.Double(.5)
			b.Byte(0)
			if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
			got, _ := dataGetVec2(t, sink, pw, "@s", "Pos")
			t.Logf("%s %s relativeX=%.8f", addr, tag, got-x)
			return got - x
		}

		if spectator {
			sendCommand(t, d.pw, "gamemode spectator @s")
			if !digSentinel(t, d.pw, d.sink, "SPECTATORREADY", 10*time.Second) {
				t.Fatal("spectator mode not established")
			}
		}
		out := []float64{move("closed")}
		d.reopen(int32(x), 160, 0, 40, "open-lid")
		if !judgeUse(d.opened).open {
			t.Fatal("shulker failed to open")
		}
		out = append(out, move("opened-no-tick"))
		shulkerStepTicks(t, pw, sink, 1)
		out = append(out, move("opened-one-tick"))
		shulkerStepTicks(t, pw, sink, 9)
		out = append(out, move("fully-open"))
		d.close()
		out = append(out, move("closed-no-tick"))
		shulkerStepTicks(t, pw, sink, 5)
		out = append(out, move("closing-half"))
		shulkerStepTicks(t, pw, sink, 5)
		out = append(out, move("fully-closed"))
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, g := run(targets.vanilla), run(targets.mcgo)
	want := []float64{target, target, start, start, start, start, target}
	if spectator {
		for i := range want {
			want[i] = target
		}
	}
	for i := range want {
		if math.Abs(v[i]-want[i]) > 1e-6 || math.Abs(g[i]-v[i]) > 1e-6 {
			t.Errorf("step%d vanilla=%v mcgo=%v want=%v", i, v[i], g[i], want[i])
		}
	}
}

// A sprint request can include an extra transition tick. Phase-sensitive
// collision checks use stepped ticks and verify the actual game clock.
func shulkerStepTicks(t *testing.T, pw *protocol.Writer, sink *chatSink, ticks int) {
	t.Helper()
	before := gameTimeOf(t, pw, sink)
	settleDeadline := time.Now().Add(10 * time.Second)
	for {
		time.Sleep(150 * time.Millisecond)
		stable := gameTimeOf(t, pw, sink)
		if stable == before {
			break
		}
		if time.Now().After(settleDeadline) {
			t.Fatal("shulker phase clock did not freeze")
		}
		before = stable
	}
	want := before + int64(ticks)
	sendCommand(t, pw, fmt.Sprintf("tick step %d", ticks))
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got := gameTimeOf(t, pw, sink)
		if got > want {
			t.Fatalf("shulker phase advanced %d ticks, want%d", got-before, ticks)
		}
		if got == want {
			// A second observation after normal server frames rejects a pending
			// extra tick rather than accepting the first matching clock reply.
			time.Sleep(150 * time.Millisecond)
			if stable := gameTimeOf(t, pw, sink); stable != want {
				t.Fatalf("shulker phase clock changed after completion: got%d want%d", stable, want)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("shulker phase never completed %d stepped ticks", ticks)
}
