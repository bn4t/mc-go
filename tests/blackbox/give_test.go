package blackbox

// TestGiveOverflowDropsParity fills a player's inventory and /gives
// one more item: vanilla drops the remainder at the player's feet as
// an item entity (no pickup delay, Owner=recipient) instead of
// deleting it. The check is the add_entity spawn packet for the item.

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestGiveOverflowDropsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("GiveP"),
	}, nil)
	defer targets.cleanup()

	run := func(addr string) bool {
		c, pr, pw := joinPlayBlackbox(t, addr, "GiveP")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "clear @s")
		advCollect(t, c, pr, pw, 600*time.Millisecond, &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}})

		// 36 storage slots * 64 = 2304 fills the whole main inventory.
		sendCommand(t, pw, "give @s minecraft:stone 2304")
		advCollect(t, c, pr, pw, 900*time.Millisecond, &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}})
		sendCommand(t, pw, "give @s minecraft:diamond 5")

		itemType, _ := server.EntityTypeID("item")
		seen := false
		end := time.Now().Add(8 * time.Second)
		for time.Now().Before(end) && !seen {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySpawnEntity:
				r := protocol.NewPacketReader(p.Data)
				if _, e1 := r.VarInt(); e1 != nil {
					continue
				}
				if _, e2 := r.Read(16); e2 != nil {
					continue
				}
				if typ, e3 := r.VarInt(); e3 == nil && typ == itemType {
					seen = true
				}
			}
		}
		return seen
	}

	if !run(targets.vanilla) {
		t.Fatalf("vanilla: no overflow item entity")
	}
	if !run(targets.mcgo) {
		t.Fatalf("mc-go: no overflow item entity")
	}
}
