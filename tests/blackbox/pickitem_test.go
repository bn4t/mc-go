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

// Oracle: official 26.2 SHA-1 823e2250d24b3ddac457a60c92a6a941943fcd6a,
// wire observation and Temurin 25.0.5 javap facts from
// ServerGamePacketListenerImpl.tryPickItem and Inventory pick helpers.
func TestPickBlockInventoryParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()
	run := func(addr string) []float64 {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		command := func(cmd string) {
			sendCommand(t, pw, cmd)
			serial++
			marker := fmt.Sprintf("PICK_BARRIER_%d", serial)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						return
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("command timeout: %s", cmd)
		}
		command("tick freeze")
		command("tp @s 0.5 70 0.5")
		command("setblock 1 70 0 stone")
		command("clear @s")
		command("gamemode creative")
		pick := func() {
			var b protocol.Buffer
			b.Position(1, 70, 0)
			b.Bool(false)
			_ = pw.WritePacket(server.IDPlayPickItemFromBlockSB, b.Bytes())
			command("say PICK_DONE")
		}
		get := func(path string) float64 {
			serial++
			marker := fmt.Sprintf("PICK_QUERY_%d", serial)
			sendCommand(t, pw, "data get entity @s "+path)
			sendCommand(t, pw, "say "+marker)
			deadline := time.Now().Add(10 * time.Second)
			reply := ""
			for time.Now().Before(deadline) {
				ended := false
				for _, line := range sink.take() {
					if strings.Contains(line, "data.entity.query") {
						reply = line
					}
					if strings.Contains(line, marker) {
						ended = true
					}
				}
				if ended {
					if i := strings.Index(reply, "data.entity.query"); i >= 0 {
						return lastNumber(reply[:i])
					}
					t.Fatalf("no query reply for %s", path)
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("query timeout %s", path)
			return -1
		}

		pick()
		out := []float64{get("SelectedItem.count"), get("SelectedItemSlot")}
		// The first main-inventory slot was skipped by the old offset search.
		command("clear @s")
		command("item replace entity @s inventory.0 with stone 17")
		command("item replace entity @s hotbar.0 with dirt 3")
		command("gamemode survival")
		pick()
		out = append(out, get("SelectedItem.count"), get("SelectedItemSlot"))
		// A matching stack with a name must not satisfy a plain pick result.
		command("clear @s")
		command("gamemode creative")
		command(`item replace entity @s hotbar.0 with stone[custom_name='"Named"'] 9`)
		pick()
		out = append(out, get("SelectedItem.count"), get("SelectedItemSlot"))
		return out
	}
	want := run(targets.vanilla)
	t.Logf("vanilla %v", want)
	got := run(targets.mcgo)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("vanilla %v; mc-go %v", want, got)
	}
	if !reflect.DeepEqual(want, []float64{1, 0, 17, 1, 1, 1}) {
		t.Fatalf("unexpected measured picks: %v", want)
	}
}
