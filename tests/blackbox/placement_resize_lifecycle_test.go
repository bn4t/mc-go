package blackbox

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Attribute writes and one use share a guarded batch. Player/world counters
// delimit its command window; an attempted in-window block/count receipt is
// diagnostic because command execution can overtake use dispatch. These
// counters do not timestamp denied action completion: native
// ACK flush precedes a player tick, and a separate raw-scale receipt permits
// that tick. Settled controls wait for actual player ticks.
func TestPlacementResizeLifecycleParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2"}, map[string]string{"ops.json": opsJSONFor("ResizePlace")}, nil)
	defer targets.cleanup()
	type result struct{ commandWindow, block, count bool }
	run := func(addr string) []result {
		conn, pr, pw := joinPlayBlackbox(t, addr, "ResizePlace")
		defer conn.Close()
		awaitPlayReadyBlackbox(t, addr, conn, pr, pw)
		var writes sync.Mutex
		sink := newChatSinkWriteGuard(pr, pw, true, &writes)
		collect := func(marker string) []string {
			var lines []string
			deadline := time.Now().Add(15 * time.Second)
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
		ack := func(seq int32, lines []string) {
			deadline := time.Now().Add(8 * time.Second)
			prefix := fmt.Sprintf("%#x:", server.IDPlayBlockChangedAck)
			for time.Now().Before(deadline) {
				for _, line := range lines {
					if strings.HasPrefix(line, prefix) {
						got, err := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix))).VarInt()
						if err == nil && got == seq {
							return
						}
					}
				}
				lines = sink.take()
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing resize use ack%d", addr, seq)
		}
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "forceload add -16 -16 0 0")
		for _, objective := range []string{"ResizeBefore", "ResizeAfter", "ResizeTimeB", "ResizeTimeA", "ResizeBase"} {
			sendCommand(t, pw, "scoreboard objectives add "+objective+" dummy")
		}
		setupPlayerTickBarrierBB(t, pw)
		sendCommand(t, pw, "say RESIZEINIT")
		collect("RESIZEINIT")
		loaded := false
		deadline := time.Now().Add(15 * time.Second)
		for n := 0; !loaded && time.Now().Before(deadline); n++ {
			marker, end := fmt.Sprintf("RESIZELOADED%dX", n), fmt.Sprintf("RESIZELOADEND%dX", n)
			sendCommand(t, pw, "execute if loaded -16 159 -16 if loaded -16 159 0 if loaded 0 159 -16 if loaded 0 159 0 run say "+marker)
			sendCommand(t, pw, "say "+end)
			loaded = structureSaidBB(collect(end), marker)
		}
		if !loaded {
			t.Fatalf("%s resize pad chunks missing", addr)
		}
		var out []result
		for i, pair := range [][2]string{{"1", "0.5"}, {"0.5", "1"}} {
			sendCommand(t, pw, "tick rate 20")
			sendCommand(t, pw, "tp @s 8 160 8")
			sendCommand(t, pw, "fill -2 159 -2 3 165 2 air")
			sendCommand(t, pw, "fill -2 159 -2 3 159 2 stone")
			sendCommand(t, pw, "attribute @s minecraft:scale base set "+pair[0])
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with stone 3")
			sendCommand(t, pw, "gamemode survival @s")
			sendCommand(t, pw, "tp @s 0.75 160 0.5")
			marker, end := fmt.Sprintf("RESIZESETUP%dX", i), fmt.Sprintf("RESIZESETUPEND%dX", i)
			sendCommand(t, pw, `execute if data entity @s {Pos:[0.75d,160.0d,0.5d],Inventory:[{Slot:0b,id:"minecraft:stone",count:3}]} run say `+marker)
			sendCommand(t, pw, "say "+end)
			if !structureSaidBB(collect(end), marker) {
				t.Fatalf("%s pair%d setup invalid", addr, i)
			}
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("RESIZEBASE%d", i), 2)
			sendCommand(t, pw, "tick rate 1")
			rateMarker := fmt.Sprintf("RESIZERATE%dX", i)
			sendCommand(t, pw, "say "+rateMarker)
			collect(rateMarker)
			var wire bytes.Buffer
			batch := protocol.NewWriter(&wire)
			batch.SetThreshold(server.CompressionThreshold)
			sendCommand(t, batch, "tick unfreeze")
			sendCommand(t, batch, "scoreboard players operation @s ResizeBefore = @s BBPlayerTick")
			sendCommand(t, batch, "execute store result score @s ResizeTimeB run time query gametime")
			sendCommand(t, batch, "attribute @s minecraft:scale base set "+pair[1])
			raw := fmt.Sprintf("RESIZERAW%dX", i)
			baseWant := 500
			if i == 1 {
				baseWant = 1000
			}
			sendCommand(t, batch, "execute store result score @s ResizeBase run attribute @s minecraft:scale base get 1000")
			sendCommand(t, batch, fmt.Sprintf("execute if score @s ResizeBase matches %d run say %s", baseWant, raw))
			seq := int32(4100 + i)
			sendUseItemOnCursor(t, batch, 1, 159, 0, 1, .5, 1, .5, seq)
			window := fmt.Sprintf("RESIZEAPPLIED%dX", i)
			sendCommand(t, batch, `execute if block 1 160 0 stone if data entity @s {Inventory:[{Slot:0b,id:"minecraft:stone",count:2}]} run say `+window)
			sendCommand(t, batch, "scoreboard players operation @s ResizeAfter = @s BBPlayerTick")
			sendCommand(t, batch, "execute store result score @s ResizeTimeA run time query gametime")
			frame, done := fmt.Sprintf("RESIZEFRAME%dX", i), fmt.Sprintf("RESIZEBATCHDONE%dX", i)
			sendCommand(t, batch, "execute if score @s ResizeBefore = @s ResizeAfter if score @s ResizeTimeB = @s ResizeTimeA run say "+frame)
			sendCommand(t, batch, "tick freeze")
			sendCommand(t, batch, "say "+done)
			writes.Lock()
			_, err := conn.Write(wire.Bytes())
			writes.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			lines := collect(done)
			commandWindow := structureSaidBB(lines, frame) && structureSaidBB(lines, raw)
			if i == 1 {
				t.Logf("%s accepted action visible within command window: %v", addr, structureSaidBB(lines, window))
			}
			ack(seq, lines)
			inspect := func(name string, accepted bool, commandWindow bool) {
				block := fmt.Sprintf("RESIZEBLOCK%s%dX", name, i)
				count := fmt.Sprintf("RESIZECOUNT%s%dX", name, i)
				end := fmt.Sprintf("RESIZECHECKEND%s%dX", name, i)
				expectedBlock, expectedCount := "air", 3
				if accepted {
					expectedBlock, expectedCount = "stone", 2
				}
				sendCommand(t, pw, "execute if block 1 160 0 "+expectedBlock+" run say "+block)
				sendCommand(t, pw, fmt.Sprintf(`execute if data entity @s {Inventory:[{Slot:0b,id:"minecraft:stone",count:%d}]} run say %s`, expectedCount, count))
				sendCommand(t, pw, "say "+end)
				lines := collect(end)
				got := result{commandWindow: commandWindow, block: structureSaidBB(lines, block), count: structureSaidBB(lines, count)}
				t.Logf("%s pair%d %s %s→%s %+v", addr, i, name, pair[0], pair[1], got)
				out = append(out, got)
			}
			inspect("immediate", i == 1, commandWindow)
			sendCommand(t, pw, "tick rate 20")
			sendCommand(t, pw, "setblock 1 160 0 air")
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with stone 3")
			sendCommand(t, pw, "tp @s 0.75 160 0.5")
			resetMarker := fmt.Sprintf("RESIZERESET%dX", i)
			sendCommand(t, pw, "say "+resetMarker)
			collect(resetMarker)
			waitPlayerTicksBB(t, pw, collect, fmt.Sprintf("RESIZEPOST%d", i), 2)
			seq += 20
			sendUseItemOnCursor(t, pw, 1, 159, 0, 1, .5, 1, .5, seq)
			ack(seq, nil)
			inspect("settled", i == 0, true)
		}
		return out
	}
	vanilla, mcgo := run(targets.vanilla), run(targets.mcgo)
	for i := range vanilla {
		want := result{true, true, true}
		if vanilla[i] != want || mcgo[i] != vanilla[i] {
			t.Errorf("phase%d vanilla%+v mcgo%+v want%+v", i, vanilla[i], mcgo[i], want)
		}
	}
}
