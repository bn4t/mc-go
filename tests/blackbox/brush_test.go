package blackbox

// TestBrushArmadilloParity holds a brush and right-clicks a summoned
// armadillo: adventure/brush_armadillo's criterion is
// player_interacted_with_entity with entity predicate
// entity_type=armadillo and item brush — "Isn't It Scute?" announces.
// The interaction packet idles through the same entity-id capture the
// bred/tame tests use, filtered to entity type 4.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestBrushArmadilloParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Brush"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	drainInto := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, f *advFlush, d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			}
		}
	}

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Brush")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		drainInto(c, pr, pw, f, 1200*time.Millisecond)
		sendCommand(t, pw, "tp @s 30 -60 28 0 0")
		sendCommand(t, pw, "fill 29 -60 29 32 -59 32 minecraft:stone")
		sendCommand(t, pw, "fill 30 -60 30 31 -59 30 minecraft:air")
		sendCommand(t, pw, "summon minecraft:armadillo 30.5 -60 30.5")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:brush")
		// Entity type 4 = minecraft:armadillo; natural spawns and the
		// pen walls' gravity are filtered by type.
		var dillID int32
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && dillID == 0 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == server.IDPlaySpawnEntity {
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 4 {
					dillID = id
				}
			}
			switch p.ID {
			case server.IDPlayUpdateAdvance:
				decodeUpdateAdv(t, p.Data, f)
			case server.IDPlaySystemChatCB:
				f.chats = append(f.chats, chatKey(p.Data))
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlaySyncPos:
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			}
		}
		if dillID == 0 {
			t.Fatalf("no armadillo spawn packet")
		}
		// Step close so the entity is inside interaction range on both
		// servers (vanilla's isWithinEntityInteractionRange ~3.5).
		_ = sendMovePosRotPitch(pw, 30.5, -60, 29.0, 0, 45)
		var ib protocol.Buffer
		ib.VarInt(dillID)
		ib.VarInt(0) // interact
		ib.VarInt(0) // main hand
		ib.Bool(false)
		_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
		drainInto(c, pr, pw, f, 3*time.Second)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:adventure/brush_armadillo"]["brush_armadillo"] {
		t.Fatalf("vanilla: brush_armadillo not obtained: %v", want.prog["minecraft:adventure/brush_armadillo"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("brush_armadillo parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
