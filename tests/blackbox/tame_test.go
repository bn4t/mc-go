package blackbox

// TestTameAnimalParity feeds a summoned wolf bones until it tames:
// husbandry/tame_an_animal's bare "tamed_animal" criterion fires and the
// task advancement announces in chat. Wolf taming rolls 1/3 per bone,
// so the test feeds until the criterion lands or the rounds run out —
// a bone on an already-tamed wolf is a no-op, so overshoot is safe.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestTameAnimalParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("TameW"),
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
			if p.ID == server.IDPlayUpdateAdvance {
				decodeUpdateAdv(t, p.Data, f)
			}
			if p.ID == server.IDPlaySystemChatCB {
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if p.ID == server.IDPlayKeepAliveCB {
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			}
			if p.ID == server.IDPlaySyncPos {
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			}
			if p.ID == server.IDPlayChunkBatchDone {
				_ = ackChunkBatch(pw)
			}
		}
	}

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "TameW")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		drainInto(c, pr, pw, f, 1200*time.Millisecond)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 30 -60 28 0 0")
		sendCommand(t, pw, "fill 29 -60 29 32 -59 32 minecraft:stone")
		sendCommand(t, pw, "fill 30 -60 30 31 -59 30 minecraft:air")
		sendCommand(t, pw, "summon minecraft:wolf 30.5 -60 30.5")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:bone 32")
		// The wolf's spawn packet carries its entity id — entity type
		// 149 = minecraft:wolf.
		var wolfID int32
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && wolfID == 0 {
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
				if e1 == nil && e2 == nil && e3 == nil && typ == 149 {
					wolfID = id
				}
			}
			if p.ID == server.IDPlayUpdateAdvance {
				decodeUpdateAdv(t, p.Data, f)
			}
			if p.ID == server.IDPlaySystemChatCB {
				f.chats = append(f.chats, chatKey(p.Data))
			}
			if p.ID == server.IDPlayKeepAliveCB {
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			}
			if p.ID == server.IDPlaySyncPos {
				if id, err := protocol.NewPacketReader(p.Data).VarInt(); err == nil {
					var tb protocol.Buffer
					tb.VarInt(id)
					_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, tb.Bytes())
				}
			}
			if p.ID == server.IDPlayChunkBatchDone {
				_ = ackChunkBatch(pw)
			}
		}
		if wolfID == 0 {
			t.Fatalf("no wolf spawn packet")
		}
		for round := 0; round < 24 && !f.prog["minecraft:husbandry/tame_an_animal"]["tamed_animal"]; round++ {
			var ib protocol.Buffer
			ib.VarInt(wolfID)
			ib.VarInt(0) // interact
			ib.VarInt(0) // main hand
			ib.Bool(false)
			_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
			drainInto(c, pr, pw, f, 350*time.Millisecond)
		}
		drainInto(c, pr, pw, f, 3500*time.Millisecond)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:husbandry/tame_an_animal"]["tamed_animal"] {
		t.Fatalf("vanilla: tame_an_animal not granted: %v", want.prog["minecraft:husbandry/tame_an_animal"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("tame_animal parity: %d diffs\n%v", len(diffs), diffs)
	}
}
