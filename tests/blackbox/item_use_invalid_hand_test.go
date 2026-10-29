package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

// Enum values are decoded before item dispatch, so malformed hands must not
// become main-hand uses. Rejoining verifies the persisted inventory too.
func TestItemUseInvalidHandParity(t *testing.T) {
	names := []string{"HandAudit", "HandBad0", "HandBad1", "HandBad2", "HandBad3"}
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer targets.cleanup()
	type result struct {
		reason                        string
		disconnect, closed, unchanged bool
		acks                          int
	}
	run := func(addr string) []result {
		observer, pr, pw := joinPlayBlackbox(t, addr, names[0])
		defer observer.Close()
		awaitPlayReadyBlackbox(t, addr, observer, pr, pw)
		audit := newChatSinkTP(pr, pw)
		collect := func(sink *chatSink, marker string) []string {
			var lines []string
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				lines = append(lines, sink.take()...)
				if structureSaidBB(lines, marker) {
					return lines
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing %s", addr, marker)
			return nil
		}
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 8 160 8")
		sendCommand(t, pw, "forceload add -16 -16 0 0")
		sendCommand(t, pw, "say BADAUDITREADY")
		collect(audit, "BADAUDITREADY")
		loaded := false
		deadline := time.Now().Add(15 * time.Second)
		for n := 0; !loaded && time.Now().Before(deadline); n++ {
			marker, end := fmt.Sprintf("BADCHUNKS%dX", n), fmt.Sprintf("BADCHUNKSEND%dX", n)
			sendCommand(t, pw, "execute if loaded -16 159 -16 if loaded -16 159 0 if loaded 0 159 -16 if loaded 0 159 0 run say "+marker)
			sendCommand(t, pw, "say "+end)
			loaded = structureSaidBB(collect(audit, end), marker)
		}
		if !loaded {
			t.Fatalf("%s invalid hand pad chunks not ready", addr)
		}
		var out []result
		for i := 0; i < 4; i++ {
			name := names[i+1]
			conn, r, w := joinPlayBlackbox(t, addr, name)
			awaitPlayReadyBlackbox(t, addr, conn, r, w)
			sink := newChatSinkTP(r, w)
			sendCommand(t, w, "gamemode creative @s")
			sendCommand(t, w, "tp @s 8 160 8")
			sendCommand(t, w, "fill -2 159 -3 2 164 3 air")
			sendCommand(t, w, "fill -2 159 -3 2 159 3 stone")
			sendCommand(t, w, "clear @s")
			sendCommand(t, w, "item replace entity @s weapon.mainhand with stone 3")
			sendCommand(t, w, "item replace entity @s weapon.offhand with dirt 5")
			sendCommand(t, w, "gamemode survival @s")
			sendCommand(t, w, "tp @s 0.5 160 -2.5")
			ready, end := fmt.Sprintf("BADHANDREADY%dX", i), fmt.Sprintf("BADHANDSETUP%dX", i)
			condition := `execute if entity @s[nbt={Inventory:[{Slot:0b,id:"minecraft:stone",count:3}],equipment:{offhand:{id:"minecraft:dirt",count:5}}}] if block 0 159 0 stone if block 0 160 0 air`
			sendCommand(t, w, condition+" run say "+ready)
			sendCommand(t, w, "say "+end)
			setupLines := collect(sink, end)
			if !structureSaidBB(setupLines, ready) {
				t.Fatalf("%s %s setup missing", addr, name)
			}
			sink.take()
			hand := int32(-1)
			if i%2 == 1 {
				hand = 2
			}
			seq := int32(3800 + i)
			var b protocol.Buffer
			b.VarInt(hand)
			packet := int32(server.IDPlayUseItemOnSB)
			if i < 2 {
				b.Position(0, 159, 0)
				b.VarInt(1)
				b.Float(.5)
				b.Float(1)
				b.Float(.5)
				b.Bool(false)
				b.Bool(false)
				b.VarInt(seq)
			} else {
				packet = server.IDPlayUseItemSB
				b.VarInt(seq)
				b.Float(0)
				b.Float(35)
			}
			if err := w.WritePacket(packet, b.Bytes()); err != nil {
				t.Fatal(err)
			}
			var lines []string
			deadline := time.Now().Add(5 * time.Second)
			got := result{}
			for time.Now().Before(deadline) {
				lines = append(lines, sink.take()...)
				sink.mu.Lock()
				closed := sink.err != nil
				sink.mu.Unlock()
				if closed {
					got.closed = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			lines = append(lines, sink.take()...)
			for _, line := range lines {
				if strings.HasPrefix(line, fmt.Sprintf("%#x:", server.IDPlayDisconnect)) {
					got.disconnect = true
					payload := strings.TrimPrefix(line, fmt.Sprintf("%#x:", server.IDPlayDisconnect))
					value, err := nbt.ReadUnnamed(bytes.NewReader([]byte(payload)))
					if err != nil {
						t.Fatalf("%s disconnect NBT: %v", addr, err)
					}
					got.reason = fmt.Sprintf("%#v", value)
				}
				if strings.HasPrefix(line, fmt.Sprintf("%#x:", server.IDPlayBlockChangedAck)) {
					got.acks++
				}
			}
			conn.Close()
			retired := false
			deadline = time.Now().Add(10 * time.Second)
			for n := 0; time.Now().Before(deadline) && !retired; n++ {
				marker, done := fmt.Sprintf("BADHANDRETIRED%d_%dX", i, n), fmt.Sprintf("BADHANDRETIREEND%d_%dX", i, n)
				sendCommand(t, pw, "execute unless entity @a[name="+name+"] run say "+marker)
				sendCommand(t, pw, "say "+done)
				retired = structureSaidBB(collect(audit, done), marker)
			}
			if !retired {
				t.Fatalf("%s %s never retired", addr, name)
			}
			restored, rr, rw := joinPlayBlackbox(t, addr, name)
			awaitPlayReadyBlackbox(t, addr, restored, rr, rw)
			check := newChatSinkTP(rr, rw)
			marker, done := fmt.Sprintf("BADHANDUNCHANGED%dX", i), fmt.Sprintf("BADHANDCHECKEND%dX", i)
			sendCommand(t, rw, condition+" run say "+marker)
			sendCommand(t, rw, "say "+done)
			got.unchanged = structureSaidBB(collect(check, done), marker)
			restored.Close()
			t.Logf("%s %s hand%d packet%#x %+v", addr, name, hand, packet, got)
			out = append(out, got)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i := range vanilla {
		packet := "use_item_on"
		if i >= 2 {
			packet = "use_item"
		}
		reason := "Internal Exception: io.netty.handler.codec.DecoderException: Failed to decode packet 'serverbound/minecraft:" + packet + "'"
		want := result{disconnect: true, closed: true, unchanged: true, reason: fmt.Sprintf("%#v", map[string]any{"translate": "disconnect.genericReason", "with": nbt.List{Elem: nbt.TagString, Items: []any{reason}}})}
		if vanilla[i] != want || mcgo[i] != vanilla[i] {
			t.Errorf("case%d vanilla%+v mcgo%+v want%+v", i, vanilla[i], mcgo[i], want)
		}
	}
}
