package blackbox

// TestBredAnimalsParity feeds two summoned cows wheat: husbandry/
// breed_an_animal's "bred" (bare trigger) and bred_all_animals'
// minecraft:cow (entity_type predicate) both fire; vanilla announces
// the task advancement in chat. Feeding repeats over a few rounds —
// the first interact occasionally lands before vanilla's
// hasClientLoaded gate lifts, and a retried feed on an in-love cow is
// a no-op.

import (
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

func TestBredAnimalsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Breed"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	// drainInto collects advancement/chat traffic while answering the
	// housekeeping packets both servers expect (keepalive echo,
	// teleport confirm).
	drainInto := func(c net.Conn, pr *protocol.Reader, pw *protocol.Writer, f *advFlush, eids map[int32]bool, d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if eids != nil && p.ID == server.IDPlaySpawnEntity {
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16) // uuid
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 30 { // minecraft:cow — the flat world naturally spawns other animals
					eids[id] = true
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
	}

	run := func(addr string) *advFlush {
		f := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		c, pr, pw := joinPlayBlackbox(t, addr, "Breed")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		drainInto(c, pr, pw, f, nil, 1200*time.Millisecond)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 30 -60 28 0 0")
		// A two-cell pen keeps the pair in interact range — wandering
		// cows otherwise drift past vanilla's ~3-block use distance.
		sendCommand(t, pw, "fill 29 -60 29 32 -59 32 minecraft:stone")
		sendCommand(t, pw, "fill 30 -60 30 31 -59 30 minecraft:air")
		sendCommand(t, pw, "summon minecraft:cow 30.5 -60 30.4")
		sendCommand(t, pw, "summon minecraft:cow 31.5 -60 30.6")
		// The summon answers carry the cows' entity ids in
		// entity_spawn packets — capture both. Vanilla's entity
		// tracking can lag the teleport's chunk stream by several
		// seconds, so collection also continues inside later drains.
		eids := map[int32]bool{}
		end := time.Now().Add(10 * time.Second)
		for time.Now().Before(end) && len(eids) < 2 {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if p.ID == server.IDPlaySpawnEntity {
				r := protocol.NewPacketReader(p.Data)
				id, e1 := r.VarInt()
				_, e2 := r.Read(16) // uuid
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 30 { // minecraft:cow — the flat world naturally spawns other animals
					eids[id] = true
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
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:wheat 16")
		drainInto(c, pr, pw, f, eids, 600*time.Millisecond)
		// Feed both cows, retrying — a re-feed on an already-loving
		// cow is a no-op, and keeps collecting entity ids.
		for round := 0; round < 4 && !f.prog["minecraft:husbandry/breed_an_animal"]["bred"]; round++ {
			for id := range eids {
				var ib protocol.Buffer
				ib.VarInt(id)
				ib.VarInt(0) // interact
				ib.VarInt(0) // main hand
				ib.Bool(false)
				_ = pw.WritePacket(server.IDPlayInteractSB, ib.Bytes())
			}
			drainInto(c, pr, pw, f, eids, 900*time.Millisecond)
		}
		drainInto(c, pr, pw, f, eids, 4500*time.Millisecond)
		return f
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	if !want.prog["minecraft:husbandry/breed_an_animal"]["bred"] {
		t.Fatalf("vanilla: breed_an_animal not granted: %v", want.prog["minecraft:husbandry/breed_an_animal"])
	}
	if diffs := diffAdvFlush(want, got); len(diffs) > 0 {
		t.Fatalf("bred_animals parity: %d diffs\n%v", len(diffs), diffs)
	}
}
