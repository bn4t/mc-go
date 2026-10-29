package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"testing"
	"time"
)

type scheduledProbeBB struct {
	t      *testing.T
	pw     *protocol.Writer
	sink   *chatSink
	serial int
}

func (p *scheduledProbeBB) command(cmd string) []string {
	p.t.Helper()
	sendCommand(p.t, p.pw, cmd)
	p.serial++
	marker := fmt.Sprintf("SCHEDULED_END_%d", p.serial)
	sendCommand(p.t, p.pw, "say "+marker)
	return waitForExactSayBB(p.t, p.sink, marker, 10*time.Second)
}
func (p *scheduledProbeBB) expect(condition, label string) {
	p.t.Helper()
	marker := "SCHEDULED_RESULT_" + label
	if !structureSaidBB(p.command("execute "+condition+" run say "+marker), marker) {
		p.t.Errorf("missing %s: %s", label, condition)
	}
}

// An applied /tp reply does not prove its client teleport was acknowledged.
// A small accepted movement is a receipt that use packets can be processed.
func (p *scheduledProbeBB) moveReady(x, y, z float64) {
	p.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		b.Byte(1)
		if err := p.pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
			p.t.Fatal(err)
		}
		if err := p.pw.WritePacket(server.IDPlayClientTickEndSB, nil); err != nil {
			p.t.Fatal(err)
		}
		lines := p.command(fmt.Sprintf("execute if entity @s[nbt={Pos:[%gd,%gd,%gd]}] run say SCHEDULED_MOVEMENT_READY", x, y, z))
		if structureSaidBB(lines, "SCHEDULED_MOVEMENT_READY") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatal("no accepted movement receipt before scheduled button use")
}

// Interaction packets may be applied after a later chat reply. Observe the
// actual powered state before replacing its type or advancing world time.
func (p *scheduledProbeBB) await(condition, label string) {
	p.t.Helper()
	marker := "SCHEDULED_RECEIPT_" + label
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if structureSaidBB(p.command("execute "+condition+" run say "+marker), marker) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatalf("missing applied state receipt %s: %s", label, condition)
}

func (p *scheduledProbeBB) step(n int) {
	p.t.Helper()
	before := gameTimeOf(p.t, p.pw, p.sink)
	p.command(fmt.Sprintf("tick step %d", n))
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		now := gameTimeOf(p.t, p.pw, p.sink)
		if now == before+int64(n) {
			return
		}
		if now > before+int64(n) {
			p.t.Fatalf("step%d advanced%d ticks", n, now-before)
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatalf("step%d did not finish", n)
}
func withScheduledProbeBB(t *testing.T, body func(*scheduledProbeBB)) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("ScheduledProbe")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "ScheduledProbe")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			p := &scheduledProbeBB{t: t, pw: pw, sink: newChatSinkTP(pr, pw)}
			p.command("tick freeze")
			body(p)
		})
	}
}

func TestScheduledBlockIdentityParity(t *testing.T) {
	withScheduledProbeBB(t, func(p *scheduledProbeBB) {
		p.command("setblock 4 -60 4 stone_button[face=floor]")
		p.command("setblock 8 -60 4 stone_button[face=floor]")
		p.command("tp @s 4.5 -60 5.5 180 56")
		p.moveReady(4.6, -60, 5.5)
		sendUseItemOnCursor(p.t, p.pw, 4, -60, 4, 1, 0.5, 0.125, 0.5, 1)
		p.await("if block 4 -60 4 stone_button[powered=true]", "old_type_armed")
		p.command("tp @s 8.5 -60 5.5 180 56")
		p.moveReady(8.6, -60, 5.5)
		sendUseItemOnCursor(p.t, p.pw, 8, -60, 4, 1, 0.5, 0.125, 0.5, 2)
		p.await("if block 8 -60 4 stone_button[powered=true]", "control_armed")
		p.command("setblock 4 -60 4 oak_button[face=floor,powered=true] strict")
		p.expect("if block 4 -60 4 oak_button[powered=true]", "replacement")
		p.command("setblock 12 -60 4 repeater[facing=north,delay=4]")
		p.command("setblock 12 -60 3 redstone_block")
		p.expect("if block 12 -60 4 repeater[powered=false,delay=4]", "long_delay_armed")
		p.step(2)
		p.command("setblock 12 -60 4 repeater[facing=north,delay=1]")
		p.command("setblock 12 -60 3 air")
		p.command("setblock 12 -60 3 redstone_block")
		p.expect("if block 12 -60 4 repeater[powered=false,delay=1]", "short_delay_requested")
		p.step(2)
		p.expect("if block 12 -60 4 repeater[powered=false]", "first_deadline_retained")
		p.step(4)
		p.expect("if block 12 -60 4 repeater[powered=true]", "first_deadline_fired")
		p.step(11)
		p.expect("if block 4 -60 4 oak_button[powered=true]", "replacement_before_due")
		p.expect("if block 8 -60 4 stone_button[powered=true]", "control_before_due")
		p.step(1)
		p.expect("if block 4 -60 4 oak_button[powered=true]", "stale_type_ignored")
		p.expect("if block 8 -60 4 stone_button[powered=false]", "control_due")
	})
}
