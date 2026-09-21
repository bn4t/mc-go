package blackbox

// TestBlockEntityPiggybackParity pins vanilla's ChunkHolder
// broadcastChanges ordering: block_entity_data rides the same
// tick-end flush as the block update and lands right after it, not at
// write time. Only block entities whose getUpdatePacket is non-null
// emit — signs do, empty chests and furnaces do not.
//
// The script setblocks a chest, an oak sign and a furnace. Both
// servers must produce: one block_update per setblock, one
// block_entity_data for the sign alone, and a byte-identical sign
// payload after the block update.

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestBlockEntityPiggybackParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, nil)
	defer targets.cleanup()

	type ev struct {
		id   byte
		body []byte
	}
	run := func(addr string) []ev {
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)

		var events []ev
		collect := func(d time.Duration) {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
				p, err := pr.ReadPacket()
				if err != nil {
					return
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					var kb protocol.Buffer
					kb.Raw(p.Data)
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					if id, err := r.VarInt(); err == nil {
						var b protocol.Buffer
						b.VarInt(id)
						_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					}
				case server.IDPlayChunkBatchStart, server.IDPlayChunkData:
					_ = ackChunkBatch(pw)
				case 0x06, 0x08, 0x54: // block_entity_data, block_update, section_blocks_update
					events = append(events, ev{byte(p.ID), p.Data})
				}
			}
		}

		sendCommand(t, pw, "tp @s 8.5 -59 15.0 180 0")
		collect(800 * time.Millisecond)
		for _, cmd := range []string{
			"setblock 8 -59 10 minecraft:chest",
			"setblock 9 -59 10 minecraft:oak_sign",
			"setblock 10 -59 10 minecraft:furnace",
		} {
			sendCommand(t, pw, cmd)
			collect(700 * time.Millisecond)
		}
		return events
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	ids := func(evs []ev) string {
		var out string
		for _, e := range evs {
			out += fmt.Sprintf("%02x ", e.id)
		}
		return out
	}
	if len(want) != len(got) {
		t.Fatalf("packet count: vanilla=[%s] mc-go=[%s]", ids(want), ids(got))
	}
	for i := range want {
		if want[i].id != got[i].id {
			t.Fatalf("packet %d id: vanilla=%02x mc-go=%02x\nvanilla=[%s]\nmc-go=[%s]",
				i, want[i].id, got[i].id, ids(want), ids(got))
		}
		if !bytes.Equal(want[i].body, got[i].body) {
			t.Fatalf("packet %d (%02x) body differs:\nvanilla %x\nmc-go    %x",
				i, want[i].id, want[i].body, got[i].body)
		}
	}
	// Assert the pin actually exercised the ordering: a sign's
	// block_update must precede its block_entity_data.
	var sawSign bool
	for i := 1; i < len(want); i++ {
		if want[i-1].id == 0x08 && want[i].id == 0x06 {
			sawSign = true
		}
	}
	if !sawSign {
		t.Fatalf("vanilla emitted no update+entity pair; pin did not run: [%s]", ids(want))
	}
}
